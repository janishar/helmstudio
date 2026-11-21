"""Text-to-speech with the official Qwen3-TTS CustomVoice weights (e.g. Qwen/Qwen3-TTS-12Hz-1.7B-CustomVoice).

Modules:
    talker       the talker and code predictor: text and a voice in, 16 codes per 80 ms frame out
    codec        the 12 Hz speech tokenizer's decoder: codes in, a 24 kHz waveform out
    synthesizer  loads a checkpoint and speaks text in one of its built-in voices

Library use:
    import torch
    from qwen3_tts import Synthesizer

    tts = Synthesizer.load("/path/to/Qwen3-TTS-12Hz-1.7B-CustomVoice", torch.device("mps"))
    samples = tts.speak("Hello there!", speaker="ryan")  # float32 at tts.sample_rate (24 kHz)

Transformers has no Qwen3-TTS model, and the official qwen-tts package pins an older Transformers than qwen38
needs, so this package reimplements the inference path on top of Transformers' Qwen3Model.
"""

from .synthesizer import Synthesizer

__all__ = ["Synthesizer"]
