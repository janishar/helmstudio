"""Qwen3-TTS's language models: text and a voice in, 16 codes per 80 ms frame of speech out.

The talker (a 28-layer Qwen3 decoder) writes the first code of each frame; the code predictor (a 5-layer Qwen3
decoder) then writes the other 15, one after another, from the talker's state. The next frame's input is the sum of
the 16 codes' embeddings. Both decoders are Transformers' Qwen3Model, fed with embeddings built here.

Follows the official qwen-tts package (Apache-2.0) for the CustomVoice models, in both of its modes: "non-streaming",
where the whole text is in the prompt before the first frame, and streaming, where the talker reads one text token
per frame it writes, so that it can speak text that is still being written.
"""

from collections.abc import Iterable, Iterator
from dataclasses import dataclass

import torch
from torch import nn
from transformers import Qwen3Config, Qwen3Model
from transformers.cache_utils import DynamicCache

from .decoder import CachedDecoder


@dataclass(frozen=True)
class SamplingSettings:
    """generation_config.json of the checkpoint; the talker and the code predictor sample separately."""

    temperature: float = 0.9
    top_k: int = 50
    repetition_penalty: float = 1.05  # talker only
    code_temperature: float = 0.9
    code_top_k: int = 50
    seed: int | None = None  # the same random draws for every speech: a designed voice stays the same voice

    @classmethod
    def from_dict(cls, values: dict) -> "SamplingSettings":
        return cls(
            temperature=values.get("temperature", cls.temperature),
            top_k=values.get("top_k", cls.top_k),
            repetition_penalty=values.get("repetition_penalty", cls.repetition_penalty),
            code_temperature=values.get("subtalker_temperature", cls.code_temperature),
            code_top_k=values.get("subtalker_top_k", cls.code_top_k),
        )


