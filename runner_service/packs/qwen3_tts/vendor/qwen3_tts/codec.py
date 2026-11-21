"""The decoder half of Qwen3-TTS's 12 Hz speech tokenizer: 16 codes per 80 ms frame in, a 24 kHz waveform out.

    codes (16 x frames) -> quantizer lookup -> causal conv -> transformer -> 2 x ConvNeXt upsampling
                        -> 4 x SnakeBeta upsampling blocks -> waveform (1,920 samples per frame)

Ported to plain PyTorch from the official qwen-tts package (Apache-2.0). Module and attribute names follow the
checkpoint (`speech_tokenizer/model.safetensors`, keys under `decoder.`), so its weights load as they are.
The tokenizer's encoder, which turns audio into codes for voice cloning, is not needed to speak and is left out.
"""

import math
from dataclasses import dataclass

import torch
import torch.nn.functional as F
from torch import nn


@dataclass(frozen=True)
class CodecConfig:
    """`decoder_config` of speech_tokenizer/config.json."""

    codebook_size: int = 2048
    codebook_dim: int = 512
    latent_dim: int = 1024
    decoder_dim: int = 1536
    hidden_size: int = 512
    intermediate_size: int = 1024
    num_hidden_layers: int = 8
    num_attention_heads: int = 16
    head_dim: int = 64
    num_quantizers: int = 16
    rms_norm_eps: float = 1e-5
    rope_theta: float = 10_000.0
    sliding_window: int = 72
    upsample_rates: tuple[int, ...] = (8, 5, 4, 3)
    upsampling_ratios: tuple[int, ...] = (2, 2)

    @classmethod
    def from_dict(cls, values: dict) -> "CodecConfig":
        fields = {name: values[name] for name in cls.__dataclass_fields__ if name in values}
        for name in ("upsample_rates", "upsampling_ratios"):
            if name in fields:
                fields[name] = tuple(fields[name])
        return cls(**fields)

    @property
    def samples_per_frame(self) -> int:
        return math.prod(self.upsample_rates + self.upsampling_ratios)


class CodecDecoder(nn.Module):
    def __init__(self, config: CodecConfig):
        super().__init__()
        self.config = config
        self.quantizer = SplitResidualVectorQuantizer(config)
        self.pre_conv = CausalConv(config.codebook_dim, config.latent_dim, kernel_size=3)
        self.pre_transformer = CodecTransformer(config)
        self.upsample = nn.ModuleList(
            nn.ModuleList(
                [
                    CausalTransposedConv(config.latent_dim, config.latent_dim, factor, factor),
                    ConvNeXtBlock(config.latent_dim),
                ]
            )
            for factor in config.upsampling_ratios
        )
        blocks = [CausalConv(config.latent_dim, config.decoder_dim, kernel_size=7)]
        blocks += [DecoderBlock(config, index) for index in range(len(config.upsample_rates))]
        output_dim = config.decoder_dim // 2 ** len(config.upsample_rates)
        blocks += [SnakeBeta(output_dim), CausalConv(output_dim, 1, kernel_size=7)]
        self.decoder = nn.ModuleList(blocks)

    def forward(self, codes: torch.Tensor) -> torch.Tensor:
        """codes: (batch, 16, frames) -> waveform (batch, 1, frames * 1920) in [-1, 1]."""
        hidden = self.pre_conv(self.quantizer.decode(codes))
        hidden = self.pre_transformer(hidden.transpose(1, 2)).transpose(1, 2)
        for transposed_conv, convnext in self.upsample:
            hidden = convnext(transposed_conv(hidden))
        for block in self.decoder:
            hidden = block(hidden)
        return hidden.clamp(min=-1, max=1)

    def decode(self, codes: torch.Tensor, chunk_frames: int = 300, context_frames: int = 25) -> torch.Tensor:
        """Decode a long (batch, 16, frames) sequence in chunks, each started with some frames of left context."""
        pieces = []
        for start in range(0, codes.shape[-1], chunk_frames):
            context = min(context_frames, start)
            waveform = self(codes[..., start - context : start + chunk_frames])
            pieces.append(waveform[..., context * self.config.samples_per_frame :])
        return torch.cat(pieces, dim=-1)


