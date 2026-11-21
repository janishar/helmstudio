// ZONOS3 Mini speaker encoder (ownmect_s2): host Kaldi fbank + MECT-B2 + ASTP as one ggml graph.
// The GGUF layout and data flow are documented in `tools/convert_gguf.py speaker-encoder --help`.
#include "compat.h"
#include "backend-device.h"
#include "spk-encoder.h"
#include "ggml-cpu.h"
#include "gguf.h"
#define POCKETFFT_NO_MULTITHREADING
#include "pocketfft_hdronly.hpp"
#include <algorithm>
#include <cmath>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <set>
#include <thread>

struct spk_fft {
    pocketfft::detail::pocketfft_r<float> plan;
    explicit spk_fft(size_t n) : plan(n) {}
};

namespace {

constexpr int kHidden = 64, kFeat = 2560, kInC = 32, kInF = 80, kBins = 257;
constexpr int kMaxSamples16k = 60 * 16000;

// MECT_B2 (backbones/mect.py:759-780): per block (cnn depth, out channels, stride F, stride T, expansion).
struct block_cfg { int cnn, out_ch, s_f, s_t, expansion; };
const std::vector<std::vector<block_cfg>> & mect_b2() {
    static const std::vector<std::vector<block_cfg>> cfg = {
        std::vector<block_cfg>(2, {3, 32, 1, 1, 4}),
        [] { std::vector<block_cfg> v = {{3, 64, 2, 1, 2}}; v.insert(v.end(), 3, {3, 64, 1, 2, 2}); return v; }(),
        [] { std::vector<block_cfg> v = {{3, 128, 2, 1, 2}}; v.insert(v.end(), 4, {3, 128, 1, 2, 2}); return v; }(),
        [] { std::vector<block_cfg> v = {{3, 256, 2, 1, 1}}; v.insert(v.end(), 2, {2, 256, 1, 2, 1}); return v; }(),
    };
    return cfg;
}

std::string bname(int stage, int k) { return "fe.b" + std::to_string(stage + 1) + "." + std::to_string(k) + "."; }

// Number of grouped-conv input slots (w0, w1, ...) stored under `prefix`.
int count_slots(const std::map<std::string, ggml_tensor *> & t, const std::string & prefix) {
    int a = 0;
    while (t.count(prefix + ".w" + std::to_string(a))) ++a;
    return a;
}

} // namespace

