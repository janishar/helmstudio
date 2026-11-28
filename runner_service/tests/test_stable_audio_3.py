"""The stable_audio_3 pack without a GPU: its models entry equals the canonical fixture (nodes and workflows in
test_packs), the download plan on the fake hub (gated: the token is asked for; must_accept; only allow_patterns),
the rename split of the one checkpoint (on the real headers when the weights are here), the qkv reorder, the
reference's latent sizes, the chunked SAME attention on tiny tensors and the stages' refusals."""

import json
import types

import pytest
from conftest import CONTRACTS
from fake_hub import FakeHub, Repo

import config
from core import download, models
from core import pack as packs
from core.download import JobError, Progress
from core.graph import RunError

sa3 = pytest.importorskip("packs.stable_audio_3.torch", reason="the torch env")
import torch

FIXTURES = CONTRACTS / "fixtures/registry"


def test_models_entry_matches_the_fixture(torch_values):
    (got,) = [m for m in torch_values["models"]["models"] if m["family"] == "stable-audio-3"]
    (want,) = json.loads((FIXTURES / "stable-audio-3.models.valid.json").read_text())["models"]
    keys = ("id", "family", "title", "media", "runtimes", "sound")
    assert {k: got[k] for k in keys} == {k: want[k] for k in keys}
    (g,), (w,) = got["variants"], want["variants"]
    assert {k: g[k] for k in ("id", "title", "precisions", "source", "dir")} == {
        k: w[k] for k in ("id", "title", "precisions", "source", "dir")
    }
    assert w["source"]["gated"] and w["source"]["must_accept"]
    if g["presence"] == "present":  # on a host with the weights: the same sizes and memory plans
        assert (g["components"], g["memory"]) == (w["components"], w["memory"])


# --- the download plan, against the fake hub (nothing reaches the internet or the models folder)

FILES = {"LICENSE.md": b"Stability AI Community License", "LICENSE_GEMMA.md": b"Gemma Terms", "NOTICE": b"n",
         "model.safetensors": bytes(range(256)) * 300, "model_config.json": b"{}", "README.md": b"no",
         "Stable_Audio_3.0_Thumbnail_1x1.png": b"png", "t5gemma-b-b-ul2/config.json": b"{}",
         "t5gemma-b-b-ul2/model.safetensors": bytes(range(256))[::-1] * 200,
         "t5gemma-b-b-ul2/README.md": b"no", "t5gemma-b-b-ul2/generation_config.json": b"{}",
         "t5gemma-b-b-ul2/special_tokens_map.json": b"{}", "t5gemma-b-b-ul2/tokenizer.json": b"{}",
         "t5gemma-b-b-ul2/tokenizer.model": b"m", "t5gemma-b-b-ul2/tokenizer_config.json": b"{}"}  # fmt: skip
REPO = "stabilityai/stable-audio-3-small-sfx"


@pytest.fixture
def pack(tmp_path, monkeypatch):
    monkeypatch.setattr(config, "MODELS", tmp_path / "models")
    monkeypatch.setattr(models, "hashes", models.Hashes())
    (tmp_path / "models").mkdir()
    real = next(p for p, _ in packs.for_env("torch") if p.name == "stable_audio_3")
    spec = json.loads(json.dumps(real.spec))
    spec["variants"]["small-sfx"]["source"]["revision"] = "main"
    return packs.Pack("stable_audio_3", real.dir, spec, "torch", real.module)


def test_the_gated_download_asks_for_the_token_and_fetches_only_its_patterns(pack):
    with FakeHub({REPO: Repo(FILES, gated_token="hf_sa3")}) as hub:
        run = lambda token: download.download(
            pack,
            "small-sfx",
            endpoint=hub.url,
            token=token,
            cache=config.MODELS.parent / "cache",
            host="t",
            progress=Progress(),
        )
        with pytest.raises(JobError, match="is gated: put a Hub token") as e:
            run(None)
        assert e.value.cls == "gated"
        with pytest.raises(JobError, match="the token was refused"):
            run("hf_wrong")
        hub.requests.clear()
        run("hf_sa3")
        assert hub.requests and all(r[3] for r in hub.requests)  # the token went with every call
    folder = config.MODELS / "Stable-Audio-3-Small-SFX"
    got = sorted(str(p.relative_to(folder)) for p in folder.rglob("*") if p.is_file() and ".cache" not in p.parts)
    want = sorted(pack.spec["variants"]["small-sfx"]["source"]["allow_patterns"])
    assert got == want  # no README, thumbnail or generation config
    entry = packs.models_entry(pack)
    (v,) = entry["variants"]
    assert v["presence"] == "present" and v["source"]["must_accept"] and entry["sound"] == "sound"


