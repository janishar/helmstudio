// zac-cli — decode mossdual audio codes [T,10] (15 Hz) to 44.1 kHz WAV or f32 NPY.
// Reads <codes>.eos.npy automatically if present: [eos] or [eos, prefill_frames]. The first
// `skip` frames (--skip-frames, default prefill_frames) are decoded but their audio is dropped.
//
//   zac-cli <zac.gguf> <codes.npy> <out.wav> [--eos N] [--skip-frames N] [--headroom-db X] [--gpu|--cpu]
//   zac-cli --seam <zac.gguf> <codes.npy> [--block N] [--gpu|--cpu]
#include "zac.h"
#include "npy.h"
#include "wav.h"

#include <algorithm>
#include <chrono>
#include <cmath>
#include <cstdio>
#include <cstring>
#include <string>
#include <vector>

// Codes [H,W] stored as f32 in a .npy, rounded to int32.
static bool load_codes(const char * path, std::vector<int32_t> & codes, std::vector<int64_t> & shape, std::string & err) {
    std::vector<float> cf;
    if (!npy::load_f32(path, cf, shape) || shape.size() != 2) {
        err = "bad codes npy";
        return false;
    }
    codes.resize(cf.size());
    for (size_t i = 0; i < cf.size(); ++i) codes[i] = (int32_t) lrintf(cf[i]);
    return true;
}

// Compare buffered decode against a stateful streamed decode in --block frame steps.
static int seam_check(int argc, char ** argv) {
    const char * gguf_path = argv[2];
    const char * codes_path = argv[3];
    bool use_gpu = false;
    int block = 10;
    for (int i = 4; i < argc; ++i) {
        std::string a = argv[i];
        if (a == "--gpu") use_gpu = true;
        else if (a == "--cpu") use_gpu = false;
        else if (a == "--block" && i + 1 < argc) block = atoi(argv[++i]);
    }

    std::vector<int32_t> codes;
    std::vector<int64_t> sh;
    std::string err;
    if (!load_codes(codes_path, codes, sh, err)) {
        fprintf(stderr, "%s\n", err.c_str());
        return 1;
    }
    const int H = (int) sh[0], W = (int) sh[1];

    zac_model m;
    if (!zac_load(m, gguf_path, use_gpu)) return 1;

    const int spf = zac_samples_per_frame(m);
    const int shear = m.n_codebooks - 1;

    std::vector<float> full;
    if (!zac_decode(m, codes.data(), H, W, /*eos=*/-1, full)) {
        zac_free(m);
        return 1;
    }

    const int safe = H - shear;
    if (safe <= 0) {
        fprintf(stderr, "seam: need more than %d code rows to compare an emitted frame\n", shear);
        zac_free(m);
        return 1;
    }
    std::vector<float> tiled;
    int emitted = 0;
    double secs;
    {
        zac_stream st; // holds a KV cache on m's backend: destroy before zac_free(m)
        const auto t0 = std::chrono::steady_clock::now();
        bool ok = true;
        while (ok && emitted < safe) {
            const int f_hi = std::min(emitted + std::max(1, block), safe);
            ok = zac_stream_decode(m, st, codes.data(), H, f_hi, tiled);
            emitted = f_hi;
        }
        ok = ok && zac_stream_flush(m, st, tiled);
        secs = std::chrono::duration<double>(std::chrono::steady_clock::now() - t0).count();
        if (!ok) {
            st = zac_stream();
            zac_free(m);
            return 1;
        }
    }
    zac_free(m);
    fprintf(stderr, "seam: streamed %d frames in %.2fs (RTF %.3f), block %d\n", safe, secs,
            secs / ((double) tiled.size() / m.sample_rate), block);

    if (tiled.size() != full.size())
        fprintf(stderr, "seam: length mismatch: streamed %zu vs buffered %zu samples\n", tiled.size(), full.size());
    const size_t n = std::min(tiled.size(), full.size());
    double maxd = 0;
    size_t at = 0;
    for (size_t i = 0; i < n; ++i) {
        double d = std::fabs((double) tiled[i] - (double) full[i]);
        if (d > maxd) {
            maxd = d;
            at = i;
        }
    }
    fprintf(stderr, "seam: H=%d block=%d shear=%d -> compared %zu samples (%d frames), max abs diff=%.3e at %zu (frame %zu)\n",
            H, block, shear, n, (int) (n / spf), maxd, at, at / spf);
    fprintf(stderr, "seam: %s\n", maxd < 1e-6 ? "PASS (bit-identical)" : (maxd < 1e-3 ? "OK (sub-millisample)" : "FAIL (audible seam)"));
    return maxd < 1e-3 && tiled.size() == full.size() ? 0 : 2;
}

