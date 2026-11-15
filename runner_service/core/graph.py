"""A resolved workflow graph (cmd.run.execute data.workflow.nodes): node id → {kind, params, in}, where `in` wires
an input port (or a wireable value param) to "<node id>.<output>".

- `problems` / `validate` check it against the node schemas this runner publishes with the graph rules of
  contracts/README.md (known kinds, params, typed wires with family-scoped handles, required inputs, no cycle, an
  output, node, size, output and seed caps), independently of studio;
- `resolve` picks the run's random seed (a seed param null or -1);
- `run` executes it pull-based: each sink (an `output` node) pulls its inputs, so a node no output needs never runs,
  and a node whose cache key is cached is not run again. The key hashes the owner, the kind, the pack code, the
  params and the upstream keys (never node ids); a Model input counts only the files of the components the stage
  uses, so a new seed re-runs sample and decode but not encode, and switching base → turbo does not re-encode.

Node functions run one at a time on the GPU thread (GPU), off the event loop, so heartbeats, cancel and
progress keep flowing; Ctx bridges their progress and output calls back to the loop."""

import asyncio
import dataclasses
import graphlib
import hashlib
import json
import logging
import re
import secrets
import time
from collections import OrderedDict
from concurrent.futures import ThreadPoolExecutor

from core import images

log = logging.getLogger("graph")

GPU = ThreadPoolExecutor(
    1, thread_name_prefix="gpu"
)  # every node and every load runs on this one thread
SEED_MAX = 2**53 - 1


class RunError(Exception):
    """The run failed; the message is the user-readable reason sent in the failed status."""


class Cancelled(Exception):
    """The run was cancelled (KV cancels)."""


def nbytes(value) -> int:
    """The bytes the arrays, byte strings and images in a node's outputs hold (dicts, lists, tuples, dataclasses
    walked); anything else counts 0."""
    if isinstance(n := getattr(value, "nbytes", None), int):  # numpy, MLX and torch arrays
        return n
    if isinstance(value, (bytes, bytearray)):
        return len(value)
    if isinstance(value, images.Image.Image):
        return value.width * value.height * len(value.getbands())
    if isinstance(value, dict):
        return sum(nbytes(v) for v in value.values())
    if isinstance(value, (list, tuple)):
        return sum(nbytes(v) for v in value)
    if dataclasses.is_dataclass(value) and not isinstance(value, type):
        return sum(nbytes(getattr(value, f.name)) for f in dataclasses.fields(value))
    return 0


class Cache(OrderedDict):
    """cache key (`keys`, which hashes the owner) → a node's outputs, least recently used first, at most SIZE
    entries and BYTES bytes (`nbytes`; the newest entry stays even when it alone is larger)."""

    SIZE = 24
    BYTES = 2 * 2**30

    def __init__(self, *args, **kwargs) -> None:
        self.sizes: dict = {}
        super().__init__(*args, **kwargs)

    def get(self, key, default=None):
        if key not in self:
            return default
        self.move_to_end(key)
        return self[key]

    def __setitem__(self, key, value) -> None:
        super().__setitem__(key, value)
        self.move_to_end(key)
        self.sizes[key] = nbytes(value)
        while len(self) > self.SIZE or (len(self) > 1 and sum(self.sizes.values()) > self.BYTES):
            self.sizes.pop(self.popitem(last=False)[0], None)

    def __delitem__(self, key) -> None:
        super().__delitem__(key)
        self.sizes.pop(key, None)

    def clear(self) -> None:
        super().clear()
        self.sizes.clear()