bool spk_load(spk_model & m, const char * path, bool use_gpu) {
    spk_free(m);
#ifdef _WIN32
    _putenv_s("NVIDIA_TF32_OVERRIDE", "0");
#else
    setenv("NVIDIA_TF32_OVERRIDE", "0", 1);
#endif
    ggml_context * meta = nullptr;
    gguf_context * gf = gguf_init_from_file(path, { true, &meta });
    if (!gf) { fprintf(stderr, "spk: cannot open GGUF '%s'\n", path); return false; }
    auto fail = [&]() { gguf_free(gf); ggml_free(meta); return false; };
    int64_t arch = gguf_find_key(gf, "general.architecture");
    if (arch < 0 || gguf_get_kv_type(gf, arch) != GGUF_TYPE_STRING ||
        std::strcmp(gguf_get_val_str(gf, arch), "mect_spk")) {
        fprintf(stderr, "spk: expected mect_spk (tools/convert_gguf.py speaker-encoder)\n");
        return fail();
    }
    auto u32 = [&](const char * key, int & out, int expected) {
        const std::string full = std::string("mect_spk.") + key;
        int64_t id = gguf_find_key(gf, full.c_str());
        if (id < 0 || gguf_get_kv_type(gf, id) != GGUF_TYPE_UINT32) return false;
        out = (int) gguf_get_val_u32(gf, id);
        return out == expected;
    };
    auto f32 = [&](const char * key, float & out, float expected) {
        const std::string full = std::string("mect_spk.") + key;
        int64_t id = gguf_find_key(gf, full.c_str());
        if (id < 0 || gguf_get_kv_type(gf, id) != GGUF_TYPE_FLOAT32) return false;
        out = gguf_get_val_f32(gf, id);
        return out == expected;
    };
    bool backbone_ok = false;
    {
        int64_t id = gguf_find_key(gf, "mect_spk.backbone");
        backbone_ok = id >= 0 && gguf_get_kv_type(gf, id) == GGUF_TYPE_STRING &&
                      !std::strcmp(gguf_get_val_str(gf, id), "MECT_B2");
    }
    if (!(backbone_ok &&
          u32("sample_rate", m.sample_rate, 16000) && u32("max_seconds", m.max_seconds, 60) &&
          u32("embedding_length", m.embedding_length, 192) && u32("n_mels", m.n_mels, 80) &&
          u32("frame_length", m.frame_length, 400) && u32("frame_shift", m.frame_shift, 160) &&
          u32("n_fft", m.n_fft, 512) && u32("n_heads", m.n_heads, 4) &&
          u32("n_experts", m.n_experts, 4) && u32("pos_kernel", m.pos_kernel, 113) &&
          f32("preemphasis", m.preemph, 0.97f) && f32("waveform_scale", m.waveform_scale, 32768.0f) &&
          f32("log_floor", m.log_floor, 1.1920928955078125e-07f) && f32("ln_eps", m.ln_eps, 1e-6f) &&
          f32("pool_var_floor", m.pool_var_floor, 1e-7f))) {
        fprintf(stderr, "spk: unsupported or incomplete MECT-B2 speaker encoder metadata\n");
        return fail();
    }

    // Expected tensor set, derived from the MECT_B2 geometry; every tensor must be F32 with this shape.
    std::map<std::string, std::vector<int64_t>> expected;
    auto shape = [&](const std::string & name, std::initializer_list<int64_t> dims) { expected[name] = dims; };
    auto pair = [&](const std::string & p, std::vector<int64_t> w, int64_t bias) {
        expected[p + ".weight"] = std::move(w); shape(p + ".bias", {bias});
    };
    auto bn = [&](const std::string & p, int64_t c) { shape(p + ".scale", {c}); shape(p + ".shift", {c}); };
    auto ln = [&](const std::string & p, int64_t c) { shape(p + ".weight", {c}); shape(p + ".bias", {c}); };
    pair("fe.head.conv", {3, 3, 1, kInC}, kInC); bn("fe.head.bn", kInC);
    std::map<std::string, ggml_tensor *> meta_t;
    for (ggml_tensor * t = ggml_get_first_tensor(meta); t; t = ggml_get_next_tensor(meta, t))
        meta_t[ggml_get_name(t)] = t;
    bool geometry_ok = true;
    {
        int c_in = kInC, f_in = kInF;
        const auto & stages = mect_b2();
        for (int s = 0; s < (int) stages.size() && geometry_ok; ++s) {
            for (int k = 0; k < (int) stages[s].size(); ++k) {
                const block_cfg & b = stages[s][k];
                const std::string p = bname(s, k);
                const int oc = b.out_ch * b.expansion;
                for (int r = 0; r < b.cnn; ++r) {
                    const int ic = r == 0 ? c_in : oc;
                    const std::string q = p + "cnn." + std::to_string(r) + ".";
                    for (const char * conv : {"conv1", "conv2"}) {
                        const int a = count_slots(meta_t, q + conv);
                        if (a < 1 || ic % a) { geometry_ok = false; break; }
                        for (int j = 0; j < a; ++j) shape(q + conv + ".w" + std::to_string(j), {3, 3, 1, oc});
                    }
                    if (!geometry_ok) break;
                    pair(q + "pw1", {1, 1, oc, oc}, oc); pair(q + "pw2", {1, 1, oc, oc}, oc);
                    bn(q + "bn1", oc); bn(q + "bn2", oc);
                    if (r == 0) { shape(q + "sc.weight", {1, 1, ic, oc}); bn(q + "sc.bn", oc); }
                }
                if (!geometry_ok) break;
                const int c_out = b.expansion > 1 ? c_in * b.s_f : oc;
                if (b.expansion > 1) { pair(p + "reduce", {1, 1, oc, c_out}, c_out); bn(p + "reduce.bn", c_out); }
                const int f_out = f_in / b.s_f;
                if ((int64_t) c_out * f_out != kFeat || (int64_t) c_in * f_in != kFeat) { geometry_ok = false; break; }
                pair(p + "rproj", {kFeat, kHidden}, kHidden); ln(p + "rproj.ln", kHidden);
                pair(p + "pos.dw", {113, kHidden}, kHidden); bn(p + "pos.bn", kHidden);
                pair(p + "pos.pw", {kHidden, kHidden}, kHidden);
                for (const char * q : {"q", "k", "v", "o"})
                    pair(p + "attn." + q, {kHidden, kHidden}, kHidden);
                ln(p + "ln1", kHidden); ln(p + "ln2", kHidden);
                shape(p + "moe.gate.weight", {kHidden, 4}); shape(p + "moe.experts.weight", {kHidden, 4 * kHidden});
                pair(p + "ffn_out", {kHidden, kHidden}, kHidden);
                pair(p + "eproj", {kHidden, kFeat}, kFeat);
                c_in = c_out; f_in = f_out;
            }
        }
    }
    if (!geometry_ok) { fprintf(stderr, "spk: tensor layout does not match MECT_B2\n"); return fail(); }
    shape("fe.dense_w", {kFeat, 4});
    pair("pool.l1", {kFeat, 128}, 128); pair("pool.l2", {128, kFeat}, kFeat);
    bn("bn", 2 * kFeat); shape("linear.weight", {2 * kFeat, 192});
    shape("frontend.window", {400}); shape("frontend.mel_banks", {kBins, 80});
    shape("frontend.resample_44100", {resample_16k::kernel_size, resample_16k::rate});
    if (gguf_get_n_tensors(gf) != (int64_t) expected.size()) {
        fprintf(stderr, "spk: tensor count %lld does not match MECT-B2 architecture (%zu)\n",
                (long long) gguf_get_n_tensors(gf), expected.size());
        return fail();
    }
    for (const auto & item : expected) {
        ggml_tensor * t = ggml_get_tensor(meta, item.first.c_str());
        if (!t) { fprintf(stderr, "spk: missing %s\n", item.first.c_str()); return fail(); }
        for (int d = 0; d < GGML_MAX_DIMS; ++d) {
            const int64_t ne = d < (int) item.second.size() ? item.second[d] : 1;
            if (t->ne[d] != ne) { fprintf(stderr, "spk: bad shape: %s\n", item.first.c_str()); return fail(); }
        }
        if (t->type != GGML_TYPE_F32) { fprintf(stderr, "spk: %s must be F32\n", item.first.c_str()); return fail(); }
    }

    ggml_backend_dev_t cpu_dev = ggml_backend_dev_by_type(GGML_BACKEND_DEVICE_TYPE_CPU);
    if (!cpu_dev) return fail();
    ggml_backend_dev_t dev = zonos3_backend_device(use_gpu);
    m.is_gpu = zonos3_backend_is_gpu(dev);
    m.backend_cpu = ggml_backend_dev_init(cpu_dev, nullptr);
    if (!m.backend_cpu) return fail();
    {
        // Many-core hosts: spinning ggml worker threads beyond ~64 only add barrier cost for these small ops.
        const char * th = getenv("SPK_THREADS");
        ggml_backend_cpu_set_n_threads(m.backend_cpu, th ? std::max(1, atoi(th)) : (int) std::clamp(std::thread::hardware_concurrency(), 1u, 64u));
    }
    m.backend = m.is_gpu ? ggml_backend_dev_init(dev, nullptr) : m.backend_cpu;
    if (!m.backend) { gguf_free(gf); ggml_free(meta); spk_free(m); return false; }
    m.ctx_w = meta;
    m.buf_w = ggml_backend_alloc_ctx_tensors(meta, m.backend);
    if (!m.buf_w) { gguf_free(gf); spk_free(m); return false; }
    FILE * f = fopen(path, "rb");
    if (!f) { gguf_free(gf); spk_free(m); return false; }
    std::vector<char> tmp;
    bool ok = true;
    for (ggml_tensor * t = ggml_get_first_tensor(meta); t; t = ggml_get_next_tensor(meta, t)) {
        const char * name = ggml_get_name(t);
        const int64_t tid = gguf_find_tensor(gf, name);
        const size_t nb = ggml_nbytes(t);
        tmp.resize(nb);
        if (tid < 0 || fseeko(f, gguf_get_data_offset(gf) + gguf_get_tensor_offset(gf, tid), SEEK_SET) ||
            fread(tmp.data(), 1, nb, f) != nb) {
            fprintf(stderr, "spk: read failed: %s\n", name); ok = false; break;
        }
        ggml_backend_tensor_set(t, tmp.data(), 0, nb);
        m.t[name] = t;
    }
    fclose(f); gguf_free(gf);
    if (!ok) { spk_free(m); return false; }

    m.window.resize(400);
    m.mel_banks.resize((size_t) 80 * kBins);
    std::vector<float> kernel((size_t) resample_16k::rate * resample_16k::kernel_size);
    ggml_backend_tensor_get(m.get("frontend.window"), m.window.data(), 0, m.window.size() * sizeof(float));
    ggml_backend_tensor_get(m.get("frontend.mel_banks"), m.mel_banks.data(), 0, m.mel_banks.size() * sizeof(float));
    ggml_backend_tensor_get(m.get("frontend.resample_44100"), kernel.data(), 0, kernel.size() * sizeof(float));
    m.resample.init(std::move(kernel));
    m.fft = std::make_shared<const spk_fft>((size_t) m.n_fft);

    std::vector<ggml_backend_t> backends;
    if (m.is_gpu) backends.push_back(m.backend);
    backends.push_back(m.backend_cpu);
    m.sched = ggml_backend_sched_new(backends.data(), nullptr, (int) backends.size(), 32768, false, true);
    if (!m.sched) { spk_free(m); return false; }
    fprintf(stderr, "spk: loaded %zu tensors on %s (MECT-B2, 192-d)\n", m.t.size(), m.is_gpu ? "GPU" : "CPU");
    return true;
}

