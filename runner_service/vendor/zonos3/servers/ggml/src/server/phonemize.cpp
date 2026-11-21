#include "server/phonemize.h"

#include "server/http.h"
#include "server/schema.h"

#include "zonos3-phonemes.h"

#include <mutex>

namespace zs {

using json = nlohmann::json;

namespace {

// espeak-ng (the guess path) is not reentrant; lexicon lookups are cheap, so one request at a time.
std::mutex phonemize_mtx;

// The checkpoint's phone inventory (text_tokenizer_spec.phonemes.symbols); empty without phoneme tokens.
json phoneme_tokens(const ServerState & s) {
    const auto & tok = s.model.tok;
    if (!tok.has_phonemes) return nullptr;
    return {{"open_marker", tok.phonemes.open_marker}, {"close_marker", tok.phonemes.close_marker},
            {"symbols", tok.phonemes.symbols}};
}

}  // namespace

bool open_phoneme_lexicon(ServerState & s, std::string & err) {
    auto lexicon = zonos3_phoneme_lexicon::open(s.phoneme_lexicon_dir, err);
    if (!lexicon) return false;
    s.phoneme_lexicon = std::shared_ptr<zonos3_phoneme_lexicon>(std::move(lexicon));
    std::string langs;
    for (const auto & l : s.phoneme_lexicon->languages()) langs += (langs.empty() ? "" : ", ") + l;
    fprintf(stderr, "zonos3-server: loaded pronunciation lexicon %s (%s)\n", s.phoneme_lexicon_dir.c_str(), langs.c_str());
    return true;
}

json phoneme_capabilities(const ServerState & s) {
    const auto & lex = s.phoneme_lexicon;
    return {{"phoneme_tokens", phoneme_tokens(s)},
            {"phonemize_enabled", lex != nullptr},
            {"phonemize_languages", lex ? json(lex->server_languages()) : json::array()},
            {"phonemize_guess_languages", lex ? json(lex->guess_languages()) : json::array()}};
}

void register_phonemize_routes(httplib::Server & svr, ServerState & s) {
    svr.Post("/v2/phonemize", guarded([&s](const httplib::Request & req, httplib::Response & res) {
        const json body = schema::parse_body(req, "PhonemizeRequest");
        if (!s.phoneme_lexicon) fail(404, "This server has no pronunciation lexicon.");
        const std::string text = body["text"].get<std::string>();
        const std::string language = body.contains("language") && !body["language"].is_null()
                                         ? body["language"].get<std::string>() : "en_us";
        const bool guess = !body.contains("guess") || body["guess"].is_null() || body["guess"].get<bool>();
        zonos3_phonemize_result result;
        std::string err;
        bool ok;
        {
            std::lock_guard<std::mutex> lk(phonemize_mtx);
            try {
                ok = s.phoneme_lexicon->phonemize(text, language, guess, result, err);
            } catch (const std::runtime_error & e) {
                fail(500, e.what());
            }
        }
        if (!ok) fail(400, err);
        if (s.model.tok.has_phonemes) zonos3_phonemize_restrict(result, s.model.tok.phonemes.symbols);
        res.set_content(zonos3_phonemize_json(result), "application/json");
    }));
}

}  // namespace zs
