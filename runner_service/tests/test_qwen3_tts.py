"""The qwen3_tts pack without a GPU: its registry values equal the canonical fixtures (models, voices; nodes and
workflows in test_packs), check_speak's refusals, the language mapping, sentence splitting, voices and key renames."""

import json
import types

import pytest
from conftest import CONTRACTS

from core.graph import RunError

FIXTURES = CONTRACTS / "fixtures/registry"
tts = pytest.importorskip("packs.qwen3_tts.torch", reason="the torch env")


def fixture(key: str) -> dict:
    return json.loads((FIXTURES / f"qwen3-tts.{key}.valid.json").read_text())


def test_models_and_voices_match_the_fixtures(torch_values):
    (got,) = [m for m in torch_values["models"]["models"] if m["family"] == "qwen3-tts"]
    (want,) = fixture("models")["models"]
    keys = ("id", "title", "precisions", "source", "dir")
    assert {k: got[k] for k in ("id", "family", "title", "media", "runtimes")} == {
        k: want[k] for k in ("id", "family", "title", "media", "runtimes")}  # fmt: skip
    assert [{k: v[k] for k in keys} for v in got["variants"]] == [{k: v[k] for k in keys} for v in want["variants"]]
    for g, w in zip(got["variants"], want["variants"], strict=True):
        if g["presence"] == "present":  # on a host with the weights: the same sizes and memory plans
            assert (g["components"], g["memory"]) == (w["components"], w["memory"])
    assert [v for v in torch_values["voices"]["voices"] if v["family"] == "qwen3-tts"] == fixture("voices")["voices"]


def model(variant: str):
    return types.SimpleNamespace(variant=variant)


PRESET = {
    "item_id": "qwen3-tts/ryan",
    "version": 1,
    "source": "preset",
    "family": "qwen3-tts",
    "speaker": "ryan",
}
DESIGN = {
    "item_id": "5a0c1e2f-3b4d-4e5f-8a6b-7c8d9e0f1a21",
    "version": 1,
    "source": "design",
    "text": "low, warm",
}


@pytest.mark.parametrize(
    ("voice", "variant", "why"),
    [
        (PRESET, "custom_voice", None),
        (DESIGN, "voice_design", None),
        (PRESET, "voice_design", ": voice qwen3-tts/ryan needs model variant custom_voice"),
        (
            DESIGN,
            "custom_voice",
            ": voice 5a0c1e2f-3b4d-4e5f-8a6b-7c8d9e0f1a21 needs model variant voice_design",
        ),
        (None, "voice_design", None),  # a workflow's null voice: the memory plans
    ],
)
def test_check_speak_pairs_the_voice_with_its_variant(voice, variant, why):
    assert tts.check_speak({"voice": voice}, {"model", "text"}, {"model": model(variant)}) == why
    assert tts.check_speak({"voice": voice}, set(), {}) is None


def test_languages_are_the_param_enum_mapped_to_the_checkpoints_names():
    (speak,) = [n for n in fixture("nodes")["nodes"] if n["kind"] == "qwen3-tts/speak"]
    (lang,) = [p for p in speak["params"] if p["name"] == "language"]
    assert list(tts.LANGUAGES) == lang["enum"]
    names = {"chinese", "english", "german", "italian", "portuguese", "spanish", "japanese", "korean", "french",
             "russian"}  # fmt: skip  # config.json talker_config.codec_language_id, dialects left out
    assert set(tts.LANGUAGES.values()) == names | {"auto"}


def test_pieces_split_long_text_on_sentences():
    assert tts.pieces("  Good morning, everyone.  ", tts.PIECE_CHARS) == ["Good morning, everyone."]
    text = " ".join(f"Sentence number {i} is here." for i in range(40))
    parts = tts.pieces(text, tts.PIECE_CHARS)
    assert len(parts) > 1 and all(len(p) <= tts.PIECE_CHARS for p in parts)
    assert " ".join(parts) == text  # no word lost, none split
    assert tts.pieces("你好。今天天气很好！我们走吧。", limit=8) == [
        "你好。",
        "今天天气很好！",
        "我们走吧。",
    ]
    long = "word " * 100
    assert all(len(p) <= 50 for p in tts.pieces(long, limit=50))
    assert tts.pieces("x" * 120, limit=50) == ["x" * 50, "x" * 50, "x" * 20]  # no space: a hard cut


