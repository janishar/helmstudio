"""Normalized Attention Guidance (NAG): a negative prompt without CFG.

NAG (Chen et al., https://github.com/ChenDarYen/Normalized-Attention-Guidance) applies a
negative prompt inside the text cross-attention instead of through a second, unconditional
model pass. Each cross-attention runs its queries against the positive and the negative
context; the two outputs ``z+`` and ``z-`` are extrapolated, the result's L1 norm is clipped
to ``tau`` times the norm of ``z+`` per token, and blended back with ``z+``:

.. code-block:: text

    z~ = z+ * scale - z- * (scale - 1)              # = z+ + (scale - 1) * (z+ - z-)
    r  = ||z~||_1 / ||z+||_1                         # per token, over the feature axis
    z^ = z~ * min(1, tau / r)                        # (computed as tau * ||z+|| / (||z~|| + 1e-7))
    z  = alpha * z^ + (1 - alpha) * z+

That costs one extra K/V projection of the negative context and one extra attention per
cross-attention call, a few percent of a step, where CFG doubles the forward. It is the
guidance distilled checkpoints can use: they are trained for CFG 1 and have no
unconditional pass to extrapolate from.

The LTX-2 port follows kijai/ComfyUI-KJNodes ``LTX2_NAG`` (``nodes/ltxv_nodes.py``): both
text cross-attentions of every block (``attn2`` and ``audio_attn2``), guidance before the
per-head gate and the output projection, any Prompt Relay mask on the positive attention
only, and the same defaults (``scale=11``, ``alpha=0.25``, ``tau=2.5``). See
:class:`NAGConfig` for where this port differs.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import NamedTuple

import mlx.core as mx

#: Defaults of kijai's ``LTX2_NAG`` node (the paper's README uses scale 3-9 on other models).
DEFAULT_NAG_SCALE = 11.0
DEFAULT_NAG_ALPHA = 0.25
DEFAULT_NAG_TAU = 2.5

_NORM_EPS = 1e-7


@dataclass(frozen=True)
class NAGConfig:
    """User-facing NAG settings, validated at construction (before any model load).

    Differences from the reference ComfyUI node, all deliberate:

    - The negative context goes through the same per-step prompt AdaLN modulation
      (``prompt_scale_shift_table``) as the positive one before its K/V projection. The
      node hands its patched ``attn2`` the raw connector output, so on LTX-2.3/2.5
      checkpoints (``cross_attention_adaln``) its negative skips the modulation its
      positive gets.
    - The guidance runs in float32 whatever the compute dtype: the L1 norm is a sum over
      4,096 features and ``scale * z+`` reaches 11 times the attention output, both out of
      float16's range.
    - ``scale == 1`` is the identity and turns NAG off (the paper's ``nag_scale > 1``
      gate); the node uses ``scale == 0`` for "off". Scales below 1 would push towards the
      negative prompt and are rejected.

    Attributes:
        scale: Extrapolation factor ``s`` (>= 1). Higher pushes harder away from the negative.
        alpha: Blend of the normalised guidance with the positive output, in [0, 1].
        tau: Clip on the L1-norm ratio between the guided and the positive output (> 0).
        audio: Also guide the audio cross-attention (``audio_attn2``) with the negative
            prompt's audio embeddings, as the node does when given an audio negative.
    """

    scale: float = DEFAULT_NAG_SCALE
    alpha: float = DEFAULT_NAG_ALPHA
    tau: float = DEFAULT_NAG_TAU
    audio: bool = True

    def __post_init__(self) -> None:
        if not self.scale >= 1.0:
            raise ValueError(f"NAG scale must be >= 1 (1 = no guidance), got {self.scale}")
        if not 0.0 <= self.alpha <= 1.0:
            raise ValueError(f"NAG alpha must be in [0, 1], got {self.alpha}")
        if not self.tau > 0.0:
            raise ValueError(f"NAG tau must be > 0, got {self.tau}")

    @property
    def active(self) -> bool:
        """Whether this config changes anything (``scale > 1``)."""
        return self.scale > 1.0


class NAGGuidance(NamedTuple):
    """Per-run NAG input for :class:`~ltx_core_mlx.model.transformer.model.LTXModel`.

    A ``NamedTuple`` of arrays and floats, so it crosses ``mx.compile`` (the ``--low-ram``
    shared block) like any other keyword argument.

    Attributes:
        video_text_embeds: Negative prompt's video context, ``(1, Nt, video_dim)``, as the
            text encoder returns it (before the per-step prompt AdaLN).
        audio_text_embeds: Negative prompt's audio context, ``(1, Nt, audio_dim)``, or
            ``None`` to leave ``audio_attn2`` unguided.
        scale: See :class:`NAGConfig`.
        alpha: See :class:`NAGConfig`.
        tau: See :class:`NAGConfig`.
    """

    video_text_embeds: mx.array
    audio_text_embeds: mx.array | None
    scale: float
    alpha: float
    tau: float

    @classmethod
    def from_config(
        cls, config: NAGConfig, video_text_embeds: mx.array, audio_text_embeds: mx.array | None
    ) -> NAGGuidance:
        """Bind encoded negative embeddings to a config (audio dropped when ``config.audio`` is off)."""
        return cls(
            video_text_embeds=video_text_embeds,
            audio_text_embeds=audio_text_embeds if config.audio else None,
            scale=config.scale,
            alpha=config.alpha,
            tau=config.tau,
        )


def normalized_attention_guidance(
    z_pos: mx.array,
    z_neg: mx.array,
    scale: float,
    alpha: float,
    tau: float,
    axis: int | tuple[int, ...] = -1,
) -> mx.array:
    """Combine positive and negative attention outputs (NAG), in float32.

    Args:
        z_pos: Attention output against the positive context.
        z_neg: Attention output against the negative context, same shape.
        scale: Extrapolation factor.
        alpha: Blend of the normalised guidance with ``z_pos``.
        tau: Clip on the per-token L1-norm ratio ``||z~|| / ||z+||``.
        axis: Feature axes the L1 norm runs over: ``-1`` for ``(B, N, H*D)``, ``(1, 3)`` for
            the ``(B, H, N, D)`` layout inside :class:`Attention` (the norm spans every head,
            as in the reference, which normalises the flattened ``H*D`` output).

    Returns:
        The guided output, in ``z_pos``'s dtype.
    """
    out_dtype = z_pos.dtype
    pos = z_pos.astype(mx.float32)
    neg = z_neg.astype(mx.float32)
    guidance = pos * scale - neg * (scale - 1.0)
    norm_pos = mx.sum(mx.abs(pos), axis=axis, keepdims=True)
    norm_guidance = mx.sum(mx.abs(guidance), axis=axis, keepdims=True)
    # The references map a NaN ratio to 10 ("clip"). NaN only arises when both norms are 0, where the guidance
    # is 0 whether clipped or not; a zero positive with a nonzero guidance gives +inf, which is clipped (to 0).
    ratio = norm_guidance / norm_pos
    adjustment = mx.where(ratio > tau, norm_pos * tau / (norm_guidance + _NORM_EPS), 1.0)
    guided = guidance * adjustment
    return (guided * alpha + pos * (1.0 - alpha)).astype(out_dtype)


__all__ = [
    "DEFAULT_NAG_ALPHA",
    "DEFAULT_NAG_SCALE",
    "DEFAULT_NAG_TAU",
    "NAGConfig",
    "NAGGuidance",
    "normalized_attention_guidance",
]