void spk_free(spk_model & m) {
    std::lock_guard<std::mutex> lock(m.mutex);
    if (m.sched) ggml_backend_sched_free(m.sched);
    if (m.buf_w) ggml_backend_buffer_free(m.buf_w);
    if (m.ctx_w) ggml_free(m.ctx_w);
    if (m.backend && m.backend != m.backend_cpu) ggml_backend_free(m.backend);
    if (m.backend_cpu) ggml_backend_free(m.backend_cpu);
    m.sched = nullptr; m.buf_w = nullptr; m.ctx_w = nullptr; m.backend = nullptr; m.backend_cpu = nullptr;
    m.t.clear(); m.window.clear(); m.mel_banks.clear(); m.resample.clear(); m.fft.reset();
    m.is_gpu = false;
}

namespace {

// Kaldi fbank (torchaudio.compliance.kaldi.fbank, fp32) of the first <= 60 s of 16 kHz audio -> [T,80].
bool fbank_host(const spk_model & m, const float * wav, int64_t n, std::vector<float> & out, int & T) {
    n = std::min<int64_t>(n, kMaxSamples16k);
    if (n < m.frame_length) { fprintf(stderr, "spk: audio shorter than 25 ms\n"); return false; }
    T = (int) (1 + (n - m.frame_length) / m.frame_shift);
    out.assign((size_t) T * m.n_mels, 0.0f);
    const int fl = m.frame_length, nf = m.n_fft, bins = nf / 2 + 1;
    std::vector<float> x(fl), buf(nf), power(bins);
    for (int t = 0; t < T; ++t) {
        const float * src = wav + (int64_t) t * m.frame_shift;
        double mean = 0.0;
        for (int j = 0; j < fl; ++j) { x[j] = src[j] * m.waveform_scale; mean += x[j]; }
        const float fmean = (float) (mean / fl);
        for (int j = 0; j < fl; ++j) x[j] -= fmean;
        std::fill(buf.begin(), buf.end(), 0.0f);
        for (int j = fl - 1; j >= 1; --j) buf[j] = (x[j] - m.preemph * x[j - 1]) * m.window[j];
        buf[0] = (x[0] - m.preemph * x[0]) * m.window[0];
        m.fft->plan.exec(buf.data(), 1.0f, true);
        power[0] = buf[0] * buf[0];
        for (int k = 1; k < bins - 1; ++k) power[k] = buf[2 * k - 1] * buf[2 * k - 1] + buf[2 * k] * buf[2 * k];
        power[bins - 1] = buf[nf - 1] * buf[nf - 1];
        for (int b = 0; b < m.n_mels; ++b) {
            const float * row = m.mel_banks.data() + (size_t) b * bins;
            float acc = 0.0f;
            for (int k = 0; k < bins; ++k) acc += row[k] * power[k];
            out[(size_t) t * m.n_mels + b] = std::log(std::max(acc, m.log_floor));
        }
    }
    return true;
}

struct builder {
    ggml_context * c;
    const spk_model & m;
    bool ok = true;
    ggml_tensor * W(const std::string & name) {
        ggml_tensor * t = m.get(name);
        if (!t) ok = false;
        return t;
    }
    // [C] vector as a per-channel broadcast over a WHCN activation.
    ggml_tensor * chan(ggml_tensor * v) { return ggml_reshape_4d(c, v, 1, 1, v->ne[0], 1); }
    ggml_tensor * bn4(ggml_tensor * x, const std::string & p) {
        return ggml_add(c, ggml_mul(c, x, chan(W(p + ".scale"))), chan(W(p + ".shift")));
    }
    ggml_tensor * conv(ggml_tensor * x, const std::string & p, int s_t, int s_f, bool bias, int pad = 0) {
        ggml_tensor * y = ggml_conv_2d_direct(c, W(p + ".weight"), x, s_t, s_f, pad, pad, 1, 1);
        return bias ? ggml_add(c, y, chan(W(p + ".bias"))) : y;
    }
    // torch Conv2d(3x3, pad 1, groups=G, bias=False), weight split into `a` input slots (see converter header).
    ggml_tensor * grouped3x3(ggml_tensor * x, const std::string & p, int s_t, int s_f) {
        const int a = count_slots(m.t, p);
        const int64_t T = x->ne[0], F = x->ne[1], IC = x->ne[2];
        const int64_t oc = W(p + ".w0")->ne[3], G = IC / a, b = oc / G;
        ggml_tensor * x4 = ggml_reshape_4d(c, x, T, F, a, G);
        ggml_tensor * y = nullptr;
        for (int j = 0; j < a; ++j) {
            ggml_tensor * xj = x;
            if (a > 1 || b > 1) {
                ggml_tensor * slot = ggml_view_4d(c, x4, T, F, 1, G, x4->nb[1], x4->nb[2], x4->nb[3], (size_t) j * x4->nb[2]);
                ggml_tensor * shape_like = ggml_new_tensor_4d(c, GGML_TYPE_F32, T, F, b, G);
                xj = ggml_reshape_3d(c, ggml_repeat(c, slot, shape_like), T, F, oc);
            }
            ggml_tensor * term = ggml_conv_2d_dw_direct(c, W(p + ".w" + std::to_string(j)), xj, s_t, s_f, 1, 1, 1, 1);
            y = y ? ggml_add(c, y, term) : term;
        }
        return y;
    }
    ggml_tensor * res_block(ggml_tensor * x, const std::string & q, int s_f, int s_t) {
        ggml_tensor * t = grouped3x3(x, q + "conv1", s_t, s_f);
        t = ggml_relu(c, bn4(conv(t, q + "pw1", 1, 1, true), q + "bn1"));
        t = grouped3x3(t, q + "conv2", 1, 1);
        t = ggml_relu(c, bn4(conv(t, q + "pw2", 1, 1, true), q + "bn2"));
        ggml_tensor * sc = x;
        if (m.t.count(q + "sc.weight")) sc = bn4(conv(x, q + "sc", s_t, s_f, false), q + "sc.bn");
        return ggml_add(c, t, sc);
    }
    ggml_tensor * linear(ggml_tensor * x, const std::string & p) {
        return ggml_add(c, ggml_mul_mat(c, W(p + ".weight"), x), W(p + ".bias"));
    }
    ggml_tensor * layer_norm(ggml_tensor * x, const std::string & p) {
        return ggml_add(c, ggml_mul(c, ggml_norm(c, x, m.ln_eps), W(p + ".weight")), W(p + ".bias"));
    }
    ggml_tensor * new_gelu(ggml_tensor * x) {
        ggml_tensor * x3 = ggml_mul(c, ggml_mul(c, x, x), x);
        ggml_tensor * inner = ggml_scale(c, ggml_add(c, x, ggml_scale(c, x3, 0.044715f)), 0.7978845608028654f);
        return ggml_scale(c, ggml_mul(c, x, ggml_scale_bias(c, ggml_tanh(c, inner), 1.0f, 1.0f)), 0.5f);
    }
    // [T,F,C] -> [F*C, T] with k = f*C + c.
    ggml_tensor * flatten(ggml_tensor * x) {
        const int64_t T = x->ne[0], F = x->ne[1], C = x->ne[2];
        return ggml_reshape_2d(c, ggml_cont(c, ggml_permute(c, x, 2, 1, 0, 3)), F * C, T);
    }
    // [F*C, T] -> [T, F, C]
    ggml_tensor * unflatten(ggml_tensor * x, int64_t F, int64_t C) {
        return ggml_cont(c, ggml_permute(c, ggml_reshape_3d(c, x, C, F, x->ne[1]), 2, 1, 0, 3));
    }
    ggml_tensor * attention(ggml_tensor * h, const std::string & p) {
        const int64_t T = h->ne[1];
        const int nh = m.n_heads, hd = kHidden / m.n_heads;
        ggml_tensor * q = ggml_scale(c, linear(h, p + "attn.q"), 1.0f / std::sqrt((float) hd));
        ggml_tensor * k = linear(h, p + "attn.k"), * v = linear(h, p + "attn.v");
        auto heads = [&](ggml_tensor * z) { // [64,T] -> [hd,T,nh]
            return ggml_cont(c, ggml_permute(c, ggml_reshape_3d(c, z, hd, nh, T), 0, 2, 1, 3));
        };
        ggml_tensor * qp = heads(q), * kp = heads(k);
        ggml_tensor * vt = ggml_cont(c, ggml_permute(c, heads(v), 1, 0, 2, 3)); // [T,hd,nh]
        ggml_tensor * o = nullptr;
        for (int64_t a = 0; a < T; a += 512) {
            const int64_t nq = std::min<int64_t>(512, T - a);
            ggml_tensor * qb = ggml_view_3d(c, qp, hd, nq, nh, qp->nb[1], qp->nb[2], (size_t) a * qp->nb[1]);
            ggml_tensor * score = ggml_soft_max(c, ggml_mul_mat(c, kp, qb)); // [T,nq,nh]
            ggml_tensor * ob = ggml_mul_mat(c, vt, score);                   // [hd,nq,nh]
            ob = ggml_reshape_2d(c, ggml_cont(c, ggml_permute(c, ob, 0, 2, 1, 3)), kHidden, nq);
            o = o ? ggml_concat(c, o, ob, 1) : ob;
        }
        return linear(o, p + "attn.o");
    }
    ggml_tensor * moe_ffn(ggml_tensor * h, const std::string & p) {
        ggml_tensor * r = ggml_soft_max(c, ggml_mul_mat(c, W(p + "moe.gate.weight"), h)); // [4,T]
        ggml_tensor * e = new_gelu(ggml_mul_mat(c, W(p + "moe.experts.weight"), h));      // [256,T]
        ggml_tensor * mix = nullptr;
        for (int i = 0; i < m.n_experts; ++i) {
            ggml_tensor * ei = ggml_view_2d(c, e, kHidden, e->ne[1], e->nb[1], (size_t) i * kHidden * sizeof(float));
            ggml_tensor * ri = ggml_view_2d(c, r, 1, r->ne[1], r->nb[1], (size_t) i * sizeof(float));
            ggml_tensor * term = ggml_mul(c, ei, ri);
            mix = mix ? ggml_add(c, mix, term) : term;
        }
        return linear(new_gelu(mix), p + "ffn_out");
    }
    ggml_tensor * pos_conv(ggml_tensor * h, const std::string & p) {
        const int K = m.pos_kernel, pad = K / 2;
        const int64_t T = h->ne[1];
        ggml_tensor * hp = ggml_pad_ext(c, h, 0, 0, pad, pad, 0, 0, 0, 0); // [64, T+2*pad]
        ggml_tensor * dw = W(p + "pos.dw.weight");                          // [K,64]
        ggml_tensor * wt = ggml_cont(c, ggml_transpose(c, dw));            // [64,K]
        ggml_tensor * acc = nullptr;
        for (int k = 0; k < K; ++k) {
            ggml_tensor * term = ggml_mul(c, ggml_view_2d(c, hp, kHidden, T, hp->nb[1], (size_t) k * hp->nb[1]),
                                          ggml_view_2d(c, wt, kHidden, 1, wt->nb[1], (size_t) k * wt->nb[1]));
            acc = acc ? ggml_add(c, acc, term) : term;
        }
        acc = ggml_add(c, acc, W(p + "pos.dw.bias"));
        acc = ggml_add(c, ggml_mul(c, acc, W(p + "pos.bn.scale")), W(p + "pos.bn.shift"));
        acc = ggml_gelu_erf(c, acc);
        return linear(acc, p + "pos.pw");
    }
    // One MECT block. x: [T,F,C] spatial and xf its flatten [2560,T]; returns spatial and flat outputs.
    std::pair<ggml_tensor *, ggml_tensor *> block(ggml_tensor * x, ggml_tensor * xf, const block_cfg & b, const std::string & p) {
        const int64_t T = x->ne[0];
        ggml_tensor * y = x;
        for (int r = 0; r < b.cnn; ++r)
            y = res_block(y, p + "cnn." + std::to_string(r) + ".", r == 0 ? b.s_f : 1, r == 0 ? b.s_t : 1);
        if (m.t.count(p + "reduce.weight")) y = bn4(conv(y, p + "reduce", 1, 1, true), p + "reduce.bn");
        const int64_t F2 = y->ne[1], C2 = y->ne[2];
        ggml_tensor * x2 = flatten(y);                                   // [2560,T']
        ggml_tensor * h = layer_norm(linear(x2, p + "rproj"), p + "rproj.ln");
        h = ggml_add(c, h, pos_conv(h, p));
        h = layer_norm(ggml_add(c, h, attention(h, p)), p + "ln1");
        h = layer_norm(ggml_add(c, h, moe_ffn(h, p)), p + "ln2");
        ggml_tensor * e = ggml_add(c, linear(h, p + "eproj"), x2);       // [2560,T']
        if (b.s_t > 1) {
            const int64_t Tp = e->ne[1];
            ggml_tensor * e3 = ggml_reshape_3d(c, e, kFeat, 1, Tp);
            ggml_tensor * rep = ggml_repeat(c, e3, ggml_new_tensor_3d(c, GGML_TYPE_F32, kFeat, b.s_t, Tp));
            e = ggml_reshape_2d(c, rep, kFeat, (int64_t) b.s_t * Tp);
            e = ggml_view_2d(c, e, kFeat, T, e->nb[1], 0);
        }
        ggml_tensor * out = ggml_add(c, e, xf);                          // [2560,T]
        return { unflatten(out, F2, C2), out };
    }
};

} // namespace

