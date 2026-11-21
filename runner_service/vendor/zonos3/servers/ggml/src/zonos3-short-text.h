// Short texts: exact port of model/src/zonos3/tts/short_text.py. A text of one or two words is generated under LACI
// as the text, the 0.5 s pause marker and its language's filler sentence, and cut once the reading position reaches
// the filler: at the first frame a codes-only silence classifier calls silent, searched from `lookback` frames before
// the reading reached the filler, with `min_voiced` non-silent frames up to it. A take that reaches no such frame
// within `max_frames` frames ran into the filler (zonos3_laci::LaciSession retakes it, then cuts where the reading
// reached the filler). The registry (model/src/zonos3/registries/short_text.json) is embedded at build time.
#pragma once

#include <cstdint>
#include <map>
#include <optional>
#include <string>
#include <utility>
#include <vector>

namespace zonos3_short {

struct Registry {
    std::string pause_marker;          // UTF-8
    int max_words = 0;
    int max_ratio[2] = {0, 0};         // short: text tokens * max_ratio[1] <= filler tokens * max_ratio[0]
    int lookback = 0, min_voiced = 0, max_frames = 0, max_retries = 0;
    int n_codebooks = 0, codebook_size = 0;
    int threshold = 0;
    std::vector<int32_t> weights;      // [n_codebooks * codebook_size]
    std::map<std::string, std::string> fillers;  // per server language, with the space joining it to the marker
};

const Registry & registry();

// The cut of a short-text take over its generated frames (frame f = codebook k of generated row f + k).
// observe_reading(frame, pos) once per decode step, observe_row(row) per generated row; decided() turns to
// ("cut", frames) (keep generated frames [0, frames)) or ("run_on", enter) once it can be told. truncate(n) keeps
// generated rows [0, n) (a LACI rollback).
class ShortCut {
public:
    ShortCut(int filler_token, int n_codebooks);

    void truncate(int n);
    void observe_reading(int frame, int pos);
    void observe_row(const std::vector<int32_t> & row);

    std::optional<int> enter() const { return enter_; }
    const std::optional<std::pair<std::string, int>> & decided() const { return decided_; }
    int max_retries() const { return reg_.max_retries; }

private:
    bool is_silent(int f) const;
    void decide();

    const Registry & reg_;
    int filler_token_ = 0, n_codebooks_ = 0;
    std::optional<int> enter_;
    std::optional<std::pair<std::string, int>> decided_;
    std::vector<std::vector<int32_t>> rows_;
    std::vector<bool> silent_;  // per complete frame
};

}  // namespace zonos3_short
