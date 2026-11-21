#include "compat.h"

#include "server/scheduler.h"

#include "server/state.h"
#include "zonos3-generation.h"

#include <algorithm>
#include <cmath>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <stdexcept>

namespace zs {

// The silence-prefill and decode-context rows are decoded (the decoder needs them as context) but their audio is
// never emitted: drop the first skip_samples samples of the job's output, then the output stage: the output gain and
// the soft limiter (zonos3_soft_limit).
static void finish_block(ReqJob & job, std::vector<float> & pcm) {
    if (job.skip_samples > 0) {
        const size_t n = std::min(pcm.size(), (size_t) job.skip_samples);
        pcm.erase(pcm.begin(), pcm.begin() + n);
        job.skip_samples -= (int64_t) n;
    }
    if (job.gain != 1.0f) for (float & v : pcm) v *= job.gain;
    zonos3_soft_limit(pcm.data(), pcm.size());
}

// Cosine fade-out over the final fade_ms milliseconds.
static void apply_fade(std::vector<float> & a, double fade_ms, int sr) {
    if (fade_ms <= 0 || a.empty()) return;
    const size_t n = std::min(a.size(), (size_t) (fade_ms * 0.001 * sr));
    for (size_t i = 0; i < n; ++i) {
        const float t = (float) i / (float) n;
        a[a.size() - n + i] *= 0.5f * (1.0f + cosf((float) M_PI * t));
    }
}

static void push_pcm(ReqJob & job, std::vector<float> && pcm) {
    if (pcm.empty()) return;
    std::lock_guard<std::mutex> lk(job.m);
    job.pcm.push_back(std::move(pcm));
    job.cv.notify_one();
}

static void job_finish(ReqJob & job, bool failed) {
    std::lock_guard<std::mutex> lk(job.m);
    job.failed = job.failed || failed;
    if (failed) job.client_dropped = true;
    job.finished = true;
    job.cv.notify_one();
}

static void zac_lane_submit(ZacLane & lane, ZacTask && t) {
    std::lock_guard<std::mutex> lk(lane.m);
    lane.q.push_back(std::move(t));
    lane.cv.notify_one();
}

// The first H delayed rows of the job's audio stream, behind its decode-context frames: the delayed matrix of
// [context frames | stream frames], whose cells left padded in the stream's first rows hold context codes. With
// fewer than ncb - 1 stream rows (an early partial-frame chunk) the matrix runs on to the context's last cell, the
// stream cells there marked missing (-1).
static std::vector<int32_t> decode_rows(const ServerState & s, const ReqJob & job, int H) {
    const int ncb = (int) s.model.hp.n_codebooks;
    const int K = job.ctx_frames;
    if (K == 0) return std::vector<int32_t>(job.codes.begin(), job.codes.begin() + (size_t) H * ncb);
    const int32_t pad = (int32_t) s.model.hp.audio_pad_id;
    const int32_t * ctx = job.gr.decode_context.data() + (job.gr.decode_context.size() / ncb - K) * ncb;
    const int R = K + std::max(H, ncb - 1);
    std::vector<int32_t> rows((size_t) R * ncb, pad);
    for (int r = 0; r < R; ++r) {
        if (r >= K + H) std::fill_n(rows.begin() + (size_t) r * ncb, ncb, -1);
        else if (r >= K) std::copy_n(job.codes.begin() + (size_t) (r - K) * ncb, ncb, rows.begin() + (size_t) r * ncb);
        for (int c = 0; c < ncb; ++c) {
            const int frame = r - c;
            if (frame >= 0 && frame < K) rows[(size_t) r * ncb + c] = ctx[(size_t) frame * ncb + c];
        }
    }
    return rows;
}

// One pool lane: decode tasks FIFO into the job channel. A job is pinned to one lane, so its blocks stay ordered.
static void zac_lane_loop(ServerState & s, ZacLane & lane) {
    const int ncb = lane.zac.n_codebooks, sr = lane.zac.sample_rate;
    // Warm this lane's pipelines before serving so the first block doesn't JIT-compile on a request's critical path.
    if (!getenv("ZONOS3_NO_WARMUP")) {
        const int H = 32;
        std::vector<int32_t> codes((size_t) H * ncb, 0);
        std::vector<float> pcm;
        zac_decode(lane.zac, codes.data(), H, ncb, /*eos=*/-1, pcm);
        zac_stream scratch;
        zac_stream_decode(lane.zac, scratch, codes.data(), H, 16, pcm);
        if (zac_partial_frames(lane.zac)) {   // the first early partial-frame chunk: residual codebooks left out
            zac_stream partial;
            zac_stream_decode(lane.zac, partial, codes.data(), 1, 1, pcm);
        }
        // The prefix-stream ramp's chunk shapes (windows 1, 2, 4, 8, 15).
        zac_stream ramp;
        for (int f : {1, 3, 7, 15, 30}) zac_stream_decode(lane.zac, ramp, codes.data(), H, f, pcm);
    }
    for (;;) {
        ZacTask t;
        {
            std::unique_lock<std::mutex> lk(lane.m);
            lane.cv.wait(lk, [&] { return !lane.q.empty() || s.stop.load(); });
            if (lane.q.empty()) { if (s.stop.load()) break; continue; }
            t = std::move(lane.q.front());
            lane.q.pop_front();
        }
        ReqJob & job = *t.job;
        bool failed;
        { std::lock_guard<std::mutex> lk(job.m); failed = job.failed; }
        bool ok = !t.fail;
        if (!t.drop && !t.fail && !failed) {
            std::vector<float> pcm;
            if (t.kind == ZacTask::WINDOW) {
                ok = zac_stream_decode(lane.zac, job.dst, t.codes.data(), t.H, t.f_hi, pcm);
                if (ok) {
                    finish_block(job, pcm);
                    push_pcm(job, std::move(pcm));
                }
            } else {
                // Continue the job's stream to the end and flush it (web runJob): no second decode.
                const int eos = job.eos_frame >= 0 ? job.eos_frame + job.ctx_frames : -1;
                const int E = std::min(eos >= 0 ? eos : t.H, t.H - ncb + 1);
                if (t.H > 0 && E <= 0) {
                    fprintf(stderr, "zac: no frames to decode (eos=%d, H=%d)\n", eos, t.H);
                    ok = false;
                } else {
                    ok = t.H <= 0 ||
                         ((E <= job.dst.n_done || zac_stream_decode(lane.zac, job.dst, t.codes.data(), t.H, E, pcm)) &&
                          zac_stream_flush(lane.zac, job.dst, pcm));
                }
                if (ok) {
                    finish_block(job, pcm);
                    apply_fade(pcm, job.gr.fade_out_ms, sr);
                    push_pcm(job, std::move(pcm));
                }
            }
        }
        if (t.kind == ZacTask::FINALIZE || !ok) job_finish(job, /*failed=*/!ok);
    }
}

int prefix_ready(const int32_t * rows, int H, int ncb, int32_t audio_pad_id, int complete, int eos_frame, int emitted,
                 int prefill, int codebooks, bool first_only) {
    if (codebooks >= ncb || (first_only && emitted > prefill)) return complete;
    int ready = H - (codebooks - 1);
    if (eos_frame >= 0) ready = std::min(ready, eos_frame);
    while (ready > std::max(complete, emitted) && rows[(size_t) (ready - 1) * ncb] >= audio_pad_id - 1) --ready;
    return ready;
}

int prefix_chunk_end(const PrefixStreamSettings & ps, int ready, int emitted, int prefill) {
    const int decodable = ready - ps.hold_back_frames;
    const int need = std::min(ps.chunk_frames, ps.first_chunk_frames + emitted - prefill);
    return decodable - emitted >= need ? decodable : -1;
}

// Prefix streaming: once `need` new frames are decodable beyond the hold-back, stream-decode up to there on the job's
// lane. `need` ramps from first_chunk_frames, doubling with what was emitted (1, 2, 4, 8, ...), up to chunk_frames.
// Early partial-frame decode: the frames handed over may lack their last residual codebooks (rows not yet
// generated: the codec leaves those codebooks out); no frame is decoded twice.
static void prefix_submit_ready(ServerState & s, const std::shared_ptr<ReqJob> & job) {
    const int ncb = (int) s.model.hp.n_codebooks;
    const int H = (int) job->codes.size() / ncb;
    int complete = H - (ncb - 1);
    if (job->eos_frame >= 0) complete = std::min(complete, job->eos_frame);
    const int prefill = job->prompt.prefill_frames;
    const int ready = prefix_ready(job->codes.data(), H, ncb, (int32_t) s.model.hp.audio_pad_id, complete, job->eos_frame,
                                   job->emitted, prefill, job->gr.stream_codebooks, job->gr.stream_codebooks_first_only);
    const int end = prefix_chunk_end(s.prefix, ready, job->emitted, prefill);
    if (end < 0) return;
    ZacTask t;
    t.job = job;
    t.kind = ZacTask::WINDOW;
    t.codes = decode_rows(s, *job, H);
    t.H = (int) t.codes.size() / ncb;
    t.f_hi = end + job->ctx_frames;
    zac_lane_submit(job->gr.decoder->lanes[job->lane], std::move(t));
    job->emitted = end;
}

// Eviction: hand the final whole decode to the lane, which finishes the channel.
static void submit_finalize(ServerState & s, const std::shared_ptr<ReqJob> & job, bool dropped) {
    const int ncb = (int) s.model.hp.n_codebooks;
    const int nf = (int) job->codes.size() / ncb;
    ZacTask t;
    t.job = job;
    t.kind = ZacTask::FINALIZE;
    t.drop = dropped;
    // EOS inside the silence prefill leaves no audio: the generation failed.
    t.fail = !dropped && job->eos_frame >= 0 && job->eos_frame <= job->prompt.prefill_frames;
    if (!dropped && !t.fail) {
        t.codes = decode_rows(s, *job, nf);
        t.H = (int) t.codes.size() / ncb;
    }
    zac_lane_submit(job->gr.decoder->lanes[job->lane], std::move(t));
}

// A throwaway prefill + decode step at every ladder width JIT-compiles every backbone graph pipeline before the
// first request.
static bool warmup_backbone(zonos3_batch_ctx & bc) {
    if (getenv("ZONOS3_NO_WARMUP")) return true;
    const int W = bc.W, n0 = 16;
    std::vector<float> ids((size_t) n0 * W, 0.0f);
    std::vector<float> logits((size_t) bc.av * bc.ncb, 0.0f);
    const auto t0 = std::chrono::steady_clock::now();
    for (const auto & dg : bc.dec_ladder) {
        zonos3_slot slot;
        slot.index = dg.width - 1; slot.active = true; slot.n_past = n0;
        if (!zonos3_batch_slot_prefill(bc, slot.index, ids.data(), n0, zonos3_conditioning{}, logits.data())) return false;
        slot.next_ids.assign(W, 0);
        if (!zonos3_batch_step(bc, {&slot})) return false;
    }
    const auto t1 = std::chrono::steady_clock::now();
    fprintf(stderr, "zonos3-server: backbone warmup (%zu ladder widths) in %.0f ms\n",
            bc.dec_ladder.size(), std::chrono::duration<double, std::milli>(t1 - t0).count());
    return true;
}

// ------------------------------------------------------------------------------------------- alignment probe / LACI

void attach_probe(const ServerState & s, ReqJob & job) {
    const GenReq & gr = job.gr;
    const zonos3_laci::LaciHeads * heads = gr.laci ? gr.laci : gr.follow;
    if (!heads) return;
    int start = -1, len = 0;
    if (!zonos3_locate_text_span(s.model, job.prompt.ids.data(), job.prompt.n_rows, start, len))
        throw std::invalid_argument("the prompt has no complete text span for the alignment probe");
    if (len > s.probe_max_text)
        throw std::invalid_argument("the text is too long for the alignment probe (" + std::to_string(len) +
                                    " text rows; at most " + std::to_string(s.probe_max_text) + ")");
    job.probe = zonos3_align_probe{};
    for (const auto & h : heads->heads) job.probe.heads.push_back({h.layer, h.head});
    job.probe.text_start = start;
    job.probe.text_len = len;
    job.probe_ctl = zonos3_laci::ProbeControl{};
    job.probe_ctl.text_len = len;
    // scheduler._span_follower: the request's span deltas placed by the reading position.
    if (gr.follow && !gr.steering.spans.empty()) {
        std::vector<zonos3_follow::Range> ranges(gr.follow_tokens.begin(), gr.follow_tokens.end());
        std::vector<std::vector<float>> deltas;
        for (const auto & sp : gr.steering.spans) deltas.push_back(sp.delta);
        zonos3_follow::FollowConfig cfg;
        cfg.lead = gr.follow_lead;
        cfg.lag = gr.follow_lag;
        job.follower = std::make_unique<zonos3_follow::SpanFollower>(std::move(ranges), std::move(deltas), cfg);
    }
    if (!gr.laci) return;
    // scheduler._laci_probe: the session of a new request.
    const auto & hp = s.model.hp;
    const int W = (int) hp.n_codebooks + 1, P = job.prompt.prefill_frames;
    zonos3_laci::SessionParams p;
    p.heads = *gr.laci;
    for (int r = job.prompt.n_rows - P; r < job.prompt.n_rows; ++r)
        p.prefill_rows.emplace_back(job.prompt.ids.begin() + (size_t) r * W, job.prompt.ids.begin() + (size_t) (r + 1) * W);
    p.seed = (int64_t) gr.sp.seed;
    p.max_tokens = gr.max_frames;
    p.n_codebooks = (int) hp.n_codebooks;
    p.duration = gr.laci_duration;
    if (hp.n_cond > 0) p.condition_values = std::vector<int>(job.prompt.cond.buckets.begin(), job.prompt.cond.buckets.end());
    p.rates = gr.laci_rates;
    if (!p.rates) {   // the checkpoint's speaking-rate condition row
        for (size_t i = 0; i < hp.cond_names.size() && i < hp.cond_edges.size(); ++i)
            if (hp.cond_names[i] == "speaking_rate" && !hp.cond_edges[i].empty()) {
                zonos3_laci::RateBuckets rb;
                rb.index = (int) i;
                rb.edges.assign(hp.cond_edges[i].begin(), hp.cond_edges[i].end());
                rb.n_buckets = (int) hp.cond_bucket_counts[i];
                p.rates = rb;
            }
    } else if (gr.laci_bucket && !(p.rates->first <= *gr.laci_bucket && *gr.laci_bucket <= p.rates->last())) {
        throw std::invalid_argument("speaking-rate bucket " + std::to_string(*gr.laci_bucket) + " is outside the scale [" +
                                    std::to_string(p.rates->first) + ", " + std::to_string(p.rates->last()) + "]");
    }
    p.bucket = gr.laci_bucket;
    p.rate_deltas = gr.laci_rate_deltas;
    p.follower = job.follower.get();
    p.hold = !gr.prefix;
    p.short_text = gr.short_text;
    job.laci = std::make_unique<zonos3_laci::LaciSession>(std::move(p));
}

// The ACI windows of the probe's masked heads (nullopt: none).
static void set_windows(zonos3_slot & slot, const std::optional<zonos3_laci::Windows> & mask) {
    slot.probe.windows.clear();
    if (mask) for (const auto & w : *mask) slot.probe.windows.push_back({w.head.layer, w.head.head, w.lo, w.hi});
}

// LACI: the session's stream rows [r.begin, r.end) became final; their audio columns go to job.codes.
static void push_rows(ReqJob & job, int ncb, zonos3_laci::RowRange r) {
    const auto & stream = job.laci->stream();
    for (int i = r.begin; i < r.end; ++i) job.codes.insert(job.codes.end(), stream[i].begin(), stream[i].begin() + ncb);
}

// A fresh sampler for the job's current attempt, seeded `seed` (mod 2^32).
static void new_sampler(const ServerState & s, ReqJob & job, uint32_t seed) {
    zonos3_sampling sp = job.gr.sp;
    sp.seed = seed;
    job.smp = std::make_unique<zonos3_sampler>(sp, s.model.hp);
    if (job.gr.stop_id >= 0) job.smp->stop_id = job.gr.stop_id;
    job.smp->min_frames = job.gr.min_frames;
}

// LACI rollback (scheduler._laci_rows): reset the slot and re-prefill it with the prompt (without its silence-prefill
// rows) plus the kept stream rows, under the continuation's seed, condition rows and steering delta; the rows past the
// prefill rows are the repetition-penalty history. The slot's next sample is the attempt's unobserved first row.
static bool laci_resume(const ServerState & s, zonos3_batch_ctx & bc, ReqJob & job, const zonos3_laci::Continuation & c) {
    const int ncb = (int) s.model.hp.n_codebooks, W = ncb + 1;
    const int base = job.n0 - job.prompt.prefill_frames;
    const auto & stream = job.laci->stream();
    std::vector<float> ids((size_t) (base + c.n_rows) * W);
    std::copy_n(job.idf.begin(), (size_t) base * W, ids.begin());
    for (int r = 0; r < c.n_rows; ++r)
        for (int k = 0; k < W; ++k) ids[(size_t) (base + r) * W + k] = (float) stream[r][k];
    zonos3_conditioning cond = job.prompt.cond;
    if (c.condition_values) cond.buckets.assign(c.condition_values->begin(), c.condition_values->end());
    if (c.swap_delta) job.gr.steering.hidden = c.steering_delta ? *c.steering_delta : std::vector<float>{};
    zonos3_slot & slot = job.slot;
    slot.done = false;
    slot.step = 0;
    slot.eos_frame = -1;
    slot.countdown = -1;
    slot.n_past = base + c.n_rows;
    slot.prefill = c.n_rows;   // stream rows already present: the cold-start index is the stream row
    slot.next_ids.clear();
    slot.steer = job.gr.steering.hidden;
    set_windows(slot, job.laci->probe_mask());
    job.probe_ctl.mask.reset();
    job.probe_ctl.stop = false;
    job.readout_ready = false;
    job.history_rows = c.history_rows;
    job.attempt_max = c.max_tokens;
    new_sampler(s, job, (uint32_t) ((uint64_t) c.seed.value_or(job.gr.sp.seed) & 0xFFFFFFFFu));
    for (int r = job.prompt.prefill_frames; r < c.n_rows; ++r)
        job.smp->accept(std::vector<int>(stream[r].begin(), stream[r].begin() + ncb));
    job.smp->generated = 0;   // min_frames counts the attempt's rows (sgl total_generated)
    return zonos3_batch_slot_prefill(bc, slot.index, ids.data(), slot.n_past, cond, job.logits.data());
}

// The hidden delta of the frame the slot generates next (sgl TTSReq.steer_delta): the constant delta, the released
// controls at that frame's weight (steer then hold) and the span deltas, placed by the reading position (span follower)
// or the character-rate schedule.
static void step_steer(ReqJob & jb) {
    const int frame = jb.history_rows + jb.slot.step;
    const zonos3_steering & st = jb.gr.steering;
    if (jb.follower) {
        std::vector<float> span, released;
        if (!st.release.empty()) st.base_at(frame, released);
        const auto & base = st.release.empty() ? st.hidden : released;
        if (jb.follower->delta(frame, span)) {
            for (size_t i = 0; i < base.size() && i < span.size(); ++i) span[i] += base[i];
            jb.slot.steer = std::move(span);
        } else {
            jb.slot.steer = base;
        }
    } else if (!st.spans.empty() || !st.release.empty()) {
        st.delta_at(frame, jb.slot.steer);
    } else {
        return;
    }
    if (jb.twin_index >= 0 && jb.gr.twin_steered) jb.twin_slot.steer = jb.slot.steer;
}

static void worker_loop(ServerState & s) {
    const int ncb = (int) s.model.hp.n_codebooks, av = (int) s.model.hp.audio_vocab;
    const int B = s.batch_slots;
    zonos3_batch_ctx bc;
    auto fail_start = [&](const char * what) {
        fprintf(stderr, "worker: %s\n", what);
        zonos3_batch_free(bc);
        { std::lock_guard<std::mutex> lk(s.sched_mtx); s.worker_failed = true; s.worker_ready = true; }
        s.sched_cv.notify_all();
    };
    zonos3_batch_features features;
    features.probe_max_text = s.probe_max_text;   // LACI / span-follow probes (0 without --laci-heads)
    if (!zonos3_batch_init(bc, s.model, B, s.context_capacity, s.steer.sources, features))
        return fail_start("batch init failed");
    if (!warmup_backbone(bc)) return fail_start("backbone warmup failed");
    std::vector<std::shared_ptr<ReqJob>> slot_job(B);
    { std::lock_guard<std::mutex> lk(s.sched_mtx); s.worker_ready = true; }
    s.sched_cv.notify_all();

    while (!s.stop.load()) {
        // 1. admit waiting jobs into free slots (prefill into the slot's cache band)
        for (;;) {
            std::shared_ptr<ReqJob> job;
            { std::lock_guard<std::mutex> lk(s.sched_mtx); if (!s.incoming.empty()) job = s.incoming.front(); }
            if (!job) break;
            // A CFG job (speaker / controls) needs two slots (conditional + twin); the FIFO head waits for them.
            const bool cfg = job->gr.cfg_scale != 1.0f;
            int primary = -1, twin = -1;
            for (int i = 0; i < B; ++i) {
                if (slot_job[i]) continue;
                if (primary < 0) { primary = i; if (!cfg) break; }
                else { twin = i; break; }
            }
            if (primary < 0 || (cfg && twin < 0)) break;
            { std::lock_guard<std::mutex> lk(s.sched_mtx); s.incoming.pop_front(); }
            job->slot = zonos3_slot{};
            job->slot.index = primary; job->slot.active = true; job->slot.n_past = job->n0;
            job->slot.prefill = job->prompt.prefill_frames;
            job->slot.steer = job->gr.steering.hidden;
            job->slot.accent = job->gr.accent;
            job->slot.accent_strength = job->gr.accent_strength;
            job->codes = zonos3_prefill_codes(s.model, job->prompt);   // stream rows: silence prefill first
            job->emitted = job->prompt.prefill_frames;
            Decoder & dec = *job->gr.decoder;
            job->lane = (int) (dec.next_lane++ % dec.lanes.size());
            new_sampler(s, *job, job->gr.sp.seed);
            job->attempt_max = job->gr.max_frames;
            job->history_rows = 0;
            job->readout_ready = false;
            if (job->probe.enabled()) {   // LACI / span follow: the conditional slot reads the alignment heads
                job->slot.probe = job->probe;
                if (job->laci) set_windows(job->slot, job->laci->probe_mask());
            }
            job->logits.assign((size_t) av * ncb, 0.0f);
            if (cfg) {
                job->twin_slot = zonos3_slot{};
                job->twin_slot.index = twin; job->twin_slot.active = true;
                job->twin_slot.n_past = job->twin.n_rows;
                job->twin_slot.prefill = job->prompt.prefill_frames;
                // The speaker twin shares the hidden delta (not the speaker row); the controls twin runs unsteered.
                if (job->gr.twin_steered) job->twin_slot.steer = job->gr.steering.hidden;
                job->twin_slot.accent = job->gr.accent;   // the accent adapter applies to both branches
                job->twin_slot.accent_strength = job->gr.accent_strength;
                job->twin_logits.assign((size_t) av * ncb, 0.0f);
                job->guided.assign((size_t) av * ncb, 0.0f);
                // The conditional prompt and its twin share one prefill graph per chunk.
                if (!zonos3_batch_pair_prefill(bc, primary, job->idf.data(), job->n0, job->prompt.cond, job->logits.data(),
                                               twin, job->idf_twin.data(), job->twin.n_rows, job->twin.cond,
                                               job->twin_logits.data())) {
                    job_finish(*job, /*failed=*/true);
                    continue;
                }
                job->twin_index = twin;
                slot_job[twin] = job;   // reserves the slot; the loops below skip it (slot.index != i)
            } else if (!zonos3_batch_slot_prefill(bc, primary, job->idf.data(), job->n0, job->prompt.cond, job->logits.data())) {
                job_finish(*job, /*failed=*/true);
                continue;
            }
            job->t_decode0 = std::chrono::steady_clock::now();
            slot_job[primary] = job;
        }

        // 2. nothing in flight -> block until a job arrives (or shutdown)
        bool any = false;
        for (int i = 0; i < B; ++i) if (slot_job[i]) { any = true; break; }
        if (!any) {
            std::unique_lock<std::mutex> lk(s.sched_mtx);
            if (s.incoming.empty() && !s.stop.load()) s.sched_cv.wait_for(lk, std::chrono::milliseconds(200));
            continue;
        }

        // 3. sample one frame per active slot from its current logits
        for (int i = 0; i < B; ++i) {
            auto & job = slot_job[i];
            if (!job || job->slot.index != i || job->slot.done) continue;
            {
                std::lock_guard<std::mutex> lk(job->m);
                if (job->client_dropped) { job->slot.done = true; continue; }
            }
            // Speaker CFG: guide the conditional logits with the twin's and sample the primary only; the twin always
            // receives the primary's sampled frame (lockstep).
            const float * source = job->logits.data();
            if (job->twin_index >= 0) {
                zonos3_cfg_mix(job->logits.data(), job->twin_logits.data(), job->gr.cfg_scale, job->guided.size(),
                               job->guided.data());
                source = job->guided.data();
            }
            // Alignment probe: the readout of the slot's last decode step goes to its observer first (sgl
            // align_probe.deliver); a stop set there makes the row sampled now the attempt's last.
            if (job->readout_ready) {
                job->readout_ready = false;
                const zonos3_probe_readout & ro = job->slot.readout;
                if (job->laci) {
                    try {
                        job->laci->on_step(job->probe_ctl, job->slot.step, ro.argmax, (double) ro.centroid);
                    } catch (const std::exception & e) {   // Detector.observe's ValueError: fail the request
                        fprintf(stderr, "LACI: %s\n", e.what());
                        job_finish(*job, /*failed=*/true);   // marks it dropped: step 4 frees the slot
                        job->slot.done = true;
                        continue;
                    }
                    set_windows(job->slot, job->probe_ctl.mask);
                } else if (job->follower) {
                    job->follower->on_step(job->slot.step, ro.argmax);
                }
            }
            if (job->laci) {
                // LACI: the session owns the stream; only rows no later rollback can change reach job->codes
                // (scheduler._process_last_data). A finishing attempt drains after finish_attempt (step 4).
                std::vector<int32_t> sampled;
                job->smp->stop_bias = (float) job->laci->stop_boost();
                zonos3_slot_sample(s.model, job->slot, *job->smp, source, sampled, job->attempt_max, {});
                std::optional<int> eos;   // the attempt's aligned EOS frame, counted in its own generated rows
                if (job->slot.eos_frame >= 0) eos = std::max(0, job->slot.eos_frame - job->laci->attempt_start());
                job->laci->append(job->slot.next_ids, eos);
                if (job->probe_ctl.stop) job->slot.done = true;
                if (!job->slot.done) push_rows(*job, ncb, job->laci->drain());
            } else {
                zonos3_slot_sample(s.model, job->slot, *job->smp, source, job->codes, job->attempt_max, {});
                job->eos_frame = job->slot.eos_frame;
            }
            if (job->twin_index >= 0) {
                job->twin_slot.next_ids = job->slot.next_ids;
                job->twin_slot.done = job->slot.done;
            }
            if (job->gr.prefix) prefix_submit_ready(s, job);
        }

        // 4. evict finished slots: hand the final decode to the lane (which signals the handler after it drains),
        //    then free the slot immediately so the backbone keeps going.
        for (int i = 0; i < B; ++i) {
            auto & job = slot_job[i];
            if (!job || job->slot.index != i || !job->slot.done) continue;
            if (s.prof) {
                const double wall_ms = std::chrono::duration<double, std::milli>(
                    std::chrono::steady_clock::now() - job->t_decode0).count();
                const int f = job->slot.step;
                fprintf(stderr, "decode[prof]: %d frames  batch_step=%.0fms (%.1f fps)  wall=%.0fms (%.1f fps)\n",
                        f, job->decode_step_ms, f ? f * 1000.0 / job->decode_step_ms : 0.0,
                        wall_ms, f ? f * 1000.0 / wall_ms : 0.0);
            }
            bool dropped;
            { std::lock_guard<std::mutex> lk(job->m); dropped = job->client_dropped; }
            if (job->laci && !dropped && !job->gr.codes_only) {
                // scheduler._laci_rows: a failure rolls the attempt back in place (the slot stays with the job), else
                // the result is final.
                std::optional<zonos3_laci::Continuation> c;
                try {
                    c = job->laci->finish_attempt();
                } catch (const std::exception & e) {
                    fprintf(stderr, "LACI: %s\n", e.what());
                    job_finish(*job, /*failed=*/true);
                    slot_job[i].reset();
                    continue;
                }
                if (c) {
                    const auto & f = job->laci->failures().back();
                    fprintf(stderr, "LACI: %s (bin %d) at row %d, rolling back to row %d, retry %d/%d\n", f.kind.c_str(),
                            f.bin, f.detect_row, f.rollback_row, job->laci->attempts(), zonos3_laci::Config{}.max_retries);
                    if (laci_resume(s, bc, *job, *c)) {
                        push_rows(*job, ncb, job->laci->drain());
                        if (job->gr.prefix) prefix_submit_ready(s, job);
                        continue;
                    }
                    fprintf(stderr, "LACI: re-prefill failed\n");
                    job_finish(*job, /*failed=*/true);
                    slot_job[i].reset();
                    continue;
                }
                push_rows(*job, ncb, job->laci->drain(/*final=*/true));
                const auto & end = job->laci->end();
                job->eos_frame = end && end->second ? *end->second : -1;   // stream row = audio-stream frame
                const zonos3_laci::Record rec = job->laci->record();
                job->laci_record = rec.to_json();
                fprintf(stderr, "LACI: end=%s attempts=%d rows=%d (generated %d)\n", rec.end.c_str(), rec.attempts,
                        rec.final_rows, rec.generated_total);
            }
            if (job->gr.codes_only) job_finish(*job, /*failed=*/dropped);   // the handler reads job->codes
            else submit_finalize(s, job, dropped);
            const int twin_index = job->twin_index;
            slot_job[i].reset();
            if (twin_index >= 0) slot_job[twin_index].reset();   // free both slots together
        }

        // 5. one batched decode step over the still-active slots, refill their logits
        std::vector<zonos3_slot *> step_list;
        for (int i = 0; i < B; ++i)
            // A slot re-prefilled by a LACI rollback this tick (no next_ids yet) samples from its prefill logits first.
            if (slot_job[i] && slot_job[i]->slot.index == i && !slot_job[i]->slot.done && !slot_job[i]->slot.next_ids.empty()) {
                ReqJob & jb = *slot_job[i];
                step_steer(jb);
                step_list.push_back(&jb.slot);
                if (jb.twin_index >= 0) step_list.push_back(&jb.twin_slot);
            }
        if (!step_list.empty()) {
            const auto st0 = std::chrono::steady_clock::now();
            if (!zonos3_batch_step(bc, step_list)) {
                fprintf(stderr, "worker: backbone decode step failed\n");
                for (auto & job : slot_job) if (job) { job_finish(*job, /*failed=*/true); job.reset(); }
                continue;
            }
            const double step_ms = std::chrono::duration<double, std::milli>(std::chrono::steady_clock::now() - st0).count();
            for (int i = 0; i < B; ++i) {
                auto & job = slot_job[i];
                if (!job || job->slot.index != i || job->slot.done || job->slot.next_ids.empty()) continue;
                memcpy(job->logits.data(), zonos3_batch_slot_logits(bc, job->slot.index), (size_t) av * ncb * sizeof(float));
                if (job->twin_index >= 0)
                    memcpy(job->twin_logits.data(), zonos3_batch_slot_logits(bc, job->twin_slot.index),
                           (size_t) av * ncb * sizeof(float));
                job->decode_step_ms += step_ms;   // exact at batch=1; upper bound when batched
                if (job->slot.probe.enabled()) job->readout_ready = true;   // observed before the next sample
            }
        }
    }
    // drain: signal any still-occupied slots so their handlers unblock
    for (auto & job : slot_job) if (job) job_finish(*job, /*failed=*/true);
    zonos3_batch_free(bc);
}

Decoder::~Decoder() {
    for (auto & lane : lanes) zac_free(lane.zac);
    zac_free(zac);
}

bool load_decoder(ServerState & s, const std::string & path, bool use_gpu, std::string & err) {
    auto d = std::make_unique<Decoder>();
    d->path = path;
    if (!zac_load(d->zac, path.c_str(), use_gpu)) {
        err = "failed to load zac " + path;
        return false;
    }
    d->id = d->zac.head_variant;
    for (const auto & other : s.decoders)
        if (other->id == d->id) {
            err = "--zac " + path + ": decoder " + d->id + " (mossdual_istft.head.variant) is already loaded from " +
                  other->path;
            return false;
        }
    d->lanes = std::vector<ZacLane>(s.zac_threads);
    for (int i = 0; i < s.zac_threads; ++i)
        if (!zac_clone(d->lanes[i].zac, d->zac)) {
            err = "failed to load zac " + path + " for pool lane " + std::to_string(i);
            return false;
        }
    s.decoders.push_back(std::move(d));
    return true;
}

void start_scheduler(ServerState & s) {
    for (auto & d : s.decoders)
        for (auto & lane : d->lanes) lane.th = std::thread(zac_lane_loop, std::ref(s), std::ref(lane));
    s.worker = std::thread(worker_loop, std::ref(s));
    std::unique_lock<std::mutex> lk(s.sched_mtx);
    s.sched_cv.wait(lk, [&] { return s.worker_ready; });
}

void stop_scheduler(ServerState & s) {
    s.stop.store(true);
    s.sched_cv.notify_all();
    if (s.worker.joinable()) s.worker.join();
    for (auto & d : s.decoders)
        for (auto & lane : d->lanes) {
            { std::lock_guard<std::mutex> lk(lane.m); }
            lane.cv.notify_all();
            if (lane.th.joinable()) lane.th.join();
        }
}

void submit_job(ServerState & s, const std::shared_ptr<ReqJob> & job) {
    { std::lock_guard<std::mutex> lk(s.sched_mtx); s.incoming.push_back(job); }
    s.sched_cv.notify_one();
}

void submit_decode(ServerState & s, const std::shared_ptr<ReqJob> & job) {
    Decoder & dec = *job->gr.decoder;
    job->lane = (int) (dec.next_lane++ % dec.lanes.size());
    submit_finalize(s, job, /*dropped=*/false);
}

}  // namespace zs
