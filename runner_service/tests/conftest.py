"""Stand-ins for NATS and the object store, so the runner is tested without either. Test modules import the helpers
with `from conftest import ...`."""

import asyncio
import json
from pathlib import Path

import pytest
from nats.js.errors import KeyNotFoundError

from consumer import Consumer
from core import registry
from messaging.hosts import Host

CONTRACTS = Path(__file__).resolve().parents[2] / "contracts"
GEN = Path(__file__).resolve().parents[1] / "gen"


def command(**echo_params) -> dict:
    """The canonical cmd.run.execute fixture, with echo params overridden (delay 0 by default)."""
    cmd = json.loads((CONTRACTS / "fixtures/messages/cmd.run.execute.valid.json").read_text())
    cmd["data"]["workflow"]["nodes"]["echo"]["params"] |= {"delay_ms": 0, "refs": []} | echo_params
    return cmd


def qwen_command(n: int = 2) -> dict:
    """cmd.run.execute.valid.<n>.json (a qwen-image run); the text-to-image one (2) predates canvas `mode` and
    encode `refs`, so they are filled with the registry defaults as studio would."""
    cmd = json.loads((CONTRACTS / f"fixtures/messages/cmd.run.execute.valid.{n}.json").read_text())
    nodes = cmd["data"]["workflow"]["nodes"]
    nodes["canvas"]["params"].setdefault("mode", "auto")
    nodes["encode"]["params"].setdefault("refs", [])
    return cmd


class FakeJS:
    """JetStream stand-in: records every publish; calls `on_publish(subject, body)` after each."""

    def __init__(self, on_publish=None) -> None:
        self.sent: list[tuple[str, bytes, dict | None]] = []
        self.on_publish = on_publish

    async def publish(self, subject: str, data: bytes, headers: dict | None = None) -> None:
        self.sent.append((subject, data, headers))
        if self.on_publish:
            self.on_publish(subject, json.loads(data) if subject.startswith("evt.") else data)

    def events(self) -> list[dict]:
        return [json.loads(d) for s, d, _ in self.sent if s.startswith("evt.")]


class FakeWatcher:
    def __init__(self) -> None:
        self.queue: asyncio.Queue = asyncio.Queue()

    def __aiter__(self):
        return self

    async def __anext__(self):
        entry = await self.queue.get()
        if entry is StopAsyncIteration:
            raise StopAsyncIteration
        return entry

    async def stop(self) -> None:
        self.queue.put_nowait(StopAsyncIteration)


class FakeKV:
    """KV bucket stand-in: put, get, delete, and watch of one key."""

    def __init__(self) -> None:
        self.data: dict[str, bytes] = {}
        self.watchers: dict[str, FakeWatcher] = {}

    async def put(self, key: str, value: bytes) -> None:
        self.data[key] = value
        if w := self.watchers.get(key):
            w.queue.put_nowait(key)

    def put_nowait(self, key: str, value: bytes = b"x") -> None:
        asyncio.get_running_loop().create_task(self.put(key, value))

    async def get(self, key: str):
        if key not in self.data:
            raise KeyNotFoundError
        return self.data[key]

    async def delete(self, key: str) -> None:
        self.data.pop(key, None)

    async def watch(self, key: str, ignore_deletes: bool = False) -> FakeWatcher:
        w = self.watchers[key] = FakeWatcher()
        w.queue.put_nowait(None)  # end of the initial values
        return w


class FakeStore:
    def __init__(self) -> None:
        self.objects: dict[str, tuple[bytes, str]] = {}
        self.reads: list[str] = []
        self.etags: dict[str, str] = {}  # key → ETag, for the keys a test replaced

    async def put(self, key: str, data: bytes, content_type: str) -> None:
        self.objects[key] = (data, content_type)

    async def upload(self, key: str, path, content_type: str) -> tuple[int, str]:
        import hashlib

        data = Path(path).read_bytes()
        self.objects[key] = (data, content_type)
        return len(data), hashlib.md5(data).hexdigest()

    async def get(self, key: str, limit: int, etag: str | None = None) -> bytes | None:
        self.reads.append(key)
        if etag and self.etags.get(key, etag) != etag:
            return None
        return self.objects[key][0][: limit + 1] if key in self.objects else None

    async def download(self, key: str, dest, limit: int, stop=None):
        import hashlib

        self.reads.append(key)
        if key not in self.objects:
            return None
        data = self.objects[key][0][: limit + 1]
        Path(dest).write_bytes(data)
        return len(data), hashlib.sha256(data).hexdigest()


class FakeMsg:
    """A delivery: records ack/term in `log`, next to the publishes (via `js.sent`)."""

    def __init__(self, cmd, js: FakeJS, subject: str = "cmd.run.fake.fake") -> None:
        self.subject, self.js = subject, js
        self.data = cmd if isinstance(cmd, bytes) else json.dumps(cmd).encode()
        self.log: list[tuple[str, int]] = []  # (action, publishes before it)

    async def ack_sync(self) -> None:
        self.log.append(("ack", len(self.js.sent)))

    async def term(self) -> None:
        self.log.append(("term", len(self.js.sent)))

    async def in_progress(self) -> None:
        pass


VALUES, IMPLS, _ = registry.build("fake")
SCHEMAS = registry.node_schemas(VALUES)


