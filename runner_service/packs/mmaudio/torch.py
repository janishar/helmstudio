"""MMAudio large 44k v2 stages on torch (plan 38): sound from a text, and sound for a video with that video muxed. The
networks are MMAudio's own code (vendor/mmaudio at 974010a, VENDORED.md); the sampling is eval_utils.generate and
the frame reading eval_utils.load_video / av_utils.read_frames, ported here as ~/GenAI/model-samples/mmaudio's
run_samples.py runs them (25 Euler steps, cfg 4.5, bf16, an empty negative prompt, the seed in a device generator).

The checkpoints are torch.save files (.pth, .bin, .pt), so the pack loads its components itself (load_component,
packs/README.md): each module is built on meta, its parameters assigned from the memory-mapped checkpoint
(core.loaders_torch.state_dict: nothing copied into RAM whole), the weight norms removed in fp32 on the CPU as
upstream does, then the whole module moved and cast to the device at bf16 (the sample's `.to(device, dtype)`).

PyAV is not in the env: frames come from ffmpeg as RGB (frame i at i / fps, a constant frame rate), picked for 8 and
25 fps exactly as read_frames picks them, then resized as load_video does, one frame at a time."""

import dataclasses
import subprocess
import sys
import tempfile
import wave
from pathlib import Path

import accelerate
import numpy as np
import torch
from PIL import Image
from torch import nn

from core import media
from core.graph import RunError

sys.path.append(str(Path(__file__).parent / "vendor"))  # the vendored model code: `import mmaudio`

from mmaudio.ext.autoencoder.vae import get_my_vae
from mmaudio.ext.bigvgan_v2.bigvgan import BigVGAN, load_hparams_from_json
from mmaudio.ext.synchformer import Synchformer
from mmaudio.model.flow_matching import FlowMatching
from mmaudio.model.networks import get_my_mmaudio
from mmaudio.model.sequence_config import CONFIG_44K

NET = "large_44k_v2"
RATE = CONFIG_44K.sampling_rate  # 44.1 kHz mono
CLIP_SIZE, CLIP_FPS = 384, 8.0  # eval_utils._CLIP_*
SYNC_SIZE, SYNC_FPS = 224, 25.0  # eval_utils._SYNC_*
CLIP_MEAN, CLIP_STD = [0.48145466, 0.4578275, 0.40821073], [0.26862954, 0.26130258, 0.27577711]
TOKENIZER = (
    "ViT-H-14-378-quickgelu"  # features_utils: the same as ViT-H-14 (open_clip's own, no download)
)
CLIP_BATCH = SYNC_BATCH = 40  # generate's clip / sync_batch_size_multiplier at batch 1


# --- components


def _meta(make):
    with accelerate.init_empty_weights(include_buffers=False):
        return make()


def _assign(module: nn.Module, sd: dict) -> nn.Module:
    """The parameters become the checkpoint's own (memory-mapped) tensors: strict, no copy."""
    nn.Module.load_state_dict(module, sd, strict=True, assign=True)
    return module


def _net(path: Path, model) -> nn.Module:
    from core.loaders_torch import state_dict

    sd = state_dict(path)
    sd.pop("t_embed.freqs", None)  # MMAudio.load_weights: made in __init__
    net = _assign(_meta(lambda: get_my_mmaudio(NET)), sd)
    net.initialize_rotations()  # built on meta: again on the CPU (each run sets its own lengths)
    return net


def _autoencoder(path: Path, model) -> nn.Module:
    """AutoEncoderModule's VAE: loaded, its weight norm removed, the encoder dropped (need_vae_encoder=False)."""
    from core.loaders_torch import state_dict

    vae = _assign(_meta(lambda: get_my_vae("44k")), state_dict(path))
    vae.remove_weight_norm()
    del vae.encoder
    return vae


def _bigvgan(path: Path, model) -> nn.Module:
    """BigVGANv2.from_pretrained on the local folder (config.json, the `generator` of bigvgan_generator.pt), built
    for real: its weight-norm keys load through torch's own compatibility hook; then remove_weight_norm()."""
    from core.loaders_torch import state_dict

    voc = BigVGAN(load_hparams_from_json(str(path.parent / "config.json")), use_cuda_kernel=False)
    voc.load_state_dict(state_dict(path)["generator"])
    voc.remove_weight_norm()
    return voc


