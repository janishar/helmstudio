"""ACE-Step 1.5 stages on torch (plan 38 section 44): diffusers 0.43's AceStepPipeline over components the core
loader fills from the original checkpoints (build on meta + rename, after diffusers'
scripts/convert_ace_step_to_diffusers.py), and the planner ported from ACE-Step's llm_inference.py
(~/GenAI/model-samples/src/ACE-Step-1.5 at ca1e85f).

song: the lyrics and settings, as the family's Conditioning (a Song), so the plan can be bypassed.
plan: the 5 Hz LM's two phases as generate_with_stop_condition runs them. Phase 1 (no CFG): the chain of thought from
  the caption and lyrics until </think> (the YAML metas: bpm, caption, duration, keyscale, language, timesignature),
  audio-code tokens blocked; a set BPM is written into the thought before it starts (the first key), the other set
  values replace what it wrote. Phase 2: exactly duration x 5 audio codes after the thought, CFG 2.0 against the
  "NO USER INPUT" prompt over the code tokens only. Temperature 0.85, top-p 0.9 (the library defaults); every token
  is drawn on the CPU from a generator seeded with the run's seed, so a seed repeats (the repo's did not).
compose / repaint / cover / extend: the pipeline's __call__ with output_type latent (8 steps, shift 3, no CFG: turbo),
  a cancel point and a progress step per step. Text to music with the plan's codes is the repo's cover task (the
  pipeline switches itself, as the repo's handler does). Extend is a repaint of silence past the song's edge.
decode: the VAE (float16, tiled past 512 latent frames), the pipeline's two-stage levelling (peak > 1 divided out,
  then -1 dBFS peak); a repaint's or extend's outside part is the source's own samples (10 ms crossfades). A song
  with tagged lyrics carries sections: each tag's line, timed by the DiT's lyric cross-attention
  (lyric_timestamp.py's heads, its bidirectional consensus and DTW, ported in `sections`)."""

import re
from dataclasses import dataclass, field, replace
from functools import lru_cache

import accelerate
import numpy as np
import torch
import torch.nn.functional as F
import yaml
from torch import nn

from core import media
from core.graph import RunError

SAMPLE_RATE = 48000
LATENT_HZ = 25  # 48000 / 1920
DIT = {"turbo-2b": "transformer", "xl-turbo": "transformer_xl"}
# the folder of each DiT's config.json and silence_latent.pt, in its variant's folder
DIT_DIR = {"transformer": "acestep-v15-turbo", "transformer_xl": ""}
UNPLANNED_S = 30.0  # Auto length without the plan (the pipeline's metadata default)
SHIFT = 3.0
# the planner (llm_inference.py, constants.py)
LM_INSTRUCTION = "Generate audio semantic tokens based on the given conditions:"
CODE0, CODES = 151669, 64000  # <|audio_code_0|> and the valid codes (MAX_AUDIO_CODE 63999)
THINK_END = 151668  # </think>
TEMPERATURE, TOP_P, LM_CFG = 0.85, 0.9, 2.0
TOP_K = 4096  # nucleus: sorted first (a full sort of 217k logits per token costs more than the LM's step)
COT_TOKENS = 600  # phase 1's cap (the repo's buffer: 500 over the codes it never writes there)
KEYS = {f"{n}{a} {m}" for n in "ABCDEFG" for a in ("", "#", "b") for m in ("major", "minor")}
TIMES = {"2/4": "2", "3/4": "3", "4/4": "4", "6/8": "6"}  # param value -> the model's timesignature
TIME_OF = {v: k for k, v in TIMES.items()}
TAG = re.compile(r"^\s*\[([^\]]+)\]\s*$")
CROSSFADE_S = 0.01
# lyric_alignment_common.py: the 2B's alignment heads (XL's are in its config.json)
LYRIC_HEADS = {2: [6], 3: [10, 11], 4: [3], 5: [8, 9], 6: [8]}


@dataclass
class Song:
    """The family's Conditioning: what the song stage set, filled by the plan (codes, metas, planned)."""

    lyrics: str
    instrumental: bool
    seconds: float  # 0: Auto
    bpm: int  # 0: Auto
    key: str  # "auto" or a key
    time: str  # "auto" or "4/4"
    language: str  # "auto" or a code
    codes: str = ""  # the plan's <|audio_code_n|> string
    planned: bool = False

    def sung(self) -> str:
        """The lyrics the models read: [Instrumental] for an instrumental or empty lyrics (the repo's cue)."""
        text = self.lyrics.strip()
        return "[Instrumental]" if self.instrumental or not text else text

    def metas(self) -> dict:
        """The pipeline's metadata kwargs: unset values None (N/A in its prompt)."""
        return {"bpm": self.bpm or None, "keyscale": None if self.key == "auto" else self.key,
                "timesignature": TIMES.get(self.time)}  # fmt: skip

    def vocal_language(self) -> str:
        return "unknown" if self.language == "auto" or self.instrumental else self.language


