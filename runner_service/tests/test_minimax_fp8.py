"""The minimax_h3_image pack's base-fp8 variant (API 1.33, NVIDIA only) on the CPU with tiny synthetic files laid out
as the community ones: the curve table, the renames, a tiny DiT in unsloth's layout through the core loader against
the dense model it was made from, a ConvRot text-encoder layer, and the models entry per device. The real files'
layout is in docs/reference-notes/nvidia-quantized.md; the curve-vs-dense check on the real weights is the last test
(skipped without them)."""

import json
import os
import types

import pytest
from conftest import e4m3

torch = pytest.importorskip("torch")
import accelerate
import diffusers
from safetensors import safe_open
from safetensors.torch import save_file

import config
from core import loaders_torch as lt
from core import models
from core import pack as packs
from core.graph import RunError
from packs.minimax_h3_image import torch as mm

FP8 = lambda files: types.SimpleNamespace(precision_of=lambda c: "fp8", files=lambda c: files)
CFG = {"num_attention_heads": 2, "attention_head_dim": 16, "hidden_size": 32, "num_layers": 1,
       "num_refiner_layers": 1, "ffn_dim": 64, "in_channels": 4, "text_dim": 24, "freq_dim": 16,
       "time_embed_hidden_dim": 16, "time_embed_dim": mm.CURVE[1], "rope_freq_dim": 2, "num_pdd_heads": 2}  # fmt: skip


def test_the_curve_table_is_read_linearly_at_t():
    ct = mm.CurveTable()
    ct.table = torch.arange(mm.CURVE[0], dtype=torch.float32)[:, None].repeat(1, 8)  # row k holds k
    t = torch.tensor([0.0, 1.0, 0.5, 2.5 / 1024, -1.0, 2.0])
    assert ct(t)[:, 0].tolist() == [0.0, 1024.0, 512.0, 2.5, 0.0, 1024.0]


def test_renames_of_the_community_files():
    te = lambda k: mm.rename("text_encoder", k)
    assert te("model.layers.3.self_attn.q_proj.weight") == "model.language_model.layers.3.self_attn.q_proj.qweight"
    assert te("model.layers.3.mlp.down_proj.weight_scale") == "model.language_model.layers.3.mlp.down_proj.scale"
    assert te("model.layers.3.input_layernorm.weight") == "model.language_model.layers.3.input_layernorm.weight"
    assert te("model.layers.3.mlp.down_proj.comfy_quant") is None
    assert te("model.embed_tokens.weight") == "model.language_model.embed_tokens.weight"
    assert te("visual.blocks.0.attn.qkv.weight") == "model.visual.blocks.0.attn.qkv.weight"
    official = "model.language_model.layers.3.self_attn.q_proj.weight"
    assert te(official) == official and te("model.visual.merger.norm.weight") == "model.visual.merger.norm.weight"
    dit = lambda k: mm.rename("transformer", k)
    assert dit("transformer_blocks.0.attn.to_q._weight_qdata") == "transformer_blocks.0.attn.to_q.weight"
    assert dit("transformer_blocks.0.ff.net.2._weight_scale") == "transformer_blocks.0.ff.net.2.scale"
    assert dit("context_embedder._weight_qdata") == "context_embedder.weight"
    assert dit("token_refiner.refiner_blocks.0.attn.to_q._weight_scale") is None  # read by the hook
    assert dit("time_embedder.table") == "time_embedder.table"