class Talker(nn.Module):
    """The talker and its code predictor, named so the checkpoint's `talker.*` weights load after `rename_key`."""

    def __init__(self, config: dict):
        super().__init__()
        self.config = config
        talker = config["talker_config"]
        self.model = Qwen3Model(qwen3_config(talker))
        self.codec_embedding = nn.Embedding(talker["vocab_size"], talker["hidden_size"])
        self.text_embedding = nn.Embedding(talker["text_vocab_size"], talker["text_hidden_size"])
        self.text_projection = TextProjection(talker["text_hidden_size"], talker["hidden_size"])
        self.codec_head = nn.Linear(talker["hidden_size"], talker["vocab_size"], bias=False)
        self.code_predictor = CodePredictor(talker)

    def enable_fast_decoding(self, compile: bool) -> None:
        """Run the code predictor through a fixed-size cache, compiled with torch.compile if `compile` (see
        decoder.py). The talker keeps Transformers' cache: it takes one step per frame, not 15, and compiled that
        step was no faster while the chat model shares the GPU, which is when speed matters."""
        self.code_predictor.decoder = CachedDecoder(
            self.code_predictor.model, len(self.code_predictor.lm_head) + 1, compile
        )

    @staticmethod
    def rename_key(key: str) -> str:
        """Checkpoint name -> name in this module (the embeddings sit next to the decoders here, not inside)."""
        key = key.removeprefix("talker.")
        for old, new in (
            ("model.codec_embedding.", "codec_embedding."),
            ("model.text_embedding.", "text_embedding."),
            ("code_predictor.model.codec_embedding.", "code_predictor.codec_embedding."),
        ):
            if key.startswith(old):
                return new + key[len(old) :]
        return key

    def text(self, token_ids: list[int]) -> torch.Tensor:
        """Text tokens as talker inputs: (1, len, hidden)."""
        ids = torch.tensor([token_ids], device=self.codec_head.weight.device)
        return self.text_projection(self.text_embedding(ids))

    def codec(self, token_ids: list[int]) -> torch.Tensor:
        """Codec tokens (codes, tags, speakers, languages) as talker inputs: (1, len, hidden)."""
        return self.codec_embedding(torch.tensor([token_ids], device=self.codec_head.weight.device))

    def build_prompt(
        self,
        text_ids: list[int],
        speaker_id: int | None,
        language_id: int | None,
        instruct_ids: list[int],
        streaming: bool = False,
    ):
        """The prompt the talker reads before writing the first frame.

        text_ids are the tokens of "<|im_start|>assistant\\n{text}<|im_end|>\\n<|im_start|>assistant\\n": a
        3-token role header, the text, and a 5-token tail that the prompt leaves out. The text track and the codec
        track are added position by position:

            instruction     role header   pad ... pad  bos     text ... eos         pad
                                          think tags   speaker  pad  ... pad        codec bos

        In streaming mode (qwen-tts's non_streaming_mode=False) the prompt ends with the text's first token, on the
        codec bos; frames() reads the rest, one token per frame (see text_track()).
        """
        talker = self.config["talker_config"]
        special = self.config
        tts_bos, tts_eos, tts_pad = self.text(
            [special["tts_bos_token_id"], special["tts_eos_token_id"], special["tts_pad_token_id"]]
        ).split(1, dim=1)

        if language_id is None:  # let the model pick the language
            tags = [talker["codec_nothink_id"], talker["codec_think_bos_id"], talker["codec_think_eos_id"]]
        else:
            tags = [talker["codec_think_id"], talker["codec_think_bos_id"], language_id, talker["codec_think_eos_id"]]
        codec_track = tags + ([speaker_id] if speaker_id is not None else []) + [talker["codec_pad_id"]]

        role, body = text_ids[:3], text_ids[3:-5]
        parts = []
        if instruct_ids:
            parts.append(self.text(instruct_ids))
        parts.append(self.text(role))
        parts.append(
            torch.cat([tts_pad.expand(-1, len(codec_track) - 1, -1), tts_bos], dim=1) + self.codec(codec_track)
        )
        if streaming:
            parts.append(self.text(body[:1]) + self.codec([talker["codec_bos_id"]]))
        else:
            parts.append(
                torch.cat([self.text(body), tts_eos], dim=1) + self.codec([talker["codec_pad_id"]] * (len(body) + 1))
            )
            parts.append(tts_pad + self.codec([talker["codec_bos_id"]]))
        return torch.cat(parts, dim=1), tts_pad

    def text_track(self, token_ids: Iterable[int]) -> Iterator[torch.Tensor]:
        """The rest of the text in streaming mode, as frames() reads it: one token per frame, then the text's end.
        token_ids can be a generator that waits for text still being written: the talker then waits with it."""
        for token in token_ids:
            yield self.text([token])
        yield self.text([self.config["tts_eos_token_id"]])

    @torch.inference_mode()
    def generate(
        self, prompt: torch.Tensor, pad: torch.Tensor, sampling: SamplingSettings, max_frames: int
    ) -> torch.Tensor:
        """Write frames until the end-of-speech code: returns (frames, 16) codes."""
        frames = list(self.frames(prompt, pad, sampling, max_frames))
        groups = self.config["talker_config"]["num_code_groups"]
        return torch.stack(frames) if frames else torch.zeros((0, groups), dtype=torch.long)

    @torch.inference_mode()
    def frames(
        self,
        prompt: torch.Tensor,
        pad: torch.Tensor,
        sampling: SamplingSettings,
        max_frames: int,
        text: Iterable[torch.Tensor] = (),
    ) -> Iterator[torch.Tensor]:
        """Write frames until the end-of-speech code, yielding each frame's 16 codes as soon as it is written.

        In streaming mode `text` is the rest of the text (text_track()): each frame's input takes its next token,
        and the padding once it has run out.
        """
        text = iter(text)
        talker = self.config["talker_config"]
        eos = talker["codec_eos_token_id"]
        # Only real codes (0-2047) and end-of-speech may be written; the ids above are tags, speakers, languages.
        allowed = torch.zeros(talker["vocab_size"], dtype=torch.bool, device=prompt.device)
        allowed[: talker["vocab_size"] - 1024] = True
        allowed[eos] = True

        cache = DynamicCache()

        def advance(embeddings: torch.Tensor) -> torch.Tensor:
            return self.model(inputs_embeds=embeddings, past_key_values=cache, use_cache=True).last_hidden_state

        hidden = advance(prompt)[:, -1:]

        first_codes = []
        for step in range(max_frames):
            logits = self.codec_head(hidden[:, -1])
            if step < 2:  # speak at least two frames
                logits[:, eos] = float("-inf")
            first = sample(
                logits.masked_fill(~allowed, float("-inf")),
                sampling.temperature,
                sampling.top_k,
                previous=first_codes,
                repetition_penalty=sampling.repetition_penalty,
            )
            if first.item() == eos:
                return
            first_codes.append(first.item())
            codes = [first] + self.code_predictor.generate(hidden, self.codec_embedding(first[:, None]), sampling)
            yield torch.cat(codes)

            # The next input: all 16 codes of this frame, plus the text track's next token, or its padding.
            embedding = self.codec_embedding(first[:, None]) + next(text, pad)
            for index, code in enumerate(codes[1:]):
                embedding = embedding + self.code_predictor.codec_embedding[index](code[:, None])
            hidden = advance(embedding)


