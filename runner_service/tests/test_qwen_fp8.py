"""The qwen_image pack's FP8 variants (API 1.33, NVIDIA only) on the CPU with tiny synthetic files laid out as
unsloth's: the torchao key names, the format check, a tiny transformer through the core loader, and the variants'
models entry. The real files' layout is in docs/reference-notes/nvidia-quantized.md."""

import types

import pytest

torch = pytest.importorskip("torch")
import accelerate
import diffusers
from safetensors.torch import save_file

import config
from core import loaders_torch as lt
from core import models
from core import pack as packs
from core.graph import RunError
from messaging import schemas
from packs.qwen_image import torch as qi

BF16 = {
    "img_in",
    "proj_out",
    "time_text_embed.timestep_embedder.linear_1",
}  # kept bf16 in the real file
TRANSFORMER = "unsloth_prequant_transformer_state_dict_v1"


def e4m3(w: torch.Tensor) -> tuple[torch.Tensor, torch.Tensor]:
    scale = w.abs().amax(1, keepdim=True).float() / lt.E4M3_MAX
    return (w.float() / scale).to(torch.float8_e4m3fn), scale


def test_rename_maps_torchao_keys_to_the_core_fp8_linear():
    assert qi.rename("transformer", "transformer_blocks.0.attn.to_q._weight_qdata") == (
        "transformer_blocks.0.attn.to_q.weight"
    )
    assert qi.rename("transformer", "modulation.1._weight_scale") == "modulation.1.scale"
    assert qi.rename("text_encoder", "lm_head.weight") == "lm_head.weight"


def test_an_fp8_file_of_another_format_is_refused(tmp_path):
    path = tmp_path / "t.safetensors"
    model = types.SimpleNamespace(precision_of=lambda c: "fp8", files=lambda c: [path])
    for fmt in ("unsloth_prequant_transformer_state_dict_v2", None):
        save_file({"a": torch.zeros(1)}, str(path), metadata={"unsloth_format": fmt} if fmt else None)
        with pytest.raises(RunError, match="not a file this pack reads"):
            qi.tensor_hook("transformer", model)
    save_file({"a": torch.zeros(1)}, str(path), metadata={"unsloth_format": TRANSFORMER})
    assert qi.tensor_hook("transformer", model) is None
    with pytest.raises(RunError):  # the text encoder's own format, not the transformer's
        qi.tensor_hook("text_encoder", model)
    bf16 = types.SimpleNamespace(precision_of=lambda c: "int8", files=lambda c: 1 / 0)
    assert qi.tensor_hook("transformer", bf16) is None  # the official files: nothing read


def test_a_tiny_transformer_loads_from_an_unsloth_layout_and_runs_as_its_dequantized_self(tmp_path):
    cls = diffusers.QwenImage21Transformer2DModel
    cfg = {"in_channels": 16, "out_channels": 16, "num_layers": 1, "attention_head_dim": 16,
           "num_attention_heads": 2, "context_in_dim": 32, "axes_dims_rope": (4, 6, 6)}  # fmt: skip
    torch.manual_seed(0)
    ref = cls(**cfg).to(torch.bfloat16)
    linears = {n for n, m in ref.named_modules() if isinstance(m, torch.nn.Linear)} - BF16
    stored, dequant = {}, {}
    for k, v in ref.state_dict().items():
        module = k.rpartition(".")[0]
        if module in linears and k.endswith(".weight"):
            q, s = e4m3(v)
            stored |= {f"{module}._weight_qdata": q, f"{module}._weight_scale": s}
            dequant[k] = (q.float() * s).to(torch.bfloat16)
        else:
            stored[k] = dequant[k] = v
    path = tmp_path / "Qwen-Image-2.1-FP8.safetensors"
    save_file(stored, str(path), metadata={"unsloth_format": TRANSFORMER})
    ref.load_state_dict(dequant)
    with accelerate.init_empty_weights(include_buffers=False):
        meta = cls(**cfg)
    got = lt.load(meta, [path], dev="cpu", dtype=torch.bfloat16, rename=lambda k: qi.rename("transformer", k))
    fp8 = {n for n, m in got.named_modules() if isinstance(m, lt.Fp8Linear)}
    assert fp8 == linears and "transformer_blocks.0.attn.to_q" in fp8
    assert got.img_in.weight.dtype == torch.bfloat16  # a bf16 Linear stays a plain one
    h = w = 4
    x = torch.randn(1, h * w, 16, dtype=torch.bfloat16)
    text = torch.randn(1, 3, 32, dtype=torch.bfloat16)
    mask = torch.arange(3 + h * w // 4)[None] >= 3  # the text, then a slot per 2x2 latents
    with torch.no_grad():
        a, b = (m(x, text, torch.tensor([0.5], dtype=torch.bfloat16), [[(1, h, w)]], mask, return_dict=False)[0]
                for m in (got, ref))  # fmt: skip
    assert torch.allclose(a.float(), b.float(), atol=1e-2, rtol=1e-2)


def test_the_fp8_variants_publish_on_cuda_only_and_turbo_fp8_shares_from_base_fp8(tmp_path, monkeypatch):
    monkeypatch.setattr(config, "MODELS", tmp_path)
    pack = next(p for p, _ in packs.for_env("torch") if p.spec["id"] == "qwen-image-2.1")
    monkeypatch.setattr(models, "device", lambda p: "mps")
    assert [v["id"] for v in models.entry(pack)["variants"]] == ["base", "turbo"]
    monkeypatch.setattr(models, "device", lambda p: "cuda")
    entry = models.entry(pack)
    v = {x["id"]: x for x in entry["variants"]}
    assert list(v) == ["base", "turbo", "base-fp8", "turbo-fp8"]
    assert v["base-fp8"]["precisions"] == ["fp8"] and v["base"]["precisions"] == ["bf16", "int8"]
    comps = {c["component"]: (c["from_variant"], c["precisions"]) for c in v["turbo-fp8"]["components"]}
    assert comps == {"transformer": ("turbo-fp8", ["fp8"]), "text_encoder": ("base-fp8", ["fp8"]),
                     "vae": ("base-fp8", ["bf16"])}  # fmt: skip
    ref = packs.ModelRef(pack, "turbo-fp8", "fp8")
    assert ref.key("text_encoder") == packs.ModelRef(pack, "base-fp8", "fp8").key("text_encoder")
    te = tmp_path / "Qwen-Image-2.1-FP8/Qwen-Image-2.1-text_encoder-FP8.safetensors"
    te.parent.mkdir()
    te.write_bytes(b"")
    assert ref.files("text_encoder") == [te] and ref.folder("vae") == te.parent
    value = {"schema_version": 1, "host": "t", "env": "torch", "published_at": "2026-11-21T10:00:00Z",
             "published_by": "t", "models": [entry], "licences": models.licences([pack])}  # fmt: skip
    assert schemas.registry_errors("models", value) == []
