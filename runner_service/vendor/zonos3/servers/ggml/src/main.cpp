// zonos3-cli — GDN FullAttnRes raw-reference synthesis and numerical validation.
#include "zonos3.h"
#include "zonos3-sampler.h"
#include "zonos3-steer.h"
#include "zonos3-accent.h"
#include "zonos3-laci.h"
#include "zac.h"
#include "zac-encoder.h"
#include "ssl-encoder.h"
#include "spk-encoder.h"
#include "audio-decode.h"
#include "wav.h"
#include "zonos3-text.h"
#include "npy.h"
#include "ggml.h"
#include "prune-stats.h"
#include "prune-policy.h"
#include "model-paths.h"
#include "zonos3-cond-io.h"
#include "zonos3-phonemes.h"
#include "json.hpp"
#include "server/generate.h"
#include "server/http.h"
#include "server/projects.h"
#include "server/schema.h"
#include "server/state.h"
#include "exe-path.h"
#include <fstream>
#include <optional>
#include <algorithm>
#include <chrono>
#include <cmath>
#include <climits>
#include <cstdio>
#include <cstring>
#include <memory>
#include <map>
#include <string>
#include <vector>

static void usage(const char * a0) {
    fprintf(stderr,
            "usage: %s <model.gguf> [--cpu|--gpu] [mode]  (default --gpu on Apple Silicon, --cpu elsewhere)\n"
            "  --validate <input_ids.npy> <out_dir> [--layers N]\n"
            "  --generate <input_ids.npy> <out.{npy,wav}>\n"
            "  --tts <text> <out.{npy,wav}> | --build-prompt <text> <out_ids.npy>\n"
            "  --request <req.json> --build-prompt <ids.npy> [--steer F] [--dump-steer <steer.npy> [--steer-frames N]]\n"
            "                      [--accent-adapters F] (accents.gguf auto-detected next to the backbone)\n"
            "      the prompt POST /v2/generate builds for a v2 GenerateRequest (ids.npy + ids.cond.json, and\n"
            "      ids.twin.npy + ids.twin.cond.json for a speaker / controls CFG twin); no generation\n"
            "  --batch-test <t1|t2|...> | --decode-bench <text> [--slots N]\n"
            "  --clone <ref_audio> | --reference-codes <codes.npy> | --speaker-embedding <emb.npy>\n"
            "      mutually exclusive speaker sources; reference codes are ordinary [T,10], IDs 0..4095;\n"
            "      the embedding is a float32 [192] npy (spk-encoder-cli output)\n"
            "  --speaker-mode embedding|reference   how --clone conditions the prompt (default embedding)\n"
            "  --spk-encoder <spk-encoder.gguf> --zac-encoder <zac-encoder.gguf> --ssl-teacher <ssl-teacher.gguf>\n"
            "      (--clone only; companions auto-discover next to the backbone)\n"
            "  --save-speaker-embedding <out.npy> saves the [192] embedding (--clone or --speaker-embedding)\n"
            "  --save-reference <out.npy> saves encoded reference codes\n"
            "  --loudness <LUFS> --fmax <Hz> --speaking-rate <bytes/s>  condition values (bucketed by the model)\n"
            "  --condition-buckets <a,b,c> explicit buckets per condition, 0..count-1 (count = unknown) with the model's counts\n"
            "      for loudness,fmax,speaking-rate; wins per name over the values above; omitted conditions are unknown\n"
            "  --cfg-scale <s> speaker CFG scale, default 1 (off; needs a speaker source; --tts only)\n"
            "  --silence-prefill <n> codec-silence frames appended to the prompt, 0..%d (default %d)\n"
            "  --headroom-db <db> output attenuation 0..24 for decoded wavs (default: decoder's value; --tts only)\n"
            "  --steer <steer.gguf> steering directions (auto-detected next to backbone when a control flag is given; --tts only)\n"
            "  --control ID=DOSE (repeatable) --control-span START:END:ID=DOSE[,ID=DOSE...] (repeatable, hidden-space controls over\n"
            "    code-point range [START,END) of the --tts text) --control-span-rate R (frames per character, default 1)\n"
            "  --steer-hold H --steer-onset S --steer-ramp S  steer then hold: the --control hidden doses at full dose for S\n"
            "    seconds, a linear ramp over S seconds to H x dose (default: the steer.gguf's steer_release, else constant)\n"
            "  --zac <zac.gguf> decodes output (default zac-keep2.gguf next to the backbone; zac.gguf = full-size decoder);\n"
            "    companions auto-discover next to backbone\n"
            "  --max N --seed N --greedy --recompute\n"
            "  --dump-ids <ids.npy> saves the full teacher-forcing sequence for calibration\n"
            "  --probe-heads <heads.json> alignment probe of the decode steps (a assets/laci_heads/ JSON: its heads, weight 1) over\n"
            "      the prompt's BOS..EOS text rows; --probe-window LAYER:HEAD:LO:HI (repeatable) masks that head to text\n"
            "      indices [LO,HI] (ACI hard mask); --dump-probe <probe.npy> saves float32 [steps,2] (argmax, centroid)\n"
            "      per decode step (--generate/--tts, KV path)\n"
            "  --accents <accents.gguf> --accent NAME [--accent-strength S] accent adapter (default strength 1;\n"
            "      accents.gguf auto-discovers next to the backbone; --generate/--tts)\n"
            "  --dump-logits <logits.npy> saves float32 [1+steps,10,4098] conditional logits: the prompt's last row, then\n"
            "      every decode step (--generate/--tts, KV path)\n"
            "  --prune-mask <stats.bin> (--keep N | --mass-eps E | --drop-below-hits H)\n"
            "   or: %s --phonemize <lexicon dir> --language L [--no-guess] [--symbols <symbols.json>] --text T\n"
            "      prints the /v2/phonemize JSON (gold LMDB lexicon; espeak-ng guesses via ZONOS3_ESPEAK_NG_LIBRARY);\n"
            "      --symbols (a JSON array of phoneme-token symbols) marks other pronunciations \"unsupported\"\n",
            a0,
            zonos3_generation().silence_prefill_max_frames, zonos3_generation().silence_prefill_default_frames, a0);
}

// Conditioning sidecar next to a prompt npy. A missing sidecar means "no replaced rows" (the
// coverage check then rejects prompts that contain speaker/condition rows).
static bool load_cond_sidecar(const std::string & ids_path, zonos3_prompt & meta) {
    std::string err;
    if (zonos3_cond_load(zonos3_cond_path(ids_path), meta, err)) return true;
    if (err == "missing") return true;
    fprintf(stderr, "%s: %s\n", zonos3_cond_path(ids_path).c_str(), err.c_str());
    return false;
}

// Persist the audio stream (silence-prefill rows + generated rows): codes npy (+ [eos, prefill]) and/or
// a decoded wav when --zac is set. The wav omits the prefill frames' audio.
static bool emit_output(const std::string & out, const zonos3_model & model, const zonos3_prompt & prompt,
                        const std::vector<int32_t> & generated, int n_frames, int eos_frame,
                        const std::string & zac_path, bool use_gpu, double headroom_db = -1.0) {
    const int ncb = (int) model.hp.n_codebooks, prefill = prompt.prefill_frames;
    std::vector<int32_t> codes = zonos3_prefill_codes(model, prompt);
    codes.insert(codes.end(), generated.begin(), generated.end());
    const int total = prefill + n_frames;
    const bool want_wav = ends_with(out, ".wav");
    if (!want_wav) {
        std::vector<float> cf(codes.begin(), codes.end());
        if (!npy::save_f32(out, cf.data(), {(int64_t) total, (int64_t) ncb})) return false;
        const float ef[2] = {(float) eos_frame, (float) prefill};
        if (!npy::save_f32(out + ".eos.npy", ef, {(int64_t) 2})) return false;
        printf("wrote %s\n", out.c_str());
    }
    if (want_wav && zac_path.empty()) {
        fprintf(stderr, "error: .wav output requested but no --zac <zac.gguf> given (zac-keep2.gguf is auto-detected next to <model.gguf>)\n");
        return false;
    }
    if (!zac_path.empty() && n_frames > 0) {
        const std::string wav = want_wav ? out : out + ".wav";
        zac_model dm;
        if (!zac_load(dm, zac_path.c_str(), use_gpu)) {
            zac_free(dm);
            return false;
        }
        std::vector<float> audio;
        bool decoded = zac_decode(dm, codes.data(), total, ncb, eos_frame, audio);
        if (decoded) {
            const size_t drop = std::min(audio.size(), (size_t) prefill * zac_samples_per_frame(dm));
            audio.erase(audio.begin(), audio.begin() + drop);
            const float gain = zac_output_gain(dm, headroom_db);
            for (float & v : audio) v *= gain;
        }
        const bool written = decoded && wav_pcm16_write(wav.c_str(), audio, dm.sample_rate);
        zac_free(dm);
        if (!written) return false;
    }
    return true;
}

