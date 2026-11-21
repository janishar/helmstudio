// wav.h — 16-bit PCM mono WAV, the one encoder of the C++ tree (zonos3.tts.audio.pcm16_samples): per sample
// NaN -> 0, clip to [-1, 1], x * 32767 rounded half to even.
#pragma once

#include <cstdint>
#include <vector>

std::vector<uint8_t> wav_pcm16_bytes(const std::vector<float> & audio, int sample_rate);
// Writes the file; prints "zac: wrote <path>" (or "zac: cannot write <path>") to stderr.
bool wav_pcm16_write(const char * path, const std::vector<float> & audio, int sample_rate);
