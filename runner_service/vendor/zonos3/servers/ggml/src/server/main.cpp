// zonos3-server: the HTTP API v2 (servers/api/openapi.yaml) over the native GGUF backbone, ZAC decoder and companions, and the
// studio at /. Module layout (src/server/):
//   main.cpp          flags, model/companion loading, route registration, the accept loop (zonos3_server_main)
//   http.*            {"detail"} errors, CORS, ZONOS3_ACCESS_TOKEN, X-Keepalive, X-TTS-Session-ID, studio serving
//   schema.*          request validation against the embedded servers/api/openapi.json (422 ValidationError)
//   scheduler.*       continuous batching (backbone worker) + ZAC decode lanes (prefix / whole decode)
//   speakers.*        default + session voices, speaker prompts of requests, /v2/speakers*
//   generate.*        /v2/generate, /v1/audio/speech
//   capabilities.*    /v2/capabilities, /v2/controls, /v2/health, /v1/models
//   projects.*        /v2/projects*, /v2/media/* (port of model/src/zonos3/server/projects.py)
//   phonemize.*, edit.*          their routes and capabilities
//   pyhost.*                     the Python host (model/src/zonos3/workers/pyhost.py) and its engine calls
//   plugins.*                    --plugins routes, forwarded to the Python host
//   workers.*                    helper processes (model/src/zonos3/workers/*.py)
//   textnorm.*                   text normalization (third_party/zyphra-textnorm, in process)
#include "compat.h"

#include "server/capabilities.h"
#include "server/edit.h"
#include "server/generate.h"
#include "server/http.h"
#include "server/phonemize.h"
#include "server/plugins.h"
#include "server/projects.h"
#include "server/scheduler.h"
#include "server/speakers.h"
#include "server/state.h"
#include "server/util.h"
#include "server/workers.h"

#include "exe-path.h"
#include "model-paths.h"
#include "server-embed.h"

#include "httplib.h"

#include <algorithm>
#include <cstdio>
#include <cstdlib>
#include <string>
#include <vector>

#ifndef _WIN32
#include <signal.h>
#endif