class CodePredictor(nn.Module):
    def __init__(self, talker: dict):
        super().__init__()
        predictor = talker["code_predictor_config"]
        groups = talker["num_code_groups"] - 1
        self.model = Qwen3Model(qwen3_config(predictor))
        self.codec_embedding = nn.ModuleList(
            nn.Embedding(predictor["vocab_size"], talker["hidden_size"]) for _ in range(groups)
        )
        self.lm_head = nn.ModuleList(
            nn.Linear(predictor["hidden_size"], predictor["vocab_size"], bias=False) for _ in range(groups)
        )
        # The 0.6B checkpoints' talker is as wide as the code predictor, so they have no projection between the two.
        self.small_to_mtp_projection = (
            nn.Linear(talker["hidden_size"], predictor["hidden_size"])
            if talker["hidden_size"] != predictor["hidden_size"]
            else nn.Identity()
        )
        self.decoder: CachedDecoder | None = None  # set by Talker.enable_fast_decoding

    def generate(
        self, talker_hidden: torch.Tensor, first_code: torch.Tensor, sampling: SamplingSettings
    ) -> list[torch.Tensor]:
        """Codes 1-15 of a frame, from the talker's last state and the embedding of code 0."""
        inputs = self.small_to_mtp_projection(torch.cat([talker_hidden, first_code], dim=1))
        if self.decoder is not None:
            self.decoder.reset()
            self.decoder.step(inputs[:, :1])  # one position per call keeps every compiled step the same shape
            hidden = self.decoder.step(inputs[:, 1:])
            advance = self.decoder.step
        else:
            cache = DynamicCache()

            def advance(embeddings: torch.Tensor) -> torch.Tensor:
                return self.model(inputs_embeds=embeddings, past_key_values=cache, use_cache=True).last_hidden_state

            hidden = advance(inputs)
        codes = []
        for index, head in enumerate(self.lm_head):
            code = sample(head(hidden[:, -1]), sampling.code_temperature, sampling.code_top_k)
            codes.append(code)
            if index + 1 < len(self.lm_head):
                hidden = advance(self.small_to_mtp_projection(self.codec_embedding[index](code[:, None])))
        return codes


class TextProjection(nn.Module):
    """Maps text-token embeddings into the talker's space: Linear -> SiLU -> Linear."""

    def __init__(self, text_size: int, hidden_size: int):
        super().__init__()
        self.linear_fc1 = nn.Linear(text_size, text_size)
        self.linear_fc2 = nn.Linear(text_size, hidden_size)

    def forward(self, embeddings: torch.Tensor) -> torch.Tensor:
        return self.linear_fc2(nn.functional.silu(self.linear_fc1(embeddings)))


def qwen3_config(values: dict) -> Qwen3Config:
    """A Transformers Qwen3Config for one of the decoders. Inputs are always embeddings, so the token
    embedding table of Qwen3Model is unused (vocab_size 1). The talker's 3-axis RoPE gets the same position on
    every axis for speech, which is exactly ordinary RoPE."""
    return Qwen3Config(
        vocab_size=1,
        hidden_size=values["hidden_size"],
        intermediate_size=values["intermediate_size"],
        num_hidden_layers=values["num_hidden_layers"],
        num_attention_heads=values["num_attention_heads"],
        num_key_value_heads=values["num_key_value_heads"],
        head_dim=values["head_dim"],
        rms_norm_eps=values["rms_norm_eps"],
        max_position_embeddings=values.get("max_position_embeddings", 32768),
        rope_parameters={"rope_type": "default", "rope_theta": values["rope_theta"]},
        attention_bias=values.get("attention_bias", False),
        tie_word_embeddings=False,
    )


def sample(
    logits: torch.Tensor,
    temperature: float,
    top_k: int,
    previous: list[int] = (),
    repetition_penalty: float = 1.0,
) -> torch.Tensor:
    """Pick one token per row: repetition penalty on `previous` tokens, then temperature and top-k sampling."""
    logits = logits.float()
    if previous and repetition_penalty != 1.0:
        seen = torch.tensor(sorted(set(previous)), device=logits.device)[None, :]
        scores = logits.gather(-1, seen)
        logits = logits.scatter(
            -1, seen, torch.where(scores < 0, scores * repetition_penalty, scores / repetition_penalty)
        )
    logits = logits / temperature
    kth_best = torch.topk(logits, top_k, dim=-1).values[:, -1:]
    logits = logits.masked_fill(logits < kth_best, float("-inf"))
    return torch.multinomial(torch.softmax(logits, dim=-1), 1)[:, 0]
