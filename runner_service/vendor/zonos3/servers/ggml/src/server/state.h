// The server's shared state: loaded models and companions, the speaker store, the scheduler and feature settings.
#pragma once

#include "server/pyhost.h"
#include "server/scheduler.h"
#include "server/textnorm.h"

#include "zac-encoder.h"
#include "zac.h"
#include "spk-encoder.h"
#include "ssl-encoder.h"
#include "zonos3-accent.h"
#include "zonos3-steer.h"
#include "zonos3.h"

#include <atomic>
#include <chrono>
#include <condition_variable>
#include <deque>
#include <map>
#include <memory>
#include <mutex>
#include <optional>
#include <string>
#include <thread>
#include <vector>

class zonos3_phoneme_lexicon; // zonos3-phonemes.h

namespace zs {

class ProjectStore;

// A voice: a default voice (default_voices/) or one cached in a session (POST /v2/speakers).
struct Speaker {
    std::string id, label;
    std::string source_type; // "audio" | "embedding"
    std::optional<std::string> original_name;
    std::optional<std::string> transcript; // default voices: the <voice>.txt sidecar
    std::string path, codes_path;          // default voices: the file and an audio voice's <stem>.codes.npy
    std::string embedding_path;            // default voices: an audio voice's <stem>.embedding.npy
    std::vector<int32_t> codes;            // reference codes, ordinary [frames, 10] (lazy for audio voices)
    std::vector<float> embedding;          // [speaker_dim] (lazy for audio voices)
    std::vector<float> pcm;                // 44.1 kHz audio of an audio voice (preview, lazy compute)
    double created = 0, mtime = 0, codes_mtime = 0, embedding_mtime = 0;
    bool is_default = false;
};

struct ServerState {
    // ---- models
    zonos3_model model;
    std::string model_path;
    // Decoders (--zac, repeatable; Decoder in scheduler.h): the first is the default (capabilities.decoder), every one a
    // request may pick with `decoder` (capabilities.decoders, in this order).
    std::vector<std::unique_ptr<Decoder>> decoders;
    zac_encoder_model encoder;
    ssl_model teacher;
    std::string encoder_path, teacher_path;
    bool reference_models_loaded = false;
    spk_model spk;
    std::string spk_path;
    bool spk_loaded = false;
    int reference_idle_unload_s = 60;
    std::chrono::steady_clock::time_point reference_last_use; // guarded by reference_compute_mtx
    std::string steer_path;
    bool steer_loaded = false;
    zonos3_steer_bundle steer; // its hidden sources (steer.sources) are the batch's steered bank sources
    // Silence prefill (zonos3.server.capabilities.silence_prefill_spec): ZONOS3_SILENCE_PREFILL_FRAMES (default: the registry's
    // silence_prefill_frames.default) frames for a request without silence_prefill_frames, unless
    // ZONOS3_SILENCE_PREFILL_DEFAULT is 0/false/no/off (configure_silence_prefill).
    int silence_prefill_frames = zonos3_generation().silence_prefill_default_frames;
    bool silence_prefill_on = true;
    std::string controls_doc; // GET /v2/controls

    // ---- settings
    int max_frames = zonos3_generation().max_frames; // --max: the frame budget of a request (R5's server_max)
    int context_capacity = 0;                        // rows of a slot (configure_frame_budget)
    int batch_slots = 8;
    int zac_threads = 4; // lanes per decoder
    bool use_gpu = ZONOS3_DEFAULT_USE_GPU;
    bool prof = false;
    std::string stream_decode_default = "prefix";
    PrefixStreamSettings prefix;
    std::string studio_dir;
    std::string default_voice_dir = "default_voices";
    std::string python_src;           // directory holding the zonos3 package (model/src or <exe>/python)
    std::string worker_python;        // --worker-python: interpreter of <python_src>/zonos3/workers
    TextNormalizer text_normalizer;   // in process (zyphra-textnorm); always available
    std::unique_ptr<PyHost> pyhost;   // speech editing and plugins; set when --worker-python is given
    std::vector<std::string> plugins; // --plugins: served by the Python host (server/plugins.h)
    bool fusealign_installed = false; // the --worker-python interpreter has fusealign_hub
    // Companions of later features (accents.gguf, --laci-heads LANG=PATH, --tts-phoneme-lexicon DIR); recorded here
    // so every module sees the configuration.
    std::string accent_adapters_path, phoneme_lexicon_dir;
    std::shared_ptr<zonos3_accents> accent_adapters;         // --accent-adapters, attached as model.accents
    std::shared_ptr<zonos3_phoneme_lexicon> phoneme_lexicon; // --tts-phoneme-lexicon, opened at startup (phonemize.cpp)
    std::map<std::string, std::string> laci_heads;           // --laci-heads: language -> heads JSON path
    // LACI / span-follow alignment heads by language (zonos3_laci::LaciHeads::load of laci_heads, checked against the
    // backbone's attention heads at startup) and the probe's text capacity (zonos3_batch_features::probe_max_text:
    // text rows a probed request may hold; 0 without heads).
    std::map<std::string, zonos3_laci::LaciHeads> laci;
    int probe_max_text = 0;
    std::unique_ptr<ProjectStore> projects; // --studio-projects-dir; null = disabled

    // ---- scheduler (scheduler.cpp)
    std::thread worker;
    std::atomic<bool> stop{false};
    std::mutex sched_mtx;
    std::condition_variable sched_cv;
    std::deque<std::shared_ptr<ReqJob>> incoming;
    bool worker_ready = false, worker_failed = false;

    // ---- speakers (speakers.cpp)
    std::mutex reference_mtx;                                       // guards sessions / default_speakers
    std::mutex reference_compute_mtx;                               // serializes the native encoders (not reentrant)
    std::map<std::string, std::map<std::string, Speaker>> sessions; // session -> id -> voice
    std::map<std::string, Speaker> default_speakers;                // id -> voice
    std::map<std::string, double> default_skipped;                  // unreadable embedding files already warned
    std::thread reference_reaper;
    std::mutex reference_reaper_mtx;
    std::condition_variable reference_cv;

    ~ServerState();
};

// The default decoder (the first --zac).
inline Decoder & default_decoder(const ServerState & s) {
    return *s.decoders.front();
}

} // namespace zs