namespace {

using namespace zs;

void usage(const char * a0) {
    fprintf(stderr,
            "usage: %s <model.gguf> [--zac <zac.gguf>]... [--spk-encoder <spk-encoder.gguf>] [--zac-encoder <encoder.gguf>] [--ssl-teacher <teacher.gguf>] [options]\n"
            "  (zac-keep2.gguf (the default keep2 decoder) and zac.gguf (the full-size one, an extra decoder),\n"
            "   spk-encoder.gguf, zac-encoder.gguf, ssl-teacher.gguf, steer.gguf and accents.gguf are\n"
            "   auto-detected next to <model.gguf> when their flags are omitted)\n"
            "  --zac PATH          a decoder (repeatable): the first is the default, every one a request may pick by\n"
            "                      its id (`decoder`: the GGUF's head variant, keep2_plain | full_mr; duplicates are\n"
            "                      an error). Omitted: zac-keep2.gguf (required) and zac.gguf (if present) next to\n"
            "                      <model.gguf>\n"
            "  --spk-encoder PATH  speaker encoder for speaker-embedding cloning of audio voices (optional;\n"
            "                      raw-reference cloning needs --zac-encoder + --ssl-teacher instead)\n"
            "  --host H            bind address (default 127.0.0.1)\n"
            "  --port P            port (default 1919; 0 picks a free ephemeral port)\n"
            "  --gpu | --cpu       backend (default: gpu on Apple Silicon, cpu elsewhere)\n"
            "  --zac-cpu           run the ZAC decoders on CPU even with --gpu (isolates the backbone graph)\n"
            "  --zac-gpu           run the ZAC decoders on the --gpu backend (default)\n"
            "  --max N             max frames per request (default %d, ~%ds; shrunk to the slot rows a prompt leaves)\n"
            "  --batch N           continuous-batching width / concurrent syntheses (default 8). A decode-graph\n"
            "                      ladder runs the narrowest width covering the active slots; cache/state memory\n"
            "                      scales with slots and context capacity.\n"
            "  --zac-threads N     ZAC decode pool lanes per decoder, parallel decode across requests (default 4)\n"
            "  --reference-idle-unload N\n"
            "                      free the reference encoder + SSL teacher after N idle seconds\n"
            "                      (default 60; 0 keeps them loaded once used)\n"
            "  --stream-decode-default prefix|whole\n"
            "                      streaming decode of requests without stream_decode (default prefix: chunks\n"
            "                      ramping from ZONOS3_PREFIX_STREAM_FIRST_CHUNK_FRAMES=1, doubling, up to\n"
            "                      ZONOS3_PREFIX_STREAM_CHUNK_FRAMES=15, behind ZONOS3_PREFIX_STREAM_HOLD_BACK_FRAMES=0;\n"
            "                      whole: one exact chunk at EOS)\n"
            "  --tts-default-voices-dir DIR\n"
            "                      default voices: audio files (+ <stem>.codes.npy reference codes and <stem>.embedding.npy\n"
            "                      speaker embedding) and .npy/.npz speaker embeddings (default default_voices; empty disables)\n"
            "  --python-src DIR    directory holding the zonos3 Python package for the helper workers (default:\n"
            "                      model/src of the checkout holding the executable, else python/ beside it)\n"
            "  --worker-python PY  interpreter of the helper worker <python-src>/zonos3/workers/pyhost.py: speech\n"
            "                      editing and plugins (zonos3.tts.edit_runtime and the --plugins routers; word\n"
            "                      timings need fusealign-hub installed), one persistent worker. Speech recognition\n"
            "                      (Qwen3-ASR, optional: transcribes clips sent without their transcript) runs in its\n"
            "                      own interpreter: ZONOS3_ASR_PYTHON (default <repo>/.venv-asr/bin/python, set up by\n"
            "                      tools/setup_asr_env.sh; ZONOS3_ASR_DEVICE picks its device)\n"
            "  --plugins NAME[,NAME...]\n"
            "                      server plugins (repeatable): the zonos3_<NAME> package installed in --worker-python\n"
            "                      serves /v2/<NAME>/* and /<NAME> through the Python host\n"
            "  --steer PATH        steering bundle (steer.gguf) for request controls and /v2/controls\n"
            "  --accent-adapters PATH      accent adapters (accents.gguf)\n"
            "  --laci-heads LANG=PATH      LACI alignment heads of a language (repeatable)\n"
            "  --tts-phoneme-lexicon DIR   gold pronunciation lexicon (LMDB) for /v2/phonemize\n"
            "  --studio-projects-dir DIR   server-side studio projects and media (/v2/projects, /v2/media)\n"
            "  --studio-dir DIR    studio production build (/ chooser, /studio, /lite) (default: studio/dist of the checkout\n"
            "                      holding the executable, else studio/ beside it)\n",
            a0, zonos3_generation().max_frames, (int) (zonos3_generation().max_frames / ZONOS3_FRAME_RATE));
}

bool env_int(const char * name, int dflt, int & out) {
    const char * v = getenv(name);
    if (!v || !*v) {
        out = dflt;
        return true;
    }
    char * end = nullptr;
    const long x = strtol(v, &end, 10);
    if (!end || *end) return false;
    out = (int) x;
    return true;
}

int server_run(int argc, char ** argv, zonos3_server_ctl * ctl) {
    if (argc < 2) {
        usage(argv[0]);
        return 1;
    }
#ifndef _WIN32
    signal(SIGPIPE, SIG_IGN); // a helper worker that exits must not kill the server mid-write
#endif

    ServerState s;
    std::string model_path = argv[1], host = "127.0.0.1", projects_dir;
    std::vector<std::string> zac_paths; // --zac, the default first
    int port = 1919;
    bool zac_cpu = false;
    auto value = [&](int & i) -> const char * { return i + 1 < argc ? argv[++i] : nullptr; };
    for (int i = 2; i < argc; ++i) {
        const std::string a = argv[i];
        const bool has_value = i + 1 < argc;
        if (a == "--zac" && has_value) zac_paths.push_back(value(i));
        else if (a == "--zac-encoder" && has_value) s.encoder_path = value(i);
        else if (a == "--ssl-teacher" && has_value) s.teacher_path = value(i);
        else if (a == "--spk-encoder" && has_value) s.spk_path = value(i);
        else if (a == "--steer" && has_value) s.steer_path = value(i);
        else if (a == "--accent-adapters" && has_value) s.accent_adapters_path = value(i);
        else if (a == "--tts-phoneme-lexicon" && has_value) s.phoneme_lexicon_dir = value(i);
        else if (a == "--laci-heads" && has_value) {
            const std::string spec = value(i);
            const size_t eq = spec.find('=');
            if (eq == std::string::npos || eq == 0 || eq + 1 == spec.size()) {
                fprintf(stderr, "error: --laci-heads takes LANG=PATH, got '%s'\n", spec.c_str());
                return 1;
            }
            std::string lang = spec.substr(0, eq); // zonos3.tts.laci.parse_laci_heads: lowercase, '-' -> '_'
            for (char & c : lang) c = c == '-' ? '_' : (char) tolower((unsigned char) c);
            s.laci_heads[lang] = spec.substr(eq + 1);
        } else if (a == "--host" && has_value) host = value(i);
        else if (a == "--port" && has_value) port = atoi(value(i));
        else if (a == "--gpu") s.use_gpu = true;
        else if (a == "--cpu") s.use_gpu = false;
        else if (a == "--zac-cpu") zac_cpu = true;
        else if (a == "--zac-gpu") zac_cpu = false;
        else if (a == "--max" && has_value) s.max_frames = atoi(value(i));
        else if (a == "--batch" && has_value) s.batch_slots = std::max(1, atoi(value(i)));
        else if (a == "--zac-threads" && has_value) s.zac_threads = std::max(1, atoi(value(i)));
        else if (a == "--reference-idle-unload" && has_value) s.reference_idle_unload_s = std::max(0, atoi(value(i)));
        else if (a == "--stream-decode-default" && has_value) {
            s.stream_decode_default = value(i);
            if (s.stream_decode_default != "prefix" && s.stream_decode_default != "whole") {
                fprintf(stderr, "error: --stream-decode-default must be prefix or whole\n");
                return 1;
            }
        } else if (a == "--tts-default-voices-dir" && has_value) s.default_voice_dir = value(i);
        else if (a == "--python-src" && has_value) s.python_src = value(i);
        else if (a == "--worker-python" && has_value) s.worker_python = value(i);
        else if (a == "--plugins" && has_value) {
            const std::string list = value(i);
            size_t start = 0;
            for (;;) {
                const size_t comma = list.find(',', start);
                const std::string name = list.substr(start, comma == std::string::npos ? std::string::npos : comma - start);
                // zonos3.server.plugins: [a-z][a-z0-9_]*
                const bool valid = !name.empty() && name[0] >= 'a' && name[0] <= 'z' &&
                                   std::all_of(name.begin(), name.end(), [](char c) {
                                       return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_';
                                   });
                if (!valid) {
                    fprintf(stderr, "error: --plugins: bad plugin name '%s' (expected [a-z][a-z0-9_]*)\n", name.c_str());
                    return 1;
                }
                if (std::find(s.plugins.begin(), s.plugins.end(), name) == s.plugins.end()) s.plugins.push_back(name);
                if (comma == std::string::npos) break;
                start = comma + 1;
            }
        } else if (a == "--studio-projects-dir" && has_value) projects_dir = value(i);
        else if (a == "--studio-dir" && has_value) s.studio_dir = value(i);
        else {
            usage(argv[0]);
            return 1;
        }
    }
    if (!mp_file_exists(model_path)) {
        fprintf(stderr, "failed to load model %s\n", model_path.c_str());
        mp_print_missing_hint("backbone-f16.gguf (or another quant)", model_path);
        return 1;
    }
    s.model_path = model_path;

    // Prefix streaming and silence prefill settings (the sgl vocoder's environment).
    if (!env_int("ZONOS3_PREFIX_STREAM_CHUNK_FRAMES", 15, s.prefix.chunk_frames) ||
        !env_int("ZONOS3_PREFIX_STREAM_FIRST_CHUNK_FRAMES", 1, s.prefix.first_chunk_frames) ||
        !env_int("ZONOS3_PREFIX_STREAM_HOLD_BACK_FRAMES", 0, s.prefix.hold_back_frames) ||
        !env_int("ZONOS3_PREFIX_STREAM_CONTEXT_FRAMES", 450, s.prefix.context_frames) ||
        s.prefix.chunk_frames < 1 || s.prefix.first_chunk_frames < 1 || s.prefix.hold_back_frames < 0 ||
        s.prefix.context_frames < s.prefix.chunk_frames) {
        fprintf(stderr, "error: prefix stream settings: chunk_frames >= 1, first_chunk_frames >= 1, hold_back_frames >= 0, "
                        "context_frames >= chunk_frames\n");
        return 1;
    }
    {
        std::string err;
        if (!configure_silence_prefill(s, err)) {
            fprintf(stderr, "error: %s\n", err.c_str());
            return 1;
        }
    }

    // Companion discovery next to the backbone (the reference-audio models load lazily).
    auto companion = [&](std::string & path, const char * name) {
        if (path.empty()) path = mp_find_companion(model_path, name);
    };
    // No --zac: the default decoder (required) and, when it sits there too, the full-size one as an extra decoder.
    if (zac_paths.empty() && !mp_find_companion(model_path, MP_DEFAULT_ZAC).empty())
        for (const char * name : {MP_DEFAULT_ZAC, MP_FULL_ZAC})
            if (std::string found = mp_find_companion(model_path, name); !found.empty()) {
                fprintf(stderr, "zonos3-server: using %s found next to model: %s\n", name, found.c_str());
                zac_paths.push_back(std::move(found));
            }
    companion(s.encoder_path, "zac-encoder.gguf");
    companion(s.teacher_path, "ssl-teacher.gguf");
    companion(s.spk_path, "spk-encoder.gguf");
    companion(s.steer_path, "steer.gguf");
    companion(s.accent_adapters_path, "accents.gguf");
    for (const std::string * p : {&s.encoder_path, &s.teacher_path, &s.spk_path, &s.steer_path, &s.accent_adapters_path}) {
        if (!p->empty() && !mp_file_exists(*p)) {
            fprintf(stderr, "error: companion file %s does not exist\n", p->c_str());
            return 1;
        }
    }
    for (const auto & [lang, path] : s.laci_heads)
        if (!mp_file_exists(path)) {
            fprintf(stderr, "error: --laci-heads %s=%s: no such file\n", lang.c_str(), path.c_str());
            return 1;
        }
    if (!s.phoneme_lexicon_dir.empty()) {
        std::string err;
        if (!open_phoneme_lexicon(s, err)) {
            fprintf(stderr, "error: --tts-phoneme-lexicon: %s\n", err.c_str());
            return 1;
        }
    }
    if (zac_paths.empty()) {
        fprintf(stderr, "error: --zac <zac.gguf> is required (also auto-detected when zac-keep2.gguf sits next to <model.gguf>)\n");
        mp_print_missing_hint(MP_DEFAULT_ZAC, mp_parent_dir(model_path) + "/" + MP_DEFAULT_ZAC);
        return 1;
    }
    if (s.python_src.empty()) s.python_src = zonos3_find_python_src();
    if (!s.plugins.empty() && s.worker_python.empty()) {
        fprintf(stderr, "error: --plugins needs --worker-python (plugins run in the Python host)\n");
        return 1;
    }
    if (!s.worker_python.empty()) {
        if (s.python_src.empty() || !mp_file_exists(s.python_src + "/zonos3/workers/pyhost.py")) {
            fprintf(stderr, "error: --worker-python needs the zonos3 package: pass --python-src DIR "
                            "(DIR/zonos3/workers/pyhost.py)\n");
            return 1;
        }
        const auto host_env = pyhost_environment(s);
        for (const std::string & name : s.plugins) {
            std::string err;
            if (run_process({s.worker_python, "-c",
                             "import importlib.util, sys; sys.exit(importlib.util.find_spec('zonos3_" + name + "') is None)"},
                            host_env, err) != 0) {
                fprintf(stderr, "error: plugin %s: zonos3_%s is not installed in %s\n", name.c_str(), name.c_str(),
                        s.worker_python.c_str());
                return 1;
            }
        }
        s.pyhost = std::make_unique<PyHost>(s);
        // zonos3.tts.edit_align.fusealign_installed, asked of the host's interpreter once (the speech_editing
        // capability)
        std::string err;
        s.fusealign_installed = run_process({s.worker_python, "-c",
                                             "import importlib.util, sys; "
                                             "sys.exit(importlib.util.find_spec('fusealign_hub') is None)"},
                                            err) == 0;
        if (!s.fusealign_installed)
            fprintf(stderr, "zonos3-server: warning: %s has no fusealign_hub; speech editing is off\n",
                    s.worker_python.c_str());
    }
    if (!projects_dir.empty()) {
        try {
            s.projects = std::make_unique<ProjectStore>(projects_dir);
        } catch (const std::exception & e) {
            fprintf(stderr, "error: --studio-projects-dir %s: %s\n", projects_dir.c_str(), e.what());
            return 1;
        }
    }
    s.prof = getenv("ZONOS3_PROFILE") != nullptr;

    if (!zonos3_model_load(s.model, model_path.c_str(), s.use_gpu)) {
        fprintf(stderr, "failed to load model %s\n", model_path.c_str());
        return 1;
    }
    if (std::string err; !configure_frame_budget(s, err)) {
        fprintf(stderr, "error: %s\n", err.c_str());
        return 1;
    }
    // LACI / span-follow alignment heads (sgl: LaciHeads.load per --laci-heads at scheduler start); every head must be a
    // query head of a global-attention layer, the layers the probe reads.
    for (const auto & [lang, path] : s.laci_heads) {
        try {
            zonos3_laci::LaciHeads heads = zonos3_laci::LaciHeads::load(path);
            for (const auto & h : heads.heads)
                if (h.layer < 0 || h.layer >= (int) s.model.layers.size() || s.model.layers[h.layer].is_gdn ||
                    h.head < 0 || h.head >= (int) s.model.hp.n_head) {
                    fprintf(stderr, "error: --laci-heads %s=%s: head (%d, %d) is not an attention head of this model\n",
                            lang.c_str(), path.c_str(), h.layer, h.head);
                    return 1;
                }
            s.laci[lang] = std::move(heads);
        } catch (const std::exception & e) {
            fprintf(stderr, "error: --laci-heads %s=%s: %s\n", lang.c_str(), path.c_str(), e.what());
            return 1;
        }
    }
    // The probe reads up to this many text rows of a request (sgl --align-probe-max-text default 2048 with heads).
    if (!s.laci.empty()) s.probe_max_text = std::min(2048, s.context_capacity);
    if (!s.steer_path.empty()) {
        std::string err;
        if (!zonos3_steer_load(s.steer, s.steer_path.c_str(), s.model, err)) {
            fprintf(stderr, "failed to load steering %s: %s\n", s.steer_path.c_str(), err.c_str());
            return 1;
        }
        s.controls_doc = zonos3_steer_controls_json(s.steer, (int) s.model.hp.speaker_dim);
        s.steer_loaded = true;
        std::string srcs;
        for (int k : s.steer.sources) srcs += (srcs.empty() ? "" : ", ") + std::to_string(k);
        fprintf(stderr, "zonos3-server: steering %s: %zu directions, hidden sources [%s] (%s)\n",
                s.steer.name.c_str(), s.steer.dirs.size(), srcs.c_str(), s.steer_path.c_str());
    }
    {
        std::string err;
        if (!load_accent_adapters(s, err)) {
            fprintf(stderr, "error: %s\n", err.c_str());
            return 1;
        }
    }
    if (s.studio_dir.empty()) s.studio_dir = zonos3_find_studio_dir();

    // Decoders (load_decoder): each with its own decode pool of --zac-threads lanes.
    for (const std::string & zac_path : zac_paths) {
        std::string err;
        if (!load_decoder(s, zac_path, s.use_gpu && !zac_cpu, err)) {
            fprintf(stderr, "%s\n", err.c_str());
            if (!mp_file_exists(zac_path)) mp_print_missing_hint(MP_DEFAULT_ZAC, zac_path);
            return 1;
        }
        const Decoder & d = *s.decoders.back();
        if (!zac_partial_frames(d.zac))
            fprintf(stderr, "zonos3-server: %s has no per-codebook residual biases (res.{i}.bias): early partial-frame "
                            "decode is off for decoder %s, prefix streaming decodes complete frames only (reconvert with "
                            "tools/convert_gguf.py codec-decoder)\n",
                    zac_path.c_str(), d.id.c_str());
    }
    std::string decoders;
    for (const auto & d : s.decoders) decoders += (decoders.empty() ? "" : ", ") + d->id + "=" + d->path;

    start_scheduler(s);
    start_reference_reaper(s);

    httplib::Server svr;
    svr.set_payload_max_length((size_t) ProjectStore::MAX_MEDIA_BYTES); // media uploads; JSON bodies carry audio
    svr.set_read_timeout(120);
    svr.set_write_timeout(600); // long synthesis streams

    install_middleware(svr);
    register_capability_routes(svr, s);
    register_generate_routes(svr, s);
    register_speaker_routes(svr, s);
    register_phonemize_routes(svr, s);
    register_edit_routes(svr, s);
    register_plugin_routes(svr, s);
    register_project_routes(svr, s.projects.get());
    install_studio(svr, s.studio_dir); // last: every API route matches first

    fprintf(stderr, "zonos3-server: backbone=%s decoders=[%s] spk-encoder=%s reference-encoder=%s teacher=%s steer=%s backend=%s batch=%d zac-threads=%d python-src=%s\n",
            model_path.c_str(), decoders.c_str(), s.spk_path.empty() ? "(none)" : s.spk_path.c_str(),
            s.encoder_path.empty() ? "(none)" : s.encoder_path.c_str(), s.teacher_path.empty() ? "(none)" : s.teacher_path.c_str(),
            s.steer_path.empty() ? "(none)" : s.steer_path.c_str(), s.model.is_gpu ? "GPU" : "CPU", s.batch_slots, s.zac_threads,
            s.python_src.empty() ? "(none)" : s.python_src.c_str());
    // Bind before announcing (and before publishing to ctl): --port 0 asks the OS for a free ephemeral port, which the
    // embedding app reads back from ctl->port.
    bool bound = !s.worker_failed;
    if (bound) {
        if (port == 0) {
            port = svr.bind_to_any_port(host);
            bound = port > 0;
        } else bound = svr.bind_to_port(host, port);
    }
    bool ok = false;
    if (bound) {
        fprintf(stderr, "zonos3-server: listening on http://%s:%d  (studio: %s)\n", host.c_str(), port,
                s.studio_dir.empty() ? "not built" : s.studio_dir.c_str());
        if (ctl) {
            ctl->svr = &svr;
            ctl->port.store(port);
        }
        ok = svr.listen_after_bind();
    }

    if (s.pyhost) s.pyhost->close(); // before the scheduler: its engine calls finish first
    stop_scheduler(s);               // stop the worker, reaper + ZAC pool before returning
    stop_reference_reaper(s);
    if (ctl) ctl->svr = nullptr; // svr is about to go out of scope
    if (!bound) {
        fprintf(stderr, "error: failed to bind %s:%d\n", host.c_str(), port);
        return 1;
    }
    // listen_after_bind() returns true on a graceful stop() and false only if the accept loop died on a real error.
    return ok ? 0 : 1;
}

} // namespace

int zonos3_server_main(int argc, char ** argv, zonos3_server_ctl * ctl) {
    const int rc = server_run(argc, argv, ctl);
    if (rc != 0 && ctl) ctl->failed.store(true);
    return rc;
}

#ifndef ZONOS3_APP_EMBED
int main(int argc, char ** argv) {
    return zonos3_server_main(argc, argv, nullptr);
}
#endif
