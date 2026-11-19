"""Packs: a folder runner_service/packs/<name>/ with model.yaml, one python module per runtime (`fake.py`,
`torch.py`) and workflows/*.json. The pack API is documented in packs/README.md (repo root); this module turns a
pack into registry values (node schemas, workflows, the models entry) and node implementations.

The core's own nodes (prompt, seed, canvas, image, output) are written in the same stage grammar in core/nodes.yaml."""

import ast
import fnmatch
import hashlib
import importlib
import json
import struct
import sys
from dataclasses import dataclass, field, replace
from pathlib import Path

import yaml

from core import looks

PACKS = Path(__file__).resolve().parent.parent / "packs"
PRECISIONS = ("bf16", "fp16", "int8", "int4")
BYTES = {"bf16": 2, "fp16": 2, "fp32": 4, "int8": 1}
STORED = {
    "BF16": 2,
    "F16": 2,
    "F32": 4,
    "F64": 8,
    "U8": 1,
    "I8": 1,
    "U32": 4,  # MLX's packed quantized weights
    "I16": 2,
    "I32": 4,
    "I64": 8,
    "BOOL": 1,
}
QUANT_GROUP = 64  # the MLX loader's quantization group
STALL_S = 600  # a stage's default stall limit: seconds without a cancel point


@dataclass(eq=False)
class Node:
    """One node kind's implementation: `fn(ctx, params, inputs) -> {output: value}`, run on the GPU thread.
    `uses` names the components it asks for, `peak` its compiled peak_gb formula, `code` the hash that goes into
    its cache keys."""

    fn: object
    uses: tuple[str, ...] = ()
    peak: object = None  # a compiled expression, or None
    stall_s: float = STALL_S
    code: str = "core"
    pack: Pack | None = None
    check: object = None  # the pack's check_<stage>(params, wired, models) -> problem text or None, run before anything
    unload_after: tuple[str, ...] = ()  # components unloaded once the stage has run
    alone: bool = (
        False  # its components never sit beside the model's others: plans count no kept ones
    )
    sees: dict = field(
        default_factory=dict
    )  # param → output: that param of the node(s) the output feeds
    reports: tuple[
        str, ...
    ] = ()  # Text outputs reported as the run's resolved.texts (`<node>.<output>`)


@dataclass(eq=False)
class Pack:
    name: str
    dir: Path
    spec: dict
    runtime: str  # the runtime key of model.yaml (the module name), e.g. "torch"
    module: object  # the runtime module, or None for a pack without one
    code: str = ""  # hash of model.yaml and the runtime module
    workflows: list = field(default_factory=list)
    looks: list = field(default_factory=list)  # starter looks (core/looks.py), checked at load
    cache: dict = field(default_factory=dict)  # values computed once (the lora block)

    @property
    def family(self) -> str:
        return self.spec["family"]

    def component(self, name: str) -> dict:
        return self.spec.get("components", {})[name]


@dataclass(frozen=True)
class LoraUse:
    """One row of a LoRA Stack as it reached the runner: the resolved Lora value (cmd.run.execute `defs lora`) with
    its strength and mode."""

    lora_id: str
    source: str  # shared | own
    name: str
    sha256: str
    bytes: int
    strength: float
    mode: str  # adapter | bake
    key: str | None = None  # own only: u/<owner_id>/loras/<lora_id>.safetensors

    @classmethod
    def of(cls, row: dict) -> LoraUse:
        v = row["lora"]
        return cls(v["lora_id"], v["source"], v["name"], v["sha256"], v["bytes"],
                   float(row["strength"]), row["mode"], v.get("key"))  # fmt: skip