# ---------- quantizer: codes -> latent vectors ----------


class Codebook(nn.Module):
    def __init__(self, dim: int, size: int):
        super().__init__()
        # Stored as running sums and counts (from training by exponential moving average): a code's vector is
        # its sum divided by its count.
        self.cluster_usage = nn.Parameter(torch.ones(size))
        self.embedding_sum = nn.Parameter(torch.zeros(size, dim))

    def decode(self, codes: torch.Tensor) -> torch.Tensor:
        embedding = self.embedding_sum / self.cluster_usage.clamp(min=1e-5)[:, None]
        return F.embedding(codes, embedding)


class VectorQuantization(nn.Module):
    def __init__(self, dim: int, size: int):
        super().__init__()
        self._codebook = Codebook(dim, size)

    def decode(self, codes: torch.Tensor) -> torch.Tensor:
        return self._codebook.decode(codes).transpose(1, 2)  # (batch, dim, frames)


class ResidualVectorQuantizer(nn.Module):
    """Several codebooks whose vectors add up; 1x1 convolutions map between the codebook and outer widths."""

    def __init__(self, num_quantizers: int, dim: int, outer_dim: int, size: int):
        super().__init__()
        self.input_proj = nn.Conv1d(outer_dim, dim, 1, bias=False)  # encoding only; kept so the checkpoint loads
        self.output_proj = nn.Conv1d(dim, outer_dim, 1, bias=False)
        self.vq = nn.ModuleDict({"layers": nn.ModuleList(VectorQuantization(dim, size) for _ in range(num_quantizers))})

    def decode(self, codes: torch.Tensor) -> torch.Tensor:
        """codes: (batch, codebooks, frames) -> (batch, outer_dim, frames)."""
        quantized = sum(layer.decode(codes[:, index]) for index, layer in enumerate(self.vq["layers"]))
        return self.output_proj(quantized)


class SplitResidualVectorQuantizer(nn.Module):
    """The first codebook (semantic) and the other 15 (acoustic) have their own projections."""

    def __init__(self, config: CodecConfig):
        super().__init__()
        dim = config.codebook_dim // 2
        self.rvq_first = ResidualVectorQuantizer(1, dim, config.codebook_dim, config.codebook_size)
        self.rvq_rest = ResidualVectorQuantizer(
            config.num_quantizers - 1, dim, config.codebook_dim, config.codebook_size
        )

    def decode(self, codes: torch.Tensor) -> torch.Tensor:
        return self.rvq_first.decode(codes[:, :1]) + self.rvq_rest.decode(codes[:, 1:])


# ---------- convolutions ----------


class CausalConv(nn.Module):
    """A 1-D convolution padded on the left only, so no output sample depends on a later input."""

    def __init__(self, in_channels: int, out_channels: int, kernel_size: int, dilation: int = 1, groups: int = 1):
        super().__init__()
        self.conv = nn.Conv1d(in_channels, out_channels, kernel_size, dilation=dilation, groups=groups)
        self.padding = (kernel_size - 1) * dilation

    def forward(self, hidden: torch.Tensor) -> torch.Tensor:
        return self.conv(F.pad(hidden, (self.padding, 0)))


class CausalTransposedConv(nn.Module):
    """Upsamples by `stride`; the samples that would need future input are cut off the right end."""

    def __init__(self, in_channels: int, out_channels: int, kernel_size: int, stride: int):
        super().__init__()
        self.conv = nn.ConvTranspose1d(in_channels, out_channels, kernel_size, stride=stride)
        self.trim = kernel_size - stride

    def forward(self, hidden: torch.Tensor) -> torch.Tensor:
        hidden = self.conv(hidden)
        return hidden[..., : hidden.shape[-1] - self.trim] if self.trim else hidden


