"""The ace_step pack without weights or a GPU: its registry values against the canonical fixtures (nodes and
workflows in test_packs), the checkpoint renames, a tiny DiT, text encoder and VAE through build / rename / the core
loader and every DiT stage and decode on the CPU, the planner's loop on a scripted LM, the helpers, and the
download plan on the fake hub (XL's shares fetched into the 2B's folder, nothing outside allow_patterns)."""

import io
import json
import types
import wave
from pathlib import Path

import numpy as np
import pytest
import yaml
from conftest import CONTRACTS
from fake_hub import FakeHub, Repo

from core.graph import RunError

torch = pytest.importorskip("torch", reason="the torch env")
ace = pytest.importorskip("packs.ace_step.torch")
FIXTURES = CONTRACTS / "fixtures/registry"


def fixture(key: str) -> dict:
    return json.loads((FIXTURES / f"ace-step.{key}.valid.json").read_text())


@pytest.fixture(scope="module")
def values():
    from core import registry

    return registry.build("torch")[0]


def test_models_match_the_fixture(values):
    (got,) = [m for m in values["models"]["models"] if m["family"] == "ace-step"]
    (want,) = fixture("models")["models"]
    top = ("id", "family", "title", "media", "runtimes", "sound")
    assert {k: got.get(k) for k in top} == {k: want.get(k) for k in top}
    keys = ("id", "title", "precisions", "source", "dir")
    assert [{k: v[k] for k in keys} for v in got["variants"]] == [{k: v[k] for k in keys} for v in want["variants"]]
    for g, w in zip(got["variants"], want["variants"], strict=True):
        if g["presence"] == "present":  # on a host with the weights: the same sizes and memory plans
            assert (g["components"], g["memory"]) == (w["components"], w["memory"])
    assert got["sound"] == "music"


def test_rename_maps_the_checkpoint_layout_onto_the_pipeline_modules():
    r = ace.rename
    assert r("transformer", "decoder.proj_in.1.weight") == "transformer.proj_in_conv.weight"
    assert r("transformer_xl", "decoder.proj_out.1.bias") == "transformer.proj_out_conv.bias"
    assert (r("transformer", "decoder.layers.3.cross_attn.o_proj.weight")
            == "transformer.layers.3.cross_attn.to_out.0.weight")  # fmt: skip
    assert (r("transformer", "encoder.lyric_encoder.layers.0.self_attn.q_norm.weight")
            == "condition_encoder.lyric_encoder.layers.0.self_attn.norm_q.weight")  # fmt: skip
    assert r("transformer", "null_condition_emb") == "condition_encoder.null_condition_emb"
    assert r("transformer", "tokenizer.quantizer.x") == "audio_tokenizer.quantizer.x"
    assert r("transformer", "detokenizer.embed_tokens.weight") == "audio_token_detokenizer.embed_tokens.weight"
    assert r("transformer", "something.else") is None
    assert r("lm", "layers.0.mlp.up_proj.weight") == "model.layers.0.mlp.up_proj.weight"
    assert r("text_encoder", "norm.weight") == "norm.weight"
    assert r("vae", "decoder.block.0.conv_t1.weight_g") == "decoder.block.0.conv_t1.weight_g"


# --- a tiny model, every stage on the CPU

TINY_DIT = {"hidden_size": 64, "intermediate_size": 128, "num_hidden_layers": 2, "num_attention_heads": 2,
            "num_key_value_heads": 1, "head_dim": 32, "in_channels": 12, "audio_acoustic_hidden_dim": 4,
            "patch_size": 2, "rope_theta": 1000000, "attention_bias": False, "attention_dropout": 0.0,
            "rms_norm_eps": 1e-6, "sliding_window": 16, "layer_types": ["sliding_attention", "full_attention"],
            "text_hidden_dim": 32, "timbre_hidden_dim": 4, "num_lyric_encoder_hidden_layers": 1,
            "num_timbre_encoder_hidden_layers": 1, "fsq_dim": 64, "fsq_input_levels": [8, 8, 8, 5, 5, 5],
            "fsq_input_num_quantizers": 1, "pool_window_size": 5, "num_attention_pooler_hidden_layers": 2,
            "is_turbo": True, "model_version": "turbo", "lyric_alignment_layers_config": {"0": [0], "1": [1]}}  # fmt: skip
