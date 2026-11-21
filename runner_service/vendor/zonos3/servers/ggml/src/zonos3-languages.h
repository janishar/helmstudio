// The language registry model/src/zonos3/registries/languages.json (Python: zonos3.languages), embedded at build
// time and parsed on first use.
#pragma once
#include <string>
#include <utility>
#include <vector>

struct zonos3_server_language {
    std::string code;    // TTS request language code
    std::string nemo;    // text-normalization grammar (NeMo package: en, zh, ...)
    std::string lexicon; // pronunciation lexicon language (ISO 639-3)
};

// The server rows, in display order.
const std::vector<zonos3_server_language> & zonos3_server_languages();
// Their codes, same order (zonos3.languages.CODES).
const std::vector<std::string> & zonos3_server_language_codes();
// Lexicon code -> the espeak-ng voice that guesses its unknown words, in registry order.
const std::vector<std::pair<std::string, std::string>> & zonos3_espeak_voices();
