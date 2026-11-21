"""Runs a Transformers Qwen3Model one position at a time over a key/value cache of fixed size.

Every step then has the same tensor shapes, so torch.compile can fuse its hundreds of small GPU operations into a
few kernels. The code predictor takes 16 such steps for every 80 ms of speech, and on Apple GPUs launching those
operations, not the arithmetic, is what takes the time: compiled, it runs about twice as fast.
"""

import torch
import torch.nn.functional as F
from transformers import Qwen3Model


class CachedDecoder:
    def __init__(self, model: Qwen3Model, max_positions: int, compile: bool):
        config = model.config
        self.layers = model.layers
        self.norm = model.norm
        self.heads = config.num_attention_heads
        self.kv_heads = config.num_key_value_heads
        self.head_dim = config.head_dim
        self.max_positions = max_positions
        self.length = 0  # positions written so far

        weight = model.norm.weight
        shape = (len(model.layers), 1, self.kv_heads, max_positions, self.head_dim)
        # Zeros, not empty memory: attention weighs masked slots by exactly 0, and 0 x NaN would still be NaN.
        self.keys = torch.zeros(shape, device=weight.device, dtype=weight.dtype)
        self.values = torch.zeros_like(self.keys)
        positions = torch.arange(max_positions, device=weight.device)
        angles = positions.float()[:, None] * model.rotary_emb.inv_freq.float()[None, :]
        angles = torch.cat((angles, angles), dim=-1)
        self.cos, self.sin = angles.cos().to(weight.dtype), angles.sin().to(weight.dtype)
        self.visible = positions[None, :] <= positions[:, None]  # [query, slot]: the causal mask
        # One tensor per position for the compiled step, so every call gets an input of the same kind rather than
        # slices at shifting offsets.
        self.spans = [torch.tensor([position], device=weight.device) for position in range(max_positions)]
        self._step = torch.compile(self._forward, dynamic=False) if compile else self._forward

    def reset(self) -> None:
        """Start a new sequence. Old entries stay in the cache but are masked out until overwritten."""
        self.length = 0

    def step(self, embedding: torch.Tensor) -> torch.Tensor:
        """One more position (1, 1, hidden); returns its output (1, 1, hidden)."""
        span = self.spans[self.length]
        self.length += 1
        return self._step(embedding, span, self.keys, self.values)

    def _forward(self, hidden: torch.Tensor, span: torch.Tensor, keys: torch.Tensor, values: torch.Tensor):
        """The decoder layers of Qwen3 (pre-norm attention with per-head q/k norms and RoPE, then SwiGLU)."""
        length = hidden.shape[1]
        cos, sin = self.cos[span], self.sin[span]
        mask = self.visible[span][None, None]
        for number, layer in enumerate(self.layers):
            attention = layer.self_attn
            x = layer.input_layernorm(hidden)
            query = attention.q_norm(attention.q_proj(x).view(1, length, self.heads, self.head_dim)).transpose(1, 2)
            key = attention.k_norm(attention.k_proj(x).view(1, length, self.kv_heads, self.head_dim)).transpose(1, 2)
            value = attention.v_proj(x).view(1, length, self.kv_heads, self.head_dim).transpose(1, 2)
            query, key = rotate(query, cos, sin), rotate(key, cos, sin)
            keys[number].index_copy_(2, span, key)
            values[number].index_copy_(2, span, value)
            attended = F.scaled_dot_product_attention(
                query, keys[number], values[number], attn_mask=mask, enable_gqa=True
            )
            hidden = hidden + attention.o_proj(attended.transpose(1, 2).reshape(1, length, -1))
            hidden = hidden + layer.mlp(layer.post_attention_layernorm(hidden))
        return self.norm(hidden)


def rotate(states: torch.Tensor, cos: torch.Tensor, sin: torch.Tensor) -> torch.Tensor:
    """Rotary position embedding (rotate-half form)."""
    half = states.shape[-1] // 2
    rotated = torch.cat((-states[..., half:], states[..., :half]), dim=-1)
    return states * cos + rotated * sin
