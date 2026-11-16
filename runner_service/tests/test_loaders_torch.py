"""The torch loader (env torch only): Int8Linear, the per-tensor hook chain, direct-to-device loading.
`pytest -m gpu` also loads the real Qwen-Image-2.1-Turbo transformer at int8 and prints its load time and peak."""

import resource
import time
import types

import pytest
from conftest import FakeQwen

torch = pytest.importorskip("torch")
from safetensors.torch import save_file
from torch import nn

from core import loaders_torch as lt
from core import pack
from core.graph import Cancelled


@pytest.mark.parametrize("rows", [1, 4, 300])  # torch's int8 kernel, and the dequantized matmul
def test_int8_linear_matches_the_bf16_linear(rows):
    torch.manual_seed(0)
    linear = nn.Linear(256, 64, bias=True).to(torch.bfloat16)
    q = lt.Int8Linear(linear)
    q.quantize(linear.weight.data)
    x = torch.randn(rows, 256, dtype=torch.bfloat16)
    ref, got = linear(x).float(), q(x).float()
    assert ((got - ref).abs().max() / ref.abs().max()) < 0.02


class Tiny(nn.Module):
    def __init__(self) -> None:
        super().__init__()
        self.blocks = nn.ModuleList([nn.Linear(32, 64)])  # 32-multiples: the MPS int8 kernel runs
        self.head = nn.Linear(64, 4)
        self.register_buffer("freqs", torch.arange(4.0))  # made in __init__, like rotary tables


def meta_tiny() -> Tiny:
    with torch.device("meta"):
        m = Tiny()
    m.register_buffer(
        "freqs", torch.arange(4.0)
    )  # a real CPU buffer, as init_empty_weights leaves it
    return m


@pytest.fixture
def weights(tmp_path):
    torch.manual_seed(0)
    ref = Tiny()
    path = tmp_path / "tiny.safetensors"
    save_file({f"model.{k}": v for k, v in ref.state_dict().items() if k != "freqs"}, str(path))
    return ref, path


def test_load_renames_casts_quantizes_onto_the_device(weights):
    ref, path = weights
    steps = []
    dev = lt.device()
    m = lt.load(
        meta_tiny(),
        [path],
        dev=dev,
        dtype=torch.bfloat16,
        quantize=lambda name: name.startswith("blocks."),
        rename=lambda key: key.removeprefix("model."),
        progress=lambda done, total: steps.append((done, total)),
    )
    assert isinstance(m.blocks[0], lt.Int8Linear) and type(m.head) is nn.Linear
    assert m.head.weight.dtype == torch.bfloat16 and m.head.weight.device.type == dev
    assert m.freqs.device.type == dev and not m.head.weight.requires_grad
    assert steps[-1] == (4, 4)  # blocks.0.weight, blocks.0.bias, head.weight, head.bias
    x = torch.randn(3, 32)
    want = ref.head(ref.blocks[0](x))
    got = m.head(m.blocks[0](x.to(dev, torch.bfloat16))).float().cpu()
    assert ((got - want).abs().max() / want.abs().max()) < 0.03


def test_load_gives_the_allocator_cache_back_as_it_goes(weights, monkeypatch):
    _, path = weights
    freed = []
    monkeypatch.setattr(lt, "free", lambda: freed.append(1))
    monkeypatch.setattr(
        lt, "FREE_EVERY_GB", 1000 / 2**30
    )  # every 1000 bytes read: the 32x32 fp32 weights
    lt.load(meta_tiny(), [path], dev=lt.device(), dtype=torch.bfloat16,
            rename=lambda key: key.removeprefix("model."))  # fmt: skip
    # after blocks.0.weight and head.weight (4096 bytes each; the biases add up to less), and once at the end
    assert len(freed) == 3


def test_load_reports_missing_and_unknown_keys(weights):
    _, path = weights
    with pytest.raises(KeyError, match="has no place"):
        lt.load(meta_tiny(), [path], dev="cpu", dtype=torch.bfloat16)  # model.* keys, no rename
    with pytest.raises(KeyError, match="not in the checkpoint: head.weight"):
        rename = lambda k: None if k == "model.head.weight" else k.removeprefix("model.")
        lt.load(meta_tiny(), [path], dev="cpu", dtype=torch.bfloat16, rename=rename)


def test_load_stops_at_a_cancel_point(weights):
    _, path = weights
    seen = []

    def check():
        seen.append(1)
        if len(seen) == 2:
            raise Cancelled

    with pytest.raises(Cancelled):
        lt.load(meta_tiny(), [path], dev="cpu", dtype=torch.bfloat16, check=check,
                rename=lambda k: k.removeprefix("model."))  # fmt: skip


