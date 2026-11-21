"""Loads an official Qwen3-TTS CustomVoice or VoiceDesign checkpoint and turns text into speech."""

import dataclasses
import json
import logging
import re
import time
from collections.abc import Callable, Iterable, Iterator
from contextlib import contextmanager
from pathlib import Path

import numpy as np
import torch
from safetensors.torch import load_file
from torch import nn
from transformers import Qwen2Tokenizer

from .codec import CodecConfig, CodecDecoder
from .talker import SamplingSettings, Talker

log = logging.getLogger(__name__)

FRAMES_PER_SECOND = 12.5
FIRST_PIECE_FRAMES = 4  # the first 0.32 s of speech is sent as soon as it is decoded,
PIECE_FRAMES = 10  # then 0.8 s at a time
CONTEXT_FRAMES = 25  # frames decoded again before each piece, so the pieces join up smoothly


class Synthesizer:
    """Text in, 24 kHz mono speech out: in one of a CustomVoice checkpoint's built-in voices, or in the voice a
    VoiceDesign checkpoint makes up from a description (the instruction; it has no built-in voices)."""

    def __init__(self, talker: Talker, codec: CodecDecoder, tokenizer, sampling: SamplingSettings, sample_rate: int):
        self.talker = talker
        self.codec = codec
        self.tokenizer = tokenizer
        self.sampling = sampling
        self.live_temperature = sampling.temperature  # the checkpoint's: see stream_live()
        self.sample_rate = sample_rate
        talker_config = talker.config["talker_config"]
        self._speaker_ids: dict[str, int] = talker_config["spk_id"]
        self._speaker_dialects: dict[str, str | bool] = talker_config.get("spk_is_dialect", {})
        self._language_ids: dict[str, int] = talker_config["codec_language_id"]

    @property
    def speakers(self) -> list[str]:
        """Built-in voices, e.g. ryan, aiden, vivian, serena, ono_anna, sohee, uncle_fu, dylan, eric; none for
        VoiceDesign."""
        return sorted(self._speaker_ids)

    @property
    def languages(self) -> list[str]:
        return ["auto", *sorted(name for name in self._language_ids if "dialect" not in name)]

    @classmethod
    def load(
        cls, weights: str | Path, device: torch.device, dtype: torch.dtype = torch.bfloat16, compile: bool | None = None
    ) -> "Synthesizer":
        """Load `weights`, a local copy of e.g. Qwen/Qwen3-TTS-12Hz-1.7B-CustomVoice or -VoiceDesign (with its
        speech_tokenizer/).

        compile (by default on for GPUs) runs the code predictor through torch.compile; the first sentence, spoken
        here while loading, then takes some seconds longer.
        """
        weights = Path(weights).expanduser()
        config = json.loads((weights / "config.json").read_text())
        if config.get("tts_model_type") not in ("custom_voice", "voice_design"):
            raise ValueError(f"{weights.name} is not a CustomVoice or VoiceDesign checkpoint")
        codec_config = json.loads((weights / "speech_tokenizer" / "config.json").read_text())

        started = time.perf_counter()
        # Build the modules on the device in the target precision; the checkpoint then replaces their random values.
        with torch.device(device), default_dtype(dtype):
            talker = Talker(config).eval()
            codec = CodecDecoder(CodecConfig.from_dict(codec_config["decoder_config"])).eval()
        load_weights(talker, weights / "model.safetensors", device, rename=Talker.rename_key)
        load_weights(
            codec,
            weights / "speech_tokenizer" / "model.safetensors",
            device,
            rename=lambda key: key.removeprefix("decoder."),
            keep=lambda key: key.startswith("decoder."),  # the tokenizer's encoder is only for voice cloning
        )
        sampling = SamplingSettings.from_dict(json.loads((weights / "generation_config.json").read_text()))
        log.info("Loaded %s in %.1fs.", weights.name, time.perf_counter() - started)
        tokenizer = Qwen2Tokenizer.from_pretrained(weights)
        synthesizer = cls(talker, codec, tokenizer, sampling, codec_config["output_sample_rate"])
        synthesizer.warm_up(compile=device.type != "cpu" if compile is None else compile)
        return synthesizer

    def warm_up(self, compile: bool) -> None:
        """Switch on the fast code predictor and speak a short sentence, so that compiling happens now."""
        started = time.perf_counter()
        self.talker.enable_fast_decoding(compile)
        try:
            if self.speakers:
                self.speak("Hello there.", speaker=self.speakers[0])
            else:
                self.speak("Hello there.", instruct="A calm, clear voice.")
        except Exception as error:  # compiling only speeds things up: without it, speech is the same
            if not compile:
                raise
            log.warning("torch.compile did not work here (%s); speaking without it.", error)
            self.talker.enable_fast_decoding(compile=False)
            compile = False
        log.info("Warmed up in %.1fs%s.", time.perf_counter() - started, " (compiled)" if compile else "")

    def speak(self, text: str, speaker: str = "ryan", language: str = "auto", instruct: str = "") -> np.ndarray:
        """Synthesize `text` as float32 samples at `sample_rate`; see stream() for the arguments."""
        pieces = list(self.stream(text, speaker, language, instruct))
        return np.concatenate(pieces) if pieces else np.zeros(0, dtype=np.float32)

    @torch.inference_mode()
    def stream(
        self,
        text: str,
        speaker: str = "ryan",
        language: str = "auto",
        instruct: str = "",
        should_stop: Callable[[], bool] = lambda: False,
    ) -> Iterator[np.ndarray]:
        """Synthesize `text` and yield the speech in pieces (float32 at `sample_rate`) while it is being made:
        the first after 0.32 s of speech, then every 0.8 s, so playback can start long before the end.

        language is "auto" or one of `languages`. With CustomVoice, speaker is one of `speakers` and instruct
        optionally says how to speak ("Very happy."); with VoiceDesign, speaker is not used and instruct describes the
        voice ("A deep, calm male voice with a slight rasp").
        should_stop is checked after every frame; when it returns True the speech ends there.
        """
        text_ids = self._text_ids(text)
        prompt, pad = self._prompt(text_ids, speaker, language, instruct)
        # A text token takes about 0.3 s to say; allow 1.2 s each, enough for slow speech, to stop a runaway voice.
        max_frames = int(FRAMES_PER_SECOND * (5 + 1.2 * len(text_ids)))
        self._seed()
        yield from self._pieces(self.talker.frames(prompt, pad, self.sampling, max_frames), should_stop)

    @torch.inference_mode()
    def stream_live(
        self,
        chunks: Iterable[str],
        speaker: str = "ryan",
        language: str = "auto",
        instruct: str = "",
        should_stop: Callable[[], bool] = lambda: False,
    ) -> Iterator[np.ndarray]:
        """Like stream(), for text that is still being written: `chunks` gives it piece by piece, and may wait for
        the next piece. In the checkpoint's streaming mode the talker reads one text token per frame it writes, so
        speech starts after the first word and the whole text is one take; having read all the text written so far,
        the talker waits for more.

        The talker samples at the checkpoint's temperature at least (the code predictor at the one set): it reads the
        text far faster than it speaks, and speaks the rest from text it read long before. Colder, it loses its place
        there: 30 s into a whole reply at 0.1 it fell silent and murmured on until its cap, 4 minutes.
        """
        tokens = self._tokens_as_written(chunks)
        first = next(tokens, None)
        if first is None:
            return
        text_ids = self._text_ids("")
        text_ids[3:3] = [first]  # role header, first token, tail
        prompt, pad = self._prompt(text_ids, speaker, language, instruct, streaming=True)
        read = 1

        def counted(tokens: Iterator[int]) -> Iterator[int]:
            nonlocal read
            for token in tokens:
                read += 1
                yield token

        self._seed()
        sampling = dataclasses.replace(self.sampling, temperature=max(self.sampling.temperature, self.live_temperature))
        frames = self.talker.frames(
            prompt, pad, sampling, int(FRAMES_PER_SECOND * 3600), text=self.talker.text_track(counted(tokens))
        )
        # The runaway guard of stream(), against the text read so far, tighter: a runaway take here holds the GPU for
        # minutes. 0.8 s a token is still twice the pace measured on a whole reply (0.38 s).
        yield from self._pieces(frames, should_stop, lambda count: count > FRAMES_PER_SECOND * (5 + 0.8 * read))

    def _pieces(
        self,
        frames: Iterable[torch.Tensor],
        should_stop: Callable[[], bool],
        too_long: Callable[[int], bool] = lambda count: False,
    ) -> Iterator[np.ndarray]:
        """Decode frames into speech as they are written: a piece after the first 0.32 s, then every 0.8 s."""
        written, sent = [], 0
        for frame in frames:
            if should_stop() or too_long(len(written)):
                return
            written.append(frame)
            if len(written) - sent >= (FIRST_PIECE_FRAMES if sent == 0 else PIECE_FRAMES):
                yield self._decode(written, sent)
                sent = len(written)
        if len(written) > sent:
            yield self._decode(written, sent)

    def _seed(self) -> None:
        """Every speech starts from the same random state, so a turn's voice is not a new draw: with VoiceDesign
        the voice is made up anew from the description each time, and sampled differently it drifts between turns.
        ponytail: the global generator; fine while one speech is made at a time per process."""
        if self.sampling.seed is not None:
            torch.manual_seed(self.sampling.seed)

    def _text_ids(self, text: str) -> list[int]:
        return self.tokenizer(f"<|im_start|>assistant\n{text}<|im_end|>\n<|im_start|>assistant\n")["input_ids"]

    def _tokens_as_written(self, chunks: Iterable[str]) -> Iterator[int]:
        """The tokens of text that is still being written, each as soon as it is settled: once it ends before the
        text's last run of whitespace. Later text can still change the tokens after that: half a word tokenizes
        differently from the whole, and a full stop takes in the line breaks that follow it (".\\n\\n")."""
        text, sent = "", 0
        for chunk in chunks:
            text += chunk
            body = text.lstrip()
            last_space = re.search(r"\s+\S*$", body)
            if last_space is None:
                continue
            encoded = self.tokenizer(body, return_offsets_mapping=True)
            settled = sum(1 for _, end in encoded["offset_mapping"] if end <= last_space.start())
            yield from encoded["input_ids"][sent:settled]
            sent = max(sent, settled)
        yield from self.tokenizer(text.strip())["input_ids"][sent:]

    def _prompt(self, text_ids: list[int], speaker: str, language: str, instruct: str, streaming: bool = False):
        speaker, language = speaker.lower(), language.lower()
        if self._speaker_ids and speaker not in self._speaker_ids:
            raise ValueError(f"unknown voice {speaker!r}; choose one of {', '.join(self.speakers)}")
        if language not in self.languages:
            raise ValueError(f"unknown language {language!r}; choose one of {', '.join(self.languages)}")
        language_id = None if language == "auto" else self._language_ids[language]
        dialect = self._speaker_dialects.get(speaker)
        if dialect and language in ("auto", "chinese"):  # the Beijing and Sichuan voices speak their dialect
            language_id = self._language_ids[dialect]

        # VoiceDesign has no voices: the prompt has no speaker, and the instruction describes the voice.
        speaker_id = self._speaker_ids.get(speaker)
        instruct_ids = self.tokenizer(f"<|im_start|>user\n{instruct}<|im_end|>\n")["input_ids"] if instruct else []
        return self.talker.build_prompt(text_ids, speaker_id, language_id, instruct_ids, streaming)

    def _decode(self, frames: list[torch.Tensor], start: int) -> np.ndarray:
        """The audio of frames[start:]. The codec is causal: decoding a few frames before `start` too gives it the
        context it had when decoding the whole sentence at once."""
        context = min(CONTEXT_FRAMES, start)
        codes = torch.stack(frames[start - context :]).T[None]  # (1, 16, frames)
        waveform = self.codec(codes)[0, 0, context * self.codec.config.samples_per_frame :]
        return waveform.float().cpu().numpy()


def load_weights(
    module: nn.Module,
    path: Path,
    device: torch.device,
    rename: Callable[[str], str],
    keep: Callable[[str], bool] = lambda key: True,
) -> None:
    """Copy a safetensors checkpoint into `module`, failing loudly if any weight is missing or left over."""
    state = {rename(key): tensor for key, tensor in load_file(path, device=str(device)).items() if keep(key)}
    missing, unexpected = module.load_state_dict(state, strict=False)
    missing = [key for key in missing if not key.endswith("embed_tokens.weight")]  # Qwen3Model's unused token table
    if missing or unexpected:
        raise RuntimeError(
            f"{path}: weights do not match the model (missing {missing[:3]}, unexpected {unexpected[:3]})"
        )


@contextmanager
def default_dtype(dtype: torch.dtype):
    previous = torch.get_default_dtype()
    torch.set_default_dtype(dtype)
    try:
        yield
    finally:
        torch.set_default_dtype(previous)
