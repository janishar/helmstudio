"""The mlx env (runner_service/envs/mlx) and the ltx pack, without weights: the published values equal the canonical
fixtures, the core stays runtime-neutral, the MLX loader (rename, convert, int8 / int4, strict), sizes from the
headers, the graph checks before anything loads, and the stage-1 noise equals the goldens' bit for bit.
`uv run --project envs/mlx pytest -q`; skipped where mlx is not installed. Real weights: tests/golden/compare_ltx.py."""

import hashlib
import json
import subprocess
import sys
from pathlib import Path

import pytest
from conftest import CONTRACTS, safetensors_file

from core import graph, pack, registry
from core.graph import Cache, Ctx, RunError

try:
    import mlx.core as mx
    from mlx import nn
except ImportError:
    mx = nn = None

ROOT = Path(__file__).resolve().parents[1]
GOLDEN = ROOT / "tests/golden/ltx-text-to-video"
FIXTURES = CONTRACTS / "fixtures/registry"


def neutral(code: str) -> str:
    """Run `code` in a fresh interpreter of this env (cwd runner_service) and return its stdout."""
    r = subprocess.run(
        [sys.executable, "-c", code], cwd=ROOT, capture_output=True, text=True, check=True
    )
    return r.stdout.strip()


def test_the_core_imports_no_runtime():
    code = ("import sys, consumer, main; from core import graph, pack, registry, residency, nodes, images, lora; "
            "print(sorted({'torch', 'mlx'} & {m.split('.')[0] for m in sys.modules}))")  # fmt: skip
    assert neutral(code) == "[]"


@pytest.mark.parametrize(("env", "other"), [("mlx", "torch"), ("torch", "mlx")])
def test_a_runner_process_never_imports_the_other_runtime(env, other):
    pytest.importorskip(env)
    code = (f"import sys; from core import registry; import main; v, i, p = registry.build({env!r}); "
            f"main.residency_for(p); print({other!r} in sys.modules, {env!r} in sys.modules)")  # fmt: skip
    assert neutral(code) == "False True"


needs_mlx = pytest.mark.skipif(
    mx is None, reason="the mlx env only (uv run --project envs/mlx pytest)"
)


@pytest.fixture
def on_cpu():
    """Unit tests stay off the GPU."""
    mx.set_default_device(mx.cpu)
    yield
    mx.set_default_device(mx.gpu)


def ltx():
    values, impls, packs = registry.build("mlx")
    return values, impls, next(p for p in packs if p.family == "ltx")


@needs_mlx
def test_ltx_pack_publishes_the_canonical_fixtures():
    values, _, _ = ltx()
    for kind in ("nodes", "workflows"):
        want = json.loads((FIXTURES / f"ltx.{kind}.valid.json").read_text())
        assert values[kind][kind] == want[kind] and values[kind]["env"] == "mlx"
    (model,) = values["models"]["models"]
    assert (model["id"], model["family"], model["title"]) == ("ltx-2.5", "ltx", "LTX-2.5")
    assert [v["id"] for v in model["variants"]] == ["distilled"]
    assert model["variants"][0]["precisions"] == ["bf16", "int8", "int4"] and "lora" not in model


