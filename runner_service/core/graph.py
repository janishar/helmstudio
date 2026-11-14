"""A resolved workflow graph (cmd.run.execute data.workflow.nodes): node id → {kind, params, in}, where `in` wires
an input port to "<node id>.<output>". `validate` checks it against the node schemas this runner publishes;
`run` executes it in dependency order. A node implementation is
`async def node(ctx, params, inputs) -> {output name: value}`."""

import graphlib
from collections.abc import Awaitable, Callable


class RunError(Exception):
    """The run failed; the message is the user-readable reason sent in the failed status."""


class Cancelled(Exception):
    """The run was cancelled (KV cancels)."""


Node = Callable[["Ctx", dict, dict], Awaitable[dict]]


class Ctx:
    """What a node sees of its run: `progress` and `output` report, `check` is a cancel point
    (raises Cancelled, or RunError when the runner is shutting down). `node` is the running node."""

    def __init__(
        self,
        progress: Callable[[str, int, int], Awaitable],
        output: Callable[..., Awaitable],
        check: Callable[[], None],
    ) -> None:
        self._progress, self._output, self.check = progress, output, check
        self.node = ""
        self.outputs = 0  # outputs stored so far: the next one's index

    async def progress(self, step: int, of: int) -> None:
        """Step `step` of `of` of the running node is done."""
        await self._progress(self.node, step, of)

    async def output(self, data: bytes, *, ext: str, kind: str, width: int, height: int) -> None:
        """Store `data` as the run's next output (<output_prefix><index>.<ext>) and report it."""
        index, self.outputs = self.outputs, self.outputs + 1
        await self._output(index, data, ext=ext, kind=kind, width=width, height=height)


def _check_param(spec: dict, value) -> str | None:
    """Why `value` does not fit the param schema `spec`, or None."""
    t = spec["type"]
    if t == "bool":
        return None if isinstance(value, bool) else "must be a bool"
    if t == "enum":
        return None if value in spec["enum"] else f"must be one of {spec['enum']}"
    if t in ("string", "text"):
        if not isinstance(value, str):
            return "must be a string"
        size = len(value)
    else:
        number = int if t == "int" else (int, float)
        if isinstance(value, bool) or not isinstance(value, number):
            return f"must be an {t}" if t == "int" else "must be a number"
        size = value
    if "min" in spec and size < spec["min"]:
        return f"is below the minimum {spec['min']}"
    if "max" in spec and size > spec["max"]:
        return f"is above the maximum {spec['max']}"
    return None


def validate(nodes: dict, schemas: dict[str, dict]) -> list[str]:
    """Errors of a resolved graph against the node schemas (kind → registry nodeSchema): known
    kinds, every declared param present and in range and no other, every input wired to an
    existing output of the same type, no cycle. Empty when it can run."""
    errs = []
    for nid, node in nodes.items():
        schema = schemas.get(node["kind"])
        if schema is None:
            errs.append(f"{nid}: unknown node kind {node['kind']!r}")
            continue
        params, specs = node.get("params", {}), {p["name"]: p for p in schema["params"]}
        errs += [f"{nid}.{name}: unknown param" for name in params.keys() - specs.keys()]
        for name, spec in specs.items():
            if name not in params:
                errs.append(f"{nid}.{name}: missing")
            elif why := _check_param(spec, params[name]):
                errs.append(f"{nid}.{name} {why}")
        wires, inputs = node.get("in", {}), {p["name"]: p["type"] for p in schema["inputs"]}
        errs += [f"{nid}.{port}: unknown input" for port in wires.keys() - inputs.keys()]
        for port, want in inputs.items():
            if port not in wires:
                errs.append(f"{nid}.{port}: not wired")
                continue
            src, _, out = wires[port].partition(".")
            src_schema = schemas.get(nodes.get(src, {}).get("kind"))
            if src_schema is None:
                errs.append(f"{nid}.{port}: wired to a missing node {src!r}")
                continue
            got = {p["name"]: p["type"] for p in src_schema["outputs"]}.get(out)
            if got != want:
                errs.append(f"{nid}.{port}: wants {want}, {wires[port]} is {got or 'missing'}")
    if not errs:
        try:
            order(nodes)
        except graphlib.CycleError as e:
            errs.append(f"the graph has a cycle: {' → '.join(e.args[1])}")
    return errs


def order(nodes: dict) -> list[str]:
    """Node ids, each after every node it is wired to. graphlib.CycleError on a cycle."""
    deps = {
        nid: {ref.partition(".")[0] for ref in n.get("in", {}).values()} for nid, n in nodes.items()
    }
    return list(graphlib.TopologicalSorter(deps).static_order())


async def run(nodes: dict, impls: dict[str, Node], ctx: Ctx) -> None:
    """Run every node of a validated graph in dependency order, with a cancel point before each."""
    values: dict[str, object] = {}  # "<node id>.<output>" → value
    for nid in order(nodes):
        node = nodes[nid]
        ctx.node = nid
        ctx.check()
        inputs = {port: values[ref] for port, ref in node.get("in", {}).items()}
        outs = await impls[node["kind"]](ctx, node.get("params", {}), inputs)
        values |= {f"{nid}.{name}": v for name, v in outs.items()}
