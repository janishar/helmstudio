"""MiniMax Music 3 stages on torch: diffusers 0.43's modular MiniMaxMusic3Blocks (the model card's code path, as
~/GenAI/model-samples/minimax-music3/run_samples.py runs it) split at its three sub-blocks, with every component
loaded by the core loader:

- lm: the semantic_generator blocks (the checkpoint's prompt template, then the 8B language model and the RVQ depth
  decoder frame by frame, 25 frames a second, until the end-of-audio token or Length). A forward pre-hook on the
  language model's body is the cancel point and progress step of every frame (the loop itself is the library's).
- flow: the denoise blocks (200-frame windows, 30 Euler steps, CFG 1.7): a cancel point and a progress step on each
  transformer pass.
- vocode: the decode blocks (vocoder per window, crop and stitch), 44.1 kHz stereo, PCM 16-bit.

One generator, as in one pipeline call: lm seeds it on the run's device (torch.Generator(device).manual_seed(seed)) and
hands its state on with the frames, so flow draws its noise from exactly where the language model stopped; a seed
therefore gives the same song bit for bit as the model card's single call (tests/test_minimax_music3_golden.py).

Instrumental: the model has no switch. Its own caption-rewriter rule is to say so in the description, and lyrics may
not be empty, so the style gains INSTRUMENTAL and the lyrics are only "[instrumental]" (also when no lyrics are
given)."""

from dataclasses import dataclass
from functools import lru_cache

import accelerate
import numpy as np
import torch

from core.graph import RunError

MAX_S = 300  # Length's max: Auto lets the model run up to this
INSTRUMENTAL = "Instrumental, no vocals."
CLASSES = {"rvq_depth_decoder": "MiniMaxMusic3RVQDepthDecoder", "condition_encoder": "MiniMaxMusic3ConditionEncoder",
           "transformer": "MiniMaxMusic3Transformer1DModel", "vocoder": "MiniMaxMusic3Vocoder"}  # fmt: skip


@dataclass
class Frames:
    hiddens: (
        torch.Tensor
    )  # [1, frames, codebooks * hidden] on the device: the flow stage's conditioning
    generator: (
        torch.Tensor
    )  # the run's generator state after the language model (Generator.get_state())


@dataclass
class Chunks:
    latents: list  # one denoised latent per 200-frame window, uncropped (the decode block crops)


# --- components


def build(component: str, model):
    """The component on the meta device from its config (buffers made in __init__ stay real, on the CPU)."""
    folder = model.folder(component) / component
    if component == "language_model":
        from transformers import AutoConfig, Qwen3ForCausalLM

        with accelerate.init_empty_weights(include_buffers=False):
            return Qwen3ForCausalLM(AutoConfig.from_pretrained(folder))
    import diffusers

    cls = getattr(diffusers, CLASSES[component])
    with accelerate.init_empty_weights(include_buffers=False):
        return cls.from_config(cls.load_config(folder))


@lru_cache(maxsize=2)
def tokenizer(folder):
    from transformers import Qwen2Tokenizer

    return Qwen2Tokenizer.from_pretrained(folder / "tokenizer")


def scheduler(folder):
    from diffusers import FlowMatchEulerDiscreteScheduler

    return FlowMatchEulerDiscreteScheduler.from_pretrained(folder / "scheduler")


def part(name: str, **components):
    """The modular pipeline of one of MiniMaxMusic3Blocks' sub-blocks over these loaded components."""
    from diffusers.modular_pipelines import MiniMaxMusic3Blocks, MiniMaxMusic3ModularPipeline

    pipe = MiniMaxMusic3ModularPipeline(blocks=MiniMaxMusic3Blocks().sub_blocks[name])
    pipe.update_components(**components)
    return pipe