bool spk_embed(const spk_model & m, const float * audio, int64_t n, int sample_rate,
               std::vector<float> & embedding, const spk_dump_fn & dump) {
    embedding.clear();
    if (!audio || n <= 0 || !m.sched || (sample_rate != 44100 && sample_rate != 16000)) {
        fprintf(stderr, "spk: need mono float32 audio at 44100 or 16000 Hz\n");
        return false;
    }
    std::vector<float> wav16;
    const float * w = audio;
    if (sample_rate == 44100) { wav16 = m.resample.run(audio, n); w = wav16.data(); n = (int64_t) wav16.size(); }
    std::vector<float> fb;
    int T = 0;
    if (!fbank_host(m, w, n, fb, T)) return false;
    const int nm = m.n_mels;
    if (dump && !dump("fbank", fb.data(), T, nm)) return false;
    // Per-utterance CMN, then transpose to the network's time-fastest layout [T, 80].
    std::vector<float> cmn(fb.size()), x0((size_t) T * nm);
    for (int b = 0; b < nm; ++b) {
        double mean = 0.0;
        for (int t = 0; t < T; ++t) mean += fb[(size_t) t * nm + b];
        const float fmean = (float) (mean / T);
        for (int t = 0; t < T; ++t) {
            const float v = fb[(size_t) t * nm + b] - fmean;
            cmn[(size_t) t * nm + b] = v;
            x0[(size_t) b * T + t] = v;
        }
    }
    if (dump && !dump("fbank_cmn", cmn.data(), T, nm)) return false;

    std::lock_guard<std::mutex> lock(m.mutex);
    constexpr size_t graph_nodes = 32768;
    ggml_init_params ip = { ggml_tensor_overhead() * graph_nodes * 2 + ggml_graph_overhead_custom(graph_nodes, false),
                            nullptr, true };
    ggml_context * ctx = ggml_init(ip);
    if (!ctx) return false;
    ggml_cgraph * graph = ggml_new_graph_custom(ctx, graph_nodes, false);
    builder g{ ctx, m };
    ggml_tensor * input = ggml_new_tensor_4d(ctx, GGML_TYPE_F32, T, nm, 1, 1);
    ggml_set_input(input);

    ggml_tensor * x = g.bn4(g.conv(input, "fe.head.conv", 1, 1, true, 1), "fe.head.bn");
    ggml_tensor * xf = g.flatten(x);
    std::vector<ggml_tensor *> feats;
    const auto & stages = mect_b2();
    for (int s = 0; s < (int) stages.size(); ++s) {
        for (int k = 0; k < (int) stages[s].size(); ++k) {
            auto r = g.block(x, xf, stages[s][k], bname(s, k));
            x = r.first; xf = r.second;
        }
        feats.push_back(xf);
        if (dump) ggml_set_output(xf);
    }
    ggml_tensor * dense = g.W("fe.dense_w");
    ggml_tensor * fused = nullptr;
    for (int s = 0; s < 4; ++s) {
        ggml_tensor * ws = ggml_view_2d(ctx, dense, kFeat, 1, dense->nb[1], (size_t) s * dense->nb[1]);
        ggml_tensor * term = ggml_mul(ctx, feats[s], ws);
        fused = fused ? ggml_add(ctx, fused, term) : term;
    }
    // ASTP over time.
    ggml_tensor * att = g.linear(ggml_tanh(ctx, g.linear(fused, "pool.l1")), "pool.l2");   // [2560,T]
    ggml_tensor * alpha = ggml_soft_max(ctx, ggml_cont(ctx, ggml_transpose(ctx, att)));   // [T,2560]
    ggml_tensor * xt = ggml_cont(ctx, ggml_transpose(ctx, fused));                        // [T,2560]
    ggml_tensor * mean = ggml_sum_rows(ctx, ggml_mul(ctx, alpha, xt));                    // [1,2560]
    ggml_tensor * sq = ggml_sum_rows(ctx, ggml_mul(ctx, alpha, ggml_mul(ctx, xt, xt)));
    ggml_tensor * var = ggml_sub(ctx, sq, ggml_mul(ctx, mean, mean));
    ggml_tensor * stdv = ggml_sqrt(ctx, ggml_clamp(ctx, var, m.pool_var_floor, 3.0e38f));
    ggml_tensor * pooled = ggml_concat(ctx, ggml_reshape_2d(ctx, mean, kFeat, 1), ggml_reshape_2d(ctx, stdv, kFeat, 1), 0); // [5120,1]
    ggml_set_output(pooled);
    ggml_tensor * pooled_bn = ggml_add(ctx, ggml_mul(ctx, pooled, g.W("bn.scale")), g.W("bn.shift"));
    ggml_tensor * emb = ggml_mul_mat(ctx, g.W("linear.weight"), pooled_bn);               // [192,1]
    ggml_set_output(emb);
    if (!g.ok) { ggml_free(ctx); return false; }
    ggml_build_forward_expand(graph, emb);
    // Keep fp32 GEMM accumulation on every backend.
    for (int i = 0; i < ggml_graph_n_nodes(graph); ++i)
        if (ggml_graph_node(graph, i)->op == GGML_OP_MUL_MAT) ggml_mul_mat_set_prec(ggml_graph_node(graph, i), GGML_PREC_F32);

    ggml_backend_sched_reset(m.sched);
    if (!ggml_backend_sched_alloc_graph(m.sched, graph)) {
        fprintf(stderr, "spk: graph allocation failed (%d frames)\n", T);
        ggml_free(ctx); return false;
    }
    ggml_backend_tensor_set(input, x0.data(), 0, x0.size() * sizeof(float));
    if (ggml_backend_sched_graph_compute(m.sched, graph) != GGML_STATUS_SUCCESS) {
        fprintf(stderr, "spk: compute failed\n");
        ggml_free(ctx); return false;
    }
    bool ok = true;
    if (dump) {
        std::vector<float> buf;
        for (int s = 0; s < 4 && ok; ++s) {
            buf.resize((size_t) kFeat * T);
            ggml_backend_tensor_get(feats[s], buf.data(), 0, buf.size() * sizeof(float));
            ok = dump(("feat_" + std::to_string(s + 1)).c_str(), buf.data(), T, kFeat);
        }
        buf.resize(2 * kFeat);
        ggml_backend_tensor_get(pooled, buf.data(), 0, buf.size() * sizeof(float));
        ok = ok && dump("pooled", buf.data(), 1, 2 * kFeat);
    }
    embedding.resize(m.embedding_length);
    ggml_backend_tensor_get(emb, embedding.data(), 0, embedding.size() * sizeof(float));
    ggml_free(ctx);
    if (ok && dump) ok = dump("embedding", embedding.data(), 1, (int) embedding.size());
    if (!ok) { embedding.clear(); return false; }
    for (float v : embedding) if (!std::isfinite(v)) { fprintf(stderr, "spk: non-finite embedding\n"); embedding.clear(); return false; }
    return true;
}