@dataclass(frozen=True)
class ModelRef:
    """The Model handle: which weights (pack, variant) at which precision, and the ordered LoRA set the LoRA Stack
    nodes upstream added. Components load through ctx.component."""

    pack: Pack
    variant: str
    precision: str
    loras: tuple[LoraUse, ...] = ()

    @property
    def lora_component(self) -> str | None:
        return (self.pack.spec.get("lora") or {}).get("component")

    def baked(self, component: str) -> tuple[LoraUse, ...]:
        """The rows of mode bake that patch `component`, in order, one file twice (chained stacks) merged into its
        first row with the summed strength: each file's delta is held once at load."""
        out: dict[str, LoraUse] = {}
        for u in self.loras:
            if component == self.lora_component and u.mode == "bake":
                first = out.get(u.sha256)
                out[u.sha256] = replace(first, strength=first.strength + u.strength) if first else u
        return tuple(out.values())

    def adapters(self, component: str) -> dict[str, tuple[LoraUse, float]]:
        """sha256 → (the first row, the summed strength) of the adapter rows that patch `component`, in order (one
        file twice in chained stacks is one adapter: the deltas add up)."""
        out: dict[str, tuple[LoraUse, float]] = {}
        for u in self.loras:
            if component == self.lora_component and u.mode == "adapter":
                first, s = out.get(u.sha256, (u, 0.0))
                out[u.sha256] = (first, s + u.strength)
        return out

    def owner(self, component: str) -> str:
        """The variant whose folder holds `component`'s files: the first variant when this one shares it, unless
        this one holds its own copy (a download found the owner's hashes differ: `separate`)."""
        shares = self.pack.spec["variants"][self.variant].get("shares", [])
        if component in shares and not owns_copy(self.pack, self.variant, component):
            return next(iter(self.pack.spec["variants"]))
        return self.variant

    def precision_of(self, component: str) -> str:
        """The precision `component` loads at: the model's, when the component offers it, else its first."""
        offered = self.pack.component(component).get("precision", ["bf16"])
        return self.precision if self.precision in offered else offered[0]

    def key(self, component: str) -> tuple:
        """The residency key: (model id, owning variant, component, precision), one file set at one precision."""
        key = (
            self.pack.spec["id"],
            self.owner(component),
            component,
            self.precision_of(component),
        )
        # a baked component is its own entry: + the baked (sha256, strength) in order (`resident_loras` is what the
        # hosts value lists; a rename is not a new entry)
        if baked := self.baked(component):
            key += (tuple((u.sha256, u.strength) for u in baked),)
        return key

    def resident_loras(self, component: str) -> list[dict] | None:
        """The hosts value's resident[].loras of `component`'s entry: its baked rows, or None."""
        baked = self.baked(component)
        return [{"lora_id": u.lora_id, "name": u.name, "strength": u.strength, "bytes": u.bytes} for u in baked] or None  # fmt: skip

    @staticmethod
    def lora_gb(uses, dtype_bytes: int) -> float:
        """An upper bound of the device memory the LoRAs `uses` take at `dtype_bytes` per element: a LoRA file holds
        at least 2 bytes per element (F16, BF16, F32), so its size / 2 bounds its element count."""
        return sum(u.bytes for u in uses) / 2 * dtype_bytes / 2**30

    def files(self, component: str) -> list[Path]:
        return component_files(self.pack, self.owner(component), component)

    def folder(self, component: str | None = None) -> Path:
        """The variant's model folder (with `component`: the folder of the variant owning its files, or the
        component's own `dir`), for the small non-weight files a pack reads itself (configs, tokenizer, scheduler)."""
        if component and (own := self.pack.component(component).get("dir")):
            return models_root() / own
        variant = self.owner(component) if component else self.variant
        return models_root() / self.pack.spec["variants"][variant]["dir"]

    def fingerprint(self, components) -> list:
        """What a cache key holds for this Model input: per component, its key and each file's size and mtime."""
        out = []
        for c in sorted(components):
            stats = [(str(p), p.stat().st_size, p.stat().st_mtime_ns) for p in self.files(c)]
            out.append([*self.key(c)[:4], stats])
            if c == self.lora_component and self.loras:  # an empty set adds nothing to any key
                out[-1].append([[u.sha256, u.strength, u.mode] for u in self.loras])
        return out

    def load(self, component: str, ctx):
        """Load `component` with the core loader of the pack's runtime (core/loaders_<runtime>.py)."""
        loader = importlib.import_module(f"core.loaders_{self.pack.runtime}")
        return loader.load_component(self, component, ctx)

    def activate(self, component: str, module, ctx) -> float:
        """Make `module` (this handle's loaded `component`) carry exactly its adapter LoRAs at their strengths;
        returns the adapters' GB (core/loaders_<runtime>.py `activate`)."""
        loader = importlib.import_module(f"core.loaders_{self.pack.runtime}")
        return loader.activate(self, component, module, ctx)

    def extra_gb(self, component: str, module) -> float:
        """GB the loaded `module` holds beyond `gb` (core/loaders_<runtime>.py `extra_gb`, 0 without one); the
        loader is the one the load that made `module` imported (none: a fake load, 0)."""
        loader = sys.modules.get(f"core.loaders_{self.pack.runtime}")
        return loader.extra_gb(module) if hasattr(loader, "extra_gb") else 0.0

    def gb(self, component: str) -> tuple[float, float]:
        """(resident size, load-time transient) in GB, from the safetensors headers and the target dtype; the
        transient includes the baked LoRAs' deltas, held in fp32 on the device while it loads."""
        gb, transient = size_gb(self.pack, self.key(component)[:4], tuple(self.files(component)))
        return gb, transient + self.lora_gb(self.baked(component), 4)


