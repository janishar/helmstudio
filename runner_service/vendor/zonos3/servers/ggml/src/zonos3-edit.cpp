#include "zonos3-edit.h"

#include <algorithm>
#include <stdexcept>

namespace zonos3_edit {

namespace {

[[noreturn]] void value_error(const std::string & msg) { throw std::invalid_argument(msg); }

}  // namespace

void validate_edit_spans(const std::vector<Span> & spans, int total_frames, int max_spans) {
    if (spans.empty() || (int) spans.size() > max_spans) value_error("Edit requires between one and max_spans regions");
    int previous_end = -1;
    for (const Span & s : spans) {
        if (s.start < 0 || s.length < 1 || s.start + s.length > total_frames || s.start <= previous_end)
            value_error("Edit spans must be ascending, separated and within the audio");
        previous_end = s.start + s.length;
    }
}

std::vector<int32_t> sheared_block(const int32_t * frames, int n_frames, const Layout & l, int end_id) {
    const int k = l.n_codebooks, W = k + 1;
    const bool has_end = end_id >= 0;
    const int rows = n_frames + (has_end ? 1 : 0) + k - 1;
    std::vector<int32_t> audio((size_t) rows * k, l.audio_pad_id);
    std::copy(frames, frames + (size_t) n_frames * k, audio.begin());
    if (has_end) std::fill(audio.begin() + (size_t) n_frames * k, audio.begin() + (size_t) (n_frames + 1) * k, end_id);
    std::vector<int32_t> out((size_t) rows * W);
    for (int r = 0; r < rows; ++r) {
        for (int c = 0; c < k; ++c) out[(size_t) r * W + c] = r - c >= 0 ? audio[(size_t) (r - c) * k + c] : l.audio_pad_id;
        out[(size_t) r * W + k] = l.text_vocab;
    }
    return out;
}

std::vector<int32_t> edit_prompt_tokens(const std::vector<int32_t> & text_rows, const std::vector<int32_t> & codes,
                                        const std::vector<Span> & spans,
                                        const std::vector<std::vector<int32_t>> & fills, const Layout & l) {
    if (l.span_end_id != l.audio_pad_id + 1) value_error("span_end_id must equal audio_pad_id + 1");
    const int k = l.n_codebooks;
    const int n = (int) (codes.size() / k);
    validate_edit_spans(spans, n, (int) spans.size());
    if (fills.size() >= spans.size()) value_error("Prompt needs an ungenerated span");
    std::vector<int32_t> out(text_rows);
    auto marker = [&](int j) {
        for (int c = 0; c < k; ++c) out.push_back(l.audio_pad_id);
        out.push_back(l.text_vocab + 1 + j);
    };
    auto append = [&](const std::vector<int32_t> & rows) { out.insert(out.end(), rows.begin(), rows.end()); };
    int cursor = 0;
    for (size_t j = 0; j < spans.size(); ++j) {
        if (spans[j].start > cursor)
            append(sheared_block(codes.data() + (size_t) cursor * k, spans[j].start - cursor, l, -1));
        marker((int) j);
        cursor = spans[j].start + spans[j].length;
    }
    append(sheared_block(codes.data() + (size_t) cursor * k, n - cursor, l, l.audio_pad_id - 1));
    for (size_t j = 0; j < fills.size(); ++j) {
        marker((int) j);
        append(sheared_block(fills[j].data(), (int) (fills[j].size() / k), l, l.span_end_id));
    }
    marker((int) fills.size());
    return out;
}

}  // namespace zonos3_edit