def unsloth_file(ref, path) -> None:
    """ref's state in unsloth's layout: FP8 Linears in the blocks, refiner and context_embedder; the time MLP as the
    curve table (silu(time MLP) at the 1025 grid points, so at a grid t the curve form equals ref exactly)."""
    stored = {}
    quantized = {n for n, m in ref.named_modules() if isinstance(m, torch.nn.Linear)
                 and n.startswith(("transformer_blocks.", "token_refiner.", "context_embedder"))
                 and "adaln_proj" not in n}  # fmt: skip
    for k, v in ref.state_dict().items():
        module = k.rpartition(".")[0]
        if module in quantized and k.endswith(".weight"):
            q, s = e4m3(v)
            stored |= {f"{module}._weight_qdata": q, f"{module}._weight_scale": s}
            v.copy_((q.float() * s).to(v.dtype))  # ref runs as its dequantized self
        elif module in quantized or "norm" in module.rpartition(".")[2]:  # bf16 in the real file
            v.copy_(v.bfloat16())
            stored[k] = v.bfloat16()
        elif not module.startswith(("time_embedder", "rope")):
            stored[k] = v.clone()
    grid = torch.linspace(0, 1, mm.CURVE[0])
    with torch.no_grad():
        stored["time_embedder.table"] = torch.nn.functional.silu(ref.time_embedder(ref.time_proj(grid)))
    save_file(stored, str(path), metadata={"unsloth_format": mm.FP8_FORMAT})


def test_a_tiny_dit_in_unsloth_layout_runs_as_the_dense_model_it_was_made_from(tmp_path):
    cls = diffusers.MiniMaxH3ImageTransformer2DModel
    torch.manual_seed(0)
    ref = cls(**CFG)  # float32 throughout
    with torch.no_grad():  # a time signal that matters (at init the time MLP's output is ~0.05)
        ref.time_embedder.linear_2.weight.mul_(40)
    path = tmp_path / "MiniMax-H3-Image-FP8.safetensors"
    unsloth_file(ref, path)
    with accelerate.init_empty_weights(include_buffers=False):
        meta = cls(**CFG)
    mm.curve_form(meta)
    got = lt.load(meta, [path], dev="cpu", dtype=None, rename=lambda k: mm.rename("transformer", k),
                  hook=mm.tensor_hook("transformer", FP8([path])))  # fmt: skip
    assert isinstance(got.transformer_blocks[0].attn.to_q, lt.Fp8Linear)
    assert isinstance(got.transformer_blocks[0].ff.net[2], lt.Fp8Linear)
    for dense in (got.context_embedder, got.token_refiner.refiner_blocks[0].attn.to_q):
        assert type(dense) is mm.Dense and dense.weight.dtype == torch.bfloat16
    assert got.transformer_blocks[0].adaln_proj.linear.in_features == 8
    text, image = 3, 4
    seq = text + image
    t = torch.tensor([256 / 1024, 1.0])  # two noise levels, both on the grid: the text (as refs) at 1
    kw = {"timestep": t, "timestep_indices": torch.tensor([1] * text + [0] * image),
          "token_tags": torch.tensor([1] * text + [0] * image), "position_ids": torch.zeros(seq, 3, dtype=torch.long),
          "image_indices": torch.arange(text, seq), "text_indices": torch.arange(text), "pdd_step_index": 1,
          "return_dict": False}  # fmt: skip
    x, cond = torch.randn(1, image, 16), torch.randn(1, text, 24)
    with torch.no_grad():
        a, b = (m(x, cond, **kw)[0] for m in (got, ref))
    # got runs the block stack in bf16 (its dense context_embedder sets the dtype), ref in float32
    assert (a.float() - b).norm() / b.norm() < 0.03


def test_the_dit_file_must_be_unsloths_format(tmp_path):
    path = tmp_path / "t.safetensors"
    save_file({"a": torch.zeros(1)}, str(path), metadata={"unsloth_format": "v2"})
    with pytest.raises(RunError, match="not a file this pack reads"):
        mm.tensor_hook("transformer", FP8([path]))


def comfy_layer(tmp_path, tag: dict, meta: dict | None = None):
    """One ConvRot Linear in the Comfy layout: W rotated per 256 by H, int8 per row, scale [out, 1]."""
    torch.manual_seed(0)
    w = torch.randn(8, 512, dtype=torch.float64)
    h = lt.hadamard(256, "cpu", torch.float64)
    w_rot = (w.view(8, 2, 256) @ h).view(8, 512)
    scale = w_rot.abs().amax(1, keepdim=True) / 127
    name = "model.layers.0.mlp.down_proj"
    path = tmp_path / "te.safetensors"
    save_file({f"{name}.weight": (w_rot / scale).round().to(torch.int8), f"{name}.weight_scale": scale.float(),
               f"{name}.comfy_quant": torch.tensor(list(json.dumps(tag).encode()), dtype=torch.uint8)},
              str(path), metadata=meta or {"minimax_h3_te": json.dumps(mm.CONVROT_TE)})  # fmt: skip
    return w, path


