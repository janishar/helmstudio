"""Qwen3-TTS stages on torch: the AI-EXP port (vendor/qwen3_tts, unchanged) with its two modules built on meta and
filled by the core loader (docs/reference-notes/voices.md sections 8-12).

speak: the text, split on sentences into pieces of at most PIECE_CHARS, each spoken as the port's Synthesizer.stream
does (its prompt, its frame cap, its piecewise decode with 25 frames of context), seeded with torch.manual_seed(seed)
before each piece (the port's _seed: every piece starts from the same random state, so the voice holds), joined with
PAUSE_S of silence. torch.compile stays off (it breaks end-of-speech on torch 2.15 MPS). A cancel point and a progress
step every generated frame."""

import dataclasses
import json
import math
from functools import lru_cache

import accelerate
import numpy as np
import torch
from torch import nn

from core.graph import RunError
from core.media import pieces
from packs.qwen3_tts.vendor.qwen3_tts.codec import CodecConfig, CodecDecoder
from packs.qwen3_tts.vendor.qwen3_tts.synthesizer import (
    FRAMES_PER_SECOND,
    Synthesizer,
    default_dtype,
)
from packs.qwen3_tts.vendor.qwen3_tts.talker import SamplingSettings, Talker

SAMPLE_RATE = 24000
MAX_CHARS = 900  # max_seconds 60 at the plan's 15 characters a second
PIECE_CHARS = 200  # about 13 s of speech per generate call
PAUSE_S = 0.25  # silence between two pieces of a long text
CHARS_PER_S = 15  # the progress estimate (frames planned), as speech_seconds
# BCP 47 tags (the param's enum) -> the checkpoint's codec_language_id names
LANGUAGES = {"auto": "auto", "en": "english", "zh": "chinese", "ja": "japanese", "ko": "korean",
             "de": "german", "fr": "french", "ru": "russian", "pt": "portuguese", "es": "spanish",
             "it": "italian"}  # fmt: skip
VARIANT_OF = {"preset": "custom_voice", "design": "voice_design"}


# --- components


def build(component: str, model) -> nn.Module:
    """The talker (talker + code predictor) or the speech tokenizer's decoder on the meta device. Each Qwen3Model's
    1-row embed_tokens is never used (inputs are embeddings) and not in the checkpoint: made real on the device."""
    from core.loaders_torch import device

    folder = model.folder(component)
    with accelerate.init_empty_weights(include_buffers=False), default_dtype(torch.bfloat16):
        if component == "talker":
            out = Talker(json.loads((folder / "config.json").read_text()))
        else:
            cfg = json.loads((folder / "speech_tokenizer/config.json").read_text())[
                "decoder_config"
            ]
            return CodecDecoder(CodecConfig.from_dict(cfg))
    for qwen in (out.model, out.code_predictor.model):
        qwen.embed_tokens = nn.Embedding(
            1, qwen.config.hidden_size, device=device(), dtype=torch.bfloat16
        )
    return out


def rename(component: str, key: str) -> str | None:
    """The talker's checkpoint names (Talker.rename_key); the tokenizer's decoder without its prefix, its encoder
    (voice cloning only) never read."""
    if component == "talker":
        return Talker.rename_key(key)
    return key.removeprefix("decoder.") if key.startswith("decoder.") else None


@lru_cache(maxsize=2)
def _text_tools(folder) -> tuple:
    """The tokenizer and generation_config.json sampling of a variant's folder."""
    from transformers import Qwen2Tokenizer

    gen = json.loads((folder / "generation_config.json").read_text())
    return Qwen2Tokenizer.from_pretrained(folder), SamplingSettings.from_dict(gen)


def synthesizer(model, talker: Talker, codec: CodecDecoder, temperature: float) -> Synthesizer:
    """The port's Synthesizer over the loaded modules, both temperatures set to `temperature`."""
    if talker.code_predictor.decoder is None:  # the fixed-size cache, uncompiled
        talker.enable_fast_decoding(compile=False)
    tokenizer, sampling = _text_tools(model.folder("talker"))
    sampling = dataclasses.replace(sampling, temperature=temperature, code_temperature=temperature)
    return Synthesizer(talker, codec, tokenizer, sampling, SAMPLE_RATE)


# --- speak


