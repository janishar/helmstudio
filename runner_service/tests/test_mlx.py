"""The mlx env (runner_service/envs/mlx) and the ltx pack, without weights: the published values equal the canonical
fixtures, the core stays runtime-neutral, the MLX loader (rename, convert, int8 / int4, strict), sizes from the
headers, the graph checks before anything loads, and the stage-1 noise equals the goldens' bit for bit.
`uv run --project envs/mlx pytest -q`; skipped where mlx is not installed. Real weights: tests/golden/compare_ltx.py."""

import hashlib
import json
import subprocess
import sys
from dataclasses import replace
from pathlib import Path
from types import SimpleNamespace

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


def only(values: dict, family: str) -> dict:
    """The mlx values as one family's pack alone publishes them (the mlx runner publishes ltx and llm together):
    its workflows, models and nodes, with the core nodes its workflows use."""
    out = {
        k: dict(v) for k, v in values.items()
    }  # the entries stay shared (fill_prefused updates them)
    out["workflows"]["workflows"] = [
        w for w in out["workflows"]["workflows"] if w["family"] == family
    ]
    out["models"]["models"] = [m for m in out["models"]["models"] if m["family"] == family]
    used = {n["kind"] for w in out["workflows"]["workflows"] for n in w["nodes"].values()}
    out["nodes"]["nodes"] = [
        n for n in out["nodes"]["nodes"] if n["family"] == family or n["kind"] in used
    ]
    return out


def ltx():
    values, impls, packs = registry.build("mlx")
    return only(values, "ltx"), impls, next(p for p in packs if p.family == "ltx")


@needs_mlx
def test_ltx_pack_publishes_the_canonical_fixtures():
    values, _, _ = ltx()
    for kind in ("nodes", "workflows"):
        want = json.loads((FIXTURES / f"ltx.{kind}.valid.json").read_text())
        assert values[kind][kind] == want[kind] and values[kind]["env"] == "mlx"
    (model,) = values["models"]["models"]
    assert (model["id"], model["family"], model["title"]) == ("ltx-2.5", "ltx", "LTX-2.5")
    assert [v["id"] for v in model["variants"]] == ["distilled", "dev"]
    assert (
        model["variants"][0]["precisions"]
        == model["variants"][1]["precisions"]
        == ["bf16", "int8", "int4"]
    )


@needs_mlx
def test_llm_pack_publishes_the_canonical_fixtures():
    values, impls, _ = registry.build("mlx")
    llm = only(values, "llm")
    for kind in ("nodes", "workflows"):
        want = json.loads((FIXTURES / f"llm.{kind}.valid.json").read_text())
        assert llm[kind][kind] == want[kind]
    (model,) = llm["models"]["models"]
    assert (model["id"], model["family"], model["title"]) == ("qwen3.8-27b", "llm", "Qwen3.8 27B")
    assert impls["llm/ask"].uses == ("model",) and model["variants"][0]["precisions"] == ["int4"]
    if (
        model["variants"][0]["presence"] == "present"
    ):  # the weights are on this host: sized from the headers
        (plan,) = model["variants"][0]["memory"]
        assert (
            plan["components"][0]["component"] == "model" and 14 < plan["components"][0]["gb"] < 16
        )
        assert plan["planned_at"]["images"] == 4 and "workflow" not in plan


@needs_mlx
def test_the_canonical_llm_ask_command_fits_the_published_nodes():
    """cmd.run.execute.valid.7 (a judge's ask over a video and an image take) passes the runner's own graph check."""
    values, _, _ = registry.build("mlx")
    cmd = json.loads((CONTRACTS / "fixtures/messages/cmd.run.execute.valid.7.json").read_text())
    assert graph.validate(cmd["data"]["workflow"]["nodes"], registry.node_schemas(values)) == []


