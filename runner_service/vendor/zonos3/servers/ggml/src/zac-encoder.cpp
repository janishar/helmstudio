// Frozen acoustic + native SSL semantic dual encoding. No Python/runtime model bridge.
#include "zac-encoder.h"
#include "ssl-encoder.h"
#include "compat.h"
#include "backend-device.h"
#include "ggml-alloc.h"
#include "ggml-cpu.h"
#include "gguf.h"
#include <algorithm>
#include <cmath>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <limits>
#include <numeric>

namespace {
constexpr const char * ARCH = "mossdual_encoder";
constexpr int LANE = 300, CONTEXT = 150, HOP = 2940, INTERNAL_HOP = 2944;

struct stage_inputs {
    ggml_context * ctx = nullptr;
    ggml_backend_buffer_t buffer = nullptr;
    ~stage_inputs() {
        if (buffer) ggml_backend_buffer_free(buffer);
        if (ctx) ggml_free(ctx);
    }
};

// Each operation reuses two device activation buffers and the graph allocator. Only
// final acoustic/semantic outputs (or explicitly requested diagnostics) cross to host.
class encoder_graph {
    const zac_model & m;
    ggml_backend_buffer_t buffers[2] = {nullptr, nullptr};
    int current = 0, channels = 0, frames = 0;
    size_t capacity = 0;
public:
    using builder = std::function<ggml_tensor *(ggml_context *, ggml_cgraph *, ggml_tensor *)>;
    explicit encoder_graph(const zac_model & model) : m(model) {}
    ~encoder_graph() { for (auto b : buffers) if (b) ggml_backend_buffer_free(b); }
    bool init(int C, int T, const float * values, size_t max_elements) {
        capacity = max_elements * sizeof(float) + ggml_backend_get_alignment(m.backend);
        for (auto & b : buffers) { b = ggml_backend_alloc_buffer(m.backend, capacity); if (!b) return false; }
        return reset(C, T, values);
    }
    bool reset(int C, int T, const float * values) {
        if ((size_t) C * T * sizeof(float) > capacity) return false;
        channels = C; frames = T;
        auto * ctx = ggml_init({4 * ggml_tensor_overhead(), nullptr, true});
        auto * x = ggml_new_tensor_2d(ctx, GGML_TYPE_F32, C, T);
        bool ok = ggml_backend_tensor_alloc(buffers[current], x, ggml_backend_buffer_get_base(buffers[current])) == GGML_STATUS_SUCCESS;
        if (ok) ggml_backend_tensor_set(x, values, 0, (size_t) C * T * sizeof(float));
        ggml_free(ctx);
        return ok;
    }
    bool run(const builder & build, const zac_encoder_dump * dump = nullptr,
             const std::string * prefix = nullptr) {
        auto * ctx = ggml_init({32 * 1024 * 1024, nullptr, true});
        if (!ctx) return false;
        auto * graph = ggml_new_graph_custom(ctx, 16384, false);
        auto * x = ggml_new_tensor_2d(ctx, GGML_TYPE_F32, channels, frames);
        if (ggml_backend_tensor_alloc(buffers[current], x, ggml_backend_buffer_get_base(buffers[current])) != GGML_STATUS_SUCCESS) { ggml_free(ctx); return false; }
        auto * y = build(ctx, graph, x);
        const int C = (int) y->ne[0], T = (int) y->ne[1];
        if ((size_t) C * T * sizeof(float) > capacity) { ggml_free(ctx); return false; }
        auto * dst = ggml_new_tensor_2d(ctx, GGML_TYPE_F32, C, T);
        if (ggml_backend_tensor_alloc(buffers[1-current], dst, ggml_backend_buffer_get_base(buffers[1-current])) != GGML_STATUS_SUCCESS) { ggml_free(ctx); return false; }
        ggml_build_forward_expand(graph, ggml_cpy(ctx, y, dst));
        // The frozen oracle disables TF32. BF16 boundaries are explicit in the
        // graph; its F32 accumulations must not acquire an extra TF32 rounding.
        std::vector<ggml_tensor *> captures;
        for (int i = 0; i < ggml_graph_n_nodes(graph); ++i) {
            auto * node = ggml_graph_node(graph, i);
            if (node->op == GGML_OP_MUL_MAT) ggml_prec_set_acc(node, GGML_PREC_F32);
            const char * name = ggml_get_name(node);
            if (dump && (strcmp(name, "stage.0.l0.norm1") == 0 ||
                         strcmp(name, "stage.0.l0.qkv") == 0 ||
                         strcmp(name, "stage.0.l0.sdpa") == 0)) {
                // Preserve diagnostic tensors beyond their last graph consumer.
                ggml_set_output(node);
                captures.push_back(node);
            }
        }
        if (!m.galloc) m.galloc = ggml_gallocr_new(m.buft);
        bool ok = ggml_gallocr_alloc_graph(m.galloc, graph);
        if (ok) {
            ok = ggml_backend_graph_compute(m.backend, graph) == GGML_STATUS_SUCCESS;
        }
        if (ok && dump) {
            for (auto * node : captures) {
                std::vector<float> values((size_t) ggml_nelements(node));
                ggml_backend_tensor_get(node, values.data(), 0, values.size()*sizeof(float));
                if (!(*dump)(*prefix + "." + ggml_get_name(node), values.data(),
                             (int) node->ne[1], (int) node->ne[0])) {
                    ok = false;
                    break;
                }
            }
        }
        if (ok) { channels = C; frames = T; current = 1-current; }
        ggml_free(ctx);
        return ok;
    }
    std::vector<float> host() const {
        auto * ctx = ggml_init({4 * ggml_tensor_overhead(), nullptr, true});
        auto * x = ggml_new_tensor_2d(ctx, GGML_TYPE_F32, channels, frames);
        std::vector<float> out((size_t) channels * frames);
        ggml_backend_tensor_alloc(buffers[current], x, ggml_backend_buffer_get_base(buffers[current]));
        ggml_backend_tensor_get(x, out.data(), 0, out.size() * sizeof(float));
        ggml_free(ctx);
        return out;
    }
    int C() const { return channels; }
    int T() const { return frames; }
};

ggml_tensor * bf16(ggml_context * ctx, ggml_tensor * x) {
    return ggml_cast(ctx, ggml_cast(ctx, x, GGML_TYPE_BF16), GGML_TYPE_F32);
}

bool acoustic_lane(const zac_model & m, const float * audio, std::vector<float> & out,
                   const std::string & prefix, const zac_encoder_dump & dump) {
    using namespace zac_encoder_detail;
    encoder_graph g(m);
    auto capture = [&](const std::string & name) {
        if (!dump) return true;
        const auto values = g.host();
        return dump(prefix + "." + name, values.data(), g.T(), g.C());
    };
    const int N = LANE * INTERNAL_HOP;
    if (!g.init(1, N, audio, (size_t) 64 * N)) return false;
    if (!g.run([&](auto * c, auto * graph, auto * x) { return conv(c, graph, m, x, "enc.in"); })) return false;
    if (!capture("enc.in")) return false;
    for (int b = 0; b < 2; ++b) {
        const std::string p = "enc.b" + std::to_string(b);
        for (int r = 0; r < 3; ++r) {
            // Both ResidualUnit operands are BF16 convolution outputs. Unlike
            // MOSS layer-scale residuals, this add returns BF16 before Snake.
            if (!g.run([&](auto * c, auto * graph, auto * x) {
                return bf16(c, residual(c, graph, m, x, p + ".res" + std::to_string(r), r == 0 ? 1 : r == 1 ? 3 : 9));
            })) return false;
            if (!capture(p + ".res" + std::to_string(r))) return false;
        }
        if (!g.run([&](auto * c, auto * graph, auto * x) { return conv(c, graph, m, snake(c, graph, m, x, p + ".snake"), p + ".conv", 1, b == 0 ? 2 : 4); })) return false;
        if (!capture(p + ".conv")) return false;
    }
    for (int s = 0; s < (int) m.stages.size(); ++s) {
        const auto & hp = m.stages[s];
        const std::string p = "stage." + std::to_string(s);
        if (!g.run([&](auto * c, auto *, auto * x) {
            // Patch down: out[ch*h+j,t] = in[ch,t*h+j].
            const int C = (int) x->ne[0], T = (int) x->ne[1], h = hp.patch;
            x = ggml_reshape_3d(c, x, C, h, T/h);
            x = ggml_reshape_2d(c, ggml_cont(c, ggml_permute(c, x, 1, 0, 2, 3)), C*h, T/h);
            return bf16(c, ggml_mul_mat(c, m.get(p + ".in.weight"), bf16(c, x)));
        })) return false;
        if (!capture(p + ".in")) return false;
        const int T = g.T();
        std::vector<int32_t> positions(T);
        std::iota(positions.begin(), positions.end(), 0);
        std::vector<float> mask((size_t) T * T, -std::numeric_limits<float>::infinity());
        for (int q = 0; q < T; ++q) for (int k = std::max(0, q-hp.context+1); k <= q; ++k) mask[(size_t) q*T+k] = 0;
        stage_inputs inputs;
        inputs.ctx = ggml_init({8 * ggml_tensor_overhead(), nullptr, true});
        auto * pos = ggml_new_tensor_1d(inputs.ctx, GGML_TYPE_I32, T);
        auto * mk = ggml_new_tensor_2d(inputs.ctx, GGML_TYPE_F32, T, T);
        inputs.buffer = ggml_backend_alloc_ctx_tensors(inputs.ctx, m.backend);
        if (!inputs.buffer) return false;
        ggml_backend_tensor_set(pos, positions.data(), 0, positions.size() * sizeof(int32_t));
        ggml_backend_tensor_set(mk, mask.data(), 0, mask.size() * sizeof(float));
        for (int l = 0; l < hp.n_layer; ++l) {
            const bool capture_attention = dump && s == 0 && l == 0;
            if (!g.run([&](auto * c, auto * graph, auto * x) {
                return moss(c, graph, m, x, p + ".l" + std::to_string(l), hp, pos, mk, l == 0);
            }, capture_attention ? &dump : nullptr, capture_attention ? &prefix : nullptr)) return false;
            if (!capture(p + ".l" + std::to_string(l))) return false;
        }
        if (!g.run([&](auto * c, auto *, auto * x) { return bf16(c, ggml_mul_mat(c, m.get(p + ".out.weight"), bf16(c, x))); })) return false;
        if (!capture(p)) return false;
    }
    if (g.T() != LANE || g.C() != 1024) return false;
    out = g.host();
    return true;
}

bool semantic_module(const zac_model & m, const std::vector<float> & input, bool encode,
                     std::vector<float> & output) {
    using namespace zac_encoder_detail;
    encoder_graph g(m);
    const int width = m.semantic_dim;
    if (!g.init(encode ? 1024 : width, LANE, input.data(), (size_t) 2048 * LANE)) return false;
    if (encode && !g.run([&](auto * c, auto *, auto * x) {
        // WNConv1d: cuDNN rounds its unbiased output before the BF16 bias add.
        return bf16(c, ggml_add(c, bf16(c, ggml_mul_mat(c, m.get("sem_enc.in.weight"), bf16(c, x))), m.get("sem_enc.in.bias")));
    })) return false;
    for (int b = 0; b < m.sem_depth; ++b) {
        const std::string p = std::string(encode ? "sem_enc." : "sem_head.") + std::to_string(b);
        if (!g.run([&](auto * c, auto * graph, auto * x) { return convnext(c, graph, m, x, p, encode); })) return false;
    }
    if (!encode && !g.run([&](auto * c, auto *, auto * x) {
        return bf16(c, ggml_add(c, bf16(c, ggml_mul_mat(c, m.get("sem_head.out.weight"), bf16(c, x))), m.get("sem_head.out.bias")));
    })) return false;
    output = g.host();
    return true;
}

std::vector<float> pool_ssl(const std::vector<float> & ssl, int T, float window_ratio) {
    constexpr int C = 1024;
    std::vector<float> prefix((size_t) (T+1)*C, 0), pooled((size_t) LANE*C);
    for (int t = 0; t < T; ++t) for (int c = 0; c < C; ++c) prefix[(size_t) (t+1)*C+c] = prefix[(size_t) t*C+c] + ssl[(size_t) t*C+c];
    const float ratio = (float) T / LANE, half = 0.5f * window_ratio * ratio;
    for (int t = 0; t < LANE; ++t) {
        const float center = (t+0.5f)*ratio;
        const float left = std::clamp(center-half, 0.0f, (float) T), right = std::clamp(center+half, 0.0f, (float) T);
        const int li = (int) floorf(left), ri = (int) floorf(right);
        const float lf = left-li, rf = right-ri;
        for (int c = 0; c < C; ++c) {
            const float a = prefix[(size_t) li*C+c] + ssl[(size_t) std::min(li,T-1)*C+c]*lf;
            const float b = prefix[(size_t) ri*C+c] + ssl[(size_t) std::min(ri,T-1)*C+c]*rf;
            pooled[(size_t) t*C+c] = (b-a)/(right-left);
        }
    }
    return pooled;
}

bool quantize(const zac_encoder_model::quantizer & q, const std::vector<float> & input,
              std::vector<float> & quantized, std::vector<int32_t> & ids,
              const std::string & prefix, const zac_encoder_dump & dump) {
    const int C = q.input_dim, D = q.code_dim, T = (int) input.size()/C;
    quantized.resize(input.size()); ids.resize(T);
    std::vector<float> projected((size_t) T*D), normalized(D), lookup(D);
    for (int t = 0; t < T; ++t) {
        float norm = 0;
        for (int d = 0; d < D; ++d) {
            float value = 0;
            for (int c = 0; c < C; ++c) value += input[(size_t) t*C+c] * q.in_weight[(size_t) d*C+c];
            value += q.in_bias[d];
            projected[(size_t) t*D+d] = value;
            norm += value*value;
        }
        norm = std::max(sqrtf(norm), 1e-12f);
        float encoding_squared = 0;
        for (int d = 0; d < D; ++d) { normalized[d] = projected[(size_t) t*D+d]/norm; encoding_squared += normalized[d]*normalized[d]; }
        float best = std::numeric_limits<float>::infinity(); int id = 0;
        for (int k = 0; k < q.size; ++k) {
            float dot = 0;
            for (int d = 0; d < D; ++d) dot += normalized[d] * q.normalized[(size_t) k*D+d];
            const float distance = (encoding_squared - 2.0f*dot) + q.norm_squared[k];
            // torch.max returns the first index on exact ties (including a zero encoding).
            if (distance < best) { best = distance; id = k; }
        }
        ids[t] = id;
        // Preserve the actual inference forward's straight-through expression in F32.
        for (int d = 0; d < D; ++d) {
            const float ze = projected[(size_t) t*D+d];
            lookup[d] = ze + (q.codebook[(size_t) id*D+d] - ze);
        }
        for (int c = 0; c < C; ++c) {
            float value = 0;
            for (int d = 0; d < D; ++d) value += lookup[d] * q.out_weight[(size_t) c*D+d];
            quantized[(size_t) t*C+c] = value + q.out_bias[c];
        }
    }
    if (!std::all_of(quantized.begin(), quantized.end(), [](float v) { return std::isfinite(v); })) return false;
    if (dump) {
        std::vector<float> codes(ids.begin(), ids.end());
        return dump(prefix + ".projected", projected.data(), T, D) && dump(prefix + ".quantized", quantized.data(), T, C) && dump(prefix + ".codes", codes.data(), T, 1);
    }
    return true;
}

std::vector<float> resample(const zac_model & m, const float * audio, int n) {
    const int orig = m.rs_orig, target = m.rs_new, width = m.rs_width, K = 2*width+orig;
    // Exactly torchaudio's phase-major FIR, then transpose/flatten and ceil-length crop.
    const int count = (int) (((int64_t) n*target + orig-1)/orig);
    std::vector<float> out(count);
    for (int i = 0; i < count; ++i) {
        const int block = i/target, phase = i%target;
        const int start = block*orig-width;
        const float * kernel = m.rs_kernel.data() + (size_t) phase*K;
        float value = 0;
        for (int k = std::max(0,-start), end = std::min(K,n-start); k < end; ++k) value += kernel[k]*audio[start+k];
        out[i] = value;
    }
    return out;
}
}

