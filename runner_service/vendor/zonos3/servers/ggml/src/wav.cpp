#include "wav.h"

#include <cmath>
#include <cstdio>

std::vector<uint8_t> wav_pcm16_bytes(const std::vector<float> & audio, int sample_rate) {
    const uint32_t sr = (uint32_t) sample_rate, data_bytes = (uint32_t) (audio.size() * 2);
    std::vector<uint8_t> out;
    out.reserve(44 + audio.size() * 2);
    auto tag = [&](const char * s) { out.insert(out.end(), s, s + 4); };
    auto w32 = [&](uint32_t v) {
        for (int i = 0; i < 4; ++i) out.push_back((uint8_t) (v >> (8 * i)));
    };
    auto w16 = [&](uint16_t v) {
        out.push_back((uint8_t) v);
        out.push_back((uint8_t) (v >> 8));
    };
    tag("RIFF");
    w32(36 + data_bytes);
    tag("WAVE");
    tag("fmt ");
    w32(16);
    w16(1); // PCM
    w16(1); // mono
    w32(sr);
    w32(sr * 2);
    w16(2);
    w16(16);
    tag("data");
    w32(data_bytes);
    for (float v : audio) {
        if (std::isnan(v)) v = 0.0f;
        v = v > 1.0f ? 1.0f : v < -1.0f ? -1.0f
                                        : v;
        w16((uint16_t) (int16_t) std::nearbyint(v * 32767.0f)); // default rounding mode: half to even
    }
    return out;
}

bool wav_pcm16_write(const char * path, const std::vector<float> & audio, int sample_rate) {
    FILE * wf = fopen(path, "wb");
    if (!wf) {
        fprintf(stderr, "zac: cannot write %s\n", path);
        return false;
    }
    const std::vector<uint8_t> buf = wav_pcm16_bytes(audio, sample_rate);
    const bool ok = fwrite(buf.data(), 1, buf.size(), wf) == buf.size();
    if (fclose(wf) != 0 || !ok) {
        fprintf(stderr, "zac: cannot write %s\n", path);
        return false;
    }
    fprintf(stderr, "zac: wrote %s\n", path);
    return true;
}