TINY_TE = {"architectures": ["Qwen3Model"], "model_type": "qwen3", "hidden_size": 32, "intermediate_size": 64,
           "num_hidden_layers": 1, "num_attention_heads": 2, "num_key_value_heads": 1, "head_dim": 16,
           "vocab_size": 151669, "max_position_embeddings": 4096, "rms_norm_eps": 1e-6, "rope_theta": 1000000,
           "tie_word_embeddings": True}  # fmt: skip
TINY_VAE = {"_class_name": "AutoencoderOobleck", "audio_channels": 2, "channel_multiples": [1, 2],
            "decoder_channels": 8, "decoder_input_channels": 4, "downsampling_ratios": [40, 48],
            "encoder_hidden_size": 8, "sampling_rate": 48000}  # fmt: skip
ORIGINAL = (("transformer.", "decoder."), ("condition_encoder.", "encoder."), ("audio_tokenizer.", "tokenizer."),
            ("audio_token_detokenizer.", "detokenizer."))  # fmt: skip


def original(name: str) -> str:
    """The checkpoint's name of a Dit parameter (rename's inverse)."""
    if name == "condition_encoder.null_condition_emb":
        return "null_condition_emb"
    for new, old in ORIGINAL:
        if name.startswith(new):
            name = old + name.removeprefix(new)
            break
    name = name.replace("proj_in_conv.", "proj_in.1.").replace("proj_out_conv.", "proj_out.1.")
    for old, new in ace.ATTN:
        name = name.replace(new, old)
    return name


@pytest.fixture(scope="module")
def tiny(tmp_path_factory):
    """A tiny ACE-Step folder (random weights in the checkpoints' own layout) and its components, loaded the core
    loader's way; the real text encoder's tokenizer when on this host."""
    from diffusers import AutoencoderOobleck
    from safetensors.torch import save_file
    from transformers import Qwen3Config, Qwen3Model

    from core import loaders_torch as lt
    from core import pack as packs

    folder = packs.models_root() / "ACE-Step-1.5" / "Qwen3-Embedding-0.6B"
    if not (folder / "tokenizer.json").is_file():
        pytest.skip("the ACE-Step-1.5 text encoder's tokenizer is not on this host")
    root = tmp_path_factory.mktemp("ace")
    torch.manual_seed(0)
    dit = ace.Dit(TINY_DIT)
    (root / "acestep-v15-turbo").mkdir()
    (root / "acestep-v15-turbo/config.json").write_text(json.dumps(TINY_DIT))
    torch.save(torch.randn(1, 4, 15000) * 0.1, root / "acestep-v15-turbo/silence_latent.pt")
    save_file({original(k): v.contiguous() for k, v in dit.state_dict().items() if k != "condition_encoder.silence_latent"},
              root / "acestep-v15-turbo/model.safetensors")  # fmt: skip
    te = Qwen3Model(Qwen3Config(**TINY_TE))
    (root / "Qwen3-Embedding-0.6B").mkdir()
    (root / "Qwen3-Embedding-0.6B/config.json").write_text(json.dumps(TINY_TE))
    save_file(
        {k: v.contiguous() for k, v in te.state_dict().items()},
        root / "Qwen3-Embedding-0.6B/model.safetensors",
    )
    vae = AutoencoderOobleck(**{k: v for k, v in TINY_VAE.items() if not k.startswith("_")})
    (root / "vae").mkdir()
    (root / "vae/config.json").write_text(json.dumps(TINY_VAE))
    save_file(
        {k: v.contiguous() for k, v in vae.state_dict().items()},
        root / "vae/diffusion_pytorch_model.safetensors",
    )
    model = types.SimpleNamespace(variant="turbo-2b", folder=lambda component=None: root)
    loaded = {}
    for c, f, dtype in (("transformer", "acestep-v15-turbo/model.safetensors", torch.float32),
                        ("text_encoder", "Qwen3-Embedding-0.6B/model.safetensors", torch.float32),
                        ("vae", "vae/diffusion_pytorch_model.safetensors", torch.float16)):  # fmt: skip
        loaded[c] = lt.load(ace.build(c, model), [root / f], dev="cpu", dtype=dtype,
                            rename=lambda k, c=c: ace.rename(c, k))  # fmt: skip
    for k, v in dit.state_dict().items():
        if k != "condition_encoder.silence_latent":
            assert torch.equal(loaded["transformer"].state_dict()[k], v), k
    tok = ace._tokenizer(folder)
    return types.SimpleNamespace(model=model, loaded=loaded, tok=tok)


