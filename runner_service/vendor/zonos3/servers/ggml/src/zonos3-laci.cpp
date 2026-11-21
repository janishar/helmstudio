// Exact port of model/src/zonos3/tts/laci.py (see zonos3-laci.h). Floating-point expressions keep Python's operand
// order so every decision matches bit for bit.
#include "zonos3-laci.h"
#include "zonos3-span-follow.h"

#include "json.hpp"

#include <algorithm>
#include <cmath>
#include <fstream>
#include <sstream>
#include <stdexcept>

namespace zonos3_laci {

using nlohmann::json;

namespace {

// statistics.median: the middle value, or the mean of the two middle values
template <typename T>
double median(std::vector<T> v) {
    std::sort(v.begin(), v.end());
    const size_t n = v.size(), i = n / 2;
    if (n % 2 == 1) return (double)v[i];
    return ((double)v[i - 1] + (double)v[i]) / 2;
}

std::string py_float(double x) { return json(x).dump(); }  // shortest round-trip repr, like Python's

FailureRecord failure_record(const Failure & f, std::optional<int64_t> retry_seed, bool ignored,
                             std::optional<int> resume_row = std::nullopt,
                             std::optional<int> retry_bucket = std::nullopt) {
    // rollback_row is where the regeneration resumed: the failure's own rollback row, or the first unsent row when
    // that lies above it (clamped) -- laci.py _failure_record
    FailureRecord r;
    r.kind = f.kind;
    r.detect_row = f.detect_row;
    r.onset_row = f.onset_row;
    r.rollback_row = resume_row ? *resume_row : f.rollback_row;
    r.clamped = r.rollback_row != f.rollback_row;
    r.bin = f.bin;
    r.frontier = f.frontier;
    r.retry_seed = retry_seed;
    r.ignored = ignored;
    r.retry_bucket = retry_bucket;
    return r;
}

}  // namespace

// ------------------------------------------------------------------------------------------------------- Detector

Detector::Detector(int text_len_, double frames_per_token_prior, const Config & cfg_, int min_rollback_row_)
    : text_len(text_len_), prior(frames_per_token_prior), cfg(cfg_), min_rollback_row(min_rollback_row_) {
    if (text_len < 1) throw std::invalid_argument("text_len must be >= 1, got " + std::to_string(text_len));
    nb = (text_len + cfg.bin_tokens - 1) / cfg.bin_tokens;
    reset();
}

void Detector::reset() {
    counts_.assign(nb, 0);
    first_row_.assign(nb, std::nullopt);
    frontier_ = 0;
    last_advance_row_.reset();
    done_row_.reset();
    read_row_.reset();
}

int Detector::tokens(int b) const { return std::min(cfg.bin_tokens, text_len - b * cfg.bin_tokens); }

double Detector::pace_tok() const {
    const int lo = std::max(0, frontier_ - cfg.pace_window_bins);
    std::vector<double> rates;
    for (int x = lo; x < frontier_; x++) rates.push_back((double)counts_[x] / tokens(x));
    if ((int)rates.size() < cfg.warmup_bins) return prior;
    return median(std::move(rates));
}

double Detector::share(int b) const { return pace_tok() * tokens(b); }

int Detector::last_pos_before(int row) const {
    const size_t i = std::lower_bound(obs_rows_.begin(), obs_rows_.end(), row) - obs_rows_.begin();
    return i ? obs_pos_[i - 1] : 0;
}

std::optional<int> Detector::stop_row() const {
    if (!done_row_) return std::nullopt;
    return *done_row_ + cfg.tail_frames;
}

int Detector::commit_row(int next_row) const {
    // Hallucination onsets are the stall clock (never decreasing); skip onsets are the first row of a bin >=
    // frontier + 2 (seen already, or a future row). Once the text is done neither can occur. -- laci.py commit_row
    if (done_row_) return COMMIT_UNBOUNDED;
    int onset = std::min(last_advance_row_ ? *last_advance_row_ : next_row, next_row);
    for (int b = frontier_ + 2; b < nb; b++)
        if (first_row_[b]) onset = std::min(onset, *first_row_[b]);
    return rollback(onset);
}

int Detector::rollback(int onset) const { return std::max(onset - cfg.rollback_margin_frames, min_rollback_row); }

std::optional<Failure> Detector::observe(int row, int pos) {
    if (!(0 <= pos && pos < text_len))
        throw std::invalid_argument("pos " + std::to_string(pos) + " outside text span [0, " + std::to_string(text_len) + ")");
    if (!obs_rows_.empty() && row <= obs_rows_.back())
        throw std::invalid_argument("rows must increase: got " + std::to_string(row) + " after " +
                                    std::to_string(obs_rows_.back()));
    obs_rows_.push_back(row);
    obs_pos_.push_back(pos);
    return step(row, pos);
}

std::optional<Failure> Detector::step(int row, int pos) {
    // 1. count
    const int b = pos / cfg.bin_tokens;
    counts_[b] += 1;
    if (!first_row_[b]) first_row_[b] = row;
    if (!last_advance_row_) last_advance_row_ = row;

    // 2. frontier / skip
    if (b > frontier_ && counts_[b] >= cfg.cover_frac * share(b)) {
        for (int x = frontier_ + 1; x < b; x++) {
            if (counts_[x] < cfg.skip_frac * share(x)) {
                std::optional<int> onset;
                for (int y = x + 1; y < nb; y++)
                    if (first_row_[y] && (!onset || *first_row_[y] < *onset)) onset = first_row_[y];
                Failure f;
                f.kind = "skip";
                f.detect_row = row;
                f.onset_row = *onset;  // bin b >= x + 1 was just seen
                f.rollback_row = rollback(*onset);
                f.bin = x;
                f.frontier = b;
                return f;
            }
        }
        frontier_ = b;
        last_advance_row_ = row;
    }

    // 3. stall
    if (!done_row_) {
        const double limit = std::max(cfg.stall_factor * pace_tok() * cfg.bin_tokens, (double)cfg.min_stall_frames);
        if (row - *last_advance_row_ > limit) {
            const int onset = *last_advance_row_;
            Failure f;
            f.kind = "hallucination";
            f.detect_row = row;
            f.onset_row = onset;
            f.rollback_row = rollback(onset);
            f.bin = frontier_ + 1;
            f.frontier = frontier_;
            return f;
        }
    }

    // 4. text done: the last bin read for its share, and the reading has reached the last text token (with two bins the
    // share alone, a pace prior, can call the text read a second before the end is spoken)
    if (!read_row_ && pos >= text_len - 1) read_row_ = row;
    if (!done_row_ && read_row_ && frontier_ >= nb - 2 && counts_[nb - 1] >= share(nb - 1)) done_row_ = row;
    return std::nullopt;
}

std::optional<Failure> Detector::on_eos(int row) const {
    if (frontier_ < nb - 2) {
        Failure f;
        f.kind = "truncation";
        f.detect_row = row;
        f.onset_row = row;
        f.rollback_row = rollback(row);
        f.bin = frontier_ + 1;
        f.frontier = frontier_;
        return f;
    }
    return std::nullopt;
}

void Detector::truncate(int row) {
    // Replays the kept observations; earlier truncate() bumps of the stall clock that lie inside them are re-applied
    // at the same point, and the survivors' stall clock moves up to `row` (laci.py Detector.truncate).
    const size_t keep = std::lower_bound(obs_rows_.begin(), obs_rows_.end(), row) - obs_rows_.begin();
    std::vector<int> rows(obs_rows_.begin(), obs_rows_.begin() + keep), poss(obs_pos_.begin(), obs_pos_.begin() + keep);
    std::vector<int> bumps;
    for (int r : bumps_)
        if (r < row) bumps.push_back(r);
    obs_rows_.clear();
    obs_pos_.clear();
    reset();
    size_t pending = 0;
    for (size_t k = 0; k < rows.size(); k++) {
        const int r = rows[k];
        while (pending < bumps.size() && r > bumps[pending]) {
            if (last_advance_row_) last_advance_row_ = std::max(*last_advance_row_, bumps[pending]);
            pending++;
        }
        obs_rows_.push_back(r);
        obs_pos_.push_back(poss[k]);
        if (step(r, poss[k])) throw std::logic_error("LACI detector: replay raised a failure");
    }
    if (last_advance_row_) last_advance_row_ = std::max(*last_advance_row_, row);
    bumps.push_back(row);
    bumps_ = std::move(bumps);
}

// ------------------------------------------------------------------------------------------------------ AciCenter

AciCenter::AciCenter(int c0_, int text_len_) : text_len(text_len_) {
    c0 = std::min(std::max(c0_, 0), text_len - 1);
    d_ = {0.0};
    center = c0;
}

int AciCenter::update(double m) {
    auto & d = d_;
    if (c0 + (int)d.size() < text_len) d.push_back(INFINITY);
    for (int i = (int)d.size() - 1; i > 0; i--) {
        const double e = m - (double)(c0 + i);
        d[i] = std::min(d[i], d[i - 1]) + e * e;
    }
    const double e0 = m - (double)c0;
    d[0] += e0 * e0;
    int best = 0;
    for (int i = 1; i < (int)d.size(); i++)
        if (d[i] < d[best]) best = i;
    center = std::min(std::max(c0 + best, center), center + 1);
    return center;
}

// ------------------------------------------------------------------------------------------------------ LaciHeads

LaciHeads LaciHeads::parse(const std::string & json_text, const std::string & path) {
    const json spec = json::parse(json_text);
    const auto it = spec.find("heads");
    // `not spec.get("heads")`: missing, null or empty
    if (it == spec.end() || it->is_null() || (it->is_array() && it->empty()) || (it->is_boolean() && !it->get<bool>()))
        throw std::runtime_error(path + ": no alignment heads");
    LaciHeads h;
    for (const auto & e : *it) {
        const Head hd{e.at("layer").get<int>(), e.at("head").get<int>()};
        h.heads.push_back(hd);
        const int r = e.at("radius").get<int>();
        auto found = std::find_if(h.radius.begin(), h.radius.end(), [&](const auto & p) { return p.first == hd; });
        if (found == h.radius.end()) h.radius.emplace_back(hd, r);
        else found->second = r;  // dict comprehension: the last value at the first position
    }
    h.frames_per_token = spec.at("frames_per_token").get<double>();
    return h;
}

LaciHeads LaciHeads::load(const std::string & path) {
    std::ifstream f(path, std::ios::binary);
    if (!f) throw std::runtime_error(path + ": cannot read the LACI heads file");
    std::stringstream ss;
    ss << f.rdbuf();
    return parse(ss.str(), path);
}

// ---------------------------------------------------------------------------------------------------- RateBuckets

double RateBuckets::rate(int bucket) const {
    const auto & e = edges;
    const int i = bucket - first;
    if (i <= 0) return e[0] - (e[1] - e[0]) / 2;
    if (i >= (int)e.size()) return e.back() + (e.back() - e[e.size() - 2]) / 2;
    return (e[i - 1] + e[i]) / 2;
}

std::optional<int> RateBuckets::retry_bucket(int bucket, double ratio) const {
    int want = first + (int)(std::upper_bound(edges.begin(), edges.end(), rate(bucket) * ratio) - edges.begin());
    want = ratio > 1 ? std::max(want, bucket + 1) : std::min(want, bucket - 1);
    if (first <= want && want <= last()) return want;
    return std::nullopt;
}

// ---------------------------------------------------------------------------------------------------- PaceMonitor

PaceMonitor::PaceMonitor(double target_frames, int origin_row, int min_rollback_row_, const Config & cfg_)
    : target(target_frames), origin(origin_row), min_rollback_row(min_rollback_row_), cfg(cfg_) {
    if (!(target_frames > 0)) throw std::invalid_argument("target_frames must be > 0, got " + py_float(target_frames));
    margin = PACE_TOLERANCE * target + PACE_SLACK_FRAMES;
}

double PaceMonitor::stall_frames(int text_len) const {
    const double per_token = target / std::max(1, text_len);
    return std::max(cfg.stall_factor * cfg.bin_tokens * per_token, (double)DURATION_MIN_STALL_FRAMES);
}

Failure PaceMonitor::pace_failure(const char * kind, int row, int frontier, double ratio) const {
    Failure f;
    f.kind = kind;
    f.detect_row = row;
    f.onset_row = min_rollback_row;
    f.rollback_row = min_rollback_row;
    f.bin = frontier;
    f.frontier = frontier;
    f.pace = true;
    f.ratio = std::min(PACE_RATIO_HI, std::max(PACE_RATIO_LO, ratio));
    return f;
}

std::optional<Failure> PaceMonitor::observe(int row, int pos, int text_len, bool text_done, int frontier) {
    if (!rows_.empty() && row <= rows_.back())
        throw std::invalid_argument("rows must increase: got " + std::to_string(row) + " after " +
                                    std::to_string(rows_.back()));
    rows_.push_back(row);
    pos_.push_back(pos);
    if (read_row) return std::nullopt;  // the tail
    const size_t n = pos_.size(), lo = n > (size_t)PACE_SMOOTH_STEPS ? n - PACE_SMOOTH_STEPS : 0;
    const double smooth = median(std::vector<int>(pos_.begin() + lo, pos_.end()));
    if (!best_ || smooth > *best_) {
        best_ = smooth;
        best_row_ = row;
    }
    const int last = text_len - 2;
    const int elapsed = row - origin;
    if (smooth >= last || text_done) {
        read_row = row;
        if (elapsed < target - margin) return pace_failure("pace_fast", row, frontier, elapsed / target);
        return std::nullopt;
    }
    if (row - *best_row_ > stall_frames(text_len)) {
        const int onset = *best_row_;
        Failure f;
        f.kind = "hallucination";
        f.detect_row = row;
        f.onset_row = onset;
        f.rollback_row = std::max(onset - cfg.rollback_margin_frames, min_rollback_row);
        f.bin = frontier + 1;
        f.frontier = frontier;
        return f;
    }
    if (elapsed > target + margin) {
        const double share = (smooth - pos_[0]) / std::max(1, last - pos_[0]);  // of the text, read so far
        const double lower = elapsed / target;                                  // the take is at least this long
        const double ratio = share > 0 ? lower / share : PACE_RATIO_HI;
        return pace_failure("pace_slow", row, frontier, std::max(ratio, lower));
    }
    return std::nullopt;
}

void PaceMonitor::truncate(int row) {
    const size_t keep = std::lower_bound(rows_.begin(), rows_.end(), row) - rows_.begin();
    rows_.resize(keep);
    pos_.resize(keep);
    if (read_row && *read_row >= row) read_row.reset();
    best_.reset();
    best_row_.reset();
    for (size_t k = 0; k < rows_.size(); k++) {
        const size_t lo = k + 1 > (size_t)PACE_SMOOTH_STEPS ? k + 1 - PACE_SMOOTH_STEPS : 0;
        const double smooth = median(std::vector<int>(pos_.begin() + lo, pos_.begin() + k + 1));
        if (!best_ || smooth > *best_) {
            best_ = smooth;
            best_row_ = rows_[k];
        }
    }
    if (best_row_) best_row_ = std::max(*best_row_, row);  // the regeneration gets a full stall budget
}

// --------------------------------------------------------------------------------------------------- LaciSession

LaciSession::LaciSession(SessionParams params) : p_(std::move(params)) {
    n_prefill_ = (int)p_.prefill_rows.size();
    stream_ = p_.prefill_rows;
    attempt_start_ = n_prefill_;
    condition_values_ = p_.condition_values;
    bucket_ = p_.bucket;
    if (p_.duration) {
        pace_.emplace(p_.duration->frames, n_prefill_, n_prefill_, p_.cfg);
        if (p_.duration->adjust_rate && (!p_.rates || !p_.condition_values))
            throw std::invalid_argument("a duration target with adjust_rate needs the checkpoint's speaking-rate condition");
    }
    if (!bucket_ && p_.rates && condition_values_) bucket_ = (*condition_values_)[p_.rates->index];
    if (p_.rate_deltas) {
        const int want = p_.rates ? p_.rates->last() - p_.rates->first + 1 : 0;
        if ((int)p_.rate_deltas->size() != want)
            throw std::invalid_argument("rate_deltas: " + std::to_string(p_.rate_deltas->size()) + " for " +
                                        std::to_string(want) + " buckets");
    }
    if (p_.short_text) short_.emplace(*p_.short_text, p_.n_codebooks);
}

std::optional<int> LaciSession::rate_bucket() const {
    if (!p_.rates || !condition_values_) return std::nullopt;
    return bucket_;
}

std::optional<Windows> LaciSession::window(int center) const {
    Windows w;
    w.reserve(p_.heads.radius.size());
    for (const auto & [h, r] : p_.heads.radius) w.push_back({h, center - r + 1, center + r - 1});
    return w;
}

std::optional<Windows> LaciSession::probe_mask() const {
    if (!probation_) return std::nullopt;
    return window(probation_->second.center);
}

double LaciSession::stop_boost() const {
    if (!detector_ || !detector_->done_row() ||
        (int)stream_.size() < *detector_->done_row() + p_.cfg.stop_boost_delay)
        return 0.0;
    return p_.cfg.stop_boost;
}

void LaciSession::on_step(ProbeControl & probe, int step, int pos, double m) {
    const int row = attempt_start_ + step;
    if (p_.follower) p_.follower->observe(row - n_prefill_, pos);
    if (!detector_) {
        text_len_ = probe.text_len;
        detector_.emplace(probe.text_len, p_.heads.frames_per_token, p_.cfg, n_prefill_);
    }
    Detector & det = *detector_;
    if (probation_) {
        auto & [failure, aci] = *probation_;
        probe.mask = window(aci.update(m));
        if (det.frontier() > failure.frontier) {
            probation_.reset();
            probe.mask.reset();
        }
    }
    if (auto f = det.observe(row, pos)) {
        if (attempts_ < p_.cfg.max_retries) {
            pending_failure_ = *f;
            probe.stop = true;
            return;
        }
        ignore(*f);
    }
    if (short_) {
        // the cut, decided over the rows so far and this reading (one row late when a row decided it)
        short_->observe_reading(row - n_prefill_, pos);
        if (short_->decided()) {
            probe.stop = true;
            return;
        }
    }
    const auto stop_row = det.stop_row();
    // keep the rows the delayed codebooks of the last kept frame need (stop_row + n_codebooks - 1), with a margin
    if (stop_row && row >= *stop_row + p_.n_codebooks + 2) {
        text_done_ = true;
        probe.stop = true;
    }
    if (pace_) pace_step(probe, row, pos);
}

bool LaciSession::rate_adjustable() const { return p_.duration && p_.duration->adjust_rate; }

void LaciSession::pace_step(ProbeControl & probe, int row, int pos) {
    const Detector & det = *detector_;
    const auto f = pace_->observe(row, pos, probe.text_len, det.done_row().has_value(), det.frontier());
    if (!f || probe.stop) return;  // in time, or the attempt already ends (text read)
    if (!f->pace) {                // a stall: a content failure like the detector's
        if (attempts_ < p_.cfg.max_retries) {
            pending_failure_ = *f;
            probe.stop = true;
        } else {
            ignore(*f);
        }
    } else if (pace_retry_bucket(*f)) {
        pending_failure_ = *f;
        probe.stop = true;
    } else if (!pace_->ignored.count(f->kind)) {
        pace_->ignored.insert(f->kind);
        failures_.push_back(failure_record(*f, std::nullopt, true));
    }
}

std::optional<int> LaciSession::pace_retry_bucket(const Failure & f) const {
    if (!p_.duration->adjust_rate || pace_retries_ >= PACE_MAX_RETRIES) return std::nullopt;
    if (attempts_ >= p_.cfg.max_retries) return std::nullopt;
    return p_.rates->retry_bucket(*rate_bucket(), f.ratio);
}

bool LaciSession::pace_open() const {
    return pace_ && p_.duration->adjust_rate && !pace_->read_row && pace_retries_ < PACE_MAX_RETRIES &&
           attempts_ < p_.cfg.max_retries;
}

void LaciSession::ignore(const Failure & f) {
    if (ignored_.insert({f.kind, f.bin, f.onset_row}).second) failures_.push_back(failure_record(f, std::nullopt, true));
}

void LaciSession::append(std::vector<int32_t> row, std::optional<int> eos_frame) {
    stream_.push_back(std::move(row));
    generated_total_ += 1;
    if (eos_frame) eos_row_ = attempt_start_ + *eos_frame;
    if (short_) short_->observe_row(stream_.back());
}

int LaciSession::commit_row() const {
    const int n = (int)stream_.size();
    if (end_) return n;
    if (p_.hold || short_) return n_prefill_;  // a short text is held until it is cut
    int row;
    if (attempts_ >= p_.cfg.max_retries) {
        row = n;
    } else if (pace_open()) {
        row = n_prefill_;
    } else {
        // truncation: EOS at a stream row >= the EOS-bearing row - (n_codebooks - 1); not yet seen -> row >= n
        const int eos = eos_row_ ? *eos_row_ : n - (p_.n_codebooks - 1);
        row = std::max(eos - p_.cfg.rollback_margin_frames, n_prefill_);
        if (detector_) row = std::min(row, detector_->commit_row(n));
        else row = n_prefill_;
    }
    return std::max(n_prefill_, std::min(row, n - 1));
}

RowRange LaciSession::drain(bool final) {
    const int upto = final ? (int)stream_.size() : commit_row();
    const int start = n_prefill_ + emitted_;
    const int end = std::max(start, std::min(upto, (int)stream_.size()));
    emitted_ += end - start;
    return {start, end};
}

std::optional<Continuation> LaciSession::finish_attempt() {
    std::optional<Failure> f = pending_failure_;
    // a short text's filler is there to be cut: one that ends before it keeps that end (no truncation to retry)
    if (!f && eos_row_ && detector_ && !short_) {
        f = detector_->on_eos(*eos_row_);
        if (f && attempts_ >= p_.cfg.max_retries) {
            ignore(*f);
            f.reset();
        }
    }
    if (!f && short_) f = short_run_on();
    // Rows already handed downstream are final: a rollback below them restarts at the first unsent row.
    int resume = 0;
    if (f) resume = std::max(f->rollback_row, n_prefill_ + emitted_);
    if (f && p_.max_tokens - (resume - n_prefill_) <= 0) {
        ignore(*f);  // no budget left to regenerate from the rollback row: keep what was generated
        f.reset();
    }
    if (!f) {
        if (short_ && short_->enter()) end_.emplace(short_end());
        else if (text_done_) end_.emplace("text_done", detector_->stop_row());
        else if (eos_row_) end_.emplace("eos", eos_row_);
        else end_.emplace("max_tokens", std::nullopt);
        return std::nullopt;
    }
    // the bucket a regeneration switches to, decided before the attempt counts
    std::optional<int> bucket;
    if (f->pace) bucket = pace_retry_bucket(*f);
    else if (f->kind == "truncation" && rate_adjustable() && *rate_bucket() > p_.rates->first)
        bucket = *rate_bucket() - 1;  // ended too early: one bucket slower
    attempts_ += 1;
    std::optional<int64_t> seed;
    if (p_.seed) seed = *p_.seed + 1000 * (int64_t)attempts_;
    failures_.push_back(failure_record(*f, seed, false, resume, bucket));
    stream_.resize(resume);
    detector_->truncate(resume);
    if (p_.follower) p_.follower->truncate(resume - n_prefill_);
    if (short_) short_->truncate(resume - n_prefill_);
    if (pace_) pace_->truncate(resume);
    if (bucket) {
        // a steered bucket's row is unknown: its pace comes from its steering delta (rate_deltas)
        bucket_ = bucket;
        (*condition_values_)[p_.rates->index] = p_.rates->row(*bucket);
    }
    if (f->pace || f->kind == "short_run_on") {
        // a fresh take (rollback_row is the first row): in the bucket the measured duration asks for, or the retake
        // of a short text that ran into its filler
        if (f->pace) pace_retries_ += 1;
        else short_retries_ += 1;
        probation_.reset();
    } else {
        probation_.emplace(*f, AciCenter(detector_->last_pos_before(resume), text_len_));
    }
    pending_failure_.reset();
    text_done_ = false;
    eos_row_.reset();
    attempt_start_ = resume;

    Continuation c;
    c.n_rows = resume;
    c.history_rows = resume - n_prefill_;
    c.seed = seed;
    c.max_tokens = p_.max_tokens - (resume - n_prefill_);
    c.condition_values = condition_values_;
    c.swap_delta = p_.rate_deltas.has_value();
    if (p_.rate_deltas && bucket_) {
        const auto & d = (*p_.rate_deltas)[*bucket_ - p_.rates->first];
        if (!d.empty()) c.steering_delta = d;
    }
    return c;
}

std::optional<Failure> LaciSession::short_run_on() {
    const auto & decided = short_->decided();
    if (!decided || decided->first != "run_on") return std::nullopt;
    Failure f;
    f.kind = "short_run_on";
    f.detect_row = (int) stream_.size() - 1;
    f.onset_row = n_prefill_ + decided->second;
    f.rollback_row = n_prefill_;
    f.bin = f.frontier = detector_->frontier();
    if (short_retries_ < short_->max_retries() && attempts_ < p_.cfg.max_retries) return f;
    ignore(f);
    return std::nullopt;
}

std::pair<std::string, std::optional<int>> LaciSession::short_end() const {
    const auto & decided = short_->decided();
    if (decided && decided->first == "cut") return {"short_text", n_prefill_ + decided->second};
    int frames = std::min(*short_->enter(), (int) stream_.size() - n_prefill_ - (p_.n_codebooks - 1));
    if (eos_row_) frames = std::min(frames, *eos_row_ - n_prefill_);
    return {"short_text_forced", n_prefill_ + std::max(frames, 0)};
}

std::optional<int> LaciSession::eos_frame() const {
    if (!end_ || !end_->second) return std::nullopt;
    return *end_->second - n_prefill_;
}

Record LaciSession::record() const {
    if (!end_) throw std::logic_error("LaciSession::record before the result is final");
    const Detector * det = detector();
    Record r;
    r.attempts = attempts_;
    r.capped = attempts_ >= p_.cfg.max_retries;
    r.end = end_->first;
    r.text_len = text_len_;
    r.n_bins = det ? det->nb : 0;
    r.final_rows = (end_->second ? *end_->second : (int)stream_.size()) - n_prefill_;
    r.final_eos_row = end_->second;
    r.generated_total = generated_total_;
    if (det) r.pace_tok = det->pace_tok();
    r.heads = p_.heads.heads;
    r.failures = failures_;
    r.pace_retries = pace_retries_;
    r.rate_bucket = rate_bucket();
    return r;
}

std::string Record::to_json() const {
    auto opt = [](const auto & o) { return o ? json(*o) : json(nullptr); };
    json fs = json::array();
    for (const auto & f : failures)
        fs.push_back({{"kind", f.kind}, {"detect_row", f.detect_row}, {"onset_row", f.onset_row},
                      {"rollback_row", f.rollback_row}, {"clamped", f.clamped}, {"bin", f.bin},
                      {"frontier", f.frontier}, {"retry_seed", opt(f.retry_seed)}, {"ignored", f.ignored},
                      {"retry_bucket", opt(f.retry_bucket)}});
    json hs = json::array();
    for (const auto & h : heads) hs.push_back({h.layer, h.head});
    const json j = {{"attempts", attempts}, {"capped", capped}, {"end", end}, {"text_len", text_len},
                    {"n_bins", n_bins}, {"final_rows", final_rows}, {"final_eos_row", opt(final_eos_row)},
                    {"generated_total", generated_total}, {"pace_tok", opt(pace_tok)}, {"heads", hs},
                    {"failures", fs}, {"pace_retries", pace_retries}, {"rate_bucket", opt(rate_bucket)}};
    return j.dump();
}

}  // namespace zonos3_laci
