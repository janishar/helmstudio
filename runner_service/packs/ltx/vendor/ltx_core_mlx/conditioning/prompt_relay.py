"""Prompt Relay: temporal cross-attention biasing for LTX-2.

A training-free technique (https://gordonchen19.github.io/Prompt-Relay/) that lets
a single generation follow a *sequence* of local prompts over time. A global prompt
is concatenated with N space-separated local prompts; each local prompt's text-token
range is softly gated — via an additive Gaussian penalty on the video↔text
cross-attention (``attn2``) — so it only influences the video frames inside its own
temporal window. Global-prompt tokens (and the connector's register/padding tokens)
receive zero penalty and attend freely across all frames.

Ported from Kijai/WhatDreamsCost's ComfyUI ``prompt_relay.py``. Two masks, as in the
reference's LTX patch (``attn2`` and ``audio_attn2``):

- :func:`build_relay_mask` — video queries, integer frame per token
  (``token // tokens_per_frame``, reference ``build_temporal_cost``), shape ``(1, 1, Nv, Nk)``;
- :func:`build_relay_mask_audio` — audio queries, which do not map to whole latent
  frames: token *i* of ``Na`` sits at ``i * F / Na`` latent frames (reference
  ``build_temporal_cost_scaled``), shape ``(1, 1, Na, Nk)``.

Both are additive biases for ``mx.fast.scaled_dot_product_attention(..., mask=...)``.

Token→position note: the Gemma feature connector front-packs valid tokens to the
front of the ``Nk``-length sequence (see ``text_encoders/gemma`` embeddings
connector), so text token *i* (in encode order) lives at sequence position *i*.
Ranges here are therefore computed by incremental tokenization of the combined
prompt and used directly as column indices into the ``Nk`` axis.
"""

from __future__ import annotations

import math
from dataclasses import dataclass

import mlx.core as mx
import numpy as np


@dataclass
class PromptRelayInput:
    """User-facing Prompt Relay request carried through the pipeline.

    Attributes:
        local_prompts: Ordered per-segment prompts (concatenated after the global
            ``--prompt`` to form the combined prompt that is actually encoded).
        segment_lengths: Optional per-segment length in *latent* frames. ``None``
            auto-distributes evenly across the timeline. Length, if given, must
            match ``local_prompts``.
        epsilon: Plateau→penalty falloff (smaller = sharper temporal gating).
        strength: Multiplier on the video penalty magnitude.
        audio_strength: Multiplier on the audio penalty (``audio_attn2``). ``0``
            leaves the audio cross-attention unmasked (video-only relay). ``None``
            (default) lets the pipeline pick: ``1.0`` (the reference's default,
            independent of ``strength``) on the distilled paths, ``0`` on the dev +
            CFG paths, where the audio mask was not seen to move the speech (see
            ``BasePipeline._prompt_relay_audio_mask_builder``).
        audio_epsilon: Falloff of the audio penalty; ``None`` inherits ``epsilon``
            (the reference's ``audio_epsilon=0`` "inherit" default).
    """

    local_prompts: list[str]
    segment_lengths: list[int | None] | None = None
    epsilon: float = 1e-3
    strength: float = 1.0
    audio_strength: float | None = None
    audio_epsilon: float | None = None

    def __post_init__(self) -> None:
        # Checked here, i.e. before any model load, rather than when the mask is built.
        if self.audio_strength is not None and self.audio_strength < 0.0:
            raise ValueError(f"Prompt Relay audio strength must be >= 0, got {self.audio_strength}")
        if self.audio_epsilon is not None:
            _relay_sigma(self.audio_epsilon)


@dataclass
class _Segment:
    """Per-local-prompt metadata for the temporal penalty."""

    tok_start: int
    tok_end: int
    midpoint: float  # in latent-frame units
    window: float  # half-width of the zero-penalty plateau, in latent frames
    strength: float


