"""The ltx pack's torch runtime (the NVIDIA variants, packs/ltx/torch.py) on the CPU with tiny random weights: the
five workflows end to end to an MP4, the community int8 ConvRot and FP8 layouts through the core loader, the
variants each device publishes, the refusals (LoRA rows, beats) and the distill swap; and, where envs/mlx is set
up, the port against mlx.py on the same tiny official-named weights (CUDA bytes never match the MLX goldens, so this
is what ties the two ports together)."""

import copy
import gzip
import json
import math
import os
import subprocess
import types
from dataclasses import replace
from pathlib import Path

import pytest

torch = pytest.importorskip("torch")
import numpy as np
from PIL import Image
from safetensors import safe_open
from safetensors.torch import save_file

import config
from core import loaders_torch as lt
from core import media, models
from core import pack as packs
from core.graph import RunError
from messaging import schemas
from packs.ltx import torch as ltx

ROOT = Path(__file__).resolve().parents[1]
FIX = Path(__file__).parent / "fixtures/ltx_nvidia_headers"
OFFICIAL = json.loads((FIX / "official-configs.json").read_text())["files"]
DIT = json.loads(ltx.CONFIG.read_text())
MLX_PYTHON = ROOT / "envs/mlx/.venv/bin/python"
WORDS = "a red fox runs through snow at dawn the camera follows blurry"


def tiny_dit() -> dict:
    """The LTX-2.5 transformer config at 2 layers, 2 heads of 8 (connector: 1 layer, 8 registers)."""
    meta = copy.deepcopy(DIT)
    meta["config"]["transformer"] |= {
        "num_layers": 2, "num_attention_heads": 2, "attention_head_dim": 8, "cross_attention_dim": 16,
        "audio_num_attention_heads": 2, "audio_attention_head_dim": 8, "audio_cross_attention_dim": 16,
        "connector_num_attention_heads": 2, "connector_attention_head_dim": 8, "audio_connector_num_attention_heads": 2,
        "audio_connector_attention_head_dim": 8, "connector_num_layers": 1, "connector_num_learnable_registers": 8,
    }  # fmt: skip
    return meta


def official(name: str) -> dict:
    return copy.deepcopy(next(v for k, v in OFFICIAL.items() if name in k))


def randomized(module, dtype=torch.bfloat16):
    """Random parameters (some ltx-core modules start from torch.empty), buffers as built; eval, no grad."""
    torch.manual_seed(0)
    for p in module.parameters():
        p.data.normal_(0, 0.05)
    return module.to(dtype).eval().requires_grad_(False)


def tiny_gemma():
    """A 2-layer Gemma 4 unified with the real config's layer types and a word-level tokenizer."""
    from ltx_core.text_encoders.gemma import LTXGemmaTextEncoder
    from ltx_core.text_encoders.gemma.encoders.encoder_configurator import (
        _create_and_populate_gemma4_unified,
    )
    from ltx_core.text_encoders.gemma.gemma_assets import GemmaAssets, build_gemma_hf_config
    from ltx_core.text_encoders.gemma.tokenizer import LTXGemmaTokenizer, PaddingSide
    from tokenizers import Tokenizer, pre_tokenizers
    from tokenizers import models as tmodels
    from transformers import AutoModelForImageTextToText, PreTrainedTokenizerFast

    with gzip.open(FIX / "ddb-text-encoder-int8.json.gz") as f:
        raw = json.loads(json.load(f)["metadata"]["gemma_config"])
    raw["text_config"] |= {"hidden_size": 64, "intermediate_size": 128, "num_attention_heads": 2,
                           "num_key_value_heads": 1, "head_dim": 32, "global_head_dim": 64, "num_hidden_layers": 2,
                           "layer_types": ["sliding_attention", "full_attention"]}  # fmt: skip
    assets = GemmaAssets(
        source="tiny", config_dict=raw, tokenizer_json=b"", sidecars={}, weight_paths=()
    )
    torch.manual_seed(0)
    model = AutoModelForImageTextToText.from_config(build_gemma_hf_config(assets))
    words = ["<pad>", "<eos>", "<bos>", "<unk>", *dict.fromkeys(WORDS.split())]
    tok = Tokenizer(tmodels.WordLevel({w: i for i, w in enumerate(words)}, unk_token="<unk>"))
    tok.pre_tokenizer = pre_tokenizers.Whitespace()
    hf = PreTrainedTokenizerFast(tokenizer_object=tok, bos_token="<bos>", eos_token="<eos>", pad_token="<pad>",
                                 unk_token="<unk>")  # fmt: skip
    encoder = LTXGemmaTextEncoder(model=model.to(torch.bfloat16).eval(), tokenizer=LTXGemmaTokenizer(hf, 1024, PaddingSide.LEFT))  # fmt: skip
    return _create_and_populate_gemma4_unified(encoder), build_gemma_hf_config(assets).text_config