def ctx_of(tiny, monkeypatch, node="compose"):
    monkeypatch.setattr(ace, "tokenizer", lambda model, component: tiny.tok)
    steps = []
    return types.SimpleNamespace(node=node, device="cpu", check=lambda: None,
                                 progress=lambda *a: steps.append(a), steps=steps,
                                 component=lambda model, c: tiny.loaded[c])  # fmt: skip


def wav_of(audio: dict) -> np.ndarray:
    return np.frombuffer(audio["wav"], "<i2").reshape(-1, 2)


def song(**kw):
    return ace.Song(**({"lyrics": "[verse]\nla la la\n[chorus]\noh oh", "instrumental": False, "seconds": 4.0,
                        "bpm": 0, "key": "auto", "time": "auto", "language": "en"} | kw))  # fmt: skip


def media_of(samples: np.ndarray) -> dict:
    buf = io.BytesIO()
    with wave.open(buf, "wb") as w:
        w.setnchannels(2)
        w.setsampwidth(2)
        w.setframerate(ace.SAMPLE_RATE)
        w.writeframes((samples.T * 32767).astype("<i2").tobytes())
    return {"take_id": "t1", "kind": "audio", "data": buf.getvalue()}


def test_compose_and_decode_make_seeded_stereo_with_sections(tiny, monkeypatch):
    ctx = ctx_of(tiny, monkeypatch)
    ins = {"model": tiny.model, "song": song(), "text": "lo-fi pop", "seed": 7}
    a = ace.compose(ctx, {"reference": [], "steps": 2}, ins)["latent"]
    assert a.latents.shape == (1, 100, 4)  # 4 s at 25 Hz
    assert [s[2] for s in ctx.steps] == ["sampling", "sampling"]
    assert torch.equal(ace.compose(ctx, {"reference": [], "steps": 2}, ins)["latent"].latents, a.latents)
    # random weights align anyhow: the tags, in time order, from 0 to the end
    assert {s["label"] for s in a.sections} <= {"verse", "chorus"}
    if a.sections:
        assert a.sections[0]["start_s"] == 0.0 and a.sections[-1]["end_s"] == 4.0
        assert all(s["start_s"] < s["end_s"] for s in a.sections)
    out = ace.decode(ctx, {}, {"model": tiny.model, "latent": a})["audio"]
    assert (out["sample_rate"], out["channels"], out["duration_s"]) == (48000, 2, 4.0)
    peak = np.abs(wav_of(out)).max() / 32767
    assert abs(peak - 10 ** (-1 / 20)) < 1e-3  # levelled to -1 dBFS peak


def test_compose_reads_codes_and_a_reference(tiny, monkeypatch):
    ctx = ctx_of(tiny, monkeypatch)
    codes = "".join(f"<|audio_code_{i}|>" for i in range(15))  # 3 s at 5 Hz
    ref = media_of(np.random.default_rng(0).uniform(-0.5, 0.5, (2, 48000)).astype(np.float32))
    latent = ace.compose(ctx, {"reference": [{"audio": ref}], "steps": 1},
                         {"model": tiny.model, "song": song(codes=codes, planned=True), "text": "x", "seed": 1})  # fmt: skip
    assert latent["latent"].latents.shape == (1, 75, 4)


def test_repaint_and_extend_keep_the_source_outside_the_window(tiny, monkeypatch):
    ctx = ctx_of(tiny, monkeypatch, "repaint")
    src = np.random.default_rng(1).uniform(-0.3, 0.3, (2, 3 * 48000)).astype(np.float32)
    ins = {"model": tiny.model, "song": song(), "text": "rock", "seed": 3}
    lat = ace.repaint(ctx, {"source": media_of(src), "span": {"start": 1.0, "end": 2.0}, "steps": 1}, ins)["latent"]
    assert lat.window == (1.0, 2.0) and lat.latents.shape[1] == 75
    out = wav_of(ace.decode(ctx, {}, {"model": tiny.model, "latent": lat})["audio"]).astype(np.float64)
    kept = np.round(src.T * 32767) / 32767
    ratio = out[:40000] / 32767 / np.where(np.abs(kept[:40000]) > 0.05, kept[:40000], np.nan)
    assert np.nanstd(ratio) < 0.01  # the kept part is the source, scaled by the levelling only
    ext = ace.extend(ctx, {"source": media_of(src), "seconds": 2, "where": "start", "steps": 1}, ins)["latent"]
    assert ext.window == (0.0, 2.0) and ext.keep.shape == (2, 5 * 48000) and not ext.keep[:, :96000].any()
    with pytest.raises(RunError, match=r"^repaint.seconds: the song would last over 600 s$"):
        ace.extend(ctx, {"source": media_of(src), "seconds": 598, "where": "end", "steps": 1}, ins)


