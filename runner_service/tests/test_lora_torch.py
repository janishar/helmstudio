"""The torch LoRA adapter and bake (core/loaders_torch.py), on small synthetic modules on the CPU: adapter == bake,
detach restores the exact base, a strength change is a scale write (no reload), stacks swap on one resident base,
int8 bases, and the loader's bake hook before quantization."""

import types

import pytest

torch = pytest.importorskip("torch")
from make_lora import make_lora
from safetensors.torch import save_file
from torch import nn

from core import loaders_torch as lt
from core.graph import RunError
from core.pack import LoraUse, ModelRef, Pack


class Net(nn.Module):
    def __init__(self) -> None:
        super().__init__()
        self.blocks = nn.ModuleList([nn.Linear(64, 64), nn.Linear(64, 64)])
        self.head = nn.Linear(64, 32)

    def forward(self, x):
        for b in self.blocks:
            x = torch.tanh(b(x))
        return self.head(x)


SHAPES = {"blocks.0": (64, 64), "blocks.1": (64, 64), "head": (64, 32)}
PACK = Pack(
    "t", None, {"family": "t", "id": "t-1", "lora": {"component": "transformer"}}, "torch", None
)


def use(n: int, strength: float, mode: str = "adapter") -> LoraUse:
    return LoraUse(f"{n:064x}", "shared", f"l{n}", f"{n:064x}", 1, strength, mode)


def model(*uses: LoraUse) -> ModelRef:
    return ModelRef(PACK, "v", "bf16", tuple(uses))


@pytest.fixture
def files(tmp_path):
    """Two LoRAs on the Net in two key styles (alpha 2, rank 4: alpha / rank = 0.5) and a ctx that serves them."""
    paths = {}
    for n, style, mods in [(1, "kohya", SHAPES), (2, "comfyui", {"blocks.1": (64, 64)})]:
        paths[f"{n:064x}"] = tmp_path / f"l{n}.safetensors"
        make_lora(paths[f"{n:064x}"], mods, rank=4, style=style, alpha=2, seed=n)
    fetched = []
    ctx = types.SimpleNamespace(
        check=lambda: None, lora_path=lambda u: fetched.append(u.name) or paths[u.sha256]
    )
    return paths, ctx, fetched


def net() -> Net:
    torch.manual_seed(0)
    return Net().eval().requires_grad_(False)


X = torch.randn(5, 64, generator=torch.Generator().manual_seed(1))


def test_adapter_equals_bake_and_detach_restores_the_exact_base(files, tmp_path):
    paths, ctx, _ = files
    m = net()
    base, linears = m(X), [m.blocks[0], m.blocks[1], m.head]
    gb = lt.activate(model(use(1, 0.8), use(2, -1.5)), "transformer", m, ctx)
    assert gb > 0 and isinstance(m.head, lt.LoraLinear)
    adapted = m(X)
    assert not torch.allclose(adapted, base)

    # bake the same rows into a fresh copy through the loader's hook (rename → cast → bake → quantize)
    weights = tmp_path / "net.safetensors"
    save_file(net().state_dict(), str(weights))
    bake = {}
    for u in (use(1, 0.8), use(2, -1.5)):
        for name, (a, b, f) in lt.lora_tensors(model(u), u, paths[u.sha256], SHAPES, "cpu", torch.float32).items():  # fmt: skip
            bake.setdefault(name, []).append((a, b, u.strength * f))
    with torch.device("meta"):
        meta = Net()
    baked = lt.load(meta, [weights], dev="cpu", dtype=torch.float32, bake=bake)
    assert torch.allclose(baked(X), adapted, atol=1e-5)

    lt.activate(model(), "transformer", m, ctx)  # no LoRAs: every adapter detached
    assert [m.blocks[0], m.blocks[1], m.head] == linears  # the very same modules, unwrapped
    assert all(type(x) is nn.Linear for x in linears)
    assert torch.equal(m(X), base)


def test_a_strength_change_is_a_scale_write_and_stacks_swap_on_one_base(files):
    _, ctx, fetched = files
    m = net()
    lt.activate(model(use(1, 1.0)), "transformer", m, ctx)
    *_, s = m.head.adapters[f"{1:064x}"]
    assert s.item() == pytest.approx(0.5)  # strength 1 * alpha 2 / rank 4
    weight = m.head.base.weight.clone()
    lt.activate(model(use(1, 0.2)), "transformer", m, ctx)
    assert fetched == ["l1"] and m.head.adapters[f"{1:064x}"][2] is s  # the same tensor, rewritten
    assert s.item() == pytest.approx(0.1) and torch.equal(m.head.base.weight, weight)
    one, two = model(use(1, 0.2)), model(use(2, 1.0))  # two Model wires, one resident base
    outs = []
    for mm in (one, two, one):
        lt.activate(mm, "transformer", m, ctx)
        outs.append(m(X))
    assert torch.equal(outs[0], outs[2]) and not torch.allclose(outs[0], outs[1])
    assert list(m.blocks[1].adapters) == [f"{1:064x}"] and type(m.head) is lt.LoraLinear
    # strength 0 is the base, exactly
    lt.activate(model(use(1, 0.0)), "transformer", m, ctx)
    base = net()
    assert torch.equal(m(X), base(X))