def test_the_licence_is_shipped_with_the_pack(pack):
    licence = pack.spec["variants"]["small-sfx"]["source"]["licence"]
    text = (pack.dir / "licences" / f"{licence}.md").read_text()
    assert "Stability AI Community License" in text and "Gemma Terms of Use" in text


# --- the rename split and the module builds


def test_rename_splits_the_one_checkpoint():
    r = sa3.rename
    assert (
        r("dit", "model.model.transformer.layers.3.ff.ff.0.proj.weight") == "dit.transformer_blocks.3.ff.proj_in.weight"
    )
    assert r("dit", "model.model.transformer.layers.3.ff.ff.2.bias") == "dit.transformer_blocks.3.ff.proj_out.bias"
    assert r("dit", "model.model.transformer.project_in.weight") == "dit.proj_in.weight"
    assert r("dit", "model.model.transformer.memory_tokens") == "dit.memory_tokens"
    assert r("dit", "model.model.to_cond_embed.2.weight") == "dit.to_cond_embed.2.weight"
    assert r("dit", "conditioner.conditioners.prompt.padding_embedding") == "dit.prompt_padding_embedding"
    assert r("dit", "conditioner.conditioners.seconds_total.embedder.embedding.1.weight") == "duration.linear.weight"
    assert r("dit", "pretransform.model.bottleneck.bias") is None
    assert r("autoencoder", "model.model.preprocess_conv.weight") is None
    assert r("autoencoder", "pretransform.model.encoder.layers.0.transformers.2.self_attn.to_qkv.weight") == (
        "encoder.blocks.0.transformers.2.attn.to_qkv.weight")  # fmt: skip
    assert r("autoencoder", "pretransform.model.decoder.layers.3.transformers.0.pre_norm.alpha") == (
        "decoder.blocks.0.transformers.0.norm_attn.alpha")  # fmt: skip
    assert r("autoencoder", "pretransform.model.decoder.layers.3.transformers.0.rope.inv_freq") == (
        "decoder.blocks.0.transformers.0.attn.rope.inv_freq")  # fmt: skip
    assert r("autoencoder", "pretransform.model.decoder.layers.1.weight") == "decoder.proj.weight"
    assert r("autoencoder", "pretransform.model.bottleneck.scaling_factor") == "bottleneck.scale"
    assert r("autoencoder", "pretransform.model.bottleneck.noise_scaling_factor") is None
    assert r("text_encoder", "model.encoder.layers.0.mlp.up_proj.weight") == "encoder.layers.0.mlp.up_proj.weight"
    assert r("text_encoder", "model.decoder.layers.0.mlp.up_proj.weight") is None


def test_the_qkv_rows_are_reordered_for_diffusers():
    hook = sa3.tensor_hook("autoencoder", None)
    t = torch.arange(5.0).repeat_interleave(2)[:, None]  # q q k k v v q' q' k' k'
    assert hook("encoder.blocks.0.transformers.0.attn.to_qkv.weight", t).flatten().tolist() == [
        0, 0, 3, 3, 1, 1, 4, 4, 2, 2]  # fmt: skip
    assert hook("encoder.proj.weight", t) is t and sa3.tensor_hook("dit", None) is None


