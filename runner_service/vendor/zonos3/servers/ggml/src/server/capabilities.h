// GET /v2/capabilities (api/openapi.yaml Capabilities), with sgl's semantics for the loaded model and companions.
#pragma once

#include "server/state.h"

#include "httplib.h"
#include "json.hpp"

#include <string>
#include <vector>

namespace zs {

// Languages LACI serves (sgl laci_languages: the --laci-heads languages once zonos3-laci drives generation).
std::vector<std::string> laci_languages(const ServerState & s);
nlohmann::json capabilities(const ServerState & s);
void register_capability_routes(httplib::Server & svr, ServerState & s);

}  // namespace zs