static void pr(const char * nm, const ggml_tensor * t) {
    if (!t) {
        printf("  %-22s <null>\n", nm);
        return;
    }
    printf("  %-22s ne=[%5lld,%5lld,%4lld,%2lld] %s\n", nm,
           (long long) t->ne[0], (long long) t->ne[1],
           (long long) t->ne[2], (long long) t->ne[3], ggml_type_name(t->type));
}

static std::vector<std::string> split_pipe(const std::string & s) {
    std::vector<std::string> out;
    size_t start = 0;
    for (;;) {
        size_t p = s.find('|', start);
        out.push_back(s.substr(start, p == std::string::npos ? std::string::npos : p - start));
        if (p == std::string::npos) break;
        start = p + 1;
    }
    return out;
}

// Raw decode-throughput probe: prefill `n_slots` identical sequences, warm up the CUDA graph, then
// time `n_steps` batched decode steps, ignoring EOS (tokens are fed back but generation never
// stops). Measures the decode-graph rate independent of model correctness — usable on quants that
// won't generate naturally (e.g. q4_k emits EOS at frame 0). Reports aggregate frames/s.
static int run_decode_bench(zonos3_model & model, const std::string & text, int n_slots, int n_steps, const zonos3_prompt_options & opt) {
    const int ncb = (int) model.hp.n_codebooks, av = (int) model.hp.audio_vocab;
    if (n_slots < 1) n_slots = 1;

    zonos3_prompt prompt;
    if (!zonos3_build_prompt(model, text, opt, prompt) || n_steps < 1 ||
        (int64_t) prompt.n_rows + n_steps + 8 > model.hp.n_ctx_train) {
        fprintf(stderr, "decode-bench: prompt and decode steps exceed model context\n");
        return 1;
    }
    const int n0 = prompt.n_rows;
    std::vector<float> idf(prompt.ids.begin(), prompt.ids.end());

    zonos3_batch_ctx bc;
    if (!zonos3_batch_init(bc, model, n_slots, n0 + n_steps + 8)) {
        fprintf(stderr, "decode-bench: batch init failed\n");
        return 1;
    }
    zonos3_sampling sp;
    sp.greedy = true; // argmax feedback; values don't affect timing
    std::vector<zonos3_sampler> smp(n_slots, zonos3_sampler(sp, model.hp));
    std::vector<zonos3_slot> slots(n_slots);
    std::vector<std::vector<float>> lo(n_slots, std::vector<float>((size_t) av * ncb));
    std::vector<int32_t> sink; // throwaway codes
    std::vector<zonos3_slot *> active(n_slots);
    for (int i = 0; i < n_slots; ++i) {
        slots[i] = zonos3_slot{};
        slots[i].index = i;
        slots[i].active = true;
        slots[i].n_past = n0;
        slots[i].prefill = prompt.prefill_frames;
        if (!zonos3_batch_slot_prefill(bc, i, idf.data(), n0, prompt.cond, lo[i].data())) {
            fprintf(stderr, "decode-bench: prefill slot %d failed\n", i);
            zonos3_batch_free(bc);
            return 1;
        }
        active[i] = &slots[i];
    }
    auto step_once = [&]() {
        for (int i = 0; i < n_slots; ++i) { // sample -> next_ids (ignore done/eos)
            sink.clear();
            zonos3_slot_sample(model, slots[i], smp[i], lo[i].data(), sink, n_steps + n0 + 16, {});
            slots[i].done = false;
        }
        if (!zonos3_batch_step(bc, active)) return false;
        for (int i = 0; i < n_slots; ++i)
            memcpy(lo[i].data(), zonos3_batch_slot_logits(bc, i), (size_t) av * ncb * sizeof(float));
        return true;
    };

    for (int w = 0; w < 8; ++w) {
        if (!step_once()) {
            fprintf(stderr, "decode-bench: warmup step failed\n");
            zonos3_batch_free(bc);
            return 1;
        }
    }
    auto t0 = std::chrono::steady_clock::now();
    for (int s = 0; s < n_steps; ++s) {
        if (!step_once()) {
            fprintf(stderr, "decode-bench: decode step failed\n");
            zonos3_batch_free(bc);
            return 1;
        }
    }
    const double dt = std::chrono::duration<double>(std::chrono::steady_clock::now() - t0).count();

    const double fps = (double) n_slots * n_steps / dt;
    const double rt = fps / ZONOS3_FRAME_RATE;
    printf("decode-bench: slots=%d steps=%d  %.3fs  %.0f frames/s  %.1fx real-time  (%.1f frames/s/slot)\n",
           n_slots, n_steps, dt, fps, rt, fps / n_slots);
    zonos3_batch_free(bc);
    return 0;
}

// Sequence-parity diagnostic. Each request is compared with isolated generation;
// every greedy request, including EOS, must match for this check to pass.
// A failure alone cannot distinguish numerical autoregressive drift from a state
// error: raw-reference-state compares identical teacher-forced rows and actual
// persistent tensors, including paused and reused slots, on CPU or GPU.
static int run_batch_test(zonos3_model & model, const std::string & texts_joined, int n_slots,
                          int max_frames, const zonos3_sampling & sp_in,
                          bool use_gpu, const zonos3_prompt_options & opt) {
    if (use_gpu && !model.is_gpu) {
        fprintf(stderr, "batch-test: --gpu requested but no GPU backend loaded\n");
        return 1;
    }
    const int ncb = (int) model.hp.n_codebooks, av = (int) model.hp.audio_vocab;
    std::vector<std::string> texts = split_pipe(texts_joined);
    const int n_req = (int) texts.size();
    if (n_slots < n_req) n_slots = n_req;

    struct Job {
        std::string text;
        zonos3_prompt prompt;
        std::vector<float> idf; // prompt ids [n0*W] floats
        int n0 = 0;
        std::unique_ptr<zonos3_sampler> smp;
        zonos3_slot slot;
        std::vector<float> logits; // [av*ncb] current sampling source
        std::vector<int32_t> codes;
        int eos_frame = -1;
    };
    std::vector<Job> jobs(n_req);
    int max_n0 = 0;
    for (int i = 0; i < n_req; ++i) {
        if (!zonos3_build_prompt(model, texts[i], opt, jobs[i].prompt) ||
            (int64_t) jobs[i].prompt.n_rows + max_frames > model.hp.n_ctx_train) {
            fprintf(stderr, "batch-test: prompt and generation exceed model context\n");
            return 1;
        }
        const int n0 = jobs[i].prompt.n_rows;
        jobs[i].text = texts[i];
        jobs[i].idf.assign(jobs[i].prompt.ids.begin(), jobs[i].prompt.ids.end());
        jobs[i].n0 = n0;
        max_n0 = std::max(max_n0, n0);
    }

    // single-sequence reference (current KV path) per request
    std::vector<std::vector<int32_t>> ref_codes(n_req);
    std::vector<int> ref_eos(n_req, -1);
    for (int i = 0; i < n_req; ++i) {
        zonos3_sampling spr = sp_in;
        int eos = -1;
        if (zonos3_generate(model, jobs[i].prompt, max_frames, spr, 1.0f, ref_codes[i], eos, true) < 0)
            return 1;
        ref_eos[i] = eos;
    }

    zonos3_batch_ctx bc;
    if (!zonos3_batch_init(bc, model, n_slots, max_n0 + max_frames)) {
        fprintf(stderr, "batch-test: batch init failed\n");
        return 1;
    }

    for (int i = 0; i < n_req; ++i) {
        jobs[i].smp = std::make_unique<zonos3_sampler>(sp_in, model.hp);
        jobs[i].slot = zonos3_slot{};
        jobs[i].slot.index = i;
        jobs[i].slot.active = true;
        jobs[i].slot.n_past = jobs[i].n0;
        jobs[i].slot.prefill = jobs[i].prompt.prefill_frames;
        jobs[i].logits.assign((size_t) av * ncb, 0.0f);
        if (!zonos3_batch_slot_prefill(bc, i, jobs[i].idf.data(), jobs[i].n0, jobs[i].prompt.cond, jobs[i].logits.data())) {
            fprintf(stderr, "batch-test: prefill slot %d failed\n", i);
            zonos3_batch_free(bc);
            return 1;
        }
    }

    for (;;) {
        bool any = false; // sample phase
        for (int i = 0; i < n_req; ++i) {
            Job & j = jobs[i];
            if (!j.slot.active || j.slot.done) continue;
            zonos3_slot_sample(model, j.slot, *j.smp, j.logits.data(), j.codes, max_frames, {});
            j.eos_frame = j.slot.eos_frame;
            any = true;
        }
        if (!any) break;
        std::vector<zonos3_slot *> step_list; // step phase
        for (int i = 0; i < n_req; ++i)
            if (jobs[i].slot.active && !jobs[i].slot.done) step_list.push_back(&jobs[i].slot);
        if (step_list.empty()) break;
        if (!zonos3_batch_step(bc, step_list)) {
            fprintf(stderr, "batch-test: decode step failed\n");
            zonos3_batch_free(bc);
            return 1;
        }
        for (zonos3_slot * sp : step_list)
            memcpy(jobs[sp->index].logits.data(), zonos3_batch_slot_logits(bc, sp->index),
                   (size_t) av * ncb * sizeof(float));
    }

    printf("\n=== batch-test: %d requests in %d slots, %s, max=%d ===\n",
           n_req, n_slots, sp_in.greedy ? "greedy" : "sampling", max_frames);
    int fails = 0;
    for (int i = 0; i < n_req; ++i) {
        const auto & b = jobs[i].codes;
        const auto & r = ref_codes[i];
        const int nb = (int) b.size() / ncb, nr = (int) r.size() / ncb, cmp = std::min(nb, nr);
        int first_div = -1, agree = 0;
        for (int f = 0; f < cmp; ++f) {
            bool eq = true;
            for (int cb = 0; cb < ncb; ++cb)
                if (b[(size_t) f * ncb + cb] != r[(size_t) f * ncb + cb]) {
                    eq = false;
                    break;
                }
            if (eq) ++agree;
            else if (first_div < 0) first_div = f;
        }
        const bool exact = (nb == nr && agree == cmp && jobs[i].eos_frame == ref_eos[i]);
        printf("  req %d \"%.28s\": batch=%d(eos=%d) ref=%d(eos=%d)  agree=%d/%d first_div=%d %s\n",
               i, jobs[i].text.c_str(), nb, jobs[i].eos_frame, nr, ref_eos[i], agree, cmp, first_div,
               exact ? "EXACT" : "");
        if (sp_in.greedy && !exact) ++fails;
    }
    for (int i = 0; i < n_req; ++i)
        for (int k = i + 1; k < n_req; ++k)
            if (jobs[i].text == jobs[k].text) {
                const bool same = jobs[i].codes == jobs[k].codes;
                printf("  F.2 slots %d,%d identical prompt -> %s\n", i, k, same ? "MATCH" : "MISMATCH");
                if (!same) ++fails;
            }

    zonos3_batch_free(bc);
    if (sp_in.greedy) {
        printf("batch-test greedy sequence parity: %s (all %d requests; logits/state not qualified by this check)\n",
               fails ? "FAIL" : "PASS", n_req);
    } else {
        printf("batch-test sampling diagnostic: %s (no batch-vs-isolated parity claim)\n",
               fails ? "FAIL" : "COMPLETE");
    }
    return fails ? 1 : 0;
}