@needs_mlx
def test_ltx_lora_block_is_generated_from_the_transformer_and_equals_the_fixture():
    """modules: the LTX transformer's Linears built from the header's config (no weights read); prefused is filled
    from the shared index (the fixture's id is a placeholder: the real one is the file's SHA-256)."""
    if not (pack.models_root() / "ltx-2.5/diffusion_models").is_dir():
        pytest.skip("the LTX-2.5 weights are not on this host")
    values, _, p = ltx()
    want = json.loads((FIXTURES / "ltx.models.valid.json").read_text())["models"][0]["lora"]
    got = values["models"]["models"][0]["lora"]
    assert {k: v for k, v in got.items() if k != "prefused"} == {
        k: v for k, v in want.items() if k != "prefused"
    }
    assert got["prefused"] == []  # until registry.fill_prefused
    f = pack.models_root() / "ltx-2.5/loras/ltx-2.5-22b-distilled-lora-450-bf16.safetensors"
    registry.fill_prefused(values, [p], {f: "ab" * 32})
    assert got["prefused"] == [{"lora_id": "ab" * 32, "variant": "distilled"}]
    registry.fill_prefused(values, [p], {})
    assert got["prefused"] == []  # the file is not on this host: no entry


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
def test_steps_take_only_the_whole_distilled_table_of_the_role():
    """values / values_by (contracts README, Video: steps): refused by the graph rules, and by the pack's own check
    when a graph reaches it anyway."""
    from packs.ltx import mlx as ltx_mlx

    values, _, _ = ltx()
    nodes = json.loads(json.dumps(values["workflows"]["workflows"][0]["nodes"]))
    nodes["sample2"]["params"]["steps"] = 8
    nodes["sample1"]["params"]["steps"] = 4
    assert graph.validate(nodes, registry.node_schemas(values)) == [
        "sample1.steps: must be one of 3, 8", "sample2.steps: must be one of 3"]  # fmt: skip
    wired = {"model", "cond", "seed", "latent"}
    assert ltx_mlx.check_sample({"role": "stage2", "steps": 8}, wired) == ".steps: must be one of 3"
    assert ltx_mlx.check_sample({"role": "stage2", "steps": 3}, wired) is None


@needs_mlx
def test_frames_off_the_grid_is_param_invalid_with_the_contract_text():
    values, _, _ = ltx()
    nodes = json.loads(json.dumps(values["workflows"]["workflows"][0]["nodes"]))
    nodes["duration"]["params"]["frames"] = 50
    assert graph.validate(nodes, registry.node_schemas(values)) == [
        "duration.frames: 50 is not on the frame grid 9 + 8k up to 321"
    ]
    nodes["duration"]["params"]["frames"] = "auto"
    assert graph.validate(nodes, registry.node_schemas(values)) == []


@needs_mlx
def test_an_image_to_video_run_needs_one_keyframe():
    values, _, _ = ltx()
    nodes = json.loads(json.dumps(values["workflows"]["workflows"][1]["nodes"]))
    schemas = registry.node_schemas(values)
    assert graph.validate(nodes, schemas, run=False) == []  # the template holds no take
    assert graph.validate(nodes, schemas) == ["keyframes.images: at least 1 items"]


def test_auto_frames_follow_the_contract_fixture():
    fx = json.loads((CONTRACTS / "fixtures/video/auto-frames.json").read_text())
    for c in fx["cases"]:
        spec = fx["param"] | {"max": c["max"], "auto": {"min_s": c["min_s"], "max_s": c["max_s"]}}
        assert graph.auto_frames(c["seconds"], c["fps"], spec) == c["frames"], c


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
    video = ltx_mlx.noised_state((1, fhw[0] * fhw[1] * fhw[2], 128), [], fhw, pos, g["seed"], 1.0)
    audio = ltx_mlx.noised_state(
        (1, t, 128), [], fhw, ltx_mlx.compute_audio_positions(t), g["seed"] + 1, 1.0
    )
    stage = g["stages"][0]
    assert list(pos.shape) == stage["video_positions"] and stage["sigmas"] == ltx_mlx.STAGE1_SIGMAS
    assert (sha(video.latent), sha(audio.latent)) == (
        stage["video_in"]["sha256_f32"],
        stage["audio_in"]["sha256_f32"],
    )
    assert g["stages"][1]["sigmas"] == ltx_mlx.STAGE2_SIGMAS


@needs_mlx
def test_an_image_on_frame_0_keeps_the_first_frame_marked(on_cpu):
    """VideoConditionByLatentIndex keeps `keyframes_mask` (the vendored apply dropped it before 0.18; GOTCHAS
    2026-11-20): latent frame 0's tokens are marked, the rest and a later keyframe's appended tokens are not."""
    from packs.ltx import mlx as ltx_mlx

    fhw = (3, 2, 2)
    hw = fhw[1] * fhw[2]
    pos = ltx_mlx.compute_video_positions(*fhw, frame_rate=24.0)
    first = ltx_mlx.VideoConditionByLatentIndex(
        frame_indices=[0], clean_latent=mx.zeros((1, hw, 128)), strength=1.0
    )
    later = ltx_mlx.VideoConditionByKeyframeIndex(
        frame_idx=8,
        keyframe_latent=mx.zeros((1, hw, 128)),
        spatial_dims=fhw,
        frame_rate=24.0,
        strength=1.0,
    )
    for conds, appended in (([first], 0), ([first, later], hw)):
        state = ltx_mlx.noised_state((1, 3 * hw, 128), conds, fhw, pos, 42, 1.0)
        assert state.keyframes_mask is not None
        marks = state.keyframes_mask.reshape(-1).tolist()
        assert marks == [1.0] * hw + [0.0] * (2 * hw + appended)


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