int main(int argc, char ** argv) {
    if (argc >= 4 && std::string(argv[1]) == "--seam") return seam_check(argc, argv);
    if (argc < 4) {
        fprintf(stderr, "usage: %s <zac.gguf> <codes.npy> <out.wav|out.npy> [--eos N] [--skip-frames N] [--headroom-db X] [--gpu|--cpu]\n"
                        "       %s --seam <zac.gguf> <codes.npy> [--block N] [--gpu|--cpu]\n",
                argv[0], argv[0]);
        return 1;
    }
    const char * gguf_path = argv[1];
    const char * codes_path = argv[2];
    const char * out_path = argv[3];
    bool use_gpu = false;
    int eos_override = -2;   // -2 = unset
    int skip_override = -1;  // -1 = unset (eos file's prefill, else 0)
    double headroom_db = -1; // < 0 = the decoder's own headroom
    for (int i = 4; i < argc; ++i) {
        std::string a = argv[i];
        if (a == "--gpu") use_gpu = true;
        else if (a == "--cpu") use_gpu = false;
        else if (a == "--eos" && i + 1 < argc) eos_override = atoi(argv[++i]);
        else if (a == "--skip-frames" && i + 1 < argc) skip_override = atoi(argv[++i]);
        else if (a == "--headroom-db" && i + 1 < argc) headroom_db = atof(argv[++i]);
        else {
            fprintf(stderr, "unknown or incomplete option: %s\n", a.c_str());
            return 1;
        }
    }

    // read raw codes [H,10] (stored f32) -> int32
    std::vector<int32_t> codes;
    std::vector<int64_t> sh;
    std::string err;
    if (!load_codes(codes_path, codes, sh, err)) {
        fprintf(stderr, "%s\n", err.c_str());
        return 1;
    }
    const int H = (int) sh[0], W = (int) sh[1];

    // eos: --eos overrides; else <codes>.eos.npy; else full length
    int eos = -1, skip = 0;
    {
        std::vector<float> ev;
        std::vector<int64_t> es;
        std::string ep = std::string(codes_path) + ".eos.npy";
        if (npy::load_f32(ep.c_str(), ev, es) && !ev.empty()) {
            eos = (int) lrintf(ev[0]);
            if (ev.size() > 1) skip = (int) lrintf(ev[1]);
        }
    }
    if (eos_override != -2) eos = eos_override;
    if (skip_override >= 0) skip = skip_override;
    if (headroom_db >= 0 && headroom_db > 24) {
        fprintf(stderr, "--headroom-db must be 0..24\n");
        return 1;
    }

    zac_model m;
    if (!zac_load(m, gguf_path, use_gpu)) return 1;
    std::vector<float> audio;
    if (!zac_decode(m, codes.data(), H, W, eos, audio)) {
        zac_free(m);
        return 1;
    }
    const size_t drop = std::min(audio.size(), (size_t) skip * (size_t) zac_samples_per_frame(m));
    audio.erase(audio.begin(), audio.begin() + drop);
    const float gain = zac_output_gain(m, headroom_db);
    for (float & v : audio) v *= gain;
    fprintf(stderr, "zac-cli: dropped %zu prefill samples, gain %.4f\n", drop, gain);
    const std::string out(out_path);
    bool ok = out.size() >= 4 && out.compare(out.size() - 4, 4, ".npy") == 0
                  ? npy::save_f32(out_path, audio.data(), {(int64_t) audio.size()})
                  : wav_pcm16_write(out_path, audio, m.sample_rate);
    zac_free(m);
    return ok ? 0 : 1;
}
