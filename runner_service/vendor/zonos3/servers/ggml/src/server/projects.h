// Zonos Voice Studio projects on the server: an exact port of model/src/zonos3/server/projects.py, so all
// servers can share one --studio-projects-dir.
//
// Layout: projects/<id>.json ({id, name, revision, updated_at, media_bytes, data}), media/<sha256> (+ <sha256>.type),
// tmp/ (atomic writes), .lock (writers: a thread lock plus flock across servers).
#pragma once

#include "json.hpp"
#include "httplib.h"

#include <cstdint>
#include <filesystem>
#include <functional>
#include <map>
#include <mutex>
#include <optional>
#include <string>
#include <utility>
#include <vector>

namespace zs {

class ProjectStore {
public:
    static constexpr int64_t MAX_PROJECT_BYTES = 20ll * 1024 * 1024;
    static constexpr int64_t MAX_MEDIA_BYTES = 2ll * 1024 * 1024 * 1024;
    static constexpr double GC_MIN_AGE_SECONDS = 3600.0;

    explicit ProjectStore(const std::filesystem::path & root, int64_t max_project_bytes = MAX_PROJECT_BYTES,
                          int64_t max_media_bytes = MAX_MEDIA_BYTES);

    nlohmann::json capabilities() const;
    nlohmann::ordered_json list();
    std::string get_bytes(const std::string & project_id);
    nlohmann::json put(const std::string & project_id, const std::string & name, const nlohmann::ordered_json & data,
                       const std::optional<int64_t> & base_revision);
    void remove(const std::string & project_id);
    nlohmann::json gc(double min_age_seconds = GC_MIN_AGE_SECONDS);
    // The blob's path and stored MIME type (empty: none); 404 when absent.
    std::pair<std::filesystem::path, std::string> media(const std::string & sha256);
    // Store a blob read through `read` (called with a sink until it returns false or the body ends) under its
    // SHA-256. A stored blob is kept and the body not read. Returns {sha256, size, created}.
    using BodyReader = std::function<bool(const std::function<bool(const char *, size_t)> &)>;
    nlohmann::json put_media(const std::string & sha256, const BodyReader & read, const std::string & mime);

    int64_t max_project_bytes, max_media_bytes;

private:
    template <class F> auto locked(F && f);
    void atomic_write(const std::filesystem::path & path, const std::string & payload);
    std::filesystem::path project_path(const std::string & project_id) const;
    void store_type(const std::string & sha256, const std::string & mime);

    std::filesystem::path root_, projects_dir_, media_dir_, tmp_dir_, lock_path_;
    std::mutex thread_lock_;
    std::mutex cache_mtx_;
    std::map<std::string, std::pair<std::pair<int64_t, int64_t>, nlohmann::ordered_json>> list_cache_;
};

// /v2/projects, /v2/projects/gc, /v2/projects/{id}, /v2/media/{sha256} on `store` (null: 404, projects disabled).
void register_project_routes(httplib::Server & svr, ProjectStore * store);

}  // namespace zs