@dataclass
class Latent:
    latents: torch.Tensor  # [1, T, 64] on the device
    sections: list = field(default_factory=list)
    keep: np.ndarray | None = (
        None  # the source, float32 [2, n] at 48 kHz, where the output keeps it
    )
    window: tuple[float, float] | None = None  # the generated seconds of the output


# --- components

DIT_KEYS = ("hidden_size", "intermediate_size", "num_hidden_layers", "num_attention_heads", "num_key_value_heads",
            "head_dim", "in_channels", "audio_acoustic_hidden_dim", "patch_size", "rope_theta", "attention_bias",
            "attention_dropout", "rms_norm_eps", "sliding_window", "layer_types")  # fmt: skip


class Dit(nn.Module):
    """One DiT checkpoint as the pipeline's four modules."""

    def __init__(self, cfg: dict) -> None:
        from diffusers import AceStepConditionEncoder, AceStepTransformer1DModel
        from diffusers.pipelines.ace_step import AceStepAudioTokenDetokenizer, AceStepAudioTokenizer

        super().__init__()
        enc = {k: cfg.get(f"encoder_{k}", cfg[k]) for k in ("hidden_size", "intermediate_size",
                                                               "num_attention_heads", "num_key_value_heads")}  # fmt: skip
        common = {k: cfg[k] for k in ("head_dim", "rope_theta", "attention_bias", "attention_dropout",
                                      "rms_norm_eps", "sliding_window")}  # fmt: skip
        pooler = cfg.get("num_attention_pooler_hidden_layers", 2)
        self.transformer = AceStepTransformer1DModel(
            **{k: cfg[k] for k in DIT_KEYS},
            encoder_hidden_size=enc["hidden_size"],
            is_turbo=bool(cfg.get("is_turbo", False)),
            model_version=cfg.get("model_version"),
        )
        self.condition_encoder = AceStepConditionEncoder(
            **enc, **common, text_hidden_dim=cfg["text_hidden_dim"], timbre_hidden_dim=cfg["timbre_hidden_dim"],
            num_lyric_encoder_hidden_layers=cfg["num_lyric_encoder_hidden_layers"],
            num_timbre_encoder_hidden_layers=cfg["num_timbre_encoder_hidden_layers"],
        )  # fmt: skip
        pool = {"audio_acoustic_hidden_dim": cfg["audio_acoustic_hidden_dim"],
                "pool_window_size": cfg.get("pool_window_size", 5),
                "num_attention_pooler_hidden_layers": pooler, "layer_types": cfg["layer_types"][:pooler]}  # fmt: skip
        self.audio_tokenizer = AceStepAudioTokenizer(
            **enc, **common, **pool, fsq_dim=cfg.get("fsq_dim", enc["hidden_size"]),
            fsq_input_levels=cfg.get("fsq_input_levels", [8, 8, 8, 5, 5, 5]),
            fsq_input_num_quantizers=cfg.get("fsq_input_num_quantizers", 1),
        )  # fmt: skip
        self.audio_token_detokenizer = AceStepAudioTokenDetokenizer(**enc, **common, **pool)
        heads = cfg.get("lyric_alignment_layers_config")
        self.lyric_heads = {int(k): v for k, v in heads.items()} if heads else LYRIC_HEADS


class TiedHead(nn.Module):
    """The LM head tied to the embedding (tie_word_embeddings): no weight of its own to load."""

    def __init__(self, embed: nn.Embedding) -> None:
        super().__init__()
        # not a submodule: its weight is loaded once, as the embedding's
        object.__setattr__(self, "embed", embed)

    def forward(self, x: torch.Tensor) -> torch.Tensor:
        return F.linear(x, self.embed.weight)