def test_cover_follows_the_source(tiny, monkeypatch):
    ctx = ctx_of(tiny, monkeypatch, "cover")
    src = np.random.default_rng(2).uniform(-0.3, 0.3, (2, 2 * 48000)).astype(np.float32)
    lat = ace.cover(ctx, {"source": media_of(src), "strength": 0.5, "steps": 1},
                    {"model": tiny.model, "song": song(), "text": "jazz", "seed": 3})["latent"]  # fmt: skip
    assert lat.latents.shape == (1, 50, 4) and lat.keep is None


def test_an_empty_style_is_refused(tiny, monkeypatch):
    ctx = ctx_of(tiny, monkeypatch)
    with pytest.raises(RunError, match=r"^compose: describe the music \(the style is empty\)$"):
        ace.compose(
            ctx,
            {"reference": [], "steps": 1},
            {"model": tiny.model, "song": song(), "text": " ", "seed": 1},
        )


# --- the planner on a scripted LM


class ScriptLM:
    """Logits that peak on the next token of `script` (then on the last), recording the batch it was given."""

    def __init__(self, script: list[int], vocab: int = 217204) -> None:
        self.script, self.vocab, self.n, self.batches = script, vocab, 0, []

    def __call__(self, input_ids, attention_mask, past_key_values, use_cache):
        self.batches.append(input_ids.shape[0])
        logits = torch.zeros(input_ids.shape[0], 1, self.vocab)
        logits[:, 0, self.script[min(self.n, len(self.script) - 1)]] = 30.0
        self.n += 1
        return types.SimpleNamespace(logits=logits, past_key_values="kv")


class Tok:
    eos_token_id, padding_side = 151645, "right"

    def __call__(self, text, return_tensors=None, padding=False):
        n = len(text) if isinstance(text, list) else 1
        return types.SimpleNamespace(input_ids=torch.ones(n, 5, dtype=torch.long),
                                     attention_mask=torch.ones(n, 5, dtype=torch.long))  # fmt: skip

    def decode(self, ids, skip_special_tokens=False):
        return " ".join(map(str, ids))


def lm_ctx():
    return types.SimpleNamespace(node="plan", device="cpu", check=lambda: None, progress=lambda *a: None)


def test_think_blocks_audio_codes_and_stops_at_think_end():
    lm = ScriptLM([ace.CODE0 + 5, 11, ace.THINK_END, 12])
    out = ace.think(lm_ctx(), lm, Tok(), "p", torch.Generator().manual_seed(1))
    toks = [int(t) for t in out.split()]
    assert toks[-1] == ace.THINK_END and all(t < ace.CODE0 for t in toks)


def flat_lm(input_ids, attention_mask, past_key_values, use_cache):
    """Every token alike: what is drawn is the generator's."""
    return types.SimpleNamespace(logits=torch.zeros(input_ids.shape[0], 1, 217204), past_key_values="kv")


def test_codes_writes_exactly_n_with_cfg_and_repeats_by_seed():
    lm = ScriptLM([ace.CODE0 + 9])
    out = ace.codes(lm_ctx(), lm, Tok(), "c", "u", 7, torch.Generator().manual_seed(3))
    assert out == "<|audio_code_9|>" * 7 and set(lm.batches) == {2}  # cond + uncond in one batch
    a, b, c = (ace.codes(lm_ctx(), flat_lm, Tok(), "c", "u", 20, torch.Generator().manual_seed(s)) for s in (5, 5, 6))
    assert a == b != c


