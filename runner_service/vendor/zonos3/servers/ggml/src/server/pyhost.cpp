#include "compat.h"

#include "server/pyhost.h"

#include "server/capabilities.h"
#include "server/generate.h"
#include "server/http.h"
#include "server/phonemize.h"
#include "server/speakers.h"
#include "server/state.h"
#include "server/util.h"
#include "server/workers.h"

#include "audio-decode.h"
#include "zonos3-edit.h"

#include <algorithm>
#include <cmath>
#include <cstdio>
#include <cstdlib>
#include <filesystem>
#include <thread>

namespace zs {

using json = nlohmann::json;
namespace fs = std::filesystem;
namespace E = zonos3_edit;

namespace {

bool set(const json & j, const char * key) {
    return j.is_object() && j.contains(key) && !j[key].is_null();
}

// soundfile.write(path, audio, sr, subtype="FLOAT"): a mono IEEE-float WAV.
void write_float_wav(const std::string & path, const std::vector<float> & audio, int sr) {
    std::vector<uint8_t> out;
    auto bytes = [&](const void * p, size_t n) { const uint8_t * b = (const uint8_t *) p; out.insert(out.end(), b, b + n); };
    auto w32 = [&](uint32_t v) { bytes(&v, 4); };
    auto w16 = [&](uint16_t v) { bytes(&v, 2); };
    const uint32_t data = (uint32_t) (audio.size() * 4);
    out.reserve(44 + data);
    bytes("RIFF", 4);
    w32(36 + data);
    bytes("WAVE", 4);
    bytes("fmt ", 4);
    w32(16);
    w16(3);
    w16(1);
    w32((uint32_t) sr);
    w32((uint32_t) sr * 4);
    w16(4);
    w16(32);
    bytes("data", 4);
    w32(data);
    bytes(audio.data(), audio.size() * 4);
    FILE * f = fopen(path.c_str(), "wb");
    if (!f) throw std::runtime_error("cannot write " + path);
    const bool ok = fwrite(out.data(), 1, out.size(), f) == out.size();
    if (fclose(f) != 0 || !ok) throw std::runtime_error("cannot write " + path);
}

// A sound file (any container ffmpeg reads) as mono PCM at the API rate (decode_audio_bytes of an upload), decoded up
// to the registry's upload limit so the runtime's own length checks see it.
std::vector<float> decode_file(const std::string & path) {
    const int max_seconds = (int) zonos3_generation().upload_max_seconds;
    std::vector<float> pcm = audio_decode_file(path.c_str(), zonos3_generation().sample_rate, max_seconds);
    if (pcm.empty())
        fail(400, "could not decode the reference audio (any container ffmpeg reads, at most " +
                      std::to_string(max_seconds) + " seconds)");
    return pcm;
}

// [[code] * K] * frames <-> row-major [frames, K].
std::vector<int32_t> flat_rows(const json & rows, int k) {
    std::vector<int32_t> out;
    out.reserve(rows.size() * (size_t) k);
    for (const auto & row : rows) {
        if (!row.is_array() || (int) row.size() != k)
            throw std::invalid_argument("codes must be rows of " + std::to_string(k) + " codebooks");
        for (const auto & v : row) out.push_back(v.get<int32_t>());
    }
    return out;
}
json rows_json(const std::vector<int32_t> & flat, int k) {
    json out = json::array();
    for (size_t r = 0; r + (size_t) k <= flat.size(); r += (size_t) k)
        out.push_back(std::vector<int32_t>(flat.begin() + (ptrdiff_t) r, flat.begin() + (ptrdiff_t) (r + k)));
    return out;
}

double frame_rate(const ServerState & s) {
    const zac_model & zac = default_decoder(s).zac;
    return (double) zac.sample_rate / zac_samples_per_frame(zac);
}

// ---------------------------------------------------------------------------------------------------- init facts

// The speaking-rate condition row's bucket edges (UTF-8 bytes / s); empty without that row. The GGUF stores the edges
// as float32: each is read back as the shortest decimal that round-trips (the checkpoint's value, as the Python servers
// report it).
std::vector<double> speaking_rate_edges(const ServerState & s) {
    const auto & hp = s.model.hp;
    std::vector<double> out;
    for (size_t i = 0; i < hp.cond_names.size() && i < hp.cond_edges.size(); ++i)
        if (hp.cond_names[i] == "speaking_rate")
            for (float v : hp.cond_edges[i]) out.push_back(float_decimal(v));
    return out;
}

// zonos3.server.plugins.HostFacts of the server (rate_extension: the steering bundle's, as
// zonos3.tts.steering.RateExtension).
json host_facts(const ServerState & s) {
    json raw = json::array(); // steering ids that move the speaker embedding
    if (s.steer_loaded)
        for (const auto & d : s.steer.dirs)
            if (!d.hidden) raw.push_back(d.id);
    json extension = nullptr;
    if (s.steer_loaded && s.steer.rate_extension) {
        const auto & e = *s.steer.rate_extension;
        json doses = json::object();
        for (const auto & [lang, by_bucket] : e.doses) {
            json d = json::object();
            for (const auto & [b, dose] : by_bucket) d[std::to_string(b)] = dose;
            doses[lang] = d;
        }
        extension = {{"direction", e.direction}, {"row_edges", e.row_edges}, {"below", e.below}, {"above", e.above}, {"doses", doses}, {"reference_sides", e.reference_sides}};
    }
    int rate_buckets = 0; // the speaking-rate table's bucket count (0 without that row)
    const auto & hp = s.model.hp;
    for (size_t i = 0; i < hp.cond_names.size() && i < hp.cond_bucket_counts.size(); ++i)
        if (hp.cond_names[i] == "speaking_rate") rate_buckets = (int) hp.cond_bucket_counts[i];
    return {{"model_name", fs::path(s.model_path).filename().string()}, {"rate_edges", speaking_rate_edges(s)}, {"rate_buckets", rate_buckets}, {"laci_languages", laci_languages(s)}, {"speaker_modes", checkpoint_speaker_modes(s)}, {"raw_controls", raw}, {"phoneme_tokens", !phoneme_capabilities(s)["phoneme_tokens"].is_null()}, {"rate_extension", extension}, {"worker_device", nullptr}};
}

// -------------------------------------------------------------------------------------------------- engine calls

// One span-end-terminated infill generation through the scheduler: the generated delayed rows (without the finishing
// row) and the aligned eos frame (-1 = none).
struct Generated {
    std::vector<int32_t> rows;
    int eos_frame = -1;
};
Generated generate_fill(ServerState & s, const std::string & text, const std::vector<float> & speaker_embedding,
                        const zonos3_steering & steering, const std::vector<int32_t> & condition_ids,
                        const std::vector<int32_t> & window_codes, const std::vector<E::Span> & window_spans,
                        const std::vector<std::vector<int32_t>> & window_fills, const zonos3_sampling & sp,
                        int max_frames, int min_frames) {
    const auto & hp = s.model.hp;
    const int K = (int) hp.n_codebooks, W = K + 1;
    auto job = std::make_shared<ReqJob>();
    zonos3_prompt_options opt;
    if (!speaker_embedding.empty()) opt.speaker_embedding = speaker_embedding.data();
    opt.condition_buckets = condition_ids;
    opt.silence_prefill_frames = 0;
    std::string err;
    if (!zonos3_build_prompt(s.model, text, opt, job->prompt, &err))
        fail(400, err.empty() ? "could not build the edit prompt" : err);
    E::Layout layout;
    layout.n_codebooks = K;
    layout.audio_pad_id = (int) hp.audio_pad_id;
    layout.text_vocab = (int) hp.text_vocab;
    layout.span_end_id = hp.span_end_id;
    job->prompt.ids = E::edit_prompt_tokens(job->prompt.ids, window_codes, window_spans, window_fills, layout);
    job->prompt.n_rows = (int) (job->prompt.ids.size() / W);
    // The fill runs on the speech-edit adapter (zonos-accent-lora/3), if loaded, at strength 1.
    if (s.accent_adapters && s.accent_adapters->edit >= 0) {
        job->prompt.cond.accent = s.accent_adapters->edit;
        job->prompt.cond.accent_strength = 1.0f;
    }
    const int budget = fit_frame_budget(s, job->prompt.n_rows, max_frames);
    job->idf.assign(job->prompt.ids.begin(), job->prompt.ids.end());
    job->n0 = job->prompt.n_rows;
    GenReq & gr = job->gr;
    gr.accent = job->prompt.cond.accent;
    gr.accent_strength = job->prompt.cond.accent_strength;
    gr.sp = sp;
    gr.max_frames = budget;
    gr.stream = false;
    gr.steering = steering;
    gr.stop_id = hp.span_end_id;
    gr.codes_only = true;
    gr.decoder = &default_decoder(s); // decodes nothing; the scheduler still pins a lane
    submit_job(s, job);
    Generated out;
    {
        std::unique_lock<std::mutex> lk(job->m);
        job->cv.wait(lk, [&] { return job->finished; });
        if (job->failed) fail(500, "generation failed");
        out.rows = job->codes;
        out.eos_frame = job->eos_frame;
    }
    if (out.rows.size() >= (size_t) K) out.rows.resize(out.rows.size() - K); // the finishing row is not emitted
    return out;
}

// The whole decode of ordinary codes [frames, K] on a lane of `decoder`, with the decoder's output gain.
std::vector<float> decode_codes(ServerState & s, Decoder & decoder, const std::vector<int32_t> & frames) {
    const int K = (int) s.model.hp.n_codebooks;
    const int n = (int) (frames.size() / K), H = n + K - 1;
    auto job = std::make_shared<ReqJob>();
    job->codes.assign((size_t) H * K, (int32_t) s.model.hp.audio_pad_id);
    for (int r = 0; r < H; ++r)
        for (int c = 0; c < K; ++c)
            if (r - c >= 0 && r - c < n) job->codes[(size_t) r * K + c] = frames[(size_t) (r - c) * K + c];
    job->gr.decoder = &decoder;
    job->gain = zac_output_gain(decoder.zac, -1.0);
    submit_decode(s, job);
    std::vector<float> audio;
    std::unique_lock<std::mutex> lk(job->m);
    job->cv.wait(lk, [&] { return job->finished; });
    if (job->failed) fail(500, "decoding the edited codes failed");
    for (auto & b : job->pcm) audio.insert(audio.end(), b.begin(), b.end());
    return audio;
}

// EditEngine.decode_audio: the upload as mono float WAV at the API rate at `out`.
json call_decode_audio(const json & c) {
    const int sample_rate = zonos3_generation().sample_rate;
    write_float_wav(c.at("out").get<std::string>(), decode_file(c.at("audio").get<std::string>()), sample_rate);
    return {{"sample_rate", sample_rate}};
}

// EditEngine.embed: the speaker encoder's embedding of the upload.
json call_embed(ServerState & s, const json & c) {
    const int max_seconds = (int) std::ceil(c.at("max_seconds").get<double>());
    return {{"embedding", embed_audio(s, decode_file(c.at("audio").get<std::string>()), max_seconds)}};
}

// EditEngine.encode / Host.encode_reference: the codec's ordinary codes of a sound file (at most max_seconds).
json call_encode(ServerState & s, const json & c) {
    const double max_seconds = c.at("max_seconds").get<double>();
    std::vector<float> pcm = decode_file(c.at("audio").get<std::string>());
    const size_t cap = (size_t) (max_seconds * zonos3_generation().sample_rate);
    if (pcm.size() > cap) pcm.resize(cap);
    return {{"codes", rows_json(encode_audio_codes(s, pcm, (int) std::ceil(max_seconds)), (int) s.model.hp.n_codebooks)}};
}

// EditEngine.check: the request's steer-then-hold fields, the target's text markers (pronunciation groups; no start or
// pause markers), every region's doses against the steering bundle and the condition buckets / values; the dosed ids
// whose direction is raw (moves the speaker embedding), sorted.
json call_check(const ServerState & s, const json & c) {
    resolve_steer_release(s, c);
    const std::string markers =
        zonos3_check_text_markers(s.model.tok, c.at("target_text").get<std::string>(), /*edit=*/true);
    if (!markers.empty()) fail(400, markers);
    std::vector<std::string> raw;
    if (set(c, "doses")) {
        if (!s.steer_loaded)
            fail(400, std::string(NO_STEERING) + "; `controls` are unavailable.");
        for (const auto & region : c["doses"]) {
            const auto doses = region.get<std::map<std::string, double>>();
            std::vector<float> probe(s.steer.speaker_dim, 0.0f);
            zonos3_steering scratch;
            std::string err;
            if (!zonos3_steer_resolve(s.steer, doses, {}, 0, ZONOS3_STEER_FRAMES_PER_CHAR, &probe, scratch, err))
                fail(400, err);
            for (const auto & [id, v] : doses)
                if (!s.steer.dirs[s.steer.index.at(id)].hidden && std::find(raw.begin(), raw.end(), id) == raw.end())
                    raw.push_back(id);
        }
    }
    resolve_condition_buckets(s, c);
    std::sort(raw.begin(), raw.end());
    return {{"raw", raw}};
}

// EditEngine.fill: one region's generation (zonos3.tts.edit_runtime.FillJob).
json call_fill(ServerState & s, const json & c) {
    const auto & hp = s.model.hp;
    const int K = (int) hp.n_codebooks;
    const std::string text = normalize_text(s, c.at("text").get<std::string>(), c.at("language").get<std::string>(),
                                            c.value("text_normalization", true));
    const std::vector<int32_t> condition_ids = resolve_condition_buckets(s, c);
    std::vector<float> speaker_embedding;
    if (set(c, "speaker_embedding")) speaker_embedding = c["speaker_embedding"].get<std::vector<float>>();
    // The region's steering: its speaker-row embedding moved by the raw doses and the hidden delta of the others (with
    // the request's steer-then-hold schedule, counted from the fill's first generated frame).
    const std::optional<zonos3_steer_release_frames> release = resolve_steer_release(s, c);
    zonos3_steering steering;
    if (set(c, "controls")) {
        if (!s.steer_loaded)
            fail(400, std::string(NO_STEERING) + "; `controls` are unavailable.");
        std::string err;
        if (!zonos3_steer_resolve(s.steer, c["controls"].get<std::map<std::string, double>>(), {},
                                  (int) utf8_length(c.value("controls_text", std::string())),
                                  ZONOS3_STEER_FRAMES_PER_CHAR, speaker_embedding.empty() ? nullptr : &speaker_embedding,
                                  steering, err, release ? &*release : nullptr))
            fail(400, err);
    }
    const json & sampling = c.at("sampling");
    zonos3_sampling sp; // the registry's sampling defaults; the call's sampling overrides what it sets
    if (set(sampling, "temperature")) sp.temperature = (float) sampling["temperature"].get<double>();
    if (set(sampling, "topk")) sp.top_k = sampling["topk"].get<int>();
    if (set(sampling, "top_p")) sp.top_p = (float) sampling["top_p"].get<double>();
    if (set(sampling, "min_p")) sp.min_p = (float) sampling["min_p"].get<double>();
    if (set(sampling, "repetition_penalty")) sp.rep_penalty = (float) sampling["repetition_penalty"].get<double>();
    if (set(sampling, "repetition_window")) sp.rep_window = sampling["repetition_window"].get<int>();
    if (set(sampling, "repetition_codebooks")) sp.rep_codebooks = sampling["repetition_codebooks"].get<int>();
    if (sp.rep_codebooks < 0) sp.rep_codebooks = K;
    sp.greedy = sp.temperature <= 0.0f;
    sp.seed = (uint32_t) c.at("seed").get<int64_t>();
    std::vector<E::Span> spans;
    for (const auto & p : c.at("spans")) spans.push_back({p.at(0).get<int>(), p.at(1).get<int>()});
    std::vector<std::vector<int32_t>> fills;
    for (const auto & f : c.at("fills")) fills.push_back(flat_rows(f, K));
    const Generated g = generate_fill(s, text, speaker_embedding, steering, condition_ids,
                                      flat_rows(c.at("window_codes"), K), spans, fills, sp,
                                      std::min(c.at("max_tokens").get<int>(), s.max_frames), c.at("min_frames").get<int>());
    return {{"rows", rows_json(g.rows, K)}, {"eos_frame", g.eos_frame}};
}

// EditEngine.decode: the codes' audio on the edit's decoder (`decoder`: an id of Capabilities.decoders, null = the
// default) with the output gain as float WAV at `out`.
json call_decode(ServerState & s, const json & c) {
    Decoder & decoder = *resolve_decoder(s, c);
    const std::vector<float> audio = decode_codes(s, decoder, flat_rows(c.at("codes"), (int) s.model.hp.n_codebooks));
    write_float_wav(c.at("out").get<std::string>(), audio, decoder.zac.sample_rate);
    return json::object();
}

// Host.synthesize_laci (zonos3.server.plugins.LaciTakeRequest): every take under LACI on the scheduler with its
// duration target and optional extended speaking-rate scale. Every request is planned before the first is queued (400
// for a bad voice / control / text / scale); at most the LACI slots are in flight.
json call_laci_takes(ServerState & s, PyHost & host, const json & c) {
    const std::optional<std::string> session =
        set(c, "session") ? std::optional<std::string>(c["session"].get<std::string>()) : std::nullopt;
    const json & takes_in = c.at("takes");
    std::vector<std::shared_ptr<ReqJob>> planned;
    for (const json & t : takes_in) {
        LaciTake take;
        take.request = t.at("request");
        take.duration.frames = t.at("target_frames").get<double>();
        take.duration.adjust_rate = t.at("adjust_rate").get<bool>();
        if (set(t, "scale")) {
            const json & scale = t["scale"];
            if (!set(scale, "bucket")) fail(400, "an extended speaking-rate scale needs the take's bucket");
            std::map<int, double> doses;
            if (set(scale, "doses"))
                for (auto it = scale["doses"].begin(); it != scale["doses"].end(); ++it)
                    doses[std::stoi(it.key())] = it.value().get<double>();
            const bool extension = s.steer_loaded && s.steer.rate_extension.has_value();
            if (!doses.empty() && !extension)
                fail(400, "a steered speaking-rate bucket needs the steering bundle's rate_extension");
            take.scale = LaciTake::Scale{scale.at("edges").get<std::vector<double>>(), scale.at("first").get<int>(),
                                         scale["bucket"].get<int>(), doses,
                                         extension ? s.steer.rate_extension->direction : std::string()};
        }
        planned.push_back(plan_laci_take(s, take, session));
    }

    std::vector<TakeResult> results(planned.size());
    std::vector<std::exception_ptr> errors(planned.size());
    std::atomic<size_t> next{0};
    std::atomic<bool> failed{false};
    auto run = [&] {
        for (;;) {
            const size_t i = next++;
            if (i >= planned.size()) return;
            if (failed) continue; // a take failed: the rest are never queued
            host.acquire_laci_slot();
            try {
                submit_job(s, planned[i]);
                results[i] = wait_take(planned[i]);
            } catch (...) {
                errors[i] = std::current_exception();
                failed = true;
            }
            host.release_laci_slot();
        }
    };
    const size_t n_threads = std::min(planned.size(), (size_t) std::max(1, 2 * s.batch_slots));
    std::vector<std::thread> threads;
    for (size_t t = 0; t < n_threads; ++t) threads.emplace_back(run);
    for (auto & t : threads) t.join();
    for (auto & e : errors)
        if (e) std::rethrow_exception(e);

    json takes = json::array();
    for (size_t i = 0; i < results.size(); ++i) {
        const bool audio = !results[i].pcm.empty();
        if (audio) write_float_wav(takes_in[i].at("out").get<std::string>(), results[i].pcm, zonos3_generation().sample_rate);
        takes.push_back({{"audio", audio}, {"record", results[i].laci}});
    }
    return {{"takes", takes}};
}

json engine_call(ServerState & s, PyHost & host, const std::string & op, const json & c) {
    if (op == "decode_audio") return call_decode_audio(c);
    if (op == "embed") return call_embed(s, c);
    if (op == "encode") return call_encode(s, c);
    if (op == "check") return call_check(s, c);
    if (op == "fill") return call_fill(s, c);
    if (op == "decode") return call_decode(s, c);
    if (op == "laci_takes") return call_laci_takes(s, host, c);
    throw std::runtime_error("unknown engine call: " + op);
}

const char * const EXITED = "the Python host exited";

} // namespace

// ------------------------------------------------------------------------------------------------------- client

struct PyHost::Run {
    std::unique_ptr<ChildProcess> process;
    std::mutex write_m; // serializes writes and the closing of stdin
    std::mutex m;       // guards replies, exited
    std::condition_variable cv;
    std::map<int64_t, std::optional<json>> replies; // waiting request id -> its reply once read
    bool exited = false;                            // the reader met the end of the host's stdout
    std::vector<std::string> keepalive_paths;       // the plugins' (init reply)
    std::thread reader;

