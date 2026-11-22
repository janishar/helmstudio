"""MiniMax H3 Image stages on torch: the generation path of the MiniMax-H3-Image studio (its generate.py and
enhance.py, which run diffusers' modular MiniMaxH3ImageBlocks) split into canvas, enhance, encode, sample and decode,
with every component loaded by the core loader (docs/reference-notes/minimax-h3-image.md).

Same settings, same pixels: the 51-layer Qwen3-VL text encoder read at hidden_states[50] (no chat template), the
fixed 6-step distilled sampler with the sigmas baked in modular_model_index.json (Euler, or LCM re-noising from the
device RNG), a CPU generator seeded with the studio's noise_seed that first samples each reference's VAE posterior
and then the noise (so the reference VAE encode runs inside sample), a 4 MP canvas floor with the resize after
decode, the float32 VAE always tiled with the two MPS patches. Refs are RGB; the first one sets an auto canvas.

The enhancer (MiniMax-H3-Image-PE, Qwen3.5 27B) ships only as NVFP4: `tensor_hook` dequantizes each NVFP4 Linear on
the device as it streams in, then the core loader quantizes it to int8. Thinking is always on (think: effort xhigh,
else low); the answer is the JSON after the last </think>.

The variant base-fp8 (API 1.33, NVIDIA only: docs/plan/17-nvidia-quantized.md) loads community pre-quantized files:
- the DiT is unsloth's FP8 build: torchao per-row FP8 Linears (`X._weight_qdata` + `X._weight_scale`, renamed to the
  core Fp8Linear's `X.weight` / `X.scale`), with context_embedder and token_refiner dequantized to bf16 at load (its
  card: FP8 there costs quality), and the adaLN in curve form: the time MLP is a table [1025, 8] read at t (diffusers'
  timestep, 1 - sigma in [0, 1], the t ComfyUI's curve form reads), linearly between the two nearest rows, and every
  adaLN projection (8 inputs) takes it with no SiLU (`curve_form`);
- the text encoder is Comfy-Org's int8 ConvRot build: per-row int8 Linears whose input axis was rotated per 256 by a
  block Hadamard matrix (core Int8Linear `rot_group`), bare Qwen3-VL names, 50 text layers, no final norm and no head:
  its last hidden state is the official stack's hidden_states[50]."""

import hashlib
import json
import logging
import math
import re
import time
import types
from dataclasses import dataclass
from functools import lru_cache

import accelerate
import torch
from PIL import Image
from transformers import StoppingCriteria, StoppingCriteriaList

from core import pack as packs
from core.graph import RunError
from core.pack import default_model

log = logging.getLogger("minimax")

STEPS = 6  # distilled to six steps, one output head each
TEXT_LAYERS = (
    51  # hidden_states[50] conditions the transformer; a 51-layer stack leaves it un-normed
)
MAX_RATIO = 4.0  # widest ratio rendered (generate.py MAX_RATIO)
MAX_MP = 20  # the old CLI's --mp cap, in MiB of pixels
PE_IMAGE_PIXELS = 1 << 20  # an edit's references reach the enhancer at about 1 MP (enhance.py)
PE_TOKENS = 4096  # thinking included, as released
_LAYER = re.compile(r"\.layers\.(\d+)\.")
# base-fp8 (module docstring)
FP8_FORMAT = "unsloth_prequant_transformer_state_dict_v1"  # its DiT's __metadata__ unsloth_format: another is refused
CURVE = (1025, 8)  # the curve form's table: grid rows x the adaLN projections' inputs
DENSE = ("context_embedder.", "token_refiner.")  # FP8 in the file, bf16 at load
CONVROT_GROUP = 256
CONVROT_LAYERS = 50  # the Comfy text encoder's layers
CONVROT_TE = {
    "num_hidden_layers": 50,
    "output": "unnormalized_hidden_after_layer_50",
}  # its __metadata__
COMFY_TE = (("model.layers.", "model.language_model.layers."),
            ("model.embed_tokens.", "model.language_model.embed_tokens."), ("visual.", "model.visual."))  # fmt: skip

