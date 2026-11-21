#pragma once
#include <filesystem>
#include <string>
#include <vector>

// The ffmpeg used for audio: $ZONOS3_FFMPEG, one next to the executable (dist layout / macOS Resources), else
// "ffmpeg" on PATH.
std::filesystem::path audio_ffmpeg_path();

// Decode mono PCM with the configured ffmpeg; failures return an empty vector.
std::vector<float> audio_decode_file(const char * path, int sample_rate = 44100, int max_seconds = 60);
// Mono 44100 Hz reference audio: a float32 [n] / [1,n] .npy as stored, anything else through ffmpeg. False with `err`
// when it cannot be read or holds no samples or more than max_seconds of them.
bool audio_load_reference(const std::string & path, int max_seconds, std::vector<float> & out, std::string & err);
