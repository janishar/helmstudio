// LACI -- Localized Attention-Constrained Inference (arXiv 2609.16989): exact port of model/src/zonos3/tts/laci.py.
//
// While a request decodes, the alignment heads' attention over the text is reduced to a reading position per step
// (argmax `pos`, centroid `m`) and fed to a Detector. A skip, a hallucination (stall) or a truncation (EOS with two or
// more bins unread) rolls the request back to the failure onset; it is regenerated from there with a new seed under an
// ACI hard attention mask (arXiv 2404.19723) until the reading passes the detection point, at most max_retries times.
// The text is done once the last bin has been read for its share and the reading has reached the last text token;
// generation stops tail_frames later, and stop_boost_delay rows after the text is done the sampler raises the stop
// id's logit by stop_boost (LaciSession::stop_boost). A DurationTarget adds a PaceMonitor that holds the take to a
// length (pace_fast / pace_slow retakes in another speaking-rate bucket).
//
// Driving a request (as the sgl scheduler and model/tests/test_laci.py `run_attempt` do), per attempt:
//   probe_mask()                    the probe windows of the attempt's prefill (nullopt: no mask)
//   on_step(probe, step, pos, m)    after every decode step (step 1 = the first decode step; the prefill-sampled row
//                                   is never observed); it may set probe.mask (windows for later steps) and probe.stop
//   append(row, eos_frame)          for every row the request emits, then drain() for the rows that are final
//   finish_attempt()                when the request finishes: a Continuation to regenerate, or nullopt when final
//                                   (then drain(true), eos_frame(), record()).
// Rows are stream indices: the prefill rows are 0..P-1, generated rows follow.
//
// A short text (zonos3-short-text.h; SessionParams::short_text, the filler's first text token) holds the take, ends it
// at the cut once the reading reaches the filler (end "short_text"), regenerates a take that ran into the filler as a
// fresh one (failure "short_run_on") and, once those retries are spent, cuts where the reading reached the filler
// ("short_text_forced").
#pragma once

#include "zonos3-short-text.h"

#include <cstdint>
#include <limits>
#include <optional>
#include <set>
#include <string>
#include <tuple>
#include <utility>
#include <vector>

namespace zonos3_follow { class SpanFollower; }

namespace zonos3_laci {

struct Config {
    int bin_tokens = 8;
    double cover_frac = 0.5;
    double skip_frac = 0.15;
    double stall_factor = 3.0;
    int min_stall_frames = 45;
    int tail_frames = 15;
    int rollback_margin_frames = 8;
    int max_retries = 8;
    int pace_window_bins = 16;
    int warmup_bins = 2;
    // Once the text is done and stop_boost_delay more rows were sampled, the sampler adds stop_boost to the stop id's
    // logit in every codebook before the sampling filters (laci.py LaciConfig); 0 turns it off.
    double stop_boost = 3.0;
    int stop_boost_delay = 6;
};

// A content failure ("skip" | "hallucination" | "truncation" | "short_run_on") or, with `pace`, a duration miss
// ("pace_slow" | "pace_fast", Python's PaceFailure) carrying `ratio` (estimated take duration / target duration).
struct Failure {
    std::string kind;
    int detect_row = 0;
    int onset_row = 0;
    int rollback_row = 0;
    int bin = 0;       // skip: first skipped bin; hallucination/truncation: first unread bin
    int frontier = 0;  // skip: the bin the reading jumped to; otherwise the frontier
    bool pace = false;
    double ratio = 0.0;
};

constexpr int COMMIT_UNBOUNDED = std::numeric_limits<int>::max();  // Python's math.inf from Detector.commit_row

// Streaming skip / hallucination / truncation detector. Feed one (row, pos) per decode step with strictly increasing
// rows; after a failure the caller rolls back with truncate(rollback_row) and re-observes from that row.
class Detector {
public:
    Detector(int text_len, double frames_per_token_prior, const Config & cfg, int min_rollback_row);

    int text_len = 0;
    double prior = 0.0;
    Config cfg;
    int min_rollback_row = 0;
    int nb = 0;

    int frontier() const { return frontier_; }
    const std::vector<int> & counts() const { return counts_; }
    std::optional<int> last_advance_row() const { return last_advance_row_; }
    std::optional<int> done_row() const { return done_row_; }

    double pace_tok() const;                   // median frames/token of the last pace_window_bins bins below frontier
    int last_pos_before(int row) const;        // last observed pos at a row < `row`; 0 if there is none
    std::optional<int> stop_row() const;       // done_row + tail_frames
    int commit_row(int next_row) const;        // COMMIT_UNBOUNDED once the text is done
    std::optional<Failure> observe(int row, int pos);  // throws std::invalid_argument like Python's ValueError
    std::optional<Failure> on_eos(int row) const;      // truncation iff two or more bins are unread
    void truncate(int row);

private:
    void reset();
    int tokens(int b) const;
    double share(int b) const;
    int rollback(int onset) const;
    std::optional<Failure> step(int row, int pos);