    bool send(const json & message) {
        const std::string line = message.dump(-1, ' ', false, json::error_handler_t::replace) + "\n";
        std::lock_guard<std::mutex> lk(write_m);
        return process->write(line);
    }
    void close_input() {
        std::lock_guard<std::mutex> lk(write_m);
        process->close_input();
    }
};

std::map<std::string, std::optional<std::string>> pyhost_environment(const ServerState & s) {
#ifdef _WIN32
    constexpr char sep = ';';
#else
    constexpr char sep = ':';
#endif
    std::string pythonpath = s.python_src;
    const char * inherited = getenv("PYTHONPATH");
    if (inherited && *inherited) pythonpath += sep + std::string(inherited);
    std::map<std::string, std::optional<std::string>> env = {
        {"PYTHONPATH", pythonpath}, {"PYTHONDONTWRITEBYTECODE", std::string("1")}, {"VIRTUAL_ENV", std::nullopt}};
    if (!getenv("HF_HUB_OFFLINE")) env["HF_HUB_OFFLINE"] = std::string("1");
    return env;
}

PyHost::PyHost(ServerState & s) : s_(s), free_slots_(std::max(1, 2 * s.batch_slots)) {
    argv_ = {s.worker_python, (fs::path(s.python_src) / "zonos3" / "workers" / "pyhost.py").string()};
    env_ = pyhost_environment(s);
}

PyHost::~PyHost() {
    close();
}

void PyHost::acquire_laci_slot() {
    std::unique_lock<std::mutex> lk(slots_m_);
    slots_cv_.wait(lk, [&] { return free_slots_ > 0; });
    --free_slots_;
}

void PyHost::release_laci_slot() {
    {
        std::lock_guard<std::mutex> lk(slots_m_);
        ++free_slots_;
    }
    slots_cv_.notify_one();
}

// The running process, started (and sent `init`) when there is none or it exited.
std::shared_ptr<PyHost::Run> PyHost::ensure_run() {
    if (run_) {
        {
            std::lock_guard<std::mutex> lk(run_->m);
            if (!run_->exited) return run_;
        }
        if (run_->reader.joinable()) run_->reader.join();
        run_.reset();
    }
    auto run = std::make_shared<Run>();
    try {
        run->process = std::make_unique<ChildProcess>(argv_, env_);
    } catch (const WorkerError & e) {
        fail(503, std::string("the Python host cannot start: ") + e.what());
    }
    run->reader = std::thread([this, weak = std::weak_ptr<Run>(run)] { read_loop(weak); });
    try {
        const json reply = exchange(*run, "init", {{"edit", edit_model(s_)}, {"host", host_facts(s_)}, {"plugins", s_.plugins}});
        if (reply.contains("plugins"))
            for (const json & p : reply["plugins"])
                for (const json & path : p.at("keepalive_paths")) run->keepalive_paths.push_back(path.get<std::string>());
    } catch (...) {
        run->close_input(); // the next request starts a fresh host
        run->reader.join();
        throw;
    }
    run_ = run;
    return run_;
}

json PyHost::exchange(Run & run, const std::string & op, json payload) {
    const int64_t id = ++next_id_;
    payload["id"] = id;
    payload["op"] = op;
    {
        std::lock_guard<std::mutex> lk(run.m);
        if (run.exited) fail(503, EXITED);
        run.replies[id];
    }
    run.send(payload); // a broken pipe: the reader meets the exit
    json reply;
    {
        std::unique_lock<std::mutex> lk(run.m);
        auto it = run.replies.find(id);
        run.cv.wait(lk, [&] { return it->second.has_value() || run.exited; });
        const bool answered = it->second.has_value();
        if (answered) reply = std::move(*it->second);
        run.replies.erase(it);
        if (!answered) fail(503, EXITED);
    }
    if (reply.value("status", "") == "ok") return reply.contains("result") ? reply["result"] : json::object();
    const int code = reply.contains("code") && reply["code"].is_number_integer() ? reply["code"].get<int>() : 500;
    throw HttpError(code, {{"detail", reply.contains("message") ? reply["message"] : json("Python host error")}});
}

json PyHost::request(const std::string & op, json payload) {
    std::shared_ptr<Run> run;
    {
        std::lock_guard<std::mutex> lk(start_m_);
        run = ensure_run();
    }
    return exchange(*run, op, std::move(payload));
}

bool PyHost::keepalive(const std::string & path) {
    std::shared_ptr<Run> run;
    {
        std::lock_guard<std::mutex> lk(start_m_);
        run = ensure_run();
    }
    for (const std::string & p : run->keepalive_paths) {
        std::string prefix = p;
        while (!prefix.empty() && prefix.back() == '/') prefix.pop_back();
        if (path == p || path.rfind(prefix + "/", 0) == 0) return true;
    }
    return false;
}

// Routes the host's lines: replies to the waiting requests, engine calls to threads of their own.
void PyHost::read_loop(std::weak_ptr<Run> weak) {
    Run & run = *weak.lock(); // the owner keeps the run alive until it joins this thread
    std::string line;
    while (run.process->read_line(line)) {
        json message = json::parse(line, nullptr, false);
        if (message.is_object() && message.contains("id") && message["id"].is_number_integer()) {
            {
                std::lock_guard<std::mutex> lk(run.m);
                auto it = run.replies.find(message["id"].get<int64_t>());
                if (it != run.replies.end()) it->second = std::move(message);
            }
            run.cv.notify_all();
        } else if (message.is_object() && message.contains("call")) {
            answer(weak.lock(), std::move(message));
        } else {
            fprintf(stderr, "zonos3-server: Python host: unexpected line: %.200s\n", line.c_str());
        }
    }
    run.close_input();
    {
        std::lock_guard<std::mutex> lk(run.m);
        run.exited = true;
    }
    run.cv.notify_all();
}

// An engine call on its own thread: {"call", "status": "ok", ...fields} or {"call", "status": "error", code, message}.
void PyHost::answer(std::shared_ptr<Run> run, json call) {
    {
        std::lock_guard<std::mutex> lk(calls_m_);
        ++calls_;
    }
    std::thread([this, run = std::move(run), call = std::move(call)]() mutable {
        json reply;
        int code = 0;
        json message;
        try {
            reply = engine_call(s_, *this, call.value("op", std::string()), call);
        } catch (const HttpError & e) {
            code = e.status;
            message = e.body.contains("detail") ? e.body["detail"] : json("error");
        } catch (const std::invalid_argument & e) {
            code = 400;
            message = e.what();
        } catch (const std::exception & e) {
            code = 500;
            message = e.what();
        }
        if (code) reply = {{"status", "error"}, {"code", code}, {"message", message.is_string() ? message : json(message.dump())}};
        else reply["status"] = "ok";
        reply["call"] = call["call"];
        run->send(reply);
        run.reset();
        std::lock_guard<std::mutex> lk(calls_m_);
        --calls_;
        calls_cv_.notify_all();
    }).detach();
}

void PyHost::close() {
    {
        std::lock_guard<std::mutex> lk(start_m_);
        if (run_) {
            run_->close_input();
            if (run_->reader.joinable()) run_->reader.join();
        }
    }
    {
        std::unique_lock<std::mutex> lk(calls_m_);
        calls_cv_.wait(lk, [&] { return calls_ == 0; });
    }
    std::lock_guard<std::mutex> lk(start_m_);
    run_.reset(); // waits for the process
}

// ------------------------------------------------------------------------------------------------------- helpers

json edit_model(const ServerState & s) {
    const auto & hp = s.model.hp;
    const int k = (int) hp.n_codebooks;
    return {{"frame_rate", frame_rate(s)}, {"n_codebooks", k}, {"audio_pad_id", (int) hp.audio_pad_id}, {"codebook_sizes", std::vector<int>(k, (int) hp.codebook_size)}, {"max_spans", zonos3_supports_editing(s.model) ? std::max(0, zonos3_edit_marker_rows(s.model)) : 0}, {"speaker_row", hp.speaker_dim > 0}};
}

bool asr_installed(const ServerState & s) {
    const char * py = getenv("ZONOS3_ASR_PYTHON");
    const fs::path python = py && *py ? fs::path(py)
                                      : (fs::path(s.python_src) / ".." / ".." / ".venv-asr" / "bin" / "python").lexically_normal();
    std::error_code ec;
    return fs::is_regular_file(python, ec);
}

} // namespace zs