def default_model(pack: Pack) -> ModelRef:
    """The Model a stage with `uses` but no Model input loads from (MiniMax's enhance: a model of its own beside the
    family's): the first variant at the model node's default precision."""
    return ModelRef(
        pack, next(iter(pack.spec["variants"])), model_schema(pack)["params"][1]["default"]
    )


def models_root() -> Path:
    import config

    return config.MODELS


def owns_copy(pack: Pack, variant: str, component: str) -> bool:
    """Whether the variant's own folder holds files of `component` (a shared one kept separate); a link (into the
    owner's folder, as a hand-made share) is not a copy."""
    from core.models import linked

    first = next(iter(pack.spec["variants"]))
    root = models_root() / pack.spec["variants"][variant]["dir"]
    if variant != first and root == models_root() / pack.spec["variants"][first]["dir"]:
        return (
            False  # one folder for both (LTX dev beside distilled): one copy, the first variant's
        )
    globs = component_globs(pack, variant, component)
    return any(p.is_file() and not p.is_symlink() and not linked(root, p.relative_to(root).as_posix())
               for g in ([globs] if isinstance(globs, str) else globs) for p in root.glob(g))  # fmt: skip


def component_globs(pack: Pack, variant: str, component: str) -> list[str]:
    """model.yaml `files` of a component (a glob or a list of globs), or the variant's own `files` for it (a variant
    whose file sits in another variant's folder under another name: LTX dev's transformer)."""
    g = (
        pack.spec["variants"][variant].get("files", {}).get(component)
        or pack.component(component)["files"]
    )
    return [g] if isinstance(g, str) else g


def component_files(pack: Pack, variant: str, component: str) -> list[Path]:
    """The files of a component in a variant's folder (or the component's own `dir`: a model of its own the family
    loads, as MiniMax's prompt enhancer), sorted per glob."""
    root = models_root() / (
        pack.component(component).get("dir") or pack.spec["variants"][variant]["dir"]
    )
    return [p for g in component_globs(pack, variant, component) for p in sorted(root.glob(g))]


def header(path: Path) -> dict:
    """A safetensors file's header: key → {dtype, shape, ...}."""
    with path.open("rb") as f:
        (n,) = struct.unpack("<Q", f.read(8))
        h = json.loads(f.read(n))
    h.pop("__metadata__", None)
    return h


def quantized(spec: dict, precision: str, module_name: str) -> bool:
    """Whether a Linear named `module_name` is stored quantized (precision int8 or int4 and a `quantize` glob
    matches)."""
    return precision in ("int8", "int4") and any(
        fnmatch.fnmatchcase(module_name, g) for g in spec.get("quantize", [])
    )


_sizes: dict = {}