def song_text(node: str, style: str, params: dict) -> tuple[str, str]:
    """(prompt, lyrics) as the model reads them: instrumental (or no lyrics) by the model's own rule."""
    style, lyrics = style.strip(), params["lyrics"].strip()
    if not style:
        raise RunError(f"{node}: describe the music (genre, mood, voice, instruments)")
    if params["instrumental"] or not lyrics:
        return f"{style.rstrip('.')}. {INSTRUMENTAL}", "[instrumental]"
    return style, lyrics


def pcm16(audio: np.ndarray) -> bytes:
    """[channels, samples] float in [-1, 1] -> interleaved PCM 16-bit."""
    return (np.clip(audio.T, -1, 1) * 32767).round().astype("<i2").tobytes()


def _passes(ctx, module, of: int, phase: str):
    """Hooks on `module`: a cancel point before each forward, a progress step (of `of`) after it."""
    done = [0]

    def before(*_) -> None:
        ctx.check()

    def after(*_) -> None:
        done[0] += 1
        ctx.progress(min(done[0], of), of, phase)

    return [module.register_forward_pre_hook(before), module.register_forward_hook(after)]


# --- stages


@torch.no_grad()
def lm(ctx, params: dict, inputs: dict) -> dict:
    model = inputs["model"]
    prompt, lyrics = song_text(ctx.node, inputs["prompt"], params)
    seconds = float(params["seconds"] or MAX_S)
    language_model = ctx.component(model, "language_model")
    pipe = part("semantic_generator", tokenizer=tokenizer(model.folder("language_model")),
                language_model=language_model, rvq_depth_decoder=ctx.component(model, "rvq_depth_decoder"))  # fmt: skip
    generator = torch.Generator(ctx.device).manual_seed(inputs["seed"])
    # one body forward for the prompt, then one per frame (`sampled.item()` syncs each, so progress follows the GPU)
    hooks = _passes(ctx, language_model.model, round(seconds * 25) + 2, "composing")
    try:
        state = pipe(prompt=prompt, lyrics=lyrics, audio_duration=seconds, generator=generator)
    except ValueError as e:  # a prompt over 5000 tokens, or no frame before the end token
        raise RunError(f"{ctx.node}: {e}") from e
    finally:
        for h in hooks:
            h.remove()
    return {"cond": Frames(state.get("frame_hiddens"), generator.get_state())}


@torch.no_grad()
def flow(ctx, params: dict, inputs: dict) -> dict:
    model, cond = inputs["model"], inputs["cond"]
    transformer = ctx.component(model, "transformer")
    pipe = part("denoise", condition_encoder=ctx.component(model, "condition_encoder"), transformer=transformer,
                scheduler=scheduler(model.folder("transformer")))  # fmt: skip
    generator = torch.Generator(ctx.device)
    generator.set_state(cond.generator)
    frames = cond.hiddens.shape[1]
    windows = (
        1 if frames <= 200 else len(range(0, frames - 100, 100))
    )  # before_denoise's chunk_starts
    hooks = _passes(
        ctx, transformer, windows * 30 * 2, "flow matching"
    )  # 30 steps, cond + uncond passes
    try:
        state = pipe(frame_hiddens=cond.hiddens, generator=generator)
    finally:
        for h in hooks:
            h.remove()
    return {"latent": Chunks(state.get("latent_chunks"))}


@torch.no_grad()
def vocode(ctx, params: dict, inputs: dict) -> dict:
    model, latent = inputs["model"], inputs["latent"]
    vocoder = ctx.component(model, "vocoder")
    hooks = _passes(ctx, vocoder, len(latent.latents), "decoding")
    try:
        audio = part("decode", vocoder=vocoder)(latent_chunks=latent.latents, output="audios")[0]
    finally:
        for h in hooks:
            h.remove()
    rate = int(vocoder.config.sampling_rate)
    return {"audio": {"wav": pcm16(audio), "sample_rate": rate, "duration_s": audio.shape[-1] / rate,
                      "channels": 2}}  # fmt: skip
