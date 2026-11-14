"""A resolved workflow graph (cmd.run.execute data.workflow.nodes): node id → {kind, params, in}, where `in` wires
an input port to "<node id>.<output>".

- `validate` checks it against the node schemas this runner publishes (kinds, params, typed ports, no cycle);
- `resolve` picks the run's random seed (a seed param null or -1);
- `run` executes it pull-based: each sink (a node without outputs) pulls its inputs, and a node whose cache key
  matches the one kept for (owner, node id) is not run again. The key hashes the owner, the kind, the pack code,
  the params and the upstream keys; a Model input counts only the files of the components the stage uses, so a
  new seed re-runs sample and decode but not encode, and switching base → turbo does not re-encode.

Node functions run one at a time on the GPU thread (GPU), off the event loop, so heartbeats, cancel and
progress keep flowing; Ctx bridges their progress and output calls back to the loop."""

import asyncio
import graphlib
import hashlib
import json
import logging
import secrets
import time
from concurrent.futures import ThreadPoolExecutor

log = logging.getLogger("graph")

GPU = ThreadPoolExecutor(
    1, thread_name_prefix="gpu"
)  # every node and every load runs on this one thread
SEED_MAX = 2**53 - 1


class RunError(Exception):
    """The run failed; the message is the user-readable reason sent in the failed status."""


class Cancelled(Exception):
    """The run was cancelled (KV cancels)."""


class Cache(dict):
    """(owner, node id) → (key, outputs): the latest result of each node per owner."""


class Ctx:
    """What a node function sees of its run (it runs on the GPU thread):

    - `check()`: a cancel point (raises Cancelled, or RunError when the runner shuts down); also feeds the stall
      watchdog;
    - `progress(step, of, phase=None)`: step `step` of `of` of the running node is done;
    - `output(data, ext=, kind=, width=, height=)`: store the run's next output;
    - `component(model, name)`: the loaded component of a Model handle, through the residency manager;
    - `device`, `owner`, `seed` (the run's resolved seed or None), `node` (the running node id)."""

    def __init__(
        self, *, progress, output, check, owner="", residency=None, device="cpu", loop=None
    ):
        self._progress, self._output, self._check = progress, output, check
        self.owner, self.residency, self.device, self.loop = owner, residency, device, loop
        self.node, self.impl, self.seed = "", None, None
        self.outputs = 0  # outputs stored so far: the next one's index
        self.started = time.monotonic()  # when the running node started
        self.alive = (
            time.monotonic()
        )  # the last cancel point or progress: the stall watchdog reads it
        self._phase = (None, None, 0.0)  # (node, phase, started) for the ETA
        self._last = (None, 0.0)  # (node, time) of the last progress

    def _call(self, coro):
        """Run a coroutine on the event loop from the GPU thread and wait for it."""
        if self.loop is None:  # tests without a loop thread
            raise RuntimeError("Ctx has no loop")
        return asyncio.run_coroutine_threadsafe(coro, self.loop).result()

    def check(self) -> None:
        self.alive = time.monotonic()
        self._check()

    def progress(self, step: int, of: int, phase: str | None = None) -> None:
        now = time.monotonic()
        self.alive = now
        if self._phase[:2] != (
            self.node,
            phase,
        ):  # a new phase starts where the node's last one ended
            start = self._last[1] if self._last[0] == self.node else self.started
            self._phase = (self.node, phase, start)
        self._last = (self.node, now)
        # ponytail: the ETA of this node's phase only, from its own pace; history-based ETA is later
        elapsed = now - self._phase[2]
        eta = round(elapsed / step * (of - step), 1) if step and elapsed > 0 else None
        self._call(self._progress(self.node, step, of, phase, eta))

    def output(self, data: bytes, *, ext: str, kind: str, width: int, height: int) -> None:
        index, self.outputs = self.outputs, self.outputs + 1
        self._call(self._output(index, data, ext=ext, kind=kind, width=width, height=height))

    def component(self, model, name: str):
        """The component `name` of the Model handle `model`, loaded (to self.device) if it is not resident."""
        if name not in self.impl.uses:
            raise RunError(f"{self.node}: component {name!r} is not in its stage's uses")
        gb, transient = model.gb(name)
        return self.residency.get(model.key(name), gb, transient, lambda: model.load(name, self))


def _check_param(spec: dict, value) -> str | None:
    """Why `value` does not fit the param schema `spec`, or None (contracts/check.py value_error)."""
    t = spec["type"]
    if t == "seed" and value is None:
        return None
    if t == "bool":
        return None if isinstance(value, bool) else "must be a bool"
    if t == "enum":
        return None if value in spec["enum"] else f"must be one of {spec['enum']}"
    if t in ("string", "text"):
        if not isinstance(value, str):
            return "must be a string"
        size = len(value)
    else:
        number = (int, float) if t == "float" else int
        if isinstance(value, bool) or not isinstance(value, number):
            return "must be a number" if t == "float" else f"must be an {t}"
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


