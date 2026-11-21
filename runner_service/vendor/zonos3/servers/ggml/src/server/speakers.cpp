#include "compat.h"

#include "server/speakers.h"

#include "server/http.h"
#include "server/schema.h"
#include "server/util.h"

#include "audio-decode.h"
#include "model-paths.h"
#include "npy.h"
#include "wav.h"

#include <algorithm>
#include <cerrno>
#include <cmath>
#include <cstdio>
#include <cstring>
#include <filesystem>

#ifdef _WIN32
#include <process.h>
#else
#include <unistd.h>
#endif

namespace zs {

namespace fs = std::filesystem;
using json = nlohmann::json;

static const char * NO_SESSION = "Cached speaker operations require the X-TTS-Session-ID header.";

// The registry's reference clip limit (seconds of uploaded or default-voice audio).
static int reference_seconds() {
    return (int) zonos3_generation().reference_audio_max_seconds;
}

// R7: reference codes keep their first reference_max_frames frames (the GGUF servers' reference prefix).
static void keep_reference_prefix(const ServerState & s, std::vector<int32_t> & codes) {
    const size_t cap = (size_t) zonos3_generation().reference_max_frames * s.model.hp.n_codebooks;
    if (codes.size() > cap) codes.resize(cap);
}

std::vector<std::string> checkpoint_speaker_modes(const ServerState & s) {
    std::vector<std::string> modes = {"reference"}; // the release architecture carries the raw reference prefix
    if (s.model.hp.speaker_dim > 0) modes.push_back("embedding");
    return modes;
}

bool reference_audio_available(const ServerState & s) {
    return !s.encoder_path.empty() && !s.teacher_path.empty();
}
bool speaker_encoder_available(const ServerState & s) {
    return !s.spk_path.empty();
}

// The modes a voice supports: an embedding voice the speaker row only; an audio voice every checkpoint mode its
// companions (or shipped <stem>.codes.npy / <stem>.embedding.npy) can compute.
static std::vector<std::string> voice_modes(const ServerState & s, const Speaker & sp) {
    std::vector<std::string> out;
    for (const std::string & m : checkpoint_speaker_modes(s)) {
        if (sp.source_type == "embedding") {
            if (m == "embedding") out.push_back(m);
            continue;
        }
        if (m == "embedding" && (speaker_encoder_available(s) || !sp.embedding_path.empty() || !sp.embedding.empty())) out.push_back(m);
        if (m == "reference" && (reference_audio_available(s) || !sp.codes_path.empty() || !sp.codes.empty())) out.push_back(m);
    }
    return out;
}

static json speaker_json(const ServerState & s, const Speaker & sp) {
    auto opt = [](const std::optional<std::string> & v) { return v ? json(*v) : json(nullptr); };
    return {
        {"id", sp.id},
        {"label", sp.label},
        {"source_type", sp.source_type},
        {"scope", sp.is_default ? "default" : "session"},
        {"is_default", sp.is_default},
        {"original_name", opt(sp.original_name)},
        {"created_at", sp.created},
        {"has_preview", sp.is_default ? sp.source_type == "audio" : !sp.pcm.empty()},
        {"speaker_modes", voice_modes(s, sp)},
        {"embedding_dim", s.model.hp.speaker_dim > 0 ? json(s.model.hp.speaker_dim) : json(nullptr)},
        {"transcript", opt(sp.transcript)},
        // steering.disabled_controls: the bundle controls calibrated to dose 0 for this voice.
        {"disabled_controls", s.steer_loaded ? zonos3_steer_disabled_controls(s.steer, sp.id) : std::vector<std::string>{}},
    };
}

// ------------------------------------------------------------------------------------------------ default voices

static bool is_audio_ext(const std::string & ext) {
    static const std::vector<std::string> exts = {".aac", ".flac", ".m4a", ".mp3", ".ogg", ".opus", ".wav", ".webm"};
    return std::find(exts.begin(), exts.end(), lower_ascii(ext)) != exts.end();
}

// zonos3.server.voices.voice_source_type: audio files and .npy/.npz embeddings; an audio voice's .codes.npy and
// .embedding.npy sidecars are no voice.
static const char * default_source_type(const fs::path & p) {
    const std::string name = lower_ascii(p.filename().string());
    if (ends_with(name, ".codes.npy") || ends_with(name, ".embedding.npy")) return nullptr;
    const std::string ext = lower_ascii(p.extension().string());
    if (ext == ".npy" || ext == ".npz") return "embedding";
    if (is_audio_ext(ext)) return "audio";
    return nullptr;
}

static std::string default_label(const fs::path & p) {
    std::string label = p.stem().string();
    for (char & c : label)
        if (c == '_' || c == '-') c = ' ';
    label = strip(label);
    return label.empty() ? p.filename().string() : label;
}

static std::optional<std::string> read_transcript(const fs::path & p) {
    std::string text;
    fs::path txt = p;
    txt.replace_extension(".txt");
    if (!read_file(txt, text)) return std::nullopt;
    text = strip(text);
    if (text.empty()) return std::nullopt;
    return text;
}

static void refresh_default_speakers_locked(ServerState & s) {
    std::map<std::string, Speaker> current;
    if (s.default_voice_dir.empty()) {
        s.default_speakers.clear();
        return;
    }
    std::error_code ec;
    const fs::path given(s.default_voice_dir);
    if (!fs::is_directory(given, ec)) {
        s.default_speakers.clear();
        return;
    }
    const fs::path root = fs::canonical(given, ec);
    if (ec) {
        s.default_speakers.clear();
        return;
    }
    std::vector<fs::path> files;
    for (fs::recursive_directory_iterator it(root, ec), end; !ec && it != end; it.increment(ec))
        if (it->is_regular_file(ec)) files.push_back(it->path());
    std::sort(files.begin(), files.end(), [](const fs::path & a, const fs::path & b) { return a.generic_string() < b.generic_string(); });
    for (const fs::path & path : files) {
        const char * type = default_source_type(path);
        if (!type) continue;
        const fs::path resolved = fs::canonical(path, ec);
        if (ec) continue;
        Speaker sp;
        sp.id = "default_" + sha1_hex(path.lexically_relative(root).generic_string()).substr(0, 16);
        sp.label = default_label(resolved);
        sp.source_type = type;
        sp.path = resolved.string();
        sp.mtime = sp.created = file_mtime(resolved);
        sp.original_name = resolved.filename().string();
        sp.transcript = read_transcript(resolved);
        sp.is_default = true;
        if (sp.source_type == "audio") { // shipped sidecars beside the clip: <stem>.codes.npy, <stem>.embedding.npy
            fs::path codes = resolved, emb = resolved;
            codes.replace_extension(".codes.npy");
            emb.replace_extension(".embedding.npy");
            if (fs::is_regular_file(codes, ec)) {
                sp.codes_path = codes.string();
                sp.codes_mtime = file_mtime(codes);
            }
            if (fs::is_regular_file(emb, ec)) {
                sp.embedding_path = emb.string();
                sp.embedding_mtime = file_mtime(emb);
            }
        }
        auto old = s.default_speakers.find(sp.id);
        if (old != s.default_speakers.end() && old->second.path == sp.path && old->second.mtime == sp.mtime &&
            old->second.source_type == sp.source_type && old->second.codes_path == sp.codes_path &&
            old->second.codes_mtime == sp.codes_mtime && old->second.embedding_path == sp.embedding_path &&
            old->second.embedding_mtime == sp.embedding_mtime) {
            old->second.transcript = sp.transcript;
            current[sp.id] = std::move(old->second);
            continue;
        }
        current[sp.id] = std::move(sp);
    }
    s.default_speakers.swap(current);
}

// ------------------------------------------------------------------------------------------------ embedding files

namespace {

uint32_t le32(const uint8_t * p) {
    return p[0] | (uint32_t) p[1] << 8 | (uint32_t) p[2] << 16 | (uint32_t) p[3] << 24;
}
uint16_t le16(const uint8_t * p) {
    return (uint16_t) (p[0] | p[1] << 8);
}

// The stored (uncompressed, numpy.savez) members of a zip archive: name -> payload.
bool parse_npz(const std::vector<uint8_t> & z, std::vector<std::pair<std::string, std::vector<uint8_t>>> & members,
               std::string & why) {
    if (z.size() < 22) {
        why = "not a zip archive";
        return false;
    }
    size_t eocd = std::string::npos;
    for (size_t i = z.size() - 22 + 1; i-- > 0 && z.size() - i <= 22 + 65535;)
        if (le32(&z[i]) == 0x06054b50) {
            eocd = i;
            break;
        }
    if (eocd == std::string::npos) {
        why = "not a zip archive";
        return false;
    }
    const size_t entries = le16(&z[eocd + 10]);
    size_t cd = le32(&z[eocd + 16]);
    for (size_t e = 0; e < entries; ++e) {
        if (cd + 46 > z.size() || le32(&z[cd]) != 0x02014b50) {
            why = "corrupt zip directory";
            return false;
        }
        const uint16_t method = le16(&z[cd + 10]);
        const uint32_t csize = le32(&z[cd + 20]);
        const uint16_t name_len = le16(&z[cd + 28]), extra_len = le16(&z[cd + 30]), comment_len = le16(&z[cd + 32]);
        const uint32_t local = le32(&z[cd + 42]);
        const std::string name((const char *) &z[cd + 46], name_len);
        cd += 46 + name_len + extra_len + comment_len;
        if (local + 30 > z.size() || le32(&z[local]) != 0x04034b50) {
            why = "corrupt zip entry";
            return false;
        }
        const size_t data = local + 30 + le16(&z[local + 26]) + le16(&z[local + 28]);
        if (method != 0) {
            why = "compressed .npz archives (numpy.savez_compressed) are not supported; use numpy.savez or .npy";
            return false;
        }
        if (data + csize > z.size()) {
            why = "truncated zip entry";
            return false;
        }
        members.emplace_back(name, std::vector<uint8_t>(z.begin() + data, z.begin() + data + csize));
    }
    return true;
}

} // namespace

// zonos3.reference.synth.load_embedding_vector: a .npy, or a .npz with an `emb` array (or exactly one array); squeezed; (D,) or (N, D)
// averaged over rows; dimension checked. Throws HttpError 400.
static std::vector<float> load_embedding_vector(const std::vector<uint8_t> & bytes, int expected_dim,
                                                const std::optional<std::string> & file_name) {
    const std::string desc = file_name && !file_name->empty() ? *file_name : "Speaker embedding file";
    const std::string invalid = desc + " must be a valid .npy or .npz file saved without pickle.";
    npy::Array arr;
    std::string why;
    if (bytes.size() >= 4 && bytes[0] == 'P' && bytes[1] == 'K') {
        std::vector<std::pair<std::string, std::vector<uint8_t>>> members;
        if (!parse_npz(bytes, members, why)) fail(400, invalid + " (" + why + ")");
        const std::vector<uint8_t> * pick = nullptr;
        for (auto & [name, payload] : members)
            if (name == "emb.npy") pick = &payload;
        if (!pick && members.size() == 1) pick = &members[0].second;
        if (!pick) fail(400, "Embedding archive must contain an 'emb' array or exactly one array.");
        if (!npy::parse(pick->data(), pick->size(), arr, why)) fail(400, invalid);
    } else if (!npy::parse(bytes.data(), bytes.size(), arr, why)) {
        fail(400, invalid);
    }
    std::vector<int64_t> shape;
    for (int64_t d : arr.shape)
        if (d != 1) shape.push_back(d);
    std::vector<float> vec;
    if (shape.size() == 1) {
        vec.assign(arr.data.begin(), arr.data.end());
    } else if (shape.size() == 2) {
        if (shape[0] == 0) fail(400, "Speaker embedding file is empty.");
        const int64_t rows = shape[0], dim = shape[1];
        vec.assign((size_t) dim, 0.0f);
        for (int64_t j = 0; j < dim; ++j) {
            float acc = 0.0f; // numpy float32 mean: accumulate in float32
            for (int64_t i = 0; i < rows; ++i) acc += (float) arr.data[(size_t) (i * dim + j)];
            vec[(size_t) j] = acc / (float) rows;
        }
    } else {
        std::string sh = "(";
        for (size_t i = 0; i < shape.size(); ++i) sh += (i ? ", " : "") + std::to_string(shape[i]);
        if (shape.size() == 1) sh += ",";
        fail(400, "Unsupported embedding array shape " + sh + "). Expected (D,) or (N, D).");
    }
    if ((int) vec.size() != expected_dim)
        fail(400, "Reference embedding dimension mismatch. Model expects " + std::to_string(expected_dim) +
                      ", but loaded file has " + std::to_string(vec.size()) + ".");
    return vec;
}

// ------------------------------------------------------------------------------------------------ native encoders

static FILE * open_temp_file(const char * suffix, fs::path & path) {
    static const auto dir = fs::temp_directory_path();
#ifdef _WIN32
    static const std::string prefix = "zonos3-" + std::to_string(_getpid()) + "-";
#else
    static const std::string prefix = "zonos3-" + std::to_string(getpid()) + "-";
#endif
    // Processes share the temporary directory; exclusive creation also protects against files left behind before a
    // PID was reused.
    for (;;) {
        path = dir / (prefix + random_hex(12) + suffix);
        FILE * f = fopen(path.string().c_str(), "wbx");
        if (f || errno != EEXIST) return f;
    }
}

// Uploaded audio (any container ffmpeg reads) -> 44.1 kHz mono PCM (at most max_seconds).
std::vector<float> decode_audio_bytes(const std::vector<uint8_t> & bytes, int max_seconds) {
    fs::path tmp;
    FILE * f = open_temp_file(".audio", tmp);
    if (!f) fail(500, "cannot write a temporary audio file");
    const bool ok = fwrite(bytes.data(), 1, bytes.size(), f) == bytes.size();
    const bool closed = fclose(f) == 0;
    std::vector<float> pcm;
    if (ok && closed) pcm = audio_decode_file(tmp.string().c_str(), zonos3_generation().sample_rate, max_seconds);
    std::error_code ec;
    fs::remove(tmp, ec);
    if (!ok || !closed) fail(500, "failed to write a temporary audio file");
    if (pcm.empty())
        fail(400, "could not decode the reference audio (any container ffmpeg reads, at most " + std::to_string(max_seconds) +
                      " seconds)");
    return pcm;
}

std::vector<uint8_t> decode_b64_field(const json & req, const char * field) {
    const std::string raw = req[field].get<std::string>();
    std::vector<uint8_t> out;
    const std::string payload = strip(raw);
    if (payload.rfind("data:", 0) == 0 && payload.find(',') == std::string::npos)
        fail(400, std::string("Invalid data URL for ") + field + ".");
    if (!b64_decode_strict(raw, out)) fail(400, std::string(field) + " must be valid base64-encoded data.");
    return out;
}

// Under reference_compute_mtx: both native allocators are non-reentrant. At most max_seconds of audio, so at most
// ceil(max_seconds * frame rate) + 1 frames of codes.
static void encode_reference_locked(ServerState & s, const std::vector<float> & pcm, std::vector<int32_t> & codes,
                                    int max_seconds) {
    const int sample_rate = zonos3_generation().sample_rate;
    const size_t max_frames = (size_t) std::ceil(max_seconds * ZONOS3_FRAME_RATE) + 1;
    if (!reference_audio_available(s))
        fail(400, "reference-mode cloning of audio requires zac-encoder.gguf and ssl-teacher.gguf; supply --zac-encoder and --ssl-teacher");
    if (!s.reference_models_loaded) {
        if (!ssl_load(s.teacher, s.teacher_path.c_str(), s.use_gpu) ||
            !zac_encoder_load(s.encoder, s.encoder_path.c_str(), s.use_gpu)) {
            zac_encoder_free(s.encoder);
            ssl_free(s.teacher);
            fail(500, "failed to load the codec encoder or SSL teacher companion");
        }
        s.reference_models_loaded = true;
    }
    s.reference_last_use = std::chrono::steady_clock::now();
    if (pcm.empty() || pcm.size() > (size_t) max_seconds * sample_rate || !zac_encode(s.encoder, s.teacher, pcm.data(), (int) pcm.size(), codes) ||
        codes.empty() || codes.size() % 10 || codes.size() > max_frames * 10) {
        codes.clear();
        fail(400, "reference audio encoding failed (maximum " + std::to_string(max_seconds) + " seconds)");
    }
}

static void embed_speaker_locked(ServerState & s, const std::vector<float> & pcm, std::vector<float> & embedding,
                                 int max_seconds = reference_seconds()) {
    const int sample_rate = zonos3_generation().sample_rate;
    if (!speaker_encoder_available(s))
        fail(400, "speaker-embedding cloning of audio requires spk-encoder.gguf; supply --spk-encoder");
    if (!s.spk_loaded) {
        if (!spk_load(s.spk, s.spk_path.c_str(), s.use_gpu)) {
            spk_free(s.spk);
            fail(500, "failed to load the speaker encoder companion");
        }
        s.spk_loaded = true;
    }
    if (pcm.empty() || pcm.size() > (size_t) max_seconds * sample_rate ||
        !spk_embed(s.spk, pcm.data(), (int64_t) pcm.size(), sample_rate, embedding) ||
        embedding.size() != (size_t) s.model.hp.speaker_dim) {
        embedding.clear();
        fail(400, "speaker embedding computation failed (maximum " + std::to_string(max_seconds) + " seconds)");
    }
    for (float v : embedding)
        if (!std::isfinite(v)) {
            embedding.clear();
            fail(400, "speaker embedding computation failed");
        }
}

std::vector<int32_t> encode_audio_codes(ServerState & s, const std::vector<float> & pcm, int max_seconds) {
    std::vector<int32_t> codes;
    std::lock_guard<std::mutex> compute(s.reference_compute_mtx);
    encode_reference_locked(s, pcm, codes, max_seconds);
    return codes;
}

std::vector<float> embed_audio(ServerState & s, const std::vector<float> & pcm, int max_seconds) {
    std::vector<float> embedding;
    std::lock_guard<std::mutex> compute(s.reference_compute_mtx);
    embed_speaker_locked(s, pcm, embedding, max_seconds);
    return embedding;
}

// Frees the ~1.6 GB reference encoder + SSL teacher once idle; the next encode reloads them. try_lock: never delays
// an encode that is starting or running.
static void reference_reaper_loop(ServerState & s) {
    std::unique_lock<std::mutex> lk(s.reference_reaper_mtx);
    while (!s.stop.load()) {
        s.reference_cv.wait_for(lk, std::chrono::seconds(1));
        if (s.stop.load()) break;
        std::unique_lock<std::mutex> compute(s.reference_compute_mtx, std::try_to_lock);
        if (!compute.owns_lock() || !s.reference_models_loaded) continue;
        if (std::chrono::steady_clock::now() - s.reference_last_use < std::chrono::seconds(s.reference_idle_unload_s)) continue;
        zac_encoder_free(s.encoder);
        ssl_free(s.teacher);
        s.reference_models_loaded = false;
        fprintf(stderr, "zonos3-server: reference encoder unloaded after %ds idle\n", s.reference_idle_unload_s);
    }
}

void start_reference_reaper(ServerState & s) {
    if (s.reference_idle_unload_s > 0) s.reference_reaper = std::thread(reference_reaper_loop, std::ref(s));
}

void stop_reference_reaper(ServerState & s) {
    { std::lock_guard<std::mutex> lk(s.reference_reaper_mtx); }
    s.reference_cv.notify_all();
    if (s.reference_reaper.joinable()) s.reference_reaper.join();
}

// ------------------------------------------------------------------------------------------------ voice lookup

// A copy of voice `id` (default voices first, then the session's), or HttpError 400 like sgl _get_cached_speaker.
static Speaker lookup_voice(ServerState & s, const std::optional<std::string> & session, const std::string & id) {
    std::lock_guard<std::mutex> lk(s.reference_mtx);
    refresh_default_speakers_locked(s);
    auto d = s.default_speakers.find(id);
    if (d != s.default_speakers.end()) return d->second;
    if (!session) fail(400, NO_SESSION);
    auto sit = s.sessions.find(*session);
    if (sit == s.sessions.end() || !sit->second.count(id)) fail(400, "Unknown cached speaker id '" + id + "'.");
    return sit->second.at(id);
}

bool speaker_known(ServerState & s, const std::optional<std::string> & session, const std::string & id) {
    std::lock_guard<std::mutex> lk(s.reference_mtx);
    refresh_default_speakers_locked(s);
    if (s.default_speakers.count(id)) return true;
    if (!session) return false;
    auto sit = s.sessions.find(*session);
    return sit != s.sessions.end() && sit->second.count(id);
}

// Store what a lookup computed back on the voice (if it is still the same voice).
static void remember(ServerState & s, const std::optional<std::string> & session, const Speaker & v) {
    std::lock_guard<std::mutex> lk(s.reference_mtx);
    Speaker * dst = nullptr;
    if (v.is_default) {
        auto it = s.default_speakers.find(v.id);
        if (it != s.default_speakers.end() && it->second.path == v.path && it->second.mtime == v.mtime) dst = &it->second;
    } else if (session) {
        auto sit = s.sessions.find(*session);
        if (sit != s.sessions.end()) {
            auto it = sit->second.find(v.id);
            if (it != sit->second.end()) dst = &it->second;
        }
    }
    if (!dst) return;
    if (dst->codes.empty()) dst->codes = v.codes;
    if (dst->embedding.empty()) dst->embedding = v.embedding;
    if (dst->pcm.empty()) dst->pcm = v.pcm;
}

static void ensure_pcm(Speaker & v) {
    if (!v.pcm.empty() || !v.is_default) return;
    v.pcm = audio_decode_file(v.path.c_str(), zonos3_generation().sample_rate, reference_seconds());
    if (v.pcm.empty()) fail(400, "Failed to read default voice '" + v.label + "'.");
}

// The voice's speaker embedding (sgl _resolve_cached_speaker_embedding): an embedding file's vector, an audio voice's
// shipped <stem>.embedding.npy (else, or when it does not load, the speaker encoder's embedding of the clip), once.
static std::vector<float> voice_embedding(ServerState & s, const std::optional<std::string> & session, const std::string & id) {
    Speaker v = lookup_voice(s, session, id);
    const int dim = (int) s.model.hp.speaker_dim;
    if (v.embedding.empty()) {
        if (v.source_type == "embedding") { // a default embedding file
            std::string raw;
            if (!read_file(v.path, raw)) fail(400, "Failed to read default voice '" + v.label + "'.");
            v.embedding = load_embedding_vector(std::vector<uint8_t>(raw.begin(), raw.end()), dim, v.original_name);
        } else {
            if (!v.embedding_path.empty()) {
                std::string raw;
                try {
                    if (!read_file(v.embedding_path, raw)) throw std::runtime_error("unreadable");
                    v.embedding = load_embedding_vector(std::vector<uint8_t>(raw.begin(), raw.end()), dim, std::nullopt);
                } catch (const std::exception & e) {
                    const auto * http = dynamic_cast<const HttpError *>(&e);
                    fprintf(stderr, "zonos3-server: cannot load shipped embedding %s (%s); embedding %s\n",
                            v.embedding_path.c_str(), http ? http->body.value("detail", std::string()).c_str() : e.what(),
                            v.path.c_str());
                    v.embedding.clear();
                }
            }
            if (v.embedding.empty()) {
                std::lock_guard<std::mutex> compute(s.reference_compute_mtx);
                ensure_pcm(v);
                embed_speaker_locked(s, v.pcm, v.embedding);
            }
        }
        remember(s, session, v);
    }
    if ((int) v.embedding.size() != dim)
        fail(400, "Cached speaker '" + v.label + "' has " + std::to_string(v.embedding.size()) +
                      " dimensions, but the current model expects " + std::to_string(dim) + ".");
    return v.embedding;
}

// The voice's reference codes (sgl _resolve_speaker_reference_audio + the frontend's codec encode): shipped
// <stem>.codes.npy, else the codec encoder's codes of the clip, computed once; the first reference_max_frames frames.
static std::vector<int32_t> voice_codes(ServerState & s, const std::optional<std::string> & session, const std::string & id) {
    Speaker v = lookup_voice(s, session, id);
    if (v.source_type != "audio") {
        if (v.is_default) fail(400, "Default voice '" + v.label + "' is an embedding file; this checkpoint needs reference audio.");
        fail(400, "Cached speaker '" + v.label + "' has no reference audio.");
    }
    if (v.codes.empty()) {
        std::string err;
        if (!v.codes_path.empty() &&
            !mp_load_reference_codes(v.codes_path, v.codes, err, zonos3_generation().reference_max_frames)) {
            fprintf(stderr, "zonos3-server: cannot load shipped codes %s (%s); encoding %s\n",
                    v.codes_path.c_str(), err.c_str(), v.path.c_str());
            v.codes.clear();
        }
        if (v.codes.empty()) {
            std::lock_guard<std::mutex> compute(s.reference_compute_mtx);
            ensure_pcm(v);
            encode_reference_locked(s, v.pcm, v.codes, reference_seconds());
            keep_reference_prefix(s, v.codes);
        }
        remember(s, session, v);
    }
    return v.codes;
}

// zonos3.reference.synth.slerp (float32), as the Python servers blend.
static std::vector<float> slerp(const std::vector<float> & v0, const std::vector<float> & v1, double t) {
    if (v0.size() != v1.size())
        fail(400, "Cannot blend speaker embeddings with different shapes (" + std::to_string(v0.size()) + ",) and (" +
                      std::to_string(v1.size()) + ",).");
    if (t < 0.0 || t > 1.0) fail(400, "speaker_blend_t must be between 0.0 and 1.0.");
    const float blend = (float) t;
    float n0 = 0, n1 = 0;
    for (float x : v0) n0 += x * x;
    for (float x : v1) n1 += x * x;
    n0 = std::sqrt(n0) + 1e-8f;
    n1 = std::sqrt(n1) + 1e-8f;
    float dot = 0;
    for (size_t i = 0; i < v0.size(); ++i) dot += (v0[i] / n0) * (v1[i] / n1);
    const float omega = std::acos(std::min(1.0f, std::max(-1.0f, dot)));
    std::vector<float> out(v0.size());
    if (omega < 1e-6f) {
        for (size_t i = 0; i < out.size(); ++i) out[i] = (1.0f - blend) * v0[i] + blend * v1[i];
        return out;
    }
    const float so = std::sin(omega);
    const float a = std::sin((1.0f - blend) * omega) / so, b = std::sin(blend * omega) / so;
    for (size_t i = 0; i < out.size(); ++i) out[i] = a * v0[i] + b * v1[i];
    return out;
}

static bool set(const json & req, const char * key) {
    return req.contains(key) && !req[key].is_null();
}
static bool nonempty(const json & req, const char * key) { // Python truthiness of an optional string
    return set(req, key) && !req[key].get<std::string>().empty();
}
static std::optional<std::string> opt_str(const json & req, const char * key) {
    if (!set(req, key)) return std::nullopt;
    return req[key].get<std::string>();
}

ResolvedSpeaker resolve_request_speaker(ServerState & s, const json & req, const std::optional<std::string> & session,
                                        const std::string & mode) {
    ResolvedSpeaker out;
    const bool audio = nonempty(req, "speaker_audio_base64"), emb_file = nonempty(req, "speaker_embedding_base64");
    const bool id = nonempty(req, "speaker_embedding_id");
    const bool blend_a = nonempty(req, "speaker_blend_embedding_id_a"), blend_b = nonempty(req, "speaker_blend_embedding_id_b");
    const bool codes = set(req, "speaker_reference_codes");
    if (mode == "reference") { // sgl _resolve_speaker_reference_audio
        if (emb_file || blend_a || blend_b)
            fail(400, "This checkpoint conditions on raw reference audio and does not accept speaker embedding files or embedding blends.");
        if (codes) {
            if (audio || id) fail(400, "Provide either speaker_reference_codes or a reference clip (audio or a speaker id), not both.");
            // R7 (zonos3.tts.plan.reference_codes): the shape, then non-negative ids, then codec ids, over every frame;
            // a reference longer than the prefix keeps its first frames.
            const json & rows = req["speaker_reference_codes"];
            const size_t ncb = s.model.hp.n_codebooks;
            const int64_t codebook_size = s.model.hp.codebook_size;
            if (rows.empty()) fail(400, "speaker_reference_codes must be a non-empty [frames, codebooks] list");
            for (const json & row : rows)
                if (!row.is_array() || row.size() != ncb)
                    fail(400, "speaker_reference_codes must be a non-empty [frames, codebooks] list");
            for (const json & row : rows)
                for (const json & c : row)
                    if (c.get<int64_t>() < 0) fail(400, "speaker_reference_codes must be non-negative");
            out.codes.reserve(rows.size() * ncb);
            for (const json & row : rows)
                for (const json & c : row) {
                    const int64_t v = c.get<int64_t>();
                    if (v >= codebook_size)
                        fail(400, "speaker_reference_codes must be codec ids in [0, " + std::to_string(codebook_size - 1) + "]");
                    out.codes.push_back((int32_t) v);
                }
            keep_reference_prefix(s, out.codes);
            out.mode = mode;
            return out;
        }
        if (id) {
            if (audio) fail(400, "Provide either a reference audio file or a cached speaker id, not both.");
            out.codes = voice_codes(s, session, req["speaker_embedding_id"].get<std::string>());
        } else if (audio) {
            const std::vector<float> pcm = decode_audio_bytes(decode_b64_field(req, "speaker_audio_base64"));
            std::lock_guard<std::mutex> compute(s.reference_compute_mtx);
            encode_reference_locked(s, pcm, out.codes, reference_seconds());
            keep_reference_prefix(s, out.codes);
        } else {
            return out;
        }
        out.mode = mode;
        return out;
    }
    if (mode != "embedding") return out;
    if (codes) fail(400, "speaker_reference_codes need speaker_mode=reference.");
    // sgl _resolve_speaker_embedding
    if (audio && emb_file) fail(400, "Provide either a reference audio file or a saved embedding file, not both.");
    const int selected = (int) (audio || emb_file) + (int) id + (int) (blend_a || blend_b);
    if (selected == 0) return out;
    if (selected > 1)
        fail(400, "Provide speaker conditioning via upload, one cached speaker id, or two cached ids for blending.");
    const int dim = (int) s.model.hp.speaker_dim;
    if (blend_a || blend_b) {
        if (!blend_a || !blend_b) fail(400, "Provide both cached speaker ids to blend between two speakers.");
        const auto a = voice_embedding(s, session, req["speaker_blend_embedding_id_a"].get<std::string>());
        const auto b = voice_embedding(s, session, req["speaker_blend_embedding_id_b"].get<std::string>());
        out.embedding = slerp(a, b, set(req, "speaker_blend_t") ? req["speaker_blend_t"].get<double>() : 0.5);
    } else if (id) {
        out.embedding = voice_embedding(s, session, req["speaker_embedding_id"].get<std::string>());
    } else if (emb_file) {
        out.embedding = load_embedding_vector(decode_b64_field(req, "speaker_embedding_base64"), dim,
                                              opt_str(req, "speaker_embedding_name"));
    } else {
        const std::vector<float> pcm = decode_audio_bytes(decode_b64_field(req, "speaker_audio_base64"));
        std::lock_guard<std::mutex> compute(s.reference_compute_mtx);
        embed_speaker_locked(s, pcm, out.embedding);
    }
    out.mode = mode;
    return out;
}

// ------------------------------------------------------------------------------------------------ routes

// zonos3.server.voices.speaker_label: the explicit label, else the file name's stem, else the fallback.
static std::string upload_label(const std::optional<std::string> & label, const std::optional<std::string> & file_name,
                                const char * fallback) {
    if (label && !strip(*label).empty()) return strip(*label);
    if (file_name && !file_name->empty()) {
        const std::string stem = strip(fs::path(*file_name).stem().string());
        if (!stem.empty()) return stem;
    }
    return fallback;
}

static void handle_create(ServerState & s, const httplib::Request & req, httplib::Response & res) {
    const json body = schema::parse_body(req, "SpeakerCreateRequest");
    const auto session = session_id(req);
    if (!session) fail(400, NO_SESSION);
    Speaker sp;
    sp.is_default = false;
    sp.created = unix_now();
    sp.id = "spk_" + random_hex(12);
    const bool audio = nonempty(body, "speaker_audio_base64");
    if (nonempty(body, "speaker_embedding_base64")) {
        if (audio) fail(400, "Provide either a reference audio file or a saved embedding file, not both.");
        sp.original_name = opt_str(body, "speaker_embedding_name");
        sp.embedding = load_embedding_vector(decode_b64_field(body, "speaker_embedding_base64"),
                                             (int) s.model.hp.speaker_dim, sp.original_name);
        sp.source_type = "embedding";
        sp.label = upload_label(opt_str(body, "label"), sp.original_name, "Speaker embedding");
    } else {
        if (!audio) fail(400, "Provide an audio file to cache as the speaker reference.");
        if (!speaker_encoder_available(s) && !reference_audio_available(s))
            fail(400, "audio cloning requires spk-encoder.gguf, or zac-encoder.gguf and ssl-teacher.gguf");
        sp.original_name = opt_str(body, "speaker_audio_name");
        sp.pcm = decode_audio_bytes(decode_b64_field(body, "speaker_audio_base64"));
        sp.source_type = "audio";
        sp.label = upload_label(opt_str(body, "label"), sp.original_name, "Speaker audio");
    }
    json out = speaker_json(s, sp);
    {
        std::lock_guard<std::mutex> lk(s.reference_mtx);
        s.sessions[*session][sp.id] = std::move(sp);
    }
    send_json(res, out);
}

static void handle_list(ServerState & s, const httplib::Request & req, httplib::Response & res) {
    const auto session = session_id(req);
    json arr = json::array();
    std::lock_guard<std::mutex> lk(s.reference_mtx);
    refresh_default_speakers_locked(s);
    std::vector<const Speaker *> defaults, cached;
    for (const auto & kv : s.default_speakers) defaults.push_back(&kv.second);
    std::stable_sort(defaults.begin(), defaults.end(), [](const Speaker * a, const Speaker * b) {
        return lower_ascii(a->label) < lower_ascii(b->label);
    });
    for (const Speaker * sp : defaults) arr.push_back(speaker_json(s, *sp));
    if (session) {
        auto it = s.sessions.find(*session);
        if (it != s.sessions.end())
            for (const auto & kv : it->second) cached.push_back(&kv.second);
        std::stable_sort(cached.begin(), cached.end(), [](const Speaker * a, const Speaker * b) { return a->created > b->created; });
        for (const Speaker * sp : cached) arr.push_back(speaker_json(s, *sp));
    }
    send_json(res, json{{"speakers", arr}});
}

static void handle_preview(ServerState & s, const httplib::Request & req, httplib::Response & res) {
    const std::string id = req.matches[1];
    const auto session = session_id(req);
    Speaker v = lookup_voice(s, session, id);
    if (v.is_default && v.source_type != "audio") fail(400, "Default embedding speakers do not include preview audio.");
    if (v.is_default) {
        ensure_pcm(v);
        remember(s, session, v);
    }
    if (v.pcm.empty()) fail(404, "Cached speaker preview audio is not available.");
    const std::vector<uint8_t> wav = wav_pcm16_bytes(v.pcm, zonos3_generation().sample_rate);
    res.set_content((const char *) wav.data(), wav.size(), "audio/wav");
}

static void handle_embedding(ServerState & s, const httplib::Request & req, httplib::Response & res) {
    const std::string id = req.matches[1];
    if (s.model.hp.speaker_dim == 0) fail(404, "No speaker embedding for " + py_repr(id) + ".");
    const std::vector<float> emb = voice_embedding(s, session_id(req), id);
    json values = json::array();
    for (float x : emb) values.push_back(std::round((double) x * 1e6) / 1e6);
    send_json(res, json{{"id", id}, {"embedding", values}});
}

void register_speaker_routes(httplib::Server & svr, ServerState & s) {
    svr.Get("/v2/speakers", guarded([&s](const httplib::Request & q, httplib::Response & r) { handle_list(s, q, r); }));
    svr.Post("/v2/speakers", guarded([&s](const httplib::Request & q, httplib::Response & r) { handle_create(s, q, r); }));
    svr.Get(R"(/v2/speakers/([^/]+)/preview)",
            guarded([&s](const httplib::Request & q, httplib::Response & r) { handle_preview(s, q, r); }));
    svr.Get(R"(/v2/speakers/([^/]+)/embedding)",
            guarded([&s](const httplib::Request & q, httplib::Response & r) { handle_embedding(s, q, r); }));
}

} // namespace zs