# --- stage B against the goldens' recorded inputs, still without weights (tests/golden/ltx-*/<case>.json)

GOLDENS = ROOT / "tests/golden"


def golden(path: str) -> dict:
    return json.loads((GOLDENS / f"{path}.json").read_text())


@needs_mlx
@pytest.mark.parametrize("case", ["i2v-448x256-49f-s42", "kf-448x256-49f-s42"])
def test_keyframe_images_are_preprocessed_as_the_goldens(case, on_cpu):
    """RGB → H.264 round trip at the generation's CRF (18 on 2.5, yuv420p) → bilinear cover on float32 + centre
    crop → x / 127.5 - 1 bf16, per stage grid, bit for bit (the round trip depends on the host's ffmpeg / libx264
    build, as the goldens' did)."""
    from PIL import Image

    from packs.ltx import mlx as ltx_mlx

    g = golden(f"ltx-image-to-video/{case}")
    assert {m["crf"] for m in g["images"]} == {ltx_mlx.image_crf((2, 5, 0))}
    rgb = {m["file"]: ltx_mlx.crf_round_trip(Image.open(GOLDENS / "ltx-image-to-video" / m["file"]), m["crf"])
           for m in g["images"]}  # fmt: skip
    for row in g["image_preprocess"]:
        px = ltx_mlx.to_pixels(rgb[row["file"]], row["height"], row["width"])
        assert sha(px) == row["pixels"]["sha256_f32"], (row["file"], row["height"])
    want = g["image_conditionings"][0]["items"][-1]
    if (
        want["type"] == "VideoConditionByKeyframeIndex"
    ):  # the appended frame's positions at the stage-1 grid
        kf = ltx_mlx.VideoConditionByKeyframeIndex(
            want["frame_idx"], mx.zeros((1, 28, 128)), (7, 4, 7), 24.0
        )
        assert sha(kf.keyframe_positions) == want["positions"]["sha256_f32"]


@needs_mlx
def test_keyframe_positions_follow_the_contract_fixture():
    """The runner's check on the resolved length (contracts/fixtures/video/keyframes.json, fixed-length cases)."""
    from packs.ltx import mlx as ltx_mlx

    for c in json.loads((CONTRACTS / "fixtures/video/keyframes.json").read_text())["cases"]:
        nodes = c["workflow"]["nodes"]
        d, kf = nodes["duration"]["params"], nodes.get("keyframes", {})
        if d["frames"] == "auto" or kf.get("bypass") or "keyframes" not in nodes:
            continue
        rows = tuple(
            (None, r.get("seconds", 0), r.get("strength", 1)) for r in kf["params"]["images"]
        )
        cond = ltx_mlx.Cond(None, None, images=rows, images_at="keyframes.images")
        try:
            ltx_mlx.keyframe_frames(cond, d["frames"], d["fps"])
            got = []
        except RunError as e:
            got = [str(e)]
        assert got == c["errors"][:1], c["description"]