// strtod that rejects empty/trailing text and non-finite values.
static bool parse_finite(const char * s, double & v) {
    char * end = nullptr;
    v = strtod(s, &end);
    return end != s && *end == '\0' && std::isfinite(v);
}

// "a,b,c": exactly three integers in 0..1024 (the model's per-table count = unknown; zonos3_build_prompt range-checks).
static bool parse_condition_buckets(const std::string & s, int out[3]) {
    size_t pos = 0;
    for (int k = 0; k < 3; ++k) {
        const size_t comma = s.find(',', pos);
        if ((k < 2) != (comma != std::string::npos)) return false;
        const std::string tok = s.substr(pos, comma == std::string::npos ? std::string::npos : comma - pos);
        char * end = nullptr;
        const long v = tok.empty() ? -1 : strtol(tok.c_str(), &end, 10);
        if (tok.empty() || *end != '\0' || v < 0 || v > 1024) return false;
        out[k] = (int) v;
        pos = comma + 1;
    }
    return true;
}

// "START:END:ID=DOSE[,ID=DOSE...]" with integer code-point bounds.
static bool parse_control_span(const std::string & s, zonos3_control_span & out) {
    const size_t c1 = s.find(':'), c2 = c1 == std::string::npos ? c1 : s.find(':', c1 + 1);
    if (c2 == std::string::npos) return false;
    auto integer = [](const std::string & t, int & v) {
        char * end = nullptr;
        const long x = t.empty() ? 0 : strtol(t.c_str(), &end, 10);
        if (t.empty() || *end != '\0' || x < INT_MIN || x > INT_MAX) return false;
        v = (int) x;
        return true;
    };
    if (!integer(s.substr(0, c1), out.start) || !integer(s.substr(c1 + 1, c2 - c1 - 1), out.end)) return false;
    size_t pos = c2 + 1;
    while (pos <= s.size()) {
        const size_t comma = s.find(',', pos);
        const std::string item = s.substr(pos, comma == std::string::npos ? std::string::npos : comma - pos);
        const size_t eq = item.rfind('=');
        double dose = 0;
        if (eq == std::string::npos || eq == 0 || !parse_finite(item.c_str() + eq + 1, dose)) return false;
        if (!out.controls.emplace(item.substr(0, eq), dose).second) return false;
        if (comma == std::string::npos) break;
        pos = comma + 1;
    }
    return !out.controls.empty();
}

// "LAYER:HEAD:LO:HI" with integer fields.
static bool parse_probe_window(const std::string & s, zonos3_align_probe::window & w) {
    int layer, head, lo, hi;
    char tail;
    if (sscanf(s.c_str(), "%d:%d:%d:%d%c", &layer, &head, &lo, &hi, &tail) != 4) return false;
    w.layer = layer;
    w.head = head;
    w.lo = lo;
    w.hi = hi;
    return true;
}

// --probe-heads / --probe-window / --dump-probe, --accent and --dump-logits of --generate and --tts.
struct cli_extras {
    std::string heads_path, probe_out, logits_out;
    std::vector<zonos3_align_probe::window> windows;
    int accent = -1;
    float accent_strength = 1.0f;
    std::vector<zonos3_probe_readout> readouts;
    std::vector<float> step_logits;
    zonos3_generate_features features;
};

static bool cli_prepare_extras(const zonos3_model & model, zonos3_prompt & prompt, cli_extras & x) {
    if (x.accent >= 0) {
        prompt.cond.accent = x.accent;
        prompt.cond.accent_strength = x.accent_strength;
    }
    if (!x.heads_path.empty()) {
        zonos3_align_probe & p = x.features.probe;
        try {
            for (const auto & h : zonos3_laci::LaciHeads::load(x.heads_path).heads) p.heads.push_back({h.layer, h.head});
        } catch (const std::exception & e) {
            fprintf(stderr, "probe: %s\n", e.what());
            return false;
        }
        p.windows = x.windows;
        if (!zonos3_locate_text_span(model, prompt.ids.data(), prompt.n_rows, p.text_start, p.text_len)) {
            fprintf(stderr, "probe: the prompt has no BOS..EOS text span\n");
            return false;
        }
        x.features.readouts = &x.readouts;
    }
    if (!x.logits_out.empty()) x.features.step_logits = &x.step_logits;
    return true;
}

static bool cli_save_extras(const zonos3_model & model, const cli_extras & x) {
    if (!x.probe_out.empty()) {
        std::vector<float> v;
        for (const auto & r : x.readouts) {
            v.push_back((float) r.argmax);
            v.push_back(r.centroid);
        }
        if (!npy::save_f32(x.probe_out, v.data(), {(int64_t) x.readouts.size(), 2})) return false;
        printf("dump-probe: %zu steps -> %s\n", x.readouts.size(), x.probe_out.c_str());
    }
    if (!x.logits_out.empty()) {
        const int64_t row = (int64_t) model.hp.n_codebooks * model.hp.audio_vocab;
        if (!npy::save_f32(x.logits_out, x.step_logits.data(),
                           {(int64_t) x.step_logits.size() / row, (int64_t) model.hp.n_codebooks, (int64_t) model.hp.audio_vocab}))
            return false;
        printf("dump-logits: %lld rows -> %s\n", (long long) (x.step_logits.size() / row), x.logits_out.c_str());
    }
    return true;
}

// zonos3-cli --phonemize <lexicon dir> --language L [--no-guess] [--symbols <symbols.json>] --text T
static int phonemize_main(int argc, char ** argv) {
    std::string lexicon_dir = argv[2], language, text, symbols_path;
    bool guess = true, have_text = false;
    for (int i = 3; i < argc; ++i) {
        if (!strcmp(argv[i], "--language") && i + 1 < argc) language = argv[++i];
        else if (!strcmp(argv[i], "--text") && i + 1 < argc) {
            text = argv[++i];
            have_text = true;
        } else if (!strcmp(argv[i], "--symbols") && i + 1 < argc) symbols_path = argv[++i];
        else if (!strcmp(argv[i], "--no-guess")) guess = false;
        else {
            usage(argv[0]);
            return 1;
        }
    }
    if (language.empty() || !have_text) {
        usage(argv[0]);
        return 1;
    }
    std::string err;
    auto lexicon = zonos3_phoneme_lexicon::open(lexicon_dir, err);
    if (!lexicon) {
        fprintf(stderr, "error: %s\n", err.c_str());
        return 1;
    }
    zonos3_phonemize_result result;
    if (!lexicon->phonemize(text, language, guess, result, err)) {
        fprintf(stderr, "error: %s\n", err.c_str());
        return 2;
    }
    if (!symbols_path.empty()) {
        std::ifstream f(symbols_path);
        if (!f) {
            fprintf(stderr, "error: cannot read %s\n", symbols_path.c_str());
            return 1;
        }
        zonos3_phonemize_restrict(result, nlohmann::json::parse(f).get<std::vector<std::string>>());
    }
    printf("%s\n", zonos3_phonemize_json(result).c_str());
    return 0;
}