    std::vector<int> obs_rows_, obs_pos_, bumps_;
    std::vector<int> counts_;
    std::vector<std::optional<int>> first_row_;
    int frontier_ = 0;
    std::optional<int> last_advance_row_, done_row_;
    std::optional<int> read_row_;  // first row the reading was on the last text token (EOS)
};

// ACI dynamic-programming attention centre (2404.19723, Eq. 2/5), held monotone and limited to +1 per update.
class AciCenter {
public:
    AciCenter(int c0, int text_len);
    int update(double m);

    int text_len = 0;
    int c0 = 0;
    int center = 0;

private:
    std::vector<double> d_;  // d_[i] is the cost of index c0 + i
};

struct Head {
    int layer = 0, head = 0;
    bool operator==(const Head & o) const { return layer == o.layer && head == o.head; }
};

// Alignment heads of one model and language (the JSON files in assets/laci_heads/).
struct LaciHeads {
    std::vector<Head> heads;                     // spec order (Python's `heads` tuple)
    std::vector<std::pair<Head, int>> radius;    // ACI mask half-width per head, dict order (first insertion)
    double frames_per_token = 0.0;               // pace prior until warmup_bins bins are read

    // Same JSON and the same error messages as LaciHeads.load; throw std::runtime_error.
    static LaciHeads load(const std::string & path);
    static LaciHeads parse(const std::string & json_text, const std::string & path);
};

// One text window per probe head: keys in [lo, hi] (inclusive) of the text span stay visible.
struct HeadWindow { Head head; int lo = 0, hi = 0; };
using Windows = std::vector<HeadWindow>;

// ------------------------------------------------------------------------------------------------ duration targets
constexpr double PACE_TOLERANCE = 0.10;
constexpr int PACE_SLACK_FRAMES = 3;
constexpr int PACE_SMOOTH_STEPS = 5;
constexpr int PACE_MAX_RETRIES = 2;
constexpr double PACE_RATIO_LO = 0.5, PACE_RATIO_HI = 2.0;  // PACE_RATIO_RANGE
constexpr int DURATION_MIN_STALL_FRAMES = 15;

struct DurationTarget {
    double frames = 0.0;      // target length of the take, in generated rows
    bool adjust_rate = true;  // on a miss, regenerate in the speaking-rate bucket the measured ratio asks for
};

// A speaking-rate scale: condition-row `index`, `n_buckets` row buckets (id n_buckets = unknown) and the scale's
// bucket edges (UTF-8 bytes / s); bucket i in [first, last] is [edges[i - first - 1], edges[i - first]).
struct RateBuckets {
    int index = 0;
    std::vector<double> edges;
    int n_buckets = 0;
    int first = 0;

    int last() const { return first + (int)edges.size(); }
    int row(int bucket) const { return 0 <= bucket && bucket < n_buckets ? bucket : n_buckets; }
    double rate(int bucket) const;
    std::optional<int> retry_bucket(int bucket, double ratio) const;
};

// Duration check over the reading position (see laci.py PaceMonitor).
class PaceMonitor {
public:
    PaceMonitor(double target_frames, int origin_row, int min_rollback_row, const Config & cfg = Config());

    double target = 0.0;
    int origin = 0;
    int min_rollback_row = 0;
    double margin = 0.0;
    Config cfg;
    std::set<std::string> ignored;  // kinds already recorded as ignored
    std::optional<int> read_row;    // row at which the text counted as read

    double stall_frames(int text_len) const;
    std::optional<Failure> observe(int row, int pos, int text_len, bool text_done = false, int frontier = 0);
    void truncate(int row);

private:
    Failure pace_failure(const char * kind, int row, int frontier, double ratio) const;

    std::vector<int> rows_, pos_;
    std::optional<double> best_;
    std::optional<int> best_row_;
};

// ---------------------------------------------------------------------------------------------------------- session

// The probe of the current attempt as on_step sees it (Python's probe object).
struct ProbeControl {
    int text_len = 0;
    std::optional<Windows> mask;  // windows of the masked heads for the following steps; nullopt: none
    bool stop = false;            // finish the attempt's request now
};

// A rolled-back request's regeneration: the original prompt (without its prefill rows) plus the stream rows
// [0, n_rows) -- after finish_attempt the session's stream() is exactly those rows.
struct Continuation {
    int n_rows = 0;
    int history_rows = 0;                 // rows past the prefill rows (repetition-penalty history)
    std::optional<int64_t> seed;          // seed + 1000 * attempt, unreduced (the sampler takes it mod 2^32)
    int max_tokens = 0;
    std::optional<std::vector<int>> condition_values;  // condition-row ids to regenerate under (nullopt: the prompt's)
    bool swap_delta = false;                           // regenerate under steering_delta (rate_deltas given)
    std::optional<std::vector<float>> steering_delta;  // the bucket's steering delta; only with swap_delta
};

struct FailureRecord {
    std::string kind;
    int detect_row = 0, onset_row = 0, rollback_row = 0;
    bool clamped = false;
    int bin = 0, frontier = 0;
    std::optional<int64_t> retry_seed;
    bool ignored = false;
    std::optional<int> retry_bucket;
};

struct Record {
    int attempts = 0;
    bool capped = false;
    std::string end;  // "text_done" | "eos" | "max_tokens" | "short_text" | "short_text_forced"
    int text_len = 0;
    int n_bins = 0;
    int final_rows = 0;
    std::optional<int> final_eos_row;
    int generated_total = 0;
    std::optional<double> pace_tok;
    std::vector<Head> heads;
    std::vector<FailureRecord> failures;
    int pace_retries = 0;
    std::optional<int> rate_bucket;

