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
from dataclasses import dataclass, field
from pathlib import Path

import yaml

PACKS = Path(__file__).resolve().parent.parent / "packs"
PRECISIONS = ("bf16", "fp16", "int8")
BYTES = {"bf16": 2, "fp16": 2, "fp32": 4, "int8": 1}
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


@dataclass(eq=False)
class Pack:
    name: str
    dir: Path
    spec: dict
    runtime: str  # the runtime key of model.yaml (the module name), e.g. "torch"
    module: object  # the runtime module, or None for a pack without one
    code: str = ""  # hash of model.yaml and the runtime module
    workflows: list = field(default_factory=list)

    @property
    def family(self) -> str:
        return self.spec["family"]

    def component(self, name: str) -> dict:
        return self.spec.get("components", {})[name]


@dataclass(frozen=True)
class ModelRef:
    """The Model handle: which weights (pack, variant) at which precision. Components load through ctx.component."""

    pack: Pack
    variant: str
    precision: str

    def owner(self, component: str) -> str:
        """The variant whose folder holds `component`'s files (the first variant when this one shares it)."""
        shares = self.pack.spec["variants"][self.variant].get("shares", [])
        return next(iter(self.pack.spec["variants"])) if component in shares else self.variant

    def precision_of(self, component: str) -> str:
        """The precision `component` loads at: the model's, when the component offers it, else its first."""
        offered = self.pack.component(component).get("precision", ["bf16"])
        return self.precision if self.precision in offered else offered[0]

    def key(self, component: str) -> tuple:
        """The residency key: (model id, owning variant, component, precision), one file set at one precision."""
        return (
            self.pack.spec["id"],
            self.owner(component),
            component,
            self.precision_of(component),
        )

    def files(self, component: str) -> list[Path]:
        return component_files(self.pack, self.owner(component), component)

    def folder(self, component: str | None = None) -> Path:
        """The variant's model folder (with `component`: the folder of the variant owning its files), for the
        small non-weight files a pack reads itself (configs, tokenizer, scheduler)."""
        variant = self.owner(component) if component else self.variant
        return models_root() / self.pack.spec["variants"][variant]["dir"]

    def fingerprint(self, components) -> list:
        """What a cache key holds for this Model input: per component, its key and each file's size and mtime."""
        out = []
        for c in sorted(components):
            stats = [(str(p), p.stat().st_size, p.stat().st_mtime_ns) for p in self.files(c)]
            out.append([*self.key(c), stats])
        return out

    def load(self, component: str, ctx):
        """Load `component` with the core loader of the pack's runtime (core/loaders_<runtime>.py)."""
        loader = importlib.import_module(f"core.loaders_{self.pack.runtime}")
        return loader.load_component(self, component, ctx)

    def gb(self, component: str) -> tuple[float, float]:
        """(resident size, load-time transient) in GB, from the safetensors headers and the target dtype."""
        return size_gb(self.pack, self.key(component), tuple(self.files(component)))


def models_root() -> Path:
    import config

    return config.MODELS


def component_files(pack: Pack, variant: str, component: str) -> list[Path]:
    """The files of a component in a variant's folder (model.yaml `files` glob), sorted."""
    root = models_root() / pack.spec["variants"][variant]["dir"]
    return sorted(root.glob(pack.component(component)["files"]))


def header(path: Path) -> dict:
    """A safetensors file's header: key → {dtype, shape, ...}."""
    with path.open("rb") as f:
        (n,) = struct.unpack("<Q", f.read(8))
        h = json.loads(f.read(n))
    h.pop("__metadata__", None)
    return h


def quantized(spec: dict, precision: str, module_name: str) -> bool:
    """Whether a Linear named `module_name` is stored int8 (precision int8 and a `quantize` glob matches)."""
    return precision == "int8" and any(
        fnmatch.fnmatchcase(module_name, g) for g in spec.get("quantize", [])
    )


_sizes: dict = {}