def test_parse_and_settle_the_thought():
    said = ace.parse_metas("<think>\nbpm: 96\ncaption: dreamy synth pop with\n  a soft vocal\nduration: 75\n"
                           "keyscale: F# minor\nlanguage: en\ntimesignature: 4\n</think>")  # fmt: skip
    assert said["caption"] == "dreamy synth pop with a soft vocal" and said["bpm"] == "96"
    metas = ace.settle(song(seconds=0.0), said)
    assert metas == {"bpm": 96, "caption": "dreamy synth pop with a soft vocal", "duration": 75,
                     "keyscale": "F# minor", "language": "en", "timesignature": 4}  # fmt: skip
    mine = ace.settle(song(seconds=40.0, bpm=120, key="C major", time="3/4", language="hi"), said)
    assert (mine["bpm"], mine["duration"], mine["keyscale"], mine["timesignature"], mine["language"]) == (
        120, 40, "C major", 3, "hi")  # fmt: skip
    bad = ace.settle(song(seconds=0.0, language="auto"), {"bpm": "fast", "duration": "9", "keyscale": "H major"})
    assert (bad["bpm"], bad["duration"], bad["keyscale"], bad["timesignature"]) == (
        None,
        None,
        "",
        "",
    )
    # a language the pack does not offer is dropped (vocal_language "unknown"), a known one read as written
    assert ace.settle(song(seconds=0.0, language="auto"), {"language": "klingon"})["language"] == ""
    assert ace.settle(song(seconds=0.0, language="auto"), {"language": " EN "})["language"] == "en"
    schema = yaml.safe_load((Path(ace.__file__).parent / "model.yaml").read_text())
    assert set(schema["stages"]["song"]["params"]["language"]["enum"]) - {"auto"} == ace.LANGUAGES
    assert ace.cot(metas).startswith("<think>\nbpm: 96\ncaption: dreamy") and ace.cot(metas).endswith("</think>")
    assert ace.plan_line(metas) == "96 BPM · F# minor · 4/4 · 1:15 · en"


def test_song_reads_instrumental_and_auto():
    s = ace.song(None, {"lyrics": " ", "instrumental": False, "seconds": 0, "bpm": 0, "key": "auto",
                        "time": "auto", "language": "auto"}, {})["song"]  # fmt: skip
    assert s.sung() == "[Instrumental]" and s.metas() == {
        "bpm": None,
        "keyscale": None,
        "timesignature": None,
    }
    assert s.vocal_language() == "unknown"
    assert song(time="6/8", key="A minor", bpm=90).metas() == {
        "bpm": 90,
        "keyscale": "A minor",
        "timesignature": "6",
    }


# --- helpers


def test_splice_ramps_and_level():
    out, keep = np.ones((2, 48000), np.float32), np.zeros((2, 48000), np.float32)
    s = ace.splice(out, keep, (0.25, 0.5))
    assert s[0, 12000] == 1 and s[0, 23999] == 1 and s[0, 24000 + 480] == 0 and 0 < s[0, 11800] < 1
    assert ace.level(np.full((2, 4), 2.0)).max() == pytest.approx(10 ** (-1 / 20))


def repo_dtw(x: np.ndarray) -> np.ndarray:
    """_dtw.py dtw_cpu's loop (and its backtrace to the edge), as written."""
    n, m = x.shape
    cost, trace = np.full((n + 1, m + 1), np.inf, np.float32), np.zeros((n + 1, m + 1))
    cost[0, 0] = 0
    for j in range(1, m + 1):
        for i in range(1, n + 1):
            c0, c1, c2 = cost[i - 1, j - 1], cost[i - 1, j], cost[i, j - 1]
            c, t = (c0, 0) if c0 < c1 and c0 < c2 else (c1, 1) if c1 < c0 and c1 < c2 else (c2, 2)
            cost[i, j], trace[i, j] = x[i - 1, j - 1] + c, t
    i, j, path = n, m, []
    while i > 0 and j > 0:
        path.append((i - 1, j - 1))
        t = trace[i, j]
        i, j = (i - 1, j - 1) if t == 0 else (i - 1, j) if t == 1 else (i, j - 1)
    return np.array(path[::-1])


def test_dtw_is_the_repos_loop_and_sections_of():
    rng = np.random.default_rng(0)
    for shape in ((5, 9), (30, 70), (13, 13)):
        x = rng.normal(size=shape)
        tokens, frames = ace.dtw(x)
        want = repo_dtw(x)
        assert (tokens == want[:, 0]).all() and (frames == want[:, 1]).all()
    lines = ["[intro]", "[verse]", "a b", "[chorus]", "c d"]
    tags = [(0, "intro"), (1, "verse"), (3, "chorus")]
    starts = [0, 3, 6, 9, 12]
    out = ace.sections_of(tags, lines, starts, {6: 5.0, 12: 20.0}, 30.0)
    assert out == [{"label": "intro", "start_s": 0.0, "end_s": 5.0}, {"label": "verse", "start_s": 5.0, "end_s": 20.0},
                   {"label": "chorus", "start_s": 20.0, "end_s": 30.0}]  # fmt: skip