def _synchformer(path: Path, model) -> nn.Module:
    from core.loaders_torch import state_dict

    sd = {
        k: v for k, v in state_dict(path).items() if k.startswith("vfeat_extractor")
    }  # Synchformer's own filter
    return _assign(_meta(Synchformer), sd)


def _clip(path: Path, model) -> nn.Module:
    """DFN5B CLIP ViT-H-14-384 from its open_clip folder, with features_utils.patch_clip (last hidden states)."""
    import open_clip
    from open_clip.transformer import _build_causal_mask

    from core.loaders_torch import state_dict

    clip = _meta(
        lambda: open_clip.create_model(
            f"local-dir:{path.parent}", load_weights=False, device="meta"
        )
    )
    _assign(clip, state_dict(path))
    clip.attn_mask = _build_causal_mask(
        clip.attn_mask.shape[0]
    )  # a buffer moved to meta with the model
    return patch_clip(clip)


BUILD = {
    "net": _net,
    "autoencoder": _autoencoder,
    "bigvgan": _bigvgan,
    "synchformer": _synchformer,
    "clip": _clip,
}


def load_component(component: str, model, ctx) -> nn.Module:
    """The pack's own loader (core.loaders_torch.load_component calls it): built and filled on the CPU from the
    memory-mapped checkpoint, then moved and cast to the device as one module."""
    from core.loaders_torch import dtype_of, free

    ctx.check()
    module = BUILD[component](model.files(component)[0], model)
    module = module.to(ctx.device, dtype_of(model.pack.component(component).get("dtype", "bf16")))
    free()
    return module.eval().requires_grad_(False)


def patch_clip(clip_model):
    """features_utils.patch_clip: encode_text returns the last hidden states (normalized), not the pooled one."""

    def new_encode_text(self, text, normalize: bool = False):
        cast_dtype = self.transformer.get_cast_dtype()
        x = self.token_embedding(text).to(cast_dtype)
        x = x + self.positional_embedding.to(cast_dtype)
        x = self.transformer(x, attn_mask=self.attn_mask)
        x = self.ln_final(x)
        return nn.functional.normalize(x, dim=-1) if normalize else x

    clip_model.encode_text = new_encode_text.__get__(clip_model)
    return clip_model


# --- features (features_utils.FeaturesUtils, at batch 1)


def encode_text(clip, text: str) -> torch.Tensor:
    import open_clip

    tokens = open_clip.get_tokenizer(TOKENIZER)([text]).to(clip.attn_mask.device)
    return clip.encode_text(tokens, normalize=True)


def encode_clip(clip, x: torch.Tensor) -> torch.Tensor:
    """x: (1, T, 3, 384, 384) in 0..1."""
    from torchvision.transforms import Normalize

    x = Normalize(mean=CLIP_MEAN, std=CLIP_STD)(x)[0]
    out = [
        clip.encode_image(x[i : i + CLIP_BATCH], normalize=True)
        for i in range(0, len(x), CLIP_BATCH)
    ]
    return torch.cat(out)[None]