// zonos3-cli <model.gguf> --request req.json --build-prompt ids.npy: the server's request resolver (plan_generate:
// the same validation, speaker, condition, text normalization and CFG-twin rules as POST /v2/generate) without
// generating. Writes ids.npy (float32 [rows, n_codebooks+1]) + ids.cond.json and, for a CFG twin, ids.twin.npy +
// ids.twin.cond.json -- the files of `python -m zonos3.reference --request req.json --dump-prompt ids.npy`.
// --dump-steer steer.npy [--steer-frames N]: the hidden steering delta of generated frames 0..N-1 (float32
// [N, sources * hidden], zeros where none; default N 120), written when the request steers hidden sources.
static int request_prompt_main(int argc, char ** argv) {
    const std::string path = argv[1];
    std::string request_path, out, zac_encoder, ssl_teacher, spk_encoder, steer_path, dump_steer;
    int steer_frames = 120;
    zs::ServerState s;
    s.default_voice_dir.clear(); // speaker ids need the server's voices
    for (int i = 2; i < argc; ++i) {
        const bool v = i + 1 < argc;
        if (!strcmp(argv[i], "--request") && v) request_path = argv[++i];
        else if (!strcmp(argv[i], "--build-prompt") && v) out = argv[++i];
        else if (!strcmp(argv[i], "--dump-steer") && v) dump_steer = argv[++i];
        else if (!strcmp(argv[i], "--steer-frames") && v) steer_frames = atoi(argv[++i]);
        else if (!strcmp(argv[i], "--gpu")) s.use_gpu = true;
        else if (!strcmp(argv[i], "--cpu")) s.use_gpu = false;
        else if (!strcmp(argv[i], "--spk-encoder") && v) spk_encoder = argv[++i];
        else if (!strcmp(argv[i], "--zac-encoder") && v) zac_encoder = argv[++i];
        else if (!strcmp(argv[i], "--ssl-teacher") && v) ssl_teacher = argv[++i];
        else if (!strcmp(argv[i], "--steer") && v) steer_path = argv[++i];
        else if (!strcmp(argv[i], "--accent-adapters") && v) s.accent_adapters_path = argv[++i];
        else {
            usage(argv[0]);
            return 1;
        }
    }
    if (request_path.empty() || out.empty()) {
        fprintf(stderr, "error: --request needs --build-prompt <ids.npy>\n");
        return 1;
    }
    nlohmann::json body;
    {
        std::ifstream f(request_path, std::ios::binary);
        body = nlohmann::json::parse(f, nullptr, /*allow_exceptions=*/false);
        if (!f || body.is_discarded()) {
            fprintf(stderr, "error: cannot read JSON request %s\n", request_path.c_str());
            return 1;
        }
    }
    if (const auto detail = zs::schema::validate("GenerateRequest", body)) {
        fprintf(stderr, "422 %s\n", nlohmann::json{{"detail", *detail}}.dump().c_str());
        return 2;
    }
    std::string err;
    if (!zs::configure_silence_prefill(s, err)) {
        fprintf(stderr, "error: %s\n", err.c_str());
        return 1;
    }
    s.spk_path = spk_encoder.empty() ? mp_find_companion(path, "spk-encoder.gguf") : spk_encoder;
    s.encoder_path = zac_encoder.empty() ? mp_find_companion(path, "zac-encoder.gguf") : zac_encoder;
    s.teacher_path = ssl_teacher.empty() ? mp_find_companion(path, "ssl-teacher.gguf") : ssl_teacher;
    // The server's accent adapters next to the backbone (accents.gguf).
    if (s.accent_adapters_path.empty()) s.accent_adapters_path = mp_find_companion(path, "accents.gguf");
    if (!zonos3_model_load(s.model, path.c_str(), s.use_gpu)) return 1;
    s.model_path = path;
    // The server's default --max and slot capacity, so R5 refuses what the server refuses.
    if (!zs::configure_frame_budget(s, err)) {
        fprintf(stderr, "error: %s\n", err.c_str());
        return 1;
    }
    if (!steer_path.empty()) {
        if (!zonos3_steer_load(s.steer, steer_path.c_str(), s.model, err)) {
            fprintf(stderr, "error: --steer %s: %s\n", steer_path.c_str(), err.c_str());
            return 1;
        }
        s.steer_path = steer_path;
        s.steer_loaded = true;
    }
    if (!zs::load_accent_adapters(s, err)) {
        fprintf(stderr, "error: %s\n", err.c_str());
        return 1;
    }
    zs::GeneratePlan plan;
    try {
        plan = zs::plan_generate(s, body, std::nullopt);
    } catch (const zs::HttpError & e) {
        fprintf(stderr, "%d %s\n", e.status, e.body.dump().c_str());
        return 2;
    }
    const int64_t W = (int64_t) s.model.hp.n_codebooks + 1;
    auto save = [&](const std::string & ids_path, const zonos3_prompt & p) {
        std::vector<float> idf(p.ids.begin(), p.ids.end());
        return npy::save_f32(ids_path, idf.data(), {(int64_t) p.n_rows, W}) &&
               zonos3_cond_save(zonos3_cond_path(ids_path), p);
    };
    bool saved = save(out, plan.prompt);
    std::string twin_path;
    if (plan.twin.n_rows > 0) {
        const bool npy_ext = ends_with(out, ".npy");
        twin_path = (npy_ext ? out.substr(0, out.size() - 4) : out) + ".twin.npy";
        saved = saved && save(twin_path, plan.twin);
    }
    printf("build-prompt: %d rows x %d -> %s", plan.prompt.n_rows, (int) W, out.c_str());
    if (!twin_path.empty()) printf(" (+ %s twin -> %s)", plan.cfg_kind.c_str(), twin_path.c_str());
    printf("\n");
    const zonos3_steering & st = plan.gr.steering;
    if (saved && !dump_steer.empty() && st.hidden_active() && steer_frames > 0) {
        const size_t n = st.sources.size() * (size_t) st.hidden_size;
        std::vector<float> rows((size_t) steer_frames * n, 0.0f), d;
        for (int f = 0; f < steer_frames; ++f) {
            st.delta_at(f, d);
            if (d.size() == n) std::copy(d.begin(), d.end(), rows.begin() + (size_t) f * n);
        }
        saved = npy::save_f32(dump_steer, rows.data(), {(int64_t) steer_frames, (int64_t) n});
        printf("steer: %d frames x %zu -> %s\n", steer_frames, n, dump_steer.c_str());
    }
    return saved ? 0 : 1;
}