def test_a_lora_whose_keys_do_not_all_apply_fails_and_names_them(files, tmp_path):
    _, ctx, _ = files
    bad = tmp_path / "bad.safetensors"
    make_lora(
        bad, {"head": (64, 32), "blocks.0": (64, 99)}, style="peft"
    )  # blocks.0's out is 64, not 99
    ctx.lora_path = lambda u: bad
    with pytest.raises(
        RunError, match=r"LoRA l1: 2 of 4 keys do not apply to t: transformer\.blocks\.0"
    ):
        lt.activate(model(use(1, 1.0)), "transformer", net(), ctx)


def test_int8_adapter_is_close_to_the_quantized_bake(tmp_path):
    torch.manual_seed(0)
    lin = nn.Linear(256, 64).to(torch.bfloat16)
    holder = nn.Module()
    holder.q = lt.Int8Linear(lin)
    holder.q.quantize(lin.weight.data)
    p = tmp_path / "l.safetensors"
    make_lora(p, {"q": (256, 64)}, rank=8, style="peft", seed=3)  # no alpha: s = strength
    ctx = types.SimpleNamespace(check=lambda: None, lora_path=lambda u: p)
    x = torch.randn(300, 256, dtype=torch.bfloat16)
    plain = holder.q(x)
    lt.activate(model(use(1, 0.0)), "transformer", holder, ctx)
    assert torch.equal(holder.q(x), plain)  # strength 0 on an int8 base: the base exactly
    lt.activate(model(use(1, 0.7)), "transformer", holder, ctx)
    adapted = holder.q(x).float()
    a, b, _ = lt.lora_tensors(model(use(1, 0.7)), use(1, 0.7), p, {"q": (256, 64)}, "cpu", torch.float32)["q"]  # fmt: skip
    merged = (lin.weight.float() + 0.7 * (b @ a)).to(torch.bfloat16)
    qb = lt.Int8Linear(lin)
    qb.quantize(merged)
    exact = nn.functional.linear(x.float(), merged.float(), lin.bias.float())
    # both are within int8's error of the exact merged layer, so within twice that of each other
    err = (qb(x).float() - exact).abs().max()
    assert (adapted - exact).abs().max() <= 1.5 * err
    assert (adapted - qb(x).float()).abs().max() <= 2.5 * err


def test_the_loader_bakes_before_it_quantizes(files, tmp_path):
    paths, _, _ = files
    weights = tmp_path / "net.safetensors"
    save_file({k: v.to(torch.bfloat16) for k, v in net().state_dict().items()}, str(weights))
    u = use(1, 1.0)
    deltas = lt.lora_tensors(model(u), u, paths[u.sha256], SHAPES, "cpu", torch.float32)
    bake = {n: [(a, b, f)] for n, (a, b, f) in deltas.items()}
    with torch.device("meta"):
        meta = Net()
    q = lt.load(meta, [weights], dev="cpu", dtype=torch.bfloat16, bake=bake,
                quantize=lambda name: name.startswith("blocks"))  # fmt: skip
    assert isinstance(q.blocks[0], lt.Int8Linear)
    ref = net()
    for name, (a, b, f) in deltas.items():
        lin = ref.get_submodule(name)
        lin.weight += f * (b @ a)
    rel = (q(X.to(torch.bfloat16)).float() - ref(X)).abs().max() / ref(X).abs().max()
    assert rel < 0.05  # bf16 + int8 of the merged weights, not of the base
    with torch.device("meta"):
        meta = Net()
    with pytest.raises(KeyError, match="without a weight: nowhere"):
        lt.load(meta, [weights], dev="cpu", dtype=torch.bfloat16, bake={"nowhere": []})


def test_keys_the_family_ignores_are_ignored_at_load(tmp_path):
    from safetensors.torch import load_file

    p = tmp_path / "te.safetensors"
    make_lora(p, SHAPES, style="kohya")
    save_file(
        load_file(str(p)) | {"lora_te1_text_model.q.lora_down.weight": torch.zeros(4, 8)}, str(p)
    )
    spec = PACK.spec | {"lora": {"component": "transformer", "ignore": ["lora_te1_*"]}}
    m = ModelRef(Pack("t", None, spec, "torch", None), "v", "bf16")
    assert set(lt.lora_tensors(m, use(1, 1.0), p, SHAPES, "cpu", torch.float32)) == set(SHAPES)
    with pytest.raises(
        RunError, match="lora_te1_text_model.q.lora_down.weight is not a LoRA weight"
    ):
        lt.lora_tensors(
            model(), use(1, 1.0), p, SHAPES, "cpu", torch.float32
        )  # no ignore globs: refused
