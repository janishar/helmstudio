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

FP8 (API 1.33, the variants base-fp8 / turbo-fp8, NVIDIA only): unsloth's torchao files keep diffusers' names but
store a Linear as `X._weight_qdata` (e4m3) + `X._weight_scale` (fp32 per row): `rename` maps them to the core
Fp8Linear's `X.weight` / `X.scale`. The text encoder is a plain FP8 cast (no scales: ones; its FP8 biases are cast
to bf16 by the loader). `tensor_hook` refuses a file of another unsloth format.

Enhance (API 1.36, optional add-ons): the old studio's enhance.py (qwen-image-2.1-studio), Qwen's own prompt
enhancers (fine-tuned Qwen3.5-VL 9B, bf16), one per task: PE-T2I without references, PE-I2I with them, each with its
own system_prompt.txt and the references first; think: enable_thinking (off: the template's empty <think> block),
temperature 1.0 / top_p 0.95 / top_k 20, max_new_tokens 16256 / 24000, torch.manual_seed(seed); the answer the JSON
after the last </think>. Its wh_ratio (snapped to the old studio's seven ratios) or an edit's ratio_follow <imageN>
(that reference's aspect) sets an auto canvas, at the canvas's area (about 1 MP in an edit). From the official
prompt_rewrite (QwenLM/Qwen-Image-2.1, pe_core.py): each reference shrunk to at most 1 MP, as trained, and its
balanced-JSON answer scan. Not taken from it: its presence penalty (1.5 for T2I) and always-on thinking (the old
studio and the model cards run neither).

Not ported: CFG."""

import hashlib
import json
import logging
import math
import re
import time
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
# enhance (module docstring): the component and max_new_tokens, by whether there are references
PE = {False: ("text_enhancer", 16256), True: ("edit_enhancer", 24000)}
PE_IMAGE_PIXELS = (
    1024 * 1024
)  # a reference reaches the enhancer at most this many pixels (as trained)
PE_ANSWER = (
    2048  # think off: progress counts to this (an answer is ~700 tokens); on, to max_new_tokens
)
RATIOS = (1, 4 / 3, 3 / 4, 3 / 2, 2 / 3, 16 / 9, 9 / 16)  # W/H of the old studio's ASPECT_RATIOS
log = logging.getLogger("qwen")


# the unsloth files' __metadata__ unsloth_format each FP8 component is read as (a later format may lay keys out
# otherwise: refused, not guessed)
FP8_FORMATS = {
    "transformer": "unsloth_prequant_transformer_state_dict_v1",
    "text_encoder": "unsloth_prequant_text_encoder_state_dict_v1",
}


def rename(component: str, key: str) -> str:
    """torchao's FP8 Linear keys → the core Fp8Linear's; every other key as is."""
    for suffix, attr in (("._weight_qdata", ".weight"), ("._weight_scale", ".scale")):
        if key.endswith(suffix):
            return key[: -len(suffix)] + attr
    return key


def tensor_hook(component: str, model):
    """No tensor changes: checks, before the first tensor, that an FP8 component's files are of the format `rename`
    reads."""
    if model.precision_of(component) != "fp8":
        return
    from safetensors import safe_open

    for path in model.files(component):
        with safe_open(path, "pt") as f:
            got = (f.metadata() or {}).get("unsloth_format")
        if got != FP8_FORMATS[component]:
            raise RunError(f"{path.name}: unsloth_format {got!r}, not {FP8_FORMATS[component]}: "
                           "not a file this pack reads")  # fmt: skip


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
    if component.endswith("_enhancer"):  # its own folder (components.<c>.dir)
        from transformers import AutoModelForImageTextToText, GenerationConfig

        folder = model.folder(component)
        with accelerate.init_empty_weights():
            out = AutoModelForImageTextToText.from_config(
                AutoConfig.from_pretrained(folder), dtype=torch.bfloat16
            )
        out.generation_config = GenerationConfig.from_pretrained(folder)
        return out
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
        if dev != "cpu":
            getattr(
                torch, dev
            ).synchronize()  # so progress, ETA and cancel follow the GPU, not the queue
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


@lru_cache(maxsize=2)
def pe_processor(folder):
    return AutoProcessor.from_pretrained(folder)


def pe_image(img: Image.Image) -> Image.Image:
    """RGB, Lanczos-shrunk to at most PE_IMAGE_PIXELS at its aspect (pe_core.py load_image)."""
    img = img.convert("RGB")
    w, h = img.size
    if w * h > PE_IMAGE_PIXELS:
        s = (PE_IMAGE_PIXELS / (w * h)) ** 0.5
        img = img.resize((max(1, int(w * s)), max(1, int(h * s))), Image.LANCZOS)
    return img


def pe_answer(gen: str) -> tuple[str, str, str] | None:
    """(rewritten_prompt, wh_ratio, ratio_follow) of the last JSON object after the last </think> (the whole text
    with thinking off) that has a prompt (pe_core.py parse_answer, without json_repair), or None."""
    found, dec, answer = None, json.JSONDecoder(), gen.rpartition("</think>")[2]
    for i in (i for i, ch in enumerate(answer) if ch == "{"):
        try:
            obj, _ = dec.raw_decode(answer, i)
        except json.JSONDecodeError:
            continue
        text = (
            (obj.get("rewritten_prompt") or obj.get("rewrited_prompt"))
            if isinstance(obj, dict)
            else None
        )
        if isinstance(text, str) and text.strip():
            found = (
                text.strip(),
                str(obj.get("wh_ratio") or "").strip(),
                str(obj.get("ratio_follow") or "").strip(),
            )
    return found


def pe_canvas(cv: dict, refs: list, wh: str, follow: str) -> dict:
    """An auto canvas with the answer's aspect: an edit's ratio_follow <imageN> (that reference's), else wh_ratio W:H
    snapped to the nearest of RATIOS (enhance.py nearest_ratio); at about AREA with references, else at the canvas's
    area. Fixed, or with neither, as it came."""
    m, r = re.fullmatch(r"<image(\d+)>", follow), re.fullmatch(r"(\d+)\s*:\s*(\d+)", wh)
    if cv["mode"] != "auto":
        return dict(cv)
    if m and 1 <= int(m[1]) <= len(refs):
        ratio = refs[int(m[1]) - 1].width / refs[int(m[1]) - 1].height
    elif r and int(r[1]) and int(r[2]):
        ratio = min(RATIOS, key=lambda x: abs(math.log(int(r[1]) / int(r[2]) / x)))
    else:
        return dict(cv)
    width, height, _ = calculate_dimensions(AREA if refs else cv["width"] * cv["height"], ratio)
    return {
        "width": min(max(width, 256), 4096),
        "height": min(max(height, 256), 4096),
        "mode": "fixed",
    }


class _Tokens(StoppingCriteria):
    """A cancel point per generated token, progress every 16."""

    def __init__(self, ctx, start: int, total: int) -> None:
        self.ctx, self.start, self.total = ctx, start, total

    def __call__(self, input_ids, scores, **kwargs) -> bool:
        self.ctx.check()
        n = input_ids.shape[1] - self.start
        if n % 16 == 0:
            self.ctx.progress(min(n, self.total), self.total, "enhancing")
        return False


@torch.no_grad()
def enhance(ctx, params: dict, inputs: dict) -> dict:
    from core.pack import default_model

    prompt, cv, seed = inputs["prompt"], inputs["canvas"], inputs["seed"]
    if not prompt.strip():
        raise RunError(f"{ctx.node}: empty prompt")
    model = default_model(ctx.impl.pack)  # no Model input: the enhancers are models of their own
    refs = [r["image"] for r in params.get("refs") or []]
    name, max_new = PE[bool(refs)]
    # first: a missing add-on fails here with its title, not on a file read
    pe = ctx.component(model, name)
    folder = model.folder(name)
    proc = pe_processor(folder)
    system = (folder / "system_prompt.txt").read_text(encoding="utf-8").strip()
    content = [{"type": "image", "image": pe_image(img)} for img in refs]
    turns = [
        {"role": "system", "content": [{"type": "text", "text": system}]},
        {"role": "user", "content": [*content, {"type": "text", "text": prompt}]},
    ]
    batch = proc.apply_chat_template(turns, add_generation_prompt=True, tokenize=True, return_dict=True,
                                     return_tensors="pt", enable_thinking=params["think"])  # fmt: skip
    batch = batch.to(ctx.device)
    n_in = batch["input_ids"].shape[1]
    total = max_new if params["think"] else PE_ANSWER
    ctx.progress(1, total, "enhancing")
    t = time.monotonic()
    torch.manual_seed(seed)
    out = pe.generate(**batch, max_new_tokens=max_new, do_sample=True, temperature=1.0, top_p=0.95, top_k=20,
                      stopping_criteria=StoppingCriteriaList([_Tokens(ctx, n_in, total)]))  # fmt: skip
    gen = proc.tokenizer.decode(out[0, n_in:], skip_special_tokens=True)
    log.info("enhance %s: %d prompt tokens, %d new in %.0f s", name, n_in, out.shape[1] - n_in, time.monotonic() - t)  # fmt: skip
    del out, batch
    if (answer := pe_answer(gen)) is None:
        log.warning("enhancer output without an answer: %s", gen[-2000:])
        raise RunError(f"{ctx.node}: the enhancer gave no answer")
    text, wh, follow = answer
    ctx.progress(total, total, "enhancing")
    return {"text": text, "canvas": pe_canvas(cv, refs, wh, follow)}