def map_token_ranges(
    tokenizer,
    global_prompt: str,
    local_prompts: list[str],
    max_length: int | None = None,
) -> tuple[str, list[tuple[int, int]]]:
    """Build the combined prompt and each local prompt's ``[start, end)`` token range.

    Uses incremental tokenization (measuring cumulative prefixes) to sidestep
    SentencePiece context-dependency, mirroring the reference implementation.
    The tokenizer is the text encoder's own, whose ``.encode()`` returns a token-id
    list: the mlx-lm Gemma-3 tokenizer on 2.3 packs (prepends ``<bos>``), the pack's
    HuggingFace Gemma-4 tokenizer on 2.5 packs (no ``<bos>``, no ``<eos>``). Either
    way the encoder tokenizes with the same ``.encode()``, so the counts below are
    the column offsets it produces (``tests/test_prompt_relay_ltx25.py``).

    Args:
        tokenizer: The text encoder's tokenizer (``pipe.prompt_encoder._text_encoder._tokenizer``).
        global_prompt: Prompt applied to every frame.
        local_prompts: Ordered per-segment prompts, gated to their time windows.
        max_length: Encoder sequence limit (``LTX2_GEMMA_MAX_LENGTH``). When set,
            a combined prompt exceeding it is rejected: the encoder left-truncates
            (keeps the last ``max_length`` tokens), which shifts every column and
            makes the ranges point at the wrong tokens — unrecoverable, so fail fast.

    Returns:
        ``(combined_prompt, token_ranges)`` where ``combined_prompt`` is what the
        pipeline must actually encode, and ``token_ranges[i]`` is the half-open
        column range of ``local_prompts[i]`` in the front-packed text axis.
    """
    prefixed_locals = [" " + lp for lp in local_prompts]
    combined_prompt = global_prompt + "".join(prefixed_locals)

    # Detect whether encode() appends an EOS so we can strip its phantom offset
    # from cumulative counts (else every local range would shift by +1).
    eos_adj = 0
    probe = tokenizer.encode("test")
    eos_id = getattr(tokenizer, "eos_token_id", None)
    if probe and ((eos_id is not None and probe[-1] == eos_id) or probe[-1] == 1):
        eos_adj = 1

    def enc_len(text: str) -> int:
        return len(tokenizer.encode(text.strip())) - eos_adj

    prev_len = enc_len(global_prompt)
    token_ranges: list[tuple[int, int]] = []
    built = global_prompt
    for plp in prefixed_locals:
        built += plp
        cur_len = enc_len(built)
        if cur_len <= prev_len:
            raise ValueError(f"Local prompt produced no tokens: '{plp.strip()}'")
        token_ranges.append((prev_len, cur_len))
        prev_len = cur_len

    if max_length is not None and len(tokenizer.encode(combined_prompt)) > max_length:
        raise ValueError(
            f"Prompt Relay combined prompt exceeds the encoder max length "
            f"({max_length}); the encoder left-truncates, which would misalign "
            "every segment's token range. Shorten the prompts/segments or raise "
            "LTX2_GEMMA_MAX_LENGTH."
        )
    return combined_prompt, token_ranges