class Ctx:
    """What a node function sees of its run (it runs on the GPU thread):

    - `check()`: a cancel point (raises Cancelled, or RunError when the runner shuts down); also feeds the stall
      watchdog;
    - `progress(step, of, phase=None)`: step `step` of `of` of the running node is done;
    - `output(data, ext=, kind=, width=, height=[, video=, poster=])`: store the run's next output;
    - `component(model, name)`: the loaded component of a Model handle, through the residency manager;
    - `device`, `owner`, `seed` (the run's resolved seed or None), `node` (the running node id);
    - `fetch(key, limit, etag)`: the core's object-store read for Image params (core.images), set by the consumer;
    - `resolved`: `<node>.<param>` → the frames an "auto" length became (a video output's frame count)."""

    def __init__(
        self, *, progress, output, check, owner="", residency=None, device="cpu", loop=None
    ):
        self._progress, self._output, self._check = progress, output, check
        self.owner, self.residency, self.device, self.loop = owner, residency, device, loop
        self.node, self.impl, self.seed = "", None, None
        self.fetch = None  # async (key, limit) -> bytes | None
        self.loras = None  # the host's core.lora.Library, set by the consumer
        self.outputs = 0  # outputs stored so far: the next one's index
        self.auto: list[
            str
        ] = []  # `<node>.<param>` of the run's frames params set to "auto" (graph.run)
        self.resolved: dict[str, int] = {}  # what they resolved to: the stored clip's frame count
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

    def output(
        self, data: bytes, *, ext: str, kind: str, width: int, height: int, video=None, poster=None
    ) -> None:
        """A video also passes `video` ({duration_s, fps, frames, has_audio}) and `poster` (PNG bytes)."""
        index, self.outputs = self.outputs, self.outputs + 1
        extra = {"video": video, "poster": poster} if video is not None else {}
        if video is not None and self.auto:  # auto length: the clip's real frames (README, Video)
            self.resolved |= dict.fromkeys(self.auto, video["frames"])
            extra["resolved"] = {"params": dict(self.resolved)}
        self._call(
            self._output(index, data, ext=ext, kind=kind, width=width, height=height, **extra)
        )

    def component(self, model, name: str):
        """The component `name` of the Model handle `model`, loaded (to self.device) if it is not resident."""
        if name not in self.impl.uses:
            raise RunError(f"{self.node}: component {name!r} is not in its stage's uses")
        gb, transient = model.gb(name)
        key = model.key(name)
        module = self.residency.get(key, gb, transient, lambda: model.load(name, self), model.resident_loras(name))  # fmt: skip
        if model.baked(
            name
        ):  # counted at what it holds, not at the plain size (loaders_mlx: float32 scales)
            gb += model.extra_gb(name, module)
            self.residency.resize(key, gb)
        if (
            name == model.lora_component
        ):  # exactly this handle's adapters, at its strengths; no reload
            from core.pack import BYTES

            uses = [u for u, _ in model.adapters(name).values()]
            dtype = BYTES.get(model.pack.component(name).get("dtype", "bf16"), 2)  # stored: as bf16
            self.residency.grow(
                key, gb + model.lora_gb(uses, dtype)
            )  # room first: nothing is allocated yet
            self.residency.resize(key, gb + model.activate(name, module, self))
        return module

    def lora_path(self, use):
        """The local file of a LoRA of this run (fetched to the host's LoRA cache first for an own one); called
        on the GPU thread by the loader."""
        if self.loras is None:
            raise RunError(f"LoRA {use.name}: this runner has no LoRA library")
        self.alive = time.monotonic()

        async def tell(phase: str) -> None:  # awaited on the loop by the library
            await self._progress(self.node, 1, 1, phase, None)

        return self._call(self.loras.path(use, self.owner, tell))


def _check_param(spec: dict, value) -> str | None:
    """Why `value` does not fit the param schema `spec`, or None (contracts/check.py value_error)."""
    t = spec["type"]
    if t == "seed" and value is None:
        return None
    # image: null (a registry workflow) or resolved (the message schema checked its fields)
    if t == "image":
        return None if value is None or _image(value) else "must be an image"
    if t == "rows":
        return _check_rows(spec, value)
    if t == "bool":
        return None if isinstance(value, bool) else "must be a bool"
    if t == "frames":  # contracts/README.md, Video: Params (and auto length where declared)
        if value == "auto":
            return None if "auto" in spec else ": auto length is not offered"
        lo, step, hi = spec["min"], spec["step"], spec["max"]
        if (
            isinstance(value, bool)
            or not isinstance(value, int)
            or not lo <= value <= hi
            or (value - lo) % step
        ):
            return f": {value!r} is not on the frame grid {lo} + {step}k up to {hi}"
        return None
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
        if t == "int" and "values" in spec and value not in spec["values"]:
            return f": must be one of {', '.join(map(str, spec['values']))}"
    if "min" in spec and size < spec["min"]:
        return f"is below the minimum {spec['min']}"
    if "max" in spec and size > spec["max"]:
        return f"is above the maximum {spec['max']}"
    return None


