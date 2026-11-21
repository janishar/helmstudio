#include "compat.h"

#include "server/projects.h"

#include "server/http.h"
#include "server/schema.h"
#include "server/util.h"

#include "zonos3-text.h"

#include <cerrno>
#include <cmath>
#include <cstdio>
#include <fstream>
#include <cstring>
#include <regex>
#include <set>
#include <sys/stat.h>

#ifdef _WIN32
#ifndef NOMINMAX
#define NOMINMAX
#endif
#include <io.h>
#include <windows.h>
#else
#include <fcntl.h>
#include <sys/file.h>
#include <unistd.h>
#endif

namespace zs {

namespace fs = std::filesystem;
using json = nlohmann::json;
using ojson = nlohmann::ordered_json;

namespace {

constexpr double STALE_TMP_SECONDS = 86400.0;
const char * MEDIA_CACHE = "public, max-age=31536000, immutable";
constexpr size_t MAX_NAME = 200;

bool valid_id(const std::string & id) {
    if (id.empty() || id.size() > 128 || !isalnum((unsigned char) id[0])) return false;
    for (char c : id)
        if (!(isalnum((unsigned char) c) || c == '_' || c == '-')) return false;
    return true;
}

bool valid_sha(const std::string & sha) {
    if (sha.size() != 64) return false;
    for (char c : sha)
        if (!((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'))) return false;
    return true;
}

const std::string & check_id(const std::string & id) {
    if (!valid_id(id)) fail(400, "invalid project id (1-128 of A-Z a-z 0-9 _ -, not starting with _ or -)");
    return id;
}

const std::string & check_sha256(const std::string & sha) {
    if (!valid_sha(sha)) fail(400, "invalid media hash (64 lowercase hex digits of the content's SHA-256)");
    return sha;
}

// The hashes a project document references: the keys of data.media.
std::vector<std::string> media_refs(const ojson & data) {
    if (!data.is_object() || !data.contains("media") || data["media"].is_null()) return {};
    const ojson & media = data["media"];
    if (!media.is_object()) fail(400, "data.media must be an object {sha256: {type, size}}");
    std::vector<std::string> out;
    for (auto it = media.begin(); it != media.end(); ++it) out.push_back(check_sha256(it.key()));
    return out;
}

struct Stat {
    bool ok = false;
    int64_t mtime_ns = 0, size = 0;
    double mtime = 0;
};

Stat stat_of(const fs::path & p) {
    Stat s;
#ifdef _WIN32
    struct _stat64 st;
    if (_stat64(p.string().c_str(), &st) != 0) return s;
    s.mtime_ns = (int64_t) st.st_mtime * 1000000000ll;
#else
    struct stat st;
    if (stat(p.string().c_str(), &st) != 0) return s;
#if defined(__APPLE__)
    s.mtime_ns = (int64_t) st.st_mtimespec.tv_sec * 1000000000ll + st.st_mtimespec.tv_nsec;
#else
    s.mtime_ns = (int64_t) st.st_mtim.tv_sec * 1000000000ll + st.st_mtim.tv_nsec;
#endif
#endif
    s.ok = true;
    s.size = (int64_t) st.st_size;
    s.mtime = (double) s.mtime_ns * 1e-9;
    return s;
}

void touch(const fs::path & p) { // os.utime(path): mtime = now
    std::error_code ec;
    fs::last_write_time(p, fs::file_time_type::clock::now(), ec);
}

// A temp file in `dir` named <prefix><random>, opened exclusively.
FILE * make_temp(const fs::path & dir, const std::string & prefix, fs::path & path) {
    for (;;) {
        path = dir / (prefix + random_hex(8));
        FILE * f = fopen(path.string().c_str(), "wbx");
        if (f || errno != EEXIST) return f;
    }
}

bool flush_sync(FILE * f) {
    if (fflush(f) != 0) return false;
#ifdef _WIN32
    return _commit(_fileno(f)) == 0;
#else
    return fsync(fileno(f)) == 0;
#endif
}

void replace_file(const fs::path & from, const fs::path & to) { // os.replace
#ifdef _WIN32
    if (!MoveFileExW(from.wstring().c_str(), to.wstring().c_str(), MOVEFILE_REPLACE_EXISTING))
        throw std::runtime_error("cannot replace " + to.string());
#else
    fs::rename(from, to);
#endif
}

// The stored record (strict JSON, keys in file order).
ojson read_record(const fs::path & path) {
    std::string raw;
    if (!read_file(path, raw)) throw std::system_error(std::make_error_code(std::errc::no_such_file_or_directory));
    return ojson::parse(raw);
}

// An exclusive flock on the store's .lock file (other servers sharing the directory).
class FileLock {
  public:
    explicit FileLock(const fs::path & path) {
#ifdef _WIN32
        h_ = CreateFileW(path.wstring().c_str(), GENERIC_READ | GENERIC_WRITE, FILE_SHARE_READ | FILE_SHARE_WRITE, nullptr,
                         OPEN_ALWAYS, FILE_ATTRIBUTE_NORMAL, nullptr);
        if (h_ == INVALID_HANDLE_VALUE) throw std::runtime_error("cannot open " + path.string());
        OVERLAPPED ov{};
        LockFileEx(h_, LOCKFILE_EXCLUSIVE_LOCK, 0, MAXDWORD, MAXDWORD, &ov);
#else
        fd_ = open(path.string().c_str(), O_RDWR | O_CREAT | O_APPEND, 0644);
        if (fd_ < 0) throw std::runtime_error("cannot open " + path.string());
        while (flock(fd_, LOCK_EX) != 0 && errno == EINTR) {}
#endif
    }
    ~FileLock() {
#ifdef _WIN32
        OVERLAPPED ov{};
        UnlockFileEx(h_, 0, MAXDWORD, MAXDWORD, &ov);
        CloseHandle(h_);
#else
        close(fd_);
#endif
    }

  private:
#ifdef _WIN32
    HANDLE h_;
#else
    int fd_;
#endif
};

} // namespace

ProjectStore::ProjectStore(const fs::path & root, int64_t max_project_bytes, int64_t max_media_bytes)
    : max_project_bytes(max_project_bytes), max_media_bytes(max_media_bytes), root_(root),
      projects_dir_(root / "projects"), media_dir_(root / "media"), tmp_dir_(root / "tmp"), lock_path_(root / ".lock") {
    for (const fs::path & d : {projects_dir_, media_dir_, tmp_dir_}) fs::create_directories(d);
    const double cutoff = unix_now() - STALE_TMP_SECONDS;
    std::error_code ec;
    for (const auto & e : fs::directory_iterator(tmp_dir_, ec)) { // leftovers of a killed server
        const Stat st = stat_of(e.path());
        if (st.ok && st.mtime < cutoff) fs::remove(e.path(), ec);
    }
}

json ProjectStore::capabilities() const {
    return {{"enabled", true}, {"max_media_bytes", max_media_bytes}, {"max_project_bytes", max_project_bytes}};
}

template <class F>
auto ProjectStore::locked(F && f) {
    std::lock_guard<std::mutex> lk(thread_lock_);
    FileLock fl(lock_path_);
    return f();
}

void ProjectStore::atomic_write(const fs::path & path, const std::string & payload) {
    fs::path tmp;
    FILE * f = make_temp(tmp_dir_, path.filename().string() + ".", tmp);
    if (!f) throw std::runtime_error("cannot create a temp file in " + tmp_dir_.string());
    const bool ok = fwrite(payload.data(), 1, payload.size(), f) == payload.size() && flush_sync(f);
    fclose(f);
    std::error_code ec;
    if (!ok) {
        fs::remove(tmp, ec);
        throw std::runtime_error("cannot write " + path.string());
    }
    try {
        replace_file(tmp, path);
    } catch (...) {
        fs::remove(tmp, ec);
        throw;
    }
}

fs::path ProjectStore::project_path(const std::string & project_id) const {
    return projects_dir_ / (check_id(project_id) + ".json");
}

ojson ProjectStore::list() {
    std::vector<ojson> entries;
    std::set<std::string> seen;
    std::error_code ec;
    std::lock_guard<std::mutex> lk(cache_mtx_);
    for (const auto & e : fs::directory_iterator(projects_dir_, ec)) {
        if (e.path().extension() != ".json") continue;
        const std::string id = e.path().stem().string();
        if (!valid_id(id)) continue;
        const Stat st = stat_of(e.path());
        if (!st.ok) continue;
        const std::pair<int64_t, int64_t> key{st.mtime_ns, st.size};
        auto cached = list_cache_.find(id);
        if (cached == list_cache_.end() || cached->second.first != key) {
            ojson record;
            try {
                record = read_record(e.path());
            } catch (...) { continue; } // deleted meanwhile, or not ours
            if (!record.is_object()) continue;
            const ojson data = record.value("data", ojson());
            ojson entry = {
                {"id", id},
                {"name", record.value("name", ojson())},
                {"updated_at", record.value("updated_at", ojson())},
                {"revision", record.value("revision", ojson())},
                {"bytes", st.size},
                {"media_bytes", record.contains("media_bytes") ? record["media_bytes"] : ojson(0)},
                {"summary", data.is_object() && data.contains("summary") ? data["summary"] : ojson()},
            };
            cached = list_cache_.insert_or_assign(id, std::make_pair(key, std::move(entry))).first;
        }
        seen.insert(id);
        entries.push_back(cached->second.second);
    }
    for (auto it = list_cache_.begin(); it != list_cache_.end();) it = seen.count(it->first) ? std::next(it) : list_cache_.erase(it);
    auto updated = [](const ojson & e) {
        const ojson & u = e["updated_at"];
        return u.is_number() ? u.get<double>() : 0.0;
    };
    std::sort(entries.begin(), entries.end(), [&](const ojson & a, const ojson & b) {
        const double ua = updated(a), ub = updated(b);
        if (ua != ub) return ua > ub;
        return a["id"].get<std::string>() < b["id"].get<std::string>();
    });
    ojson out = ojson::array();
    for (auto & e : entries) out.push_back(std::move(e));
    return out;
}

std::string ProjectStore::get_bytes(const std::string & project_id) {
    std::string raw;
    if (!read_file(project_path(project_id), raw)) fail(404, "no such project");
    return raw;
}

json ProjectStore::put(const std::string & project_id, const std::string & name, const ojson & data,
                       const std::optional<int64_t> & base_revision) {
    const fs::path path = project_path(project_id);
    if (strip(name).empty() || utf8_length(name) > MAX_NAME)
        fail(400, "name must be a non-empty string of at most " + std::to_string(MAX_NAME) + " characters");
    if (!data.is_object()) fail(400, "data must be a JSON object");
    const auto refs = media_refs(data);
    return locked([&]() -> json {
        std::optional<int64_t> stored;
        std::error_code ec;
        if (fs::exists(path, ec)) {
            const ojson record = read_record(path);
            if (record.contains("revision") && record["revision"].is_number_integer()) stored = record["revision"].get<int64_t>();
        }
        if (base_revision != stored) {
            const char * detail = base_revision && stored ? "the project changed elsewhere"
                                  : stored                ? "a project with this id exists"
                                                          : "the project does not exist (deleted elsewhere?)";
            fail(409, detail, json{{"revision", stored ? json(*stored) : json(nullptr)}});
        }
        int64_t media_bytes = 0;
        std::vector<std::string> missing;
        for (const auto & sha : refs) {
            const Stat st = stat_of(media_dir_ / sha);
            if (st.ok) media_bytes += st.size;
            else missing.push_back(sha);
        }
        if (!missing.empty())
            fail(400, std::to_string(missing.size()) + " media not uploaded (PUT /v2/media/<sha256> first)",
                 json{{"missing", missing}});
        const int64_t revision = stored.value_or(0) + 1;
        const int64_t updated_at = (int64_t) (unix_now() * 1000);
        ojson record;
        record["id"] = project_id;
        record["name"] = name;
        record["revision"] = revision;
        record["updated_at"] = updated_at;
        record["media_bytes"] = media_bytes;
        record["data"] = data;
        const std::string payload = record.dump();
        if ((int64_t) payload.size() > max_project_bytes)
            fail(413, "project too large (" + std::to_string(payload.size()) + " > " + std::to_string(max_project_bytes) + " bytes)");
        atomic_write(path, payload);
        return {{"id", project_id}, {"revision", revision}, {"updated_at", updated_at}};
    });
}

void ProjectStore::remove(const std::string & project_id) {
    const fs::path path = project_path(project_id);
    locked([&] {
        std::error_code ec;
        if (!fs::remove(path, ec)) fail(404, "no such project");
        return 0;
    });
    std::lock_guard<std::mutex> lk(cache_mtx_);
    list_cache_.erase(project_id);
}

json ProjectStore::gc(double min_age_seconds) {
    return locked([&]() -> json {
        std::set<std::string> referenced;
        std::error_code ec;
        for (const auto & e : fs::directory_iterator(projects_dir_, ec)) {
            if (e.path().extension() != ".json") continue;
            try { // an unreadable project could reference anything: keep every blob
                const ojson record = read_record(e.path());
                for (const auto & sha : media_refs(record.value("data", ojson()))) referenced.insert(sha);
            } catch (const HttpError & err) {
                fail(500, "gc aborted: " + e.path().filename().string() + " is unreadable (" + err.body["detail"].get<std::string>() + ")");
            } catch (const std::exception & err) {
                fail(500, "gc aborted: " + e.path().filename().string() + " is unreadable (" + err.what() + ")");
            }
        }
        const double cutoff = unix_now() - min_age_seconds;
        int64_t removed = 0, freed = 0, kept = 0;
        std::vector<fs::path> paths;
        for (const auto & e : fs::directory_iterator(media_dir_, ec)) paths.push_back(e.path());
        for (const fs::path & path : paths) {
            const std::string name = path.filename().string();
            const bool is_type = name.size() > 5 && name.compare(name.size() - 5, 5, ".type") == 0;
            const std::string sha = is_type ? name.substr(0, name.size() - 5) : name;
            if (!valid_sha(sha) || referenced.count(sha)) {
                kept += name == sha;
                continue;
            }
            if (name != sha) { // a type file goes with its blob
                if (!fs::exists(media_dir_ / sha, ec)) fs::remove(path, ec);
                continue;
            }
            const Stat st = stat_of(path);
            if (!st.ok) continue;
            if (st.mtime > cutoff) {
                ++kept;
                continue;
            }
            fs::remove(path, ec);
            fs::remove(media_dir_ / (sha + ".type"), ec);
            ++removed;
            freed += st.size;
        }
        return {{"removed", removed}, {"freed_bytes", freed}, {"kept", kept}};
    });
}

std::pair<fs::path, std::string> ProjectStore::media(const std::string & sha256) {
    const fs::path path = media_dir_ / check_sha256(sha256);
    std::error_code ec;
    if (!fs::is_regular_file(path, ec)) fail(404, "no such media");
    std::string mime;
    if (read_file(media_dir_ / (sha256 + ".type"), mime)) mime = strip(mime);
    return {path, mime};
}

void ProjectStore::store_type(const std::string & sha256, const std::string & mime) {
    const fs::path type_path = media_dir_ / (sha256 + ".type");
    std::error_code ec;
    if (!mime.empty() && !fs::exists(type_path, ec)) atomic_write(type_path, mime);
}

json ProjectStore::put_media(const std::string & sha256, const BodyReader & read, const std::string & mime_in) {
    check_sha256(sha256);
    const std::string mime = strip(mime_in);
    static const std::regex MIME(R"([A-Za-z0-9][\w.+-]*/[\w.+-]+(\s*;\s*[\w.+-]+=[\w.+"-]+)*)");
    if (!mime.empty() && (utf8_length(mime) > 200 || !std::regex_match(mime, MIME))) fail(400, "invalid media type");
    const fs::path path = media_dir_ / sha256;
    std::error_code ec;
    if (fs::is_regular_file(path, ec)) {
        touch(path); // restart the GC grace period: a save referencing it follows
        store_type(sha256, mime);
        return {{"sha256", sha256}, {"size", (int64_t) fs::file_size(path, ec)}, {"created", false}};
    }
    Sha256 digest;
    int64_t size = 0;
    bool too_large = false;
    fs::path tmp;
    FILE * f = make_temp(tmp_dir_, sha256 + ".", tmp);
    if (!f) throw std::runtime_error("cannot create a temp file in " + tmp_dir_.string());
    bool write_ok = true;
    const bool read_ok = read([&](const char * data, size_t n) {
        size += (int64_t) n;
        if (size > max_media_bytes) {
            too_large = true;
            return false;
        }
        digest.update(data, n);
        if (fwrite(data, 1, n, f) != n) {
            write_ok = false;
            return false;
        }
        return true;
    });
    const bool synced = write_ok && flush_sync(f);
    fclose(f);
    try {
        if (too_large) fail(413, "media too large (> " + std::to_string(max_media_bytes) + " bytes)");
        if (!read_ok) fail(400, "the upload was interrupted");
        if (!synced) throw std::runtime_error("cannot write " + tmp.string());
        const std::string actual = digest.hex();
        if (actual != sha256) fail(400, "content does not match its hash", json{{"sha256", actual}});
        replace_file(tmp, path); // a concurrent upload of the same blob renames identical bytes
    } catch (...) {
        fs::remove(tmp, ec);
        throw;
    }
    store_type(sha256, mime);
    return {{"sha256", sha256}, {"size", size}, {"created", true}};
}

// ------------------------------------------------------------------------------------------------ routes

void register_project_routes(httplib::Server & svr, ProjectStore * store) {
    auto need = [store]() -> ProjectStore & {
        if (!store) fail(404, "server-side projects are disabled (--studio-projects-dir)");
        return *store;
    };
    auto no_store = [](httplib::Response & res) { res.set_header("Cache-Control", "no-store"); };

    svr.Get("/v2/projects", guarded([need, no_store](const httplib::Request &, httplib::Response & res) {
                const ojson list = need().list();
                no_store(res);
                res.set_content(list.dump(), "application/json");
            }));
    svr.Post("/v2/projects/gc", guarded([need, no_store](const httplib::Request & req, httplib::Response & res) {
                 double min_age = ProjectStore::GC_MIN_AGE_SECONDS;
                 if (req.has_param("min_age_seconds")) {
                     const std::string raw = req.get_param_value("min_age_seconds");
                     char * end = nullptr;
                     min_age = strtod(raw.c_str(), &end);
                     if (raw.empty() || !end || *end)
                         throw HttpError(422, json{{"detail", json::array({json{{"loc", json::array({"query", "min_age_seconds"})},
                                                                                {"msg", "Input should be a valid number"},
                                                                                {"type", "float_parsing"}}})}});
                 }
                 if (!(min_age >= 0)) fail(400, "min_age_seconds must be >= 0");
                 const json out = need().gc(min_age);
                 no_store(res);
                 send_json(res, out);
             }));
    svr.Get(R"(/v2/projects/([^/]+))", guarded([need, no_store](const httplib::Request & req, httplib::Response & res) {
                const std::string raw = need().get_bytes(req.matches[1]);
                no_store(res);
                res.set_content(raw, "application/json");
            }));
    svr.Put(R"(/v2/projects/([^/]+))", guarded([need, no_store](const httplib::Request & req, httplib::Response & res) {
                ProjectStore & s = need();
                const std::string id = req.matches[1];
                check_id(id);
                // 413 before the body is parsed; strict JSON (no NaN / Infinity) is a 400
                if ((int64_t) req.body.size() > s.max_project_bytes)
                    fail(413, "request too large (> " + std::to_string(s.max_project_bytes) + " bytes)");
                ojson parsed;
                try {
                    parsed = ojson::parse(req.body);
                } catch (const std::exception & e) {
                    fail(400, std::string("invalid JSON: ") + e.what());
                }
                // The body is a ProjectPutRequest (422 like every request schema of the API); the store checks media
                // references, revisions and sizes (400 / 409 / 413).
                if (auto errors = schema::validate("ProjectPutRequest", json::parse(parsed.dump()))) throw HttpError(422, *errors);
                std::optional<int64_t> base;
                if (parsed.contains("base_revision") && !parsed["base_revision"].is_null()) base = parsed["base_revision"].get<int64_t>();
                const json out = s.put(id, parsed["name"].get<std::string>(), parsed["data"], base);
                no_store(res);
                send_json(res, out);
            }));
    svr.Delete(R"(/v2/projects/([^/]+))", guarded([need, no_store](const httplib::Request & req, httplib::Response & res) {
                   const std::string id = req.matches[1];
                   need().remove(id);
                   no_store(res);
                   send_json(res, json{{"deleted", id}});
               }));
    svr.Get(R"(/v2/media/([^/]+))", guarded([need](const httplib::Request & req, httplib::Response & res) {
                const std::string sha = req.matches[1];
                const auto [path, mime] = need().media(sha);
                res.set_header("ETag", "\"" + sha + "\"");
                res.set_header("Cache-Control", MEDIA_CACHE);
                if (req.get_header_value("If-None-Match").find(sha) != std::string::npos) {
                    res.status = 304;
                    return;
                }
                std::error_code ec;
                const auto size = (size_t) fs::file_size(path, ec);
                auto file = std::make_shared<std::ifstream>(path, std::ios::binary);
                if (ec || !*file) fail(404, "no such media");
                if (size == 0) return res.set_content("", mime.empty() ? "application/octet-stream" : mime);
                res.set_content_provider(size, mime.empty() ? "application/octet-stream" : mime,
                                         [file](size_t offset, size_t length, httplib::DataSink & sink) {
                                             char buf[65536];
                                             file->clear();
                                             file->seekg((std::streamoff) offset);
                                             file->read(buf, (std::streamsize) std::min(length, sizeof(buf)));
                                             const auto got = (size_t) file->gcount();
                                             return got > 0 && sink.write(buf, got);
                                         });
            }));
    svr.Put(R"(/v2/media/([^/]+))", [need, no_store](const httplib::Request & req, httplib::Response & res,
                                                     const httplib::ContentReader & reader) {
        bool body_read = false;
        guarded([&](const httplib::Request &, httplib::Response & r) {
            ProjectStore & s = need();
            const std::string length = req.get_header_value("Content-Length");
            if (!length.empty() && length.find_first_not_of("0123456789") == std::string::npos &&
                std::stoll(length) > s.max_media_bytes)
                fail(413, "media too large (> " + std::to_string(s.max_media_bytes) + " bytes)");
            std::string type = req.get_param_value("type");
            if (type.empty()) type = req.get_header_value("Content-Type");
            const json out = s.put_media(req.matches[1], [&](const std::function<bool(const char *, size_t)> & sink) {
                body_read = reader([&](const char * data, size_t n) { return sink(data, n); });
                return body_read; }, type);
            no_store(r);
            send_json(r, out, out["created"].get<bool>() ? 201 : 200);
        })(req, res);
        // A body left unread (stored already, refused, or aborted) would be parsed as the next request on this
        // keep-alive connection: close it instead.
        if (!body_read) res.set_header("Connection", "close");
    });
}

} // namespace zs
