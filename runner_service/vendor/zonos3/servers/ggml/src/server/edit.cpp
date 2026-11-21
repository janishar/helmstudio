#include "compat.h"

#include "server/edit.h"

#include "server/generate.h"
#include "server/http.h"
#include "server/pyhost.h"
#include "server/schema.h"
#include "server/speakers.h"

namespace zs {

using json = nlohmann::json;

namespace {

// The request (schema-checked) through the Python host. `check` validates the body first (fail() for a 400).
json forward(ServerState & s, const char * op, const httplib::Request & req, const char * schema_name,
             void (*check)(const ServerState &, const json &) = nullptr) {
    const json body = schema::parse_body(req, schema_name);
    if (check) check(s, body);
    if (!s.pyhost) {
        if (!zonos3_supports_editing(s.model))
            fail(400, "The loaded checkpoint does not support speech editing (needs span_end_id and marker rows).");
        fail(503, "speech editing needs the Python host (--worker-python)");
    }
    return s.pyhost->request(op, {{"request", body}});
}

}  // namespace

// speech_editing: an edit checkpoint, the Python host with FuseAlign (fusealign_hub in the --worker-python
// interpreter) and the reference encoder (the clip's codes). edit_transcribe: speech_editing and the ASR interpreter
// (ZONOS3_ASR_PYTHON / .venv-asr), which transcribes clips sent without their transcript and runs verify.
json edit_capabilities(const ServerState & s) {
    const json m = edit_model(s);
    const bool editing = m["max_spans"].get<int>() > 0;
    const bool available = s.pyhost && s.fusealign_installed && reference_audio_available(s);
    const bool speech_editing = editing && available;
    // The registry's speech-edit clip limit bounds transcribe / align and every edit (zonos3.tts.edit_runtime).
    const double max_seconds = zonos3_generation().edit_max_seconds;
    return {{"speech_editing", speech_editing}, {"edit_transcribe", speech_editing && asr_installed(s)},
            {"edit_max_spans", m["max_spans"]}, {"edit_frame_rate", editing ? m["frame_rate"] : json()},
            {"edit_max_seconds", max_seconds}, {"edit_align_max_seconds", max_seconds}};
}

void register_edit_routes(httplib::Server & svr, ServerState & s) {
    svr.Post("/v2/edit/transcribe", keepalive([&s](const httplib::Request & req, httplib::Response & res) {
        send_json(res, forward(s, "edit_transcribe", req, "EditTranscribeRequest"));
    }));
    svr.Post("/v2/edit/align", keepalive([&s](const httplib::Request & req, httplib::Response & res) {
        send_json(res, forward(s, "edit_align", req, "EditAlignRequest"));
    }));
    // The edit decodes on the request's `decoder` (the Python host passes it back in its decode engine calls).
    svr.Post("/v2/edit", keepalive([&s](const httplib::Request & req, httplib::Response & res) {
        send_json(res, forward(s, "edit", req, "EditRequest", [](const ServerState & st, const json & body) {
                      resolve_decoder(st, body);
                  }));
    }));
}

}  // namespace zs
