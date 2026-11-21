"""The torch loader (env torch only): Int8Linear, the per-tensor hook chain, direct-to-device loading.
`pytest -m gpu` also loads the real Qwen-Image-2.1-Turbo transformer at int8 and prints its load time and peak."""

import time
import types

import pytest
from conftest import FakeQwen

torch = pytest.importorskip("torch")
import torch.nn.functional as F
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
    dev = lt.device()  # mps or cuda (M6): the device's own allocation counter
    mem = torch.mps.driver_allocated_memory if dev == "mps" else torch.cuda.memory_reserved
    lt.free()
    base = mem()
    peak, rss_peak = [0], [0.0]

    def progress(done, total):
        peak[0] = max(peak[0], mem())
        rss_peak[0] = max(rss_peak[0], lt.rss_gb() or 0)

    files = sorted(root.glob("*.safetensors"))
    t = time.monotonic()
    with accelerate.init_empty_weights(include_buffers=False):
        meta = cls.from_config(cls.load_config(root))
    m = lt.load(
        meta,
        files,
        dev=dev,
        dtype=torch.bfloat16,
        quantize=lambda name: pack.fnmatch.fnmatchcase(name, "transformer_blocks.*"),
        progress=progress,
    )
    getattr(torch, dev).synchronize()
    seconds = time.monotonic() - t
    loaded = mem()
    n_int8 = sum(isinstance(x, lt.Int8Linear) for x in m.modules())
    del m, meta
    lt.free()
    after = mem()
    pinned = ""
    if dev == "cuda":  # the pinned staging blocks go back at free() (plan 6.3)
        st = torch.cuda.memory.host_memory_stats()
        pinned = f", pinned after free {st.get('reserved_bytes.current', 0) / 2**30:.2f} GB"
    gbps = sum(f.stat().st_size for f in files) / 2**30 / seconds
    with capsys.disabled():
        print(
            f"\nturbo transformer int8 on {dev}: {seconds:.1f} s ({gbps:.2f} GB/s read), {n_int8} Int8Linear, "
            f"loaded {(loaded - base) / 2**30:.2f} GB, peak {(peak[0] - base) / 2**30:.2f} GB, "
            f"RSS peak {rss_peak[0]:.2f} GB, after free {(after - base) / 2**30:.2f} GB{pinned}"
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


def test_pinned_staging_counts_the_allocators_power_of_two():
    """A pinned block is rounded up to a power of two (torch's CachingHostAllocator): the host-RAM check counts that,
    so Qwen3-VL's 1.24 GB embed_tokens counts 2 GiB; unpinned (CPU, MPS) counts its size."""
    size = int(1.24 * 2**30)
    assert lt.staging_gb(size, True) == 2.0 and lt.staging_gb(size, False) == size / 2**30
    assert lt.staging_gb(2**30, True) == 1.0 and lt.staging_gb(2**30 + 1, True) == 2.0


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


# --- API 1.33: community FP8 and int8 ConvRot weights


def e4m3(w: torch.Tensor) -> tuple[torch.Tensor, torch.Tensor]:
    """Per-row FP8 as the community files store it: qweight e4m3 [out, in], scale fp32 [out, 1]."""
    scale = w.abs().amax(1, keepdim=True).float() / lt.E4M3_MAX
    return (w.float() / scale).to(torch.float8_e4m3fn), scale


def test_fp8_linear_dequantizes_to_its_bf16_reference():
    torch.manual_seed(0)
    linear = nn.Linear(64, 32, bias=True)
    m = lt.Fp8Linear(linear)
    m.qweight, m.scale = e4m3(linear.weight.data)
    x = torch.randn(5, 64, dtype=torch.bfloat16)
    ref = F.linear(
        x, (m.qweight.float() * m.scale).to(torch.bfloat16), linear.bias.to(torch.bfloat16)
    )
    got = m(x)
    assert got.dtype == torch.bfloat16 and ((got - ref).abs().max() / ref.abs().max()) < 0.01
    exact = linear(x.float())  # e4m3 keeps 3 mantissa bits: a few percent from the float weights
    assert ((got.float() - exact).abs().max() / exact.abs().max()) < 0.08


def test_fp8_linear_takes_scaled_mm_only_for_inputs_of_many_rows():
    from types import SimpleNamespace

    m = lt.Fp8Linear(nn.Linear(64, 32))
    cuda = lambda rows: SimpleNamespace(is_cuda=True, numel=lambda: rows * 64)
    assert m.takes_scaled_mm(cuda(lt.FP8_MIN_ROWS)) and m.takes_scaled_mm(cuda(4096))
    assert not m.takes_scaled_mm(cuda(2))  # adaLN modulation: one row per image, dequantized
    assert not m.takes_scaled_mm(torch.zeros(4096, 64))  # not CUDA


def test_load_makes_fp8_linears_from_the_headers_and_keeps_them_as_stored(tmp_path):
    torch.manual_seed(0)
    ref = Tiny()
    q, s = e4m3(ref.blocks[0].weight.data)
    path = tmp_path / "fp8.safetensors"
    save_file({"blocks.0.weight": q, "blocks.0.scale": s, "blocks.0.bias": ref.blocks[0].bias.data,
               "head.weight": e4m3(ref.head.weight.data)[0], "head.bias": ref.head.bias.data}, str(path))  # fmt: skip
    m = lt.load(meta_tiny(), [path], dev="cpu", dtype=torch.bfloat16, quantize=lambda n: True)
    b, h = m.blocks[0], m.head
    assert isinstance(b, lt.Fp8Linear) and isinstance(
        h, lt.Fp8Linear
    )  # header-driven, never Int8Linear
    assert (
        b.qweight.dtype == torch.float8_e4m3fn
        and b.scale.dtype == torch.float32
        and torch.equal(b.scale, s)
    )
    assert (
        torch.equal(h.scale, torch.ones(4, 1)) and b.bias.dtype == torch.bfloat16
    )  # a plain cast: ones
    assert "blocks.0" in lt.linears(m)
    with pytest.raises(lt.RunError, match="LoRA bake is not available on fp8"):
        lt.load(meta_tiny(), [path], dev="cpu", dtype=torch.bfloat16, bake={"blocks.0": []})


def test_a_bake_into_a_convrot_linear_fails_the_run(tmp_path):
    path = tmp_path / "w.safetensors"
    save_file({"blocks.0.weight": torch.zeros(64, 32, dtype=torch.int8)}, str(path))
    kw = {"dev": "cpu", "dtype": torch.bfloat16, "quantize": lambda n: n == "blocks.0"}
    with pytest.raises(lt.RunError, match="not available on int8 ConvRot weights \\(blocks.0\\)"):
        lt.load(meta_tiny(), [path], rotated=lambda n: 4, bake={"blocks.0": []}, **kw)
    # unrotated, the bake goes on (here past the guard to the missing head)
    with pytest.raises(KeyError, match="not in the checkpoint"):
        lt.load(meta_tiny(), [path], bake={"blocks.0": []}, **kw)


def test_fp8_weights_count_one_byte_and_a_scale_per_row(tmp_path):
    q, s = e4m3(torch.randn(64, 32))
    save_file({"l.weight": q, "l.scale": s, "n.weight": torch.ones(64, dtype=torch.bfloat16)}, str(tmp_path / "f.safetensors"))  # fmt: skip
    p = pack.Pack(
        "t",
        tmp_path,
        {"components": {"c": {"files": "*.safetensors", "dtype": "bf16"}}},
        "torch",
        None,
    )
    gb, _ = pack.size_gb(p, ("t", "v", "c", "fp8"), (tmp_path / "f.safetensors",))
    assert (
        round(gb * 2**30) == 64 * 32 + 64 * 4 + 64 * 2 + 64 * 2
    )  # weight + its row scale; the scale key; the norm
    p.module = type("M", (), {"DENSE": ("l.",)})  # the pack keeps l dense: its F8 weight loads bf16
    pack._sizes.clear()
    gb, _ = pack.size_gb(p, ("t", "v", "c", "fp8"), (tmp_path / "f.safetensors",))
    assert round(gb * 2**30) == 64 * 32 * 2 + 64 * 2 + 64 * 2


def test_convrot_rotating_w_offline_and_x_at_run_time_gives_x_wt():
    torch.manual_seed(0)
    h = lt.hadamard(256, "cpu", torch.float64)
    assert torch.allclose(h @ h, torch.eye(256, dtype=torch.float64)) and torch.equal(h, h.T)
    w, x = torch.randn(48, 512, dtype=torch.float64), torch.randn(3, 512, dtype=torch.float64)
    w_rot = (w.view(48, 2, 256) @ h).view(48, 512)  # what the community file stores (before int8)
    assert torch.allclose((x.view(3, 2, 256) @ h).view(3, 512) @ w_rot.T, x @ w.T)
    linear = nn.Linear(512, 48, bias=False)
    m = lt.Int8Linear(linear, rot_group=256)
    m.quantize(w_rot.float())
    xb = x.to(torch.bfloat16)
    ref = xb.double() @ w.T
    assert ((m(xb).double() - ref).abs().max() / ref.abs().max()) < 0.03
    with pytest.raises(ValueError, match="not a multiple of 256"):
        lt.Int8Linear(nn.Linear(100, 4), rot_group=256)
