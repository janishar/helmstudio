// Speech editing: POST /v2/edit/transcribe, /v2/edit/align, /v2/edit and the edit capabilities. The requests run in
// the Python host (server/pyhost.h: zonos3.tts.edit_runtime.EditRuntime), which calls back into the served model
// for each region's span-end-terminated generation and the decode of the spliced codes.
#pragma once

#include "server/state.h"

#include "httplib.h"
#include "json.hpp"

namespace zs {

// {speech_editing, edit_transcribe, edit_max_spans, edit_frame_rate, edit_max_seconds, edit_align_max_seconds}
nlohmann::json edit_capabilities(const ServerState & s);
void register_edit_routes(httplib::Server & svr, ServerState & s);

}  // namespace zs