int main(int argc, char ** argv) {
    if (argc < 2) {
        usage(argv[0]);
        return 1;
    }
    if (!strcmp(argv[1], "--phonemize") && argc >= 3) return phonemize_main(argc, argv);
    for (int i = 2; i < argc; ++i)
        if (!strcmp(argv[i], "--request")) return request_prompt_main(argc, argv);
    const std::string path = argv[1];
    bool use_gpu = ZONOS3_DEFAULT_USE_GPU;
    bool validate = false, generate = false, do_tts = false, do_build_prompt = false, do_batch_test = false, do_decode_bench = false;
    std::string ids_path, out_dir, out_codes, reference_path, text, prompt_out, zac_path, dump_ids_path;
    std::string clone_path, encoder_path, teacher_path, save_reference_path;
    std::string speaker_mode, speaker_embedding_path, save_speaker_embedding_path, spk_encoder_path;
    std::string loudness_arg, fmax_arg, rate_arg, buckets_arg, cfg_arg, silence_arg, headroom_arg;
    std::string prune_mask_path; // --prune-mask: runtime expert pruning
    int mask_keep = -1, mask_drop_hits = -1;
    double mask_mass_eps = -1.0;
    int n_layer_limit = -1, max_frames = 450, n_slots = 1;
    bool use_kv = true;
    std::string steer_path, span_rate_arg, hold_arg, onset_arg, ramp_arg;
    std::vector<std::string> control_args, span_args;
    zonos3_sampling sp;
    cli_extras extras;
    std::string accents_path, accent_name, accent_strength_arg;
    std::vector<std::string> window_args;
    for (int i = 2; i < argc; ++i) {
        if (!strcmp(argv[i], "--gpu")) use_gpu = true;
        else if (!strcmp(argv[i], "--cpu")) use_gpu = false;
        else if (!strcmp(argv[i], "--validate") && i + 2 < argc) {
            validate = true;
            ids_path = argv[++i];
            out_dir = argv[++i];
        } else if (!strcmp(argv[i], "--generate") && i + 2 < argc) {
            generate = true;
            ids_path = argv[++i];
            out_codes = argv[++i];
        } else if (!strcmp(argv[i], "--tts") && i + 2 < argc) {
            do_tts = true;
            text = argv[++i];
            out_codes = argv[++i];
        } else if (!strcmp(argv[i], "--build-prompt") && i + 2 < argc) {
            do_build_prompt = true;
            text = argv[++i];
            prompt_out = argv[++i];
        } else if (!strcmp(argv[i], "--batch-test") && i + 1 < argc) {
            do_batch_test = true;
            text = argv[++i];
        } else if (!strcmp(argv[i], "--decode-bench") && i + 1 < argc) {
            do_decode_bench = true;
            text = argv[++i];
        } else if (!strcmp(argv[i], "--slots") && i + 1 < argc) n_slots = atoi(argv[++i]);
        else if (!strcmp(argv[i], "--layers") && i + 1 < argc) n_layer_limit = atoi(argv[++i]);
        else if (!strcmp(argv[i], "--max") && i + 1 < argc) max_frames = atoi(argv[++i]);
        else if (!strcmp(argv[i], "--zac") && i + 1 < argc) zac_path = argv[++i];
        else if (!strcmp(argv[i], "--reference-codes") && i + 1 < argc) reference_path = argv[++i];
        else if (!strcmp(argv[i], "--clone") && i + 1 < argc) clone_path = argv[++i];
        else if (!strcmp(argv[i], "--zac-encoder") && i + 1 < argc) encoder_path = argv[++i];
        else if (!strcmp(argv[i], "--ssl-teacher") && i + 1 < argc) teacher_path = argv[++i];
        else if (!strcmp(argv[i], "--save-reference") && i + 1 < argc) save_reference_path = argv[++i];
        else if (!strcmp(argv[i], "--speaker-mode") && i + 1 < argc) speaker_mode = argv[++i];
        else if (!strcmp(argv[i], "--speaker-embedding") && i + 1 < argc) speaker_embedding_path = argv[++i];
        else if (!strcmp(argv[i], "--save-speaker-embedding") && i + 1 < argc) save_speaker_embedding_path = argv[++i];
        else if (!strcmp(argv[i], "--spk-encoder") && i + 1 < argc) spk_encoder_path = argv[++i];
        else if (!strcmp(argv[i], "--loudness") && i + 1 < argc) loudness_arg = argv[++i];
        else if (!strcmp(argv[i], "--fmax") && i + 1 < argc) fmax_arg = argv[++i];
        else if (!strcmp(argv[i], "--speaking-rate") && i + 1 < argc) rate_arg = argv[++i];
        else if (!strcmp(argv[i], "--condition-buckets") && i + 1 < argc) buckets_arg = argv[++i];
        else if (!strcmp(argv[i], "--cfg-scale") && i + 1 < argc) cfg_arg = argv[++i];
        else if (!strcmp(argv[i], "--silence-prefill") && i + 1 < argc) silence_arg = argv[++i];
        else if (!strcmp(argv[i], "--headroom-db") && i + 1 < argc) headroom_arg = argv[++i];
        else if (!strcmp(argv[i], "--seed") && i + 1 < argc) sp.seed = (uint32_t) atoi(argv[++i]);
        else if (!strcmp(argv[i], "--greedy")) sp.greedy = true;
        else if (!strcmp(argv[i], "--recompute")) use_kv = false;
        else if (!strcmp(argv[i], "--steer") && i + 1 < argc) steer_path = argv[++i];
        else if (!strcmp(argv[i], "--control") && i + 1 < argc) control_args.push_back(argv[++i]);
        else if (!strcmp(argv[i], "--control-span") && i + 1 < argc) span_args.push_back(argv[++i]);
        else if (!strcmp(argv[i], "--control-span-rate") && i + 1 < argc) span_rate_arg = argv[++i];
        else if (!strcmp(argv[i], "--steer-hold") && i + 1 < argc) hold_arg = argv[++i];
        else if (!strcmp(argv[i], "--steer-onset") && i + 1 < argc) onset_arg = argv[++i];
        else if (!strcmp(argv[i], "--steer-ramp") && i + 1 < argc) ramp_arg = argv[++i];
        else if (!strcmp(argv[i], "--dump-ids") && i + 1 < argc) dump_ids_path = argv[++i];
        else if (!strcmp(argv[i], "--probe-heads") && i + 1 < argc) extras.heads_path = argv[++i];
        else if (!strcmp(argv[i], "--probe-window") && i + 1 < argc) window_args.push_back(argv[++i]);
        else if (!strcmp(argv[i], "--dump-probe") && i + 1 < argc) extras.probe_out = argv[++i];
        else if (!strcmp(argv[i], "--dump-logits") && i + 1 < argc) extras.logits_out = argv[++i];
        else if (!strcmp(argv[i], "--accents") && i + 1 < argc) accents_path = argv[++i];
        else if (!strcmp(argv[i], "--accent") && i + 1 < argc) accent_name = argv[++i];
        else if (!strcmp(argv[i], "--accent-strength") && i + 1 < argc) accent_strength_arg = argv[++i];
        else if (!strcmp(argv[i], "--prune-mask") && i + 1 < argc) prune_mask_path = argv[++i];
        else if (!strcmp(argv[i], "--keep") && i + 1 < argc) mask_keep = atoi(argv[++i]);
        else if (!strcmp(argv[i], "--drop-below-hits") && i + 1 < argc) mask_drop_hits = atoi(argv[++i]);
        else if (!strcmp(argv[i], "--mass-eps") && i + 1 < argc) mask_mass_eps = atof(argv[++i]);
        else {
            usage(argv[0]);
            return 1;
        }
    }
    if (max_frames < 1 || n_slots < 1) {
        fprintf(stderr, "error: --max and --slots must be positive\n");
        return 1;
    }
    if ((int) validate + (int) generate + (int) do_tts + (int) do_build_prompt +
            (int) do_batch_test + (int) do_decode_bench >
        1) {
        fprintf(stderr, "error: select exactly one execution mode\n");
        return 1;
    }
    for (const auto & a : window_args) {
        zonos3_align_probe::window w;
        if (!parse_probe_window(a, w)) {
            fprintf(stderr, "error: --probe-window expects LAYER:HEAD:LO:HI\n");
            return 1;
        }
        extras.windows.push_back(w);
    }
    if ((!window_args.empty() || !extras.probe_out.empty()) && extras.heads_path.empty()) {
        fprintf(stderr, "error: --probe-window and --dump-probe need --probe-heads\n");
        return 1;
    }
    if ((!accents_path.empty() || !accent_strength_arg.empty()) && accent_name.empty()) {
        fprintf(stderr, "error: --accents and --accent-strength need --accent\n");
        return 1;
    }
    if (!accent_strength_arg.empty()) {
        double v;
        if (!parse_finite(accent_strength_arg.c_str(), v)) {
            fprintf(stderr, "error: --accent-strength must be finite\n");
            return 1;
        }
        extras.accent_strength = (float) v;
    }
    if ((!extras.heads_path.empty() || !accent_name.empty() || !extras.logits_out.empty()) && !generate && !do_tts) {
        fprintf(stderr, "error: --probe-heads, --accent and --dump-logits require --generate or --tts\n");
        return 1;
    }
    if ((!extras.heads_path.empty() || !extras.logits_out.empty()) && !use_kv) {
        fprintf(stderr, "error: --probe-heads and --dump-logits need the KV decode path; drop --recompute\n");
        return 1;
    }

    if (!mp_file_exists(path)) {
        fprintf(stderr, "failed to load model %s\n", path.c_str());
        mp_print_missing_hint("backbone-f16.gguf (or another quant)", path);
        return 1;
    }

    // Companion discovery, gated on the modes that actually need the file (an unconditional
    // zac pickup would silently start emitting sibling .wavs on .npy outputs).
    if (zac_path.empty() && !out_codes.empty() && ends_with(out_codes, ".wav")) {
        zac_path = mp_find_companion(path, MP_DEFAULT_ZAC);
        if (!zac_path.empty()) fprintf(stderr, "zonos3-cli: using %s found next to model: %s\n", MP_DEFAULT_ZAC, zac_path.c_str());
        else {
            // fail before generation rather than after minutes of decode
            fprintf(stderr, "error: .wav output requested but no --zac <zac.gguf> given (zac-keep2.gguf is auto-detected next to <model.gguf>)\n");
            mp_print_missing_hint(MP_DEFAULT_ZAC, mp_parent_dir(path) + "/" + MP_DEFAULT_ZAC);
            return 1;
        }
    }
    const bool clone = !clone_path.empty();
    if ((int) clone + (int) !reference_path.empty() + (int) !speaker_embedding_path.empty() > 1) {
        fprintf(stderr, "error: --clone, --reference-codes and --speaker-embedding are mutually exclusive\n");
        return 1;
    }
    if (!speaker_mode.empty() && !clone) {
        fprintf(stderr, "error: --speaker-mode requires --clone\n");
        return 1;
    }
    if (!speaker_mode.empty() && speaker_mode != "embedding" && speaker_mode != "reference") {
        fprintf(stderr, "error: --speaker-mode must be embedding or reference\n");
        return 1;
    }
    const bool have_speaker = clone || !reference_path.empty() || !speaker_embedding_path.empty();
    double cond_value[3] = {}; // loudness, fmax, speaking-rate
    bool cond_have[3] = {};
    const std::string * cond_arg[3] = {&loudness_arg, &fmax_arg, &rate_arg};
    const char * cond_flag[3] = {"--loudness", "--fmax", "--speaking-rate"};
    for (int k = 0; k < 3; ++k) {
        if (cond_arg[k]->empty()) continue;
        if (!parse_finite(cond_arg[k]->c_str(), cond_value[k])) {
            fprintf(stderr, "error: %s must be a finite number\n", cond_flag[k]);
            return 1;
        }
        cond_have[k] = true;
    }
    int bucket_id[3] = {-1, -1, -1};
    if (!buckets_arg.empty() && !parse_condition_buckets(buckets_arg, bucket_id)) {
        fprintf(stderr, "error: --condition-buckets needs 3 comma-separated non-negative integers\n");
        return 1;
    }
    float cfg_scale = 1.0f;
    if (!cfg_arg.empty()) {
        double v;
        if (!parse_finite(cfg_arg.c_str(), v) || v < 0.0) {
            fprintf(stderr, "error: --cfg-scale must be finite and >= 0\n");
            return 1;
        }
        cfg_scale = (float) v;
        if (cfg_scale != 1.0f && !have_speaker) {
            fprintf(stderr, "error: --cfg-scale needs a speaker prompt (--clone, --reference-codes or --speaker-embedding)\n");
            return 1;
        }
    }
    const zonos3_generation_registry & limits = zonos3_generation();
    int silence_prefill = limits.silence_prefill_default_frames;
    if (!silence_arg.empty()) {
        double v;
        if (!parse_finite(silence_arg.c_str(), v) || v != std::floor(v) || v < 0 || v > limits.silence_prefill_max_frames) {
            fprintf(stderr, "error: --silence-prefill must be 0..%d\n", limits.silence_prefill_max_frames);
            return 1;
        }
        silence_prefill = (int) v;
    }
    double headroom_db = -1.0; // < 0: the decoder's own value
    if (!headroom_arg.empty() && (!parse_finite(headroom_arg.c_str(), headroom_db) || headroom_db < 0.0 || headroom_db > 24.0)) {
        fprintf(stderr, "error: --headroom-db must be 0..24\n");
        return 1;
    }
    const bool have_conditions = !buckets_arg.empty() || cond_have[0] || cond_have[1] || cond_have[2];
    const bool text_mode = do_tts || do_build_prompt || do_batch_test || do_decode_bench;
    if ((have_speaker || have_conditions || !silence_arg.empty() || !save_reference_path.empty() ||
         !save_speaker_embedding_path.empty()) &&
        !text_mode) {
        fprintf(stderr, "error: speaker, condition and silence-prefill flags require a text/prompt mode; --generate/--validate already contain full prompts\n");
        return 1;
    }
    if ((!cfg_arg.empty() || !headroom_arg.empty()) && !do_tts) {
        fprintf(stderr, "error: --cfg-scale and --headroom-db require --tts\n");
        return 1;
    }
    // Steering: raw controls move the speaker embedding, hidden controls (and spans) add to AttnRes bank sources.
    std::map<std::string, double> controls;
    std::vector<zonos3_control_span> control_spans;
    double span_rate = 0.0;
    if ((!steer_path.empty() || !control_args.empty() || !span_args.empty() || !span_rate_arg.empty() ||
         !hold_arg.empty() || !onset_arg.empty() || !ramp_arg.empty()) && !do_tts) {
        fprintf(stderr, "error: --steer, --control, --control-span, --control-span-rate and --steer-hold/-onset/-ramp require --tts\n");
        return 1;
    }
    // Steer then hold: NaN = the bundle's default.
    double release_hold = std::nan(""), release_onset = std::nan(""), release_ramp = std::nan("");
    auto parse_release = [](const std::string & arg, double max, const char * name, double & out) {
        if (arg.empty()) return true;
        if (parse_finite(arg.c_str(), out) && out >= 0.0 && out <= max) return true;
        fprintf(stderr, "error: %s must be a number in [0, %g]\n", name, max);
        return false;
    };
    if (!parse_release(hold_arg, 1.0, "--steer-hold", release_hold) ||
        !parse_release(onset_arg, 600.0, "--steer-onset", release_onset) ||
        !parse_release(ramp_arg, 600.0, "--steer-ramp", release_ramp))
        return 1;
    for (const auto & a : control_args) {
        const size_t eq = a.rfind('=');
        double dose = 0;
        if (eq == std::string::npos || eq == 0 || !parse_finite(a.c_str() + eq + 1, dose)) {
            fprintf(stderr, "error: --control expects ID=DOSE\n");
            return 1;
        }
        if (!controls.emplace(a.substr(0, eq), dose).second) {
            fprintf(stderr, "error: duplicate --control %s\n", a.substr(0, eq).c_str());
            return 1;
        }
    }
    for (const auto & a : span_args) {
        zonos3_control_span span;
        if (!parse_control_span(a, span)) {
            fprintf(stderr, "error: --control-span expects START:END:ID=DOSE[,ID=DOSE...]\n");
            return 1;
        }
        control_spans.push_back(std::move(span));
    }
    if (!span_rate_arg.empty()) {
        if (span_args.empty()) {
            fprintf(stderr, "error: --control-span-rate needs --control-span\n");
            return 1;
        }
        if (!parse_finite(span_rate_arg.c_str(), span_rate) || span_rate <= 0.0) {
            fprintf(stderr, "error: --control-span-rate must be a positive number\n");
            return 1;
        }
    }
    if (steer_path.empty() && (!controls.empty() || !control_spans.empty())) {
        steer_path = mp_find_companion(path, "steer.gguf");
        if (steer_path.empty()) {
            mp_print_missing_hint("steer.gguf", mp_parent_dir(path) + "/steer.gguf");
            return 1;
        }
        fprintf(stderr, "zonos3-cli: using steer.gguf found next to model: %s\n", steer_path.c_str());
    }
    if (!save_reference_path.empty() && !clone && reference_path.empty()) {
        fprintf(stderr, "error: --save-reference requires --clone or --reference-codes\n");
        return 1;
    }
    if (!save_speaker_embedding_path.empty() && !clone && speaker_embedding_path.empty()) {
        fprintf(stderr, "error: --save-speaker-embedding requires --clone or --speaker-embedding\n");
        return 1;
    }
    if (!clone && (!encoder_path.empty() || !teacher_path.empty() || !spk_encoder_path.empty())) {
        fprintf(stderr, "error: --spk-encoder, --zac-encoder and --ssl-teacher overrides require --clone\n");
        return 1;
    }
    // --clone computes the speaker embedding (embedding mode, or --save-speaker-embedding) and/or the
    // reference codes (reference mode, or --save-reference); each needs its own companion models.
    const bool embed_mode = clone && speaker_mode != "reference";
    const bool need_spk = clone && (embed_mode || !save_speaker_embedding_path.empty());
    const bool need_codec = clone && (!embed_mode || !save_reference_path.empty());
    if (need_spk) {
        if (spk_encoder_path.empty()) spk_encoder_path = mp_find_companion(path, "spk-encoder.gguf");
        if (spk_encoder_path.empty()) {
            if (embed_mode) fprintf(stderr, "error: --clone in embedding mode requires spk-encoder.gguf (--spk-encoder or sibling companion)\n");
            else fprintf(stderr, "error: --save-speaker-embedding with --clone requires spk-encoder.gguf (--spk-encoder or sibling companion)\n");
            mp_print_missing_hint("spk-encoder.gguf", mp_parent_dir(path) + "/spk-encoder.gguf");
            return 1;
        }
    }
    if (need_codec) {
        if (encoder_path.empty()) encoder_path = mp_find_companion(path, "zac-encoder.gguf");
        if (teacher_path.empty()) teacher_path = mp_find_companion(path, "ssl-teacher.gguf");
        if (encoder_path.empty() || teacher_path.empty()) {
            fprintf(stderr, "error: --clone requires both --zac-encoder and --ssl-teacher (or sibling companions)\n");
            if (encoder_path.empty()) mp_print_missing_hint("zac-encoder.gguf", mp_parent_dir(path) + "/zac-encoder.gguf");
            if (teacher_path.empty()) mp_print_missing_hint("ssl-teacher.gguf", mp_parent_dir(path) + "/ssl-teacher.gguf");
            return 1;
        }
    }

    zonos3_model model;
    if (!zonos3_model_load(model, path.c_str(), use_gpu)) {
        fprintf(stderr, "failed to load model %s\n", path.c_str());
        if (!mp_file_exists(path)) mp_print_missing_hint("backbone-f16.gguf (or another quant)", path);
        return 1;
    }

    // --prune-mask: dynamically drop low-MSAN experts (runtime equivalent of prune-cli) so a
    // pruned recipe can be auditioned by ear before baking it into a GGUF.
    if (!prune_mask_path.empty()) {
        if (mask_keep < 0 && mask_drop_hits < 0 && mask_mass_eps < 0.0) {
            fprintf(stderr, "error: --prune-mask needs --keep N, --mass-eps E, or --drop-below-hits H\n");
            zonos3_model_free(model);
            return 1;
        }
        std::map<int, prune_stats::layer> st;
        if (!prune_stats::load(prune_mask_path, st)) {
            zonos3_model_free(model);
            return 1;
        }
        const auto keep = prune_policy::select(st, {mask_keep, mask_drop_hits, mask_mass_eps});
        if (!zonos3_set_expert_mask(model, keep)) {
            zonos3_model_free(model);
            return 1;
        }
    }
    // --accent: the bank outlives every use of model.accents (freed at exit).
    struct accent_holder {
        zonos3_accents bank;
        ~accent_holder() { zonos3_accents_free(bank); }
    } accents;
    if (!accent_name.empty()) {
        if (accents_path.empty()) accents_path = mp_find_companion(path, "accents.gguf");
        if (accents_path.empty()) {
            fprintf(stderr, "error: --accent needs accents.gguf (--accents or next to the backbone)\n");
            mp_print_missing_hint("accents.gguf", mp_parent_dir(path) + "/accents.gguf");
            zonos3_model_free(model);
            return 1;
        }
        std::string err;
        if (!zonos3_accents_load(accents.bank, accents_path.c_str(), model, err)) {
            fprintf(stderr, "accents: %s\n", err.c_str());
            zonos3_model_free(model);
            return 1;
        }
        extras.accent = zonos3_accent_index(accents.bank, accent_name);
        if (extras.accent < 0 || extras.accent == accents.bank.edit) {
            fprintf(stderr, "error: unknown accent '%s'%s\n", accent_name.c_str(),
                    extras.accent >= 0 ? " (the speech-edit adapter is not an accent)" : "");
            zonos3_model_free(model);
            return 1;
        }
        model.accents = &accents.bank;
    }

    // Speaker sources: reference codes (raw-reference prefix) or the 192-d embedding (speaker row).
    std::vector<int32_t> reference_codes;
    std::vector<float> speaker_embedding;
    if (clone) {
        auto pcm = audio_decode_file(clone_path.c_str(), 44100, (int) limits.reference_audio_max_seconds);
        if (pcm.empty()) {
            fprintf(stderr, "clone: failed to decode %s\n", clone_path.c_str());
            zonos3_model_free(model);
            return 1;
        }
        if (need_spk) {
            spk_model spk;
            const bool ok = spk_load(spk, spk_encoder_path.c_str(), use_gpu) &&
                            spk_embed(spk, pcm.data(), (int64_t) pcm.size(), 44100, speaker_embedding);
            spk_free(spk);
            if (!ok || speaker_embedding.size() != model.hp.speaker_dim) {
                fprintf(stderr, "clone: failed to load the speaker encoder or embed %s\n", clone_path.c_str());
                zonos3_model_free(model);
                return 1;
            }
        }
        if (need_codec) {
            zac_encoder_model encoder;
            ssl_model teacher;
            const bool teacher_ok = ssl_load(teacher, teacher_path.c_str(), use_gpu);
            const bool encoder_ok = teacher_ok && zac_encoder_load(encoder, encoder_path.c_str(), use_gpu);
            const bool encoded = encoder_ok &&
                                 zac_encode(encoder, teacher, pcm.data(), (int) pcm.size(), reference_codes);
            zac_encoder_free(encoder);
            ssl_free(teacher);
            if (!encoded || reference_codes.empty()) {
                fprintf(stderr, "clone: failed to load companions, decode or encode %s\n", clone_path.c_str());
                zonos3_model_free(model);
                return 1;
            }
        }
        // Like the server: a reference keeps its first reference_max_frames frames.
        if (reference_codes.size() > (size_t) limits.reference_max_frames * 10)
            reference_codes.resize((size_t) limits.reference_max_frames * 10);
    } else if (!reference_path.empty()) {
        std::string err;
        if (!mp_load_reference_codes(reference_path, reference_codes, err, limits.reference_max_frames)) {
            fprintf(stderr, "reference: %s\n", err.c_str());
            zonos3_model_free(model);
            return 1;
        }
    } else if (!speaker_embedding_path.empty()) {
        std::string err;
        if (!mp_load_speaker_embedding(speaker_embedding_path, speaker_embedding, err)) {
            fprintf(stderr, "speaker embedding: %s\n", err.c_str());
            zonos3_model_free(model);
            return 1;
        }
        if (speaker_embedding.size() != model.hp.speaker_dim) {
            fprintf(stderr, "speaker embedding: expected %u values, got %zu\n", model.hp.speaker_dim, speaker_embedding.size());
            zonos3_model_free(model);
            return 1;
        }
    }
    if (!save_reference_path.empty() && !mp_save_reference_codes(save_reference_path, reference_codes)) {
        fprintf(stderr, "failed to save reference codes: %s\n", save_reference_path.c_str());
        zonos3_model_free(model);
        return 1;
    }
    if (!save_speaker_embedding_path.empty()) {
        if (!mp_save_speaker_embedding(save_speaker_embedding_path, speaker_embedding)) {
            fprintf(stderr, "failed to save speaker embedding: %s\n", save_speaker_embedding_path.c_str());
            zonos3_model_free(model);
            return 1;
        }
        printf("wrote %s\n", save_speaker_embedding_path.c_str());
    }

    // The prompt carries the raw-reference prefix OR the speaker row, never both: --clone picks one by mode.
    zonos3_prompt_options reference_opt;
    const bool use_reference = !reference_path.empty() || (clone && !embed_mode);
    const bool use_embedding = !speaker_embedding_path.empty() || embed_mode;
    zonos3_steering steering;
    {
        zonos3_steer_bundle bundle;
        std::string err;
        std::optional<zonos3_steer_release_frames> release;
        const bool loaded = !steer_path.empty() && zonos3_steer_load(bundle, steer_path.c_str(), model, err);
        if ((!steer_path.empty() && !loaded) ||
            !zonos3_steer_request_release(loaded ? &bundle : nullptr, release_hold, release_onset, release_ramp,
                                          ZONOS3_FRAME_RATE, release, err) ||
            (loaded && !zonos3_steer_resolve(bundle, controls, control_spans, (int) utf8_length(text), span_rate,
                                             use_embedding ? &speaker_embedding : nullptr, steering, err,
                                             release ? &*release : nullptr))) {
            fprintf(stderr, "steering: %s\n", err == "no-bundle" ? "--steer-hold below 1 needs a steer.gguf with steer_release" : err.c_str());
            zonos3_model_free(model);
            return 1;
        }
        if (steering.hidden_active() && !use_kv) {
            fprintf(stderr, "error: hidden-space controls need the KV decode path; drop --recompute\n");
            zonos3_model_free(model);
            return 1;
        }
    }
    if (use_reference) {
        reference_opt.reference_codes = reference_codes.data();
        reference_opt.reference_frames = (int) reference_codes.size() / 10;
    }
    if (use_embedding) reference_opt.speaker_embedding = speaker_embedding.data();
    reference_opt.silence_prefill_frames = silence_prefill;
    if (have_conditions) {
        // Per name: an explicit bucket wins; otherwise the value flag is bucketed; otherwise unknown.
        static const char * const names[3] = {"loudness", "fmax", "speaking_rate"};
        reference_opt.condition_buckets.assign(model.hp.cond_bucket_counts.begin(), model.hp.cond_bucket_counts.end());
        for (int k = 0; k < 3; ++k) {
            const int idx = zonos3_condition_index(model, names[k]);
            if (idx < 0) {
                fprintf(stderr, "error: model has no '%s' condition\n", names[k]);
                zonos3_model_free(model);
                return 1;
            }
            int b = (int) model.hp.cond_bucket_counts[idx];
            if (bucket_id[k] >= 0) b = bucket_id[k];
            else if (cond_have[k]) b = zonos3_condition_bucket(model, idx, cond_value[k]);
            if (b < 0) {
                fprintf(stderr, "error: %s must be a finite number\n", cond_flag[k]);
                zonos3_model_free(model);
                return 1;
            }
            reference_opt.condition_buckets[idx] = b;
        }
    }

    if (do_batch_test) {
        const int rc = run_batch_test(model, text, n_slots, max_frames, sp, use_gpu, reference_opt);
        zonos3_model_free(model);
        return rc;
    }

    if (do_decode_bench) {
        const int rc = run_decode_bench(model, text, n_slots, max_frames, reference_opt);
        zonos3_model_free(model);
        return rc;
    }

    if (validate) {
        std::vector<float> ids;
        std::vector<int64_t> shape;
        if (!npy::load_f32(ids_path, ids, shape) || shape.size() != 2 ||
            shape[0] < 1 || shape[0] > model.hp.n_ctx_train || shape[1] != model.hp.n_codebooks + 1) {
            fprintf(stderr, "validate: expected input_ids [T,11] within model context\n");
            zonos3_model_free(model);
            return 1;
        }
        zonos3_prompt meta;
        if (!load_cond_sidecar(ids_path, meta)) {
            zonos3_model_free(model);
            return 1;
        }
        const int n_tokens = (int) shape[0];
        printf("validate: input_ids [%lld, %lld], n_tokens=%d\n",
               (long long) shape[0], (long long) shape[1], n_tokens);
        const bool okv = zonos3_validate(model, ids.data(), n_tokens, meta.cond, out_dir.c_str(), n_layer_limit);
        zonos3_model_free(model);
        return okv ? 0 : 1;
    }

    if (generate) {
        std::vector<float> ids;
        std::vector<int64_t> shape;
        if (!npy::load_f32(ids_path, ids, shape) || shape.size() != 2 || shape[0] < 1 ||
            shape[1] != model.hp.n_codebooks + 1 ||
            shape[0] + max_frames > model.hp.n_ctx_train) {
            fprintf(stderr, "generate: expected prompt [T,11] with space for generation in model context\n");
            zonos3_model_free(model);
            return 1;
        }
        zonos3_prompt prompt;
        if (!load_cond_sidecar(ids_path, prompt)) {
            zonos3_model_free(model);
            return 1;
        }
        const int n0 = (int) shape[0];
        prompt.n_rows = n0;
        prompt.ids.assign(ids.begin(), ids.end());
        if (!cli_prepare_extras(model, prompt, extras)) {
            zonos3_model_free(model);
            return 1;
        }
        printf("generate: prompt [%d, %lld], max=%d, %s, seed=%u, %s\n",
               n0, (long long) shape[1], max_frames, sp.greedy ? "greedy" : "sampling", sp.seed,
               use_kv ? "kv-cache" : "recompute");
        std::vector<int32_t> codes, full_ids;
        int eos_frame = -1;
        const int n_frames = zonos3_generate(model, prompt, max_frames, sp, 1.0f, codes, eos_frame,
                                             use_kv, dump_ids_path.empty() ? nullptr : &full_ids, {}, nullptr,
                                             &extras.features);
        const int ncb = (int) model.hp.n_codebooks;
        if (n_frames < 0) {
            zonos3_model_free(model);
            return 1;
        }
        if (!dump_ids_path.empty()) {
            const int W = ncb + 1;
            std::vector<float> ff(full_ids.begin(), full_ids.end());
            if (!npy::save_f32(dump_ids_path, ff.data(), {(int64_t) (full_ids.size() / W), (int64_t) W}) ||
                !zonos3_cond_save(zonos3_cond_path(dump_ids_path), prompt)) {
                zonos3_model_free(model);
                return 1;
            }
            printf("dump-ids: %zu rows x %d -> %s\n", full_ids.size() / W, W, dump_ids_path.c_str());
        }
        if (!cli_save_extras(model, extras)) {
            zonos3_model_free(model);
            return 1;
        }
        printf("generate: %d frames, eos_frame=%d\n", n_frames, eos_frame);
        if (n_frames > 0) {
            printf("  frame0:");
            for (int cb = 0; cb < ncb; ++cb) printf(" %d", codes[cb]);
            printf("\n");
        }
        const bool emitted = emit_output(out_codes, model, prompt, codes, n_frames, eos_frame, zac_path, use_gpu);
        zonos3_model_free(model);
        return emitted ? 0 : 1;
    }

    if (do_build_prompt) {
        zonos3_prompt prompt;
        if (!zonos3_build_prompt(model, text, reference_opt, prompt)) {
            fprintf(stderr, "build-prompt: failed\n");
            zonos3_model_free(model);
            return 1;
        }
        const int W = (int) model.hp.n_codebooks + 1;
        std::vector<float> idf(prompt.ids.begin(), prompt.ids.end());
        const bool saved = npy::save_f32(prompt_out, idf.data(), {(int64_t) prompt.n_rows, (int64_t) W}) &&
                           zonos3_cond_save(zonos3_cond_path(prompt_out), prompt);
        printf("build-prompt: \"%s\" -> %d rows x %d -> %s\n",
               text.c_str(), prompt.n_rows, W, prompt_out.c_str());
        zonos3_model_free(model);
        return saved ? 0 : 1;
    }

    if (do_tts) {
        zonos3_prompt prompt;
        if (!zonos3_build_prompt(model, text, reference_opt, prompt)) {
            zonos3_model_free(model);
            return 1; // build_prompt reported the reason
        }
        if ((int64_t) prompt.n_rows + max_frames > model.hp.n_ctx_train) {
            fprintf(stderr, "tts: prompt (%d rows) and %d generated frames exceed the %u-position context\n",
                    prompt.n_rows, max_frames, model.hp.n_ctx_train);
            zonos3_model_free(model);
            return 1;
        }
        if (!cli_prepare_extras(model, prompt, extras)) {
            zonos3_model_free(model);
            return 1;
        }
        const int n0 = prompt.n_rows;
        const int ncb = (int) model.hp.n_codebooks;
        printf("tts: \"%s\" -> prompt [%d, %d] (speaker_rows=%d, silence_prefill=%d), max=%d, %s, %s, cfg=%g\n",
               text.c_str(), n0, ncb + 1, prompt.speaker_rows, prompt.prefill_frames, max_frames,
               sp.greedy ? "greedy" : "sampling", use_kv ? "kv-cache" : "recompute", (double) cfg_scale);
        std::vector<int32_t> codes, full_ids;
        int eos_frame = -1;
        const int n_frames = zonos3_generate(model, prompt, max_frames, sp, cfg_scale, codes,
                                             eos_frame, use_kv, dump_ids_path.empty() ? nullptr : &full_ids, {}, &steering,
                                             &extras.features);
        if (n_frames < 0) {
            zonos3_model_free(model);
            return 1;
        }
        if (!dump_ids_path.empty()) {
            const int W = ncb + 1;
            std::vector<float> ff(full_ids.begin(), full_ids.end());
            if (!npy::save_f32(dump_ids_path, ff.data(), {(int64_t) (full_ids.size() / W), (int64_t) W}) ||
                !zonos3_cond_save(zonos3_cond_path(dump_ids_path), prompt)) {
                zonos3_model_free(model);
                return 1;
            }
            printf("dump-ids: %zu rows x %d -> %s\n", full_ids.size() / W, W, dump_ids_path.c_str());
        }
        if (!cli_save_extras(model, extras)) {
            zonos3_model_free(model);
            return 1;
        }
        printf("tts: %d frames, eos_frame=%d\n", n_frames, eos_frame);
        if (n_frames > 0) {
            printf("  frame0:");
            for (int cb = 0; cb < ncb; ++cb) printf(" %d", codes[cb]);
            printf("\n");
        }
        const bool emitted = emit_output(out_codes, model, prompt, codes, n_frames, eos_frame, zac_path, use_gpu, headroom_db);
        zonos3_model_free(model);
        return emitted ? 0 : 1;
    }

    const auto & hp = model.hp;
    printf("\n=== %s backbone ===\n", "bingua2 GDN FullAttnRes rawclone");
    const zonos3_layer * moe_ly = nullptr;
    for (const auto & ly : model.layers)
        if (ly.is_moe) {
            moe_ly = &ly;
            break;
        }
    printf("layers=%u  embd=%u  head_dim=%u  heads=%u/%u  ff=%u  experts=%u  ctx=%u  moe_latent=%lld  residual=full_attnres\n",
           hp.n_layer, hp.n_embd, hp.head_dim, hp.n_head, hp.n_head_kv, hp.n_ff,
           hp.n_expert, hp.n_ctx_train, moe_ly ? (long long) moe_ly->latent_down->ne[1] : 0LL);
    printf("rope_base=%.1f  rms_eps=%.2e  qk_eps=%.2e  softcap=%.1f\n",
           hp.rope_freq_base, hp.rms_eps, hp.qk_norm_eps, hp.logit_softcap);
    printf("codebooks=%u  cb_size=%u  audio_vocab=%u  eoa=%u  pad=%u  text_vocab=%u\n",
           hp.n_codebooks, hp.codebook_size, hp.audio_vocab, hp.eoa_id, hp.audio_pad_id, hp.text_vocab);

    int n_moe = 0, n_dense = 0;
    for (const auto & ly : model.layers) (ly.is_moe ? n_moe : n_dense)++;
    printf("dense=%d  moe=%d (top%d)\n", n_dense, n_moe, moe_ly ? moe_ly->top_k : 0);

    printf("spot-check tensors:\n");
    pr("audio_embd[0]", model.audio_embd[0]);
    pr("text_embd", model.text_embd);
    pr("output", model.output);
    pr("output_norm", model.output_norm);
    pr("attnres_q", model.attnres_q);
    pr("blk0.attn_q", model.layers[0].wq);
    pr("blk0.attn_k", model.layers[0].wk);
    pr("blk0.attn_temp", model.layers[0].attn_temp);
    pr("blk0.gdn_in", model.layers[0].gdn_in);
    pr("blk0.gdn_conv", model.layers[0].gdn_conv);
    pr("blk0.gdn_norm", model.layers[0].gdn_norm);
    pr("blk0.gater", model.layers[0].gater);
    pr("blk0.ffn_gate", model.layers[0].ffn_gate);
    if (moe_ly) {
        pr("moe.gate_exps", moe_ly->ffn_gate_exps);
        pr("moe.down_exps", moe_ly->ffn_down_exps);
        pr("moe.latent_down", moe_ly->latent_down);
        pr("moe.router", moe_ly->router_w);
    }

    zonos3_model_free(model);
    return 0;
}
