// Server plugins (--plugins NAME...): plugin N's routes (/v2/N, /v2/N/*, and GET|HEAD /N, /N/*) are forwarded to the
// Python host (server/pyhost.h), which mounts the zonos3_N package's FastAPI router (zonos3.server.plugins). Request
// and response bodies cross as temporary files.
#pragma once

#include "server/state.h"

#include "httplib.h"

namespace zs {

// Registers every plugin's routes; call before install_studio (its catch-all must match last).
void register_plugin_routes(httplib::Server & svr, ServerState & s);

}  // namespace zs