def _image(value) -> bool:
    return isinstance(value, dict) and "key" in value


def _check_rows(spec: dict, value) -> str | None:
    if not isinstance(value, list):
        return "must be a list"
    if value == [] and "Image" in spec["item"].values():
        return (
            None  # a workflow holds no takes: [] even under min; `validate` needs min items at run
        )
    if not spec.get("min", 0) <= len(value) <= spec["max"]:
        return f"must have {spec.get('min', 0)} to {spec['max']} items"
    for i, row in enumerate(value, 1):
        if not isinstance(row, dict) or row.keys() - spec["item"].keys():
            return f"item {i}: fields must be among {sorted(spec['item'])}"
        for f, item in spec["item"].items():
            if item == "Image":
                if not _image(row.get(f)):
                    return f"item {i}.{f}: an image is required"
            elif item == "Lora":  # {lora_id} in a workflow, resolved (`defs lora`) in a command
                v = row.get(f)
                if not isinstance(v, dict) or not LORA_ID.match(str(v.get("lora_id"))):
                    return f"item {i}.{f}: a LoRA is required"
            elif f in row and (why := _check_param(item, row[f])):
                return f"item {i}.{f} {why}"
    return None


LORA_ID = re.compile(
    r"^([0-9a-f]{64}|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$"
)
HANDLES = {"Model", "Conditioning", "Latent"}  # family-scoped: Model[f] is not Model[g]
MEDIA = {"Image", "Video"}  # what an input of type Media takes
WIREABLE = {
    "int": "Int",
    "float": "Float",
    "seed": "Seed",
    "string": "Text",
    "text": "Text",
    "image": "Image",
}
MAX_NODES, MAX_BYTES, MAX_OUTPUTS = 64, 262144, 4


def family(kind: str) -> str | None:
    return kind.split("/")[0] if "/" in kind else None


def passthrough(schema: dict) -> list[str] | None:
    """The inputs a bypassed node passes through (per output, the first input of its type), or None when the kind
    cannot be bypassed."""
    through = []
    for o in schema["outputs"]:
        i = next((i["name"] for i in schema["inputs"] if i["type"] == o["type"]), None)
        if i is None:
            return None
        through.append(i)
    return list(dict.fromkeys(through)) or None


def lora_field(spec: dict) -> str | None:
    """A rows param's Lora field (the lora-stack's `lora`), else None."""
    if spec["type"] == "rows":
        return next((f for f, t in spec["item"].items() if t == "Lora"), None)
    return None


def port_type(schema: dict, name: str) -> str | None:
    """The type a wire into `name` carries: an input's type, or a wireable value param's port type."""
    t = next((i["type"] for i in schema["inputs"] if i["name"] == name), None)
    p = next((p for p in schema["params"] if p["name"] == name), None)
    return t or (WIREABLE.get(p["type"]) if p else None)


def fits(got: str | None, want: str) -> bool:
    """A wire of type `got` may feed a port of type `want` (contracts/README.md, Graph rules: fits)."""
    return got is not None and (got == want or (want == "Media" and got in MEDIA))


def _typed(t: str, kind: str) -> str:
    return f"{t}[{family(kind)}]" if t in HANDLES else t


