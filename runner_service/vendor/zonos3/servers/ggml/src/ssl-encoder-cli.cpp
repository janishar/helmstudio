// Native teacher/frontend intermediates for comparison with `tools/dump_golden.py ssl-teacher`.
#include "compat.h"
#include "ssl-encoder.h"
#include "audio-decode.h"
#include "npy.h"
#include <cmath>
#include <cstdlib>
#include <cstring>
#include <filesystem>
#include <string>
#include <vector>

int main(int argc, char ** argv) {
    if (argc < 6) {
        fprintf(stderr,"usage: %s <ssl-teacher.gguf> {--audio audio|--pcm44100 lane.npy|--features features.npy} --out dir [--mask mask.npy] [--pad-seconds 20] [--dtype f32|bf16] [--cpu] [--frontend-only]\n",argv[0]);
        return 1;
    }
    std::string mode, input, out_dir, mask_path;
    double pad_seconds = 0;
    bool gpu = true, frontend_only = false;
    ssl_dtype dtype = ssl_dtype::bf16;
    for (int i = 2; i < argc; ++i) {
        std::string arg = argv[i];
        if ((arg == "--audio" || arg == "--pcm44100" || arg == "--features") && i+1 < argc && mode.empty()) {
            mode = arg; input = argv[++i];
        } else if (arg == "--out" && i+1 < argc) out_dir = argv[++i];
        else if (arg == "--mask" && i+1 < argc) mask_path = argv[++i];
        else if (arg == "--dtype" && i+1 < argc) {
            const std::string value = argv[++i];
            if (value == "f32") dtype = ssl_dtype::f32;
            else if (value == "bf16") dtype = ssl_dtype::bf16;
            else { fprintf(stderr,"ssl: dtype must be f32 or bf16\n"); return 1; }
        }
        else if (arg == "--pad-seconds" && i+1 < argc) {
            char * end = nullptr;
            pad_seconds = std::strtod(argv[++i],&end);
            if (!end || *end || !std::isfinite(pad_seconds) || pad_seconds < 0 || pad_seconds > 60) return 1;
        } else if (arg == "--cpu") gpu = false;
        else if (arg == "--frontend-only") frontend_only = true;
        else { fprintf(stderr,"ssl: invalid option %s\n",argv[i]); return 1; }
    }
    if (mode.empty() || out_dir.empty() || (!mask_path.empty() && mode != "--features")) return 1;
    ggml_backend_load_all();
    ssl_model m;
    if (!ssl_load(m,argv[1],gpu)) return 1;
    auto fail = [&]() { ssl_free(m); return 1; };
    std::vector<float> feats, wav;
    std::vector<int32_t> mask;
    std::vector<int64_t> shape;
    int T = 0;
    if (mode == "--features") {
        if (!npy::load_f32(input,feats,shape) || shape.size() != 2 || shape[0] <= 0 || shape[0] > 3000 || shape[1] != 160) return fail();
        T = (int) shape[0]; mask.assign(T,1);
        if (!mask_path.empty()) {
            std::vector<float> values;
            if (!npy::load_f32(mask_path,values,shape) || shape != std::vector<int64_t>{T}) return fail();
            for (int i = 0; i < T; ++i) {
                if (values[i] != 0.0f && values[i] != 1.0f) return fail();
                mask[i] = (int32_t) values[i];
            }
        }
    } else {
        if (mode == "--audio") wav = audio_decode_file(input.c_str(),44100,60);
        else if (!npy::load_f32(input,wav,shape) || shape.size() != 1) return fail();
        if (wav.empty() || wav.size() > 60*44100) return fail();
        if (pad_seconds > 0) {
            const size_t samples = (size_t) std::llround(pad_seconds*44100);
            if (samples < wav.size()) return fail();
            wav.resize(samples,0.0f);
        }
        if (!ssl_features_pcm(m,wav.data(),(int) wav.size(),44100,feats,T,mask)) return fail();
    }
    for (float v : feats) if (!std::isfinite(v)) return fail();
    std::error_code ec;
    std::filesystem::create_directories(std::filesystem::u8path(out_dir),ec);
    if (ec) return fail();
    auto save = [&](const std::string & name, const std::vector<float> & values, std::vector<int64_t> dims) {
        return npy::save_f32((std::filesystem::u8path(out_dir)/(name+".npy")).string(),values.data(),dims);
    };
    std::vector<float> mask_f(mask.begin(),mask.end());
    if (!save("features",feats,{T,160}) || !save("attention_mask",mask_f,{T})) return fail();
    if (!wav.empty() && !save("wav44100",wav,{(int64_t) wav.size()})) return fail();
    if (!frontend_only) {
        std::vector<std::vector<float>> states;
        std::map<std::string,std::vector<float>> projection_states;
        if (!ssl_encode_features_trace(m,feats.data(),T,mask.data(),states,dtype,&projection_states)) return fail();
        for (const auto & state : projection_states)
            if (!save(state.first,state.second,{T,(int64_t) state.second.size()/T})) return fail();
        for (size_t i = 0; i < states.size(); ++i)
            if (!save("hidden_"+std::to_string(i),states[i],{T,1024})) return fail();
        if (!save("output",states.back(),{T,1024})) return fail();
    }
    fprintf(stderr,"ssl: dumped %d frames (%s) to %s\n",T,dtype == ssl_dtype::bf16 ? "bf16" : "f32",out_dir.c_str());
    ssl_free(m);
    return 0;
}
