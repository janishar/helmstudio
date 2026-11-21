// Plugin routes: each request of /v2/<name>(/...) and GET|HEAD /<name>(/...) goes to the Python host's `http` op, which
// runs it through the plugin's FastAPI router (model/src/zonos3/workers/pyhost.py); the request body and the response
// body cross as files in a temporary directory.
#include "compat.h"

#include "server/plugins.h"

#include "server/http.h"
#include "server/pyhost.h"
#include "server/util.h"

#include <cstdio>
#include <filesystem>

namespace zs {

namespace fs = std::filesystem;
using json = nlohmann::json;

namespace {

// A scratch directory removed with its contents (tempfile.TemporaryDirectory).
struct TempDir {
    fs::path path;
    TempDir() {
        path = fs::temp_directory_path() / ("zonos3-plugin-" + random_hex(16));
        fs::create_directories(path);
    }
    ~TempDir() { std::error_code ec; fs::remove_all(path, ec); }
    TempDir(const TempDir &) = delete;
    TempDir & operator=(const TempDir &) = delete;
    std::string file(const std::string & name) const { return (path / name).string(); }
};

// The request headers a plugin sees (ASGI names) and the response headers it may set.
const char * const REQUEST_HEADERS[] = {"Content-Type", "X-TTS-Session-ID"};
const char * const RESPONSE_HEADERS[] = {"Content-Disposition", "Cache-Control"};

// One request through the plugin's router: its status, headers and body; an error status is an HttpError with the
// router's JSON body ({"detail"} or a 422 ValidationError).
void forward(ServerState & s, const std::string & name, const httplib::Request & req, httplib::Response & res) {
    if (!s.pyhost) fail(503, "plugin " + name + " needs the Python host (--worker-python)");
    TempDir tmp;
    json body = nullptr;
    if (!req.body.empty()) {
        const std::string path = tmp.file("body");
        FILE * f = fopen(path.c_str(), "wb");
        if (!f) fail(500, "cannot write a temporary request body");
        const bool ok = fwrite(req.body.data(), 1, req.body.size(), f) == req.body.size();
        if (fclose(f) != 0 || !ok) fail(500, "cannot write a temporary request body");
        body = path;
    }
    json headers = json::array();
    for (const char * h : REQUEST_HEADERS)
        if (req.has_header(h)) headers.push_back(json::array({lower_ascii(h), req.get_header_value(h)}));
    const size_t q = req.target.find('?');   // the raw query (req.params also holds a form body's fields)
    const std::string query = q == std::string::npos ? std::string() : req.target.substr(q + 1);
    const std::string out = tmp.file("out");
    // GET handlers serve HEAD (httplib drops the body); the router sees GET.
    const std::string method = req.method == "HEAD" ? "GET" : req.method;
    const json reply = s.pyhost->request("http", {{"plugin", name}, {"method", method}, {"path", req.path},
                                                  {"query", query}, {"headers", headers}, {"body", body}, {"out", out}});
    const int status = reply.at("status").get<int>();
    std::string data;
    read_file(out, data);   // no file: an empty body
    if (status >= 400) {
        json parsed = json::parse(data, nullptr, false);
        if (parsed.is_discarded() || !parsed.is_object()) parsed = {{"detail", data}};
        throw HttpError(status, std::move(parsed));
    }
    std::string content_type = "application/octet-stream";
    for (const json & h : reply.value("headers", json::array())) {
        const std::string key = lower_ascii(h.at(0).get<std::string>()), value = h.at(1).get<std::string>();
        if (key == "content-type") content_type = value;
        for (const char * allowed : RESPONSE_HEADERS)
            if (key == lower_ascii(allowed)) res.set_header(allowed, value);
    }
    res.status = status;
    res.set_content(data, content_type);
}

}  // namespace

void register_plugin_routes(httplib::Server & svr, ServerState & s) {
    for (const std::string & name : s.plugins) {
        Handler h = [&s, name](const httplib::Request & req, httplib::Response & res) { forward(s, name, req, res); };
        Handler alive = keepalive(h), plain = guarded(h);
        // X-Keepalive only on the POST paths the plugin lists (KeepaliveMiddleware of the Python servers).
        Handler route = [&s, alive, plain](const httplib::Request & req, httplib::Response & res) {
            bool listed = false;
            if (req.method == "POST" && req.get_header_value("X-Keepalive") == "1" && s.pyhost) {
                try {
                    listed = s.pyhost->keepalive(req.path);
                } catch (const HttpError &) {   // the host cannot start: the plain route answers 503
                }
            }
            (listed ? alive : plain)(req, res);
        };
        const std::string api = "/v2/" + name + "(/.*)?", page = "/" + name + "(/.*)?";
        svr.Get(api, route);   // GET handlers serve HEAD too
        svr.Post(api, route);
        svr.Put(api, route);
        svr.Delete(api, route);
        svr.Patch(api, route);
        svr.Get(page, route);
    }
}

}  // namespace zs
