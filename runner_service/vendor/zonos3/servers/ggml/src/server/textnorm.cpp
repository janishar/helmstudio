#include "server/textnorm.h"

#include "zonos3-languages.h"
#include "zyphra_textnorm.h"

#include <memory>

namespace zs {

namespace {

std::string owned(char * s) {
    const std::unique_ptr<char, decltype(&zyphra_textnorm_free)> out(s, &zyphra_textnorm_free);
    return out.get();
}

// A start / pause marker (U+E002..U+E00A = EE 80 82..8A) at byte i.
bool text_marker_at(const std::string & s, size_t i) {
    return i + 2 < s.size() && (unsigned char) s[i] == 0xEE && (unsigned char) s[i + 1] == 0x80 &&
           (unsigned char) s[i + 2] >= 0x82 && (unsigned char) s[i + 2] <= 0x8A;
}

// zonos3.tokenizer.textnorm: the start / pause markers go through the grammar wrapped as one-character pronunciation
// groups (U+E000 marker U+E001), which the normalizer keeps verbatim with the whitespace around them.
std::string wrap_text_markers(const std::string & s) {
    std::string out;
    out.reserve(s.size());
    for (size_t i = 0; i < s.size(); ++i) {
        if (text_marker_at(s, i)) {
            out += "\xEE\x80\x80";
            out.append(s, i, 3);
            out += "\xEE\x80\x81";
            i += 2;
        } else out += s[i];
    }
    return out;
}

// Undo wrap_text_markers: EE 80 80, a marker, EE 80 81 -> the marker.
std::string unwrap_text_markers(const std::string & s) {
    std::string out;
    out.reserve(s.size());
    for (size_t i = 0; i < s.size(); ++i) {
        if (s.compare(i, 3, "\xEE\x80\x80") == 0 && text_marker_at(s, i + 3) && s.compare(i + 6, 3, "\xEE\x80\x81") == 0) {
            out.append(s, i + 3, 3);
            i += 8;
        } else out += s[i];
    }
    return out;
}

} // namespace

void TextNormalizer::Delete::operator()(zyphra_textnorm * tn) const {
    zyphra_textnorm_delete(tn);
}

TextNormalizer::TextNormalizer() {
    tn_.reset(zyphra_textnorm_new(nullptr, 0));
}

std::string TextNormalizer::normalize(const std::string & text, const std::string & language) const {
    for (const auto & l : zonos3_server_languages())
        if (l.code == language) {
            const std::string in = wrap_text_markers(text);
            return unwrap_text_markers(
                owned(zyphra_textnorm_normalize(tn_.get(), in.data(), in.size(), l.nemo.data(), l.nemo.size())));
        }
    return owned(zyphra_textnorm_collapse_line_breaks(text.data(), text.size()));
}

} // namespace zs
