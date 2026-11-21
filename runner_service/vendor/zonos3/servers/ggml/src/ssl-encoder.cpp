// Sixteen-layer semantic teacher and SeamlessM4T frontend.
#include "compat.h"
#include "backend-device.h"
#include "ssl-encoder.h"
#include "ggml.h"
#include "ggml-alloc.h"
#include "ggml-backend.h"
#include "ggml-cpu.h"
#include "gguf.h"
#include <algorithm>
#include <array>
#include <cfloat>
#include <memory>
#include <cmath>
#include <complex>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <string>
#include <thread>
#include <vector>

struct ssl_graph_cache {
    ggml_context * ctx = nullptr;
    ggml_gallocr_t alloc = nullptr;
    ggml_cgraph * graph = nullptr;
    ggml_tensor * input = nullptr, * valid = nullptr, * attn_mask = nullptr;
    std::vector<ggml_tensor *> states;
    std::vector<std::pair<std::string,ggml_tensor *>> operation_states;
    std::vector<ggml_tensor *> trace_order;
    std::vector<float> valid_data, mask_data;
    int frames = 0;
    bool trace = false;
    ssl_dtype dtype = ssl_dtype::bf16;
    ~ssl_graph_cache() {
        if (alloc) ggml_gallocr_free(alloc);
        if (ctx) ggml_free(ctx);
    }
};

