// Request validation against servers/api/openapi.json (embedded at build time) with pboettch/json-schema-validator.
#pragma once

#include "json.hpp"
#include "httplib.h"

#include <optional>
#include <string>

namespace zs::schema {

using json = nlohmann::json;

// The embedded API spec (servers/api/openapi.json).
const json & spec();
// Validate `body` against #/components/schemas/<name>: nullopt when it conforms, else the 422 ValidationError body
// {"detail": [{"loc": ["body", ...], "msg", "type": "value_error"}, ...]}.
std::optional<json> validate(const std::string & name, const json & body);
// Parse the request body as JSON and validate it against <name>; throws a 422 HttpError when either fails.
json parse_body(const httplib::Request & req, const std::string & name);

}  // namespace zs::schema
