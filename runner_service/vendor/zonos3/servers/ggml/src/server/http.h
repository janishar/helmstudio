// HTTP plumbing of zonos3-server: {"detail"} errors, CORS, the ZONOS3_ACCESS_TOKEN gate, the X-Keepalive wrapper,
// X-TTS-Session-ID and the studio serving rule (servers/api/openapi.yaml info.description).
#pragma once

#include "httplib.h"
#include "json.hpp"

#include <exception>
#include <functional>
#include <optional>
#include <string>

namespace zs {

using json = nlohmann::json;
using Handler = std::function<void(const httplib::Request &, httplib::Response &)>;

// An HTTP error raised inside a handler: rendered as `body` (an Error {detail, ...} or a 422 ValidationError).
struct HttpError : std::exception {
    int status;
    json body;
    HttpError(int status, json body) : status(status), body(std::move(body)) {}
    const char * what() const noexcept override { return "HttpError"; }
};
// HTTPException(status_code=status, detail=detail).
[[noreturn]] void fail(int status, const std::string & detail);
[[noreturn]] void fail(int status, const std::string & detail, const json & extra);

void send_json(httplib::Response & res, const json & body, int status = 200);
void send_error(httplib::Response & res, int status, const std::string & detail);

// Runs `h`, rendering HttpError and any other exception (500 "<what>") as JSON error bodies.
Handler guarded(Handler h);
// X-Keepalive: 1 on a POST (keepalive.py): answers 200 at once, a space every 10 s while `h` runs on its own thread,
// then h's JSON body; an error status travels in the body as {"detail", "status_code"}. Other requests run `h`.
Handler keepalive(Handler h);

// X-TTS-Session-ID, stripped; nullopt when absent or blank (zonos3.server.voices.normalize_session_id).
std::optional<std::string> session_id(const httplib::Request & req);

// CORS (allow every origin, expose the audio/seed/decoder headers), the access-token gate and the 404 body filler.
void install_middleware(httplib::Server & svr);
// The studio serving rule, registered after every API route (GET|HEAD / and /<file>; any other method 404).
void install_studio(httplib::Server & svr, const std::string & studio_dir);

} // namespace zs
