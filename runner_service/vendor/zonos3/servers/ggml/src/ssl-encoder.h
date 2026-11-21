// Native sixteen-layer Wav2Vec2-BERT semantic teacher and SeamlessM4T frontend.
#pragma once
#include "ggml.h"
#include "ggml-backend.h"
#include "resample-16k.h"
#include <array>
#include <cstdio>
#include <cstdint>
#include <map>
#include <mutex>
#include <string>
#include <vector>

struct ssl_graph_cache;
// BF16 is the frozen CUDA-autocast recipe (F32 LayerNorm/softmax/residual states);
// F32 is an explicitly selected diagnostic without autocast.
enum class ssl_dtype { f32, bf16 };
struct ssl_model {
    ggml_context * ctx_w = nullptr;
    ggml_backend_t backend = nullptr;
    ggml_backend_buffer_type_t buft = nullptr;
    ggml_backend_buffer_t buf_w = nullptr;
    bool is_gpu = false;
    std::map<std::string, ggml_tensor *> t;
    int sample_rate = 16000, max_seconds = 60, n_layer = 16, n_embd = 1024,
        n_head = 16, n_ff = 4096, rel_left = 64, rel_right = 8,
        conv_kernel = 31, n_mels = 80, frame_length = 400, hop_length = 160,
        fft_length = 512, stride = 2;
    float eps = 1e-5f, preemph = 0.97f, mel_floor = 1.192092955078125e-07f;
    std::vector<double> mel_filters, window;
    resample_16k resample;
    mutable std::mutex graph_mutex;
    mutable ssl_graph_cache * graph_cache = nullptr;
    ggml_tensor * get(const std::string & n) const {
        auto it = t.find(n);
        if (it == t.end()) { fprintf(stderr, "ssl: missing tensor '%s'\n", n.c_str()); return nullptr; }
        return it->second;
    }
};

bool ssl_load(ssl_model & m, const char * path, bool use_gpu);
void ssl_free(ssl_model & m);
// Features C-order [T,160]; outputs C-order [T,1024], including padded positions.
// Null mask means all valid; otherwise mask[T] is 0/1, matching the HF attention_mask.
bool ssl_encode_features(const ssl_model & m, const float * features, int T, std::vector<float> & out,
                         ssl_dtype dtype = ssl_dtype::bf16);
bool ssl_encode_features(const ssl_model & m, const float * features, int T,
                         const int32_t * mask, std::vector<float> & out, ssl_dtype dtype = ssl_dtype::bf16);
bool ssl_encode_pcm(const ssl_model & m, const float * audio, int n, int sample_rate,
                    std::vector<float> & out, int & frames, ssl_dtype dtype = ssl_dtype::bf16);
// Full externally padded lane is processed, with no valid-audio-length trimming.
bool ssl_features_pcm(const ssl_model & m, const float * audio, int n, int sample_rate,
                      std::vector<float> & features, int & frames, std::vector<int32_t> & mask);
// Diagnostic forward: states[0] is masked feature projection; states[1..16] are
// F32 final-LayerNorm outputs in the frozen BF16 CUDA-autocast recipe.
// Optional projection_states also captures first-layer FFN/attention/convolution
// boundaries as [T,channels]; attention matrices flatten [head,key] per query.
bool ssl_encode_features_trace(const ssl_model & m, const float * features, int T,
                               const int32_t * mask, std::vector<std::vector<float>> & states,
                               ssl_dtype dtype = ssl_dtype::bf16,
                               std::map<std::string,std::vector<float>> * projection_states = nullptr);