def test_voice_args():
    speakers = {"ryan": 1, "uncle_fu": 2}
    assert tts.voice_args("speak", PRESET, " calm ", speakers) == ("ryan", "calm")
    assert tts.voice_args("speak", PRESET | {"speaker": "Uncle_Fu"}, "", speakers) == (
        "uncle_fu",
        "",
    )
    assert tts.voice_args("speak", DESIGN | {"style": "whispering"}, "ignored", {}) == (
        "",
        "low, warm whispering",
    )
    with pytest.raises(RunError, match=r"^speak.voice: Qwen3-TTS has no speaker 'bob'$"):
        tts.voice_args("speak", PRESET | {"speaker": "bob"}, "", speakers)


def test_rename():
    assert tts.rename("talker", "talker.model.codec_embedding.weight") == "codec_embedding.weight"
    assert tts.rename("talker", "talker.model.layers.0.mlp.up_proj.weight") == "model.layers.0.mlp.up_proj.weight"
    assert tts.rename("speech_tokenizer", "decoder.pre_conv.conv.weight") == "pre_conv.conv.weight"
    assert tts.rename("speech_tokenizer", "encoder.layers.0.weight") is None


def test_speak_refuses_empty_and_too_long_text():
    ctx = types.SimpleNamespace(node="speak")
    for text, why in (
        ("  ", "^speak: nothing to say$"),
        ("a" * 901, "^speak: at most 900 characters"),
    ):
        with pytest.raises(RunError, match=why):
            tts.speak(ctx, {"voice": PRESET}, {"model": model("custom_voice"), "text": text, "seed": 1})


def test_pieces_cut_long_cjk_sentences_after_their_commas():
    assert tts.pieces("一二三四五，六七八九十、甲乙丙", limit=8) == [
        "一二三四五，",
        "六七八九十、",
        "甲乙丙",
    ]
    assert tts.pieces("ab cd；efgh：ij", limit=6) == ["ab", "cd；", "efgh：", "ij"]


def test_voice_args_refuses_a_voice_without_a_source_and_reads_a_null_text():
    with pytest.raises(RunError, match=r"^speak.voice: Qwen3-TTS cannot speak a voice of source None$"):
        tts.voice_args("speak", {"item_id": "x"}, "", {})
    with pytest.raises(RunError, match="needs a description"):
        tts.voice_args("speak", DESIGN | {"text": None}, "", {})


def test_language_auto_uses_the_voices_own():
    assert tts.language_of("auto", {"language": "en-US"}) == "english"
    assert tts.language_of("auto", {"language": "zh"}) == "chinese"
    assert tts.language_of("auto", {"language": "sw"}) == "auto"  # not mapped
    assert tts.language_of("auto", {}) == "auto"
    assert tts.language_of("ja", {"language": "en"}) == "japanese"  # the param wins


def test_speak_fails_when_no_speech_comes_out(monkeypatch):
    import numpy as np

    monkeypatch.setattr(tts, "synthesizer", lambda *a: types.SimpleNamespace(_speaker_ids={"ryan": 1}))
    monkeypatch.setattr(tts, "say", lambda *a: (np.zeros(0, dtype=np.float32), None))
    ctx = types.SimpleNamespace(node="speak", component=lambda *a: None, check=lambda: None, progress=lambda *a: None)
    params = {"voice": PRESET, "temperature": 0.3, "language": "auto", "style": ""}
    with pytest.raises(RunError, match=r"^speak: no speech came out$"):
        tts.speak(ctx, params, {"model": model("custom_voice"), "text": "Hi.", "seed": 1})


def test_both_variants_publish_on_cuda(monkeypatch):
    """M9: the official bf16 variants run on NVIDIA hosts too (no `devices` per variant: every host)."""
    from core import models, pack

    (p,) = [p for p, _ in pack.for_env("torch") if p.family == "qwen3-tts"]
    assert p.spec["runtimes"]["torch"]["devices"] == ["mps", "cuda"]
    monkeypatch.setattr(models, "device", lambda _p: "cuda")
    assert [v["id"] for v in models.entry(p)["variants"]] == ["custom_voice", "voice_design"]