class ConvNeXtBlock(nn.Module):
    def __init__(self, dim: int):
        super().__init__()
        self.dwconv = CausalConv(dim, dim, kernel_size=7, groups=dim)
        self.norm = nn.LayerNorm(dim, eps=1e-6)
        self.pwconv1 = nn.Linear(dim, 4 * dim)
        self.act = nn.GELU()
        self.pwconv2 = nn.Linear(4 * dim, dim)
        self.gamma = nn.Parameter(torch.full((dim,), 1e-6))

    def forward(self, hidden: torch.Tensor) -> torch.Tensor:
        residual = hidden
        hidden = self.norm(self.dwconv(hidden).transpose(1, 2))
        hidden = self.gamma * self.pwconv2(self.act(self.pwconv1(hidden)))
        return residual + hidden.transpose(1, 2)


class SnakeBeta(nn.Module):
    """x + sin²(αx) / β, a periodic activation that suits waveforms (α and β are learnt per channel, as logs)."""

    def __init__(self, channels: int):
        super().__init__()
        self.alpha = nn.Parameter(torch.zeros(channels))
        self.beta = nn.Parameter(torch.zeros(channels))

    def forward(self, hidden: torch.Tensor) -> torch.Tensor:
        alpha = self.alpha.exp()[None, :, None]
        beta = self.beta.exp()[None, :, None]
        return hidden + torch.sin(hidden * alpha).pow(2) / (beta + 1e-9)


class ResidualUnit(nn.Module):
    def __init__(self, dim: int, dilation: int):
        super().__init__()
        self.act1 = SnakeBeta(dim)
        self.conv1 = CausalConv(dim, dim, kernel_size=7, dilation=dilation)
        self.act2 = SnakeBeta(dim)
        self.conv2 = CausalConv(dim, dim, kernel_size=1)

    def forward(self, hidden: torch.Tensor) -> torch.Tensor:
        return hidden + self.conv2(self.act2(self.conv1(self.act1(hidden))))


class DecoderBlock(nn.Module):
    """Halves the channels and upsamples time by the block's rate."""

    def __init__(self, config: CodecConfig, index: int):
        super().__init__()
        in_dim = config.decoder_dim // 2**index
        out_dim = config.decoder_dim // 2 ** (index + 1)
        rate = config.upsample_rates[index]
        layers = [SnakeBeta(in_dim), CausalTransposedConv(in_dim, out_dim, 2 * rate, rate)]
        layers += [ResidualUnit(out_dim, dilation) for dilation in (1, 3, 9)]
        self.block = nn.ModuleList(layers)

    def forward(self, hidden: torch.Tensor) -> torch.Tensor:
        for layer in self.block:
            hidden = layer(hidden)
        return hidden


# ---------- the transformer between the quantizer and the upsampling ----------


class RMSNorm(nn.Module):
    def __init__(self, dim: int, eps: float):
        super().__init__()
        self.weight = nn.Parameter(torch.ones(dim))
        self.eps = eps

    def forward(self, hidden: torch.Tensor) -> torch.Tensor:
        variance = hidden.float().pow(2).mean(-1, keepdim=True)
        return self.weight * (hidden.float() * torch.rsqrt(variance + self.eps)).to(hidden.dtype)


class LayerScale(nn.Module):
    def __init__(self, dim: int):
        super().__init__()
        self.scale = nn.Parameter(torch.full((dim,), 0.01))

    def forward(self, hidden: torch.Tensor) -> torch.Tensor:
        return self.scale * hidden