# E2M1 values by 4-bit code (bit 3 is the sign), and by packed byte: low nibble is the first element.
FP4 = torch.tensor([0, 0.5, 1, 1.5, 2, 3, 4, 6, -0.0, -0.5, -1, -1.5, -2, -3, -4, -6])
FP4_PAIRS = torch.stack((FP4.repeat(16), FP4.repeat_interleave(16)), dim=1)  # [byte] -> (low, high)


@dataclass
class Cond:
    state: object  # the encoder part's PipelineState (prompt_embeds, text_token_tags, the resized references)
    refs: list  # the references, RGB, for the auto size and the noise seed


@dataclass
class Latent:
    latents: torch.Tensor  # [1, 24, h, w] float32 on the device (after_denoise)
    width: int  # the output size: the canvas, resized to after decode when it was under 4 MP
    height: int


# --- components


def community(model) -> bool:
    """Whether the model is the community build (base-fp8): its transformer loads at fp8."""
    return model.precision_of("transformer") == "fp8"


def rename(component: str, key: str) -> str | None:
    """The text encoder keeps 51 layers and no LM head; the enhancer's NVFP4 scales are read by tensor_hook. The
    community files (their keys never occur in the official ones): the Comfy text encoder's bare Qwen3-VL names, its
    int8 weights into Int8Linear.qweight and its scales into .scale (its comfy_quant tags are read by tensor_hook);
    unsloth's FP8 suffixes into Fp8Linear's (the dense modules' scales are read by tensor_hook)."""
    if component == "text_encoder":
        if key.endswith(".comfy_quant"):
            return None
        for old, new in COMFY_TE:
            if key.startswith(old):
                key = new + key.removeprefix(old)
                if old == "model.layers." and key.endswith("_proj.weight"):
                    key = key.removesuffix("weight") + "qweight"
                elif key.endswith(".weight_scale"):
                    key = key.removesuffix("weight_scale") + "scale"
    if component == "transformer":
        if key.startswith(DENSE) and key.endswith("._weight_scale"):
            return None
        for suffix, attr in (("._weight_qdata", ".weight"), ("._weight_scale", ".scale")):
            if key.endswith(suffix):
                return key.removesuffix(suffix) + attr
    if component == "text_encoder" and (
        key.startswith("lm_head.")
        or ((m := _LAYER.search(key)) and "language_model" in key and int(m[1]) >= TEXT_LAYERS)
    ):
        return None
    if component == "enhancer" and key.endswith(("input_scale", "weight_scale", "weight_scale_2")):
        return None
    return key


def patch_single_frame_conv() -> None:
    """Run the VAE's causal 3-D convolutions on a single frame as the 2-D convolutions they are (generate.py): on MPS,
    conv3d keeps driver memory it never frees (a 2048 px reference took 85 GB)."""
    from diffusers.models.autoencoders.autoencoder_kl_minimax_h3_image import (
        MiniMaxH3ImageCausalConv3d as Conv,
    )

    conv3d = Conv.forward
    if getattr(conv3d, "single_frame", False):
        return

    def forward(self, hidden_states: torch.Tensor) -> torch.Tensor:
        if hidden_states.shape[2] != 1 or self.temporal_padding != self.kernel_size[0] - 1:
            return conv3d(self, hidden_states)
        hidden_states = (
            hidden_states.contiguous()
        )  # a tile is a view; MPS keeps buffers per view offset
        if self.spatial_padding > 0:
            p = self.spatial_padding
            hidden_states = torch.nn.functional.pad(
                hidden_states, (p, p, p, p, 0, 0), mode=self.spatial_padding_mode
            )
        out = torch.nn.functional.conv2d(hidden_states[:, :, 0], self.weight[:, :, -1], self.bias,
                                         stride=self.stride[1:], dilation=self.dilation[1:])  # fmt: skip
        return out[:, :, None]

    forward.single_frame = True
    Conv.forward = forward


