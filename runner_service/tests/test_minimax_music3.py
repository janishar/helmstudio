"""The minimax_music3 pack without a GPU: its models entry equals the canonical fixture (nodes and workflows in
test_packs), the instrumental rule, the download plan on the fake hub (the diffusers folders only, never SGLang's),
and the three stages on tiny random components on the CPU: the same audio as diffusers' one-call pipeline, stereo
PCM, a cancel point every frame."""

import json
import types

import pytest
from conftest import CONTRACTS
from fake_hub import FakeHub, Repo

import config
from core import download, models
from core import pack as packs
from core.download import Progress
from core.graph import Cancelled, RunError

FIXTURES = CONTRACTS / "fixtures/registry"
mm = pytest.importorskip("packs.minimax_music3.torch", reason="the torch env")
torch = pytest.importorskip("torch")


def fixture(key: str) -> dict:
    return json.loads((FIXTURES / f"minimax-music3.{key}.valid.json").read_text())


@pytest.fixture(scope="module")
def values():
    from core import registry

    return registry.build("torch")[0]


def test_models_entry_matches_the_fixture(values):
    (got,) = [m for m in values["models"]["models"] if m["family"] == "minimax-music3"]
    (want,) = fixture("models")["models"]
    keys = ("id", "family", "title", "media", "runtimes", "sound", "licences")
    assert {k: got.get(k) for k in keys} == {k: want.get(k) for k in keys}
    vkeys = ("id", "title", "precisions", "source", "dir")
    assert [{k: v[k] for k in vkeys} for v in got["variants"]] == [{k: v[k] for k in vkeys} for v in want["variants"]]
    for g, w in zip(got["variants"], want["variants"], strict=True):
        if g["presence"] == "present":  # on a host with the weights: the same sizes and memory plans
            assert (g["components"], g["memory"]) == (w["components"], w["memory"])


@pytest.mark.parametrize(
    ("style", "lyrics", "instrumental", "want"),
    [
        ("indie pop", "[verse]\nla la", False, ("indie pop", "[verse]\nla la")),
        (
            "film score.",
            "[verse]\nla",
            True,
            ("film score. Instrumental, no vocals.", "[instrumental]"),
        ),
        ("film score", "  ", False, ("film score. Instrumental, no vocals.", "[instrumental]")),
    ],
)
def test_instrumental_says_so_and_sings_only_the_tag(style, lyrics, instrumental, want):
    assert mm.song_text("lm", style, {"lyrics": lyrics, "instrumental": instrumental}) == want


def test_an_empty_style_is_refused():
    with pytest.raises(RunError, match="lm: describe the music"):
        mm.song_text("lm", " ", {"lyrics": "", "instrumental": True})


# --- the download plan


SGLANG = [
    "qwen_7B/qwen_7B/model-00000-of-00048.safetensors",
    "flowmatching_vae.pth",
    "dav.pth",
    "README.md",
]


def hub_files() -> dict[str, bytes]:
    """The repo's layout in small: every diffusers folder and the SGLang-Omni files beside them."""
    blob = bytes(range(256)) * 40
    out = {"modular_model_index.json": b"{}", "LICENSE": b"MiniMax-Music3 COMMUNITY LICENSE\n"}
    for c in ("language_model", "rvq_depth_decoder", "condition_encoder", "transformer", "vocoder"):
        out |= {f"{c}/config.json": b"{}", f"{c}/model.safetensors": blob + c.encode()}
    out |= {"tokenizer/tokenizer.json": b"{}", "scheduler/scheduler_config.json": b"{}"}
    return out | {p: blob for p in SGLANG}


def test_the_download_fetches_the_diffusers_folders_only(tmp_path, monkeypatch):
    monkeypatch.setattr(config, "MODELS", tmp_path / "models")
    monkeypatch.setattr(models, "hashes", models.Hashes())
    (tmp_path / "models").mkdir()
    (p,) = [p for p, _ in packs.for_env("torch") if p.name == "minimax_music3"]
    files = hub_files()
    src = p.spec["variants"]["base"]["source"]
    hub = FakeHub({src["repo"]: Repo(files, commit=src["revision"])})
    with hub:
        download.download(p, "base", endpoint=hub.url, token=None, cache=tmp_path / "cache", host="t",
                          progress=Progress())  # fmt: skip
    root = tmp_path / "models/MiniMax-Music3"
    got = sorted(str(f.relative_to(root)) for f in root.rglob("*") if f.is_file() and ".cache" not in f.parts)
    assert got == sorted(k for k in files if k not in SGLANG)
    assert not [r for r in hub.requests if any(s in r[1] for s in SGLANG)]  # never asked for


# --- the stages on tiny components (CPU)


