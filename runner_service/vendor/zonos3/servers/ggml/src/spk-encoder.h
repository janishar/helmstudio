// Native ZONOS3 Mini speaker encoder: Kaldi fbank + MECT-B2 (ownmect_s2) + ASTP.
// Input is mono PCM, output is the raw 192-d float32 speaker vector (NOT L2-normalized).
#pragma once
#include "ggml.h"
#include "ggml-alloc.h"
#include "ggml-backend.h"
#include "resample-16k.h"
#include <cstdint>
#include <functional>
#include <map>
#include <memory>
#include <mutex>
#include <string>
#include <vector>

struct spk_fft; // pocketfft_r<float>(512) plan, built once at load and read-only afterwards.

struct spk_model {
    ggml_context * ctx_w = nullptr;
    ggml_backend_buffer_t buf_w = nullptr;
    ggml_backend_t backend = nullptr;      // primary (GPU when requested and available)
    ggml_backend_t backend_cpu = nullptr;  // always present; sched fallback
    ggml_backend_sched_t sched = nullptr;
    bool is_gpu = false;
    std::map<std::string, ggml_tensor *> t;
    int sample_rate = 16000, max_seconds = 60, embedding_length = 192, n_mels = 80,
        frame_length = 400, frame_shift = 160, n_fft = 512, n_heads = 4, n_experts = 4, pos_kernel = 113;
    float preemph = 0.97f, waveform_scale = 32768.0f, log_floor = 1.1920928955078125e-07f,
          ln_eps = 1e-6f, pool_var_floor = 1e-7f;
    std::vector<float> window;      // [frame_length]
    std::vector<float> mel_banks;   // [n_mels][n_fft/2+1], last column zero
    resample_16k resample;          // torchaudio Resample(44100, 16000) polyphase kernel
    std::shared_ptr<const spk_fft> fft;
    mutable std::mutex mutex;       // the scheduler is stateful: one embedding at a time
    ggml_tensor * get(const std::string & n) const {
        auto it = t.find(n);
        if (it == t.end()) { fprintf(stderr, "spk: missing tensor '%s'\n", n.c_str()); return nullptr; }
        return it->second;
    }
};

bool spk_load(spk_model & m, const char * path, bool use_gpu);
void spk_free(spk_model & m);

// Diagnostic tensors, row-major [rows, cols]: fbank [T,80] (before CMN), fbank_cmn, feat_1..feat_4
// [T,2560], pooled [1,5120], embedding [1,192]. Returning false aborts with failure.
using spk_dump_fn = std::function<bool(const char * name, const float * data, int rows, int cols)>;

// audio: mono float32 at 44100 or 16000 Hz (the first 60 s are used). Returns 192 floats, unnormalized.
bool spk_embed(const spk_model & m, const float * audio, int64_t n, int sample_rate,
               std::vector<float> & embedding, const spk_dump_fn & dump = {});