    std::string to_json() const;  // the dict of LaciSession.record() as a JSON object
};

struct SessionParams {
    LaciHeads heads;
    Config cfg;
    std::vector<std::vector<int32_t>> prefill_rows;  // [P][frame width]: rows that open the stream; may be empty
    std::optional<int64_t> seed;
    int max_tokens = 0;
    int n_codebooks = 0;
    // a whole decode: nothing goes downstream before the result is final, so no rollback is ever clamped
    bool hold = false;
    std::optional<DurationTarget> duration;
    std::optional<RateBuckets> rates;
    std::optional<std::vector<int>> condition_values;  // the request's condition-row ids
    std::optional<int> bucket;                         // default: condition_values[rates.index]
    // per bucket of `rates` (rates.first..rates.last): the request's steering_delta in that bucket; an empty
    // vector is Python's None
    std::optional<std::vector<std::vector<float>>> rate_deltas;
    zonos3_follow::SpanFollower * follower = nullptr;  // not owned: sees every readout, rolled back with the stream
    std::optional<int> short_text;  // a short text: the filler's first text-span token (nullopt: an ordinary request)
};

struct RowRange { int begin = 0, end = 0; };  // stream rows [begin, end)

// One request under LACI: its stream, detector, retries and ACI probation.
class LaciSession {
public:
    explicit LaciSession(SessionParams params);  // throws std::invalid_argument like the Python __post_init__

    std::optional<Windows> window(int center) const;
    std::optional<Windows> probe_mask() const;
    // The logit the sampler adds to the stop id (every codebook) for the next row: cfg.stop_boost once the text is done
    // and cfg.stop_boost_delay rows followed, else 0 (laci.py LaciSession.stop_boost).
    double stop_boost() const;
    void on_step(ProbeControl & probe, int step, int pos, double m);
    void append(std::vector<int32_t> row, std::optional<int> eos_frame);
    int commit_row() const;
    // Generated rows that became final since the last drain (all remaining ones when `final`). Drained rows are never
    // rolled back, so the range stays valid for stream() for the session's lifetime.
    RowRange drain(bool final = false);
    std::optional<Continuation> finish_attempt();

    std::optional<int> eos_frame() const;  // the final result's EOS as an aligned frame in generated rows
    Record record() const;

    const std::vector<std::vector<int32_t>> & stream() const { return stream_; }
    int n_prefill() const { return n_prefill_; }
    std::optional<int> rate_bucket() const;
    int attempts() const { return attempts_; }
    int attempt_start() const { return attempt_start_; }
    int emitted() const { return emitted_; }
    int pace_retries() const { return pace_retries_; }
    bool text_done() const { return text_done_; }
    const Detector * detector() const { return detector_ ? &*detector_ : nullptr; }
    const std::vector<FailureRecord> & failures() const { return failures_; }
    // (kind, stream eos row) once final
    const std::optional<std::pair<std::string, std::optional<int>>> & end() const { return end_; }

private:
    bool rate_adjustable() const;
    void pace_step(ProbeControl & probe, int row, int pos);
    std::optional<int> pace_retry_bucket(const Failure & f) const;
    bool pace_open() const;
    void ignore(const Failure & f);
    std::optional<Failure> short_run_on();
    std::pair<std::string, std::optional<int>> short_end() const;

    SessionParams p_;
    int n_prefill_ = 0;
    std::vector<std::vector<int32_t>> stream_;
    int attempt_start_ = 0;
    int emitted_ = 0;
    std::optional<Detector> detector_;
    int text_len_ = 0;
    int attempts_ = 0;
    std::vector<FailureRecord> failures_;
    std::optional<std::pair<Failure, AciCenter>> probation_;  // while the ACI mask is on
    std::optional<Failure> pending_failure_;
    bool text_done_ = false;
    std::optional<int> eos_row_;
    int generated_total_ = 0;
    std::optional<std::pair<std::string, std::optional<int>>> end_;
    std::set<std::tuple<std::string, int, int>> ignored_;
    std::optional<std::vector<int>> condition_values_;
    std::optional<int> bucket_;
    std::optional<PaceMonitor> pace_;
    int pace_retries_ = 0;
    std::optional<zonos3_short::ShortCut> short_;
    int short_retries_ = 0;
};

}  // namespace zonos3_laci