def build(component: str, model) -> nn.Module:
    """The component on the meta device (buffers made in __init__ stay real, on the CPU; the loader moves them). The
    DiT's silence latent ([1, 15000, 64], silence_latent.pt transposed, as the converter bakes it) is set here."""
    import json

    from transformers import AutoConfig

    folder = model.folder(component) / DIT_DIR.get(component, "")
    if component in DIT.values():
        cfg = json.loads((folder / "config.json").read_text())
        with accelerate.init_empty_weights(include_buffers=False):
            out = Dit(cfg)
        silence = torch.load(folder / "silence_latent.pt", weights_only=True, map_location="cpu")
        out.condition_encoder.silence_latent = silence.transpose(1, 2).float().contiguous()
        return out
    if component == "vae":
        from diffusers import AutoencoderOobleck

        with accelerate.init_empty_weights(include_buffers=False):
            out = AutoencoderOobleck.from_config(AutoencoderOobleck.load_config(folder / "vae"))
        out.use_tiling = True  # encodes past 30 s, decodes past 512 latent frames (20.5 s) in tiles
        return out
    sub = "acestep-5Hz-lm-1.7B" if component == "lm" else "Qwen3-Embedding-0.6B"
    config = AutoConfig.from_pretrained(folder / sub)
    if component == "text_encoder":
        from transformers import Qwen3Model

        with accelerate.init_empty_weights(include_buffers=False):
            return Qwen3Model(config)
    from transformers import Qwen3ForCausalLM

    with accelerate.init_empty_weights(include_buffers=False):
        out = Qwen3ForCausalLM(config)
    out.lm_head = TiedHead(out.model.embed_tokens)
    return out


ATTN = ((".q_proj.", ".to_q."), (".k_proj.", ".to_k."), (".v_proj.", ".to_v."), (".o_proj.", ".to_out.0."),
        (".q_norm.", ".norm_q."), (".k_norm.", ".norm_k."))  # fmt: skip
DIT_PARTS = (("decoder.", "transformer."), ("encoder.", "condition_encoder."), ("tokenizer.", "audio_tokenizer."),
             ("detokenizer.", "audio_token_detokenizer."))  # fmt: skip


def rename(component: str, key: str) -> str | None:
    """The converter's renames: the DiT file's four prefixes onto the Dit's modules, the decoder's Sequential
    proj_in / proj_out convolutions, the attention names; the LM's bare Qwen3 names under `model.`."""
    if component == "lm":
        return f"model.{key}"
    if component not in DIT.values():
        return key
    if key == "null_condition_emb":
        return "condition_encoder.null_condition_emb"
    for old, new in DIT_PARTS:
        if key.startswith(old):
            key = new + key.removeprefix(old)
            break
    else:
        return None
    key = key.replace("proj_in.1.", "proj_in_conv.").replace("proj_out.1.", "proj_out_conv.")
    for old, new in ATTN:
        key = key.replace(old, new)
    return key


@lru_cache(maxsize=2)
def _tokenizer(path):
    from transformers import AutoTokenizer

    return AutoTokenizer.from_pretrained(path)


def tokenizer(model, component: str):
    sub = "acestep-5Hz-lm-1.7B" if component == "lm" else "Qwen3-Embedding-0.6B"
    return _tokenizer(model.folder(component) / sub)


# --- song


def song(ctx, params: dict, inputs: dict) -> dict:
    return {"song": Song(params["lyrics"], params["instrumental"], float(params["seconds"]), int(params["bpm"]),
                         params["key"], params["time"], params["language"])}  # fmt: skip


# --- plan


def _style(ctx, text: str) -> str:
    caption = text.strip()
    if not caption:
        raise RunError(f"{ctx.node}: describe the music (the style is empty)")
    return caption


def _chat(tok, user: str) -> str:
    return tok.apply_chat_template(
        [
            {"role": "system", "content": f"# Instruction\n{LM_INSTRUCTION}\n\n"},
            {"role": "user", "content": user},
        ],
        tokenize=False,
        add_generation_prompt=True,
    )


def nucleus(logits: torch.Tensor, gen: torch.Generator) -> int:
    """One token from [vocab] logits (float32 on the CPU) as the repo draws it: top-p on the untempered softmax,
    then the kept logits / TEMPERATURE, drawn with `gen`. The sort runs over the top TOP_K only when they already
    hold more than TOP_P (nearly always: the kept set is the same), else over the whole vocabulary."""
    sorted_l, order = torch.topk(logits, min(TOP_K, len(logits)))
    probs = torch.exp(sorted_l - torch.logsumexp(logits, dim=-1))
    if probs.sum() <= TOP_P:
        sorted_l, order = torch.sort(logits, descending=True)
        probs = torch.softmax(sorted_l, dim=-1)
    cut = torch.cumsum(probs, dim=-1) > TOP_P
    cut[1:] = cut[:-1].clone()
    cut[0] = False
    sorted_l[cut] = float("-inf")
    pick = torch.multinomial(torch.softmax(sorted_l / TEMPERATURE, dim=-1), 1, generator=gen)
    return int(order[pick])