@needs_mlx
def test_prompt_beats_give_the_goldens_ranges_and_relay_masks(on_cpu):
    """Prompt Relay without weights: the combined prompt and token ranges (the tokenizer is read from the text
    encoder file's assets) and each stage's mask, bit for bit."""
    from packs.ltx import mlx as ltx_mlx

    f = pack.models_root() / "ltx-2.5/text_encoders/gemma4-12b-with-proj-ltx-2.5-bf16.safetensors"
    if not f.is_file():
        pytest.skip("the LTX-2.5 text encoder is not on this host")
    g = golden("ltx-prompt-beats/beats-448x256-49f-s42")
    tok = ltx_mlx._tokenizer(f, f.stat().st_size, f.stat().st_mtime_ns)
    text, ranges = ltx_mlx.map_token_ranges(
        tok, g["prompt"], [s["text"] for s in g["segments"]], max_length=1024
    )
    assert (text, [list(r) for r in ranges]) == (g["prompt_relay"]["encoded_prompt"], g["prompt_relay"]["token_ranges"])  # fmt: skip
    seconds = tuple(
        s["length"] * 8 / g["fps"] if s["length"] else 0.0 for s in g["segments"]
    )  # latent frames → s
    cond = ltx_mlx.Cond(
        mx.zeros((1, 1024, 8)), None, seconds, tuple(ranges), beats_at="encode.beats"
    )
    for stage, fhw in zip(g["stages"], [(7, 4, 7), (7, 8, 14)], strict=True):
        mask = ltx_mlx.relay_mask(cond, fhw, stage["video_tokens_total"], g["fps"])
        assert sha(mask) == stage["video_cross_attention_mask"]["sha256_f32"]
        # plan 19 phase 2: the audio mask too (upstream's distilled default, strength 1); none for a frozen audio
        audio = SimpleNamespace(latent=mx.zeros(stage["audio_positions"][:2] + [128]), frozen=False)
        amask = ltx_mlx.relay_audio_mask(cond, fhw, audio, g["fps"])
        assert sha(amask) == stage["audio_cross_attention_mask"]["sha256_f32"]
        frozen = SimpleNamespace(latent=audio.latent, frozen=True)
        assert ltx_mlx.relay_audio_mask(cond, fhw, frozen, g["fps"]) is None
    with pytest.raises(
        RunError, match=r"encode\.beats\[2\]: no frames left for this beat \(the clip has 2 latent"
    ):
        ltx_mlx.beat_lengths(replace(cond, beats=(3.0, 0.0)), 2, 24.0)


@needs_mlx
def test_vae_encoder_keys_are_renamed():
    from packs.ltx import mlx as ltx_mlx

    r = ltx_mlx.rename
    assert r("vae_encoder", "encoder.conv_in.conv.weight") == "conv_in.conv.weight"
    assert (
        r("vae_encoder", "per_channel_statistics.mean-of-means")
        == "per_channel_statistics.mean_of_means"
    )
    assert r("vae_encoder", "decoder.conv_in.conv.weight") is None


@needs_mlx
def test_sample_peak_counts_the_tokens_keyframes_append():
    """Each keyframe appends a latent frame of tokens and encodes its image at the stage's grid (model.yaml)."""
    _, impls, _ = ltx()
    peak = impls["ltx/sample"].peak
    mp = 1024 * 576 / 1e6
    at = {"mp": mp, "frames": 121, "role": "stage2", "steps": 3, "mp_in": 1}
    extra = pack.evaluate(peak, at | {"images": 8}) - pack.evaluate(peak, at | {"images": 0})
    assert extra == pytest.approx(8 * (0.7 + 1.5 * mp) + 0.26 * 8 * mp)
    assert (
        pack.evaluate(impls["ltx/keyframes"].peak, {"mp_in": 50, "images": 8, "mp": 0, "frames": 0})
        == 2.0
    )


@needs_mlx
def test_keyframes_round_trip_each_take_once_and_keep_at_most_the_largest_canvas(monkeypatch):
    from PIL import Image

    from packs.ltx import mlx as ltx_mlx

    trips = []
    real = ltx_mlx.crf_round_trip
    monkeypatch.setattr(
        ltx_mlx, "crf_round_trip", lambda img, crf: trips.append((img.size, crf)) or real(img, crf)
    )
    big, small = (
        Image.new("RGB", (4000, 2400), (200, 10, 10)),
        Image.new("RGB", (640, 400), (10, 200, 10)),
    )
    c = Ctx(progress=None, output=None, check=lambda: None)
    c.node = "keyframes"
    cond = ltx_mlx.Cond(mx.zeros((1, 1, 1)), mx.zeros((1, 1, 1)), crf=18)
    rows = [{"image": small, "seconds": 0, "strength": 1}, {"image": big, "seconds": 1, "strength": 1},
            {"image": small, "seconds": 2, "strength": 0.5}]  # fmt: skip
    got = ltx_mlx.keyframes(c, {"images": rows}, {"cond": cond})["cond"].images
    assert trips == [
        ((640, 400), 18),
        ((4000, 2400), 18),
    ]  # the repeated take once, at the Cond's CRF
    assert [r[0].shape for r in got] == [(400, 640, 3), (1920, 3200, 3), (400, 640, 3)]
    assert got[0][0] is got[2][0] and [r[1:] for r in got] == [(0.0, 1.0), (1.0, 1.0), (2.0, 0.5)]


