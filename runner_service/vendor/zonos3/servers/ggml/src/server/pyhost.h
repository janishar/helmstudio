// The Python host: speech editing (/v2/edit*) and the --plugins routes run in Python, in
// <python_src>/zonos3/workers/pyhost.py (zonos3.tts.edit_runtime.EditRuntime and the plugins' FastAPI routers over
// zonos3.server.plugins.Host, with their Qwen3-ASR / FuseAlign workers), the same implementation the sgl and mlx
// servers run in-process.
//
// One persistent process under --worker-python, started on first use and again after it exited; `init` (the
// checkpoint's edit facts, its HostFacts and the plugin names) is its first request after every start. stdin / stdout
// carry JSON lines both ways, multiplexed: requests {"id", "op", ...} are answered by id, many in flight; while one
// runs, the host calls back into the served model ({"call", "op", ...}: decode_audio, embed, encode, check, fill,
// decode, laci_takes), and every call is answered on its own thread (the calls block on the scheduler). Audio crosses
// as file paths.
#pragma once

#include "json.hpp"

#include <atomic>
#include <condition_variable>
#include <map>
#include <memory>
#include <mutex>
#include <optional>
#include <string>
#include <vector>

namespace zs {

struct ServerState;

class PyHost {
public:
    explicit PyHost(ServerState & s);
    ~PyHost();   // close()
    PyHost(const PyHost &) = delete;
    PyHost & operator=(const PyHost &) = delete;

    // One request {"op": op, ...payload}: its result. Throws HttpError with the host's status and detail when it
    // answers with an error, and 503 when the host cannot start or exits before answering.
    nlohmann::json request(const std::string & op, nlohmann::json payload);
    // Ends the host's input, waits for it to exit and for every engine call to finish.
    void close();

    // A plugin lists `path` among its keepalive paths (prefix match, as zonos3.server.keepalive); starts the host
    // when it is not running (503 HttpError when it cannot).
    bool keepalive(const std::string & path);

    // The LACI take slots: takes in flight across requests (twice the running-request cap) so requests interleave.
    void acquire_laci_slot();
    void release_laci_slot();

private:
    struct Run;
    std::shared_ptr<Run> ensure_run();   // start_m_ held
    nlohmann::json exchange(Run & run, const std::string & op, nlohmann::json payload);
    void read_loop(std::weak_ptr<Run> weak);
    void answer(std::shared_ptr<Run> run, nlohmann::json call);

    ServerState & s_;
    std::vector<std::string> argv_;
    std::map<std::string, std::optional<std::string>> env_;
    std::mutex start_m_;                // serializes (re)starts and their init
    std::shared_ptr<Run> run_;          // the current process
    std::atomic<int64_t> next_id_{0};
    std::mutex calls_m_;                // guards calls_
    std::condition_variable calls_cv_;
    int calls_ = 0;                     // engine calls running
    std::mutex slots_m_;
    std::condition_variable slots_cv_;
    int free_slots_;
};

// The host's environment overrides: PYTHONPATH = <python_src> then any inherited PYTHONPATH, no VIRTUAL_ENV,
// PYTHONDONTWRITEBYTECODE=1, HF_HUB_OFFLINE=1 unless set. The startup plugin check runs with it too.
std::map<std::string, std::optional<std::string>> pyhost_environment(const ServerState & s);

// The interpreter of the host's Qwen3-ASR worker exists: ZONOS3_ASR_PYTHON, else <python_src>/../../.venv-asr/bin/python
// (set up by tools/setup_asr_env.sh).
bool asr_installed(const ServerState & s);
// zonos3.tts.edit_runtime.EditModel of the checkpoint ({frame_rate, n_codebooks, audio_pad_id, codebook_sizes,
// max_spans (0 when it cannot edit), speaker_row}): the host's `init` and the edit capabilities.
nlohmann::json edit_model(const ServerState & s);

}  // namespace zs