def consumer(js: FakeJS, **kw) -> Consumer:
    """A Consumer on fakes, with the fake runner's real registry and node implementations."""
    return Consumer(
        js=js,
        store=FakeStore(),
        cancels_kv=FakeKV(),
        host=Host(FakeKV()),
        impls=kw.pop("impls", IMPLS),
        node_schemas=kw.pop("node_schemas", SCHEMAS),
        **kw,
    )


README = Path(__file__).resolve().parents[1] / "packs" / "README.md"


def readme_model_yaml() -> dict:
    """The worked model.yaml example of packs/README.md (the qwen-image pack)."""
    import yaml

    text = README.read_text()
    return yaml.safe_load(text.split("```yaml\n", 1)[1].split("```", 1)[0])


def safetensors_file(path: Path, tensors: dict[str, tuple[str, list[int]]]) -> None:
    """A safetensors file of zeros: name → (dtype, shape)."""
    size = {"BF16": 2, "F32": 4, "I8": 1}
    header, offset = {}, 0
    for name, (dtype, shape) in tensors.items():
        n = size[dtype]
        for d in shape:
            n *= d
        header[name] = {"dtype": dtype, "shape": shape, "data_offsets": [offset, offset + n]}
        offset += n
    blob = json.dumps(header).encode()
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(len(blob).to_bytes(8, "little") + blob + bytes(offset))


class FakeQwen:
    """The README's qwen-image pack with stage functions that only record calls, weights faked as tiny
    safetensors files under a temporary models folder, and a loader that counts loads."""

    def __init__(self, models: Path, monkeypatch) -> None:
        import types

        import config
        from core import pack

        monkeypatch.setattr(config, "MODELS", models)
        spec = readme_model_yaml()
        for vid, v in spec["variants"].items():
            root = models / v["dir"]
            for comp, c in spec["components"].items():
                if comp not in v.get("shares", []) and "addon" not in c:  # add-ons: not downloaded
                    shape = [64, 32] if comp == "transformer" else [16, 8]
                    name = "transformer_blocks.0.ff.weight" if comp == "transformer" else "w"
                    path = root / c["files"].replace("*", vid)
                    safetensors_file(path, {name: ("BF16", shape)})
        self.calls: list[tuple[str, dict]] = []
        self.loads: list[tuple] = []
        self.active: list[tuple] = []  # (component, ((sha256, strength), ...)) per ctx.component activation

        def stage(name: str, out: str):
            def fn(ctx, params, inputs):
                self.calls.append((name, params))
                for c in ctx.impl.uses:
                    ctx.component(inputs["model"], c)
                ctx.check()
                ctx.progress(1, 1, name)
                if out == "image":
                    from PIL import Image

                    return {"image": Image.new("RGBA", (inputs["latent"]["w"], 8))}
                if out == "latent":
                    return {"latent": {"w": inputs["canvas"]["width"] // 32}}
                return {out: object()}

            return fn

        module = types.SimpleNamespace(
            __name__="fake_qwen",
            enhance=stage("enhance", "text"),
            encode=stage("encode", "cond"),
            sample=stage("sample", "latent"),
            decode=stage("decode", "image"),
            ask=stage("ask", "answer"),
        )
        self.pack = pack.Pack("qwen_image", models, spec, "torch", module, "c0de")
        monkeypatch.setattr(pack.ModelRef, "load", lambda ref, c, ctx: self._load(ref, c, ctx))
        monkeypatch.setattr(pack.ModelRef, "activate", lambda ref, c, m, ctx: self._activate(ref, c))
        core = pack.core_nodes()
        model = pack.model_schema(self.pack)
        stages = pack._nodes(spec["stages"], self.pack.family, "c0de", self.pack, module)
        every = (
            {k: core[k] for k in ("prompt", "seed", "canvas", "image")}
            | {model["kind"]: (model, pack.Node(pack.model_node(self.pack), code="c0de"))}
            | {
                f"{self.pack.family}/lora-stack": (
                    pack.lora_stack_schema(self.pack),
                    pack.Node(pack.lora_stack, code="c0de"),
                )
            }
            | stages
            | {"output": core["output"], "answer": core["answer"]}
        )
        self.schemas = {k: s for k, (s, _) in every.items()}
        self.impls = {k: n for k, (_, n) in every.items()}

    def _load(self, ref, component: str, ctx):
        self.loads.append(ref.key(component))
        return object()

    def _activate(self, ref, component: str) -> float:
        adapters = ref.adapters(component)
        self.active.append((component, tuple((sha, s) for sha, (_, s) in adapters.items())))
        return 0.001 * len(adapters)

    def ran(self) -> list[str]:
        out, self.calls = [c for c, _ in self.calls], []
        return out


# --- the torch tests' shared helpers


@pytest.fixture(scope="session")
def torch_values():
    """The torch runner's registry values, built once (read only)."""
    return registry.build("torch")[0]


def snr(want, got) -> float:
    """The golden tests' signal-to-noise ratio in dB of numpy arrays: -inf when the shapes differ, inf when equal."""
    import numpy as np

    if want.shape != got.shape:
        return float("-inf")
    noise = np.sum((want - got) ** 2)
    return float("inf") if noise == 0 else float(10 * np.log10(np.sum(want**2) / noise))


def e4m3(w):
    """Per-row FP8 as the community files store it: qweight e4m3 [out, in], scale fp32 [out, 1]."""
    import torch

    from core import loaders_torch as lt

    scale = w.abs().amax(1, keepdim=True).float() / lt.E4M3_MAX
    return (w.float() / scale).to(torch.float8_e4m3fn), scale
