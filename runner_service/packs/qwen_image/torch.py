"""Qwen Image 2.1 stages on torch: the generation path of the qwen-image-2.1 studio (its generate.py, which calls
diffusers' QwenImage21Pipeline) split into encode, sample and decode, with every component loaded by the core
loader. Same settings, same pixels: the raw chat template, the pre-norm hidden states, the CPU noise generator,
Turbo's baked sigmas vs Base's steps + dynamic shift, the KV cache, no CFG, whole (untiled) decode up to 2.5 MP.

Not ported: reference images (multi-reference edit; stage 1 has no Image source), the prompt enhancer, CFG."""

import json
from dataclasses import dataclass
from functools import lru_cache

import accelerate
import numpy as np
import torch
from diffusers import (
    AutoencoderKLQwenImage21,
    FlowMatchEulerDiscreteScheduler,
    QwenImage21Transformer2DModel,
)
from diffusers.image_processor import VaeImageProcessor
from diffusers.models.transformers.transformer_qwenimage21 import QwenImage21KVCache
from diffusers.pipelines.qwenimage21.pipeline_qwenimage21 import (
    calculate_shift,
    retrieve_timesteps,
)
from diffusers.utils.torch_utils import randn_tensor
from transformers import AutoConfig, AutoProcessor, Qwen3VLForConditionalGeneration

from core.graph import RunError

SYSTEM = "Comprehend and analyze the provided prompt."
TEMPLATE = f"<|im_start|>system\n{SYSTEM}<|im_end|>\n<|im_start|>user\n{{}}<|im_end|>\n<|im_start|>assistant\n"
SCALE = 16  # the VAE compresses 16x; sizes are floored to multiples of 32
# tiled: auto tiles above this many MP (the studio tiled only when asked; the goldens are <= 1 MP)
AUTO_TILE_MP = 2.5


@dataclass
class Cond:
    embeds: torch.Tensor  # [1, L, 4096] bf16 on the device; one unpadded prompt, so no mask


@dataclass
class Latent:
    latents: torch.Tensor  # packed [1, h*w, 64]
    width: int
    height: int


def build(component: str, model):
    """The component on the meta device (rotary buffers stay real, on the CPU; the loader moves them)."""
    sub = model.folder(component) / component
    with accelerate.init_empty_weights(include_buffers=False):
        if component == "text_encoder":
            return Qwen3VLForConditionalGeneration(AutoConfig.from_pretrained(sub))
        cls = {"transformer": QwenImage21Transformer2DModel, "vae": AutoencoderKLQwenImage21}[
            component
        ]
        return cls.from_config(cls.load_config(sub))


@lru_cache(maxsize=2)
def processor(folder):
    """(processor, number of leading system tokens to drop). Read from the text encoder's variant (base): Turbo's
    copy differs only in offsets and decoding, so both give the same ids and encode stays one cache identity."""
    p = AutoProcessor.from_pretrained(folder / "processor")
    system = [{"role": "system", "content": [{"type": "text", "text": SYSTEM}]}]
    return p, len(p.apply_chat_template(system, tokenize=True, return_dict=False)[0])


@torch.no_grad()
def encode(ctx, params: dict, inputs: dict) -> dict:
    text = inputs["prompt"]
    if not text.strip():
        raise RunError("empty prompt")
    if params["transparent"]:
        text = (
            "This is an RGBA image with transparency. "
            f"{text} The image has alpha channel and the background is transparent."
        )
    model = inputs["model"]
    te = ctx.component(model, "text_encoder")
    proc, drop = processor(model.folder("text_encoder"))
    ctx.check()
    batch = proc(
        text=[TEMPLATE.format(text)], padding=True, padding_side="left", return_tensors="pt"
    ).to(ctx.device)
    kw = {"input_ids": batch.input_ids, "attention_mask": batch.attention_mask}
    if hasattr(batch, "mm_token_type_ids"):
        kw["mm_token_type_ids"] = batch.mm_token_type_ids
    # transformers 5 returns hidden_states[-1] after the final RMSNorm; the transformer was trained on the
    # pre-norm states, so a hook hands back the norm's input (the pipeline's own workaround)
    lm = getattr(te.model, "language_model", te.model)
    hook = lm.norm.register_forward_hook(lambda module, args, output: args[0])
    try:
        hidden = te(**kw, output_hidden_states=True).hidden_states[-1]
    finally:
        hook.remove()
    embeds = hidden[0][batch.attention_mask[0].bool()][drop:]
    ctx.progress(1, 1, "encoding")
    return {"cond": Cond(embeds[None].clone())}