def problems(
    nodes: dict, schemas: dict[str, dict], workflow_family: str | None = None
) -> list[tuple]:
    """The graph rules of contracts/README.md ("Graph rules") that a runner can check, as (code, node, port, param,
    message): the palette is this runner's node schemas (so a kind of another env is `unknown_kind`); `wrong_family`
    only when `workflow_family` is given; no studio block. Every param must be present (studio fills defaults):
    `param_missing`, a runner-only code. Bypass follows the rules, though studio splices it out (see `validate`)."""
    out = []

    def add(code, node=None, port=None, param=None, message=""):
        out.append((code, node, port, param, message))

    if len(nodes) > MAX_NODES:
        add("too_many_nodes", message=f"the graph has {len(nodes)} nodes, at most {MAX_NODES}")
    if len(json.dumps(nodes, separators=(",", ":"), ensure_ascii=False).encode()) > MAX_BYTES:
        add("too_large", message=f"the graph is larger than {MAX_BYTES} bytes")
    if out:
        return out  # nothing else is checked: the work stays bounded
    if (outputs := sum(n["kind"] == "output" for n in nodes.values())) > MAX_OUTPUTS:
        add("too_many_outputs", message=f"the graph has {outputs} outputs, at most {MAX_OUTPUTS}")
    seeds = [
        f"{nid}.{p['name']}"
        for nid, n in nodes.items()
        if n["kind"] in schemas
        for p in schemas[n["kind"]]["params"]
        if p["type"] == "seed" and p["name"] not in n.get("in", {})
    ]
    if len(seeds) > 1:
        add("too_many_seeds", message=f"the graph has {len(seeds)} unwired seeds, at most one")
    for nid, n in nodes.items():
        kind, wires = n["kind"], n.get("in", {})
        if workflow_family and family(kind) and family(kind) != workflow_family:
            add(
                "wrong_family", nid, message=f"{nid}: {kind} is not of the family {workflow_family}"
            )
        s = schemas.get(kind)
        if s is None:
            add("unknown_kind", nid, message=f"{nid}: unknown node kind {kind!r}")
            continue
        params, specs = n.get("params", {}), {p["name"]: p for p in s["params"]}
        for name in params.keys() - specs.keys():
            add("unknown_param", nid, param=name, message=f"{nid}.{name}: unknown param")
        for name, spec in specs.items():
            if name not in params:
                add("param_missing", nid, param=name, message=f"{nid}.{name}: missing")
            elif why := _check_param(spec, params[name]):
                sep = "" if why.startswith(":") else " "
                add("param_invalid", nid, param=name, message=f"{nid}.{name}{sep}{why}")
            elif (vb := spec.get("values_by")) and params[name] not in (
                ok := vb["values"].get(params.get(vb["param"]), spec["values"])
            ):  # README, Video: steps allowed for the node's role
                add("param_invalid", nid, param=name,
                    message=f"{nid}.{name}: must be one of {', '.join(map(str, ok))}")  # fmt: skip
            elif field := lora_field(spec):  # README, LoRAs: one LoRA once per stack
                ids = [r[field]["lora_id"] for r in params[name]]
                if dup := next((x for i, x in enumerate(ids) if x in ids[:i]), None):
                    add("lora_duplicate", nid, param=name,
                        message=f"{nid}.{name}: LoRA {dup} is already in row {ids.index(dup) + 1}")  # fmt: skip
        for port, ref in wires.items():
            want = port_type(s, port)
            if not want:
                add("unknown_input", nid, port, message=f"{nid}.{port}: unknown input")
                continue
            src_id, _, out_name = ref.partition(".")
            src = nodes.get(src_id)
            if src is None:
                add(
                    "unknown_source",
                    nid,
                    port,
                    message=f"{nid}.{port}: wired to a missing node {src_id!r}",
                )
                continue
            ss = schemas.get(src["kind"])
            if ss is None:
                continue  # an unknown kind's outputs are not checked
            got = next((o["type"] for o in ss["outputs"] if o["name"] == out_name), None)
            if not got:
                add(
                    "unknown_source",
                    nid,
                    port,
                    message=f"{nid}.{port}: {src_id} has no output {out_name!r}",
                )
            elif not fits(got, want) or (want in HANDLES and family(src["kind"]) != family(kind)):
                add("type_mismatch", nid, port,
                    message=f"{nid}.{port}: wants {_typed(want, kind)}, {ref} is {_typed(got, src['kind'])}")  # fmt: skip
        if n.get("bypass"):
            through = passthrough(s)
            if through is None:
                add("not_bypassable", nid, message=f"{nid}: {kind} cannot be bypassed")
            required = through or []
        else:
            required = [i["name"] for i in s["inputs"] if not i.get("optional")]
        for i in required:
            if i not in wires:
                add("required_input", nid, i, message=f"{nid}.{i}: not wired")
    succ = {nid: set() for nid in nodes}
    for nid, n in nodes.items():
        for ref in n.get("in", {}).values():
            if (src := ref.partition(".")[0]) in nodes:
                succ[src].add(nid)
    for nid in nodes:  # on a cycle = reachable from itself; ponytail: O(n·e), fine at 64 nodes
        seen, todo = set(), list(succ[nid])
        while todo:
            if (x := todo.pop()) not in seen:
                seen.add(x)
                todo += succ[x]
        if nid in seen:
            add("cycle", nid, message=f"{nid}: on a cycle")
    if not any(n["kind"] == "output" for n in nodes.values()):
        add("no_output", message="the graph has no output node")
    return out


