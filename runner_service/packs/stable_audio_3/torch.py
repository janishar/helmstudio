"""Stable Audio 3 Small-SFX stages on torch: diffusers' StableAudio3 model classes (StableAudio3DiTModel,
StableAudio3DurationEmbedder, AutoencoderSAME) and transformers' T5GemmaEncoderModel, built on meta and filled by the
core loader from the original checkpoint (one model.safetensors split into dit and autoencoder by `rename`, plus the
t5gemma folder). The run is the reference's (github.com/Stability-AI/stable-audio-3 at 4007b6f,
StableAudioModel.generate, the sample script's code path), ported on those classes:

- the checkpoint is an inpaint model (model_type diffusion_cond_inpaint): every run feeds the DiT its local
  conditioning [mask, masked latents] (zeros when nothing is kept), which diffusers' text and audio-to-audio pipelines
  leave out, so the stages run their own loop (`sample`) rather than the pipelines;
- 6 s of headroom past the length (duration_padding_sec), the latent length on 8192 samples, the values of padded
  latents zeroed in self-attention (the reference's MPS path, `Padded`);
- the log-SNR schedule (diffusers' logsnr_sigma_schedule, -6.2 .. 2.0) and ping-pong sampling, 8 steps;
- the SAME-S of this checkpoint is not diffusers' SAME-S: its attention runs in chunks of two segments, the second
  half of the layers shifted by one (`_chunked`), its decoder maps channels with a 3-tap conv, its fused qkv rows are
  [q, k, v, q', k'] (`tensor_hook` reorders them), and at inference the bottleneck and the decoder's new tokens add a
  little noise, as the reference does.

Noise comes from torch's global generator seeded with the run's seed and drawn on the device in the reference's
order (start noise, one per step, the decoder's two), so a run repeats the reference's output on the same device
(tests/test_stable_audio_3_golden.py). The torch env runs one run at a time (qwen3_tts does the same)."""

from functools import lru_cache

import accelerate
import numpy as np
import torch
import torch.nn.functional as F
from torch import nn

from core import media
from core.graph import RunError

SAMPLE_RATE = 44100
STEPS = 8
LOGSNR = (
    -6.2,
    2.0,
)  # the reference's sampling shift: LogSNRShift(rate=0, anchor_logsnr=-6.2, logsnr_end=2.0)
HEADROOM_S = 6.0  # StableAudioModel.generate duration_padding_sec
RATIO = 4096  # samples per latent: patch 256 x stride 16
ALIGN = RATIO * 2  # the SAME chunk (32 frames) is 2 latents: the latent length is even
MAX_SAMPLES = 5292032  # model_config.json sample_size (120 s)
MAX_S = 60.0  # the longest take a stage writes (Extend: source + added)
BOTTLENECK_NOISE = 1e-3  # SoftNormBottleneck.decode at inference: x + randn * running_std * 1e-3
MASK_NOISE = 0.01  # the decoder's mask_noise: added to its new tokens, inference too
DIT = {"embed_dim": 1024, "depth": 20, "num_heads": 16, "use_differential_attention": False}
DIM_HEADS = 64  # SAME's attention heads (to_qkv reorder)


# --- components


class Padded:
    """The DiT's self-attention with the reference's padding mask on MPS: the values of tokens past `valid` (memory
    tokens and the valid latents) are zeroed; queries and keys are not masked."""

    valid: int | None = None

    def __call__(self, attn, hidden_states, rope):
        from diffusers.models.attention_dispatch import dispatch_attention_fn
        from diffusers.models.transformers.transformer_stable_audio3 import _apply_rotary_partial

        q, k, v = (x.unflatten(-1, (attn.heads, attn.dim_heads)) for x in attn.to_qkv(hidden_states).chunk(3, dim=-1))
        q, k = (
            _apply_rotary_partial(attn.q_norm(q), rope),
            _apply_rotary_partial(attn.k_norm(k), rope),
        )
        if self.valid is not None and self.valid < v.shape[1]:
            v = torch.cat([v[:, : self.valid], torch.zeros_like(v[:, self.valid :])], dim=1)
        return attn.to_out(dispatch_attention_fn(q, k, v).flatten(2, 3))