@torch.no_grad()
def sample(ctx, params: dict, inputs: dict) -> dict:
    model, cond, canvas = inputs["model"], inputs["cond"], inputs["canvas"]
    dit = ctx.component(model, "transformer")
    width, height = canvas["width"] // 32 * 32, canvas["height"] // 32 * 32
    h, w = height // SCALE, width // SCALE
    embeds, dev = cond.embeds, ctx.device

    generator = torch.Generator("cpu").manual_seed(inputs["seed"])  # a seed repeats across devices
    channels = dit.config.in_channels
    noise = randn_tensor(
        (1, 1, channels, h, w), generator=generator, device=dev, dtype=embeds.dtype
    )
    latents = noise.view(1, channels, h * w).transpose(1, 2)

    folder = model.folder()
    scheduler = FlowMatchEulerDiscreteScheduler.from_pretrained(folder / "scheduler")
    # Turbo bakes its 8 sigmas into model_index.json and ignores steps; base spaces `steps` and shifts them
    sigmas = json.loads((folder / "model_index.json").read_text()).get("sample_sigmas")
    if sigmas is None:
        sigmas = np.linspace(1.0, 1 / params["steps"], params["steps"])
    cfg = scheduler.config
    mu = calculate_shift(
        latents.shape[1],
        cfg.get("base_image_seq_len", 256),
        cfg.get("max_image_seq_len", 4096),
        cfg.get("base_shift", 0.5),
        cfg.get("max_shift", 1.15),
    )
    timesteps, _ = retrieve_timesteps(scheduler, None, dev, sigmas=sigmas, mu=mu)
    scheduler.set_begin_index(0)

    img_shapes = [[(1, h, w)]]
    no_image = torch.zeros(embeds.shape[:2], dtype=torch.bool, device=embeds.device)
    img_mask = torch.cat([no_image, no_image.new_ones(1, latents.shape[1] // 4)], dim=1)
    # text keys/values are step-independent under causal_condition: step 0 fills the cache, later steps reuse it
    cache = QwenImage21KVCache(len(dit.transformer_blocks)) if dit.config.causal_condition else None
    n = len(timesteps)
    for i, t in enumerate(timesteps):
        ctx.check()
        mode = None if cache is None else ("extract" if i == 0 else "cached")
        with dit.cache_context("cond"):
            pred = dit(
                hidden_states=latents,
                timestep=t.expand(1).to(latents.dtype) / 1000,
                encoder_hidden_states=embeds,
                encoder_hidden_states_mask=None,
                img_shapes=img_shapes,
                img_mask=img_mask,
                attention_kwargs={},
                kv_cache=cache,
                kv_cache_mode=mode,
                return_dict=False,
            )[0]
        dtype = latents.dtype
        latents = scheduler.step(pred[:, -latents.size(1) :], t, latents, return_dict=False)[0]
        latents = latents.to(dtype)  # MPS returns fp32 here (pytorch#99272)
        if dev == "mps":
            torch.mps.synchronize()  # so progress, ETA and cancel follow the GPU, not the queue
        ctx.progress(i + 1, n, "sampling")
    return {"latent": Latent(latents.clone(), width, height)}


def tiles(param: str, width: int, height: int) -> bool:
    return param == "on" or (param == "auto" and width * height / 1e6 > AUTO_TILE_MP)


@torch.no_grad()
def decode(ctx, params: dict, inputs: dict) -> dict:
    model, latent = inputs["model"], inputs["latent"]
    vae = ctx.component(model, "vae")
    ctx.check()
    h, w = latent.height // SCALE, latent.width // SCALE
    z = latent.latents.transpose(1, 2).reshape(1, -1, 1, h, w).to(vae.dtype)
    shape = (1, vae.config.z_dim, 1, 1, 1)
    mean = torch.tensor(vae.config.latents_mean).view(shape).to(z.device, z.dtype)
    std = torch.tensor(vae.config.latents_std).view(shape).to(z.device, z.dtype)
    # the VAE is shared and stays resident: set tiling every time. 1024 px tiles 768 apart: 27 GB vs 65 at 4 MP
    if tiles(params["tiled"], latent.width, latent.height):
        vae.enable_tiling(1024, 1024, 768, 768)
    else:
        vae.disable_tiling()
    image = vae.decode(z * std + mean, return_dict=False)[0][:, :, 0]
    out = VaeImageProcessor(vae_scale_factor=SCALE, vae_latent_channels=vae.config.z_dim)
    pil = out.postprocess(image, output_type="pil")[0]  # RGBA: the VAE has 4 channels
    ctx.progress(1, 1, "decoding")
    return {"image": pil}
