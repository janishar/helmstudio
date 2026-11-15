"""Qwen Image 2.1 stages on torch: the generation path of the qwen-image-2.1 studio (its generate.py, which calls
diffusers' QwenImage21Pipeline) split into encode, sample and decode, with every component loaded by the core
loader. Same settings, same pixels: the raw chat template, the pre-norm hidden states, the CPU noise generator,
Turbo's baked sigmas vs Base's steps + dynamic shift, the KV cache, no CFG, whole (untiled) decode up to 2.5 MP.

Multi-reference edit (encode `refs`, 1-10 PIL images from the core): each reference → RGBA, Lanczos-resized to
about 1 MP at its own aspect; the vision encoder reads it composited over white behind `<imageN>` placeholders in
the template, the VAE encodes all four channels whole into latent tokens that go before the noise. The output is
sized from the last reference (canvas mode auto) and the noise seed mixes in the references' pixels (the studio's
noise_seed), so re-editing a take with its own seed does not start from the noise that made it.

Ask (API 1.14, workflow qwen-image-ask): the text encoder is Qwen3-VL 8B with its generation head, so a judge,
write or decide node asks it after a still without loading anything (docs/reference-notes/llm.md 3). Greedy, seed
core.ask.SEED, the stock chat template (no thinking in this instruct model); no constrained decoding in this env:
the schema is in the prompt and core.ask checks the answer (one more try).

Not ported: the prompt enhancer, CFG."""

import hashlib
import json
from dataclasses import dataclass, field
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
    calculate_dimensions,
    calculate_shift,
    retrieve_latents,
    retrieve_timesteps,
)
from diffusers.utils.torch_utils import randn_tensor
from PIL import Image
from transformers import (
    AutoConfig,
    AutoProcessor,
    Qwen3VLForConditionalGeneration,
    StoppingCriteria,
    StoppingCriteriaList,
)

from core import ask as asks
from core.graph import RunError

SYSTEM = "Comprehend and analyze the provided prompt."
TEMPLATE = f"<|im_start|>system\n{SYSTEM}<|im_end|>\n<|im_start|>user\n{{}}<|im_end|>\n<|im_start|>assistant\n"
SLOT = (
    "<image{}><|vision_start|><|image_pad|><|vision_end|>"  # one reference's place in the user turn
)
AREA = (
    1024 * 1024
)  # references, and an auto-sized edit, are about this many pixels (output_resolution²)
SCALE = 16  # the VAE compresses 16x; sizes are floored to multiples of 32
# tiled: auto tiles above this many MP (the studio tiled only when asked; the goldens are <= 1 MP)
AUTO_TILE_MP = 2.5


@dataclass
class Cond:
    embeds: torch.Tensor  # [1, L, 4096] bf16 on the device; one unpadded prompt, so no mask
    pads: (
        torch.Tensor
    )  # [1, L] bool: which positions hold a reference's vision tokens (none in text-to-image)
    latents: torch.Tensor | None = None  # [1, sum(h*w), 64]: the references' VAE latents, in order
    shapes: list = field(default_factory=list)  # (1, h, w) of each reference's latents
    refs: list = field(
        default_factory=list
    )  # the references as given (PIL), for auto size and the noise seed


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


def prompt_text(text: str, n: int) -> str:
    """The raw template the checkpoint expects: n reference slots (`<image1>…`, space-separated) then the text."""
    slots = " ".join(SLOT.format(i) for i in range(1, n + 1))
    return TEMPLATE.format(slots + text)


def noise_seed(seed: int, refs: list) -> int:
    """The studio's noise_seed: the seed itself, or for an edit the seed mixed with each reference's mode, size
    and pixels."""
    if not refs:
        return seed
    digest = hashlib.sha256(str(seed).encode())
    for img in refs:
        digest.update(f"{img.mode}{img.size}".encode())
        digest.update(img.tobytes())
    return int.from_bytes(digest.digest()[:8], "big") >> 1


def reference(img: Image.Image, pre: VaeImageProcessor) -> tuple[Image.Image, torch.Tensor]:
    """(what the vision encoder reads: RGB over white, VAE input [1, 4, 1, H, W] in [-1, 1]), both one Lanczos
    resize of the RGBA image to about AREA pixels at its own aspect."""
    img = img if img.mode == "RGBA" else img.convert("RGBA")
    w, h, _ = calculate_dimensions(AREA, img.width / img.height)
    small = pre.resize(img, width=w, height=h)
    seen = Image.new("RGB", small.size, (255, 255, 255))
    seen.paste(small, mask=small.getchannel("A"))
    return seen, pre.preprocess(img, width=w, height=h).unsqueeze(2)