def test_the_generation_rules_come_from_model_version():
    """detect_model_version / detect_params / should_use_ancestral_sampler: CRF 18 from 2.4 on, ancestral from 2.5."""
    from packs.ltx import common

    v = common.model_version
    assert [v({"model_version": x}) for x in ("2.5.0", "2.4-rc2", "2.3.rc1", "", None)] == [(2, 5, 0), (2, 4), (2, 3), (), ()]  # fmt: skip
    assert [common.image_crf(x) for x in ((2, 5, 0), (2, 4), (2, 3), ())] == [18, 18, 33, 33]
    assert [common.ancestral(x) for x in ((2, 5, 0), (2, 4), ())] == [True, False, False]


def test_keyframe_images_decode_and_round_trip_as_upstream():
    """media_io.decode_image + preprocess: EXIF orientation, ICC → sRGB, alpha dropped, odd sides cropped, CRF 0 kept."""
    import io

    import numpy as np
    from PIL import Image, ImageCms

    from packs.ltx import common

    img = Image.new("RGB", (5, 3), (250, 10, 10))
    exif = img.getexif()
    exif[0x0112] = 6  # rotate 270 (upright as the camera held it)
    buf = io.BytesIO()
    img.save(buf, "PNG", exif=exif.tobytes())
    assert common.decode_image(Image.open(buf)).shape == (5, 3, 3)
    tagged = Image.new("RGB", (4, 4), (90, 140, 200))
    tagged.info["icc_profile"] = ImageCms.ImageCmsProfile(ImageCms.createProfile("sRGB")).tobytes()
    assert (
        np.abs(common.decode_image(tagged).astype(int) - (90, 140, 200)).max() <= 1
    )  # converted, sRGB to sRGB
    rgba = Image.new("RGBA", (4, 4), (10, 200, 10, 0))
    assert common.decode_image(rgba).tolist() == np.full((4, 4, 3), (10, 200, 10)).tolist()
    bad = Image.new("RGB", (4, 4), (1, 2, 3))
    bad.info["icc_profile"] = b"not a profile"
    assert (
        common.decode_image(bad).tolist() == np.full((4, 4, 3), (1, 2, 3)).tolist()
    )  # a plain RGB conversion
    odd = Image.new("RGB", (65, 33), (120, 60, 30))
    assert common.crf_round_trip(odd, 18).shape == (32, 64, 3)
    assert common.crf_round_trip(odd, 0).shape == (33, 65, 3)


@needs_mlx
def test_the_resize_is_upstream_torch_bilinear():
    """resize_and_center_crop against Lightricks' torch interpolate(bilinear, align_corners=False) outputs
    (ltx-2-studio tests/fixtures/image_preprocess_resize_golden.npz: 4 cases, down and up)."""
    import numpy as np

    from packs.ltx import mlx as ltx_mlx

    g = np.load(ROOT / "tests/fixtures/ltx_image_resize_golden.npz")
    for i in range(4):
        want = g[f"out_{i}"]
        got = ltx_mlx.resize_and_center_crop(g[f"src_{i}"], *want.shape[:2])
        assert got.dtype == np.float32 and np.abs(got - want).max() < 1e-3, i


@needs_mlx
def test_a_bake_keeps_the_scales_and_biases_dtype():
    """fuse_loras f3c5bad: requantized from float32, the layer keeps its bf16 scales and biases (else
    quantized_matmul runs the whole layer in float32)."""
    from core import loaders_mlx

    w = mx.random.normal((8, 128)).astype(mx.bfloat16)
    q, s, b = mx.quantize(w, group_size=64, bits=8)
    weights = {"l.weight": q, "l.scales": s, "l.biases": b}
    a, bb = mx.ones((2, 128)), mx.ones((8, 2))
    loaders_mlx._bake(weights, "l", [(a, bb, 0.5)], 8, True)
    assert weights["l.scales"].dtype == weights["l.biases"].dtype == mx.bfloat16
    assert not mx.array_equal(weights["l.weight"], q)


@needs_mlx
def test_a_baked_entry_counts_its_float32_scales():
    from core import loaders_mlx

    q = nn.QuantizedLinear(
        128, 8, group_size=64, bits=8
    )  # quantized from float32: float32 scales and biases
    assert q.scales.dtype == mx.float32
    assert (
        loaders_mlx.extra_gb(q) * 2**30 == 2 * 8 * 2 * 2
    )  # 8 x 2 groups, scales and biases, 2 bytes more each
    q.scales, q.biases = q.scales.astype(mx.bfloat16), q.biases.astype(mx.bfloat16)
    assert loaders_mlx.extra_gb(q) == 0