bool ssl_load(ssl_model & m, const char * path, bool use_gpu) {
    ssl_free(m);
    // The frozen teacher was trained without TF32. Set this before CUDA creates
    // its cuBLAS handles; F32 precision hints alone do not disable TF32 math mode.
#ifdef _WIN32
    _putenv_s("NVIDIA_TF32_OVERRIDE", "0");
#else
    setenv("NVIDIA_TF32_OVERRIDE", "0", 1);
#endif
    ggml_context * meta = nullptr;
    gguf_context * gf = gguf_init_from_file(path, { true, &meta });
    if (!gf) { fprintf(stderr, "ssl: cannot open GGUF '%s'\n", path); return false; }
    auto fail = [&]() { gguf_free(gf); ggml_free(meta); return false; };
    int64_t arch = gguf_find_key(gf, "general.architecture");
    if (arch < 0 || gguf_get_kv_type(gf,arch) != GGUF_TYPE_STRING ||
        std::strcmp(gguf_get_val_str(gf, arch), "w2vbert_ssl")) {
        fprintf(stderr, "ssl: expected w2vbert_ssl (tools/convert_gguf.py ssl-teacher)\n");
        return fail();
    }
    auto u32 = [&](const char * key, int & out, int expected) {
        std::string full = std::string("w2vbert_ssl.") + key;
        int64_t id = gguf_find_key(gf, full.c_str());
        if (id < 0 || gguf_get_kv_type(gf, id) != GGUF_TYPE_UINT32) return false;
        out = (int) gguf_get_val_u32(gf, id);
        return out == expected;
    };
    auto f32 = [&](const char * key, float & out, float expected) {
        std::string full = std::string("w2vbert_ssl.") + key;
        int64_t id = gguf_find_key(gf, full.c_str());
        if (id < 0 || gguf_get_kv_type(gf, id) != GGUF_TYPE_FLOAT32) return false;
        out = gguf_get_val_f32(gf, id);
        return out == expected;
    };
    float padding_value = 1.0f;
    if (!(u32("sample_rate", m.sample_rate, 16000) && u32("max_seconds", m.max_seconds, 60) &&
          u32("n_layer", m.n_layer, 16) && u32("n_embd", m.n_embd, 1024) &&
          u32("n_head", m.n_head, 16) && u32("n_ff", m.n_ff, 4096) &&
          u32("rel_left", m.rel_left, 64) && u32("rel_right", m.rel_right, 8) &&
          u32("conv_kernel", m.conv_kernel, 31) && u32("n_mels", m.n_mels, 80) &&
          u32("frame_length", m.frame_length, 400) && u32("hop_length", m.hop_length, 160) &&
          u32("fft_length", m.fft_length, 512) && u32("stride", m.stride, 2) &&
          f32("eps", m.eps, 1e-5f) && f32("preemphasis", m.preemph, 0.97f) &&
          f32("padding_value",padding_value,1.0f) &&
          f32("mel_floor", m.mel_floor, 1.192092955078125e-07f))) {
        fprintf(stderr, "ssl: unsupported or incomplete sixteen-layer teacher metadata\n");
        return fail();
    }
    std::map<std::string, std::vector<int64_t>> expected;
    auto shape = [&](const std::string & name, std::initializer_list<int64_t> dims) { expected[name] = dims; };
    auto pair = [&](const std::string & p, int in, int out) {
        shape(p + ".weight", {in,out}); shape(p + ".bias", {out});
    };
    auto norm = [&](const std::string & p, int dim) {
        shape(p + ".weight", {dim}); shape(p + ".bias", {dim});
    };
    norm("fp.ln",160); pair("fp.proj",160,1024);
    shape("frontend.window",{400}); shape("frontend.mel_filters",{80,257});
    shape("frontend.resample_44100",{475,160});
    for (int l = 0; l < 16; ++l) {
        const std::string p = "enc." + std::to_string(l) + ".";
        for (int j : {1,2}) {
            const std::string f = p + "ffn" + std::to_string(j);
            norm(f + "_ln",1024); pair(f + ".up",1024,4096); pair(f + ".down",4096,1024);
        }
        norm(p + "attn_ln",1024);
        for (const char * q : {"q","k","v","o"}) pair(p + "attn." + q,1024,1024);
        shape(p + "attn.dist",{64,73}); norm(p + "conv_ln",1024);
        shape(p + "conv.pw1.weight",{1024,2048}); shape(p + "conv.dw.weight",{31,1024});
        norm(p + "conv.dw_ln",1024); shape(p + "conv.pw2.weight",{1024,1024});
        norm(p + "final_ln",1024);
    }
    if (gguf_get_n_tensors(gf) != (int64_t) expected.size()) {
        fprintf(stderr,"ssl: tensor count does not match teacher architecture\n"); return fail();
    }
    for (const auto & item : expected) {
        ggml_tensor * t = ggml_get_tensor(meta,item.first.c_str());
        if (!t) { fprintf(stderr,"ssl: missing %s\n",item.first.c_str()); return fail(); }
        for (int d = 0; d < GGML_MAX_DIMS; ++d) {
            int64_t ne = d < (int) item.second.size() ? item.second[d] : 1;
            if (t->ne[d] != ne) { fprintf(stderr,"ssl: bad shape: %s\n",item.first.c_str()); return fail(); }
        }
        if (item.first.rfind("frontend.",0) == 0 &&
            t->type != (item.first == "frontend.resample_44100" ? GGML_TYPE_F32 : GGML_TYPE_F64)) {
            fprintf(stderr,"ssl: frontend table dtype mismatch\n"); return fail();
        }
    }
    ggml_backend_dev_t dev = zonos3_backend_device(use_gpu);
    if (!dev) return fail();
    m.backend = ggml_backend_dev_init(dev,nullptr);
    if (!m.backend) return fail();
    m.buft = ggml_backend_dev_buffer_type(dev);
    m.is_gpu = zonos3_backend_is_gpu(dev);
    if (!m.is_gpu) ggml_backend_cpu_set_n_threads(m.backend,std::max(1u,std::thread::hardware_concurrency()));
    m.ctx_w = meta;
    m.buf_w = ggml_backend_alloc_ctx_tensors(meta,m.backend);
    if (!m.buf_w) { gguf_free(gf); ssl_free(m); return false; }
    FILE * f = fopen(path,"rb");
    if (!f) { gguf_free(gf); ssl_free(m); return false; }
    std::vector<char> tmp;
    bool ok = true;
    for (ggml_tensor * t = ggml_get_first_tensor(meta); t; t = ggml_get_next_tensor(meta,t)) {
        const char * name = ggml_get_name(t);
        int64_t tid = gguf_find_tensor(gf,name);
        const size_t nb = ggml_nbytes(t);
        tmp.resize(nb);
        if (tid < 0 || fseeko(f,gguf_get_data_offset(gf) + gguf_get_tensor_offset(gf,tid),SEEK_SET) ||
            fread(tmp.data(),1,nb,f) != nb) { fprintf(stderr,"ssl: read failed: %s\n",name); ok = false; break; }
        ggml_backend_tensor_set(t,tmp.data(),0,nb); m.t[name] = t;
    }
    fclose(f); gguf_free(gf);
    if (!ok) { ssl_free(m); return false; }
    m.window.resize(400); m.mel_filters.resize(257*80);
    std::vector<float> resample_kernel(160*475);
    ggml_backend_tensor_get(m.get("frontend.window"),m.window.data(),0,m.window.size()*sizeof(double));
    ggml_backend_tensor_get(m.get("frontend.mel_filters"),m.mel_filters.data(),0,m.mel_filters.size()*sizeof(double));
    ggml_backend_tensor_get(m.get("frontend.resample_44100"),resample_kernel.data(),0,resample_kernel.size()*sizeof(float));
    m.resample.init(std::move(resample_kernel));
    fprintf(stderr,"ssl: loaded %zu tensors on %s (layer16,1024 channels)\n",m.t.size(),m.is_gpu ? "GPU" : "CPU");
    return true;
}
void ssl_free(ssl_model & m) {
    std::lock_guard<std::mutex> lock(m.graph_mutex);
    delete m.graph_cache; m.graph_cache = nullptr;
    if (m.buf_w) ggml_backend_buffer_free(m.buf_w);
    if (m.ctx_w) ggml_free(m.ctx_w);
    if (m.backend) ggml_backend_free(m.backend);
    m.buf_w = nullptr; m.ctx_w = nullptr; m.backend = nullptr; m.t.clear();
    m.mel_filters.clear(); m.window.clear(); m.resample.clear();
}
// NumPy's spectrogram computes an F64 FFT, then materializes its complex64 result.
static void fft(std::vector<std::complex<double>> & a) {
    const int n = (int) a.size();
    for (int i = 1, j = 0; i < n; ++i) {
        int bit = n >> 1;
        for (; j & bit; bit >>= 1) j ^= bit;
        j ^= bit;
        if (i < j) std::swap(a[i], a[j]);
    }
    // The checkpoint fixes n=512; cache each stage's twiddle factors once.
    static const std::array<std::complex<double>, 256> twiddle = [] {
        std::array<std::complex<double>, 256> t{};
        for (int i = 0; i < 256; ++i) {
            double angle = -2.0 * std::acos(-1.0) * i / 512;
            t[i] = { std::cos(angle), std::sin(angle) };
        }
        return t;
    }();
    for (int len = 2; len <= n; len <<= 1) {
        for (int i = 0; i < n; i += len) {
            for (int j = 0; j < len / 2; ++j) {
                auto u = a[i + j], v = a[i + j + len / 2] * twiddle[j * (n / len)];
                a[i + j] = u + v; a[i + j + len / 2] = u - v;
            }
        }
    }
}
// The extractor transposes its C-order [mel,frame] fbank, so NumPy reduces each
// contiguous frame column with this eight-accumulator, 128-element pairwise
// ordering. Sequential F32 accumulation changes long padded-lane statistics.
template <bool squared>
static float frontend_pairwise_sum(const float * x, int n, int stride, float mean = 0.0f) {
    auto value = [&](int i) {
        if constexpr (squared) {
            const float delta = x[(size_t) i*stride] - mean;
            // NumPy materializes F32 squared deviations before their reduction.
            // Force that boundary even on compilers that contract multiply/add.
            volatile const float square = delta*delta;
            return square;
        } else {
            return x[(size_t) i*stride];
        }
    };
    if (n < 8) {
        float sum = -0.0f;
        for (int i = 0; i < n; ++i) sum += value(i);
        return sum;
    }
    if (n <= 128) {
        float sums[8];
        for (int j = 0; j < 8; ++j) sums[j] = value(j);
        const int end = n - n%8;
        int i = 8;
        for (; i < end; i += 8)
            for (int j = 0; j < 8; ++j) sums[j] += value(i+j);
        float sum = ((sums[0]+sums[1])+(sums[2]+sums[3])) +
                    ((sums[4]+sums[5])+(sums[6]+sums[7]));
        for (; i < n; ++i) sum += value(i);
        return sum;
    }
    int half = n/2;
    half -= half%8;
    return frontend_pairwise_sum<squared>(x,half,stride,mean) +
           frontend_pairwise_sum<squared>(x+(size_t) half*stride,n-half,stride,mean);
}
static bool ssl_features(const ssl_model & m, const float * wav, int n, std::vector<float> & feats,
                         int & T, std::vector<int32_t> & mask) {
    T = 0; feats.clear(); mask.clear();
    const int nf = n < m.frame_length ? 0 : 1 + (n - m.frame_length) / m.hop_length;
    if (nf < 2) { fprintf(stderr,"ssl: audio too short for normalized fbank\n"); return false; }
    const int bins = m.fft_length / 2 + 1;
    std::vector<float> fb((size_t) nf * m.n_mels), mean(m.n_mels,0.0f), var(m.n_mels,0.0f);
    std::vector<std::complex<double>> z(m.fft_length);
    std::vector<double> powers(bins);
    for (int fr = 0; fr < nf; ++fr) {
        double avg = 0;
        for (int i = 0; i < m.frame_length; ++i) avg += (double) wav[fr * m.hop_length + i] * 32768.0;
        avg /= m.frame_length;
        for (int i = 0; i < m.frame_length; ++i)
            z[i] = (double) wav[fr * m.hop_length + i] * 32768.0 - avg;
        for (int i = m.frame_length - 1; i > 0; --i) z[i] -= 0.97 * z[i - 1];
        z[0] *= 1.0 - 0.97;
        for (int i = 0; i < m.frame_length; ++i) z[i] *= m.window[i];
        for (int i = m.frame_length; i < m.fft_length; ++i) z[i] = 0;
        fft(z);
        for (int k = 0; k < bins; ++k) {
            double re = (float) z[k].real(), im = (float) z[k].imag();
            powers[k] = re*re + im*im;
        }
        for (int mel = 0; mel < m.n_mels; ++mel) {
            double power = 0.0;
            for (int k = 0; k < bins; ++k)
                power += powers[k] * m.mel_filters[(size_t) k*m.n_mels + mel];
            float v = (float) std::log(std::max(power,(double) m.mel_floor));
            fb[(size_t) fr * m.n_mels + mel] = v;
        }
    }
    for (int j = 0; j < m.n_mels; ++j) {
        mean[j] = frontend_pairwise_sum<false>(fb.data()+j,nf,m.n_mels)/nf;
        var[j] = std::sqrt(frontend_pairwise_sum<true>(fb.data()+j,nf,m.n_mels,mean[j])/(nf-1) + 1e-7f);
    }
    T = (nf + m.stride - 1) / m.stride;
    feats.assign((size_t) T * m.stride * m.n_mels,1.0f);
    mask.assign(T,1);
    if (nf % m.stride) mask.back() = 0;
    for (int i = 0; i < nf; ++i)
        for (int j = 0; j < m.n_mels; ++j)
            feats[(size_t) i * m.n_mels + j] = (float) ((fb[(size_t) i * m.n_mels + j] - mean[j]) / var[j]);
    return true;
}
// The frozen adapter runs the BF16 teacher under CUDA autocast. LayerNorm and
// softmax return F32; GEMMs/convolutions return BF16. Materialize only those
// actual BF16 tensor boundaries, never a blanket BF16 cast of every operation.
static ggml_tensor * rounded(ggml_context * c, ggml_tensor * x, ssl_dtype dtype) {
    return dtype == ssl_dtype::bf16 ? ggml_cast(c, ggml_cast(c, x, GGML_TYPE_BF16), GGML_TYPE_F32) : x;
}
static ggml_tensor * matmul(ggml_context * c, ggml_tensor * w, ggml_tensor * x, ssl_dtype dtype) {
    const ggml_type operand_type = dtype == ssl_dtype::bf16 ? GGML_TYPE_BF16 : GGML_TYPE_F32;
    if (w->type != operand_type && !ggml_is_quantized(w->type)) w = ggml_cast(c, w, operand_type);
    ggml_tensor * y = ggml_mul_mat(c, w, x);
    // BF16 operands use tensor cores with F32 accumulation, not an F32 GEMM
    // approximation. Keep the accumulator output until the caller's PyTorch
    // tensor boundary (in particular, after Linear's bias).
    ggml_prec_set_acc(y, GGML_PREC_F32);
    return y;
}
static ggml_tensor * ln(ggml_context * c, const ssl_model & m, ggml_tensor * x,
                        const std::string & p) {
    return ggml_add(c, ggml_mul(c, ggml_norm(c, x, m.eps), m.get(p + ".weight")),
                       m.get(p + ".bias"));
}
static ggml_tensor * linear(ggml_context * c, const ssl_model & m, ggml_tensor * x,
                            const std::string & p, ssl_dtype dtype, bool bias = true) {
    ggml_tensor * y = matmul(c, m.get(p + ".weight"), x, dtype);
    return rounded(c, bias ? ggml_add(c, y, m.get(p + ".bias")) : y, dtype);
}
static void capture(ggml_context * c, ssl_graph_cache * cache, const std::string & name, ggml_tensor * x) {
    ggml_tensor * snapshot = ggml_dup(c,x);
    cache->operation_states.emplace_back(name,snapshot);
    cache->trace_order.push_back(snapshot);
}
static ggml_tensor * ffn(ggml_context * c, const ssl_model & m, ggml_tensor * x,
                         const std::string & p, ssl_dtype dtype, ssl_graph_cache * debug = nullptr) {
    ggml_tensor * norm = ln(c,m,x,p + "_ln");
    if (debug) capture(c,debug,p + ".norm",norm);
    ggml_tensor * up = linear(c,m,norm,p + ".up",dtype);
    if (debug) capture(c,debug,p + ".up",up);
    ggml_tensor * act = rounded(c,ggml_silu(c,up),dtype);
    if (debug) capture(c,debug,p + ".act",act);
    ggml_tensor * down = linear(c,m,act,p + ".down",dtype);
    if (debug) capture(c,debug,p + ".down",down);
    return down;
}
static ggml_tensor * rows(ggml_context * c, ggml_tensor * x, int start, int count) {
    return ggml_cont(c, ggml_view_2d(c, x, count, x->ne[1], x->nb[1], (size_t) start * x->nb[0]));
}
static ssl_graph_cache * build_graph(const ssl_model & m, int T, bool trace, ssl_dtype dtype) {
    const int D = m.n_embd, H = m.n_head, HD = D / H;
    auto cache = std::make_unique<ssl_graph_cache>();
    cache->frames = T; cache->trace = trace; cache->dtype = dtype;
    cache->valid_data.resize(T); cache->mask_data.resize((size_t) T*T);
    ggml_context * c = ggml_init({16*1024*1024,nullptr,true});
    if (!c) return nullptr;
    cache->ctx = c;
    ggml_tensor * input = ggml_new_tensor_2d(c,GGML_TYPE_F32,160,T);
    ggml_tensor * valid = ggml_new_tensor_2d(c,GGML_TYPE_F32,1,T);
    ggml_tensor * attn_mask = ggml_new_tensor_2d(c,GGML_TYPE_F32,T,T);
    ggml_tensor * ridx = ggml_new_tensor_1d(c,GGML_TYPE_I32,2*T-1);
    ggml_set_input(input); ggml_set_input(valid); ggml_set_input(attn_mask); ggml_set_input(ridx);
    // This constant is initialized once for the cached graph. INPUT alone only
    // reserves it before execution; retain its storage after the last GET_ROWS
    // so later activations cannot overwrite the indices before graph replay.
    ggml_set_output(ridx);
    cache->input = input; cache->valid = valid; cache->attn_mask = attn_mask;
    ggml_tensor * feature_input = rounded(c,input,dtype);
    ggml_tensor * feature_norm = ln(c,m,feature_input,"fp.ln");
    ggml_tensor * feature_projection = linear(c,m,feature_norm,"fp.proj",dtype);
    ggml_tensor * x = ggml_mul(c,feature_projection,valid);
    if (trace) {
        capture(c,cache.get(),"feature_input",feature_input);
        capture(c,cache.get(),"feature_norm",feature_norm);
        capture(c,cache.get(),"feature_projection",feature_projection);
    }
    // Diagnostic copies pin the actual value even if the backend reuses storage.
    std::vector<ggml_tensor *> states = { trace ? ggml_dup(c, x) : x };
    if (trace) cache->trace_order.push_back(states.back());
    for (int l = 0; l < m.n_layer; ++l) {
        std::string p = "enc." + std::to_string(l) + ".";
        ssl_graph_cache * debug = trace && l == 0 ? cache.get() : nullptr;
        auto capture0 = [&](const char * name, ggml_tensor * value) {
            if (debug) capture(c,debug,name,value);
        };
        auto capture_attention = [&](const char * name, ggml_tensor * value) {
            if (debug) {
                ggml_tensor * time_major = ggml_cont(c,ggml_permute(c,value,0,2,1,3));
                capture(c,debug,name,ggml_reshape_2d(c,time_major,time_major->ne[0]*H,T));
            }
        };
        // Only the first layer's residual is BF16. final_layer_norm is autocast
        // F32, so subsequent residual additions promote their BF16 branch to F32.
        const ssl_dtype residual_dtype = l == 0 ? dtype : ssl_dtype::f32;
        x = rounded(c, ggml_add(c, x, rounded(c, ggml_scale(c, ffn(c, m, x, p + "ffn1", dtype, debug), 0.5f), dtype)), residual_dtype);
        capture0("enc.0.residual.ffn1",x);
        ggml_tensor * an = ln(c, m, x, p + "attn_ln");
        capture0("enc.0.attn.norm",an);
        auto proj = [&](const char * name) {
            ggml_tensor * projected = linear(c,m,an,p + "attn." + name,dtype);
            if (debug) capture(c,debug,p + "attn." + name,projected);
            return ggml_reshape_3d(c,projected,HD,H,T);
        };
        ggml_tensor * q = proj("q"), * k = proj("k"), * v = proj("v");
        ggml_tensor * qp = ggml_cont(c, ggml_permute(c, q, 0, 2, 1, 3));
        ggml_tensor * kp = ggml_cont(c, ggml_permute(c, k, 0, 2, 1, 3));
        const float scale = 1.0f / std::sqrt((float) HD);
        // torch.matmul -> division, and einsum -> division, are separate BF16
        // results before the score sum. Scaling after summing changes the teacher.
        ggml_tensor * content = rounded(c,matmul(c,kp,qp,dtype),dtype);
        capture_attention("enc.0.attn.content",content);
        ggml_tensor * score = rounded(c,ggml_scale(c,content,scale),dtype);
        ggml_tensor * rel_raw = rounded(c,matmul(c,m.get(p + "attn.dist"),qp,dtype),dtype);
        ggml_tensor * rel = rounded(c,ggml_scale(c,rel_raw,scale),dtype);
        ggml_tensor * relT = ggml_cont(c, ggml_transpose(c, ggml_reshape_2d(c, rel, m.rel_left + m.rel_right + 1, T * H)));
        ggml_tensor * full = ggml_cont(c, ggml_transpose(c, ggml_get_rows(c, relT, ridx)));
        ggml_tensor * bias = ggml_cont(c, ggml_view_3d(c, full, T, T, H,
            (size_t) std::max(1, 2 * T - 2) * sizeof(float), (size_t) T * (2 * T - 1) * sizeof(float),
            (size_t) (T - 1) * sizeof(float)));
        if (debug) {
            ggml_tensor * rawT = ggml_cont(c,ggml_transpose(c,ggml_reshape_2d(c,rel_raw,m.rel_left + m.rel_right + 1,T*H)));
            ggml_tensor * raw_full = ggml_cont(c,ggml_transpose(c,ggml_get_rows(c,rawT,ridx)));
            ggml_tensor * raw_bias = ggml_view_3d(c,raw_full,T,T,H,
                (size_t) std::max(1,2*T-2)*sizeof(float),(size_t) T*(2*T-1)*sizeof(float),(size_t) (T-1)*sizeof(float));
            capture_attention("enc.0.attn.relative",raw_bias);
        }
        score = rounded(c, ggml_add(c, score, bias), dtype);
        score = rounded(c, ggml_add(c, score, attn_mask), dtype);
        capture_attention("enc.0.attn.masked_scores",score);
        score = ggml_soft_max(c, score);
        capture_attention("enc.0.attn.probs",score);
        ggml_tensor * vp = ggml_cont(c, ggml_permute(c, v, 1, 2, 0, 3));
        ggml_tensor * attn = ggml_cont(c, ggml_permute(c, rounded(c, matmul(c, vp, score, dtype), dtype), 0, 2, 1, 3));
        ggml_tensor * attn_flat = ggml_reshape_2d(c,attn,D,T);
        capture0("enc.0.attn.value",attn_flat);
        ggml_tensor * attn_out = linear(c,m,attn_flat,p + "attn.o",dtype);
        capture0("enc.0.attn.o",attn_out);
        x = rounded(c,ggml_add(c,x,attn_out),residual_dtype);
        capture0("enc.0.residual.attn",x);
        ggml_tensor * conv_norm = ln(c,m,x,p + "conv_ln");
        capture0("enc.0.conv.norm",conv_norm);
        ggml_tensor * conv = ggml_mul(c,conv_norm,valid);
        conv = linear(c,m,conv,p + "conv.pw1",dtype,false);
        capture0("enc.0.conv.pw1",conv);
        // nn.GLU computes sigmoid and multiplication in opmath, rounds only its output.
        conv = rounded(c, ggml_mul(c, rows(c, conv, 0, D), ggml_sigmoid(c, rows(c, conv, D, D))), dtype);
        capture0("enc.0.conv.glu",conv);
        ggml_tensor * padded = ggml_cont(c, ggml_pad_ext(c, ggml_cont(c, ggml_transpose(c, conv)),
                                                          m.conv_kernel - 1, 0, 0, 0, 0, 0, 0, 0));
        if (m.is_gpu && m.conv_kernel == 31) {
            // CUDA's ssm_conv only supports kernels up to 15. These shifted time-major
            // windows compute the same causal depthwise dot product without that kernel.
            ggml_tensor * w = m.get(p + "conv.dw.weight");
            ggml_tensor * sum = nullptr;
            for (int j = 0; j < m.conv_kernel; ++j) {
                ggml_tensor * window = ggml_view_2d(c, padded, T, D, padded->nb[1], (size_t) j * padded->nb[0]);
                ggml_tensor * coeff = ggml_view_2d(c, w, 1, D, w->nb[1], (size_t) j * w->nb[0]);
                ggml_tensor * term = ggml_mul(c, window, coeff);
                sum = sum ? ggml_add(c, sum, term) : term;
            }
            conv = ggml_cont(c, ggml_transpose(c, sum));
        } else {
            conv = ggml_reshape_2d(c, ggml_ssm_conv(c,
                ggml_reshape_3d(c, padded, T + m.conv_kernel - 1, D, 1),
                m.get(p + "conv.dw.weight")), D, T);
        }
        conv = rounded(c, conv, dtype);
        capture0("enc.0.conv.dw",conv);
        // Depthwise LayerNorm and its SiLU consume/produce F32; autocast narrows
        // only the following pointwise convolution's operands.
        conv = ln(c,m,conv,p + "conv.dw_ln");
        capture0("enc.0.conv.dw_norm",conv);
        conv = ggml_silu(c,conv);
        capture0("enc.0.conv.act",conv);
        conv = linear(c,m,conv,p + "conv.pw2",dtype,false);
        capture0("enc.0.conv.pw2",conv);
        x = rounded(c,ggml_add(c,x,conv),residual_dtype);
        capture0("enc.0.residual.conv",x);
        x = rounded(c,ggml_add(c,x,rounded(c,ggml_scale(c,ffn(c,m,x,p + "ffn2",dtype,debug),0.5f),dtype)),residual_dtype);
        capture0("enc.0.residual.ffn2",x);
        x = ln(c, m, x, p + "final_ln");
        states.push_back(trace ? ggml_dup(c, x) : x);
        if (trace) cache->trace_order.push_back(states.back());
    }
    ggml_set_output(x);
    if (trace) for (auto * state : states) ggml_set_output(state);
    if (trace) for (const auto & state : cache->operation_states) ggml_set_output(state.second);
    cache->states = std::move(states);
    cache->graph = ggml_new_graph_custom(c,16384,false);
    // Expand snapshots in execution order, not after the full graph: an earlier
    // state may otherwise have been reused before its diagnostic copy executes.
    if (trace) for (auto * state : cache->trace_order) ggml_build_forward_expand(cache->graph,state);
    ggml_build_forward_expand(cache->graph,x);
    cache->alloc = ggml_gallocr_new(m.buft);
    if (!ggml_gallocr_alloc_graph(cache->alloc,cache->graph)) {
        fprintf(stderr,"ssl: graph allocation failed\n"); return nullptr;
    }
    std::vector<int32_t> idx(2*T-1);
    for (int j = 0; j < 2*T-1; ++j) idx[j] = std::clamp(j-(T-1),-m.rel_left,m.rel_right) + m.rel_left;
    ggml_backend_tensor_set(ridx,idx.data(),0,idx.size()*sizeof(int32_t));
    return cache.release();
}
static bool encode(const ssl_model & m, const float * feats, int T, const int32_t * mask,
                   std::vector<float> * out, std::vector<std::vector<float>> * states, ssl_dtype dtype,
                   std::map<std::string,std::vector<float>> * projection_states = nullptr) {
    if (out) out->clear();
    if (states) states->clear();
    if (projection_states) projection_states->clear();
    if (!feats || T <= 0 || T > 3000 || !m.backend) return false;
    std::lock_guard<std::mutex> lock(m.graph_mutex);
    const bool trace = states != nullptr;
    if (!m.graph_cache || m.graph_cache->frames != T || m.graph_cache->trace != trace || m.graph_cache->dtype != dtype) {
        delete m.graph_cache;
        m.graph_cache = build_graph(m,T,trace,dtype);
        if (!m.graph_cache) return false;
    }
    auto & g = *m.graph_cache;
    bool any = false;
    for (int i = 0; i < T; ++i) {
        if (mask && mask[i] != 0 && mask[i] != 1) return false;
        g.valid_data[i] = !mask || mask[i] ? 1.0f : 0.0f;
        any |= g.valid_data[i] != 0;
    }
    if (!any) return false;
    for (int q = 0; q < T; ++q)
        for (int k = 0; k < T; ++k)
            g.mask_data[(size_t) q*T+k] = g.valid_data[k] ? 0.0f :
                (dtype == ssl_dtype::bf16 ? -ggml_bf16_to_fp32(ggml_bf16_t{0x7f7f}) : -FLT_MAX);
    ggml_backend_tensor_set(g.input,feats,0,(size_t) T*160*sizeof(float));
    ggml_backend_tensor_set(g.valid,g.valid_data.data(),0,g.valid_data.size()*sizeof(float));
    ggml_backend_tensor_set(g.attn_mask,g.mask_data.data(),0,g.mask_data.size()*sizeof(float));
    if (ggml_backend_graph_compute(m.backend,g.graph) != GGML_STATUS_SUCCESS) {
        fprintf(stderr,"ssl: compute failed\n"); return false;
    }
    if (out) {
        out->resize((size_t) T*1024);
        ggml_backend_tensor_get(g.states.back(),out->data(),0,out->size()*sizeof(float));
    }
    if (states) {
        states->resize(g.states.size());
        for (size_t i = 0; i < g.states.size(); ++i) {
            (*states)[i].resize((size_t) T*1024);
            ggml_backend_tensor_get(g.states[i],(*states)[i].data(),0,(*states)[i].size()*sizeof(float));
        }
    }
    if (projection_states) {
        for (const auto & state : g.operation_states) {
            auto & values = (*projection_states)[state.first];
            values.resize((size_t) ggml_nelements(state.second));
            ggml_backend_tensor_get(state.second,values.data(),0,values.size()*sizeof(float));
        }
    }
    return true;
}
bool ssl_encode_features(const ssl_model & m, const float * feats, int T, std::vector<float> & out,
                         ssl_dtype dtype) {
    return encode(m,feats,T,nullptr,&out,nullptr,dtype);
}
bool ssl_encode_features(const ssl_model & m, const float * feats, int T, const int32_t * mask,
                         std::vector<float> & out, ssl_dtype dtype) {
    return encode(m,feats,T,mask,&out,nullptr,dtype);
}
bool ssl_encode_features_trace(const ssl_model & m, const float * feats, int T, const int32_t * mask,
                               std::vector<std::vector<float>> & states, ssl_dtype dtype,
                               std::map<std::string,std::vector<float>> * projection_states) {
    return encode(m,feats,T,mask,nullptr,&states,dtype,projection_states);
}
bool ssl_features_pcm(const ssl_model & m, const float * audio, int n, int sample_rate,
                      std::vector<float> & feats, int & frames, std::vector<int32_t> & mask) {
    feats.clear(); frames = 0; mask.clear();
    if (!audio || n <= 0 || !m.backend || (sample_rate != 44100 && sample_rate != 16000) ||
        n > (int64_t) sample_rate*m.max_seconds) return false;
    if (sample_rate == 16000) return ssl_features(m,audio,n,feats,frames,mask);
    std::vector<float> wav = m.resample.run(audio,n);
    return ssl_features(m,wav.data(),(int) wav.size(),feats,frames,mask);
}
bool ssl_encode_pcm(const ssl_model & m, const float * audio, int n, int sample_rate,
                    std::vector<float> & out, int & frames, ssl_dtype dtype) {
    out.clear();
    std::vector<float> feats; std::vector<int32_t> mask;
    if (!ssl_features_pcm(m,audio,n,sample_rate,feats,frames,mask)) return false;
    return ssl_encode_features(m,feats.data(),frames,mask.data(),out,dtype);
}