def build(component: str, model):
    """The component on the meta device (buffers made in __init__ stay real, on the CPU; the loader moves them)."""
    from transformers import AutoConfig

    folder = model.folder(component)
    if component == "enhancer":
        from transformers import AutoModelForImageTextToText, GenerationConfig

        config = AutoConfig.from_pretrained(folder)
        del config.quantization_config  # or transformers sets up the (CUDA-only) modelopt quantizer
        with accelerate.init_empty_weights():
            out = AutoModelForImageTextToText.from_config(config, dtype=torch.bfloat16)
        out.generation_config = GenerationConfig.from_pretrained(folder)
        return out
    if component == "text_encoder":
        from transformers import Qwen3VLForConditionalGeneration

        config = AutoConfig.from_pretrained(folder / "text_encoder")
        text = config.text_config
        text.num_hidden_layers = TEXT_LAYERS  # the 13 layers after hidden_states[50] are never read
        if getattr(text, "layer_types", None):
            text.layer_types = text.layer_types[:TEXT_LAYERS]
        with accelerate.init_empty_weights(include_buffers=False):
            out = Qwen3VLForConditionalGeneration(config)
        out.lm_head = torch.nn.Identity()  # the pipeline reads text_encoder.model's hidden states
        if community(model):
            # 50 layers and no norm: the last hidden state (hidden_states[50], tied to the norm's output) is then the
            # 51-layer stack's hidden_states[50]; the config keeps 51 for the pipeline's "more than 50" check
            text_model = out.model.language_model
            text_model.layers = text_model.layers[:CONVROT_LAYERS]
            text_model.norm = torch.nn.Identity()
        return out
    from diffusers import AutoencoderKLMiniMaxH3Image, MiniMaxH3ImageTransformer2DModel

    if component == "transformer":
        cls, sub = MiniMaxH3ImageTransformer2DModel, folder / "transformer_diffusers"
    else:
        patch_single_frame_conv()
        cls, sub = AutoencoderKLMiniMaxH3Image, folder / "vae_diffusers"
    with accelerate.init_empty_weights(include_buffers=False):
        out = cls.from_config(cls.load_config(sub))
    if component == "transformer" and community(model):
        curve_form(out)
    if component == "vae" and torch.backends.mps.is_available():
        # Python queues every tile before the GPU runs the first, and MPS keeps each queued tile's buffers (121 tiles
        # of a 2048 px reference held 38 GB): wait after each
        for part in (out.encoder, out.decoder):
            part.register_forward_hook(lambda *_: torch.mps.synchronize())
    return out


class CurveTable(torch.nn.Module):
    """The curve form's time embedding: the table row at t in [0, 1] (row 0 at t = 0, the last at t = 1), linear
    between the two rows either side."""

    def __init__(self) -> None:
        super().__init__()
        self.register_buffer("table", torch.empty(*CURVE, device="meta"))

    def forward(self, t: torch.Tensor) -> torch.Tensor:
        last = self.table.shape[0] - 1
        pos = t.float().clamp(0, 1) * last
        lo = pos.floor().long().clamp(max=last - 1)  # t = 1 reads the last interval's end
        return torch.lerp(self.table[lo], self.table[lo + 1], (pos - lo)[:, None])


class Dense(torch.nn.Linear):
    """A Linear the loader keeps dense: it swaps only exact nn.Linear (tensor_hook dequantizes its FP8 weight)."""


def _modulation(self, temb: torch.Tensor) -> tuple[torch.Tensor, ...]:
    """MiniMaxH3ImageAdaLayerNormModulation.forward in curve form: no SiLU, float32, the bf16 stack's dtype out."""
    temb = self.linear(temb.float()).to(torch.bfloat16).view(-1, 6 * self.hidden_size)
    return temb.chunk(6, dim=-1)


def _norm_out(self, hidden_states, temb, timestep_indices) -> torch.Tensor:
    """MiniMaxH3ImageAdaLayerNormOut.forward in curve form (as _modulation)."""
    shift, scale = self.linear(temb.float()).to(torch.bfloat16).chunk(2, dim=-1)
    hidden_states = self.norm(hidden_states)
    return hidden_states * (1.0 + scale.index_select(0, timestep_indices)) + shift.index_select(
        0, timestep_indices
    )


def curve_form(dit) -> None:
    """The meta DiT → unsloth's layout: the curve table for time_proj + time_embedder, 8-input float32 adaLN
    projections without the SiLU, context_embedder and token_refiner's Linears kept dense."""
    dit.time_proj, dit.time_embedder = torch.nn.Identity(), CurveTable()
    for m in [b.adaln_proj for b in dit.transformer_blocks] + [dit.norm_out]:
        m.linear = torch.nn.Linear(CURVE[1], m.linear.out_features, device="meta")
        m.forward = types.MethodType(_modulation if m is not dit.norm_out else _norm_out, m)
    for name, m in dit.named_modules():
        if f"{name}.".startswith(DENSE) and type(m) is torch.nn.Linear:
            m.__class__ = Dense


