#include "compat.h"

#include "server/generate.h"

#include "server/http.h"
#include "server/schema.h"
#include "server/speakers.h"
#include "server/textnorm.h"
#include "server/util.h"

#include "wav.h"
#include "zonos3-languages.h"
#include "zonos3-short-text.h"
#include "zonos3-text.h"

#include <algorithm>
#include <cmath>
#include <random>
#include <stdexcept>
#include <unordered_map>

namespace zs {

using json = nlohmann::json;

namespace {

std::string py_list(const std::vector<std::string> & items) {
    std::string out = "[";
    for (size_t i = 0; i < items.size(); ++i) out += (i ? ", " : "") + py_repr(items[i]);
    return out + "]";
}

bool set(const json & j, const char * key) {
    return j.contains(key) && !j[key].is_null();
}
double num(const json & j, const char * key, double d) {
    return set(j, key) ? j[key].get<double>() : d;
}
// Python truthiness of an optional container (None, {} and [] are false).
bool truthy(const json & j, const char * key) {
    return set(j, key) && !j[key].empty();
}

// R2 (zonos3.tts.plan.resolve_speaker_mode).
std::string resolve_speaker_mode(const ServerState & s, const json & body) {
    const auto modes = checkpoint_speaker_modes(s);
    if (!set(body, "speaker_mode")) return modes.empty() ? "" : modes[0];
    const std::string mode = body["speaker_mode"].get<std::string>();
    if (std::find(modes.begin(), modes.end(), mode) == modes.end())
        fail(400, "speaker_mode=" + py_repr(mode) + " is not available for this checkpoint (supported: " + py_list(modes) + ")");
    return mode;
}

} // namespace

// R3 (zonos3.tts.plan.resolve_condition_buckets): explicit ids win, raw values map through the edges, anything else is
// unknown.
std::vector<int32_t> resolve_condition_buckets(const ServerState & s, const json & body) {
    const auto & hp = s.model.hp;
    const json buckets = set(body, "condition_buckets") ? body["condition_buckets"] : json();
    const json values = set(body, "condition_values") ? body["condition_values"] : json();
    if (hp.n_cond == 0) {
        if ((!buckets.is_null() && !buckets.empty()) || (!values.is_null() && !values.empty()))
            fail(400, "This checkpoint has no condition rows (loudness / fmax / speaking rate)");
        return {};
    }
    const auto & names = hp.cond_names;
    auto as_map = [&](const json & raw, const char * what) {
        std::map<std::string, json> out;
        if (raw.is_null()) return out;
        if (raw.is_object()) {
            std::vector<std::string> unknown;
            for (auto it = raw.begin(); it != raw.end(); ++it) {
                if (std::find(names.begin(), names.end(), it.key()) == names.end()) unknown.push_back(it.key());
                else out[it.key()] = it.value();
            }
            if (!unknown.empty())
                fail(400, std::string(what) + ": unknown condition name(s) " + py_list(unknown) + "; expected " + py_list(names));
            return out;
        }
        if (raw.size() != names.size())
            fail(400, std::string(what) + ": expected " + std::to_string(names.size()) + " entries in the order " + py_list(names));
        for (size_t i = 0; i < names.size(); ++i) out[names[i]] = raw[i];
        return out;
    };
    const auto ids = as_map(buckets, "condition_buckets");
    const auto vals = as_map(values, "condition_values");
    std::vector<int32_t> out;
    for (size_t i = 0; i < names.size(); ++i) {
        const std::string & name = names[i];
        const int n_buckets = (int) hp.cond_bucket_counts[i];
        auto id = ids.find(name);
        auto val = vals.find(name);
        int bucket;
        if (id != ids.end() && !id->second.is_null()) {
            bucket = id->second.get<int>();
            if (bucket < 0 || bucket > n_buckets)
                fail(400, "condition_buckets[" + name + "] must be in [0, " + std::to_string(n_buckets - 1) + "] or " +
                              std::to_string(n_buckets) + " (= unknown)");
        } else if (val != vals.end() && !val->second.is_null()) {
            const double v = val->second.get<double>();
            if (!std::isfinite(v)) fail(400, "condition_values[" + name + "] must be finite");
            if (i >= hp.cond_edges.size() || hp.cond_edges[i].empty())
                fail(400, "the checkpoint carries no edges for " + name + "; send condition_buckets[" + name + "] instead");
            bucket = zonos3_condition_bucket(s.model, (int) i, v);
        } else {
            bucket = n_buckets;
        }
        out.push_back(bucket);
    }
    return out;
}

std::optional<zonos3_steer_release_frames> resolve_steer_release(const ServerState & s, const json & body) {
    const double nan = std::nan("");
    std::optional<zonos3_steer_release_frames> out;
    std::string err;
    if (!zonos3_steer_request_release(s.steer_loaded ? &s.steer : nullptr, num(body, "steer_hold", nan),
                                      num(body, "steer_onset_seconds", nan), num(body, "steer_ramp_seconds", nan),
                                      ZONOS3_FRAME_RATE, out, err))
        fail(400, s.steer_loaded ? err : std::string(NO_STEERING) + "; steer_hold below 1 needs its steer_release.");
    return out;
}

// R4 (zonos3.languages.normalize).
std::string resolve_language(const json & body) {
    const std::string given = set(body, "language") ? body["language"].get<std::string>() : "en_us";
    std::string lang = lower_ascii(strip(given));
    std::replace(lang.begin(), lang.end(), '-', '_');
    const auto & langs = zonos3_server_language_codes();
    if (std::find(langs.begin(), langs.end(), lang) == langs.end()) {
        std::string supported;
        for (const auto & l : langs) supported += (supported.empty() ? "" : ", ") + l;
        fail(400, "Unsupported language code: " + py_repr(given) + ". Supported: " + supported + ".");
    }
    return lang;
}

Decoder * resolve_decoder(const ServerState & s, const json & body) {
    if (!set(body, "decoder")) return s.decoders.empty() ? nullptr : s.decoders.front().get();
    const std::string id = body["decoder"].get<std::string>();
    std::string served;
    for (const auto & d : s.decoders) {
        if (d->id == id) return d.get();
        served += (served.empty() ? "" : ", ") + d->id;
    }
    fail(400, "Unknown decoder '" + id + "'; this server serves: " + served + ".");
}

std::string normalize_text(ServerState & s, const std::string & text, const std::string & language, bool requested) {
    if (!requested) return text;
    return s.text_normalizer.normalize(text, language);
}

namespace {

// The request's speaker embedding when it conditions on one (sgl: msg.speaker_embedding is not None).
std::vector<float> * request_embedding(GenReq & gr) {
    return gr.speaker_mode == "embedding" && !gr.speaker_embedding.empty() ? &gr.speaker_embedding : nullptr;
}

// R9 (zonos3.tts.plan.resolve_accent): an accent selects its adapter for the scheduler (gr.accent).
void apply_accent(const ServerState & s, const json & body, GenReq & gr) {
    if (!set(body, "accent")) return;
    const std::string accent = body["accent"].get<std::string>();
    if (accent.empty() || accent == "none") return;
    const std::vector<std::string> accents = server_accents(s);
    if (std::find(accents.begin(), accents.end(), accent) == accents.end()) {
        std::string loaded;
        for (const auto & a : accents) loaded += (loaded.empty() ? "" : ", ") + a;
        if (loaded.empty()) loaded = "none (load accent adapters)";
        fail(400, "Unknown accent " + py_repr(accent) + "; loaded accents: " + loaded + ".");
    }
    gr.accent = zonos3_accent_index(*s.accent_adapters, accent);
    gr.accent_strength = (float) num(body, "accent_strength", 1.0);
}

// R10 (zonos3.tts.plan.request_rules' controls): the steer-then-hold schedule, then steering-bundle controls and spans ->
// resolved steering (raw controls move the embedding in place; with a schedule the request-level hidden controls not
// exempt from it release).
void apply_controls(ServerState & s, const json & body, GenReq & gr) {
    const std::optional<zonos3_steer_release_frames> release = resolve_steer_release(s, body);
    if (!truthy(body, "controls") && !truthy(body, "control_spans")) return;
    if (!s.steer_loaded)
        fail(400, std::string(NO_STEERING) + "; `controls` are unavailable.");
    std::map<std::string, double> controls;
    if (set(body, "controls"))
        for (auto it = body["controls"].begin(); it != body["controls"].end(); ++it) controls[it.key()] = it.value().get<double>();
    std::vector<zonos3_control_span> spans;
    if (set(body, "control_spans"))
        for (const json & e : body["control_spans"]) {
            zonos3_control_span sp;
            sp.start = e["start"].get<int>();
            sp.end = e["end"].get<int>();
            for (auto it = e["controls"].begin(); it != e["controls"].end(); ++it) sp.controls[it.key()] = it.value().get<double>();
            spans.push_back(std::move(sp));
        }
    const double rate = set(body, "control_span_rate") ? body["control_span_rate"].get<double>() : 0.0;
    std::string err;
    if (!zonos3_steer_resolve(s.steer, controls, spans, (int) utf8_length(gr.raw_text), rate,
                              gr.speaker_mode == "embedding" ? &gr.speaker_embedding : nullptr, gr.steering, err,
                              release ? &*release : nullptr))
        fail(400, err);
}

// R13 (zonos3.tts.plan.validate_text_markers): pronunciation groups need a phoneme-token checkpoint and must be well
// formed; the start marker needs a start token and must open the text, once; pause markers need pause tokens and a
// bucket the checkpoint has.
void check_text_markers(const ServerState & s, const std::string & text) {
    const std::string err = zonos3_check_text_markers(s.model.tok, text);
    if (!err.empty()) fail(400, err);
}

// UTF-8 bytes behind a model text id of the SuperBPE tokenizer: a byte-level symbol holds one code point per byte; an
// added token its literal content. The server loads one model, so the table is built once.
int token_bytes(const zonos3_tokenizer & tok, int32_t id) {
    static const std::unordered_map<int32_t, int> table = [&tok] {
        std::unordered_map<int32_t, int> t;
        for (const auto & [sym, v] : tok.vocab) {
            int n = 0;
            for (unsigned char c : sym) n += (c & 0xC0) != 0x80;
            t[v + tok.offset] = n;
        }
        for (const auto & [content, v] : tok.added) t[v + tok.offset] = (int) content.size();
        return t;
    }();
    const auto it = table.find(id);
    return it == table.end() ? 0 : it->second;
}

// R14 (zonos3.tts.plan.check_laci; every rollback here re-prefills the slot, so no cache-type clause): heads for the
// request's language and no CFG twin (the probe reads and masks the conditional request alone).
void check_laci(const ServerState & s, const json & body, const std::string & language) {
    if (!(set(body, "laci") && body["laci"].get<bool>())) return;
    if (s.laci.empty()) fail(400, "LACI is not enabled on this server (start it with --laci-heads LANG=PATH)");
    if (!s.laci.count(language)) {
        std::string configured;
        for (const auto & [lang, heads] : s.laci) configured += (configured.empty() ? "" : ", ") + lang;
        fail(400, "No LACI alignment heads for language " + py_repr(language) + " (configured: " + configured + ")");
    }
    if (num(body, "speaker_cfg_scale", 1.0) != 1.0 || num(body, "controls_cfg_scale", 1.0) != 1.0)
        fail(400, "LACI needs speaker_cfg_scale = controls_cfg_scale = 1");
}

// R11 (zonos3.tts.plan.request_rules' span follow): the request's spans follow the reading position when it asks for
// it, has span-schedule rows and its language has alignment heads; otherwise the character-rate schedule places them.
void resolve_span_follow(const ServerState & s, const json & body, GenReq & gr) {
    if (!(set(body, "control_span_follow") && body["control_span_follow"].get<std::string>() == "reading")) return;
    if (gr.steering.spans.empty()) return;
    const auto heads = s.laci.find(gr.language);
    if (heads == s.laci.end()) return;
    const zonos3_follow::FollowConfig defaults;
    gr.follow = &heads->second;
    gr.follow_lead = num(body, "control_span_lead", defaults.lead);
    gr.follow_lag = num(body, "control_span_lag", defaults.lag);
}

// span_follow.token_char_ranges over the loaded tokenizer: the character range of `text` behind each id of its text
// span (SuperBPE offsets with the checkpoint's text markers; one range per UTF-8 byte without a SuperBPE tokenizer).
std::vector<zonos3_follow::Range> text_token_ranges(const zonos3_tokenizer & tok, const std::u32string & text) {
    zonos3_follow::BpeOffsets bpe;
    if (!tok.vocab.empty()) {
        // SuperBPE offsets: each byte-level token symbol is one code point per UTF-8 byte; a token's characters run
        // from the character of its first byte to the one of its last (tokenizers' byte-level offsets).
        bpe = [&tok](const std::u32string & piece) {
            std::vector<zonos3_follow::Range> out;
            const std::string utf8 = utf8_from_u32(piece);
            std::vector<int> char_of(utf8.size() + 1, (int) piece.size());
            for (size_t b = 0, c = 0; b < utf8.size(); ++b) {
                if (b > 0 && ((unsigned char) utf8[b] & 0xC0) != 0x80) ++c;
                char_of[b] = (int) c;
            }
            const std::vector<int32_t> ids = zonos3_tokenize(tok, utf8); // [BOS] tokens [EOS]
            size_t at = 0;
            for (size_t i = 1; i + 1 < ids.size(); ++i) {
                const size_t n = (size_t) token_bytes(tok, ids[i]);
                if (n == 0) continue;
                out.push_back({char_of[at], char_of[at + n - 1] + 1});
                at += n;
            }
            return out;
        };
    }
    zonos3_follow::TextMarkers text_markers;
    if (tok.has_phonemes) text_markers.phonemes = zonos3_follow::PhonemeMarkers{utf8_to_u32(tok.phonemes.open_marker),
                                                                               utf8_to_u32(tok.phonemes.close_marker)};
    text_markers.start = tok.start_id >= 0;
    text_markers.pauses = !tok.pause_ids.empty();
    return zonos3_follow::token_char_ranges(text, bpe, text_markers);
}

// The tokenizer's follow tokens (span_follow.span_token_ranges): each span's characters in the request text -> the
// text-span positions [a, b) of the tokens spelling them in the normalized text.
void resolve_follow_tokens(const ServerState & s, GenReq & gr) {
    if (!gr.follow) return;
    const std::u32string text = utf8_to_u32(gr.raw_text), final_ = utf8_to_u32(gr.text);
    std::vector<zonos3_follow::Range> spans;
    for (const auto & sp : gr.steering.spans) spans.push_back({sp.char_start, sp.char_end});
    const auto tokens = zonos3_follow::span_token_ranges(text, final_, spans, text_token_ranges(s.model.tok, final_));
    gr.follow_tokens.assign(tokens.begin(), tokens.end());
}

// R17 (zonos3.tts.plan.request_rules' short_text): the request may run as a short text -- short_text "auto" (else
// "off"), its language has alignment heads, and no CFG twin, control spans or LACI duration target.
bool short_text_allowed(const ServerState & s, const json & body, const std::string & language, double speaker_cfg,
                        double controls_cfg, bool duration_target) {
    const std::string mode = set(body, "short_text") ? body["short_text"].get<std::string>() : "auto";
    if (mode != "auto" && mode != "off") fail(400, "short_text must be 'auto' or 'off'");
    return mode == "auto" && s.laci.count(language) && !truthy(body, "control_spans") && speaker_cfg == 1.0 &&
           controls_cfg == 1.0 && !duration_target;
}

// zonos3.tts.short_text.prompt: a qualifying normalized text gets the pause marker and its language's filler; returns
// the text-span index of the filler's first token (the first past the marker that is not all whitespace), else
// nullopt (the text unchanged).
std::optional<int> short_text_prompt(const ServerState & s, const std::string & language, std::string & text) {
    const auto & tok = s.model.tok;
    const auto & reg = zonos3_short::registry();
    const uint32_t marker = utf8_to_u32(reg.pause_marker)[0];
    const int bucket = (int) marker - 0xE003;
    if (tok.vocab.empty() || bucket < 0 || bucket >= (int) tok.pause_ids.size()) return std::nullopt;
    const auto filler = reg.fillers.find(language);
    if (filler == reg.fillers.end()) return std::nullopt;
    const std::u32string t32 = utf8_to_u32(text);
    int words = 0;
    bool in_word = false;
    for (char32_t c : t32) {
        if (c == 0xE000 || c == 0xE001 || (c >= 0xE002 && c <= 0xE00A)) return std::nullopt;  // markers
        const bool space = is_py_space(c);
        words += !space && !in_word;
        in_word = !space;
    }
    if (words > reg.max_words) return std::nullopt;
    std::vector<int32_t> ids, filler_ids;
    std::string err;
    if (!zonos3_payload_ids(tok, text, ids, err) || !zonos3_payload_ids(tok, filler->second, filler_ids, err))
        return std::nullopt;
    if (ids.empty() || (int64_t) ids.size() * reg.max_ratio[1] > (int64_t) filler_ids.size() * reg.max_ratio[0])
        return std::nullopt;
    const std::string full = text + reg.pause_marker + filler->second;
    const std::u32string f32 = utf8_to_u32(full);
    const auto ranges = text_token_ranges(tok, f32);
    for (size_t i = 0; i < ranges.size(); ++i) {
        const auto [a, b] = ranges[i];
        if (a <= (int) t32.size() || a >= b) continue;
        bool all_space = true;
        for (int c = a; c < b; ++c) all_space = all_space && is_py_space(f32[c]);
        if (!all_space) {
            text = full;
            return (int) i;
        }
    }
    throw std::runtime_error("short text: the filler has no token");
}

// zonos3.server.speech.resolve_stream_decode for a streaming response.
bool resolve_prefix(const ServerState & s, const json & body, bool streaming) {
    if (!streaming) return false;
    if (!set(body, "stream_decode")) return s.stream_decode_default == "prefix";
    return body["stream_decode"].get<std::string>() == "prefix";
}

// GenerateRequest -> GenReq in the rules' order (zonos3.tts.plan.request_head + request_rules, which sgl
// _tts_generate_msg runs): speaker mode, condition rows, speaker, language, max_tokens, silence prefill,
// stream codebooks, accent, controls, stream decode, speaker CFG, text markers (start_immediately), LACI, short text;
// then the scheduler's CFG twin choice. `duration_target`: a LACI duration-target take.
GeneratePlan resolve(ServerState & s, const json & body, const std::optional<std::string> & session,
                     bool duration_target) {
    GeneratePlan out;
    GenReq & gr = out.gr;
    const std::string text = body["text"].get<std::string>();
    gr.decoder = resolve_decoder(s, body);
    gr.raw_text = text;
    const double speaker_cfg = num(body, "speaker_cfg_scale", 1.0), controls_cfg = num(body, "controls_cfg_scale", 1.0);
    const std::string mode = resolve_speaker_mode(s, body);
    gr.condition_buckets = resolve_condition_buckets(s, body);
    ResolvedSpeaker spk = resolve_request_speaker(s, body, session, mode);
    gr.speaker_mode = spk.mode;
    gr.reference_codes = std::move(spk.codes);
    gr.speaker_embedding = std::move(spk.embedding);
    gr.language = resolve_language(body);
    // R5 (zonos3.tts.plan.max_tokens_budget): the server's frame limit, or the request's (positive) budget below it.
    gr.max_frames = s.max_frames;
    if (set(body, "max_tokens")) {
        const int64_t requested = body["max_tokens"].get<int64_t>();
        if (requested <= 0) fail(400, "max_tokens must be positive.");
        gr.max_frames = (int) std::min<int64_t>(requested, s.max_frames);
    }
    // start_immediately, or a typed start marker: the prompt text opens with the start marker (plan.Rules.start).
    const bool start_flag = set(body, "start_immediately") && body["start_immediately"].get<bool>();
    const bool start = start_flag || text.rfind(ZONOS3_START_MARKER, 0) == 0;
    // silence_prefill_frames: unset = the server default (0 with the start marker, plan.start_silence), else 0..the
    // registry maximum (never clamped).
    gr.silence_prefill = start ? 0 : (s.silence_prefill_on ? s.silence_prefill_frames : 0);
    if (set(body, "silence_prefill_frames")) {
        const int max_prefill = zonos3_generation().silence_prefill_max_frames;
        const int64_t frames = body["silence_prefill_frames"].get<int64_t>();
        if (frames < 0 || frames > max_prefill)
            fail(400, "silence_prefill_frames must be 0.." + std::to_string(max_prefill));
        gr.silence_prefill = (int) frames;
    }
    // stream_codebooks / stream_codebooks_scope (prefix streaming's early partial-frame decode): unset = the registry's
    // stream_codebooks default / scope; above n_codebooks a 400. A decoder GGUF without the per-codebook residual
    // biases (or no decoder: zonos3-cli --request) decodes complete frames only (n_codebooks).
    const int ncb = (int) s.model.hp.n_codebooks;
    if (set(body, "stream_codebooks")) {
        const int64_t k = body["stream_codebooks"].get<int64_t>();
        if (k < 1 || k > ncb) fail(400, "stream_codebooks must be 1.." + std::to_string(ncb));
        gr.stream_codebooks = (int) k;
    }
    if (set(body, "stream_codebooks_scope")) {
        const std::string scope = body["stream_codebooks_scope"].get<std::string>();
        if (scope != "first" && scope != "all") fail(400, "stream_codebooks_scope must be first or all");
        gr.stream_codebooks_first_only = scope == "first";
    }
    if (!gr.decoder || !zac_partial_frames(gr.decoder->zac)) gr.stream_codebooks = ncb;
    apply_accent(s, body, gr);
    apply_controls(s, body, gr);
    resolve_span_follow(s, body, gr);
    // X-Control-Span-Follow: how the request's control spans are placed
    if (truthy(body, "control_spans")) out.span_follow = gr.follow ? "reading" : "rate";
    gr.stream = !set(body, "stream") || body["stream"].get<bool>();
    gr.prefix = resolve_prefix(s, body, gr.stream);
    if (speaker_cfg != 1.0) {
        const bool has_prompt = (gr.speaker_mode == "reference" && !gr.reference_codes.empty()) ||
                                (gr.speaker_mode == "embedding" && !gr.speaker_embedding.empty());
        if (!has_prompt)
            fail(400, "Speaker CFG needs a speaker prompt: speaker_mode=reference with reference audio or codes, or "
                      "speaker_mode=embedding with a clip or an embedding");
        if (controls_cfg != 1.0) fail(400, "Speaker and controls CFG cannot be combined");
        if (s.batch_slots < 2) fail(400, "speaker CFG needs --batch >= 2 (the CFG twin takes a second slot)");
        gr.cfg_scale = (float) speaker_cfg;
        out.cfg_kind = "speaker";
    }
    // Controls CFG (scheduler._schedule_prefill): a request with a hidden steering delta (constant or released) and a
    // speaker embedding runs an unconditional twin on the same prompt without the delta; otherwise controls_cfg_scale
    // does nothing.
    if (controls_cfg != 1.0 && (!gr.steering.hidden.empty() || !gr.steering.release.empty()) && request_embedding(gr)) {
        if (s.batch_slots < 2) fail(400, "controls CFG needs --batch >= 2 (the CFG twin takes a second slot)");
        gr.cfg_scale = (float) controls_cfg;
        gr.twin_steered = false;
        out.cfg_kind = "controls";
    }
    check_text_markers(s, text);
    if (start_flag && s.model.tok.start_id < 0) fail(400, "start_immediately needs a checkpoint with a start token");
    check_laci(s, body, gr.language);
    if (set(body, "laci") && body["laci"].get<bool>()) gr.laci = &s.laci.at(gr.language);
    const bool short_allowed = short_text_allowed(s, body, gr.language, speaker_cfg, controls_cfg, duration_target);

    // sampling (TTSSamplingParams): gr.sp starts at the registry's defaults; the request overrides what it sets.
    if (set(body, "temperature")) gr.sp.temperature = (float) body["temperature"].get<double>();
    if (set(body, "topk")) gr.sp.top_k = body["topk"].get<int>();
    if (set(body, "top_p")) gr.sp.top_p = (float) body["top_p"].get<double>();
    if (set(body, "min_p")) gr.sp.min_p = (float) body["min_p"].get<double>();
    if (set(body, "repetition_penalty")) gr.sp.rep_penalty = (float) body["repetition_penalty"].get<double>();
    if (set(body, "repetition_window")) gr.sp.rep_window = body["repetition_window"].get<int>();
    if (set(body, "repetition_codebooks")) gr.sp.rep_codebooks = body["repetition_codebooks"].get<int>();
    if (gr.sp.rep_codebooks < 0) gr.sp.rep_codebooks = (int) s.model.hp.n_codebooks;
    if (set(body, "seed")) gr.sp.seed = (uint32_t) body["seed"].get<uint64_t>();
    else { // secrets.randbelow(2**31)
        static std::random_device rd;
        gr.sp.seed = (uint32_t) (rd() & 0x7FFFFFFFu);
    }
    gr.sp.greedy = gr.sp.temperature <= 0.0f;
    gr.fade_out_ms = num(body, "fade_out_ms", 0.0);
    gr.headroom_db = set(body, "headroom_db") ? body["headroom_db"].get<double>() : -1.0;
    // decode_context: the last decode_context_frames (unset or 0: the registry's decode_context_frames) of the
    // reference prompt, decoded ahead of the stream.
    if (set(body, "decode_context") && body["decode_context"].get<bool>() && !gr.reference_codes.empty()) {
        const int64_t asked = set(body, "decode_context_frames") ? body["decode_context_frames"].get<int64_t>() : 0;
        const int64_t frames = asked ? asked : zonos3_generation().decode_context_frames;
        const int64_t ref_frames = (int64_t) gr.reference_codes.size() / s.model.hp.n_codebooks;
        const int64_t k = std::max<int64_t>(0, std::min(frames, ref_frames));
        gr.decode_context.assign(gr.reference_codes.end() - (size_t) k * s.model.hp.n_codebooks, gr.reference_codes.end());
    }
    // text normalization (the tokenizer's: TTSTextNormalizer when enabled and requested)
    gr.text = normalize_text(s, text, gr.language, !set(body, "text_normalization") || body["text_normalization"].get<bool>());
    if (start && gr.text.rfind(ZONOS3_START_MARKER, 0) != 0) gr.text = ZONOS3_START_MARKER + gr.text;  // with_start_marker
    if (short_allowed) {
        gr.short_text = short_text_prompt(s, gr.language, gr.text);
        if (gr.short_text) gr.laci = &s.laci.at(gr.language);
    }
    resolve_follow_tokens(s, gr);
    return out;
}

void generate(ServerState & s, const json & body, const std::optional<std::string> & session, httplib::Response & res) {
    const bool wav = set(body, "format") && body["format"].get<std::string>() == "wav";
    const bool stream = !set(body, "stream") || body["stream"].get<bool>();
    if (stream && wav) fail(400, "format=wav needs stream=false");
    GeneratePlan r = plan_generate(s, body, session);
    r.gr.wav = wav;
    const std::string span_follow = r.span_follow;
    auto job = make_job(s, std::move(r));
    const int sr = job->gr.decoder->zac.sample_rate;
    res.set_header("X-Audio-Sample-Rate", std::to_string(sr));
    res.set_header("X-Audio-Channels", "1");
    res.set_header("X-Audio-Format", wav ? "pcm16" : "float32");
    res.set_header("X-Seed", std::to_string(job->gr.sp.seed));
    res.set_header("X-Decoder", job->gr.decoder->id);
    if (!span_follow.empty()) res.set_header("X-Control-Span-Follow", span_follow);
    submit_job(s, job);

    if (job->gr.stream) {
        res.set_chunked_content_provider("audio/pcm", [job](size_t, httplib::DataSink & sink) -> bool {
            for (;;) {
                std::vector<float> block;
                bool done = false, failed = false;
                {
                    std::unique_lock<std::mutex> lk(job->m);
                    job->cv.wait(lk, [&] { return !job->pcm.empty() || job->finished; });
                    if (!job->pcm.empty()) { block = std::move(job->pcm.front()); job->pcm.pop_front(); }
                    else done = job->finished;
                    failed = job->failed;
                }
                if (!block.empty() && !sink.write((const char *) block.data(), block.size() * sizeof(float))) {
                    std::lock_guard<std::mutex> lk(job->m);
                    job->client_dropped = true;
                    return false;   // client gone; the worker evicts the slot next tick
                }
                if (done) {
                    if (failed) return false;
                    sink.done();
                    return true;
                }
            } }, [job](bool) { // the response is gone (finished, failed or never written): stop generating
            std::lock_guard<std::mutex> lk(job->m);
            if (!job->finished) job->client_dropped = true;
        });
        return;
    }
    std::vector<float> audio;
    bool failed;
    {
        std::unique_lock<std::mutex> lk(job->m);
        job->cv.wait(lk, [&] { return job->finished; });
        failed = job->failed;
        for (auto & b : job->pcm) audio.insert(audio.end(), b.begin(), b.end());
    }
    if (failed) fail(500, "generation failed");
    if (wav) {
        const std::vector<uint8_t> bytes = wav_pcm16_bytes(audio, sr);
        res.set_content((const char *) bytes.data(), bytes.size(), "audio/wav");
    } else {
        res.set_content((const char *) audio.data(), audio.size() * sizeof(float), "audio/pcm");
    }
}

// The speaker input fields a SpeechRequest `voice` cannot be combined with.
const char * SPEAKER_INPUT_FIELDS[] = {
    "speaker_audio_base64",
    "speaker_audio_name",
    "speaker_reference_codes",
    "speaker_embedding_base64",
    "speaker_embedding_name",
    "speaker_embedding_id",
    "speaker_blend_embedding_id_a",
    "speaker_blend_embedding_id_b",
    "speaker_blend_t",
};

} // namespace

bool configure_silence_prefill(ServerState & s, std::string & err) {
    const zonos3_generation_registry & limits = zonos3_generation();
    s.silence_prefill_frames = limits.silence_prefill_default_frames;
    if (const char * v = getenv("ZONOS3_SILENCE_PREFILL_FRAMES"); v && *v) {
        char * end = nullptr;
        const long x = strtol(v, &end, 10);
        if (end && !*end) s.silence_prefill_frames = (int) std::max(0L, x);
    }
    if (s.silence_prefill_frames > limits.silence_prefill_max_frames) {
        err = "ZONOS3_SILENCE_PREFILL_FRAMES must be 0.." + std::to_string(limits.silence_prefill_max_frames);
        return false;
    }
    const char * on = getenv("ZONOS3_SILENCE_PREFILL_DEFAULT");
    const std::string v = lower_ascii(strip(on ? on : "1"));
    s.silence_prefill_on = !(v == "0" || v == "false" || v == "no" || v == "off") && s.silence_prefill_frames > 0;
    return true;
}

bool configure_frame_budget(ServerState & s, std::string & err) {
    if (s.max_frames < 1 || s.max_frames + 16 >= (int) s.model.hp.n_ctx_train) {
        err = "--max must be positive and leave prompt headroom in model context";
        return false;
    }
    s.context_capacity = std::min((int) s.model.hp.n_ctx_train, s.max_frames + 4096);
    return true;
}

int fit_frame_budget(const ServerState & s, int64_t prompt_rows, int budget) {
    if (prompt_rows >= s.context_capacity)
        fail(400, "TTS input len " + std::to_string(prompt_rows) + " exceeds " + std::to_string(s.context_capacity));
    return (int) std::min<int64_t>(budget, s.context_capacity - prompt_rows);
}

std::vector<std::string> server_accents(const ServerState & s) {
    return s.accent_adapters ? zonos3_accent_names(*s.accent_adapters) : std::vector<std::string>{};
}

bool load_accent_adapters(ServerState & s, std::string & err) {
    if (s.accent_adapters_path.empty()) return true;
    auto bank = std::shared_ptr<zonos3_accents>(new zonos3_accents(), [](zonos3_accents * b) {
        zonos3_accents_free(*b);
        delete b;
    });
    if (!zonos3_accents_load(*bank, s.accent_adapters_path.c_str(), s.model, err)) return false;
    s.accent_adapters = bank;
    s.model.accents = bank.get();
    std::string names;
    for (const auto & n : bank->names) names += (names.empty() ? "" : ", ") + n;
    fprintf(stderr, "zonos3-server: accent adapters %s: %s\n", s.accent_adapters_path.c_str(), names.c_str());
    return true;
}

GeneratePlan plan_generate(ServerState & s, const json & body, const std::optional<std::string> & session,
                           bool duration_target) {
    GeneratePlan plan = resolve(s, body, session, duration_target);
    const GenReq & gr = plan.gr;
    zonos3_prompt_options opt;
    if (gr.speaker_mode == "reference" && !gr.reference_codes.empty()) {
        opt.reference_codes = gr.reference_codes.data();
        opt.reference_frames = (int) (gr.reference_codes.size() / s.model.hp.n_codebooks);
    } else if (gr.speaker_mode == "embedding" && !gr.speaker_embedding.empty()) {
        opt.speaker_embedding = gr.speaker_embedding.data();
    }
    opt.condition_buckets = gr.condition_buckets;
    opt.silence_prefill_frames = gr.silence_prefill;
    std::string err;
    if (!zonos3_build_prompt(s.model, gr.text, opt, plan.prompt, &err))
        fail(400, "could not build the prompt: " + err);
    // The prompt's rows run with the request's accent adapter.
    plan.prompt.cond.accent = gr.accent;
    plan.prompt.cond.accent_strength = gr.accent_strength;
    if (plan.cfg_kind == "speaker") {
        if (!zonos3_build_cfg_twin(s.model, plan.prompt, plan.twin))
            fail(400, "could not build the speaker-CFG twin prompt");
    } else if (plan.cfg_kind == "controls") {
        // The same prompt and speaker (the scheduler leaves the twin unsteered).
        plan.twin = plan.prompt;
    }
    // The accent adapter applies to both branches.
    plan.twin.cond.accent = gr.accent;
    plan.twin.cond.accent_strength = gr.accent_strength;
    // R5: the frame budget shrinks to the slot rows the longer of the two prompts leaves.
    plan.gr.max_frames = fit_frame_budget(s, std::max(plan.prompt.n_rows, plan.twin.n_rows), plan.gr.max_frames);
    return plan;
}

std::shared_ptr<ReqJob> make_job(ServerState & s, GeneratePlan && plan) {
    auto job = std::make_shared<ReqJob>();
    job->prompt = std::move(plan.prompt);
    job->twin = std::move(plan.twin);
    const GenReq & gr = plan.gr;
    job->idf.assign(job->prompt.ids.begin(), job->prompt.ids.end());
    job->idf_twin.assign(job->twin.ids.begin(), job->twin.ids.end());
    job->n0 = job->prompt.n_rows;
    job->ctx_frames = (int) (gr.decode_context.size() / s.model.hp.n_codebooks);
    const zac_model & zac = gr.decoder->zac;
    job->skip_samples = (int64_t) (job->prompt.prefill_frames + job->ctx_frames) * zac_samples_per_frame(zac);
    job->gain = zac_output_gain(zac, gr.headroom_db);
    job->gr = std::move(plan.gr);
    try {
        attach_probe(s, *job);
    } catch (const std::invalid_argument & e) {
        fail(400, e.what());
    }
    return job;
}

std::shared_ptr<ReqJob> plan_laci_take(ServerState & s, const LaciTake & take, const std::optional<std::string> & session) {
    const json & body = take.request;
    if (!(set(body, "laci") && body["laci"].get<bool>())) fail(400, "A LACI duration target needs laci=true");
    GeneratePlan plan = plan_generate(s, body, session, /*duration_target=*/true);
    GenReq & gr = plan.gr;
    gr.stream = false;
    gr.prefix = false;
    gr.laci_duration = take.duration;
    if (take.scale) { // _laci_take_rate_scale
        const LaciTake::Scale & sc = *take.scale;
        const auto & hp = s.model.hp;
        int index = -1;
        for (size_t i = 0; i < hp.cond_names.size(); ++i)
            if (hp.cond_names[i] == "speaking_rate") index = (int) i;
        if (index < 0) fail(400, "an extended speaking-rate scale needs the checkpoint's speaking-rate condition");
        zonos3_laci::RateBuckets rates;
        rates.index = index;
        rates.edges = sc.edges;
        rates.n_buckets = (int) hp.cond_bucket_counts[index];
        rates.first = sc.first;
        if (!(rates.first <= sc.bucket && sc.bucket <= rates.last()))
            fail(400, "speaking-rate bucket " + std::to_string(sc.bucket) + " is outside the scale [" +
                          std::to_string(rates.first) + ", " + std::to_string(rates.last()) + "]");
        const std::vector<float> base = gr.steering.hidden;
        std::vector<std::vector<float>> deltas;
        for (int b = rates.first; b <= rates.last(); ++b) {
            const auto dose = sc.doses.find(b);
            if (dose == sc.doses.end()) {
                deltas.push_back(base);
                continue;
            }
            if (!s.steer_loaded) fail(400, "a steered speaking-rate bucket needs the steering bundle (steer.gguf)");
            zonos3_steering tempo;
            std::string err;
            if (!zonos3_steer_resolve(s.steer, {{sc.direction, dose->second}}, {}, 0, 0.0, nullptr, tempo, err))
                fail(400, err);
            std::vector<float> d = tempo.hidden.empty() ? base : tempo.hidden;
            if (!tempo.hidden.empty())
                for (size_t i = 0; i < base.size() && i < d.size(); ++i) d[i] += base[i];
            deltas.push_back(std::move(d));
        }
        gr.steering.hidden = deltas[(size_t) (sc.bucket - rates.first)];
        gr.laci_rates = rates;
        gr.laci_bucket = sc.bucket;
        gr.laci_rate_deltas = std::move(deltas);
    }
    return make_job(s, std::move(plan));
}

TakeResult wait_take(const std::shared_ptr<ReqJob> & job) {
    TakeResult out;
    bool failed;
    {
        std::unique_lock<std::mutex> lk(job->m);
        job->cv.wait(lk, [&] { return job->finished; });
        failed = job->failed;
        for (auto & b : job->pcm) out.pcm.insert(out.pcm.end(), b.begin(), b.end());
    }
    if (failed) throw std::runtime_error("the LACI take failed");
    if (job->laci_record.empty()) throw std::runtime_error("the scheduler sent no LACI record");
    out.laci = json::parse(job->laci_record);
    return out;
}

void register_generate_routes(httplib::Server & svr, ServerState & s) {
    svr.Post("/v2/generate", guarded([&s](const httplib::Request & req, httplib::Response & res) {
                 const json body = schema::parse_body(req, "GenerateRequest");
                 generate(s, body, session_id(req), res);
             }));
    // OpenAI-compatible: the /v2/generate request of `input` (pcm streams, wav is buffered), with `voice` (a speaker
    // id of GET /v2/speakers) as its speaker_embedding_id.
    svr.Post("/v1/audio/speech", guarded([&s](const httplib::Request & req, httplib::Response & res) {
                 json body = schema::parse_body(req, "SpeechRequest");
                 const auto session = session_id(req);
                 const std::string format = set(body, "response_format") ? body["response_format"].get<std::string>() : "pcm";
                 if (set(body, "voice")) {
                     for (const char * field : SPEAKER_INPUT_FIELDS)
                         if (set(body, field)) fail(400, "voice cannot be combined with speaker_* fields");
                     const std::string voice = body["voice"].get<std::string>();
                     if (!speaker_known(s, session, voice)) fail(400, "unknown voice '" + voice + "'");
                     body["speaker_embedding_id"] = voice;
                 }
                 body["text"] = body["input"];
                 body["stream"] = format == "pcm";
                 body["format"] = format;
                 for (const char * k : {"model", "input", "voice", "response_format"}) body.erase(k);
                 generate(s, body, session, res);
             }));
}

} // namespace zs