def validate(nodes: dict, schemas: dict[str, dict], run: bool = True) -> list[str]:
    """Why this runner refuses a resolved graph (cmd.run.execute), each message naming its node; empty when it can
    run. Checked here, independently of studio: the graph rules (`problems`), every param present, no bypassed
    node (studio splices them out before it sends a graph) and, for a run, `min` items in a rows param of takes
    (`run=False`: a registry workflow, which holds no takes)."""
    errs = [
        f"{nid}: a bypassed node reached the runner" for nid, n in nodes.items() if n.get("bypass")
    ]
    for nid, n in nodes.items() if run else ():  # takes rows: [] in a workflow, min items in a run
        for spec in schemas.get(n["kind"], {}).get("params", []):
            rows = n.get("params", {}).get(spec["name"])
            if (
                spec["type"] == "rows"
                and rows == []
                and "Image" in spec["item"].values()
                and spec.get("min")
            ):
                errs.append(f"{nid}.{spec['name']}: at least {spec['min']} items")
    return errs + [p[4] for p in problems(nodes, schemas)]


def order(nodes: dict) -> list[str]:
    """Node ids, each after every node it is wired to. graphlib.CycleError on a cycle."""
    deps = {
        nid: {ref.partition(".")[0] for ref in n.get("in", {}).values()} for nid, n in nodes.items()
    }
    return list(graphlib.TopologicalSorter(deps).static_order())


def upstream(nodes: dict, ids) -> set[str]:
    """`ids` and every node they are wired from, transitively."""
    seen, todo = set(), list(ids)
    while todo:
        if (cur := todo.pop()) not in seen:
            seen.add(cur)
            todo += [ref.partition(".")[0] for ref in nodes[cur].get("in", {}).values()]
    return seen


def _wired_params(node: dict, schema: dict) -> set[str]:
    """The params of `node` wired from upstream (their `params` entries are ignored)."""
    return node.get("in", {}).keys() & {p["name"] for p in schema["params"]}


def resolve(nodes: dict, schemas: dict[str, dict]) -> tuple[dict, dict]:
    """(nodes with every random seed picked and each rows item's missing fields at their defaults, the resolved
    values {"seed": n} when the graph has a seed; the graph rules allow one unwired seed param, so it is the run's
    only seed)."""
    nodes, resolved = json.loads(json.dumps(nodes)), {}
    for node in nodes.values():
        wired = _wired_params(node, schemas[node["kind"]])
        for spec in schemas[node["kind"]]["params"]:
            if spec["type"] == "rows" and node.get("params", {}).get(spec["name"]):
                # item fields left out take their defaults (contracts/README.md, Video: keyframes)
                defaults = {f: s["default"] for f, s in spec["item"].items() if isinstance(s, dict) and "default" in s}  # fmt: skip
                node["params"][spec["name"]] = [defaults | r for r in node["params"][spec["name"]]]
            if spec["type"] == "seed" and spec["name"] not in wired:
                params = node.setdefault("params", {})
                if params.get(spec["name"]) in (None, -1):
                    params[spec["name"]] = secrets.randbelow(SEED_MAX + 1)
                resolved["seed"] = params[spec["name"]]
    return nodes, resolved


def _model(nodes: dict, impls: dict, schemas: dict, ref: str):
    """The Model handle wired at `ref`. Nodes that make or pass a Model (<family>/model, <family>/lora-stack) are
    pure functions of their params and Model inputs, so it is built through the graph without running anything."""
    src_id, _, out = ref.partition(".")
    src = nodes[src_id]
    types = {i["name"]: i["type"] for i in schemas[src["kind"]]["inputs"]}
    inputs = {
        p: _model(nodes, impls, schemas, r)
        for p, r in src.get("in", {}).items()
        if types.get(p) == "Model"
    }
    return impls[src["kind"]].fn(None, src.get("params", {}), inputs)[out]