@pytest.fixture(scope="module")
def tiny():
    """component → its tiny module, each from ltx-core's configurator (the connector as its configurator does it,
    minus the Gemma file it reads the text config from)."""
    from ltx_core.text_encoders.gemma import EmbeddingsProcessor
    from ltx_core.text_encoders.gemma.embeddings_connector import (
        AudioEmbeddings1DConnectorConfigurator,
        Embeddings1DConnectorConfigurator,
    )
    from ltx_core.text_encoders.gemma.encoders.encoder_configurator import _create_feature_extractor

    dit = tiny_dit()
    te, text_config = tiny_gemma()
    connector = EmbeddingsProcessor(feature_extractor=_create_feature_extractor(dit["config"]["transformer"], text_config),
                                    video_connector=Embeddings1DConnectorConfigurator.from_metadata(dit),
                                    audio_connector=AudioEmbeddings1DConnectorConfigurator.from_metadata(dit))  # fmt: skip
    vae = official("video-vae")
    vae["config"]["vae"]["decoder_base_channels"] = (
        8  # the encoder is as wide as its 128 latent channels: no growth
    )
    vae["config"]["vae"]["encoder_blocks"] = [[n, {"num_layers": 1} if n == "res_x" else {"multiplier": 1}]
                                              for n, _ in vae["config"]["vae"]["encoder_blocks"]]  # fmt: skip
    audio = official("audio-vae")
    for part, width in (
        ("vocoder", 128),
        ("bwe", 64),
    ):  # a channel halves at each upsample: 6 and 5 of them
        audio["config"]["vocoder"][part] |= {"upsample_initial_channel": width, "resblock_kernel_sizes": [3],
                                             "resblock_dilation_sizes": [[1, 3, 5]]}  # fmt: skip
    up = official("upscaler")
    up["config"]["mid_channels"] = 32
    metas = {"transformer": dit, "vae": vae, "vae_encoder": vae, "audio_vae": audio, "vocoder": audio,
             "audio_encoder": audio, "upscaler": up}  # fmt: skip
    out = {c: randomized(ltx.PARTS[c][0].from_metadata(m), torch.float32 if c == "vocoder" else torch.bfloat16)
           for c, m in metas.items()}  # fmt: skip
    return out | {"text_encoder": te, "connector": randomized(connector)}


class Ctx:
    """A run's ctx without a run: components are the tiny ones; records which variant each was asked for."""

    device, node = "cpu", "n"

    def __init__(self, modules):
        self.modules, self.asked = modules, []

    def check(self):
        pass

    def progress(self, *a):
        pass

    def component(self, model, name):
        self.asked.append((model.variant, name))
        return self.modules[name]


@pytest.fixture(scope="module")
def pack():
    return packs.load(ROOT / "packs/ltx", "torch")[0]


def ffmpeg(*args) -> None:
    subprocess.run([ltx.FFMPEG, "-v", "error", "-y", *args], check=True, timeout=60)


def clip(data: bytes, tmp_path: Path, frames: int, size=128) -> dict:
    (tmp_path / "out.mp4").write_bytes(data)
    info = media.probe(tmp_path / "out.mp4")
    assert (info["stream"]["width"], info["stream"]["height"], info["frames"]) == (
        size,
        size,
        frames,
    )
    return info