@pytest.mark.parametrize("component", ["dit", "autoencoder", "text_encoder"])
def test_every_kept_tensor_has_its_place_and_shape(component):
    """On the real headers (no weights read): every key rename keeps names a parameter or buffer of the meta module
    with the stored shape, and every parameter is filled."""
    root = packs.models_root() / "Stable-Audio-3-Small-SFX"
    if not root.is_dir():
        pytest.skip("Stable Audio 3 Small-SFX is not in the models folder")
    model = types.SimpleNamespace(folder=lambda c=None: root)
    module = sa3.build(component, model)
    have = {n: tuple(t.shape) for n, t in [*module.named_parameters(), *module.named_buffers()]}
    path = root / ("t5gemma-b-b-ul2/model.safetensors" if component == "text_encoder" else "model.safetensors")
    kept = {}
    for key, t in packs.header(path).items():
        if (name := sa3.rename(component, key)) is not None:
            kept[name] = tuple(t["shape"])
    assert {n: s for n, s in kept.items() if have.get(n) != s} == {}
    params = {n for n, _ in module.named_parameters()}
    assert params - kept.keys() == set()


# --- the reference's sizes and the SAME attention


def test_latent_sizes_follow_the_reference():
    assert sa3.latent_length(2.0) == 88  # (2 + 6) s up to 8192 samples: 44 x 8192 / 4096
    assert sa3.valid_latents(2.0, 88) == 22 + 64 - 0  # ceil(88200 / 4096) + int(6 s / 4096)
    assert sa3.latent_length(200) == 5292032 // 4096  # at most sample_size
    assert sa3.latent_length(3.0) % 2 == 0


def test_chunked_attention_keeps_the_last_tokens_of_each_segment():
    """With identity layers the chunked pass is a gather: the second half's shifted chunks undo themselves."""
    block = types.SimpleNamespace(transformers=[lambda x: x] * 4)
    x = torch.arange(4 * 3, dtype=torch.float32).reshape(1, 12, 1)  # 4 segments of 3 tokens
    assert sa3._chunked(block, x, 3, 1).flatten().tolist() == [2.0, 5.0, 8.0, 11.0]
    assert sa3._chunked(block, x, 3, 2).flatten().tolist() == [1, 2, 4, 5, 7, 8, 10, 11]


def test_chunked_attention_attends_within_two_segments_then_shifted():
    """A layer that sums its chunk: the first half sees segments (0, 1) and (2, 3), the second half (pad 0, 0),
    (1, 2), (3, pad 3)."""
    total = lambda x: x.sum(dim=1, keepdim=True).expand_as(x)
    block = types.SimpleNamespace(transformers=[total, total])
    x = torch.ones(1, 8, 1)  # 4 segments of 2 tokens
    out = sa3._chunked(block, x, 2, 1).flatten().tolist()
    assert out == [16.0, 16.0, 16.0, 16.0]  # every chunk of 4 ones sums to 4, then 4 of those to 16


def test_place_pads_and_cuts():
    a = torch.ones(2, 5)
    assert sa3.place(a, 2, 6).sum().item() == 8 and sa3.place(a, 0, 3).shape == (2, 3)


def ctx():
    return types.SimpleNamespace(node="sound")


def test_stages_refuse_an_empty_prompt_and_a_too_long_extend(monkeypatch):
    with pytest.raises(RunError, match="sound: describe the sound"):
        sa3.sound(
            ctx(),
            {"from_sound": [], "seconds": 2, "change": 0.7},
            {"text": "  ", "model": None, "seed": 1},
        )
    monkeypatch.setattr(sa3, "waveform", lambda v: torch.zeros(2, 44100 * 30))
    with pytest.raises(RunError, match="seconds: the sound would last over 60 s"):
        sa3.extend(
            ctx(),
            {"source": {}, "seconds": 30.5, "where": "end"},
            {"text": "x", "model": None, "seed": 1},
        )
    monkeypatch.setattr(sa3, "waveform", lambda v: torch.zeros(2, 100))
    with pytest.raises(RunError, match="source: the sound is too short"):
        sa3.vary(ctx(), {"source": {}, "change": 0.5}, {"text": "x", "model": None, "seed": 1})


def test_take_is_stereo_pcm16():
    out = sa3.take(torch.tensor([[0.5, -1.0], [0.0, 1.0]]))["audio"]
    assert out["channels"] == 2 and out["sample_rate"] == 44100 and out["duration_s"] == 2 / 44100
    assert out["wav"] == torch.tensor([16384, 0, -32767, 32767], dtype=torch.int16).numpy().astype("<i2").tobytes()