def encode_sync(synchformer, x: torch.Tensor) -> torch.Tensor:
    """x: (1, T, 3, 224, 224) normalized to -1..1; segments of 16 frames every 8."""
    t = x.shape[1]
    segments = torch.stack(
        [x[:, i * 8 : i * 8 + 16] for i in range((t - 16) // 8 + 1)], dim=1
    )  # (1, S, 16, ...)
    segments = segments[0][:, None]  # (S, 1, 16, 3, 224, 224)
    out = torch.cat(
        [synchformer(segments[i : i + SYNC_BATCH]) for i in range(0, len(segments), SYNC_BATCH)]
    )
    return out.reshape(1, -1, out.shape[-1])  # (1, S * 8, 768)


# --- frames (eval_utils.load_video, av_utils.read_frames)


@dataclasses.dataclass
class Frames:
    clip: torch.Tensor  # (1, T8, 3, 384, 384)
    sync: torch.Tensor  # (1, T25, 3, 224, 224)
    seconds: float  # load_video's duration_sec: shortened when the frames run out
    first: Image.Image
    last: Image.Image


def read_frames(
    path: Path, width: int, height: int, fps: float, seconds: float, check=lambda: None
) -> Frames:
    """The source's frames up to `seconds` (frame i at i / fps): for each of 8 and 25 fps, slot k takes the first
    frame at or after k / rate (read_frames), each resized as load_video does as it arrives (never all frames)."""
    from torchvision.transforms import v2

    clip_t = v2.Compose([v2.Resize((CLIP_SIZE, CLIP_SIZE), interpolation=v2.InterpolationMode.BICUBIC),
                         v2.ToImage(), v2.ToDtype(torch.float32, scale=True)])  # fmt: skip
    sync_t = v2.Compose([v2.Resize(SYNC_SIZE, interpolation=v2.InterpolationMode.BICUBIC), v2.CenterCrop(SYNC_SIZE),
                         v2.ToImage(), v2.ToDtype(torch.float32, scale=True),
                         v2.Normalize(mean=[0.5, 0.5, 0.5], std=[0.5, 0.5, 0.5])])  # fmt: skip
    size = width * height * 3
    proc = subprocess.Popen([media.FFMPEG or "ffmpeg", "-v", "error", "-nostdin", "-protocol_whitelist", "file",
                             "-i", str(path), "-map", "0:v:0", "-f", "rawvideo", "-pix_fmt", "rgb24", "pipe:1"],
                            stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)  # fmt: skip
    outs: list[list] = [[], []]
    nxt, deltas, first, last = [0.0, 0.0], [1 / CLIP_FPS, 1 / SYNC_FPS], None, None
    try:
        i = 0
        while len(raw := proc.stdout.read(size)) == size:
            t = i / fps
            if t > seconds:
                break
            check()
            frame = np.frombuffer(raw, np.uint8).reshape(height, width, 3)
            if first is None:
                first = Image.fromarray(frame)
            last = frame
            chw = torch.from_numpy(frame.copy()).permute(2, 0, 1)
            for k, tf in enumerate((clip_t, sync_t)):
                while t >= nxt[k]:
                    outs[k].append(tf(chw))
                    nxt[k] += deltas[k]
            i += 1
    finally:
        proc.kill()
        proc.wait()
    if first is None:
        raise RunError("the video has no frames ffmpeg can read")
    clip, sync = torch.stack(outs[0]), torch.stack(outs[1])
    seconds = min(
        seconds, len(clip) / CLIP_FPS, len(sync) / SYNC_FPS
    )  # load_video truncates to its frames
    return Frames(clip[None, : int(CLIP_FPS * seconds)], sync[None, : int(SYNC_FPS * seconds)], seconds, first,
                  Image.fromarray(last))  # fmt: skip


# --- generate (eval_utils.generate at batch 1)


def generate(ctx, comps: dict, text: str, avoid: str, seconds: float, seed: int, guidance: float, steps: int,
             frames: Frames | None = None) -> np.ndarray:  # fmt: skip
    """Float32 mono samples of `seconds` (MMAudio's sequence lengths for that duration)."""
    net, clip = comps["net"], comps["clip"]
    seq = dataclasses.replace(CONFIG_44K, duration=seconds)
    net.update_seq_lengths(seq.latent_seq_len, seq.clip_seq_len, seq.sync_seq_len)
    dev, dtype = net.device, net.latent_mean.dtype
    with torch.inference_mode():
        ctx.progress(1, 1, "encoding")
        if frames is None:
            clip_f, sync_f = net.get_empty_clip_sequence(1), net.get_empty_sync_sequence(1)
        else:
            clip_f = encode_clip(clip, frames.clip.to(dev, dtype))
            sync_f = encode_sync(comps["synchformer"], frames.sync.to(dev, dtype))
        text_f, avoid_f = encode_text(clip, text), encode_text(clip, avoid)
        rng = torch.Generator(device=dev)
        rng.manual_seed(seed)
        x0 = torch.randn(
            1, net.latent_seq_len, net.latent_dim, device=dev, dtype=dtype, generator=rng
        )
        cond = net.preprocess_conditions(clip_f, sync_f, text_f)
        empty = net.get_empty_conditions(1, negative_text_features=avoid_f)
        done = [0]

        def step(t, x):
            ctx.check()
            done[0] += 1
            ctx.progress(done[0], steps, "sampling")
            return net.ode_wrapper(t, x, cond, empty, guidance)

        x1 = FlowMatching(min_sigma=0, inference_mode="euler", num_steps=steps).to_data(step, x0)
        ctx.progress(1, 1, "decoding")
        spec = comps["autoencoder"].decode(net.unnormalize(x1).transpose(1, 2))
        audio = comps["bigvgan"](spec)
    return audio.float().cpu()[0, 0].numpy()


def _audio(samples: np.ndarray) -> dict:
    pcm = (np.clip(samples, -1, 1) * 32767).round().astype("<i2").tobytes()
    return {"wav": pcm, "sample_rate": RATE, "duration_s": len(samples) / RATE}


def _comps(ctx, model, names) -> dict:
    return {c: ctx.component(model, c) for c in names}


# --- sound


def sound(ctx, params: dict, inputs: dict) -> dict:
    text = inputs["text"].strip()
    if not text:
        raise RunError(f"{ctx.node}: describe the sound")
    comps = _comps(ctx, inputs["model"], ("net", "clip", "autoencoder", "bigvgan"))
    samples = generate(ctx, comps, text, params["avoid"].strip(), float(params["seconds"]), inputs["seed"],
                       float(params["guidance"]), int(params["steps"]))  # fmt: skip
    return {"audio": _audio(samples)}


# --- foley: a video in, its sound and the video with it


def _cut(mp4: Path, seconds: float, out: Path) -> None:
    """The picture's first `seconds`, H.264 copied (no sound kept)."""
    media._ffmpeg(["-protocol_whitelist", "file", "-i", str(mp4), "-t", f"{seconds:.3f}", "-map", "0:v:0",
                   "-c", "copy", "-movflags", "+faststart", str(out)])  # fmt: skip


def foley(ctx, params: dict, inputs: dict) -> dict:
    """The source's sound for Length seconds (0: the source's length, less what its sync frames miss at the end),
    and the source cut to that length with the sound muxed in (core.media.mux: H.264 copied, AAC added)."""
    src = params["source"]
    want = float(params["seconds"]) or src["duration_s"]
    if want > src["duration_s"] + 1e-6:
        raise RunError(
            f"{ctx.node}: Length {want:g} s is past the video's end ({src['duration_s']:.2f} s)"
        )
    comps = _comps(ctx, inputs["model"], ("net", "clip", "synchformer", "autoencoder", "bigvgan"))
    with tempfile.TemporaryDirectory(prefix="mmaudio-") as tmp:
        tmp = Path(tmp)
        (tmp / "source.mp4").write_bytes(src["data"])
        frames = read_frames(
            tmp / "source.mp4", src["width"], src["height"], src["fps"], want, ctx.check
        )
        samples = generate(ctx, comps, inputs["text"].strip(), params["avoid"].strip(), frames.seconds,
                           inputs["seed"], float(params["guidance"]), int(params["steps"]), frames)  # fmt: skip
        audio = _audio(samples)
        ctx.progress(1, 1, "muxing")
        if want < src["duration_s"] - 1e-6:
            _cut(tmp / "source.mp4", want, tmp / "cut.mp4")
            picture = (tmp / "cut.mp4").read_bytes()
        else:
            picture = src["data"]
        wav = tmp / "sound.wav"
        with wave.open(str(wav), "wb") as w:
            w.setnchannels(1)
            w.setsampwidth(2)
            w.setframerate(RATE)
            w.writeframes(audio["wav"])
        (tmp / "out.mp4").write_bytes(media.mux(picture, wav.read_bytes()))
        info = media.probe(tmp / "out.mp4")
        num, den = (int(x) for x in info["stream"]["frame_rate"].split("/"))
        video = {"mp4": (tmp / "out.mp4").read_bytes(), "poster": frames.first, "last": frames.last,
                 "frames": info["frames"], "fps": num / den, "has_audio": True, "stream": info["stream"]}  # fmt: skip
    return {"audio": audio, "video": video}