def keys(nodes: dict, impls: dict, schemas: dict, owner: str) -> dict[str, str]:
    """node id → cache key, over what the node computes from, never its id or its sources' ids: the owner, kind,
    pack code, its own params (not those wired from upstream) and per wired key the source's key and output."""
    out = {}
    for nid in order(nodes):
        node, impl = nodes[nid], impls[nodes[nid]["kind"]]
        types = {p["name"]: p["type"] for p in schemas[node["kind"]]["inputs"]}
        ins = {}
        for port, ref in node.get("in", {}).items():
            if types.get(port) == "Model":
                ins[port] = _model(nodes, impls, schemas, ref).fingerprint(impl.uses)
            else:
                src, _, name = ref.partition(".")
                ins[port] = [out[src], name]
        wired = _wired_params(node, schemas[node["kind"]])
        params = {p: v for p, v in node.get("params", {}).items() if p not in wired}
        blob = [owner, node["kind"], impl.code, params, ins]
        out[nid] = hashlib.sha256(json.dumps(blob, sort_keys=True).encode()).hexdigest()
    return out


def _image_values(nodes: dict, schemas: dict, nid: str) -> list[dict]:
    """The Image values in the params of `nid` and of every node upstream of it, in graph order."""
    seen = upstream(nodes, [nid])
    return [
        v
        for n in order(nodes)
        if n in seen
        for v in images.values(schemas[nodes[n]["kind"]]["params"], nodes[n].get("params", {}))
    ]


def _images(nodes: dict, schemas: dict, nid: str) -> int:
    """How many Image values the params of `nid` and of every node upstream of it hold (peak_gb's `images`)."""
    return len(_image_values(nodes, schemas, nid))


def _canvas_mp(nodes: dict, schemas: dict, nid: str) -> float:
    """Megapixels of the Canvas nearest upstream of `nid` (breadth first), or 0 without one. Canvas `auto` with Image
    values upstream is sized from the last one (images.auto_size), as the stage sizes its output."""
    queue, seen = [nid], set()
    while queue:
        cur = queue.pop(0)
        if cur in seen:
            continue
        seen.add(cur)
        node = nodes[cur]
        if any(o["type"] == "Canvas" for o in schemas[node["kind"]]["outputs"]):
            p, refs = node.get("params", {}), _image_values(nodes, schemas, nid)
            if p.get("mode") == "auto" and refs:
                width, height = images.auto_size(refs[-1]["width"], refs[-1]["height"])
                return width * height / 1e6
            return p["width"] * p["height"] / 1e6
        queue += [ref.partition(".")[0] for ref in node.get("in", {}).values()]
    return 0.0


def auto_frames(seconds: float, fps: float, spec: dict) -> int:
    """Auto length (contracts/README.md, Video; check.py `auto_frames`): `seconds` as frames clamped to the param's
    auto min_s..max_s and its max, floored onto min + k·step, one step up when that fell below min_s (round is half
    to even, as the old studio)."""
    lo_, step, top = (
        spec["min"],
        spec["step"],
        spec["min"] + (spec["max"] - spec["min"]) // spec["step"] * spec["step"],
    )
    lo, hi = round(spec["auto"]["min_s"] * fps), min(round(spec["auto"]["max_s"] * fps), top)
    f = min(max(round(seconds * fps), lo), hi)
    f = lo_ + max(f - lo_, 0) // step * step
    return min(f + step if f < lo else f, top)


def _frames(nodes: dict, schemas: dict, nid: str) -> int:
    """The `frames` param of the node nearest upstream of `nid` (breadth first) that has one, or 0; "auto" counts
    as its upper bound (admission, like the client's estimate, plans for the longest clip)."""
    queue, seen = [nid], set()
    while queue:
        cur = queue.pop(0)
        if cur in seen:
            continue
        seen.add(cur)
        node = nodes[cur]
        for p in schemas[node["kind"]]["params"]:
            if p["type"] == "frames":
                v = node.get("params", {}).get(p["name"], 0)
                fps = node.get("params", {}).get(p["fps"], 24)
                return auto_frames(p["auto"]["max_s"], fps, p) if v == "auto" else v
        queue += [ref.partition(".")[0] for ref in node.get("in", {}).values()]
    return 0