def test_text_and_image_to_video_make_an_mp4_of_the_canvas(tiny, pack, tmp_path):
    ctx, model = Ctx(tiny), packs.ModelRef(pack, "distilled-fp8", "fp8")
    cond = ltx.encode(ctx, {"beats": []}, {"model": model, "prompt": "a red fox runs"})["cond"]
    assert cond.video.shape == (1, 1024, 16) and cond.audio.shape == (1, 1024, 16)
    img = Image.fromarray(np.random.default_rng(0).integers(0, 255, (96, 160, 3), dtype=np.uint8))
    rows = [
        {"image": img, "seconds": 0, "strength": 1},
        {"image": img, "seconds": 0.3, "strength": 0.8},
    ]
    for c in (cond, ltx.keyframes(ctx, {"images": rows}, {"cond": cond})["cond"]):
        size, d = {"width": 128, "height": 128}, {"frames": 9, "fps": 24}
        lat = ltx.sample(ctx, {"role": "stage1", "steps": 8},
                         {"model": model, "cond": c, "seed": 7, "canvas": size, "duration": d})["latent"]  # fmt: skip
        assert lat.fhw == (2, 2, 2) and lat.video.shape[1] == 8 + 4 * bool(
            c.images
        )  # the frame-7 row's tokens
        lat = ltx.upscale(ctx, {}, {"model": model, "latent": lat})["latent"]
        lat = ltx.sample(ctx, {"role": "stage2", "steps": 3}, {"model": model, "cond": c, "seed": 7, "latent": lat})["latent"]  # fmt: skip
        assert lat.fhw == (2, 4, 4) and torch.isfinite(lat.video.float()).all()
        out = ltx.decode(ctx, {"audio": True}, {"model": model, "latent": lat})["video"]
        info = clip(out["mp4"], tmp_path, 9)
        assert (
            info["audio"] is not None
            and out["poster"].size == (128, 128)
            and out["last"].size == (128, 128)
        )
    assert {v for v, _ in ctx.asked} == {"distilled-fp8"}


