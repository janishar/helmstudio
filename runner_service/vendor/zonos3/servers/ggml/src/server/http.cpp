#include "server/http.h"

#include "server/util.h"

#include <atomic>
#include <chrono>
#include <condition_variable>
#include <cstdlib>
#include <filesystem>
#include <memory>
#include <mutex>
#include <thread>

namespace zs {

void fail(int status, const std::string & detail) {
    throw HttpError(status, json{{"detail", detail}});
}

void fail(int status, const std::string & detail, const json & extra) {
    json body = {{"detail", detail}};
    for (auto it = extra.begin(); it != extra.end(); ++it) body[it.key()] = it.value();
    throw HttpError(status, std::move(body));
}

void send_json(httplib::Response & res, const json & body, int status) {
    res.status = status;
    res.set_content(body.dump(-1, ' ', false, json::error_handler_t::replace), "application/json");
}

void send_error(httplib::Response & res, int status, const std::string & detail) {
    send_json(res, json{{"detail", detail}}, status);
}

Handler guarded(Handler h) {
    return [h = std::move(h)](const httplib::Request & req, httplib::Response & res) {
        try {
            h(req, res);
        } catch (const HttpError & e) {
            res.headers.erase("Content-Type");
            send_json(res, e.body, e.status);
        } catch (const std::exception & e) {
            send_error(res, 500, e.what());
        }
    };
}

namespace {

constexpr auto KEEPALIVE_INTERVAL = std::chrono::seconds(10);

struct KeepaliveRun {
    std::mutex m;
    std::condition_variable cv;
    bool done = false;
    httplib::Response res;
    std::thread th;
    void join() {
        if (th.joinable()) th.join();
    }
};

// The body the keepalive response ends with: the endpoint's JSON, or {"detail", "status_code"} for an error.
std::string keepalive_payload(const httplib::Response & r) {
    const int status = r.status < 0 ? 200 : r.status;
    if (status < 400) return r.body;
    json detail;
    json parsed = json::parse(r.body, nullptr, false);
    if (!parsed.is_discarded() && parsed.is_object() && parsed.contains("detail")) detail = parsed["detail"];
    else if (!parsed.is_discarded() && parsed.is_object()) detail = nullptr;
    else detail = r.body;
    return json{{"detail", detail}, {"status_code", status}}.dump(-1, ' ', false, json::error_handler_t::replace);
}

} // namespace

Handler keepalive(Handler h) {
    Handler inner = guarded(std::move(h));
    return [inner](const httplib::Request & req, httplib::Response & res) {
        if (req.method != "POST" || req.get_header_value("X-Keepalive") != "1") {
            inner(req, res);
            return;
        }
        auto run = std::make_shared<KeepaliveRun>();
        // `req` outlives the response: httplib writes the body (and runs the releaser below, which joins) while the
        // connection still holds the request.
        run->th = std::thread([run, inner, &req] {
            inner(req, run->res);
            std::lock_guard<std::mutex> lk(run->m);
            run->done = true;
            run->cv.notify_all();
        });
        res.status = 200;
        res.set_header("Cache-Control", "no-store");
        res.set_header("x-keepalive", "1");
        res.set_chunked_content_provider(
            "application/json",
            [run](size_t, httplib::DataSink & sink) -> bool {
                if (!sink.write(" ", 1)) return false;
                for (;;) {
                    std::unique_lock<std::mutex> lk(run->m);
                    if (run->cv.wait_for(lk, KEEPALIVE_INTERVAL, [&] { return run->done; })) break;
                    lk.unlock();
                    if (!sink.write(" ", 1)) return false;
                }
                run->join();
                const std::string payload = keepalive_payload(run->res);
                if (!sink.write(payload.data(), payload.size())) return false;
                sink.done();
                return true;
            },
            [run](bool) { run->join(); });
    };
}

std::optional<std::string> session_id(const httplib::Request & req) {
    if (!req.has_header("X-TTS-Session-ID")) return std::nullopt;
    std::string v = strip(req.get_header_value("X-TTS-Session-ID"));
    if (v.empty()) return std::nullopt;
    return v;
}

namespace {

bool digest_equal(const std::string & a, const std::string & b) { // secrets.compare_digest
    unsigned char diff = a.size() == b.size() ? 0 : 1;
    const std::string & ref = diff ? a : b;
    for (size_t i = 0; i < a.size(); ++i) diff |= (unsigned char) (a[i] ^ ref[i]);
    return diff == 0;
}

std::string cookie_value(const httplib::Request & req, const std::string & name) {
    const std::string header = req.get_header_value("Cookie");
    size_t pos = 0;
    while (pos < header.size()) {
        size_t end = header.find(';', pos);
        if (end == std::string::npos) end = header.size();
        std::string item = header.substr(pos, end - pos);
        const size_t eq = item.find('=');
        if (eq != std::string::npos) {
            std::string key = strip(item.substr(0, eq)), value = strip(item.substr(eq + 1));
            if (value.size() >= 2 && value.front() == '"' && value.back() == '"') value = value.substr(1, value.size() - 2);
            if (key == name) return value;
        }
        pos = end + 1;
    }
    return "";
}

const char * EXPOSED = "X-Audio-Sample-Rate, X-Audio-Channels, X-Audio-Format, X-Control-Span-Follow, X-Seed, X-Decoder";

const char * reason(int status) {
    switch (status) {
    case 400: return "Bad Request";
    case 404: return "Not Found";
    case 405: return "Method Not Allowed";
    case 413: return "Request Entity Too Large";
    case 414: return "URI Too Long";
    case 416: return "Range Not Satisfiable";
    case 431: return "Request Header Fields Too Large";
    default: return status >= 500 ? "Internal Server Error" : "Error";
    }
}

} // namespace

void install_middleware(httplib::Server & svr) {
    // ZONOS3_ACCESS_TOKEN (api_server.py optional_private_access), outermost: checked before routing and before the
    // body is read. A Bearer header, the zonos3_access cookie, or GET /?access_token= (303 to / with the cookie).
    svr.set_pre_routing_handler([](const httplib::Request & req, httplib::Response & res) {
        const char * token_env = getenv("ZONOS3_ACCESS_TOKEN");
        if (!token_env || !*token_env) return httplib::Server::HandlerResponse::Unhandled;
        const std::string token = token_env;
        std::string candidate = req.get_header_value("Authorization");
        if (candidate.rfind("Bearer ", 0) == 0) candidate = candidate.substr(7);
        if (candidate.empty()) candidate = cookie_value(req, "zonos3_access");
        if (!candidate.empty() && digest_equal(candidate, token)) return httplib::Server::HandlerResponse::Unhandled;
        const std::string query = req.get_param_value("access_token");
        if (req.method == "GET" && req.path == "/" && !query.empty() && digest_equal(query, token)) {
            res.status = 303;
            res.set_header("Location", "/");
            std::string cookie = "zonos3_access=" + token + "; HttpOnly; Path=/; SameSite=strict";
            if (req.get_header_value("X-Forwarded-Proto") == "https") cookie += "; Secure";
            res.set_header("Set-Cookie", cookie);
            res.set_content("", "text/plain");
            return httplib::Server::HandlerResponse::Handled;
        }
        res.status = 403;
        res.set_content("Access denied", "text/plain; charset=utf-8");
        return httplib::Server::HandlerResponse::Handled;
    });

    // Every response: CORS (allow_origins=["*"], the exposed audio/seed headers) and, behind an access token,
    // Referrer-Policy plus Cache-Control (private-immutable for /v2/media/* successes, no-store otherwise).
    svr.set_post_routing_handler([](const httplib::Request & req, httplib::Response & res) {
        res.set_header("Access-Control-Allow-Origin", "*");
        res.set_header("Access-Control-Expose-Headers", EXPOSED);
        const char * token = getenv("ZONOS3_ACCESS_TOKEN");
        if (token && *token) {
            res.set_header("Referrer-Policy", "no-referrer");
            res.headers.erase("Cache-Control");
            const bool media_ok = req.path.rfind("/v2/media/", 0) == 0 &&
                                  (res.status == 200 || res.status == 206 || res.status == 304);
            res.set_header("Cache-Control", media_ok ? "private, max-age=31536000, immutable" : "no-store");
        }
    });

    // CORS preflight (Starlette CORSMiddleware): any origin, method and requested headers.
    svr.Options(R"(.*)", [](const httplib::Request & req, httplib::Response & res) {
        if (!req.has_header("Access-Control-Request-Method")) {
            res.set_header("Allow", "GET, HEAD, POST, PUT, DELETE, PATCH");
            send_error(res, 405, "Method Not Allowed");
            return;
        }
        res.status = 200;
        res.set_header("Access-Control-Allow-Methods", "DELETE, GET, HEAD, OPTIONS, PATCH, POST, PUT");
        const std::string asked = req.get_header_value("Access-Control-Request-Headers");
        if (!asked.empty()) res.set_header("Access-Control-Allow-Headers", asked);
        res.set_header("Access-Control-Max-Age", "600");
        res.set_content("OK", "text/plain; charset=utf-8");
    });

    // Errors without a body (unrouted paths, oversized payloads) get the {"detail"} shape.
    svr.set_error_handler([](const httplib::Request &, httplib::Response & res) {
        if (res.body.empty()) send_error(res, res.status, reason(res.status));
    });
}

void install_studio(httplib::Server & svr, const std::string & studio_dir) {
    namespace fs = std::filesystem;
    auto serve = [studio_dir](const httplib::Request & req, httplib::Response & res) {
        const std::string path = req.matches[1];
        const std::string top = path.substr(0, path.find('/'));
        if (top == "v1" || top == "v2") return send_error(res, 404, "Not Found");
        std::error_code ec;
        const fs::path root = studio_dir.empty() ? fs::path() : fs::weakly_canonical(fs::path(studio_dir), ec);
        if (path.empty()) {
            std::string html;
            if (root.empty() || !fs::is_regular_file(root / "index.html", ec) || !read_file(root / "index.html", html))
                return send_error(res, 404, "The studio is not built (pnpm --dir studio build).");
            res.set_header("Cache-Control", "no-store, max-age=0");
            res.set_content(html, "text/html; charset=utf-8");
            return;
        }
        if (root.empty()) return send_error(res, 404, "Not Found");
        // The file itself, else the page <path>.html (/studio, /lite).
        for (const fs::path & candidate : {root / fs::path(path), root / fs::path(path + ".html")}) {
            const fs::path target = fs::weakly_canonical(candidate, ec);
            const fs::path rel = target.lexically_relative(root);
            std::string body;
            if (ec || rel.empty() || *rel.begin() == ".." || !fs::is_regular_file(target, ec) || !read_file(target, body))
                continue;
            // zonos3.server.studio.studio_cache_control: hashed assets/ are immutable; pages, the service worker and
            // the manifest are no-store; every other (unhashed) file is revalidated.
            const std::string rel_path = rel.generic_string();
            const char * cache = rel_path.rfind("assets/", 0) == 0 ? "public, max-age=31536000, immutable"
                                 : ends_with(rel_path, ".html") || rel_path == "sw.js" || rel_path == "manifest.webmanifest"
                                     ? "no-store, max-age=0"
                                     : "no-cache";
            res.set_header("Cache-Control", cache);
            res.set_content(body, httplib::detail::find_content_type(target.string(), {}, "application/octet-stream"));
            return;
        }
        send_error(res, 404, "Not Found");
    };
    auto not_found = [](const httplib::Request &, httplib::Response & res) { send_error(res, 404, "Not Found"); };
    svr.Get(R"(/(.*))", serve); // GET handlers serve HEAD too
    svr.Post(R"(/(.*))", not_found);
    svr.Put(R"(/(.*))", not_found);
    svr.Delete(R"(/(.*))", not_found);
    svr.Patch(R"(/(.*))", not_found);
}

} // namespace zs