def test_load_component_builds_through_the_pack_and_reports_its_phase(tmp_path, monkeypatch):
    q = FakeQwen(
        tmp_path, monkeypatch
    )  # its transformer file: transformer_blocks.0.ff.weight [64, 32]

    def build(component, model):
        with torch.device("meta"):
            m = nn.Module()
            m.transformer_blocks = nn.ModuleList([nn.Module()])
            m.transformer_blocks[0].ff = nn.Linear(32, 64, bias=False)
        return m

    q.pack.module.build = build
    events = []
    ctx = types.SimpleNamespace(
        device="cpu", check=lambda: None, progress=lambda *a: events.append(a)
    )
    m = lt.load_component(pack.ModelRef(q.pack, "turbo", "int8"), "transformer", ctx)
    assert isinstance(m.transformer_blocks[0].ff, lt.Int8Linear)
    assert events == [(1, 1, "loading transformer")]


def test_free_returns_mps_memory():
    if lt.device() != "mps":
        pytest.skip("MPS only")
    lt.free()
    before = torch.mps.driver_allocated_memory()
    x = torch.empty(256 * 2**20, dtype=torch.uint8, device="mps")  # 256 MB
    x.fill_(1)
    torch.mps.synchronize()
    assert torch.mps.driver_allocated_memory() - before >= 200 * 2**20
    del x
    lt.free()
    assert torch.mps.driver_allocated_memory() - before < 64 * 2**20


@pytest.mark.gpu
def test_turbo_transformer_int8_load_peak_and_time(capsys):
    """Loads the real Qwen-Image-2.1-Turbo transformer at int8 (about 7 GB) and reports the numbers."""
    import accelerate
    import diffusers

    import config

    root = config.MODELS / "Qwen-Image-2.1-Turbo" / "transformer"
    if not root.is_dir():
        pytest.skip(f"{root} is not there")
    cls = diffusers.QwenImage21Transformer2DModel
    lt.free()
    base = torch.mps.driver_allocated_memory()
    peak = [0]

    def progress(done, total):
        peak[0] = max(peak[0], torch.mps.driver_allocated_memory())

    t = time.monotonic()
    with accelerate.init_empty_weights(include_buffers=False):
        meta = cls.from_config(cls.load_config(root))
    m = lt.load(
        meta,
        sorted(root.glob("*.safetensors")),
        dev="mps",
        dtype=torch.bfloat16,
        quantize=lambda name: pack.fnmatch.fnmatchcase(name, "transformer_blocks.*"),
        progress=progress,
    )
    torch.mps.synchronize()
    seconds = time.monotonic() - t
    loaded = torch.mps.driver_allocated_memory()
    n_int8 = sum(isinstance(x, lt.Int8Linear) for x in m.modules())
    del m, meta
    lt.free()
    after = torch.mps.driver_allocated_memory()
    rss = resource.getrusage(resource.RUSAGE_SELF).ru_maxrss / 2**30  # bytes on macOS
    with capsys.disabled():
        print(
            f"\nturbo transformer int8: {seconds:.1f} s, {n_int8} Int8Linear, "
            f"loaded {(loaded - base) / 2**30:.2f} GB, peak {(peak[0] - base) / 2**30:.2f} GB "
            f"(MPS driver), max RSS {rss:.2f} GB, after free {(after - base) / 2**30:.2f} GB"
        )
    assert n_int8 > 0 and (after - base) / 2**30 < 1


def test_a_torch_pack_gets_a_residency_manager_on_the_device(tmp_path, monkeypatch):
    import main
    from core.residency import ram_gb

    q = FakeQwen(tmp_path, monkeypatch)
    res, dev, loader = main.residency_for([q.pack])
    assert loader is lt and res.total_gb == pytest.approx(lt.total_gb())
    assert dev == lt.device() and res.budget_gb == pytest.approx(ram_gb() - 12)
    assert res.free is lt.free and res.measure is lt.measure_gb


# --- CUDA (M6): what can run without a GPU; the rest is on the GPU host's checklist (packs/README.md)