def parse_metas(text: str) -> dict:
    """The thought's YAML fields (parse_lm_output): `key: value` lines, indented lines continuing the value."""
    body = text.split("</think>")[0].replace("<think>", "")
    out, key = {}, None
    for line in body.split("\n"):
        if line.strip().startswith("<"):
            continue
        if line and not line[0].isspace() and ":" in line:
            key, _, value = line.partition(":")
            key = key.strip().lower()
            out[key] = value.strip()
        elif key and line.strip():
            out[key] = f"{out[key]} {line.strip()}".strip()
    return out


def cot(metas: dict) -> str:
    """The thought for phase 2 (_format_metadata_as_cot): the set fields as sorted YAML."""
    items = {k: metas[k] for k in ("bpm", "caption", "duration", "keyscale", "language", "timesignature")
             if metas.get(k) not in (None, "")}  # fmt: skip
    return f"<think>\n{yaml.dump(items, allow_unicode=True, sort_keys=True).strip()}\n</think>"


def settle(song: Song, said: dict) -> dict:
    """The metas phase 2 reads: the song's set values, else the thought's when valid (BPM 30-300, length 10-600 s,
    a known key and time), else none."""

    def num(v, lo, hi):
        try:
            n = int(float(v))
        except TypeError, ValueError:
            return None
        return n if lo <= n <= hi else None

    key = song.key if song.key != "auto" else said.get("keyscale", "")
    time = TIMES.get(song.time) or (said.get("timesignature", "").split("/")[0] or None)
    return {
        "bpm": song.bpm or num(said.get("bpm"), 30, 300),
        "caption": said.get("caption", "").strip(),
        "duration": int(song.seconds) or num(said.get("duration"), 10, 600),
        "keyscale": key if key in KEYS else "",
        "language": song.language if song.language != "auto" else said.get("language", "").strip(),
        "timesignature": int(time) if time in TIMES.values() else "",
    }


def _step(lm, ids, mask, cache):
    out = lm(input_ids=ids, attention_mask=mask, past_key_values=cache, use_cache=True)
    return out.logits[:, -1].float(), out.past_key_values


def think(ctx, lm, tok, prompt: str, gen: torch.Generator) -> str:
    """Phase 1: the thought after `prompt` up to </think> (audio codes blocked), one cancel point per token."""
    ids = tok(prompt, return_tensors="pt").input_ids.to(ctx.device)
    mask, cache, out = torch.ones_like(ids), None, []
    with torch.inference_mode():
        for n in range(COT_TOKENS):
            ctx.check()
            logits, cache = _step(lm, ids if cache is None else ids[:, -1:], mask, cache)
            logits = logits[0].cpu()
            logits[CODE0:] = float("-inf")
            token = nucleus(logits, gen)
            out.append(token)
            ctx.progress(min(n + 1, 99), 100, "planning")
            if token in (THINK_END, tok.eos_token_id):
                break
            ids = torch.cat([ids, torch.tensor([[token]], device=ctx.device)], dim=1)
            mask = torch.ones_like(ids)
    return tok.decode(out, skip_special_tokens=False)


def codes(ctx, lm, tok, cond: str, uncond: str, n: int, gen: torch.Generator) -> str:
    """Phase 2: exactly `n` audio codes after the thought, CFG LM_CFG between `cond` and `uncond` (left-padded, one
    batch) over the code tokens only."""
    tok.padding_side = "left"
    batch = tok([cond, uncond], return_tensors="pt", padding=True)
    tok.padding_side = "right"
    ids, mask = batch.input_ids.to(ctx.device), batch.attention_mask.to(ctx.device)
    cache, out = None, []
    with torch.inference_mode():
        for i in range(n):
            ctx.check()
            logits, cache = _step(lm, ids if cache is None else ids[:, -1:], mask, cache)
            c, u = logits[:, CODE0 : CODE0 + CODES].cpu()
            pick = nucleus(u + LM_CFG * (c - u), gen)
            out.append(pick)
            ctx.progress(i + 1, n, "planning")
            nxt = torch.full((2, 1), CODE0 + pick, device=ctx.device)
            ids, mask = torch.cat([ids, nxt], dim=1), torch.cat([mask, torch.ones_like(nxt)], dim=1)
    return "".join(f"<|audio_code_{c}|>" for c in out)