class Attention(nn.Module):
    def __init__(self, config: CodecConfig):
        super().__init__()
        self.heads = config.num_attention_heads
        self.head_dim = config.head_dim
        width = self.heads * self.head_dim
        self.q_proj = nn.Linear(config.hidden_size, width, bias=False)
        self.k_proj = nn.Linear(config.hidden_size, width, bias=False)
        self.v_proj = nn.Linear(config.hidden_size, width, bias=False)
        self.o_proj = nn.Linear(width, config.hidden_size, bias=False)

    def forward(self, hidden: torch.Tensor, cos: torch.Tensor, sin: torch.Tensor, mask: torch.Tensor) -> torch.Tensor:
        batch, length, _ = hidden.shape

        def heads(projection: nn.Linear) -> torch.Tensor:
            return projection(hidden).view(batch, length, self.heads, self.head_dim).transpose(1, 2)

        query, key = apply_rope(heads(self.q_proj), cos, sin), apply_rope(heads(self.k_proj), cos, sin)
        output = F.scaled_dot_product_attention(query, key, heads(self.v_proj), attn_mask=mask)
        return self.o_proj(output.transpose(1, 2).reshape(batch, length, -1))


class MLP(nn.Module):
    def __init__(self, config: CodecConfig):
        super().__init__()
        self.gate_proj = nn.Linear(config.hidden_size, config.intermediate_size, bias=False)
        self.up_proj = nn.Linear(config.hidden_size, config.intermediate_size, bias=False)
        self.down_proj = nn.Linear(config.intermediate_size, config.hidden_size, bias=False)

    def forward(self, hidden: torch.Tensor) -> torch.Tensor:
        return self.down_proj(F.silu(self.gate_proj(hidden)) * self.up_proj(hidden))


class TransformerLayer(nn.Module):
    def __init__(self, config: CodecConfig):
        super().__init__()
        self.input_layernorm = RMSNorm(config.hidden_size, config.rms_norm_eps)
        self.self_attn = Attention(config)
        self.self_attn_layer_scale = LayerScale(config.hidden_size)
        self.post_attention_layernorm = RMSNorm(config.hidden_size, config.rms_norm_eps)
        self.mlp = MLP(config)
        self.mlp_layer_scale = LayerScale(config.hidden_size)

    def forward(self, hidden: torch.Tensor, cos: torch.Tensor, sin: torch.Tensor, mask: torch.Tensor) -> torch.Tensor:
        hidden = hidden + self.self_attn_layer_scale(self.self_attn(self.input_layernorm(hidden), cos, sin, mask))
        return hidden + self.mlp_layer_scale(self.mlp(self.post_attention_layernorm(hidden)))


class CodecTransformer(nn.Module):
    """Causal transformer over the frames; each frame attends to itself and the 71 frames before it."""

    def __init__(self, config: CodecConfig):
        super().__init__()
        self.config = config
        self.input_proj = nn.Linear(config.latent_dim, config.hidden_size)
        self.layers = nn.ModuleList(TransformerLayer(config) for _ in range(config.num_hidden_layers))
        self.norm = RMSNorm(config.hidden_size, config.rms_norm_eps)
        self.output_proj = nn.Linear(config.hidden_size, config.latent_dim)

    def forward(self, hidden: torch.Tensor) -> torch.Tensor:
        """hidden: (batch, frames, latent_dim) -> same shape."""
        length = hidden.shape[1]
        positions = torch.arange(length, device=hidden.device)
        distance = positions[:, None] - positions[None, :]
        mask = (distance >= 0) & (distance < self.config.sliding_window)
        cos, sin = rope_angles(positions, self.config.head_dim, self.config.rope_theta, hidden.dtype)

        hidden = self.input_proj(hidden)
        for layer in self.layers:
            hidden = layer(hidden, cos, sin, mask)
        return self.output_proj(self.norm(hidden))


def rope_angles(positions: torch.Tensor, head_dim: int, theta: float, dtype: torch.dtype):
    inverse_frequency = 1.0 / theta ** (torch.arange(0, head_dim, 2, device=positions.device).float() / head_dim)
    angles = positions.float()[:, None] * inverse_frequency[None, :]
    angles = torch.cat((angles, angles), dim=-1)
    return angles.cos().to(dtype), angles.sin().to(dtype)


def apply_rope(states: torch.Tensor, cos: torch.Tensor, sin: torch.Tensor) -> torch.Tensor:
    half = states.shape[-1] // 2
    rotated = torch.cat((-states[..., half:], states[..., :half]), dim=-1)
    return states * cos + rotated * sin