def full_nucleus(logits: torch.Tensor, gen: torch.Generator) -> int:
    """llm_inference.py's draw over the whole vocabulary (_apply_top_p_filter, then _sample_tokens)."""
    sorted_l, order = torch.sort(logits, descending=True)
    cut = torch.cumsum(torch.softmax(sorted_l, dim=-1), dim=-1) > ace.TOP_P
    cut[1:] = cut[:-1].clone()
    cut[0] = False
    sorted_l[cut] = float("-inf")
    probs = torch.softmax(sorted_l / ace.TEMPERATURE, -1)
    return int(order[torch.multinomial(probs, 1, generator=gen)])


@pytest.mark.parametrize("scale", [0.5, 3.0, 12.0])  # flat (the full sort), peaked, very peaked (top-k)
def test_nucleus_draws_what_the_full_sort_draws(scale):
    logits = torch.randn(217204, generator=torch.Generator().manual_seed(1)) * scale
    for seed in range(5):
        a = ace.nucleus(logits.clone(), torch.Generator().manual_seed(seed))
        assert a == full_nucleus(logits.clone(), torch.Generator().manual_seed(seed))


# --- downloads on the fake hub


@pytest.fixture
def hub_pack(tmp_path, monkeypatch):
    import config
    from core import models
    from core import pack as packs

    monkeypatch.setattr(config, "MODELS", tmp_path / "models")
    monkeypatch.setattr(models, "hashes", models.Hashes())
    (tmp_path / "models").mkdir()
    (p,) = [p for p, _ in packs.for_env("torch") if p.family == "ace-step"]
    return p


def st(keys: list[str]) -> bytes:
    from safetensors.torch import save

    return save({k: torch.zeros(2, 2) for k in keys})


def test_xl_download_fetches_the_shared_components_into_the_2b_folder(hub_pack, tmp_path):
    import config
    from core import download, models

    v = hub_pack.spec["variants"]
    base = {"README.md": b"card", "acestep-v15-turbo/config.json": b"{}",
            "acestep-v15-turbo/model.safetensors": st(["decoder.a"]), "acestep-v15-turbo/silence_latent.pt": b"pt",
            "acestep-v15-turbo/modeling_acestep_v15_turbo.py": b"code",
            "acestep-v15-base/model.safetensors": st(["decoder.b"]),
            "acestep-5Hz-lm-1.7B/config.json": b"{}", "acestep-5Hz-lm-1.7B/model.safetensors": st(["embed_tokens.weight"]),
            "Qwen3-Embedding-0.6B/config.json": b"{}", "Qwen3-Embedding-0.6B/model.safetensors": st(["norm.weight"]),
            "vae/config.json": b"{}", "vae/diffusion_pytorch_model.safetensors": st(["decoder.x"])}  # fmt: skip
    xl = {"config.json": b"{}", "model.safetensors.index.json": b"{}", "silence_latent.pt": b"pt",
          "modeling_acestep_v15_xl_turbo.py": b"code"} | {
        f"model-0000{i}-of-00004.safetensors": st([f"decoder.l{i}"]) for i in range(1, 5)}  # fmt: skip
    repos = {v["turbo-2b"]["source"]["repo"]: Repo(base, commit=v["turbo-2b"]["source"]["revision"]),
             v["xl-turbo"]["source"]["repo"]: Repo(xl, commit=v["xl-turbo"]["source"]["revision"])}  # fmt: skip
    with FakeHub(repos) as hub:

        def run(vid):
            return download.download(hub_pack, vid, endpoint=hub.url, token=None, cache=tmp_path / "cache",
                                     host="t", progress=download.Progress())  # fmt: skip

        run("xl-turbo")
        presence = {x["id"]: x["presence"] for x in models.entry(hub_pack)["variants"]}
        assert presence == {"turbo-2b": "partial", "xl-turbo": "present"}
        two_b, big = config.MODELS / "ACE-Step-1.5", config.MODELS / "ACE-Step-1.5-XL-Turbo"
        assert (two_b / "acestep-5Hz-lm-1.7B/model.safetensors").is_file() and not (big / "vae").exists()
        assert not (big / "modeling_acestep_v15_xl_turbo.py").exists()
        run("turbo-2b")
        assert (two_b / "acestep-v15-turbo/silence_latent.pt").is_file()
        for skipped in (
            "README.md",
            "acestep-v15-base",
            "acestep-v15-turbo/modeling_acestep_v15_turbo.py",
        ):
            assert not (two_b / skipped).exists(), skipped
        presence = {x["id"]: x["presence"] for x in models.entry(hub_pack)["variants"]}
        assert presence == {"turbo-2b": "present", "xl-turbo": "present"}
        assert not any(r[3] for r in hub.requests)  # never a token: not gated
