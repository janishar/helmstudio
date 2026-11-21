// POST /v2/generate and POST /v1/audio/speech: a GenerateRequest (or SpeechRequest) resolved against the loaded model,
// scheduled, and answered as streamed / buffered float32 PCM or a buffered 16-bit WAV.
#pragma once

#include "server/state.h"

#include "httplib.h"
#include "json.hpp"

#include <optional>
#include <string>
#include <map>
#include <memory>
#include <vector>

namespace zs {

// A validated GenerateRequest resolved into what the scheduler runs (sgl _tts_generate_msg, the tokenizer worker and
// TTSScheduler._schedule_prefill): the request settings, the conditional prompt and, for CFG, its twin.
struct GeneratePlan {
    GenReq gr;
    zonos3_prompt prompt;
    zonos3_prompt twin; // n_rows == 0: no CFG twin
    // "" | "speaker" (speaker row / reference prefix dropped) | "controls" (same prompt, no steering delta)
    std::string cfg_kind;
    std::string span_follow; // X-Control-Span-Follow: "" (no control_spans) | "rate" | "reading"
};

// Throws HttpError (fail) with sgl's status and detail for an invalid request. The plan's frame budget is the
// request's max_tokens (at most --max) shrunk to the slot rows its prompt leaves (fit_frame_budget).
// `duration_target`: a LACI duration-target take (plan_laci_take), which never runs as a short text.
GeneratePlan plan_generate(ServerState & s, const nlohmann::json & body, const std::optional<std::string> & session,
                           bool duration_target = false);

// The job of a planned request: the decoder settings and, for LACI / span follow, the alignment probe
// (attach_probe). fail() 400 when it cannot run; submit it with submit_job.
std::shared_ptr<ReqJob> make_job(ServerState & s, GeneratePlan && plan);

// R5 against the slot context capacity: a prompt of prompt_rows >= capacity is a 400 "TTS input len {rows} exceeds
// {capacity}"; otherwise the budget shrinks to min(budget, capacity - prompt_rows).
int fit_frame_budget(const ServerState & s, int64_t prompt_rows, int budget);

// A LACI take with a duration target (the Python host's laci_takes call; zonos3.server.plugins.LaciTakeRequest).
// `request` is a GenerateRequest body (laci=true, stream=false); `duration` the take's LACI duration target. `scale`
// puts the take on an extended speaking-rate scale (zonos3.tts.laci.RateBuckets):
// in a steered bucket (one of `doses`) the take's hidden delta gains `doses[bucket]` of the bundle direction
// `direction` (steer.gguf rate_extension); the take carries its delta in every bucket so a rate-adjusting retake can
// switch buckets. Without `scale` retakes move along the checkpoint's speaking-rate row.
struct LaciTake {
    nlohmann::json request;
    zonos3_laci::DurationTarget duration;
    struct Scale {
        std::vector<double> edges; // bucket edges (UTF-8 bytes / s); buckets first..first + edges.size()
        int first = 0;
        int bucket = 0;              // the take's bucket on the scale
        std::map<int, double> doses; // steered buckets -> tempo dose
        std::string direction;       // steering-bundle direction id of the tempo dose
    };
    std::optional<Scale> scale;
};
// Validates and plans a take like /v2/generate (fail(): HttpError 400 for a bad voice / control / text / scale).
// Plan every take first, then submit_job each and wait_take (the takes interleave with other requests).
std::shared_ptr<ReqJob> plan_laci_take(ServerState & s, const LaciTake & take, const std::optional<std::string> & session);
struct TakeResult {
    std::vector<float> pcm; // 44.1 kHz mono float32 with the decoder gain (empty: no audio)
    nlohmann::json laci;    // LaciSession.record()
};
// Blocks until the job finishes; throws std::runtime_error when the generation failed.
TakeResult wait_take(const std::shared_ptr<ReqJob> & job);

// The silence-prefill default (zonos3.server.capabilities.silence_prefill_spec) from the environment: ZONOS3_SILENCE_PREFILL_FRAMES (default
// and unparsable: the registry's silence_prefill_frames.default) frames, off when ZONOS3_SILENCE_PREFILL_DEFAULT is
// 0/false/no/off. False when above the registry's maximum.
bool configure_silence_prefill(ServerState & s, std::string & err);
// --max against the loaded backbone: the frame budget must be positive and leave prompt headroom; sets the slot
// context capacity (min(n_ctx_train, max_frames + 4096)). Call after the backbone is loaded. False with the reason.
bool configure_frame_budget(ServerState & s, std::string & err);

// Loads the accent adapters (accents.gguf; attached as model.accents) recorded in `s` (empty path = none). Call after
// the backbone is loaded. False with the reason in `err`.
bool load_accent_adapters(ServerState & s, std::string & err);
// The loaded accent adapters' names (zonos3.tts.plan RulesContext.accent_names).
std::vector<std::string> server_accents(const ServerState & s);

// Request rules shared with /v2/edit (fail() with the rules' 400 / 500 details):
// R4 zonos3.languages.normalize (body["language"], default en_us).
std::string resolve_language(const nlohmann::json & body);
// The request's `decoder` (Capabilities.decoders): absent / null = the default (s.decoders[0]; nullptr when no decoder
// is loaded, zonos3-cli --request); an id the server does not serve is a 400 "Unknown decoder 'X'; this server serves:
// a, b.".
Decoder * resolve_decoder(const ServerState & s, const nlohmann::json & body);
// R3 zonos3.tts.plan.resolve_condition_buckets (condition_buckets / condition_values): one bucket per condition row,
// hp.cond_bucket_counts[i] = unknown.
std::vector<int32_t> resolve_condition_buckets(const ServerState & s, const nlohmann::json & body);
// R10's first step (zonos3.tts.steering.request_release): the request's steer_hold / steer_onset_seconds /
// steer_ramp_seconds over the bundle's steer_release, in codec frames; nullopt = constant controls (hold 1). A hold below 1
// without the bundle key is a 400, with or without `controls`.
std::optional<zonos3_steer_release_frames> resolve_steer_release(const ServerState & s, const nlohmann::json & body);
// The tokenizer's TTSTextNormalizer step (the Rust normalizer of third_party/zyphra-textnorm, in process): `text` unchanged unless
// requested.
std::string normalize_text(ServerState & s, const std::string & text, const std::string & language, bool requested);

void register_generate_routes(httplib::Server & svr, ServerState & s);

} // namespace zs