def nvfp4_dequant(packed: torch.Tensor, scale: torch.Tensor, scale_2: torch.Tensor) -> torch.Tensor:
    """ModelOpt NVFP4 -> bf16: uint8 [out, in/2] codes, float8_e4m3fn [out, in/16] block scales, fp32 global scale."""
    out = packed.shape[0]
    # code * block scale is exact in bf16 (2 + 4 significant bits), so only the global scale rounds
    w = FP4_PAIRS.to(packed.device, torch.bfloat16)[packed.int()].view(out, -1, 16)
    w = w * scale.to(torch.bfloat16)[..., None]
    return (w.float() * scale_2.float()).to(torch.bfloat16).view(out, -1)


def community_hook(component: str, model):
    """base-fp8: refuses files of another layout before the first tensor; then dequantizes the DiT's dense modules'
    FP8 weights to bf16 (their scales read here)."""
    from safetensors import safe_open

    files = model.files(component)
    handles = [safe_open(p, "pt", device="cpu") for p in files]
    where = {k: f for p, f in zip(files, handles) for k in packs.header(p)}
    for path, f in zip(files, handles):
        meta = f.metadata() or {}
        if component == "transformer" and meta.get("unsloth_format") != FP8_FORMAT:
            raise RunError(f"{path.name}: unsloth_format {meta.get('unsloth_format')!r}, not {FP8_FORMAT}: "
                           "not a file this pack reads")  # fmt: skip
        if component == "text_encoder":
            tags = {
                bytes(f.get_tensor(k).tolist()).decode()
                for k in packs.header(path)
                if k.endswith(".comfy_quant")
            }
            want = {
                "format": "int8_tensorwise",
                "convrot": True,
                "convrot_groupsize": CONVROT_GROUP,
            }
            if json.loads(meta.get("minimax_h3_te", "{}")) != CONVROT_TE or any(
                json.loads(t) != want for t in tags
            ):
                raise RunError(
                    f"{path.name}: not the 50-layer int8 ConvRot text encoder this pack reads"
                )
    if component == "text_encoder":
        return None

    def hook(name: str, tensor: torch.Tensor) -> torch.Tensor:
        if not (name.startswith(DENSE) and tensor.dtype == torch.float8_e4m3fn):
            return tensor
        key = name.removesuffix("weight") + "_weight_scale"
        scale = where[key].get_tensor(key)
        return (tensor.float() * scale.to(tensor.device)).to(torch.bfloat16)

    return hook


def rotated(component: str, model):
    """The Comfy text encoder's quantized Linears are ConvRot, group 256 (core Int8Linear `rot_group`)."""
    group = CONVROT_GROUP if component == "text_encoder" and community(model) else None
    return lambda name: group


def tensor_hook(component: str, model):
    """The enhancer's NVFP4 weights → bf16 on the device as they arrive (the core loader then quantizes them to
    int8); their scales are read from the CPU here (MPS has no float8: e4m3 → bf16, exactly, before moving). The
    community files: community_hook."""
    if component in ("transformer", "text_encoder") and community(model):
        return community_hook(component, model)
    if component != "enhancer":
        return None
    from safetensors import safe_open

    folder = model.folder(component)
    weight_map = json.loads((folder / "model.safetensors.index.json").read_text())["weight_map"]
    files: dict = {}

    def get(key: str, device) -> torch.Tensor:
        file = weight_map[key]
        if file not in files:
            files[file] = safe_open(folder / file, "pt", device="cpu")
        t = files[file].get_tensor(key)
        return (t.to(torch.bfloat16) if t.dtype == torch.float8_e4m3fn else t).to(device)

    def hook(name: str, tensor: torch.Tensor) -> torch.Tensor:
        module, _, attr = name.rpartition(".")
        if attr != "weight" or f"{module}.weight_scale" not in weight_map:
            return tensor
        return nvfp4_dequant(tensor, get(f"{module}.weight_scale", tensor.device), get(f"{module}.weight_scale_2", tensor.device))  # fmt: skip

    return hook