def test_a_convrot_text_encoder_layer_loads_and_runs_as_its_unrotated_self(tmp_path):
    tag = {"format": "int8_tensorwise", "convrot": True, "convrot_groupsize": 256}
    w, path = comfy_layer(tmp_path, tag)
    model = FP8([path])
    assert mm.tensor_hook("text_encoder", model) is None  # the checks pass; no tensor changes
    module = torch.nn.Module()
    module.model = torch.nn.Module()
    module.model.language_model = torch.nn.Module()
    with accelerate.init_empty_weights():
        layer = torch.nn.Module()
        layer.mlp = torch.nn.Module()
        layer.mlp.down_proj = torch.nn.Linear(512, 8, bias=False)
    module.model.language_model.layers = torch.nn.ModuleList([layer])
    got = lt.load(module, [path], dev="cpu", dtype=None, quantize=lambda n: True,
                  rotated=mm.rotated("text_encoder", model), rename=lambda k: mm.rename("text_encoder", k))  # fmt: skip
    proj = got.model.language_model.layers[0].mlp.down_proj
    assert isinstance(proj, lt.Int8Linear) and proj.rot_group == 256
    x = torch.randn(3, 512)
    want = x.double() @ w.T
    assert (proj(x.bfloat16()).double() - want).norm() / want.norm() < 0.02
    w, path = comfy_layer(tmp_path, tag | {"convrot_groupsize": 128})
    with pytest.raises(RunError, match="int8 ConvRot"):
        mm.tensor_hook("text_encoder", FP8([path]))


def test_base_fp8_publishes_on_cuda_only_and_base_everywhere(tmp_path, monkeypatch):
    monkeypatch.setattr(config, "MODELS", tmp_path)
    pack = next(p for p, _ in packs.for_env("torch") if p.spec["id"] == "minimax-h3-image")
    monkeypatch.setattr(models, "device", lambda p: "mps")
    assert [v["id"] for v in models.entry(pack)["variants"]] == ["base"]
    monkeypatch.setattr(models, "device", lambda p: "cuda")
    base, v = models.entry(pack)["variants"]
    assert base["id"] == "base" and v["id"] == "base-fp8" and v["precisions"] == ["fp8"]
    comps = {c["component"]: c["precisions"] for c in v["components"]}
    # API 1.36: the enhancer is an optional add-on, its own entry, in no variant's components
    assert comps == {"text_encoder": ["int8"], "transformer": ["fp8"], "vae": ["fp32"]}
    (addon,) = models.entry(pack)["addons"]
    assert (addon["component"], addon["dir"], addon["presence"]) == (
        "enhancer",
        "MiniMax-H3-Image-PE",
        "missing",
    )
    ref = packs.ModelRef(pack, "base-fp8", "fp8")
    assert mm.community(ref) and not mm.community(packs.ModelRef(pack, "base", "int8"))
    assert ref.precision_of("text_encoder") == "int8" and mm.rotated("text_encoder", ref)("x") == 256
    assert mm.rotated("text_encoder", packs.ModelRef(pack, "base", "int8"))("x") is None


FP8_FILE = packs.models_root() / "MiniMax-H3-Image-FP8/MiniMax-H3-Image-FP8.safetensors"
DENSE_DIR = packs.models_root() / "MiniMax-H3-Image/transformer_diffusers"