def size_gb(pack: Pack, key: tuple, files: tuple[Path, ...]) -> tuple[float, float]:
    """Resident GB at the target dtype and the transient: the largest tensor in fp32. Only the keys the pack's
    `rename(component, key)` keeps count, under their new names; `dtype: stored` counts each tensor's own dtype. A
    quantized weight: torch int8 is 1 byte + a bf16 scale per row; MLX is bits / 8 per value + a bf16 scale and
    bias per group of 64."""
    if (key, files) not in _sizes:
        component, precision = key[2], key[3]
        spec, rename = pack.component(component), getattr(pack.module, "rename", None)
        dtype = spec.get("dtype", "bf16")
        total = largest = 0
        for p in files:
            for raw, t in header(p).items():
                name = rename(component, raw) if rename else raw
                if name is None:
                    continue
                numel = 1
                for d in t["shape"]:
                    numel *= d
                largest = max(largest, numel * 4)
                module, _, attr = name.rpartition(".")
                if attr == "weight" and len(t["shape"]) == 2 and quantized(spec, precision, module):
                    if pack.runtime == "mlx":
                        total += (
                            numel * (8 if precision == "int8" else 4) // 8
                            + numel // QUANT_GROUP * 4
                        )
                    else:  # a U8 weight packs two 4-bit values per byte (NVFP4, dequantized at load)
                        total += numel * (2 if t["dtype"] == "U8" else 1) + t["shape"][0] * 2
                else:
                    total += numel * (
                        STORED.get(t["dtype"], 2) if dtype == "stored" else BYTES[dtype]
                    )
        _sizes[(key, files)] = (total / 2**30, largest / 2**30)
    return _sizes[(key, files)]


# --- peak_gb formulas: a Python expression over `mp` (megapixels of the run's canvas), `images` (Image values in
# the node's and its upstream nodes' params) and the node's params

_ALLOWED = (
    ast.Expression, ast.BinOp, ast.UnaryOp, ast.BoolOp, ast.Compare, ast.IfExp, ast.Name, ast.Load,
    ast.Constant, ast.Call, ast.Add, ast.Sub, ast.Mult, ast.Div, ast.Pow, ast.USub, ast.Not, ast.And,
    ast.Or, ast.Eq, ast.NotEq, ast.Lt, ast.LtE, ast.Gt, ast.GtE,
)  # fmt: skip


def compile_formula(text: str, names: set[str]):
    """Compile a peak_gb formula; ValueError for anything but arithmetic, comparisons, `a if c else b`,
    min/max/len (len: a rows param's items), and the names `names`."""
    tree = ast.parse(str(text), mode="eval")
    for n in ast.walk(tree):
        if not isinstance(n, _ALLOWED):
            raise ValueError(f"peak_gb {text!r}: {type(n).__name__} is not allowed")  # noqa: TRY004
        if isinstance(n, ast.Call) and not (
            isinstance(n.func, ast.Name) and n.func.id in ("min", "max", "len")
        ):
            raise ValueError(f"peak_gb {text!r}: only min(), max() and len() may be called")
        if isinstance(n, ast.Name) and n.id not in names | {"min", "max", "len"}:
            raise ValueError(f"peak_gb {text!r}: unknown name {n.id!r} (known: {sorted(names)})")
    return compile(tree, f"<peak_gb {text}>", "eval")


def evaluate(code, values: dict) -> float:
    return float(eval(code, {"__builtins__": {}, "min": min, "max": max, "len": len}, values))


# --- node schemas


def stage_schema(name: str, stage: dict, family: str | None) -> dict:
    """The registry nodeSchema of one stage: kind <family>/<name> (a core node: <name>)."""
    schema = {
        "kind": f"{family}/{name}" if family else name,
        "title": stage["title"],
        "family": family,
        "params": [{"name": p} | spec for p, spec in (stage.get("params") or {}).items()],
        # an input is `name: Type`, or `name: {type: Type, optional: true}`
        "inputs": [
            {"name": p} | (t if isinstance(t, dict) else {"type": t})
            for p, t in (stage.get("inputs") or {}).items()
        ],
        "outputs": [{"name": p, "type": t} for p, t in (stage.get("outputs") or {}).items()],
    }
    if "help" in stage:
        schema["help"] = stage["help"]
    return schema


def model_schema(pack: Pack) -> dict:
    """The generated <family>/model node: a `variant` enum over model.yaml variants and a `precision` enum over
    what its components offer; labels, help and defaults from model.yaml `model`."""
    spec, extra = pack.spec, pack.spec.get("model", {})
    variants = list(spec["variants"])
    offered = {p for c in spec.get("components", {}).values() for p in c.get("precision", ["bf16"])}
    precisions = [p for p in PRECISIONS if p in offered]
    schema = {
        "kind": f"{pack.family}/model",
        "title": spec["title"],
        "family": pack.family,
        "params": [
            {"name": "variant", "type": "enum", "enum": variants, "default": variants[0]}
            | extra.get("variant", {}),
            {"name": "precision", "type": "enum", "enum": precisions, "default": precisions[0]}
            | extra.get("precision", {}),
        ],
        "inputs": [],
        "outputs": [{"name": "model", "type": "Model"}],
    }
    if "help" in extra:
        schema["help"] = extra["help"]
    return schema


