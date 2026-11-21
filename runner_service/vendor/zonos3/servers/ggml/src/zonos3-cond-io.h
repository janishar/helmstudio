// Conditioning sidecar next to a saved ids.npy: <stem>.cond.json carries the replaced-row metadata
// (speaker row + embedding, condition row + buckets) that the id matrix alone cannot express.
#pragma once

#include "zonos3.h"
#include "json.hpp"

#include <cstdio>
#include <fstream>
#include <string>

inline std::string zonos3_cond_path(const std::string & ids_npy) {
    const std::string ext = ".npy";
    if (ids_npy.size() >= ext.size() && ids_npy.compare(ids_npy.size() - ext.size(), ext.size(), ext) == 0)
        return ids_npy.substr(0, ids_npy.size() - ext.size()) + ".cond.json";
    return ids_npy + ".cond.json";
}

inline bool zonos3_cond_save(const std::string & path, const zonos3_prompt & p) {
    nlohmann::json j;
    j["speaker_row"] = p.cond.speaker_row;
    if (p.cond.speaker_row >= 0) j["speaker_embedding"] = p.cond.speaker;
    j["condition_row"] = p.cond.condition_row;
    j["condition_buckets"] = p.cond.buckets;
    j["speaker_rows"] = p.speaker_rows;
    j["prefill_frames"] = p.prefill_frames;
    std::ofstream f(path, std::ios::binary);
    if (!f) { fprintf(stderr, "cannot write %s\n", path.c_str()); return false; }
    f << j.dump() << '\n';
    f.flush();
    if (!f) { fprintf(stderr, "cannot write %s\n", path.c_str()); return false; }
    return true;
}

// Fills meta.cond, meta.speaker_rows and meta.prefill_frames. A missing file returns false with err = "missing".
inline bool zonos3_cond_load(const std::string & path, zonos3_prompt & meta, std::string & err) {
    std::ifstream f(path, std::ios::binary);
    if (!f) { err = "missing"; return false; }
    try {
        const nlohmann::json j = nlohmann::json::parse(f);
        zonos3_conditioning c;
        c.speaker_row = j.at("speaker_row").get<int>();
        if (c.speaker_row >= 0) c.speaker = j.at("speaker_embedding").get<std::vector<float>>();
        c.condition_row = j.at("condition_row").get<int>();
        c.buckets = j.at("condition_buckets").get<std::vector<int32_t>>();
        const int speaker_rows = j.at("speaker_rows").get<int>();
        const int prefill = j.at("prefill_frames").get<int>();
        if (speaker_rows < 0 || prefill < 0) { err = "negative speaker_rows/prefill_frames"; return false; }
        meta.cond = std::move(c);
        meta.speaker_rows = speaker_rows;
        meta.prefill_frames = prefill;
    } catch (const std::exception & e) {
        err = std::string("invalid conditioning sidecar: ") + e.what();
        return false;
    }
    return true;
}