@needs_mlx
def test_the_metal_cache_is_capped_and_measure_is_the_process_footprint():
    """At MLX's default cache limit one VAE decode grew the cache to 27 GB beside its 9 GB peak (GOTCHAS.md)."""
    import mlx.core as mx

    from core import loaders_mlx
    from core.residency import footprint_gb

    limit = mx.set_cache_limit(0)  # returns the limit the import set
    mx.set_cache_limit(limit)
    assert limit <= loaders_mlx.CACHE_GB * 2**30
    assert loaders_mlx.measure_gb() == pytest.approx(footprint_gb(), abs=0.05)


# --- M7 pack 1: retake, extend, audio to video (no weights: the maps, the schedule, every state the loops start
# from equal the goldens' recorded shas; the variant checks before anything loads; the memory plans)


@needs_mlx
def test_media_maps_follow_the_contract_fixture():
    from packs.ltx import mlx as ltx_mlx

    fx = json.loads((CONTRACTS / "fixtures/video/sources.json").read_text())
    fns = {"retake": ltx_mlx.retake_map, "extend": ltx_mlx.extend_map, "audio": ltx_mlx.audio_map}
    for c in fx["cases"]:
        assert fns[c["kind"]](*c["args"]) == c["expect"], c["description"]


class Probe:
    """What a node function sees of a run, without one: progress dropped, components refused."""

    node = "retake"

    def check(self):
        pass

    def progress(self, *a):
        pass

    def component(self, model, name):
        return f"<{name}>"


def source_latent(g: dict):
    """A Latent shaped like the golden's source (the t2v golden: 7 x 8 x 14 latent, 51 audio tokens)."""
    from packs.ltx import mlx as ltx_mlx

    f, h, w = g["source"]["video_latent"]["shape"][2:]
    t = g["source"]["audio_latent"]["shape"][2]
    return ltx_mlx.Latent(mx.zeros((1, f * h * w, 128), dtype=mx.bfloat16), mx.zeros((1, t, 128)), (f, h, w),
                          g["source"]["meta"]["num_frames"], g["source"]["meta"]["frame_rate"],
                          source_map={"take_id": "t"})  # fmt: skip


def edited_states(monkeypatch, fn, params: dict, latent):
    from packs.ltx import mlx as ltx_mlx

    seen = {}

    def edit(ctx, params, inputs, video, audio, src, fhw, frames, smap):
        seen.update(video=video, audio=audio, fhw=fhw, frames=frames, smap=smap)
        return {}

    monkeypatch.setattr(ltx_mlx, "_edit", edit)
    fn(Probe(), params, {"latent": latent})
    return seen


@needs_mlx
@pytest.mark.parametrize(
    "case", ["ltx-retake/retake-448x256-49f-s42", "ltx-extend/extend-448x256-49f+8-s42"]
)
def test_retake_and_extend_states_are_the_goldens(case, on_cpu, monkeypatch):
    """The masks (video float32 for retake, bf16 for extend; audio bf16), the keyframes mask on the first latent
    frame, the positions at the source's fps and the sigmas of the 30-step schedule over the whole clip."""
    from packs.ltx import mlx as ltx_mlx

    g = golden(case)
    loop, src = g["loops"][0], source_latent(g)
    if g["kind"] == "retake":
        fps = g["source"]["meta"]["frame_rate"]
        params = {
            "span": {"start": 8 * g["start"] / fps, "end": 8 * g["end"] / fps},
            "audio": g["regenerate_audio"],
        }
        seen = edited_states(monkeypatch, ltx_mlx.retake, params, src)
        assert seen["smap"] == {
            "offset_s": 0,
            "new_in_s": 9 / 24,
            "new_out_s": 33 / 24,
        }  # pixels 9-32
    else:
        params = {"seconds": 0.3, "direction": g["direction"]}
        seen = edited_states(monkeypatch, ltx_mlx.extend, params, src)
        assert seen["frames"] == 57 and seen["smap"]["new_in_s"] == 49 / 24
    v, a = seen["video"], seen["audio"]
    for (part, k), x in {("video_in", "denoise_mask"): v.denoise_mask, ("video_in", "keyframes_mask"): v.keyframes_mask,
                         ("video_in", "positions"): v.positions, ("audio_in", "denoise_mask"): a.denoise_mask,
                         ("audio_in", "positions"): a.positions}.items():  # fmt: skip
        assert sha(x) == loop[part][k]["sha256_f32"], (part, k)
        assert str(x.dtype).split(".")[-1] == loop[part][k]["dtype"], (part, k)
    tokens = seen["fhw"][0] * seen["fhw"][1] * seen["fhw"][2]
    assert tokens == loop["video_in"]["tokens"]
    assert (
        max(
            abs(x - y)
            for x, y in zip(ltx_mlx.ltx2_schedule(30, tokens), loop["sigmas"], strict=True)
        )
        < 1e-6
    )