def plan_line(metas: dict) -> str:
    """`90 BPM · A minor · 4/4 · 2:10 · en` (the parts known)."""
    d = metas.get("duration")
    parts = [f"{metas['bpm']} BPM" if metas.get("bpm") else "", metas.get("keyscale") or "",
             TIME_OF.get(str(metas.get("timesignature")), ""), f"{d // 60}:{d % 60:02d}" if d else "",
             metas.get("language") or ""]  # fmt: skip
    return " · ".join(p for p in parts if p)


def plan(ctx, params: dict, inputs: dict) -> dict:
    model, song = inputs["model"], inputs["song"]
    caption, lyrics = _style(ctx, inputs["text"]), song.sung()
    lm, tok = ctx.component(model, "lm"), tokenizer(model, "lm")
    gen = torch.Generator().manual_seed(int(inputs["seed"]))
    user = f"# Caption\n{caption}\n\n# Lyric\n{lyrics}\n"
    head = (
        f"<think>\nbpm: {song.bpm}\n" if song.bpm else ""
    )  # the first key: the thought goes on from a set BPM
    said = parse_metas(head + think(ctx, lm, tok, _chat(tok, user) + head, gen))
    metas = settle(song, said)
    if not metas["duration"]:
        metas["duration"] = int(UNPLANNED_S)
    metas["caption"] = metas["caption"] or caption
    thought = cot(metas)
    n = metas["duration"] * 5
    audio = codes(ctx, lm, tok, _chat(tok, user) + thought + "\n\n",
                  _chat(tok, "NO USER INPUT") + "<think>\n\n</think>\n\n", n, gen)  # fmt: skip
    t = metas["timesignature"]
    planned = replace(song, seconds=float(metas["duration"]), bpm=metas["bpm"] or 0, key=metas["keyscale"] or "auto",
                      time=TIME_OF.get(str(t), "auto"), language=metas["language"] or song.language,
                      codes=audio, planned=True)  # fmt: skip
    return {"song": planned, "style": metas["caption"], "plan": plan_line(metas)}


# --- the DiT stages


@lru_cache(maxsize=1)
def _pipe_class():
    from diffusers import AceStepPipeline

    class Pipe(AceStepPipeline):
        def encode_prompt(self, *a, **kw):  # the float32 text encoder under a bf16 DiT (XL)
            out = super().encode_prompt(*a, **kw)
            return tuple(t.to(self.transformer.dtype) if t.is_floating_point() else t for t in out)

    return Pipe


def _pipe(ctx, model, vae):
    """The pipeline over the loaded components (its own scheduler: t in [0, 1], the converter's settings)."""
    from diffusers import FlowMatchEulerDiscreteScheduler

    dit = ctx.component(model, DIT[model.variant])
    pipe = _pipe_class()(
        vae=vae, text_encoder=ctx.component(model, "text_encoder"), tokenizer=tokenizer(model, "text_encoder"),
        transformer=dit.transformer, condition_encoder=dit.condition_encoder,
        scheduler=FlowMatchEulerDiscreteScheduler(num_train_timesteps=1, shift=1.0),
        audio_tokenizer=dit.audio_tokenizer, audio_token_detokenizer=dit.audio_token_detokenizer)  # fmt: skip
    pipe.set_progress_bar_config(disable=True)
    return pipe, dit


def source_audio(ctx, value: dict, name: str = "source") -> np.ndarray:
    """A media value (an audio take or a video's sound) as float32 [2, n] at 48 kHz."""
    pcm = np.frombuffer(media.pcm(value["data"], SAMPLE_RATE, 2), "<i2")
    if not len(pcm):
        raise RunError(f"{ctx.node}.{name}: take {value.get('take_id')} has no sound")
    return (pcm.reshape(-1, 2).T / 32768.0).astype(np.float32)


def _dit(ctx, params: dict, inputs: dict, task: str, src: np.ndarray | None = None, **kw) -> tuple:
    """One pipeline call: (the latent, its sections), a cancel point and a progress step per step; seeded noise from
    the CPU (and the VAE's posterior samples from the device generator, seeded the same)."""
    model, song, seed = inputs["model"], inputs["song"], int(inputs["seed"])
    caption = _style(ctx, inputs["text"])
    vae = ctx.component(model, "vae")
    pipe, dit = _pipe(ctx, model, vae)
    steps = int(params["steps"])
    torch.manual_seed(seed)

    def step(_pipe, i, _t, tensors):
        ctx.check()
        ctx.progress(i + 1, steps, "sampling")
        return tensors

    duration = len(src[0]) / SAMPLE_RATE if src is not None else (song.seconds or UNPLANNED_S)
    with torch.inference_mode():
        latents = pipe(prompt=caption, lyrics=song.sung(), audio_duration=duration,
                       vocal_language=song.vocal_language(), num_inference_steps=steps, guidance_scale=1.0,
                       shift=SHIFT, generator=torch.Generator().manual_seed(seed), output_type="latent",
                       callback_on_step_end=step, task_type=task, **song.metas(),
                       src_audio=None if src is None else torch.from_numpy(src), **kw).audios  # fmt: skip
    ctx.check()
    out, sections = latents.clone(), []
    if task in ("text2music", "cover") and not song.instrumental and song.lyrics.strip():
        sections = lyric_sections(pipe, dit, out, caption, song, src, kw.get("audio_codes"))
    return out, sections