@needs_mlx
def test_ltx_sizes_come_from_the_headers(tmp_path, monkeypatch):
    """dtype stored keeps each tensor's bytes; rename's None keys do not count; MLX int8 / int4 is bits / 8 per value
    plus a bf16 scale and bias per group of 64."""
    _, _, p = ltx()
    monkeypatch.setattr(pack, "models_root", lambda: tmp_path)
    pack._sizes.clear()
    dit = tmp_path / "ltx-2.5/diffusion_models/ltx-2.5-22b-distilled-transformer-bf16.safetensors"
    safetensors_file(dit, {
        "model.diffusion_model.transformer_blocks.0.attn1.to_q.weight": ("BF16", [64, 128]),
        "model.diffusion_model.scale_shift_table": ("F32", [2, 64]),
        "model.diffusion_model.video_embeddings_connector.x.weight": ("BF16", [64, 64]),  # the connector's
    })  # fmt: skip
    gb = {
        prec: pack.ModelRef(p, "distilled", prec).gb("transformer")[0] * 2**30
        for prec in ("bf16", "int8", "int4")
    }
    assert gb == {"bf16": 64 * 128 * 2 + 2 * 64 * 4, "int8": 64 * 128 + 64 * 128 // 64 * 4 + 2 * 64 * 4,
                  "int4": 64 * 128 // 2 + 64 * 128 // 64 * 4 + 2 * 64 * 4}  # fmt: skip


@needs_mlx
def test_mlx_loader_renames_converts_quantizes_and_loads_strictly(tmp_path, on_cpu):
    from core import loaders_mlx

    class Tiny(nn.Module):
        def __init__(self):
            super().__init__()
            self.proj = nn.Linear(128, 64)
            self.norm = nn.RMSNorm(64)

    w = mx.random.normal((64, 128)).astype(mx.bfloat16)
    f = tmp_path / "t.safetensors"
    mx.save_safetensors(str(f), {"m.proj.weight": w, "m.proj.bias": mx.zeros((64,)), "m.norm.weight": mx.ones((64,)),
                                 "skip.me": mx.zeros((3,))})  # fmt: skip
    seen = []
    kw = {"rename": lambda k: k.removeprefix("m.") if k.startswith("m.") else None,
          "convert": lambda n, v: seen.append(n) or v, "quantize": lambda name: name == "proj"}  # fmt: skip
    m = loaders_mlx.load(Tiny(), [f], bits=8, check=lambda: seen.append("check"), **kw)
    assert isinstance(m.proj, nn.QuantizedLinear) and m.proj.bits == 8 and m.proj.group_size == 64
    q, scales, _biases = mx.quantize(w, group_size=64, bits=8)
    assert mx.array_equal(m.proj.weight, q) and mx.array_equal(m.proj.scales, scales)
    assert (
        m.norm.weight.dtype == mx.float32 and seen.count("check") == 1
    )  # stored dtype kept; a cancel point
    m4 = loaders_mlx.load(Tiny(), [f], bits=4, dtype=mx.bfloat16, **kw)
    assert m4.proj.bits == 4 and m4.norm.weight.dtype == mx.bfloat16
    plain = loaders_mlx.load(Tiny(), [f], **kw)
    assert isinstance(plain.proj, nn.Linear) and mx.array_equal(plain.proj.weight, w)
    with pytest.raises(ValueError):  # strict: the module's norm.weight is not in the file
        loaders_mlx.load(
            Tiny(), [f], rename=lambda k: k.removeprefix("m.") if k.startswith("m.proj") else None
        )


@needs_mlx
def test_ltx_renames_the_official_keys():
    from packs.ltx import mlx as ltx_mlx

    r = ltx_mlx.rename
    assert r("transformer", "model.diffusion_model.transformer_blocks.3.ff.net.0.proj.weight") == \
        "transformer_blocks.3.ff.proj_in.weight"  # fmt: skip
    assert r("transformer", "model.diffusion_model.audio_embeddings_connector.a.weight") is None
    assert r("connector", "model.diffusion_model.audio_embeddings_connector.a.weight") == \
        "connector.audio_embeddings_connector.a.weight"  # fmt: skip
    assert r("connector", "text_embedding_projection.video_aggregate_embed.bias") == \
        "connector.text_embedding_projection.video_aggregate_embed.bias"  # fmt: skip
    assert (
        r("text_encoder", "model.layers.0.self_attn.q_proj.weight")
        == "layers.0.self_attn.q_proj.weight"
    )
    assert (
        r("text_encoder", "tokenizer_json") is None
        and r("text_encoder", "vision_model.patch_ln1.bias") is None
    )
    assert r("vae", "per_channel_statistics.mean-of-means") == "per_channel_statistics.mean"
    assert r("vae", "encoder.conv_in.conv.weight") is None
    assert (
        r("audio_vae", "audio_vae.per_channel_statistics.std-of-means")
        == "per_channel_statistics.std_of_means"
    )
    assert r("vocoder", "vocoder.vocoder.conv_pre.weight") == "conv_pre.weight"
    assert r("vocoder", "audio_vae.decoder.x") is None


def run_graph(nodes, impls, schemas):
    import asyncio

    ctx = Ctx(progress=None, output=None, check=lambda: None)
    nodes, _ = graph.resolve(nodes, schemas)
    return asyncio.run(graph.run(nodes, impls, schemas, ctx, Cache()))


@needs_mlx
@pytest.mark.parametrize(
    ("edit", "error"),
    [
        (
            lambda n: n["sample1"]["in"].pop("canvas"),
            "sample1: stage 1 needs canvas and duration, not latent",
        ),
        (
            lambda n: n["sample2"]["in"].update(canvas="canvas.canvas"),
            "sample2: stage 2 needs latent, not canvas",
        ),
        (
            lambda n: n["sample2"]["params"].update(steps=4),
            "sample2.steps: stage 2 has 3 steps at most",
        ),
    ],
)
def test_ltx_role_rules_fail_the_run_before_anything_loads(edit, error):
    values, impls, _ = ltx()
    schemas = registry.node_schemas(values)
    nodes = json.loads(json.dumps(values["workflows"]["workflows"][0]["nodes"]))
    nodes["prompt"]["params"]["text"] = "a cat"
    edit(nodes)
    assert (
        graph.validate(nodes, schemas) == []
    )  # the graph rules let it through: the pack's check does not
    with pytest.raises(RunError, match=error.replace(".", r"\.")):
        run_graph(nodes, impls, schemas)  # no residency: any load would fail with another error


@needs_mlx
def test_frames_off_the_grid_is_param_invalid_with_the_contract_text():
    values, _, _ = ltx()
    nodes = json.loads(json.dumps(values["workflows"]["workflows"][0]["nodes"]))
    nodes["duration"]["params"]["frames"] = 50
    assert graph.validate(nodes, registry.node_schemas(values)) == [
        "duration.frames: 50 is not on the frame grid 9 + 8k up to 321"
    ]


def sha(a) -> str:
    import numpy as np

    return hashlib.sha256(np.array(a.astype(mx.float32)).tobytes()).hexdigest()


@needs_mlx
@pytest.mark.parametrize("case", ["t2v-448x256-49f-s42", "t2v-512x320-57f-s7"])
def test_stage1_noise_is_the_goldens_bit_for_bit(case, on_cpu):
    """Seeds and shapes as DistilledPipeline draws them (reference note section 6), with no weights."""
    from packs.ltx import mlx as ltx_mlx

    g = json.loads((GOLDEN / f"{case}.json").read_text())
    h, w = ltx_mlx.snap_output_dimensions(g["height"], g["width"], two_stage=True, warn=False)
    fhw = ltx_mlx.compute_video_latent_shape(g["frames"], h // 2, w // 2)
    t = ltx_mlx.compute_audio_token_count(g["frames"], frame_rate=g["fps"])
    pos = ltx_mlx.compute_video_positions(*fhw, frame_rate=g["fps"])
    video = ltx_mlx._state((1, fhw[0] * fhw[1] * fhw[2], 128), fhw, pos, g["seed"], 1.0)
    audio = ltx_mlx._state((1, t, 128), fhw, ltx_mlx.compute_audio_positions(t), g["seed"] + 1, 1.0)
    stage = g["stages"][0]
    assert list(pos.shape) == stage["video_positions"] and stage["sigmas"] == ltx_mlx.STAGE1_SIGMAS
    assert (sha(video.latent), sha(audio.latent)) == (
        stage["video_in"]["sha256_f32"],
        stage["audio_in"]["sha256_f32"],
    )
    assert g["stages"][1]["sigmas"] == ltx_mlx.STAGE2_SIGMAS


@needs_mlx
def test_a_second_upscale_of_one_latent_fails_before_it_loads_anything(on_cpu):
    """Each upscale doubles the latent: chained, it would grow past the canvas admission budgeted and past memory."""
    from packs.ltx import mlx as ltx_mlx

    tokens = mx.zeros((1, 2 * 4 * 4, 128))
    latent = ltx_mlx.Latent(tokens, mx.zeros((1, 8, 128)), (2, 4, 4), 9, 24.0, upscaled=True)
    ctx = Ctx(progress=None, output=None, check=lambda: None)
    ctx.node = "upscale2"
    with pytest.raises(RunError, match="upscale2: the latent is already upscaled"):
        ltx_mlx.upscale(
            ctx, {}, {"model": None, "latent": latent}
        )  # model None: any load would fail otherwise