def test_staged_reads_every_dtype_like_safetensors_one_tensor_at_a_time(tmp_path):
    from safetensors import safe_open

    path = tmp_path / "mixed.safetensors"
    want = {"w": torch.randn(64, 32).to(torch.bfloat16), "h": torch.randn(3, 5).half(), "f": torch.randn(7),
            "q": torch.randint(-127, 127, (4, 4), dtype=torch.int8), "alpha": torch.tensor(8.0),
            "ids": torch.arange(5), "empty": torch.zeros(0, 3)}  # fmt: skip
    save_file(want, str(path))
    got = dict(lt.staged(path, "cpu"))
    with safe_open(str(path), "pt", device="cpu") as f:
        ref = {k: f.get_tensor(k) for k in f.keys()}  # noqa: SIM118
    assert got.keys() == ref.keys()
    for k, t in ref.items():
        assert got[k].dtype == t.dtype and got[k].shape == t.shape and torch.equal(got[k], t), k
    assert dict(lt.staged(path, "cpu", {"alpha"})).keys() == {"alpha"}


def test_staging_beyond_the_host_ram_budget_fails_with_the_contract_text(tmp_path, monkeypatch):
    import config

    path = tmp_path / "w.safetensors"
    save_file({"w": torch.zeros(1024, 1024)}, str(path))  # 4 MB
    monkeypatch.setattr(lt, "rss_gb", lambda: 9.9)
    monkeypatch.setattr(lt, "host_ram", lambda: {"budget_gb": 9.9, "used_gb": 9.9})
    with pytest.raises(
        lt.RunError, match=rf"^needs 9\.9 GB of host RAM at its peak; host {config.HOST} has 9\.9"
    ):
        list(lt.staged(path, "cpu"))
    monkeypatch.setattr(lt, "host_ram", lambda: {"budget_gb": 10.0, "used_gb": 9.9})
    assert next(lt.staged(path, "cpu"))[1].shape == (1024, 1024)


def test_runner_device_forces_one_that_is_present(monkeypatch):
    import config

    monkeypatch.setattr(config, "DEVICE", "cpu")
    assert lt.device.__wrapped__() == "cpu"
    monkeypatch.setattr(config, "DEVICE", "cuda" if not torch.cuda.is_available() else "nope")
    with pytest.raises(ValueError, match="is not present here"):
        lt.device.__wrapped__()


@pytest.fixture
def fake_cuda(monkeypatch):
    """torch.cuda as an 80 GB card with compute capability 8.9 (no GPU needed); `calls` records sync and frees."""
    calls = []
    monkeypatch.setattr(lt, "device", lambda: "cuda")
    cuda = types.SimpleNamespace(
        mem_get_info=lambda *a: (70 * 2**30, 80 * 2**30), memory_reserved=lambda *a: 6 * 2**30,
        get_device_name=lambda *a: "NVIDIA RTX PRO 6000", get_device_capability=lambda *a: (8, 9),
        synchronize=lambda: calls.append("sync"), empty_cache=lambda: calls.append("empty"),
    )  # fmt: skip
    monkeypatch.setattr(lt.torch, "cuda", cuda)
    lt.context_gb.cache_clear()
    yield calls
    lt.context_gb.cache_clear()


def test_cuda_budget_measure_and_hosts_fields(fake_cuda, monkeypatch):
    from messaging import schemas
    from messaging.hosts import Host

    assert lt.context_gb() == 10.0  # measured once: what the device held when the runner started
    assert lt.measure_gb() == 16.0 and lt.total_gb() == 80.0
    monkeypatch.setattr(lt.torch.version, "cuda", "13.2")
    info = lt.device_info()
    assert info | {"runtime": None} == {"kind": "cuda", "name": "NVIDIA RTX PRO 6000", "memory_total_gb": 80.0,
                                        "memory_used_gb": 10.0, "capability": "8.9", "runtime": None} | (
        {"driver": lt._driver()} if lt._driver() else {})  # fmt: skip
    assert info["runtime"].endswith("· CUDA 13.2")
    value = Host(None, extra=lambda: {"device": info, "host_ram": lt.host_ram(),
                                      "memory": {"budget_gb": 78, "used_gb": 16}}).value()  # fmt: skip
    assert schemas.registry_errors("host", value) == []
    lt.free()
    assert fake_cuda == ["sync", "empty"]  # synchronize, then give the cache back


def test_bf16_loads_as_fp16_below_capability_8(fake_cuda, monkeypatch):
    assert lt.dtype_of("bf16") == torch.bfloat16 and lt.dtype_of("fp32") == torch.float32
    monkeypatch.setattr(lt.torch.cuda, "get_device_capability", lambda *a: (7, 5))
    assert lt.dtype_of("bf16") == torch.float16


def test_int8_kernel_is_used_only_where_torch_has_it():
    assert lt._int8_kernel("cpu")  # the int8 tests above run it
    assert lt._int8_kernel("cuda") == torch._C._dispatch_has_kernel_for_dispatch_key(
        "aten::_weight_int8pack_mm", "CUDA"
    )
