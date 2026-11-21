// Continuous-batching scheduler: one worker thread owns the backbone and a zonos3_batch_ctx and only produces codes;
// ZAC decode runs on each decoder's pool of lanes (each its own zac_model sharing that decoder's weights). HTTP
// handlers enqueue a ReqJob and read PCM blocks back through its channel.
#pragma once

#include "zonos3.h"
#include "zonos3-sampler.h"
#include "zonos3-steer.h"
#include "zac.h"
#include "zonos3-laci.h"
#include "zonos3-span-follow.h"

#include <atomic>
#include <chrono>
#include <condition_variable>
#include <cstdint>
#include <optional>
#include <deque>
#include <memory>
#include <mutex>
#include <string>
#include <utility>
#include <thread>
#include <vector>

namespace zs {

struct ServerState;
struct Decoder;

// A generation request resolved against the loaded model (generate.cpp builds it from a GenerateRequest).
struct GenReq {
    std::string text;                       // tokenized text (normalized when normalization ran)
    std::string raw_text;                   // the request's text; control spans index it
    std::string language;
    zonos3_sampling sp;
    int max_frames = 0;
    std::string speaker_mode;               // "embedding" | "reference" | "" (no speaker prompt)
    std::vector<int32_t> reference_codes;   // reference mode: ordinary [frames, n_codebooks]
    std::vector<float> speaker_embedding;   // embedding mode: [speaker_dim]
    std::vector<int32_t> condition_buckets; // [n_cond]; hp.cond_bucket_counts[i] = unknown
    int silence_prefill = zonos3_generation().silence_prefill_default_frames;
    std::vector<int32_t> decode_context;    // decode_context: the reference's last frames, ordinary [K, n_codebooks]
    float cfg_scale = 1.0f;                 // speaker / controls CFG (1 = off; runs a twin slot)
    bool twin_steered = true;               // the twin decodes with the hidden steering delta (false: controls CFG,
                                            // whose twin is the same prompt without the steering)
    int accent = -1;                        // accent adapter of model.accents (-1 = none), on both CFG branches
    float accent_strength = 0.0f;
    Decoder * decoder = nullptr;            // the decoder of the audio (resolve_decoder; null only without decoders)
    double headroom_db = -1.0;              // < 0: the decoder's own headroom
    double fade_out_ms = 0;
    bool stream = true;
    bool wav = false;
    bool prefix = false;                    // streaming with stream_decode=prefix
    // Early partial-frame decode (prefix streaming): a frame decodes once its first stream_codebooks codebooks
    // arrived (n_codebooks = complete frames only), on the first chunk only when stream_codebooks_first_only.
    int stream_codebooks = zonos3_generation().stream_codebooks_default;
    bool stream_codebooks_first_only = zonos3_generation().stream_codebooks_first_only;
    zonos3_steering steering;
    // Speech-edit infill (pyhost.cpp): the stop id (-1 = EOA; span-end for an infill), masked until min_frames frames
    // are generated (sgl min_frames); a codes-only job finishes with its generated rows in ReqJob::codes and decodes
    // nothing.
    int stop_id = -1;
    int min_frames = 0;
    bool codes_only = false;
    // LACI (laci=true; zonos3-laci.h): the language's alignment heads (null = off) and, for a duration-target take, its
    // duration target and extended speaking-rate scale (nullopt: the checkpoint's speaking-rate row) with the take's
    // bucket on it and its hidden steering delta in every bucket of the scale (rates.first..rates.last, steering
    // layout; empty vector = none). A rollback re-prefills the prompt plus the kept rows (zonos3_laci::Continuation).
    const zonos3_laci::LaciHeads * laci = nullptr;
    std::optional<zonos3_laci::DurationTarget> laci_duration;
    std::optional<zonos3_laci::RateBuckets> laci_rates;
    std::optional<int> laci_bucket;
    std::optional<std::vector<std::vector<float>>> laci_rate_deltas;
    // A short text (zonos3-short-text.h; R17): the filler's first text-span token, which runs under `laci` with the
    // text's pause marker and filler (nullopt: an ordinary request).
    std::optional<int> short_text;
    // control_span_follow "reading" (zonos3-span-follow.h): the language's heads (null = the character-rate schedule
    // of steering.spans), each steering span's text-span token range [a, b) and the follow gate.
    const zonos3_laci::LaciHeads * follow = nullptr;
    std::vector<std::pair<int, int>> follow_tokens;
    double follow_lead = 1.0, follow_lag = 0.0;
};

// A ZAC decode unit handed from the backbone worker to the job's lane, with a snapshot of the code rows (the worker
// keeps appending to job->codes). WINDOW stream-decodes frames up to f_hi (prefix streaming); FINALIZE continues the
// job's stream to the end and flushes it, then finishes the channel. `drop` finishes without decoding
// (client gone); `fail` finishes as failed.
struct ReqJob;
struct ZacTask {
    std::shared_ptr<ReqJob> job;
    std::vector<int32_t> codes;   // delayed rows [H, n_codebooks], decode-context rows first
    int H = 0;
    enum Kind { WINDOW, FINALIZE } kind = WINDOW;
    int f_hi = 0;
    bool drop = false;
    bool fail = false;
};

struct ZacLane {
    zac_model zac;
    std::thread th;
    std::mutex m;
    std::condition_variable cv;
    std::deque<ZacTask> q;
};

// A loaded decoder (one zac.gguf), identified by its head variant: its weights and its decode pool, one backend +
// thread per lane (ggml backends are not reentrant) sharing the weights. Each lane keeps its own graph caches.
struct Decoder {
    std::string id;   // mossdual_istft.head.variant (keep2_plain | full_mr)
    std::string path; // the zac.gguf
    zac_model zac;    // owns the weights shared by every lane
    std::vector<ZacLane> lanes;
    std::atomic<uint64_t> next_lane{0}; // the backbone worker and submit_decode (handler threads) pick lanes
    ~Decoder();
};

struct ReqJob {
    // request (handler-filled, read-only to the worker)
    GenReq gr;
    zonos3_prompt prompt;
    std::vector<float> idf;              // prompt ids [n0 * W] as floats
    int n0 = 0;
    zonos3_prompt twin;                  // speaker-CFG twin prompt (empty without CFG)
    std::vector<float> idf_twin;
    int ctx_frames = 0;                  // decode_context frames decoded ahead of the stream (gr.decode_context)
    int64_t skip_samples = 0;            // decoded audio never emitted: decode context + silence prefill
    float gain = 1.0f;                   // decoder headroom, applied by the lane