def distribute_segment_lengths(
    num_segments: int,
    latent_frames: int,
    specified_lengths: list[int | None] | None = None,
) -> list[int]:
    """Validate or auto-distribute per-segment lengths (in latent frames).

    ``specified_lengths`` may be ``None`` (all beats auto → even ceil split) or a list
    matching ``num_segments`` where each entry is either a pinned length or ``None``
    (auto). Pinned beats keep their length; the leftover timeline is spread evenly across
    the ``None`` beats. Lengths are then clamped so the running sum never exceeds
    ``latent_frames``.
    """
    if specified_lengths is None:
        step = -(-latent_frames // num_segments)  # ceil division
        lengths = [step] * num_segments
    else:
        if len(specified_lengths) != num_segments:
            raise ValueError(
                f"segment length count ({len(specified_lengths)}) must match number of local prompts ({num_segments})"
            )
        auto_idx = [i for i, length in enumerate(specified_lengths) if length is None]
        if auto_idx:
            pinned_total = sum(length for length in specified_lengths if length is not None)
            remaining = max(latent_frames - pinned_total, 0)
            per_auto = -(-remaining // len(auto_idx)) if remaining > 0 else 0  # ceil split
            lengths = [per_auto if length is None else length for length in specified_lengths]
        else:
            lengths = [int(length) for length in specified_lengths]

    effective: list[int] = []
    cursor = 0
    for length in lengths:
        end = min(cursor + length, latent_frames)
        effective.append(max(end - cursor, 0))
        cursor = end
    return effective


def _build_segments(
    token_ranges: list[tuple[int, int]],
    segment_lengths: list[int],
    strength: float,
) -> list[_Segment]:
    segments: list[_Segment] = []
    frame_cursor = 0
    for (tok_start, tok_end), length in zip(token_ranges, segment_lengths):
        if length <= 0:
            frame_cursor += length
            continue
        # Floor-divide to match the reference (WhatDreamsCost build_segments):
        # an odd-length segment keeps one fully-free anchor frame at its center.
        midpoint = float((2 * frame_cursor + length) // 2)
        window = max(length // 2 - 2, 0)
        segments.append(
            _Segment(
                tok_start=tok_start,
                tok_end=tok_end,
                midpoint=midpoint,
                window=float(window),
                strength=strength,
            )
        )
        frame_cursor += length
    return segments


def _relay_sigma(epsilon: float) -> float:
    """Gaussian width of the penalty: the reference's constant ``1 / ln(1 / epsilon)``."""
    # Reject out-of-range epsilon rather than silently substituting a default: e.g.
    # epsilon=1.0 means "no falloff" (sigma -> inf) but a fallback would produce the
    # opposite (sharp default gating).
    if not 0.0 < epsilon < 1.0:
        raise ValueError(f"Prompt Relay epsilon must be in (0, 1), got {epsilon}")
    return 1.0 / math.log(1.0 / epsilon)


def _check_segments(
    token_ranges: list[tuple[int, int]],
    segment_lengths: list[int],
    num_text_tokens: int,
) -> None:
    # A zero-length beat has no temporal window; leaving its columns at cost 0 would
    # let it attend to *every* frame — the inverse of gating. Fail fast instead.
    if any(length <= 0 for length in segment_lengths):
        raise ValueError(
            f"Prompt Relay segment lengths {segment_lengths} contain a zero-length "
            "beat: that segment would attend to every frame instead of being gated. "
            "Give it an explicit length or drop a segment."
        )
    # numpy silently clips out-of-bounds column slices, which would ungate a segment
    # without warning. Reject ranges past the text axis.
    if any(end > num_text_tokens for _, end in token_ranges):
        raise ValueError(
            f"Prompt Relay token ranges {token_ranges} exceed the text axis (num_text_tokens={num_text_tokens})"
        )


def _penalty_bias(
    segments: list[_Segment],
    query_frames: np.ndarray,
    real: np.ndarray | None,
    num_text_tokens: int,
    sigma: float,
    dtype: mx.Dtype,
) -> mx.array:
    """``-strength * relu(|frame - midpoint| - window)^2 / (2 sigma^2)`` on each segment's columns.

    ``query_frames`` holds each query row's position in latent frames; rows where
    ``real`` is False get no penalty. Returns the bias as ``(1, 1, Nq, Nk)``.
    """
    cost = np.zeros((query_frames.shape[0], num_text_tokens), dtype=np.float32)
    inv_two_sigma_sq = 1.0 / (2.0 * sigma * sigma)
    for seg in segments:
        d = np.abs(query_frames - seg.midpoint)
        penalty = seg.strength * np.square(np.maximum(d - seg.window, 0.0)) * inv_two_sigma_sq
        if real is not None:
            penalty = np.where(real, penalty, 0.0)
        cost[:, seg.tok_start : seg.tok_end] = penalty.astype(np.float32)[:, None]

    mask = -cost  # additive negative bias
    return mx.array(mask, dtype=dtype)[None, None, :, :]


def build_relay_mask(
    token_ranges: list[tuple[int, int]],
    segment_lengths: list[int],
    num_video_tokens: int,
    tokens_per_frame: int,
    latent_frames: int,
    num_text_tokens: int,
    epsilon: float = 1e-3,
    strength: float = 1.0,
    dtype: mx.Dtype = mx.bfloat16,
) -> mx.array:
    """Build the additive Prompt Relay cross-attention bias for the video queries (``attn2``).

    Args:
        token_ranges: Per-segment ``[start, end)`` column ranges (from
            :func:`map_token_ranges`).
        segment_lengths: Per-segment length in latent frames (from
            :func:`distribute_segment_lengths`).
        num_video_tokens: Actual video token count ``Nv`` at attention time. May
            exceed ``latent_frames * tokens_per_frame`` when keyframe conditioning
            appends tokens — those trailing rows receive zero penalty.
        tokens_per_frame: Video tokens per latent frame (``H*W`` of the latent grid).
        latent_frames: Number of latent frames ``F``.
        num_text_tokens: Text sequence length ``Nk`` (e.g. ``LTX2_GEMMA_MAX_LENGTH``).
        epsilon: Controls plateau→penalty falloff (smaller = sharper gating).
        strength: Global multiplier on the penalty magnitude.
        dtype: Output dtype (model runs bf16).

    Returns:
        Additive bias ``(1, 1, Nv, Nk)`` — ``0`` where attention is free, negative
        where a text range is penalised for a video frame outside its window.
    """
    # Reference uses a constant sigma = 1/ln(1/epsilon) regardless of segment length.
    sigma = _relay_sigma(epsilon)
    _check_segments(token_ranges, segment_lengths, num_text_tokens)
    segments = _build_segments(token_ranges, segment_lengths, strength)

    rows = np.arange(num_video_tokens)
    query_frames = (rows // tokens_per_frame).astype(np.float32)
    # Only the first F*tokens_per_frame rows map to real frames; appended keyframe
    # tokens (if any) sit beyond that and must attend freely.
    real = rows < latent_frames * tokens_per_frame
    return _penalty_bias(segments, query_frames, real, num_text_tokens, sigma, dtype)


def build_relay_mask_audio(
    token_ranges: list[tuple[int, int]],
    segment_lengths: list[int],
    num_audio_tokens: int,
    latent_frames: int,
    num_text_tokens: int,
    epsilon: float = 1e-3,
    strength: float = 1.0,
    dtype: mx.Dtype = mx.bfloat16,
) -> mx.array:
    """Build the additive Prompt Relay cross-attention bias for the audio queries (``audio_attn2``).

    Port of the reference's ``build_temporal_cost_scaled``: audio tokens run at their
    own rate (25 per second) and do not map to whole latent frames, so token *i* of
    ``Na`` is placed at ``i * F / Na`` latent frames and penalised with the same
    segments (midpoint, plateau half-width) as the video mask.

    Args:
        token_ranges: Per-segment ``[start, end)`` column ranges (from
            :func:`map_token_ranges`).
        segment_lengths: Per-segment length in latent frames (from
            :func:`distribute_segment_lengths`), the same as the video mask's.
        num_audio_tokens: Audio token count ``Na`` at attention time.
        latent_frames: Number of video latent frames ``F`` the segments are laid on.
        num_text_tokens: Text sequence length ``Nk``.
        epsilon: Falloff of the audio penalty (the reference's ``audio_epsilon``, which
            defaults to the video epsilon).
        strength: Multiplier on the audio penalty (the reference's ``audio_strength``).
        dtype: Output dtype.

    Returns:
        Additive bias ``(1, 1, Na, Nk)``.
    """
    sigma = _relay_sigma(epsilon)
    _check_segments(token_ranges, segment_lengths, num_text_tokens)
    if num_audio_tokens <= 0:
        raise ValueError(f"Prompt Relay audio mask needs at least one audio token, got {num_audio_tokens}")
    segments = _build_segments(token_ranges, segment_lengths, strength)
    query_frames = np.arange(num_audio_tokens, dtype=np.float32) * latent_frames / num_audio_tokens
    return _penalty_bias(segments, query_frames, None, num_text_tokens, sigma, dtype)


__all__ = [
    "PromptRelayInput",
    "build_relay_mask",
    "build_relay_mask_audio",
    "distribute_segment_lengths",
    "map_token_ranges",
]