@lru_cache(maxsize=2)
def text_tools(folder):
    from transformers import Qwen2TokenizerFast, Qwen3VLProcessor

    return Qwen2TokenizerFast.from_pretrained(folder / "tokenizer"), Qwen3VLProcessor.from_pretrained(folder / "processor")  # fmt: skip


@lru_cache(maxsize=2)
def pe_processor(folder):
    from transformers import AutoProcessor

    return AutoProcessor.from_pretrained(folder)


def part(model, *names: str):
    """The modular pipeline of these blocks (generate.py `part`), configured from the model folder."""
    from diffusers.modular_pipelines import (
        MiniMaxH3ImageBlocks,
        MiniMaxH3ImageModularPipeline,
        SequentialPipelineBlocks,
    )

    blocks = MiniMaxH3ImageBlocks()
    sub = SequentialPipelineBlocks.from_blocks_dict({n: blocks.sub_blocks[n] for n in names})
    return MiniMaxH3ImageModularPipeline(
        blocks=sub, pretrained_model_name_or_path=str(model.folder())
    )


def scheduler(model, sampler: str):
    """The checkpoint's six-step scheduler: Euler as released, or LCM (re-noise the predicted image every step)."""
    from diffusers import MiniMaxH3Scheduler

    kw = {"stochastic_sampling": True, "euler_update_form": "blend"} if sampler == "lcm" else {}
    return MiniMaxH3Scheduler.from_pretrained(model.folder() / "scheduler", **kw)


# --- sizes and seeds (generate.py)


def size_for(ratio: float, mp: float) -> tuple[int, int]:
    """(width, height) at `mp` MiB of pixels, multiples of 32, worked out on the long side (3:4 is 4:3 turned)."""
    if ratio < 1:
        return size_for(1 / ratio, mp)[::-1]
    width = round(math.sqrt(mp * 1024 * 1024 * ratio) / 32) * 32
    return width, round(width / ratio / 32) * 32


def clamp(ratio: float) -> float:
    return min(max(ratio, 1 / MAX_RATIO), MAX_RATIO)


def output_size(canvas: dict, refs: list) -> tuple[int, int]:
    """Canvas auto with references: the first one's aspect (clamped) at the canvas's area; else width × height."""
    if canvas.get("mode") == "auto" and refs:
        return size_for(
            clamp(refs[0].width / refs[0].height), canvas["width"] * canvas["height"] / 2**20
        )
    return canvas["width"], canvas["height"]


def too_big(width: int, height: int) -> str | None:
    return f"at most {MAX_MP} MP ({width} × {height})" if width * height > MAX_MP * 2**20 else None


def noise_seed(seed: int, images: list) -> int:
    """The seed itself, or for an edit the seed mixed with the (RGB) references' pixels."""
    if not images:
        return seed
    digest = hashlib.sha256(str(seed).encode())
    for img in images:
        digest.update(f"{img.mode}{img.size}".encode())
        digest.update(img.tobytes())
    return int.from_bytes(digest.digest()[:8], "big") >> 1


def sync(device: str) -> None:
    if device in ("mps", "cuda"):
        getattr(torch, device).synchronize()


# --- stages


def check_canvas(params: dict, wired: set, models: dict) -> str | None:
    """Before anything loads: auto keeps the area, so width × height bounds the output."""
    why = too_big(params["width"], params["height"])
    return f": {why}" if why else None


def canvas(ctx, params: dict, inputs: dict) -> dict:
    return {
        "canvas": {"width": params["width"], "height": params["height"], "mode": params["mode"]}
    }


def reduced_ratio(w: int, h: int) -> str:
    g = math.gcd(w, h)
    return f"{w // g}:{h // g}"


def parse_answer(gen: str, ratio_key: str) -> tuple[str, str | None] | None:
    """The (caption, W:H or None) in the JSON after the last </think>; None when there is none (enhance.py)."""
    _, sep, answer = gen.rpartition("</think>")
    answer = re.sub(r"^```(?:json)?\s*|\s*```$", "", answer.strip())
    try:
        result = json.loads(answer) if sep else None
        caption = result["expanded_caption"].strip()
    except json.JSONDecodeError, TypeError, KeyError, AttributeError:
        return None
    ratio = result.get(ratio_key)
    m = re.fullmatch(r"\s*(\d+)\s*:\s*(\d+)\s*", ratio) if isinstance(ratio, str) else None
    return caption, (reduced_ratio(int(m[1]), int(m[2])) if m and int(m[1]) and int(m[2]) else None)


