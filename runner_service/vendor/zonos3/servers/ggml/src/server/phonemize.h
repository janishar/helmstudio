// POST /v2/phonemize and the pronunciation capabilities (zonos3.server.capabilities.phoneme_capabilities): the gold lexicon
// of --tts-phoneme-lexicon (zonos3-phonemes, espeak-ng guessing when ZONOS3_ESPEAK_NG_LIBRARY is set), restricted to
// the checkpoint's phone inventory on a phoneme-token checkpoint ("unsupported" segments).
#pragma once

#include "server/state.h"

#include "httplib.h"
#include "json.hpp"

#include <string>

namespace zs {

// Opens s.phoneme_lexicon_dir into s.phoneme_lexicon; false with the reason in `err`.
bool open_phoneme_lexicon(ServerState & s, std::string & err);
// {phoneme_tokens, phonemize_enabled, phonemize_languages, phonemize_guess_languages}
nlohmann::json phoneme_capabilities(const ServerState & s);
void register_phonemize_routes(httplib::Server & svr, ServerState & s);

} // namespace zs
