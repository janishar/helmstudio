// Speech editing: the edit prompt layout of model/src/zonos3/tts/edit.py, which the Python host's fill calls build
// over the served model (the planning, fill extraction and splice run in Python: zonos3.tts.edit_runtime).
//
// Codes are row-major int32 matrices: ordinary codec frames [frames, n_codebooks], prompt rows
// [rows, n_codebooks + 1]. Invalid input throws std::invalid_argument with the Python ValueError message.
#pragma once

#include <cstdint>
#include <string>
#include <vector>

namespace zonos3_edit {

struct Span { int start = 0, length = 0; };   // (start_frame, length_frames)

struct Layout {
    int n_codebooks = 10;
    int audio_pad_id = 4097;   // EOA = audio_pad_id - 1
    int text_vocab = 8196;     // the text pad; edit marker j sits at text_vocab + 1 + j
    int span_end_id = 4098;
};

void validate_edit_spans(const std::vector<Span> & spans, int total_frames, int max_spans);
// Delay-stacked frames (+ one all-`end_id` row when end_id >= 0) followed by K-1 pad rows, text column = text pad.
std::vector<int32_t> sheared_block(const int32_t * frames, int n_frames, const Layout & l, int end_id);
// The training edit layout up to the next infill marker: text rows, the unmasked context (markers at the spans) ending
// on EOA, every generated fill ending on span-end, then marker len(fills).
std::vector<int32_t> edit_prompt_tokens(const std::vector<int32_t> & text_rows, const std::vector<int32_t> & codes,
                                        const std::vector<Span> & spans,
                                        const std::vector<std::vector<int32_t>> & fills, const Layout & l);

}  // namespace zonos3_edit
