// Voices (servers/api/openapi.yaml Speaker): default voices from --tts-default-voices-dir, session voices from
// POST /v2/speakers, and the speaker prompt of a generation request (embedding, reference codes, blends).
#pragma once

#include "server/state.h"

#include "httplib.h"
#include "json.hpp"

#include <optional>
#include <string>
#include <vector>

namespace zs {

// The speaker paths of the checkpoint, in sgl _speaker_modes order: the raw reference prefix, then the speaker row.
std::vector<std::string> checkpoint_speaker_modes(const ServerState & s);
bool reference_audio_available(const ServerState & s); // zac-encoder.gguf + ssl-teacher.gguf
bool speaker_encoder_available(const ServerState & s); // spk-encoder.gguf

// The speaker prompt of a GenerateRequest for `mode` ("embedding" | "reference" | "" = none): empty when the request
// names no speaker. Throws HttpError 400 with sgl's messages.
struct ResolvedSpeaker {
    std::string mode;
    std::vector<int32_t> codes;   // reference: ordinary [frames, 10]
    std::vector<float> embedding; // embedding: [speaker_dim]
};
ResolvedSpeaker resolve_request_speaker(ServerState & s, const nlohmann::json & req,
                                        const std::optional<std::string> & session, const std::string & mode);
// Whether `id` is a default voice or one of the session's voices.
bool speaker_known(ServerState & s, const std::optional<std::string> & session, const std::string & id);

// A base64 request field (zonos3.reference.synth.decode_base64_blob, "data:" URLs allowed); HttpError 400 on an invalid payload.
std::vector<uint8_t> decode_b64_field(const nlohmann::json & req, const char * field);
// Uploaded audio (any container ffmpeg reads) -> mono PCM at the API rate, cut at max_seconds (default: the registry's
// reference clip limit); HttpError 400 when undecodable.
std::vector<float> decode_audio_bytes(const std::vector<uint8_t> & bytes,
                                      int max_seconds = (int) zonos3_generation().reference_audio_max_seconds);
// The codec encoder's ordinary codes [frames, 10] / the speaker encoder's embedding of 44.1 kHz PCM of at most
// max_seconds (HttpError 400 / 500 like the speaker paths).
std::vector<int32_t> encode_audio_codes(ServerState & s, const std::vector<float> & pcm, int max_seconds);
std::vector<float> embed_audio(ServerState & s, const std::vector<float> & pcm, int max_seconds);

void register_speaker_routes(httplib::Server & svr, ServerState & s);
void start_reference_reaper(ServerState & s); // frees the idle reference encoder + SSL teacher
void stop_reference_reaper(ServerState & s);

} // namespace zs