void zac_encoder_free(zac_encoder_model & m) {
    zac_free(m.codec);
    m.codec.t.clear(); m.codec.rs_kernel.clear(); m.codec.stages.clear();
    m.rvq.clear();
}

bool zac_encoder_load(zac_encoder_model & model, const char * path, bool use_gpu) {
    zac_encoder_free(model);
    auto & m = model.codec;
    ggml_context * meta = nullptr;
    gguf_context * file = gguf_init_from_file(path, {true, &meta});
    if (!file) { fprintf(stderr, "zac-encoder: cannot open %s\n", path); return false; }
    auto reject = [&](const char * why) { fprintf(stderr, "zac-encoder: invalid mossdual_encoder GGUF: %s\n", why); gguf_free(file); ggml_free(meta); zac_encoder_free(model); return false; };
    int64_t arch = gguf_find_key(file, "general.architecture");
    if (arch < 0 || gguf_get_kv_type(file,arch) != GGUF_TYPE_STRING || strcmp(gguf_get_val_str(file,arch),ARCH)) return reject("architecture");
    auto integer = [&](const char * k, int & value) {
        int64_t i = gguf_find_key(file, (std::string(ARCH)+"."+k).c_str());
        if (i < 0 || gguf_get_kv_type(file,i) != GGUF_TYPE_UINT32) return false;
        value = (int) gguf_get_val_u32(file,i); return value > 0;
    };
    auto number = [&](const char * k, float & value) {
        int64_t i = gguf_find_key(file, (std::string(ARCH)+"."+k).c_str());
        if (i < 0 || gguf_get_kv_type(file,i) != GGUF_TYPE_FLOAT32) return false;
        value = gguf_get_val_f32(file,i); return std::isfinite(value) && value > 0;
    };
    int lane=0, context=0;
    if (!integer("sample_rate",m.sample_rate) || !integer("internal_sample_rate",m.internal_rate) || !integer("frame_rate",m.frame_rate) || !integer("hop",m.hop) || !integer("internal_hop",m.internal_hop) || !integer("n_codebooks",m.n_codebooks) || !integer("codebook_size",m.codebook_size) || !integer("latent_dim",m.latent_dim) || !integer("semantic_dim",m.semantic_dim) || !integer("semantic_depth",m.sem_depth) || !integer("lane_frames",lane) || !integer("context_frames",context) || !integer("resample.orig",m.rs_orig) || !integer("resample.new",m.rs_new) || !integer("resample.width",m.rs_width) || !number("ln_eps",m.ln_eps) || !number("convnext_eps",m.convnext_eps) || !number("window_ratio",model.window_ratio)) return reject("metadata");
    if (m.sample_rate != 44100 || m.internal_rate != 44160 || m.frame_rate != 15 || m.hop != HOP || m.internal_hop != INTERNAL_HOP || m.n_codebooks != 10 || m.codebook_size != 4096 || m.latent_dim != 1024 || m.semantic_dim != 768 || m.sem_depth != 4 || lane != LANE || context != CONTEXT || m.rs_orig != 735 || m.rs_new != 736 || model.window_ratio != 2.0f) return reject("recipe dimensions");
    std::vector<std::vector<int32_t>> arrays;
    for (const char * k : {"in_dim", "out_dim", "d_model", "n_head", "n_layer", "n_ff", "context", "patch"}) {
        int64_t i = gguf_find_key(file,(std::string(ARCH)+".stage."+k).c_str());
        if (i < 0 || gguf_get_kv_type(file,i) != GGUF_TYPE_ARRAY || gguf_get_arr_type(file,i) != GGUF_TYPE_INT32 || gguf_get_arr_n(file,i) != 5) return reject("stage metadata");
        auto * values = (const int32_t *) gguf_get_arr_data(file,i);
        arrays.emplace_back(values,values+5);
    }
    for (int s=0; s<5; ++s) {
        zac_stage_hp hp{arrays[0][s],arrays[1][s],arrays[2][s],arrays[3][s],arrays[4][s],arrays[5][s],arrays[6][s],arrays[7][s]};
        if (hp.in_dim <= 0 || hp.out_dim <= 0 || hp.d_model <= 0 || hp.n_head <= 0 || hp.n_layer <= 0 || hp.n_ff <= 0 || hp.context <= 0 || hp.patch != (s==0 ? 23 : 2) || hp.d_model/hp.n_head != 64 || hp.d_model%hp.n_head || (s==0 ? hp.in_dim != 256*23 : hp.in_dim != m.stages.back().out_dim*hp.patch)) return reject("stage dimensions");
        m.stages.push_back(hp);
    }
    if (m.stages.back().out_dim != 1024) return reject("latent dimension");
    ggml_backend_dev_t dev = zonos3_backend_device(use_gpu);
    if (!dev || !(m.backend = ggml_backend_dev_init(dev,nullptr))) return reject("backend");
    m.buft = ggml_backend_dev_buffer_type(dev);
    m.is_gpu = zonos3_backend_is_gpu(dev);
    if (!m.is_gpu) {
        const char * threads = getenv("ZAC_THREADS");
        if (threads) ggml_backend_cpu_set_n_threads(m.backend,std::max(1,atoi(threads)));
    }
    m.ctx_w = ggml_init({(size_t) gguf_get_n_tensors(file)*ggml_tensor_overhead(),nullptr,true});
    std::map<std::string,ggml_tensor *> original;
    std::map<std::string,std::vector<float>> host;
    for (auto * t=ggml_get_first_tensor(meta); t; t=ggml_get_next_tensor(meta,t)) {
        const std::string name = ggml_get_name(t);
        const bool host_tensor = name == "resample.kernel" || name.rfind("rvq.",0)==0;
        if (host_tensor) {
            if (t->type != GGML_TYPE_F32) return reject("RVQ/resampler must be F32");
            host[name].resize((size_t) ggml_nelements(t));
        } else {
            const bool stage_matrix = name.rfind("stage.",0)==0 && name.find(".weight") != std::string::npos && name.find("norm") == std::string::npos && ggml_n_dims(t)==2;
            if (t->type != GGML_TYPE_F32 && !(stage_matrix && (t->type==GGML_TYPE_F16 || ggml_is_quantized(t->type)))) return reject("tensor precision");
            const bool linear_matrix = ggml_n_dims(t)==2 && (stage_matrix ||
                name == "sem_enc.in.weight" || name == "sem_head.out.weight" ||
                name.find(".pw1.weight") != std::string::npos || name.find(".pw2.weight") != std::string::npos);
            // Keep the actual autocast operand dtype, not F32 storage containing
            // rounded BF16 values: that would select a different GEMM arithmetic.
            auto * dst = linear_matrix && !ggml_is_quantized(t->type)
                ? ggml_new_tensor_2d(m.ctx_w, GGML_TYPE_BF16, t->ne[0], t->ne[1])
                : ggml_dup_tensor(m.ctx_w,t);
            ggml_set_name(dst,name.c_str()); m.t[name]=dst;
        }
        original[name]=t;
    }
    m.buf_w = ggml_backend_alloc_ctx_tensors(m.ctx_w,m.backend);
    FILE * input = fopen(path,"rb");
    if (!m.buf_w || !input) { if (input) fclose(input); return reject("weight allocation/open"); }
    std::vector<char> bytes;
    std::vector<float> converted;
    std::vector<ggml_bf16_t> converted_bf16;
    for (const auto & kv : original) {
        auto * t = kv.second;
        const size_t nbytes = ggml_nbytes(t);
        bytes.resize(nbytes);
        const int64_t id = gguf_find_tensor(file,kv.first.c_str());
        if (fseeko(input,gguf_get_data_offset(file)+gguf_get_tensor_offset(file,id),SEEK_SET) || fread(bytes.data(),1,nbytes,input)!=nbytes) { fclose(input); return reject("tensor read"); }
        auto hi=host.find(kv.first);
        if (hi != host.end()) memcpy(hi->second.data(),bytes.data(),nbytes);
        else {
            const bool autocast_parameter = kv.first.find("norm") == std::string::npos &&
                (kv.first.find(".weight") != std::string::npos || kv.first.find(".bias") != std::string::npos);
            // Tap/depthwise convolution views still require F32 storage. Linear
            // matrices use BF16 operands with F32 accumulation and explicit output
            // rounding (after bias where present), matching CUDA autocast.
            auto * dst = m.t.at(kv.first);
            if (dst->type == GGML_TYPE_BF16) {
                const float * values = reinterpret_cast<const float *>(bytes.data());
                if (t->type == GGML_TYPE_F16) {
                    converted.resize((size_t) ggml_nelements(t));
                    ggml_fp16_to_fp32_row(reinterpret_cast<const ggml_fp16_t *>(bytes.data()), converted.data(), ggml_nelements(t));
                    values = converted.data();
                }
                converted_bf16.resize((size_t) ggml_nelements(t));
                ggml_fp32_to_bf16_row(values, converted_bf16.data(), ggml_nelements(t));
                ggml_backend_tensor_set(dst, converted_bf16.data(), 0, converted_bf16.size()*sizeof(ggml_bf16_t));
            } else {
                if (autocast_parameter && t->type == GGML_TYPE_F32) {
                    float * values = reinterpret_cast<float *>(bytes.data());
                    for (int64_t j=0;j<ggml_nelements(t);++j) values[j]=ggml_bf16_to_fp32(ggml_fp32_to_bf16(values[j]));
                }
                ggml_backend_tensor_set(m.t.at(kv.first),bytes.data(),0,nbytes);
            }
        }
    }
    fclose(input);
    auto shape = [&](const std::string & name, std::initializer_list<int64_t> dims) {
        auto it=original.find(name); if (it==original.end()) return false;
        int d=0; for (auto dim:dims) if (it->second->ne[d++] != dim) return false;
        for (;d<4;++d) if (it->second->ne[d] != 1) return false;
        return true;
    };
    auto conv_ok = [&](const std::string & p,int ic,int oc,int k) { return shape(p+".weight",{ic,oc,k}) && shape(p+".bias",{1,oc}); };
    auto snake_ok = [&](const std::string & p,int c) { return shape(p+".alpha",{1,c}) && shape(p+".inv_alpha",{1,c}); };
    if (!conv_ok("enc.in",1,64,7)) return reject("acoustic input convolution");
    for (int b=0;b<2;++b) {
        const std::string p="enc.b"+std::to_string(b); const int C=b==0?64:128;
        if (!snake_ok(p+".snake",C) || !conv_ok(p+".conv",C,2*C,b==0?4:8)) return reject("acoustic downsampling block");
        for (int r=0;r<3;++r) {
            const std::string q=p+".res"+std::to_string(r);
            if (!snake_ok(q+".snake1",C) || !snake_ok(q+".snake2",C) || !conv_ok(q+".conv1",C,C,7) || !conv_ok(q+".conv2",C,C,1)) return reject("acoustic residual unit");
        }
    }
    for (int s=0;s<5;++s) {
        const auto & hp=m.stages[s]; const std::string p="stage."+std::to_string(s);
        if (!shape(p+".in.weight",{hp.in_dim,hp.d_model}) || !shape(p+".out.weight",{hp.d_model,hp.out_dim})) return reject("MOSS projection");
        for (int l=0;l<hp.n_layer;++l) {
            const std::string q=p+".l"+std::to_string(l);
            for (const char * k:{"norm1.weight","norm1.bias","norm2.weight","norm2.bias","attn_scale","ffn_scale"}) if (!shape(q+"."+k,{hp.d_model})) return reject("MOSS normalization/scale");
            if (!shape(q+".qkv.weight",{hp.d_model,3*hp.d_model}) || !shape(q+".wo.weight",{hp.d_model,hp.d_model}) || !shape(q+".ffn1.weight",{hp.d_model,hp.n_ff}) || !shape(q+".ffn2.weight",{hp.n_ff,hp.d_model})) return reject("MOSS linear");
        }
    }
    for (const char * branch:{"sem_enc","sem_head"}) for (int b=0;b<4;++b) {
        const std::string p=std::string(branch)+"."+std::to_string(b);
        if (!shape(p+".dw.weight",{768,7}) || !shape(p+".dw.bias",{768}) || !shape(p+".norm.weight",{768}) || !shape(p+".norm.bias",{768}) || !shape(p+".pw1.weight",{768,2048}) || !shape(p+".pw1.bias",{2048}) || !shape(p+".pw2.weight",{2048,768}) || !shape(p+".pw2.bias",{768})) return reject("semantic ConvNeXt");
    }
    if (!shape("sem_enc.in.weight",{1024,768}) || !shape("sem_enc.in.bias",{768}) || !shape("sem_head.out.weight",{768,1024}) || !shape("sem_head.out.bias",{1024})) return reject("semantic projection");
    if (!shape("resample.kernel",{2*m.rs_width+m.rs_orig,m.rs_new})) return reject("resampler");
    m.rs_kernel=std::move(host.at("resample.kernel"));
    for (int c=0;c<10;++c) {
        const std::string p="rvq."+std::to_string(c); const int C=c==0?768:1024;
        if (!shape(p+".in_proj.weight",{C,8}) || !shape(p+".in_proj.bias",{8}) || !shape(p+".out_proj.weight",{8,C}) || !shape(p+".out_proj.bias",{C}) || !shape(p+".codebook",{8,4096})) return reject("RVQ input projection/original codebook/output projection");
        zac_encoder_model::quantizer q; q.input_dim=C; q.code_dim=8; q.size=4096;
        q.in_weight=std::move(host.at(p+".in_proj.weight")); q.in_bias=std::move(host.at(p+".in_proj.bias"));
        q.out_weight=std::move(host.at(p+".out_proj.weight")); q.out_bias=std::move(host.at(p+".out_proj.bias")); q.codebook=std::move(host.at(p+".codebook"));
        q.normalized.resize(q.codebook.size()); q.norm_squared.resize(q.size);
        for (int k=0;k<q.size;++k) {
            float norm=0; for (int d=0;d<q.code_dim;++d) { float v=q.codebook[(size_t)k*q.code_dim+d]; norm+=v*v; }
            norm=std::max(sqrtf(norm),1e-12f); float squared=0;
            for (int d=0;d<q.code_dim;++d) { float v=q.codebook[(size_t)k*q.code_dim+d]/norm; q.normalized[(size_t)k*q.code_dim+d]=v; squared+=v*v; }
            q.norm_squared[k]=squared;
        }
        model.rvq.push_back(std::move(q));
    }
    gguf_free(file); ggml_free(meta);
    {   // Encoder K>1 convolutions: Metal implicit-GEMM conv1d (BF16 weights, F32 activations),
        // bit-identical to im2col + mul_mat there. CPU keeps im2col: its reference op sums in a
        // different order than the CPU BF16 GEMM and is slower.
        ggml_context * pc = ggml_init({ 4 * ggml_tensor_overhead(), nullptr, true });
        ggml_tensor * w = ggml_new_tensor_2d(pc, GGML_TYPE_BF16, 7 * 64, 64);
        ggml_tensor * x = ggml_new_tensor_2d(pc, GGML_TYPE_F32, 64, 128);
        m.use_conv1d = m.is_gpu && ggml_backend_supports_op(m.backend, ggml_conv_1d_implicit(pc, w, x, 7, 1, 1, 6));
        ggml_free(pc);
    }
    fprintf(stderr,"zac-encoder: loaded %zu tensors on %s (native BF16 modules, F32 RVQ)\n",m.t.size(),m.is_gpu?"GPU":"CPU");
    return true;
}