    // runtime (worker-owned)
    zonos3_slot slot;
    std::unique_ptr<zonos3_sampler> smp;
    std::vector<float> logits;
    zonos3_slot twin_slot;
    int twin_index = -1;
    std::vector<float> twin_logits, guided;
    std::vector<int32_t> codes;          // delayed audio-stream rows (silence prefill first)
    int eos_frame = -1;
    int emitted = 0;                     // prefix streaming: frames handed to the lane
    int lane = 0;                        // of gr.decoder
    zac_stream dst;                      // lane-owned incremental decoder state (prefix streaming)

    std::chrono::steady_clock::time_point t_decode0;
    double decode_step_ms = 0.0;
    // Alignment probe (LACI / span follow; attach_probe, then worker-owned). The session owns the stream: only drained
    // rows reach `codes`; a rollback re-prefills the slot in place.
    zonos3_align_probe probe;            // heads and text span of the conditional prompt
    std::unique_ptr<zonos3_follow::SpanFollower> follower;
    std::unique_ptr<zonos3_laci::LaciSession> laci;
    zonos3_laci::ProbeControl probe_ctl;
    bool readout_ready = false;          // slot.readout holds the last decode step's, not yet observed
    int history_rows = 0;                // generated rows before the current attempt (steering / follower frames)
    int attempt_max = 0;                 // the current attempt's row budget (max_frames, then Continuation::max_tokens)
    std::string laci_record;             // LaciSession.record() as JSON once final (empty: not a LACI job)

    // channel: ZAC lane -> handler
    std::mutex m;
    std::condition_variable cv;
    std::deque<std::vector<float>> pcm;
    bool finished = false;
    bool failed = false;
    bool client_dropped = false;
};

// Prefix streaming: a chunk once `need` new frames are decodable beyond ZONOS3_PREFIX_STREAM_HOLD_BACK_FRAMES held-back
// frames, need = min(CHUNK_FRAMES, FIRST_CHUNK_FRAMES + frames emitted past the silence prefill): chunks ramp 1, 2, 4,
// 8, then steady 15 (earliest first audio). CONTEXT_FRAMES is validated but unused.
struct PrefixStreamSettings {
    int chunk_frames = 15;
    int first_chunk_frames = 1;
    int hold_back_frames = 0;
    int context_frames = 450;
};

// The frames of a prefix stream decodable now (zonos3.tts.stream.PrefixDecoder.ready): `rows` delayed rows [H,
// ncb] (silence-prefill rows first), `complete` frames undelayable (H - (ncb - 1), capped at the EOS frame), the EOS
// frame (-1: not yet), `emitted` frames handed to the codec (prefill frames at the start). With codebooks < ncb (and
// not first_only past the first chunk) a frame is decodable once its first `codebooks` codebooks arrived, capped at
// EOS and walked back over rows whose semantic code is EOA / padding (>= audio_pad_id - 1).
int prefix_ready(const int32_t * rows, int H, int ncb, int32_t audio_pad_id, int complete, int eos_frame, int emitted,
                 int prefill, int codebooks, bool first_only);
// The next prefix chunk's end frame (> emitted), or -1 while fewer than `need` new frames are decodable behind the
// hold-back.
int prefix_chunk_end(const PrefixStreamSettings & ps, int ready, int emitted, int prefill);
void start_scheduler(ServerState & s);   // every decoder's ZAC lanes + backbone worker; waits until the worker is ready
void stop_scheduler(ServerState & s);
// Loads the decoder at `path` (zac_load, on the GPU backend when use_gpu) with s.zac_threads lanes and appends it to
// s.decoders (the first loaded is the default). False with the reason in `err`: unreadable GGUF, or a head variant
// already loaded. Call before start_scheduler.
bool load_decoder(ServerState & s, const std::string & path, bool use_gpu, std::string & err);
void submit_job(ServerState & s, const std::shared_ptr<ReqJob> & job);
// Whole-decode the job's delayed code rows (job->codes; eos_frame < 0 = every complete frame) on a lane of
// job->gr.decoder without the backbone; the PCM arrives on the job's channel like a generation's.
void submit_decode(ServerState & s, const std::shared_ptr<ReqJob> & job);

// The alignment probe of a job whose prompt is built (gr.laci / gr.follow): the probed text span, the span follower and
// the LACI session. Throws std::invalid_argument (a 400) when the request cannot run it (text longer than the probe's
// capacity, LaciSession's own checks).
void attach_probe(const ServerState & s, ReqJob & job);
}  // namespace zs