@needs_mlx
def test_audio_to_video_stage1_state_is_the_golden(on_cpu, monkeypatch):
    """Stage 1: noise from the seed at half size (the scalar blend), positions at the default 24 fps, the first
    latent frame marked, the audio frozen (mask 0); the schedule over the half-size tokens."""
    from packs.ltx import mlx as ltx_mlx

    g = golden("ltx-audio-to-video/a2v-448x256-49f-s42")
    loop, seen = g["loops"][0], {}

    def guided(ctx, x0, video, audio, cond, factories, sigmas, phase):
        seen.update(video=video, audio=audio, sigmas=sigmas, factories=factories)
        return video.latent, audio.latent

    monkeypatch.setattr(ltx_mlx, "_guided", guided)
    monkeypatch.setattr(ltx_mlx, "X0Model", lambda m: m)
    cond = ltx_mlx.Cond(mx.zeros((1, 4, 8)), mx.zeros((1, 4, 8)), neg_video=mx.zeros((1, 4, 8)),
                        neg_audio=mx.zeros((1, 4, 8)))  # fmt: skip
    audio = ltx_mlx.Latent(
        None, mx.zeros((1, 51, 128)), None, 49, 24.0, source_map={"take_id": "t"}, sound=(b"", 0)
    )
    inputs = {"model": None, "cond": cond, "seed": g["seed"], "canvas": {"width": 448, "height": 256},
              "duration": {"frames": 49, "fps": 24}, "audio": audio}  # fmt: skip
    out = ltx_mlx.sample_guided(Probe(), {"steps": 30, "cfg": 3.0, "stg": 1.0}, inputs)["latent"]
    v, a = seen["video"], seen["audio"]
    for (part, k), x in {("video_in", "latent"): v.latent, ("video_in", "denoise_mask"): v.denoise_mask,
                         ("video_in", "keyframes_mask"): v.keyframes_mask, ("video_in", "positions"): v.positions,
                         ("audio_in", "denoise_mask"): a.denoise_mask, ("audio_in", "positions"): a.positions}.items():  # fmt: skip
        assert sha(x) == loop[part][k]["sha256_f32"], (part, k)
    assert max(abs(x - y) for x, y in zip(seen["sigmas"], loop["sigmas"], strict=True)) < 1e-6
    vg, ag = (f.build_from_sigma(1.0) for f in seen["factories"])
    assert (
        vg.params.cfg_scale,
        vg.params.stg_blocks,
        ag.params.cfg_scale,
        ag.params.stg_scale,
    ) == (3.0, [28], 1.0, 0.0)
    assert out.sound == (b"", 0) and out.fhw == (7, 4, 7)


def dev_workflow(name: str) -> tuple[dict, dict, dict]:
    values, impls, _ = ltx()
    nodes = json.loads(
        json.dumps(next(w for w in values["workflows"]["workflows"] if w["id"] == name)["nodes"])
    )
    return nodes, impls, registry.node_schemas(values)


@needs_mlx
@pytest.mark.parametrize(
    ("name", "edit", "error"),
    [
        (
            "ltx-audio-to-video",
            lambda n: (
                n["model"]["params"].update(variant="dev"),
                n["sample2"]["in"].update(model="model.model"),
            ),
            "sample2: needs model variant distilled",
        ),
        (
            "ltx-text-to-video",
            lambda n: n["model"]["params"].update(variant="dev"),
            "sample1: needs model variant distilled",
        ),
    ],
)
def test_the_model_variant_is_checked_before_anything_loads(name, edit, error):
    nodes, impls, schemas = dev_workflow(name)
    for n in nodes.values():
        n.pop("only", None)  # studio drops it from a command
        n.pop("bypass", None) if n["kind"] != "ltx/keyframes" else None
    nodes.pop("keyframes", None)
    for n in (
        nodes.values()
    ):  # keyframes bypassed: studio splices it out (its input feeds its consumers)
        for port, ref in n.get("in", {}).items():
            if ref == "keyframes.cond":
                n["in"][port] = "encode.cond"
    nodes["prompt"]["params"]["text"] = "a cat"
    edit(nodes)
    with pytest.raises(RunError, match=error):
        run_graph(nodes, impls, schemas)


