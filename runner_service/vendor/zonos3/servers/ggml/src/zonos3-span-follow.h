// Span steering that follows the model's reading position (control_span_follow: "reading"): exact port of
// model/src/zonos3/tts/span_follow.py.
//
// A request's control_spans are mapped from characters of the request text to text-span token positions [a, b)
// (map_offsets through text normalization, then span_token_ranges over the tokens' character ranges). While the
// request decodes, the alignment probe's argmax text position is fed to a SpanFollower once per step; it smooths it
// (median of the last `smooth` readouts, held monotone, at most +1 token per readout). Span k ramps in (`ramp` frames)
// on the first frame decided with the smoothed position at or past a - lead and ramps out on the first frame decided
// at or past b + lag. Decisions are recorded per generated frame, so the speaker-CFG twin and a LACI rollback
// (truncate) see the same weights for the frames they share.
//
// Strings are UTF-32 so character offsets equal Python's str indices.
#pragma once

#include "zonos3-steer.h"  // ZONOS3_STEER_RAMP_FRAMES (zonos3.tts.steering.RAMP_FRAMES)

#include <functional>
#include <map>
#include <optional>
#include <string>
#include <utility>
#include <vector>

namespace zonos3_follow {

struct FollowConfig {
    double lead = 1.0;  // tokens: ramp in once the reading reaches a span's first token - lead
    double lag = 0.0;   // tokens: ramp out once the reading reaches the token after the span + lag
    int smooth = 3;     // readouts in the running median
    int ramp = ZONOS3_STEER_RAMP_FRAMES;
};

using Range = std::pair<int, int>;

bool py_isspace(char32_t c);  // Python's str.isspace for one code point

// difflib.SequenceMatcher(None, a, b, autojunk=False).get_opcodes(): (tag, i1, i2, j1, j2)
struct Opcode { const char * tag; int i1, i2, j1, j2; };
std::vector<Opcode> diff_opcodes(const std::u32string & a, const std::u32string & b);

// Character offsets of `original` mapped into `final` (original after normalization): exact inside unchanged
// stretches, proportional inside rewritten ones (rounded outward: down for starts, up when `end`).
std::vector<int> map_offsets(const std::u32string & original, const std::u32string & final_,
                             const std::vector<int> & offsets, bool end = false);

// Character offsets (code points into s) of the SuperBPE tokens of s (tokenizers' encode(...).offsets).
using BpeOffsets = std::function<std::vector<Range>(const std::u32string & s)>;
struct PhonemeMarkers { std::u32string open, close; };
// The tokenizer spec's text markers: pronunciation groups (`phonemes`, when the spec has them), the start token
// (U+E002) and the pause tokens (U+E003..U+E00A).
struct TextMarkers {
    std::optional<PhonemeMarkers> phonemes;
    bool start = false;
    bool pauses = false;
};

// Character range of `text` behind each id of its text span: BOS and EOS are the empty ranges at 0 and len(text).
// Without a tokenizer spec (`bpe` empty) one range per UTF-8 byte; otherwise SuperBPE offsets, with a phoneme group
// one range per character from its open to its close marker, and the start and pause ids their marker.
std::vector<Range> token_char_ranges(const std::u32string & text, const BpeOffsets & bpe, const TextMarkers & markers);

// Text-span positions [a, b) of the tokens spelling each character span [start, end) of `text`, once the text is
// `final_`. `ranges` = token_char_ranges(final_, ...); all overlapping non-whitespace tokens participate.
std::vector<Range> span_token_ranges(const std::u32string & text, const std::u32string & final_,
                                     const std::vector<Range> & spans, const std::vector<Range> & ranges);

// The span gate of one request. observe(frame, pos) takes the readout of the step that generated `frame` (frames
// count the stream's generated rows: a LACI continuation's start at its history_rows); delta(frame) decides and
// returns the summed span delta for the frame generated now (false: no span is on); truncate(frame) forgets the
// readouts and decisions of frames >= frame. As a probe observer (on_step) step s maps to frame origin + s.
class SpanFollower {
public:
    // deltas: [ranges.size()][dim]; throws std::invalid_argument with the Python messages
    SpanFollower(std::vector<Range> ranges, std::vector<std::vector<float>> deltas, FollowConfig cfg = FollowConfig(),
                 int origin = 0);

    void on_step(int step, int pos) { observe(origin + step, pos); }
    void observe(int frame, int pos);
    double position() const { return best_; }
    const std::vector<double> & weights(int frame);
    bool delta(int frame, std::vector<float> & out);  // out = sum_k w_k * deltas[k] (float32, like torch)
    void truncate(int frame);

    std::vector<Range> ranges;
    std::vector<std::vector<float>> deltas;
    FollowConfig cfg;
    int origin = 0;

private:
    void advance(size_t n);

    std::vector<int> frames_, pos_;
    double best_ = 0.0;  // smoothed, monotone reading position (0 = BOS before any readout)
    std::vector<std::optional<std::pair<int, int>>> enter_, exit_;  // (decision frame, ramp anchor) per span
    std::map<int, std::vector<double>> weights_;                     // frame -> per-span weights decided for it
};

}  // namespace zonos3_follow