def size_gb(pack: Pack, key: tuple, files: tuple[Path, ...]) -> tuple[float, float]:
    """Resident GB at the target dtype (an int8 weight: 1 byte + a bf16 scale per row) and the transient: the
    largest tensor in fp32."""
    if (key, files) not in _sizes:
        spec, precision = pack.component(key[2]), key[3]
        dtype_bytes = BYTES[spec.get("dtype", "bf16")]
        total = largest = 0
        for p in files:
            for name, t in header(p).items():
                numel = 1
                for d in t["shape"]:
                    numel *= d
                largest = max(largest, numel * 4)
                module, _, attr = name.rpartition(".")
                if attr == "weight" and len(t["shape"]) == 2 and quantized(spec, precision, module):
                    total += numel + t["shape"][0] * 2
                else:
                    total += numel * dtype_bytes
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
    min/max, and the names `names`."""
    tree = ast.parse(str(text), mode="eval")
    for n in ast.walk(tree):
        if not isinstance(n, _ALLOWED):
            raise ValueError(f"peak_gb {text!r}: {type(n).__name__} is not allowed")  # noqa: TRY004
        if isinstance(n, ast.Call) and not (
            isinstance(n.func, ast.Name) and n.func.id in ("min", "max")
        ):
            raise ValueError(f"peak_gb {text!r}: only min() and max() may be called")
        if isinstance(n, ast.Name) and n.id not in names | {"min", "max"}:
            raise ValueError(f"peak_gb {text!r}: unknown name {n.id!r} (known: {sorted(names)})")
    return compile(tree, f"<peak_gb {text}>", "eval")


def evaluate(code, values: dict) -> float:
    return float(eval(code, {"__builtins__": {}, "min": min, "max": max}, values))


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


def model_node(pack: Pack):
    def model(ctx, params: dict, inputs: dict) -> dict:
        return {"model": ModelRef(pack, params["variant"], params["precision"])}

    return model


def models_entry(pack: Pack) -> dict:
    """The pack's entry in the `models` registry value, with each variant's presence measured on disk:
    present when every component's files are there; bytes = its own files (not links, not shared)."""
    spec = pack.spec
    entry = {"id": spec["id"], "family": pack.family, "title": spec["title"]}
    if "variants" not in spec:
        return entry
    entry["variants"] = []
    offered = {p for c in spec.get("components", {}).values() for p in c.get("precision", ["bf16"])}
    for vid, v in spec["variants"].items():
        ref = ModelRef(pack, vid, "bf16")
        root = models_root() / v["dir"]
        present = root.is_dir() and all(ref.files(c) for c in spec.get("components", {}))
        variant = {"id": vid, "presence": "present" if present else "missing"}
        if present:
            variant["bytes"] = sum(
                f.stat().st_size
                for f in root.rglob("*")
                if f.is_file()
                and not f.is_symlink()
                and not any(part.startswith(".") for part in f.relative_to(root).parts)
            )
        variant["precisions"] = [p for p in PRECISIONS if p in offered]
        entry["variants"].append(variant)
    return entry


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
        names = {"mp", "images"} | set(stage.get("params") or {})
        peak = compile_formula(stage["peak_gb"], names) if "peak_gb" in stage else None
        fn = getattr(module, name, None)
        if fn is None:
            raise ValueError(f"{schema['kind']}: no function {name}() in {module.__name__}")
        uses = tuple(stage.get("uses", ()))
        if pack and (unknown := set(uses) - set(pack.spec.get("components", {}))):
            raise ValueError(f"{schema['kind']}: uses unknown components {sorted(unknown)}")
        out[schema["kind"]] = (
            schema,
            Node(fn, uses, peak, stage.get("stall_s", STALL_S), code, pack),
        )
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
    nodes = {}
    if "variants" in spec:
        schema = model_schema(pack)
        nodes[schema["kind"]] = (schema, Node(model_node(pack), code=code, pack=pack))
    nodes |= _nodes(spec.get("stages", {}), pack.family, code, pack, module)
    return pack, nodes


def for_env(env: str, root: Path = PACKS) -> list[tuple[Pack, dict]]:
    """Every pack with a runtime whose env is `env`, in folder order."""
    out = []
    for d in sorted(p for p in root.iterdir() if (p / "model.yaml").is_file()):
        spec = yaml.safe_load((d / "model.yaml").read_text())
        for runtime, r in spec.get("runtimes", {}).items():
            if r["env"] == env:
                out.append(load(d, runtime))
    return out