class Dit(nn.Module):
    """The DiT and the seconds_total embedder (both in model.safetensors)."""

    def __init__(self) -> None:
        from diffusers import StableAudio3DiTModel, StableAudio3DurationEmbedder

        super().__init__()
        self.dit = StableAudio3DiTModel(**DIT)
        self.duration = StableAudio3DurationEmbedder()
        self.padded = Padded()
        for block in self.dit.transformer_blocks:
            block.self_attn.processor = self.padded


def _chunked(block, x: torch.Tensor, sub: int, keep: int) -> torch.Tensor:
    """The reference's TransformerResamplingBlock attention (chunk_midpoint_shift, no sliding window): x (B, n * sub,
    C) holds n segments of `sub` tokens; the first half of the layers attend within chunks of two segments, the second
    half within chunks shifted by one segment (the first and last segments repeated as padding); then the last
    `keep` tokens of each segment, (B, n * keep, C)."""
    b, n, c = x.shape
    cc, split = 2 * sub, len(block.transformers) // 2
    x = x.reshape(-1, cc, c)
    for layer in block.transformers[:split]:
        x = layer(x)
    x = x.reshape(b, n, c)
    x = torch.cat([x[:, :sub], x, x[:, -sub:]], dim=1).reshape(-1, cc, c)
    for layer in block.transformers[split:]:
        x = layer(x)
    x = x.reshape(b, n + cc, c)[:, sub:-sub]
    return x.reshape(b, n // sub, sub, c)[:, :, -keep:].reshape(b, -1, c)


def _encode(block, x: torch.Tensor) -> torch.Tensor:
    """(B, C_in, T) -> (B, C_out, T / stride): T padded to a whole chunk (2 segments) before the mapping."""
    s = block.stride
    x = block.mapping(F.pad(x, (0, -x.shape[-1] % (2 * s)))).transpose(1, 2)
    b, t, c = x.shape
    x = torch.cat([x.reshape(-1, s, c), block.new_tokens.expand(b * t // s, 1, c)], dim=1)
    return _chunked(block, x.reshape(b, -1, c), s + 1, 1).transpose(1, 2)


def _decode(block, x: torch.Tensor) -> torch.Tensor:
    """(B, C_in, T) -> (B, C_out, T' * stride), T' = T padded to whole chunks (even); the new tokens with the
    reference's inference noise."""
    s = block.stride
    x = F.pad(x, (0, x.shape[-1] % 2)).transpose(1, 2)
    b, t, c = x.shape
    new = block.new_tokens.expand(b * t, s, c)
    new = new + torch.randn_like(new) * MASK_NOISE
    x = torch.cat([x.reshape(-1, 1, c), new], dim=1).reshape(b, -1, c)
    return block.mapping(_chunked(block, x, s + 1, s).transpose(1, 2))


def autoencoder():
    """SAME-S as this checkpoint builds it: diffusers' AutoencoderSAME with the reference's chunked resampling
    blocks and its decoder's 3-tap channel mapping."""
    from diffusers import AutoencoderSAME
    from torch.nn.utils import weight_norm

    vae = AutoencoderSAME()
    enc, dec = vae.encoder.blocks[0], vae.decoder.blocks[0]
    enc._encode = lambda x: _encode(enc, x)
    dec._decode = lambda x: _decode(dec, x)
    dec.mapping = weight_norm(nn.Conv1d(dec.in_channels, dec.out_channels, 3, padding="same"))
    return vae


def build(component: str, model) -> nn.Module:
    """dit, autoencoder or text_encoder on the meta device."""
    import warnings

    with accelerate.init_empty_weights(include_buffers=False), warnings.catch_warnings():
        warnings.simplefilter("ignore", FutureWarning)  # torch's old weight_norm, as diffusers builds it
        if component == "dit":
            return Dit()
        if component == "autoencoder":
            return autoencoder()
        from transformers import AutoConfig, T5GemmaEncoderModel

        config = AutoConfig.from_pretrained(model.folder(component) / "t5gemma-b-b-ul2")
        config.is_encoder_decoder = False  # the encoder only, as the reference's T5GemmaConditioner
        return T5GemmaEncoderModel(config)


DIT_KEYS = (
    ("model.model.transformer.layers.", "dit.transformer_blocks."),
    ("model.model.transformer.project_in.", "dit.proj_in."),
    ("model.model.transformer.project_out.", "dit.proj_out."),
    ("model.model.transformer.", "dit."),
    ("model.model.", "dit."),
    ("conditioner.conditioners.prompt.padding_embedding", "dit.prompt_padding_embedding"),
    ("conditioner.conditioners.seconds_total.embedder.embedding.1.", "duration.linear."),
)
VAE_KEYS = (
    ("pretransform.model.encoder.layers.0.", "encoder.blocks.0."),
    ("pretransform.model.encoder.layers.2.", "encoder.proj."),
    ("pretransform.model.decoder.layers.1.", "decoder.proj."),
    ("pretransform.model.decoder.layers.3.", "decoder.blocks.0."),
    ("pretransform.model.bottleneck.scaling_factor", "bottleneck.scale"),
    ("pretransform.model.bottleneck.", "bottleneck."),
)
PARTS = (  # inside a SAME transformer and a DiT block
    (".self_attn.", ".attn."),
    (".pre_norm.", ".norm_attn."),
    (".ff_norm.", ".norm_ff."),
    (".rope.", ".attn.rope."),
)
FF = ((".ff.ff.0.proj.", ".ff.proj_in."), (".ff.ff.2.", ".ff.proj_out."))


def _swap(key: str, pairs) -> str | None:
    for old, new in pairs:
        if key.startswith(old):
            return new + key[len(old) :]
    return None


def rename(component: str, key: str) -> str | None:
    """The original checkpoint's names for diffusers' classes: model.model.* and two conditioner tensors are the
    dit, pretransform.model.* the autoencoder (its empty noise_scaling_factor dropped), the t5gemma folder's encoder
    the text encoder (its decoder never read)."""
    if component == "text_encoder":
        return key.removeprefix("model.") if key.startswith("model.encoder.") else None
    if component == "dit":
        name = _swap(key, DIT_KEYS)
    else:
        name = None if key.endswith("noise_scaling_factor") else _swap(key, VAE_KEYS)
        if name and ".transformers." in name:
            for old, new in PARTS:
                name = name.replace(old, new)
    for old, new in FF:
        name = name.replace(old, new) if name else None
    return name


def tensor_hook(component: str, model):
    """SAME's fused qkv rows: the reference's [q, k, v, q', k'] as diffusers' [q, q', k, k', v]."""
    if component != "autoencoder":
        return None

    def hook(name: str, t: torch.Tensor) -> torch.Tensor:
        if not name.endswith(".attn.to_qkv.weight"):
            return t
        q, k, v, q2, k2 = t.chunk(5, dim=0)
        return torch.cat([q, q2, k, k2, v], dim=0)

    return hook


# --- the run


@lru_cache(maxsize=1)
def tokenizer(folder):
    from transformers import AutoTokenizer

    return AutoTokenizer.from_pretrained(folder)


def latent_length(seconds: float) -> int:
    """StableAudioModel._adapt_sample_size: (seconds + headroom) of samples up to whole ALIGN, at most sample_size,
    in latents."""
    n = int((seconds + HEADROOM_S) * SAMPLE_RATE)
    return min(-(-n // ALIGN) * ALIGN, MAX_SAMPLES) // RATIO


def valid_latents(seconds: float, length: int) -> int:
    """sample_diffusion's padding mask: ceil(seconds of samples / RATIO) + the headroom's latents, at most `length`."""
    return min(-(-int(seconds * SAMPLE_RATE) // RATIO) + int(HEADROOM_S * SAMPLE_RATE / RATIO), length)


def waveform(value: dict) -> torch.Tensor:
    """A media value's sound as float32 (2, samples) at 44.1 kHz stereo."""
    pcm = np.frombuffer(media.pcm(value["data"], SAMPLE_RATE, 2), "<i2")
    return torch.from_numpy(pcm.reshape(-1, 2).T.astype(np.float32) / 32768)


def place(audio: torch.Tensor, at: int, samples: int) -> torch.Tensor:
    """`audio` (2, n) at sample `at` of `samples` of silence, cut to fit (PadCrop)."""
    out = torch.zeros(2, samples)
    n = max(0, min(audio.shape[-1], samples - at))
    out[:, at : at + n] = audio[:, :n]
    return out


def sample(
    ctx,
    model,
    text: str,
    seconds: float,
    seed: int,
    *,
    init=None,
    change=1.0,
    keep=None,
    audio=None,
):
    """One generation as StableAudioModel.generate: `seconds` long; `init` (2, samples) a source to vary from at
    noise level `change`; `keep` (samples,) 1 where `audio` (2, samples) is kept (inpaint), 0 where it is made.
    Returns float32 (2, seconds * rate) in [-1, 1]."""
    dit = ctx.component(model, "dit")
    vae = ctx.component(model, "autoencoder")
    te = ctx.component(model, "text_encoder")
    dev = next(dit.parameters()).device
    length = latent_length(seconds)
    samples = length * RATIO
    with torch.inference_mode():
        torch.manual_seed(seed)
        x = torch.randn([1, 256, length], device=dev)
        tok = tokenizer(model.folder("text_encoder") / "t5gemma-b-b-ul2")
        ids = tok([text], truncation=True, max_length=256, padding="max_length", return_tensors="pt")
        mask = ids["attention_mask"].to(dev).bool()
        emb = te(input_ids=ids["input_ids"].to(dev), attention_mask=mask)["last_hidden_state"].float()
        glob = dit.duration(torch.tensor([float(seconds)], device=dev))
        context = torch.cat([emb, glob[:, None]], dim=1)
        context_mask = torch.cat([mask, mask.new_ones(1, 1)], dim=1)
        ctx.check()

        def encode(wave: torch.Tensor) -> torch.Tensor:
            # contiguous, as the reference's .repeat(batch) leaves it: randn_like follows a tensor's strides, so the
            # step noise drawn after mixing with a transposed latent would land elsewhere
            return vae.encode(place(wave, 0, samples)[None].to(dev)).latents.contiguous()

        local = torch.zeros(1, 257, length, device=dev)
        if keep is not None:
            m = place(keep[None].repeat(2, 1), 0, samples)[:1]
            m[:, int(seconds * SAMPLE_RATE) :] = 0  # the reference: nothing kept past seconds_total
            m = F.interpolate(m[None].to(dev), size=length, mode="nearest")[0]
            local = torch.cat([m, encode(audio)[0] * m], dim=0)[None]
        sigma_max = change if init is not None else 1.0
        if init is not None:
            x = encode(init) * (1 - sigma_max) + x * sigma_max
        from diffusers.pipelines.stable_audio_3.pipeline_stable_audio_3 import logsnr_sigma_schedule

        t = [*logsnr_sigma_schedule(STEPS, *LOGSNR, sigma_max=sigma_max), 0.0]
        dit.padded.valid = 64 + valid_latents(seconds, length)  # 64 memory tokens
        for i in range(STEPS):
            ctx.check()
            v = dit.dit(x, torch.full((1,), t[i], device=dev), encoder_hidden_states=context,
                        global_hidden_states=glob, encoder_attention_mask=context_mask, local_add_cond=local,
                        return_dict=False)[0]  # fmt: skip
            denoised = x - t[i] * v
            x = (1 - t[i + 1]) * denoised + t[i + 1] * torch.randn_like(x)
            ctx.progress(i + 1, STEPS, "sampling")
        z = x * vae.bottleneck.running_std
        z = z + torch.randn_like(z) * vae.bottleneck.running_std * BOTTLENECK_NOISE
        out = vae.patch_embed.decode(vae.decoder(z))
        n = valid_latents(seconds, length) * RATIO
        out[..., n:] = 0
        return out[0, :, : int(seconds * SAMPLE_RATE)].float().clamp(-1, 1).cpu()


def take(wave: torch.Tensor) -> dict:
    """A stereo Audio value (PCM 16-bit interleaved) of float (2, n), levels as the model gives them (sample clamps them to [-1, 1])."""
    return {"audio": {"wav": media.pcm16(wave.T.numpy()), "sample_rate": SAMPLE_RATE, "channels": 2,
                      "duration_s": wave.shape[-1] / SAMPLE_RATE}}  # fmt: skip


def _text(ctx, inputs: dict) -> str:
    text = inputs["text"].strip()
    if not text:
        raise RunError(f"{ctx.node}: describe the sound")
    return text


def _source(ctx, params: dict) -> torch.Tensor:
    wave = waveform(params["source"])
    if wave.shape[-1] < SAMPLE_RATE // 10:
        raise RunError(f"{ctx.node}.source: the sound is too short (under 0.1 s)")
    return wave


# --- stages


def sound(ctx, params: dict, inputs: dict) -> dict:
    """Text to sound, Length seconds; From a sound: varied from that sound at Change."""
    rows = params["from_sound"]
    init = waveform(rows[0]["audio"]) if rows else None
    return take(sample(ctx, inputs["model"], _text(ctx, inputs), params["seconds"], inputs["seed"],
                       init=init, change=params["change"]))  # fmt: skip


def vary(ctx, params: dict, inputs: dict) -> dict:
    """The source made again at noise level Change (low: close to it), as long as it."""
    wave = _source(ctx, params)
    seconds = wave.shape[-1] / SAMPLE_RATE
    return take(sample(ctx, inputs["model"], _text(ctx, inputs), seconds, inputs["seed"], init=wave,
                       change=params["change"]))  # fmt: skip


def repaint(ctx, params: dict, inputs: dict) -> dict:
    """The source with the span made again from the prompt (inpaint), the rest kept."""
    wave, span = _source(ctx, params), params["span"]
    n = wave.shape[-1]
    keep = torch.ones(n)
    keep[int(span["start"] * SAMPLE_RATE) : int(span["end"] * SAMPLE_RATE)] = 0
    return take(sample(ctx, inputs["model"], _text(ctx, inputs), n / SAMPLE_RATE, inputs["seed"], keep=keep,
                       audio=wave))  # fmt: skip


def extend(ctx, params: dict, inputs: dict) -> dict:
    """The source with Add seconds made after its end (End) or before its start (Start): an inpaint of the new part."""
    wave = _source(ctx, params)
    n, add = wave.shape[-1], int(params["seconds"] * SAMPLE_RATE)
    if (n + add) / SAMPLE_RATE > MAX_S:
        raise RunError(f"{ctx.node}.seconds: the sound would last over {MAX_S:g} s")
    at = add if params["where"] == "start" else 0
    keep = place(torch.ones(1, n), at, n + add)[0]
    return take(sample(ctx, inputs["model"], _text(ctx, inputs), (n + add) / SAMPLE_RATE, inputs["seed"],
                       keep=keep, audio=place(wave, at, n + add)))  # fmt: skip