def check_speak(params: dict, wired: set, models: dict | None = None) -> str | None:
    """Before loading: a preset speaks only on custom_voice, a designed voice only on voice_design."""
    voice, model = params.get("voice"), (models or {}).get("model")
    if not isinstance(voice, dict) or model is None:
        return None
    need = VARIANT_OF.get(voice.get("source"))
    if need and model.variant != need:
        return f": voice {voice.get('item_id')} needs model variant {need}"
    return None


def say(
    tts: Synthesizer, text: str, speaker: str, language: str, instruct: str, seed: int, on_frame
) -> tuple:
    """One piece as Synthesizer.stream speaks it: (float32 samples, codes [frames, 16] int64). `on_frame(n)` runs
    before each frame is kept (a cancel point)."""
    text_ids = tts._text_ids(text)
    prompt, pad = tts._prompt(text_ids, speaker, language, instruct)
    max_frames = int(FRAMES_PER_SECOND * (5 + 1.2 * len(text_ids)))
    codes: list = []

    def tapped(frames):
        for f in frames:
            codes.append(f)
            yield f

    def stop() -> bool:
        on_frame(len(codes))
        return False

    torch.manual_seed(seed)
    with torch.inference_mode():
        out = list(
            tts._pieces(tapped(tts.talker.frames(prompt, pad, tts.sampling, max_frames)), stop)
        )
    samples = np.concatenate(out) if out else np.zeros(0, dtype=np.float32)
    return samples, (torch.stack(codes).cpu() if codes else torch.zeros((0, 16), dtype=torch.long))


def voice_args(node: str, voice: dict, style: str, speakers: dict) -> tuple[str, str]:
    """(speaker, instruct) of a resolved voice: a preset's speaker key and the style param; a designed voice's
    description (and its own style)."""
    source = voice.get("source")
    if source not in VARIANT_OF:
        raise RunError(f"{node}.voice: Qwen3-TTS cannot speak a voice of source {source!r}")
    if source == "preset":
        speaker = str(voice.get("speaker", "")).lower()
        if speaker not in speakers:
            raise RunError(f"{node}.voice: Qwen3-TTS has no speaker {voice.get('speaker')!r}")
        return speaker, style.strip()
    design = " ".join(
        x for x in ((voice.get("text") or "").strip(), (voice.get("style") or "").strip()) if x
    )
    if not design:
        raise RunError(f"{node}.voice: a designed voice needs a description")
    return "", design


def language_of(param: str, voice: dict) -> str:
    """The checkpoint's language: the param's, or with `auto` the voice's own (its BCP 47 tag's language, when the
    pack maps it; else auto)."""
    if param != "auto":
        return LANGUAGES[param]
    tag = str(voice.get("language") or "").split("-")[0].lower()
    return LANGUAGES.get(tag, "auto")


def speak(ctx, params: dict, inputs: dict) -> dict:
    model, text = inputs["model"], inputs["text"].strip()
    if not text:
        raise RunError(f"{ctx.node}: nothing to say")
    if len(text) > MAX_CHARS:
        raise RunError(
            f"{ctx.node}: at most {MAX_CHARS} characters (about 60 s of speech), not {len(text)}"
        )
    voice = params["voice"]
    talker = ctx.component(model, "talker")
    codec = ctx.component(model, "speech_tokenizer")
    tts = synthesizer(model, talker, codec, float(params["temperature"]))
    speaker, instruct = voice_args(ctx.node, voice, params.get("style", ""), tts._speaker_ids)
    language = language_of(params["language"], voice)
    parts = pieces(text, PIECE_CHARS)
    planned = max(1, math.ceil(len(text) / CHARS_PER_S * FRAMES_PER_SECOND))
    done, audio = [0], []

    def on_frame(_n: int) -> None:
        ctx.check()
        done[0] += 1
        ctx.progress(min(done[0], planned - 1) or 1, planned, "speaking")

    for i, part in enumerate(parts):
        ctx.check()
        samples, _ = say(tts, part, speaker, language, instruct, inputs["seed"], on_frame)
        if not len(samples):
            raise RunError(f"{ctx.node}: no speech came out")
        if i:
            audio.append(np.zeros(int(PAUSE_S * SAMPLE_RATE), dtype=np.float32))
        audio.append(samples)
    ctx.progress(planned, planned, "speaking")
    samples = np.concatenate(audio)
    pcm = (np.clip(samples, -1, 1) * 32767).round().astype("<i2").tobytes()
    return {
        "audio": {"wav": pcm, "sample_rate": SAMPLE_RATE, "duration_s": len(samples) / SAMPLE_RATE}
    }