def output_size(canvas: dict, refs: list) -> tuple[int, int]:
    """(width, height) floored to 32: canvas auto with references → about 1 MP at the last one's aspect."""
    if canvas.get("mode") == "auto" and refs:
        width, height, _ = calculate_dimensions(AREA, refs[-1].width / refs[-1].height)
    else:
        width, height = canvas["width"], canvas["height"]
    return width // 32 * 32, height // 32 * 32


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
    model, refs = inputs["model"], [r["image"] for r in params["refs"]]
    te = ctx.component(model, "text_encoder")
    proc, drop = processor(model.folder("text_encoder"))
    ctx.check()
    vae, extra, pixels = None, {}, ()
    if refs:
        vae = ctx.component(model, "vae")
        pre = VaeImageProcessor(vae_scale_factor=SCALE, vae_latent_channels=vae.config.z_dim)
        seen, pixels = zip(*(reference(img, pre) for img in refs), strict=True)
        extra = {"images": list(seen)}
    batch = proc(
        text=[prompt_text(text, len(refs))],
        padding=True,
        padding_side="left",
        return_tensors="pt",
        **extra,
    ).to(ctx.device)
    kw = {"input_ids": batch.input_ids, "attention_mask": batch.attention_mask}
    if refs:
        kw |= {"pixel_values": batch.pixel_values, "image_grid_thw": batch.image_grid_thw}
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
    keep = batch.attention_mask[0].bool()
    embeds = hidden[0][keep][drop:]
    pad_id = proc.tokenizer.encode("<|image_pad|>")[0]
    pads = (batch.input_ids[0][keep] == pad_id)[drop:]
    cond = Cond(embeds[None].clone(), pads[None].clone(), refs=refs)
    ctx.progress(1, len(refs) + 1, "encoding")  # the prompt, then each reference
    if refs:  # whole (untiled) encodes, one reference at a time, in the transformer's dtype
        vae.disable_tiling()
        shape = (1, vae.config.z_dim, 1, 1, 1)
        latents = []
        for i, px in enumerate(pixels, 2):
            ctx.check()
            z = retrieve_latents(vae.encode(px.to(ctx.device, embeds.dtype)), sample_mode="argmax")
            mean = torch.tensor(vae.config.latents_mean).view(shape).to(z.device, z.dtype)
            std = torch.tensor(vae.config.latents_std).view(shape).to(z.device, z.dtype)
            z = (z - mean) / std  # [1, C, 1, h, w]
            cond.shapes.append((1, z.shape[3], z.shape[4]))
            latents.append(z.view(1, z.shape[1], -1).transpose(1, 2))
            ctx.progress(i, len(refs) + 1, "encoding")
        cond.latents = torch.cat(latents, dim=1).clone()
    return {"cond": cond}


@torch.no_grad()
def sample(ctx, params: dict, inputs: dict) -> dict:
    model, cond, canvas = inputs["model"], inputs["cond"], inputs["canvas"]
    dit = ctx.component(model, "transformer")
    width, height = output_size(canvas, cond.refs)
    h, w = height // SCALE, width // SCALE
    embeds, dev = cond.embeds, ctx.device

    # a seed repeats across devices; an edit mixes in its references
    generator = torch.Generator("cpu").manual_seed(noise_seed(inputs["seed"], cond.refs))
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

    img_shapes = [[*cond.shapes, (1, h, w)]]
    # the mask spans the text (true at the references' vision tokens) then one slot per 2x2 target latents
    img_mask = torch.cat([cond.pads, cond.pads.new_ones(1, latents.shape[1] // 4)], dim=1)
    # text keys/values are step-independent under causal_condition: step 0 fills the cache, later steps reuse it
    cache = QwenImage21KVCache(len(dit.transformer_blocks)) if dit.config.causal_condition else None
    n = len(timesteps)
    for i, t in enumerate(timesteps):
        ctx.check()
        mode = None if cache is None else ("extract" if i == 0 else "cached")
        with dit.cache_context("cond"):
            pred = dit(
                hidden_states=latents
                if cond.latents is None
                else torch.cat([cond.latents, latents], 1),
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


class _Check(StoppingCriteria):
    """A cancel point per generated token."""

    def __init__(self, ctx) -> None:
        self.ctx = ctx

    def __call__(self, input_ids, scores, **kwargs) -> bool:
        self.ctx.check()
        return False


@torch.no_grad()
def ask(ctx, params: dict, inputs: dict) -> dict:
    model = inputs["model"]
    te = ctx.component(model, "text_encoder")
    proc, _ = processor(model.folder("text_encoder"))

    def generate(prompt: str, thumbs: list) -> tuple[str, int, int]:
        turns = (
            [{"role": "system", "content": [{"type": "text", "text": params["system"]}]}]
            if params["system"]
            else []
        )
        content = [{"type": "image"} for _ in thumbs] + [{"type": "text", "text": prompt}]
        turns.append({"role": "user", "content": content})
        text = proc.apply_chat_template(turns, add_generation_prompt=True, tokenize=False)
        batch = proc(text=[text], images=thumbs or None, return_tensors="pt").to(ctx.device)
        torch.manual_seed(asks.SEED)
        out = te.generate(**batch, do_sample=False, max_new_tokens=params["max_tokens"],
                          stopping_criteria=StoppingCriteriaList([_Check(ctx)]))  # fmt: skip
        new = out[0][batch.input_ids.shape[1] :]
        return proc.decode(new, skip_special_tokens=True), batch.input_ids.shape[1], len(new)

    return asks.ask(ctx, params, generate)