MODE_HELP = (
    "Adapter: strength changes and swaps never reload the model. "
    "Bake: merged into the weights at load (its own copy in memory)."
)


def lora_stack_schema(pack: Pack) -> dict:
    """The generated <family>/lora-stack node (Model → Model, rows of {lora, strength, mode}) from model.yaml
    `lora` (component, modes, max_rows, strength {min, max, default})."""
    spec = pack.spec["lora"]
    st = {"min": -4, "max": 4, "default": 1} | spec.get("strength", {})
    modes = spec.get("modes", ["adapter", "bake"])
    item = {
        "lora": "Lora",
        "strength": {"type": "float", "label": "Strength", "default": st["default"],
                     "min": st["min"], "max": st["max"], "step": 0.05},
        "mode": {"type": "enum", "label": "Mode", "default": modes[0], "enum": modes,
                 "help": spec.get("mode_help", MODE_HELP)},
    }  # fmt: skip
    return {
        "kind": f"{pack.family}/lora-stack",
        "title": "LoRA Stack",
        "family": pack.family,
        "help": spec.get(
            "help",
            f"Applies LoRAs to the model's {spec['component']}, in order. Empty: the model as it is.",
        ),
        "params": [
            {
                "name": "rows",
                "type": "rows",
                "label": "LoRAs",
                "default": [],
                "min": 0,
                "max": spec.get("max_rows", 8),
                "item": item,
                "help": spec.get(
                    "rows_help",
                    "Each row: a LoRA that fits this model, its strength and how it is applied.",
                ),
            }
        ],
        "inputs": [{"name": "model", "type": "Model"}],
        "outputs": [{"name": "model", "type": "Model"}],
    }


def lora_stack(ctx, params: dict, inputs: dict) -> dict:
    """The Model with this node's rows after the upstream set; no rows: the same handle (same cache identity)."""
    model, rows = inputs["model"], params["rows"]
    if not rows:
        return {"model": model}
    return {"model": replace(model, loras=model.loras + tuple(LoraUse.of(r) for r in rows))}


def lora_block(pack: Pack) -> dict | None:
    """The registry lora block {component, modules, renames, ignore[, prefused]}: modules are the Linear names of
    the component built on the meta device (no weights) from the first present variant, compressed to `{a..b}`
    ranges. None without a `lora` block, a `build()` or a present variant; a model-free pack may list `modules`
    itself. `prefused` starts empty: registry.fill_prefused puts the shared index's ids in. Computed once per pack."""
    if "lora_block" not in pack.cache:
        spec, block = pack.spec.get("lora"), None
        build = getattr(pack.module, "build", None)
        present = [v for v in pack.spec.get("variants", {}) if all(
            ModelRef(pack, v, "bf16").files(c) for c in pack.spec.get("components", {}))]  # fmt: skip
        if spec and "modules" in spec:  # a model-free pack (the fake echo) names them itself
            block = {k: spec.get(k, []) for k in ("component", "modules", "renames", "ignore")}
        elif spec and build and present:
            from core import lora

            loader = importlib.import_module(f"core.loaders_{pack.runtime}")
            meta = build(spec["component"], ModelRef(pack, present[0], "bf16"))
            block = {
                "component": spec["component"],
                "modules": lora.compress(loader.linears(meta)),
                "renames": spec.get("renames", []),
                "ignore": spec.get("ignore", []),
            }
            if "prefused" in spec:
                block["prefused"] = []
        pack.cache["lora_block"] = block
    return pack.cache["lora_block"]


def model_node(pack: Pack):
    def model(ctx, params: dict, inputs: dict) -> dict:
        return {"model": ModelRef(pack, params["variant"], params["precision"])}

    return model