def resolve(nodes: dict, schemas: dict[str, dict]) -> tuple[dict, dict]:
    """(nodes with every random seed picked, the resolved values {"seed": n} when the graph has a seed)."""
    nodes, resolved = json.loads(json.dumps(nodes)), {}
    for node in nodes.values():
        for spec in schemas[node["kind"]]["params"]:
            if spec["type"] == "seed":
                params = node.setdefault("params", {})
                if params.get(spec["name"]) in (None, -1):
                    params[spec["name"]] = secrets.randbelow(SEED_MAX + 1)
                resolved["seed"] = params[spec["name"]]
    return nodes, resolved


def _model(nodes: dict, impls: dict, ref: str):
    """The Model handle wired at `ref`: model nodes are pure, so it is built from the node's params."""
    src = nodes[ref.partition(".")[0]]
    return impls[src["kind"]].fn(None, src.get("params", {}), {})["model"]


def keys(nodes: dict, impls: dict, schemas: dict, owner: str) -> dict[str, str]:
    """node id → cache key."""
    out = {}
    for nid in order(nodes):
        node, impl = nodes[nid], impls[nodes[nid]["kind"]]
        types = {p["name"]: p["type"] for p in schemas[node["kind"]]["inputs"]}
        ins = {}
        for port, ref in node.get("in", {}).items():
            if types[port] == "Model":
                ins[port] = _model(nodes, impls, ref).fingerprint(impl.uses)
            else:
                ins[port] = [out[ref.partition(".")[0]], ref]
        blob = [owner, node["kind"], impl.code, node.get("params", {}), ins]
        out[nid] = hashlib.sha256(json.dumps(blob, sort_keys=True).encode()).hexdigest()
    return out


def _canvas_mp(nodes: dict, schemas: dict, nid: str) -> float:
    """Megapixels of the Canvas nearest upstream of `nid` (breadth first), or 0 without one."""
    queue, seen = [nid], set()
    while queue:
        cur = queue.pop(0)
        if cur in seen:
            continue
        seen.add(cur)
        node = nodes[cur]
        if any(o["type"] == "Canvas" for o in schemas[node["kind"]]["outputs"]):
            p = node.get("params", {})
            return p["width"] * p["height"] / 1e6
        queue += [ref.partition(".")[0] for ref in node.get("in", {}).values()]
    return 0.0


def _components(nodes: dict, impls: dict, schemas: dict, nid: str) -> dict[tuple, object]:
    """residency key → Model handle, for the components the stage at `nid` uses."""
    node, impl = nodes[nid], impls[nodes[nid]["kind"]]
    types = {p["name"]: p["type"] for p in schemas[node["kind"]]["inputs"]}
    out = {}
    for port, ref in node.get("in", {}).items():
        if types[port] == "Model":
            model = _model(nodes, impls, ref)
            out |= {model.key(c): model for c in impl.uses}
    return out


async def run(nodes: dict, impls: dict, schemas: dict, ctx: Ctx, cache: Cache) -> None:
    """Run every sink of a validated, resolved graph, pulling only what is not cached."""
    loop = asyncio.get_running_loop()
    ctx.loop = loop
    k = keys(nodes, impls, schemas, ctx.owner)
    done: dict[str, dict] = {}
    pending = set(nodes)

    def later() -> set[tuple]:
        return {key for n in pending for key in _components(nodes, impls, schemas, n)}

    def job(nid: str, impl, params: dict, inputs: dict) -> dict:
        ctx.check()
        if ctx.residency is not None and (impl.uses or impl.peak is not None):
            comps = _components(nodes, impls, schemas, nid)
            needs = {key: m.gb(key[2])[0] for key, m in comps.items()}
            peak = 0.0
            if impl.peak is not None:
                from core.pack import evaluate

                peak = evaluate(impl.peak, params | {"mp": _canvas_mp(nodes, schemas, nid)})
            ctx.residency.admit(needs, peak, later() - set(needs))
        return impl.fn(ctx, params, inputs)

    async def get(nid: str) -> dict:
        if nid in done:
            return done[nid]
        node, impl = nodes[nid], impls[nodes[nid]["kind"]]
        sink = not schemas[node["kind"]]["outputs"]
        hit = cache.get((ctx.owner, nid))
        if not sink and hit and hit[0] == k[nid]:
            log.info("%s: cache hit", nid)
            done[nid] = hit[1]
            pending.discard(nid)
            return hit[1]
        inputs = {}
        for port, ref in node.get("in", {}).items():
            src, _, out = ref.partition(".")
            inputs[port] = (await get(src))[out]
        ctx.node, ctx.impl, ctx.started = nid, impl, time.monotonic()
        pending.discard(nid)
        outs = await loop.run_in_executor(GPU, job, nid, impl, node.get("params", {}), inputs)
        log.info("%s: ran in %.1f s", nid, time.monotonic() - ctx.started)
        if not sink:
            cache[(ctx.owner, nid)] = (k[nid], outs)
        done[nid] = outs
        return outs

    for nid in order(nodes):
        if not schemas[nodes[nid]["kind"]]["outputs"]:
            await get(nid)
