// TTS text normalization, in process: the Rust library of the zyphra-textnorm submodule (zyphra_textnorm.h), linked
// statically.
#pragma once

#include <memory>
#include <string>

struct zyphra_textnorm;

namespace zs {

class TextNormalizer {
  public:
    // A normalizer that keeps pronunciation groups and the start / pause markers (U+E002..U+E00A) as typed.
    TextNormalizer();
    // The spoken form of `text` for a server language code, in its registry `nemo` grammar; never fails (an
    // unsupported language comes back with its line breaks collapsed, a grammar failure keeps the failing sentence as
    // typed). Thread-safe; memoized.
    std::string normalize(const std::string & text, const std::string & language) const;

  private:
    struct Delete {
        void operator()(zyphra_textnorm * tn) const;
    };
    std::unique_ptr<zyphra_textnorm, Delete> tn_;
};

} // namespace zs