def compose(ctx, params: dict, inputs: dict) -> dict:
    song, kw = inputs["song"], {}
    if rows := params["reference"]:
        ref = source_audio(ctx, rows[0]["audio"], "reference[1].audio")[:, : 30 * SAMPLE_RATE]
        kw["reference_audio"] = torch.from_numpy(np.ascontiguousarray(ref))
    if song.codes:
        kw["audio_codes"] = song.codes
    latents, sections = _dit(ctx, params, inputs, "text2music", **kw)
    return {"latent": Latent(latents, sections)}


def repaint(ctx, params: dict, inputs: dict) -> dict:
    src, span = source_audio(ctx, params["source"]), params["span"]
    end = len(src[0]) / SAMPLE_RATE
    a, b = float(span["start"]), min(float(span["end"]), end)
    if not 0 <= a < b:
        raise RunError(f"{ctx.node}.span: start must be before end")
    latents, _ = _dit(ctx, params, inputs, "repaint", src, repainting_start=a, repainting_end=b)
    return {"latent": Latent(latents, [], src, (a, b))}


def cover(ctx, params: dict, inputs: dict) -> dict:
    src = source_audio(ctx, params["source"])
    latents, sections = _dit(ctx, params, inputs, "cover", src,
                             audio_cover_strength=float(params["strength"]))  # fmt: skip
    return {"latent": Latent(latents, sections)}


def extend(ctx, params: dict, inputs: dict) -> dict:
    src, add = source_audio(ctx, params["source"]), float(params["seconds"])
    have = len(src[0]) / SAMPLE_RATE
    if have + add > media.MAX_TAKE_S:
        raise RunError(f"{ctx.node}.seconds: the song would last over {media.MAX_TAKE_S} s")
    pad = np.zeros((2, round(add * SAMPLE_RATE)), np.float32)
    end = params["where"] == "end"
    padded = np.concatenate([src, pad] if end else [pad, src], axis=1)
    a, b = (have, have + add) if end else (0.0, add)
    latents, _ = _dit(ctx, params, inputs, "repaint", padded, repainting_start=a, repainting_end=b)
    return {"latent": Latent(latents, [], padded, (a, b))}


# --- decode


def splice(out: np.ndarray, keep: np.ndarray, window: tuple[float, float]) -> np.ndarray:
    """`out` inside the window, `keep` outside, CROSSFADE_S linear ramps just outside its edges
    (repaint_waveform_splice.py)."""
    n = min(out.shape[1], keep.shape[1])
    mask = np.zeros(n, np.float32)
    a, b = (min(n, round(t * SAMPLE_RATE)) for t in window)
    mask[a:b] = 1
    r = round(CROSSFADE_S * SAMPLE_RATE)
    if (lo := max(a - r, 0)) < a:
        mask[lo:a] = np.linspace(0, 1, a - lo + 2, dtype=np.float32)[1:-1]
    if b < (hi := min(b + r, n)):
        mask[b:hi] = np.linspace(1, 0, hi - b + 2, dtype=np.float32)[1:-1]
    return out[:, :n] * mask + keep[:, :n] * (1 - mask)


def level(audio: np.ndarray) -> np.ndarray:
    """The pipeline's levelling: a peak over 1 divided out, then the peak at -1 dBFS."""
    peak = float(np.abs(audio).max()) if audio.size else 0.0
    if peak > 1:
        audio, peak = audio / peak, 1.0
    return audio * (10 ** (-1 / 20) / max(peak, 1e-6))