@pytest.mark.skipif(not (FP8_FILE.is_file() and DENSE_DIR.is_dir()), reason="needs the official and the FP8 DiT")
def test_the_curve_form_equals_the_dense_adaln_at_the_sampling_timesteps():
    """Block 0's and norm_out's adaLN: the official W·silu(time MLP(t)) against unsloth's W8·curve(t) at the six
    sampling timesteps and t = 1 (references). Measured once by range reads (2026-11-21): relative error <= 9e-4;
    reading the table at 1 - t instead is 2-37 % off."""
    from diffusers.models.embeddings import Timesteps

    weight_map = json.loads((DENSE_DIR / "diffusion_pytorch_model.safetensors.index.json").read_text())["weight_map"]

    def dense(key):
        with safe_open(DENSE_DIR / weight_map[key], "pt") as f:
            return f.get_tensor(key).float()

    with safe_open(FP8_FILE, "pt") as f:
        curve = {k: f.get_tensor(k) for k in packs.header(FP8_FILE) if k.startswith(("time_embedder.", "transformer_blocks.0.adaln", "norm_out.linear"))}  # fmt: skip
    sigmas = json.loads((DENSE_DIR.parent / "modular_model_index.json").read_text())["sample_sigmas"][:-1]
    t = torch.tensor([1 - s for s in sigmas] + [1.0])
    temb = Timesteps(num_channels=256, flip_sin_to_cos=True, downscale_freq_shift=0)(t)
    mlp = [dense(f"time_embedder.linear_{i}.{a}") for i in (1, 2) for a in ("weight", "bias")]
    temb = torch.nn.functional.linear(torch.nn.functional.silu(torch.nn.functional.linear(temb, *mlp[:2])), *mlp[2:])
    table = mm.CurveTable()
    table.table = curve["time_embedder.table"]
    for name in ("transformer_blocks.0.adaln_proj.linear", "norm_out.linear"):
        want = torch.nn.functional.linear(
            torch.nn.functional.silu(temb), dense(f"{name}.weight"), dense(f"{name}.bias")
        )
        got = torch.nn.functional.linear(table(t), curve[f"{name}.weight"], curve[f"{name}.bias"])
        assert ((got - want).norm(dim=1) / want.norm(dim=1)).max() < 3e-3, name


TE_DIR = packs.models_root() / "MiniMax-H3-Image/text_encoder"
COMFY_TE = (
    "https://huggingface.co/Comfy-Org/MiniMax-H3-Image/resolve/89c635085f539d03ad35485ed542c5a286e3c6d6/"
    "text_encoders/qwen3vl_32b_minimax_h3_int8_convrot.safetensors"
)


@pytest.mark.skipif(
    not (TE_DIR.is_dir() and os.environ.get("RUNNER_NET_TESTS")),
    reason="needs the official text encoder and RUNNER_NET_TESTS=1 (range reads 5 MB from the Hub)",
)
def test_a_comfy_convrot_layer_dequantizes_to_the_official_weight():
    """Our hadamard() against the real file: layer 0's k_proj, W = (q·s) @ H per group of 256, is the official bf16
    weight (cosine > 0.999); a wrong H gives about 0."""
    import struct
    import urllib.request

    def read(a, b):
        req = urllib.request.Request(COMFY_TE, headers={"Range": f"bytes={a}-{b - 1}"})
        with urllib.request.urlopen(req, timeout=60) as r:
            return r.read()

    n = struct.unpack("<Q", read(0, 8))[0]
    head = json.loads(read(8, 8 + n))

    def get(key, dtype):
        a, b = head[key]["data_offsets"]
        data = bytearray(read(8 + n + a, 8 + n + b))
        return torch.frombuffer(data, dtype=dtype).reshape(head[key]["shape"])

    name = "model.layers.0.self_attn.k_proj"
    q, s = get(f"{name}.weight", torch.int8).float(), get(f"{name}.weight_scale", torch.float32)
    w = (q * s).reshape(q.shape[0], -1, 256) @ lt.hadamard(256, "cpu", torch.float32)
    want_key = "model.language_model.layers.0.self_attn.k_proj.weight"
    index = json.loads((TE_DIR / "model.safetensors.index.json").read_text())
    shard = index["weight_map"][want_key]
    with safe_open(TE_DIR / shard, "pt") as f:
        want = f.get_tensor(want_key).float()
    assert torch.nn.functional.cosine_similarity(w.flatten(), want.flatten(), dim=0) > 0.999