@needs_mlx
def test_distill_is_the_dev_model_with_the_distilled_lora_baked():
    """A second transformer entry (its own residency key, the LoRA listed); stage 2 accepts it."""
    from packs.ltx import mlx as ltx_mlx

    _, _, p = ltx()
    dev = pack.ModelRef(p, "dev", "int8")
    baked = ltx_mlx.distill(None, {}, {"model": dev})["model"]
    assert baked.variant == "dev" and [(u.strength, u.mode) for u in baked.loras] == [(1.0, "bake")]
    assert ltx_mlx.distilled_lora(baked) and not ltx_mlx.distilled_lora(dev)
    if baked.files("transformer"):
        assert (
            baked.key("transformer")[:4] == dev.key("transformer")
            and len(baked.key("transformer")) == 5
        )
    assert (
        ltx_mlx.check_sample({"role": "stage2", "steps": 3}, {"latent"}, {"model": baked}) is None
    )
    assert ltx_mlx.check_sample({"role": "stage1", "steps": 8}, {"canvas", "duration"}, {"model": baked}) == \
        ": needs model variant distilled"  # fmt: skip


@needs_mlx
def test_distilled_actions_pass_distill_through_and_key_the_encode_by_variant():
    """Plan 19 23.4 phase 1: on Distilled, ltx/distill is the identity (the LoRA is fused in) and its check passes;
    encode-guided's cache key holds the variant (Dev's Conditioning carries the negative prompt, Distilled's not)."""
    from packs.ltx import mlx as ltx_mlx

    _, _, p = ltx()
    distilled = pack.ModelRef(p, "distilled", "int8")
    assert ltx_mlx.distill(None, {}, {"model": distilled})["model"] is distilled
    assert ltx_mlx.check_distill({}, {"model"}, {"model": distilled}) is None
    nodes, impls, schemas = dev_workflow("ltx-retake")
    nodes["prompt"]["params"]["text"] = "a cat"
    nodes, _ = graph.resolve(nodes, schemas)
    keys = graph.keys(nodes, impls, schemas, "o")
    nodes["model"]["params"]["variant"] = "dev"
    dev = graph.keys(nodes, impls, schemas, "o")
    assert keys["encode"] != dev["encode"] and keys["source"] == dev["source"]
    # Extend on Distilled (the default) offers after only: the graph check refuses before (a cross-node values_by)
    nodes, _, _ = dev_workflow("ltx-extend")
    nodes["extend"]["params"]["direction"] = "before"
    nodes, _ = graph.resolve(nodes, schemas)
    assert "extend.direction: must be one of after" in graph.validate(nodes, schemas, run=False)
    nodes["model"]["params"]["variant"] = "dev"
    assert not [e for e in graph.validate(nodes, schemas, run=False) if "direction" in e]


@needs_mlx
def test_dev_memory_plans_cover_only_the_runs_that_can_use_it():
    """Retake and extend in one item at the goldens' source size, audio to video its own; text to video is planned on
    the distilled variant only, and nothing is planned at bf16 for the dev templates (`only`)."""
    values, impls, p = ltx()
    schemas = registry.node_schemas(values)
    if not pack.ModelRef(p, "dev", "int8").files("transformer"):
        pytest.skip("the dev transformer is not on this host")
    plans = registry.memory_plans(p, "dev", "int8", impls, schemas)
    assert [x.get("workflow") for x in plans] == [None, "ltx-audio-to-video"]
    assert plans[0]["planned_at"] == {"mp": 0.1147, "frames": 97}  # 49 + the default 2 s extend
    assert {c["variant"] for c in plans[0]["components"] if c["component"] == "transformer"} == {
        "dev"
    }
    assert registry.memory_plans(p, "dev", "bf16", impls, schemas) == []
    # from API 1.39 retake, extend and audio to video run on Distilled too: audio to video gets its own item there
    assert [
        x.get("workflow") for x in registry.memory_plans(p, "distilled", "int8", impls, schemas)
    ] == [
        None,
        "ltx-audio-to-video",
    ]