def shrink(img: Image.Image) -> Image.Image:
    scale = math.sqrt(PE_IMAGE_PIXELS / (img.width * img.height))
    return (
        img
        if scale >= 1
        else img.resize((round(img.width * scale), round(img.height * scale)), Image.LANCZOS)
    )


def edit_content(
    task_prompt: str, prompt: str, images: list, ratio: str | None = None
) -> list[dict]:
    """The one user message of an edit's enhancement (enhance.py `edit_content`); `ratio`: W:H the user fixed."""
    n = len(images)
    content = [
        {"type": "text", "text": task_prompt},
        {"type": "text", "text": f"Reference Image Count:\n{n} input image(s), labeled <Picture 1> through <Picture {n}>."},
    ]  # fmt: skip
    for i, img in enumerate(images, start=1):
        content += [
            {"type": "text", "text": f"Reference Media:\n- <Picture {i}> [aspect_ratio {reduced_ratio(*img.size)}]"},
            {"type": "image", "image": shrink(img)},
        ]  # fmt: skip
    content.append({"type": "text", "text": (
        "\nUser Editing Instruction (JSON string):\n"
        f"{json.dumps(prompt, ensure_ascii=False)}\n\n"
        f"User Aspect Ratio Selection:\n{ratio or 'adaptive'}\n\n"
        "Apply the instructions above to the supplied inputs and reference images. "
        "Output only the required two-field JSON object."
    )})  # fmt: skip
    return content


def messages(folder, prompt: str, images: list, ratio: str | None) -> tuple[list, str]:
    """(chat messages, the answer's ratio key): text-to-image (system + task prompt) or an edit (one user turn)."""
    prompts = folder / "prompt"
    if images:
        task = (prompts / "edit" / "task_prompt.txt").read_text(encoding="utf-8")
        return [
            {"role": "user", "content": edit_content(task, prompt, images, ratio)}
        ], "target_aspect_ratio"
    system = (prompts / "t2i" / "system_prompt.txt").read_text(encoding="utf-8").strip()
    task = (prompts / "t2i" / "task_prompt.txt").read_text(encoding="utf-8")
    return [
        {"role": "system", "content": system},
        {"role": "user", "content": task.format(prompt)},
    ], "aspect_ratio"


class _Tokens(StoppingCriteria):
    """A cancel point per generated token, progress every 16."""

    def __init__(self, ctx, start: int) -> None:
        self.ctx, self.start = ctx, start

    def __call__(self, input_ids, scores, **kwargs) -> bool:
        self.ctx.check()
        n = input_ids.shape[1] - self.start
        if n % 16 == 0:
            self.ctx.progress(min(n, PE_TOKENS), PE_TOKENS, "enhancing")
        return False