def decode(ctx, params: dict, inputs: dict) -> dict:
    latent = inputs["latent"]
    vae = ctx.component(inputs["model"], "vae")
    ctx.progress(1, 1, "decoding")
    with torch.inference_mode():
        wav = (
            vae.decode(latent.latents.transpose(1, 2).to(vae.dtype)).sample[0].float().cpu().numpy()
        )
    ctx.check()
    peak = float(np.abs(wav).max()) if wav.size else 0.0
    if peak > 1:
        wav = wav / peak
    if latent.keep is not None:
        wav = splice(wav, latent.keep, latent.window)
    wav = level(wav)
    pcm = (np.clip(wav, -1, 1) * 32767).round().astype("<i2").T.tobytes()
    audio = {
        "wav": pcm,
        "sample_rate": SAMPLE_RATE,
        "channels": 2,
        "duration_s": wav.shape[1] / SAMPLE_RATE,
    }
    if latent.sections:
        audio["sections"] = latent.sections
    return {"audio": audio}


# --- sections (lyric_timestamp.py, core/scoring/dit_alignment.py, _dtw.py)


class _Capture:
    """A cross-attention processor that also keeps softmax(q k^T / sqrt d) of the wanted heads, [heads, Lq, Lk]."""

    def __init__(self, base, heads: list[int], store: list) -> None:
        self.base, self.heads, self.store = base, heads, store

    def __call__(self, attn, hidden_states, encoder_hidden_states=None, attention_mask=None, **kw):
        q = attn.norm_q(attn.to_q(hidden_states).unflatten(-1, (attn.heads, attn.head_dim)))
        k = attn.norm_k(
            attn.to_k(encoder_hidden_states).unflatten(-1, (attn.kv_heads, attn.head_dim))
        )
        k = k.repeat_interleave(attn.heads // attn.kv_heads, dim=2)
        h = self.heads
        w = torch.einsum("bqhd,bkhd->bhqk", q[:, :, h].float(), k[:, :, h].float()) * attn.scaling
        self.store.append(w.softmax(-1)[0].cpu())
        return self.base(attn, hidden_states, encoder_hidden_states, attention_mask, **kw)


def dtw(cost: np.ndarray) -> tuple[np.ndarray, np.ndarray]:
    """The monotonic alignment path (_dtw.py dtw_cpu, its tie rule: the diagonal or up only when strictly least,
    else left): text index and time index per step. One anti-diagonal at a time (its cells depend only on the two
    before), so a 10 min song's matrix takes seconds, not minutes."""
    n, m = cost.shape
    acc = np.full((n + 1, m + 1), np.inf, np.float32)
    trace = np.full((n + 1, m + 1), 2, np.int8)
    acc[0, 0] = 0
    for d in range(2, n + m + 1):
        i = np.arange(max(1, d - m), min(n, d - 1) + 1)
        j = d - i
        c0, c1, c2 = acc[i - 1, j - 1], acc[i - 1, j], acc[i, j - 1]
        t = np.where((c0 < c1) & (c0 < c2), 0, np.where((c1 < c0) & (c1 < c2), 1, 2)).astype(
            np.int8
        )
        acc[i, j] = cost[i - 1, j - 1] + np.choose(t, [c0, c1, c2])
        trace[i, j] = t
    i, j, path = n, m, []
    while i > 0 and j > 0:
        path.append((i - 1, j - 1))
        t = trace[i, j]
        i, j = (i - 1, j - 1) if t == 0 else (i - 1, j) if t == 1 else (i, j - 1)
    path.reverse()
    a = np.array(path)
    return a[:, 0], a[:, 1]


def align(weights: torch.Tensor) -> np.ndarray:
    """The consensus matrix [tokens, frames] of stacked heads' attention [H, tokens, frames] (dit_alignment.py:
    violence 2.0, median width 1, which filters nothing), one head at a time (torch.quantile takes at most 16M
    values: a song past about 1500 lyric tokens by 7500 frames fails it, and so has no markers)."""
    heads = []
    for w in weights:
        p = w.softmax(-1) * w.softmax(-2)
        p = torch.relu(p - 2.0 * torch.quantile(p, 0.5, dim=-1, keepdim=True))
        heads.append(torch.relu(p - 2.0 * torch.quantile(p, 0.5, dim=-2, keepdim=True)) ** 2)
    p = torch.stack(heads)
    std, mean = torch.std_mean(p, unbiased=False)
    return ((p - mean) / (std + 1e-9)).mean(0).numpy()


def line_starts(tok, header: str, lines: list[str]) -> list[int]:
    """The lyric token index (from 0, after the header) where each line starts."""
    n = len(tok.encode(header, add_special_tokens=False))
    return [len(tok.encode(header + "".join(f"{x}\n" for x in lines[:k]), add_special_tokens=False)) - n
            for k in range(len(lines))]  # fmt: skip


def lyric_sections(pipe, dit, latents, caption, song, src, codes) -> list:
    """Each tag line of the lyrics as a section (get_lyric_timestamp): one DiT pass at t = 1/8 over the song's
    latent (fresh seed-42 noise mixed in, as the repo does) with the generation's conditions, its cross-attention of
    the alignment heads against the lyric tokens, the consensus and DTW; a section starts where its first sung line
    does and ends at the next. Empty when the alignment fails (a song without markers is still a song)."""
    lines = song.sung().split("\n")
    tags = [
        (i, m.group(1).strip().lower()) for i, line in enumerate(lines) if (m := TAG.match(line))
    ]
    if not tags:
        return []
    tok, lang = pipe.tokenizer, song.vocal_language()
    header = f"# Languages\n{lang}\n\n# Lyric\n"
    starts = line_starts(tok, header, lines)
    head = len(tok.encode(header, add_special_tokens=False))
    total = len(tok.encode(header + song.sung(), add_special_tokens=False)) - head
    seconds = latents.shape[1] / LATENT_HZ
    store: list = []
    layers = dit.transformer.layers
    procs = {i: layers[i].cross_attn.processor for i in dit.lyric_heads}
    dev, dtype, task = (
        latents.device,
        latents.dtype,
        "cover" if codes or src is not None else "text2music",
    )
    try:
        for i, heads in dit.lyric_heads.items():
            layers[i].cross_attn.set_processor(_Capture(procs[i], heads, store))
        with torch.inference_mode():
            text, text_mask, lyric, lyric_mask = pipe.encode_prompt(
                prompt=caption, lyrics=song.sung(), device=dev, vocal_language=lang, audio_duration=seconds,
                instruction=pipe._get_task_instruction(task), **song.metas())  # fmt: skip
            ref = dit.condition_encoder.silence_latent[:, : 30 * LATENT_HZ].to(dev, dtype)
            enc, _ = dit.condition_encoder(text, text_mask, lyric, lyric_mask, ref,
                                           torch.zeros(1, dtype=torch.long, device=dev))  # fmt: skip
            context, _ = pipe.prepare_src_latents(
                dev, dtype, 1, src_audio=None if src is None else torch.from_numpy(src), audio_codes=codes,
                latent_length=latents.shape[1], task_type=task)  # fmt: skip
            context = torch.cat([context[:, : latents.shape[1]], torch.ones_like(latents)], dim=-1)
            t = torch.full((1,), 1.0 / 8, device=dev, dtype=dtype)
            noise = torch.randn(latents.shape, generator=torch.Generator().manual_seed(42)).to(
                dev, dtype
            )
            xt = t * noise + (1 - t) * latents
            dit.transformer(hidden_states=xt, timestep=t, timestep_r=t, encoder_hidden_states=enc,
                            context_latents=context, return_dict=False)  # fmt: skip
        weights = torch.cat(
            [w[:, :, head : head + total].transpose(-1, -2) for w in store]
        )  # [H, tokens, frames]
        tokens, frames = dtw(-align(weights).astype(np.float64))
    except Exception:  # ponytail: markers are a nicety; the take must not fail on them
        import logging

        logging.getLogger("ace_step").exception("lyric timestamps failed")
        return []
    finally:
        for i, p in procs.items():
            layers[i].cross_attn.set_processor(p)
    per_frame, first = seconds / weights.shape[-1], {}
    for tk, fr in zip(tokens, frames, strict=True):
        first.setdefault(int(tk), float(fr) * per_frame)
    return sections_of(tags, lines, starts, first, seconds)


def sections_of(tags, lines, starts, first: dict, seconds: float) -> list:
    """[{label, start_s, end_s}] from each tag's first sung line's first token time (`first`: token -> s); the first
    section starts at 0, each ends where the next starts, empty ones dropped."""
    out = []
    for n, (i, label) in enumerate(tags):
        nxt = tags[n + 1][0] if n + 1 < len(tags) else len(lines)
        sung = [
            starts[j] for j in range(i + 1, nxt) if lines[j].strip() and not TAG.match(lines[j])
        ]
        at = first.get(sung[0] if sung else starts[i], 0.0) if n else 0.0
        out.append({"label": label, "start_s": round(min(at, seconds), 3)})
    out.sort(key=lambda s: s["start_s"])
    for n, s in enumerate(out):
        s["end_s"] = out[n + 1]["start_s"] if n + 1 < len(out) else round(seconds, 3)
    return [s for s in out if s["end_s"] > s["start_s"]]