def source(tmp_path: Path) -> bytes:
    ffmpeg("-f", "lavfi", "-i", "testsrc=size=64x64:rate=24", "-f", "lavfi", "-i", "sine=f=440:sample_rate=48000",
           "-t", "0.4", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", str(tmp_path / "src.mp4"))  # fmt: skip
    return (tmp_path / "src.mp4").read_bytes()


GUIDED = {"steps": 2, "cfg": 3, "stg": 1}


def test_retake_and_extend_give_the_whole_clip_with_its_source_map(tiny, pack, tmp_path):
    ctx, model = Ctx(tiny), packs.ModelRef(pack, "dev-int8", "int8")
    cond = ltx.encode_guided(ctx, {}, {"model": model, "prompt": "a red fox"})["cond"]
    assert cond.neg_video.shape == cond.video.shape
    src = ltx.encode_video(
        ctx, {"source": {"take_id": "t", "data": source(tmp_path)}}, {"model": model}
    )["latent"]
    assert (src.frames, src.fhw, src.fps) == (9, (2, 2, 2), 24.0) and src.audio.shape[2] == 128
    inputs = {"model": model, "cond": cond, "seed": 3, "latent": src}
    lat = ltx.retake(ctx, {"span": {"start": 0.3, "end": 0.6}, "audio": True} | GUIDED, inputs)[
        "latent"
    ]
    assert lat.source_map["take_id"] == "t" and lat.video.shape == src.video.shape
    clip(
        ltx.decode(ctx, {"audio": True}, {"model": model, "latent": lat})["video"]["mp4"],
        tmp_path,
        9,
        64,
    )
    lat = ltx.extend(ctx, {"seconds": 0.3, "direction": "before"} | GUIDED, inputs)["latent"]
    assert (
        lat.frames == 17
        and lat.fhw == (3, 2, 2)
        and lat.source_map["offset_s"] == pytest.approx(8 / 24)
    )
    out = ltx.decode(ctx, {"audio": False}, {"model": model, "latent": lat})["video"]
    assert clip(out["mp4"], tmp_path, 17, 64)["audio"] is None and out["source_map"][
        "new_out_s"
    ] == pytest.approx(8 / 24)


def test_audio_to_video_freezes_the_sound_and_stage_2_runs_the_share_owners_transformer(
    tiny, pack, tmp_path
):
    ctx, dev = Ctx(tiny), packs.ModelRef(pack, "dev-int8", "int8")
    cond = ltx.encode_guided(ctx, {}, {"model": dev, "prompt": "a red fox"})["cond"]
    ffmpeg("-f", "lavfi", "-i", "sine=f=330:sample_rate=48000", "-t", "1", str(tmp_path / "a.wav"))
    take = {
        "take_id": "a",
        "data": (tmp_path / "a.wav").read_bytes(),
        "duration_s": 1.0,
        "in": 0.1,
        "out": 0.6,
    }
    got = ltx.encode_audio_(ctx, {"source": take, "fps": 24}, {"model": dev})
    a = got["latent"]
    assert got["duration"] == {"frames": 9, "fps": 24} and a.audio.shape[1] == round(9 / 24 * 25)
    lat = ltx.sample_guided(ctx, GUIDED, {"model": dev, "cond": cond, "seed": 5, "canvas": {"width": 128, "height": 128},
                                          "duration": got["duration"], "audio": a})["latent"]  # fmt: skip
    assert torch.equal(lat.audio, a.audio)  # frozen: mask 0 keeps the source's tokens
    lat = ltx.upscale(ctx, {}, {"model": dev, "latent": lat})["latent"]
    distilled = ltx.distill(None, {}, {"model": dev})["model"]
    assert (distilled.variant, distilled.precision_of("transformer")) == ("distilled-fp8", "fp8")
    assert distilled.key("text_encoder") == dev.key(
        "text_encoder"
    )  # the shared parts stay one residency entry
    ctx.asked.clear()
    lat = ltx.sample(ctx, {"role": "stage2", "steps": 3}, {"model": distilled, "cond": cond, "seed": 5, "latent": lat})["latent"]  # fmt: skip
    assert ctx.asked == [("distilled-fp8", "transformer")]
    out = ltx.decode(ctx, {"audio": True}, {"model": dev, "latent": lat})["video"]
    info = clip(out["mp4"], tmp_path, 9)
    assert out["source_map"] == {
        "take_id": "a",
        "offset_s": -0.1,
        "new_in_s": 0,
        "new_out_s": 9 / 24,
    }
    assert float(info["audio"]["duration"]) == pytest.approx(
        9 / 24, abs=0.03
    )  # the source part, trimmed


def test_checks_refuse_lora_rows_beats_and_take_the_variants_as_the_official_ones(
    pack, tmp_path, monkeypatch
):
    monkeypatch.setattr(config, "MODELS", tmp_path)
    fp8, int8 = (
        packs.ModelRef(pack, "distilled-fp8", "fp8"),
        packs.ModelRef(pack, "dev-int8", "int8"),
    )
    stage1 = {"role": "stage1", "steps": 8}, {"canvas", "duration"}
    assert ltx.check_sample(*stage1, {"model": fp8}) is None
    assert ltx.check_sample(*stage1, {"model": int8}) == ": needs model variant distilled"
    assert ltx.check_retake({}, set(), {"model": fp8}) == ": needs model variant dev"
    assert getattr(ltx, "check_sample-guided")({}, set(), {"model": int8}) is None
    row = packs.LoraUse("l", "shared", "style", "0" * 64, 10, 1.0, "bake")
    for check, m in ((ltx.check_sample, fp8), (ltx.check_retake, int8), (ltx.check_distill, int8)):
        assert check(*stage1, {"model": replace(m, loras=(row,))}) == ltx.NO_LORA
    assert (
        ltx.check_encode({"beats": [{"text": "x", "seconds": 0}]}, set())
        == f".beats: {ltx.NO_RELAY}"
    )
    assert ltx.check_encode({"beats": []}, set()) is None
    # distill needs its distill_from variant's transformer on this host (the contract's text, as the dispatcher's)
    assert (
        ltx.check_distill({}, set(), {"model": int8})
        == ": no live host for env torch has the distilled LoRA"
    )
    (tmp_path / "ltx-2.5-nvidia/LTX-2.5-FP8.safetensors").parent.mkdir()
    (tmp_path / "ltx-2.5-nvidia/LTX-2.5-FP8.safetensors").write_bytes(b"")
    assert ltx.check_distill({}, set(), {"model": int8}) is None
    assert ltx.check_distill({}, set(), {"model": fp8}) == ": needs model variant dev"


def test_the_nvidia_variants_publish_on_cuda_only_and_the_official_ones_on_the_mac(
    pack, tmp_path, monkeypatch
):
    monkeypatch.setattr(config, "MODELS", tmp_path)
    mlx = replace(pack, runtime="mlx")
    for device, torch_ids, mlx_ids in (
        ("cuda", ["distilled-fp8", "dev-int8"], []),
        ("mps", [], ["distilled", "dev"]),
    ):
        monkeypatch.setattr(models, "device", lambda p, d=device: d)
        assert [v for v in pack.spec["variants"] if models.runs_here(pack, v)] == torch_ids
        if device == "mps":  # the MLX runner runs only on the Mac
            assert [v for v in pack.spec["variants"] if models.runs_here(mlx, v)] == mlx_ids
    monkeypatch.setattr(models, "device", lambda p: "cuda")
    entry = models.entry(pack)
    v = {x["id"]: x for x in entry["variants"]}
    assert v["distilled-fp8"]["precisions"] == ["fp8"] and v["dev-int8"]["precisions"] == ["int8"]
    # the dispatcher's built-in distill source; LoRAs are the MLX runner's only (no block, no meta build)
    assert v["dev-int8"]["distill_from"] == "distilled-fp8"
    assert "distill_from" not in v["distilled-fp8"]
    assert "lora" not in entry
    comps = {c["component"]: c["from_variant"] for c in v["dev-int8"]["components"]}
    assert (
        comps["transformer"] == "dev-int8"
        and comps["text_encoder"] == comps["vae"] == "distilled-fp8"
    )
    value = {"schema_version": 1, "host": "t", "env": "torch", "published_at": "2026-11-21T10:00:00Z",
             "published_by": "t", "models": [entry], "licences": models.licences([pack])}  # fmt: skip
    assert schemas.registry_errors("models", value) == []


def test_the_torch_registry_publishes_the_ltx_nodes_but_not_its_mlx_workflows():
    from core import registry

    values, _, _ = registry.build("torch")
    assert "ltx/sample" in registry.node_schemas(values)
    assert "ltx/lora-stack" not in registry.node_schemas(values)
    assert not [w["id"] for w in values["workflows"]["workflows"] if w["id"].startswith("ltx")]


# --- the community layouts through the core loader


def hadamard_rotated(w: torch.Tensor) -> torch.Tensor:
    g = ltx.CONVROT_GROUP
    return (w.float().reshape(w.shape[0], -1, g) @ lt.hadamard(g, "cpu", torch.float32)).reshape(
        w.shape
    )


def test_int8_convrot_and_fp8_layouts_load_as_their_dequantized_selves(tmp_path):
    """A DmitryDB-layout pair of Linears (one int8 ConvRot with its tag and scale, one kept bf16) and an unsloth one
    (torchao FP8) load through the core loader with torch.py's rename, rotated and tensor_hook into an Int8Linear on
    the file's own int8 values, an Int8Linear quantized at load, unrotated, and an Fp8Linear."""
    torch.manual_seed(0)
    w = {n: torch.randn(64, 512) / 20 for n in ("to_q", "to_k", "to_v")}
    rot = hadamard_rotated(w["to_q"])
    scale = rot.abs().amax(1, keepdim=True) / 127
    q = (rot / scale).round().to(torch.int8)
    tag = torch.frombuffer(bytearray(json.dumps(ltx.CONVROT).encode()), dtype=torch.uint8)
    e4m3_scale = w["to_v"].abs().amax(1, keepdim=True) / lt.E4M3_MAX
    e4m3 = (w["to_v"] / e4m3_scale).to(torch.float8_e4m3fn)
    ddb, uns = "model.diffusion_model.transformer_blocks.0.attn1.", "transformer_blocks.0.attn1."
    conn = "model.diffusion_model.video_embeddings_connector.transformer_1d_blocks.0.attn1.to_q"
    path = tmp_path / "t.safetensors"
    save_file({f"{ddb}to_q.weight": q, f"{ddb}to_q.weight_scale": scale.float(), f"{ddb}to_q.comfy_quant": tag,
               f"{ddb}to_k.weight": w["to_k"].to(torch.bfloat16), f"{uns}to_v._weight_qdata": e4m3,
               f"{uns}to_v._weight_scale": e4m3_scale.float(),
               # the dev file tags its connector copy too, which the transformer does not load
               f"{conn}.weight": q.clone(), f"{conn}.weight_scale": scale.float().clone(), f"{conn}.comfy_quant": tag.clone()}, str(path))  # fmt: skip
    spec = {"quantize": ["transformer_blocks.*"]}
    model = types.SimpleNamespace(files=lambda c: [path], precision_of=lambda c: "int8",
                                  pack=types.SimpleNamespace(component=lambda c: spec))  # fmt: skip
    with torch.device("meta"):
        attn = torch.nn.Module()
        for n in w:
            setattr(attn, n, torch.nn.Linear(512, 64, bias=False))
        block = torch.nn.Module()
        block.attn1 = attn
        module = torch.nn.Module()
        module.transformer_blocks = torch.nn.ModuleList([block])
    got = lt.load(module, [path], dev="cpu", dtype=None, quantize=lambda n: packs.quantized(spec, "int8", n),
                  rotated=ltx.rotated("transformer", model), rename=lambda k: ltx.rename("transformer", k),
                  hook=ltx.tensor_hook("transformer", model)).transformer_blocks[0].attn1  # fmt: skip
    assert (got.to_q.rot_group, got.to_k.rot_group, type(got.to_v)) == (256, None, lt.Fp8Linear)
    assert torch.equal(got.to_q.qweight, q)  # the file's own int8 values, requantized exactly
    x = torch.randn(3, 512)
    w["to_v"] = (
        e4m3.float() * e4m3_scale
    )  # FP8 against its own dequantized weight (e4m3 itself is ~3 % off)
    for n, m in (("to_q", got.to_q), ("to_k", got.to_k), ("to_v", got.to_v)):
        want = x @ w[n].T
        assert (m(x) - want).norm() / want.norm() < 0.02, n


def test_a_convrot_tag_of_another_layout_is_refused(tmp_path):
    path = tmp_path / "t.safetensors"
    tag = json.dumps(ltx.CONVROT | {"convrot_groupsize": 128}).encode()
    save_file({"model.layers.0.self_attn.q_proj.comfy_quant": torch.frombuffer(bytearray(tag), dtype=torch.uint8)},
              str(path))  # fmt: skip
    with pytest.raises(RunError, match="int8 ConvRot"):
        ltx.rotated("text_encoder", types.SimpleNamespace(files=lambda c: [path]))


# --- the port against mlx.py on the same tiny weights (envs/mlx has mlx and no torch: its side runs there)

MLX_SIDE = r"""
import json, sys
import mlx.core as mx
from pathlib import Path
from packs.ltx import mlx as m
from ltx_core_mlx.conditioning.types.keyframe_cond import _compute_keyframe_positions
from ltx_core_mlx.model.transformer.model import X0Model
from ltx_core_mlx.utils.positions import compute_audio_positions, compute_video_positions
tmp = Path(sys.argv[1])
x = mx.load(str(tmp / "in.safetensors"))
model = type("M", (), {"files": lambda self, c: [tmp / "dit.safetensors"]})()
dit = m.build("transformer", model)
weights = mx.load(str(tmp / "dit.safetensors"))
dit.load_weights([(n, v) for k, v in weights.items() if (n := m.rename("transformer", k)) is not None])
f, h, w = (int(v) for v in x["fhw"].tolist())
vx0, ax0 = X0Model(dit)(video_latent=x["video"], audio_latent=x["audio"], sigma=x["sigma"],
    video_text_embeds=x["vctx"], audio_text_embeds=x["actx"], video_positions=compute_video_positions(f, h, w, 24.0),
    audio_positions=compute_audio_positions(x["audio"].shape[1]), video_keyframes_mask=x["keyframes"])
mx.save_safetensors(str(tmp / "out.safetensors"), {"video": vx0, "audio": ax0,
    "vpos": compute_video_positions(f, h, w, 24.0), "apos": compute_audio_positions(x["audio"].shape[1]),
    "kpos": _compute_keyframe_positions(7, h, w, 24.0)})
(tmp / "schedule.json").write_text(json.dumps(m.ltx2_schedule(30, 1234)))
"""


@pytest.mark.skipif(not MLX_PYTHON.is_file(), reason="needs envs/mlx (uv sync --project envs/mlx)")
def test_the_torch_port_matches_mlx_on_the_same_tiny_weights(tmp_path):
    """The same random official-named transformer through both ports' builds and renames, the same inputs: the
    x0 predictions agree (float32), and so do the RoPE positions and the guided schedule. ponytail: no VAE parity,
    the MLX decoder is fixed at full width (a ~0.5B random model); the VAEs are ltx-core's own code."""
    dit = tiny_dit()
    module = randomized(ltx.PARTS["transformer"][0].from_metadata(dit), torch.float32)
    save_file({f"model.diffusion_model.{k}": v.contiguous() for k, v in module.state_dict().items()},
              str(tmp_path / "dit.safetensors"),
              metadata={k: v if isinstance(v, str) else json.dumps(v) for k, v in dit.items()})  # fmt: skip
    f, h, w, t = 2, 2, 3, ltx.audio_tokens(9, 24)
    g = torch.Generator().manual_seed(1)
    keyframes = ltx.first_frame(torch.ones(1, f * h * w, 1), h * w)
    x = {"video": torch.randn(1, f * h * w, 128, generator=g), "audio": torch.randn(1, t, 128, generator=g),
         "vctx": torch.randn(1, 32, 16, generator=g), "actx": torch.randn(1, 32, 16, generator=g),
         "sigma": torch.tensor([0.5]), "keyframes": keyframes, "fhw": torch.tensor([f, h, w])}  # fmt: skip
    save_file(x, str(tmp_path / "in.safetensors"))
    r = subprocess.run([str(MLX_PYTHON), "-c", MLX_SIDE, str(tmp_path)], cwd=ROOT, capture_output=True, text=True,
                       timeout=300, check=False, env=os.environ | {"PYTHONPATH": str(ROOT)})  # fmt: skip
    assert r.returncode == 0, r.stderr[-2000:]
    with safe_open(str(tmp_path / "out.safetensors"), "pt") as o:
        mlx = {k: o.get_tensor(k) for k in o.keys()}  # noqa: SIM118
    video = ltx.State(x["video"], x["video"], torch.ones(1, f * h * w, 1), ltx.video_positions(f, h, w, 24.0, "cpu"), keyframes)  # fmt: skip
    audio = ltx.State(x["audio"], x["audio"], torch.ones(1, t, 1), ltx.audio_positions(t, "cpu"))
    assert torch.allclose(video.positions.mean(-1).permute(0, 2, 1), mlx["vpos"], atol=1e-6)
    assert torch.allclose(audio.positions.mean(-1).permute(0, 2, 1), mlx["apos"], atol=1e-6)
    # an appended keyframe's tokens (VideoConditionByKeyframeIndex), one pixel frame wide at frame 7
    kpos = ltx.keyframe_positions(7, h, w, 24.0, "cpu").mean(-1).permute(0, 2, 1)
    assert torch.allclose(kpos, mlx["kpos"], atol=1e-6)
    with torch.no_grad():
        vx0, ax0 = ltx._pass(
            ltx.X0Model(module), x["video"], x["audio"], video, audio, 0.5, x["vctx"], x["actx"]
        )
    for got, want, noisy in ((vx0, mlx["video"], x["video"]), (ax0, mlx["audio"], x["audio"])):
        # against the model's own term (x − x0 = v·σ), not x0, which the input dominates: measured 0.17 %
        assert (got.float() - want).norm() / (noisy - want).norm() < 1e-2
    assert ltx.ltx2_schedule(30, 1234) == pytest.approx(
        json.loads((tmp_path / "schedule.json").read_text())
    )
    assert math.isclose(ltx.ltx2_schedule(30, 1234)[-1], 0.0)