@torch.no_grad()
def enhance(ctx, params: dict, inputs: dict) -> dict:
    prompt, cv, seed = inputs["prompt"], inputs["canvas"], inputs["seed"]
    if not prompt.strip():
        raise RunError(f"{ctx.node}: empty prompt")
    model = default_model(ctx.impl.pack)  # no Model input: the enhancer is a model of its own
    # main() opens them as RGB, enhance() again; the core applied a JPEG's EXIF orientation (contracts/README.md),
    # the same pixels encode and noise_seed see
    images = [r["image"].convert("RGB") for r in params.get("refs") or []]
    # an edit's enhancer writes its caption for a canvas the user fixed (its reduced W:H), else "adaptive"
    fixed = reduced_ratio(cv["width"], cv["height"]) if images and cv["mode"] == "fixed" else None
    # first: a missing add-on fails here with its title, not on a file read
    pe = ctx.component(model, "enhancer")
    folder = model.folder("enhancer")
    proc = pe_processor(folder)
    turns, ratio_key = messages(folder, prompt, images, fixed)
    batch = proc.apply_chat_template(turns, add_generation_prompt=True, tokenize=True, return_dict=True,
                                     return_tensors="pt", enable_thinking=True,
                                     reasoning_effort="xhigh" if params["think"] else "low")  # fmt: skip
    batch = batch.to(ctx.device)
    n_in = batch["input_ids"].shape[1]
    ctx.progress(1, PE_TOKENS, "enhancing")
    t = time.monotonic()
    torch.manual_seed(seed)
    out = pe.generate(**batch, max_new_tokens=PE_TOKENS, do_sample=True, temperature=1.0, top_p=0.95, top_k=20,
                      stopping_criteria=StoppingCriteriaList([_Tokens(ctx, n_in)]))  # fmt: skip
    gen = proc.tokenizer.decode(out[0, n_in:], skip_special_tokens=True)
    log.info(
        "enhance: %d prompt tokens, %d new in %.0f s",
        n_in,
        out.shape[1] - n_in,
        time.monotonic() - t,
    )
    del out, batch
    if (answer := parse_answer(gen, ratio_key)) is None:
        log.warning("enhancer output without an answer: %s", gen[-2000:])
        raise RunError(f"{ctx.node}: the enhancer gave no answer")
    caption, ratio = answer
    ctx.progress(PE_TOKENS, PE_TOKENS, "enhancing")
    if cv["mode"] == "auto" and ratio:  # its aspect replaces the canvas's, at the canvas's area
        w, h = (int(x) for x in ratio.split(":"))
        width, height = size_for(clamp(w / h), cv["width"] * cv["height"] / 2**20)
        cv = {"width": width, "height": height, "mode": "fixed"}
    return {"text": caption, "canvas": dict(cv)}


@torch.no_grad()
def encode(ctx, params: dict, inputs: dict) -> dict:
    text, model = inputs["prompt"], inputs["model"]
    if not text.strip():
        raise RunError(f"{ctx.node}: empty prompt")
    refs = [
        r["image"].convert("RGB") for r in params["refs"]
    ]  # generate.py: alpha dropped, no compositing
    te = ctx.component(model, "text_encoder")
    tokenizer, processor = text_tools(model.folder())
    encoder = part(model, "before_encode", "text_encoder")
    encoder.update_components(text_encoder=te, tokenizer=tokenizer, processor=processor)
    ctx.check()
    try:
        state = encoder(prompt=text, image=refs or None)
    except ValueError as e:  # a reference under 32 px
        raise RunError(f"{ctx.node}: {e}") from e
    ctx.progress(1, 1, "encoding")
    return {"cond": Cond(state, refs)}


@torch.no_grad()
def sample(ctx, params: dict, inputs: dict) -> dict:
    model, cond = inputs["model"], inputs["cond"]
    width, height = output_size(inputs["canvas"], cond.refs)
    if why := too_big(width, height):
        raise RunError(f"{ctx.node}: {why}")
    dit = ctx.component(model, "transformer")
    vae = ctx.component(model, "vae")
    render = part(model, "vae_encoder", "denoise")
    render.update_components(
        transformer=dit, vae=vae, scheduler=scheduler(model, params["sampler"])
    )
    seed = noise_seed(inputs["seed"], cond.refs)
    torch.manual_seed(seed)  # LCM's per-step noise comes from the global (device) RNG
    step = [0]

    def before(*_) -> None:
        ctx.check()

    def after(*_) -> None:
        sync(ctx.device)  # so progress, ETA and cancel follow the GPU, not the queue
        step[0] += 1
        ctx.progress(step[0], STEPS, "sampling")

    hooks = [dit.register_forward_pre_hook(before), dit.register_forward_hook(after)]
    try:
        state = render(state=cond.state, height=height, width=width,
                       generator=torch.Generator().manual_seed(seed))  # fmt: skip
    except ValueError as e:
        raise RunError(f"{ctx.node}: {e}") from e
    finally:
        for h in hooks:
            h.remove()
    latents = state.get("latents")
    return {
        "latent": Latent(latents.clone(), state.get("output_width"), state.get("output_height"))
    }


@torch.no_grad()
def decode(ctx, params: dict, inputs: dict) -> dict:
    model, latent = inputs["model"], inputs["latent"]
    vae = ctx.component(model, "vae")
    ctx.check()
    decoder = part(model, "decode")
    decoder.update_components(vae=vae)
    image = decoder(latents=latent.latents, output_height=latent.height, output_width=latent.width,
                    output="images")[0]  # fmt: skip
    ctx.progress(1, 1, "decoding")
    return {"image": image}