def _components(nodes: dict, impls: dict, schemas: dict, nid: str) -> dict[tuple, object]:
    """residency key → Model handle, for the components the stage at `nid` uses."""
    node, impl = nodes[nid], impls[nodes[nid]["kind"]]
    types = {p["name"]: p["type"] for p in schemas[node["kind"]]["inputs"]}
    out = {}
    for port, ref in node.get("in", {}).items():
        if types.get(port) == "Model":
            model = _model(nodes, impls, schemas, ref)
            out |= {model.key(c): model for c in impl.uses}
    return out


async def run(nodes: dict, impls: dict, schemas: dict, ctx: Ctx, cache: Cache) -> None:
    """Run every sink (an `output` node) of a validated, resolved graph, pulling only what is not cached; a node no
    sink needs is not run. A node's result is reused when its cache key matches, whatever its id: within the run
    (two identical nodes run once) and across runs."""
    loop = asyncio.get_running_loop()
    ctx.loop = loop
    k = keys(nodes, impls, schemas, ctx.owner)
    ctx.auto = [f"{nid}.{p['name']}" for nid, n in nodes.items() for p in schemas[n["kind"]]["params"]
                if p["type"] == "frames" and n.get("params", {}).get(p["name"]) == "auto"]  # fmt: skip
    done: dict[str, dict] = {}
    sinks = [nid for nid in order(nodes) if not schemas[nodes[nid]["kind"]]["outputs"]]
    pending = upstream(nodes, sinks)

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

                mp, refs = _canvas_mp(nodes, schemas, nid), _image_values(nodes, schemas, nid)
                # an input that carries its clip's frames (a Latent after stage 1) has the resolved length, which
                # an "auto" param upstream only bounds
                known = [
                    v.frames for v in inputs.values() if isinstance(getattr(v, "frames", None), int)
                ]
                frames = known[0] if known else _frames(nodes, schemas, nid)
                mp_in = max((v["width"] * v["height"] / 1e6 for v in refs), default=0.0)
                peak = evaluate(
                    impl.peak,
                    params | {"mp": mp, "images": len(refs), "frames": frames, "mp_in": mp_in},
                )
            ctx.residency.admit(needs, peak, later() - set(needs))
        outs = impl.fn(ctx, params, inputs)
        if (
            impl.unload_after and ctx.residency is not None
        ):  # model.yaml: the stage's components go now
            comps = _components(nodes, impls, schemas, nid)
            ctx.residency.release({k for k in comps if k[2] in impl.unload_after})
        return outs

    async def get(nid: str) -> dict:
        if nid in done:
            return done[nid]
        node, impl, schema = nodes[nid], impls[nodes[nid]["kind"]], schemas[nodes[nid]["kind"]]
        # a node that makes or passes a Model (<family>/model, lora-stack) is pure and cheap: rebuilt every run, never
        # cached (a lora-stack's key does not cover its upstream handle)
        uncached = not schema["outputs"] or any(o["type"] == "Model" for o in schema["outputs"])
        if not uncached and (hit := cache.get(k[nid])) is not None:
            log.info("%s: cache hit", nid)
            done[nid] = hit
            pending.discard(nid)
            return hit
        wired = _wired_params(node, schema)
        inputs, upstream_params = {}, {}
        for port, ref in node.get("in", {}).items():
            src, _, out = ref.partition(".")
            (upstream_params if port in wired else inputs)[port] = (await get(src))[out]
        ctx.node, ctx.impl, ctx.started = nid, impl, time.monotonic()
        pending.discard(nid)
        specs = schema["params"]
        params = {p: v for p, v in node.get("params", {}).items() if p not in wired}
        # read the Image values before the node's first step; the bytes go after decoding
        if images.values(specs, params):
            params = await images.load(specs, params, ctx.owner, ctx.fetch)
        params |= upstream_params  # a wired value param gets the upstream value
        outs = await loop.run_in_executor(GPU, job, nid, impl, params, inputs)
        log.info("%s: ran in %.1f s", nid, time.monotonic() - ctx.started)
        if not uncached:
            cache[k[nid]] = outs
        done[nid] = outs
        return outs

    for nid in upstream(nodes, sinks):  # a pack's own checks, before anything loads or runs
        impl, node = impls[nodes[nid]["kind"]], nodes[nid]
        if impl.check and (why := impl.check(node.get("params", {}), set(node.get("in", {})))):
            raise RunError(f"{nid}{why}")
    for nid in sinks:
        await get(nid)