def models_entry(pack: Pack) -> dict:
    """The pack's entry in the `models` registry value, as on disk now (core.models.entry)."""
    from core import models

    return models.entry(pack)


# --- loading


def _nodes(stages: dict, family: str | None, code: str, pack: Pack | None, module) -> dict:
    """kind → (schema, Node) for each stage; `module` holds the stage functions (named after the stage)."""
    out = {}
    for name, stage in stages.items():
        schema = stage_schema(name, stage, family)
        if shared := {p["name"] for p in schema["params"]} & {i["name"] for i in schema["inputs"]}:
            raise ValueError(
                f"{schema['kind']}: params and inputs share the names {sorted(shared)}"
            )
        names = {"mp", "images", "frames", "mp_in", "fps"} | set(stage.get("params") or {})
        peak = compile_formula(stage["peak_gb"], names) if "peak_gb" in stage else None
        fn = getattr(module, name, None)
        if fn is None:
            raise ValueError(f"{schema['kind']}: no function {name}() in {module.__name__}")
        uses = tuple(stage.get("uses", ()))
        if pack and (unknown := set(uses) - set(pack.spec.get("components", {}))):
            raise ValueError(f"{schema['kind']}: uses unknown components {sorted(unknown)}")
        unload = tuple(stage.get("unload_after", ()))
        if unknown := set(unload) - set(uses):
            raise ValueError(
                f"{schema['kind']}: unload_after names components outside its uses {sorted(unknown)}"
            )
        check = getattr(module, f"check_{name}", None)
        node = Node(fn, uses, peak, stage.get("stall_s", STALL_S), code, pack, check, unload,
                    bool(stage.get("alone")), dict(stage.get("sees") or {}), tuple(stage.get("reports", ())))  # fmt: skip
        outs = {o["name"]: o["type"] for o in schema["outputs"]}
        if bad := [o for o in (*node.sees.values(), *node.reports) if outs.get(o) != "Text"]:
            raise ValueError(
                f"{schema['kind']}: sees / reports name outputs that are not Text: {bad}"
            )
        out[schema["kind"]] = (schema, node)
    return out


def core_nodes() -> dict:
    """kind → (schema, Node) of the core nodes (core/nodes.yaml, functions in core/nodes.py)."""
    from core import nodes

    spec = yaml.safe_load((Path(__file__).parent / "nodes.yaml").read_text())
    return _nodes(spec["stages"], None, "core", None, nodes)


def load(dir: Path, runtime: str) -> tuple[Pack, dict]:
    """One pack for one runtime: (Pack, kind → (schema, Node)). ValueError when it is inconsistent."""
    spec = yaml.safe_load((dir / "model.yaml").read_text())
    path = dir / f"{runtime}.py"
    code = hashlib.sha256((dir / "model.yaml").read_bytes() + path.read_bytes()).hexdigest()[:16]
    module = importlib.import_module(f"packs.{dir.name}.{runtime}")
    pack = Pack(dir.name, dir, spec, runtime, module, code)
    pack.workflows = [json.loads(p.read_text()) for p in sorted(dir.glob("workflows/*.json"))]
    pack.looks = looks.load(pack)
    stages = _nodes(spec.get("stages", {}), pack.family, code, pack, module)
    if "variants" not in spec:
        return pack, stages
    nodes = {
        k: v for k, v in stages.items() if not v[0]["inputs"]
    }  # sources (canvas, ...) first, then the model
    schema = model_schema(pack)
    nodes[schema["kind"]] = (schema, Node(model_node(pack), code=code, pack=pack))
    if "lora" in spec:  # Model → Model: only a family with a model node
        schema = lora_stack_schema(pack)
        nodes[schema["kind"]] = (schema, Node(lora_stack, code=code, pack=pack))
    return pack, nodes | stages


def for_env(env: str, root: Path = PACKS) -> list[tuple[Pack, dict]]:
    """Every pack with a runtime whose env is `env`, in folder order."""
    out = []
    for d in sorted(p for p in root.iterdir() if (p / "model.yaml").is_file()):
        spec = yaml.safe_load((d / "model.yaml").read_text())
        for runtime, r in spec.get("runtimes", {}).items():
            if r["env"] == env:
                out.append(load(d, runtime))
    return out
