// Gold-lexicon phonemization for the pronunciation input: a port of zonos3/tts/phonemes.py.
// The lexicon is the LMDB the training data used (a "meta" JSON record plus one named DB per
// language, keys = zonos3_normalize_headword forms, values = IPA, "" = ambiguous). espeak-ng, when
// ZONOS3_ESPEAK_NG_LIBRARY names its shared library (data dir ZONOS3_ESPEAK_NG_DATA), guesses
// ambiguous and unknown words of the space-delimited languages; it is dlopen'ed, never linked.
#pragma once
#include <map>
#include <memory>
#include <mutex>
#include <optional>
#include <string>
#include <vector>

struct zonos3_phoneme_segment {
    std::string text;                   // UTF-8; segment texts concatenate back to the input
    std::optional<std::string> ipa;     // null unless status is "ok" or "guessed"
    std::optional<std::string> status;  // ok|ambiguous|unknown|skipped|guessed|unsupported; null = not a word
};
struct zonos3_phonemize_result {
    std::string language;               // lexicon code
    std::vector<zonos3_phoneme_segment> segments;
};

// Lexicon key of a UTF-8 word: NFC(strip(word)) with U+2019 -> "'", casefolded.
std::string zonos3_normalize_headword(const std::string & word);
// espeak-ng IPA in the lexicon's notation; nullopt for output with tone digits or brackets.
std::optional<std::string> zonos3_harmonize_espeak(const std::string & raw, const std::string & code, bool ties);
// Whether the espeak-ng guesser loaded (first call loads it from the environment).
bool zonos3_espeak_available();

struct MDB_env;
class zonos3_phoneme_lexicon {
public:
    // nullptr with `err` set when `path` is not a readable lexicon of the supported version.
    static std::unique_ptr<zonos3_phoneme_lexicon> open(const std::string & path, std::string & err);
    ~zonos3_phoneme_lexicon();
    zonos3_phoneme_lexicon(const zonos3_phoneme_lexicon &) = delete;
    zonos3_phoneme_lexicon & operator=(const zonos3_phoneme_lexicon &) = delete;

    const std::vector<std::string> & languages() const { return codes_; }  // lexicon codes, meta order
    // Lexicon code for a server or lexicon code; false + err ("No pronunciation lexicon for language '<l>'").
    bool resolve_language(const std::string & language, std::string & code, std::string & err) const;
    std::vector<std::string> server_languages() const;
    std::vector<std::string> guess_languages() const;  // server languages espeak-ng can guess
    // nullopt: unknown; "": ambiguous; otherwise the gold IPA. `code` must be a lexicon code.
    std::optional<std::string> get(const std::string & code, const std::string & word) const;
    bool uses_ties(const std::string & code);           // affricates written with U+0361 in the gold IPA
    // PhonemeLexicon.phonemize; false + err (a 400 message) for an unknown language.
    bool phonemize(const std::string & text, const std::string & language, bool guess,
                   zonos3_phonemize_result & out, std::string & err);

private:
    zonos3_phoneme_lexicon() = default;
    MDB_env * env_ = nullptr;
    std::vector<std::string> codes_;
    std::map<std::string, std::string> modes_;          // code -> words|korean|japanese|<other: chinese>
    std::map<std::string, unsigned> dbs_;               // code -> MDB_dbi
    std::mutex ties_mu_;
    std::map<std::string, bool> ties_;
};

// The phoneme-token checkpoint rewrite of the phonemize route: a segment whose IPA has a code point
// that is not one of `symbols` becomes {ipa: null, status: "unsupported"}.
void zonos3_phonemize_restrict(zonos3_phonemize_result & result, const std::vector<std::string> & symbols);
// {"language": ..., "segments": [{"text", "ipa", "status"}, ...]}
std::string zonos3_phonemize_json(const zonos3_phonemize_result & result);
