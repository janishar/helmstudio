// Embed mono reference audio into a 192-d ZONOS3 Mini speaker vector (float32 [192] .npy).
#include "spk-encoder.h"
#include "audio-decode.h"
#include "model-paths.h"
#include "npy.h"
#include <chrono>
#include <cmath>
#include <cstdio>
#include <filesystem>
#include <string>
#include <vector>

int main(int argc, char ** argv) {
    if (argc < 5) {
        fprintf(stderr, "usage: %s <spk-encoder.gguf> --audio <ref.mp3|pcm.npy> <out.npy> [--dump-dir DIR] [--gpu|--cpu]\n", argv[0]);
        return 1;
    }
    std::string audio_path, output, dir;
    bool gpu = false;
    for (int i = 2; i < argc; ++i) {
        const std::string a = argv[i];
        if (a == "--gpu") gpu = true;
        else if (a == "--cpu") gpu = false;
        else if (a == "--audio" && i + 1 < argc) audio_path = argv[++i];
        else if (a == "--dump-dir" && i + 1 < argc) dir = argv[++i];
        else if (!a.empty() && a[0] == '-') {
            fprintf(stderr, "unknown/incomplete flag %s\n", a.c_str());
            return 1;
        } else if (output.empty()) output = a;
        else {
            fprintf(stderr, "unexpected argument %s\n", a.c_str());
            return 1;
        }
    }
    if (audio_path.empty() || output.empty()) {
        fprintf(stderr, "--audio and output path are required\n");
        return 1;
    }
    std::vector<float> audio;
    std::string err;
    if (!audio_load_reference(audio_path, 60, audio, err)) {
        fprintf(stderr, "%s\n", err.c_str());
        return 1;
    }
    spk_dump_fn dump;
    if (!dir.empty()) {
        std::error_code ec;
        std::filesystem::create_directories(dir, ec);
        if (ec) {
            fprintf(stderr, "cannot create dump directory: %s\n", ec.message().c_str());
            return 1;
        }
        dump = [&](const char * name, const float * data, int rows, int cols) {
            return npy::save_f32(dir + "/" + name + ".npy", data, {rows, cols});
        };
    }
    spk_model model;
    if (!spk_load(model, argv[1], gpu)) return 1;
    std::vector<float> embedding;
    const auto start = std::chrono::steady_clock::now();
    bool ok = spk_embed(model, audio.data(), (int64_t) audio.size(), 44100, embedding, dump);
    const double seconds = std::chrono::duration<double>(std::chrono::steady_clock::now() - start).count();
    spk_free(model);
    if (ok) ok = mp_save_speaker_embedding(output, embedding);
    if (!ok) {
        fprintf(stderr, "speaker embedding failed\n");
        return 1;
    }
    double norm = 0.0;
    for (float v : embedding) norm += (double) v * v;
    fprintf(stderr, "embedded %zu samples -> %zu floats (norm %.4f) in %.3fs\n",
            audio.size(), embedding.size(), std::sqrt(norm), seconds);
    return 0;
}