class Tokens:
    """Stands in for the checkpoint's tokenizer: ids from the text's bytes (the prompt template is the library's)."""

    def __call__(self, text, return_tensors="pt"):
        ids = [151643 + b % 20 for b in text.encode()[:60]] + [151669]
        return {"input_ids": torch.tensor([ids])}


def tiny() -> dict:
    from diffusers import (
        MiniMaxMusic3ConditionEncoder,
        MiniMaxMusic3RVQDepthDecoder,
        MiniMaxMusic3Transformer1DModel,
        MiniMaxMusic3Vocoder,
    )
    from transformers import Qwen3Config, Qwen3ForCausalLM

    torch.manual_seed(0)
    lm = Qwen3ForCausalLM(Qwen3Config(vocab_size=168064, hidden_size=16, intermediate_size=32, num_hidden_layers=1,
                                      num_attention_heads=2, num_key_value_heads=1, head_dim=8))  # fmt: skip
    rvq = MiniMaxMusic3RVQDepthDecoder(audio_vocab_size=16, hidden_size=16, intermediate_size=32,
                                       max_position_embeddings=16, num_attention_heads=2, num_codebooks=8,
                                       num_layers=1)  # fmt: skip
    ce = MiniMaxMusic3ConditionEncoder(condition_hidden_dim=16, num_condition_layers=8, out_dim=8)
    dit = MiniMaxMusic3Transformer1DModel(attention_head_dim=8, condition_dim=8, ff_inner_dim=32,
                                          fourier_embedding_dim=16, in_channels=8, num_attention_heads=2,
                                          num_layers=1, rotary_dim=4)  # fmt: skip
    voc = MiniMaxMusic3Vocoder(decoder_hidden_dim=16, decoder_input_dim=8, latent_channels=8)
    out = {"language_model": lm, "rvq_depth_decoder": rvq, "condition_encoder": ce, "transformer": dit, "vocoder": voc}
    return {k: m.eval().requires_grad_(False) for k, m in out.items()}


@pytest.fixture(scope="module")
def parts(tmp_path_factory):
    from diffusers import FlowMatchEulerDiscreteScheduler

    folder = tmp_path_factory.mktemp("mm3")
    FlowMatchEulerDiscreteScheduler(invert_sigmas=True, num_train_timesteps=1).save_config(folder / "scheduler")
    return tiny(), folder


def run(parts, monkeypatch, seconds=2.0, seed=3, check=lambda: None):
    comps, folder = parts
    monkeypatch.setattr(mm, "tokenizer", lambda f: Tokens())
    model = types.SimpleNamespace(folder=lambda component=None: folder)
    steps = []
    ctx = types.SimpleNamespace(node="lm", device="cpu", check=check, component=lambda m, c: comps[c],
                                progress=lambda *a: steps.append(a))  # fmt: skip
    params = {"lyrics": "[verse]\nla la", "instrumental": False, "seconds": seconds}
    cond = mm.lm(ctx, params, {"model": model, "prompt": "pop", "seed": seed})["cond"]
    latent = mm.flow(ctx, {}, {"model": model, "cond": cond})["latent"]
    return mm.vocode(ctx, {}, {"model": model, "latent": latent})["audio"], steps


def test_the_stages_give_the_one_call_pipelines_audio(parts, monkeypatch):
    import numpy as np
    from diffusers.modular_pipelines import MiniMaxMusic3Blocks, MiniMaxMusic3ModularPipeline

    audio, steps = run(parts, monkeypatch)
    comps, folder = parts
    pipe = MiniMaxMusic3ModularPipeline(blocks=MiniMaxMusic3Blocks())
    pipe.update_components(tokenizer=Tokens(), scheduler=mm.scheduler(folder), **comps)
    want = pipe(prompt="pop", lyrics="[verse]\nla la", audio_duration=2.0,
                generator=torch.Generator("cpu").manual_seed(3), output="audios")[0]  # fmt: skip
    assert audio["wav"] == mm.pcm16(want) and audio["channels"] == 2 and audio["sample_rate"] == 44100
    assert audio["duration_s"] == want.shape[-1] / 44100 and len(audio["wav"]) == want.shape[-1] * 4
    phases = [s[2] for s in steps]
    assert phases.count("composing") >= 2 and "flow matching" in phases and phases[-1] == "decoding"
    assert np.abs(want).max() > 0


def test_a_cancel_stops_the_language_model_within_a_frame(parts, monkeypatch):
    calls = []

    def check():
        calls.append(1)
        if len(calls) == 3:  # the prompt, then two frames
            raise Cancelled

    with pytest.raises(Cancelled):
        run(parts, monkeypatch, seconds=10.0, check=check)
    assert len(calls) == 3