bool zac_encode_diagnostic(const zac_encoder_model & model, const ssl_model & teacher,
                           const float * audio, int n, std::vector<int32_t> & codes,
                           const zac_encoder_dump & dump) {
    codes.clear();
    if (!model.codec.backend || model.rvq.size()!=10 || !audio || n<=0 || n>ZAC_ENCODER_MAX_SECONDS*44100 || !std::all_of(audio,audio+n,[](float v){return std::isfinite(v);})) {
        fprintf(stderr,"zac-encoder: expected finite nonempty mono 44100 Hz audio, at most %d seconds\n",ZAC_ENCODER_MAX_SECONDS); return false;
    }
    const auto & m=model.codec;
    const int frames=(n+HOP-1)/HOP;
    auto internal=resample(m,audio,n);
    internal.resize((size_t) frames*INTERNAL_HOP,0);
    if (dump && !dump("internal_audio",internal.data(),(int)internal.size(),1)) return false;
    std::vector<int32_t> result((size_t) frames*10);
    std::vector<float> lane_audio((size_t)LANE*INTERNAL_HOP), teacher_audio((size_t)LANE*HOP);
    int lane=0;
    for (int start=0;start<frames;++lane) {
        const int context=start==0?0:CONTEXT;
        const int count=std::min(frames-start,LANE-context), left=start-context;
        std::fill(lane_audio.begin(),lane_audio.end(),0); std::fill(teacher_audio.begin(),teacher_audio.end(),0);
        std::copy_n(internal.data()+(size_t)left*INTERNAL_HOP,(size_t)(context+count)*INTERNAL_HOP,lane_audio.data());
        const int teacher_start=left*HOP, teacher_count=std::min(n-teacher_start,(context+count)*HOP);
        std::copy_n(audio+teacher_start,teacher_count,teacher_audio.data());
        const std::string p="lane."+std::to_string(lane);
        if (dump && (!dump(p+".audio",lane_audio.data(),(int)lane_audio.size(),1) || !dump(p+".teacher_audio",teacher_audio.data(),(int)teacher_audio.size(),1))) return false;
        std::vector<float> ssl,acoustic; int T=0;
        if (!ssl_encode_pcm(teacher,teacher_audio.data(),(int)teacher_audio.size(),44100,ssl,T) || T<1 || ssl.size()!=(size_t)T*1024) return false;
        if (!acoustic_lane(m,lane_audio.data(),acoustic,p,dump)) return false;
        auto pooled=pool_ssl(ssl,T,model.window_ratio);
        std::vector<float> semantic,q1,prediction,residual,qr;
        std::vector<int32_t> ids;
        if (!semantic_module(m,pooled,true,semantic) || !quantize(model.rvq[0],semantic,q1,ids,p+".rvq.0",dump) || !semantic_module(m,q1,false,prediction)) return false;
        for (int t=0;t<count;++t) result[(size_t)(start+t)*10]=ids[context+t];
        residual.resize(acoustic.size());
        for (size_t i=0;i<residual.size();++i) residual[i]=acoustic[i]-prediction[i];
        if (dump && (!dump(p+".ssl",ssl.data(),T,1024) || !dump(p+".acoustic",acoustic.data(),LANE,1024) || !dump(p+".pooled",pooled.data(),LANE,1024) || !dump(p+".semantic",semantic.data(),LANE,768) || !dump(p+".q1",q1.data(),LANE,768) || !dump(p+".semantic_prediction",prediction.data(),LANE,1024) || !dump(p+".residual",residual.data(),LANE,1024))) return false;
        for (int c=1;c<10;++c) {
            if (!quantize(model.rvq[c],residual,qr,ids,p+".rvq."+std::to_string(c),dump)) return false;
            for (int t=0;t<count;++t) result[(size_t)(start+t)*10+c]=ids[context+t];
            for (size_t i=0;i<residual.size();++i) residual[i]-=qr[i];
        }
        start+=count;
    }
    if (dump) { std::vector<float> values(result.begin(),result.end()); if (!dump("codes",values.data(),frames,10)) return false; }
    codes=std::move(result);
    return true;
}

bool zac_encode(const zac_encoder_model & m, const ssl_model & teacher, const float * audio,
                int n_samples, std::vector<int32_t> & codes) {
    return zac_encode_diagnostic(m,teacher,audio,n_samples,codes,{});
}
