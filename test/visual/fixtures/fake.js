// Canned data for the launcher's goldens: one studio in every state the card
// vocabulary names (03 §6), a group mid-start, a failed install with its
// steps, and a weights cache with an orphan.
//
// Time is frozen. Every screen states an elapsed time, and a golden of a
// running clock would differ on every run, so Date.now() is fixed here and
// every timestamp below is relative to it.

export const NOW = Date.parse("2026-09-16T12:16:40.000Z");

export function freezeTime() {
  const RealDate = Date;
  Date.now = () => NOW;
  // `new Date()` with no arguments is the other way a clock leaks in.
  globalThis.Date = class extends RealDate {
    constructor(...args) {
      super(...(args.length ? args : [NOW]));
    }
    static now() {
      return NOW;
    }
  };
}

const at = (secondsAgo) => new Date(NOW - secondsAgo * 1000).toISOString();

export const studios = [
  {
    manifest_loaded: true, id: "ltx-studio", name: "ltx studio",
    description: "LTX-2.5 video with synchronised audio, MLX on Apple Silicon.",
    kinds: ["video", "audio"], heavy: true, peak_ram_gb: 20,
    root: "~/.helmstudio/studios/ltx-studio/src", root_present: true,
    install_state: "ready", size_bytes: 21_400_000_000,
    runtime_env: { engine: "MLX", backend: "Metal", python: "3.12.7", mlx: "0.31.2" },
    group: {
      studio_id: "ltx-studio", group_run_id: "01JB9AAAAAAAAAAAAAAAAAAAC4", state: "running",
      processes: [
        { spec_name: "studio", role: "main", state: "running", health_state: "passing", command: "[\"./dist/ltx\"]", port: 8720, pid: 4821, started_at: at(724), ui: "/" },
      ],
    },
  },
  {
    manifest_loaded: true, id: "h3-studio", name: "h3 studio",
    description: "MiniMax-H3 video and audio through a native Metal engine.",
    kinds: ["video", "audio"], heavy: true, peak_ram_gb: 21,
    root: "~/.helmstudio/studios/h3-studio/src", root_present: true,
    install_state: "failed_build", job_id: "01JB9BBBBBBBBBBBBBBBBBBBB1",
    commit_sha: "a4f91c2d9e6b3a", size_bytes: 1_240_000_000,
    runtime_env: { engine: "h3.c", backend: "metal", go: "1.27.1", clang: "16.0.0" },
    last_failure: {
      code: "step_failed", phase: "build", step_index: 2, exit_code: 2,
      message: "Build the Metal engine failed. make exited with code 2 — the log shows a missing Metal header, which usually means the Xcode command line tools aren't installed. Run xcode-select --install, then retry this step.",
    },
    group: { studio_id: "h3-studio", processes: [] },
  },
  {
    manifest_loaded: true, id: "iris-studio", name: "iris studio",
    description: "FLUX.2 Klein and Z-Image-Turbo still image generation.",
    kinds: ["image"], heavy: true, peak_ram_gb: 30,
    root: "~/.helmstudio/studios/iris-studio/src", root_present: true,
    install_state: "fetching_weights", job_id: "01JB9CCCCCCCCCCCCCCCCCCCC2",
    size_bytes: 9_200_000_000,
    group: { studio_id: "iris-studio", processes: [] },
  },
  {
    manifest_loaded: true, id: "auk-studio", name: "AuK studio",
    description: "Zero-shot and instruct speech generation, editing and enhancement.",
    kinds: ["audio"], heavy: true, peak_ram_gb: 25,
    root: "~/.helmstudio/studios/auk-studio/src", root_present: false,
    install_state: "auth_required",
    group: { studio_id: "auk-studio", processes: [] },
  },
  {
    manifest_loaded: true, id: "wan-studio", name: "wan studio",
    description: "A studio someone else wrote, listed in the registry and not yet installed.",
    kinds: ["video"], heavy: false,
    root: "~/.helmstudio/studios/wan-studio/src", root_present: false,
    install_state: "update_available", size_bytes: 3_100_000_000,
    group: { studio_id: "wan-studio", processes: [] },
  },
];

/** A group mid-start, for 03 §9's starting screen. */
export const starting = {
  manifest_loaded: true, id: "ltx-studio", name: "ltx studio",
  description: "LTX-2.5 video with synchronised audio, MLX on Apple Silicon.",
  kinds: ["video", "audio"], heavy: true, peak_ram_gb: 20,
  root: "~/.helmstudio/studios/ltx-studio/src", root_present: true,
  install_state: "ready",
  group: {
    studio_id: "ltx-studio", group_run_id: "01JB9DDDDDDDDDDDDDDDDDDDD3", state: "starting",
    processes: [
      { spec_name: "engine", role: "sidecar", state: "running", health_state: "passing", command: "[\"./engine\"]", port: 8781, started_at: at(38), health_timeout_s: 300 },
      { spec_name: "studio", role: "main", state: "starting", health_state: "failing", command: "[\"./dist/ltx\"]", port: 8782, started_at: at(24), health_timeout_s: 240, ui: "/" },
      { spec_name: "batch", role: "worker", state: "queued", health_state: "", command: "[\"./batch\"]" },
    ],
  },
};

export const jobs = {
  "h3-studio": {
    id: "01JB9BBBBBBBBBBBBBBBBBBBB1", kind: "install", studio_id: "h3-studio", state: "failed",
    progress_num: 2, progress_den: 5, created_at: at(300), started_at: at(295),
    last_error: {
      code: "step_failed", phase: "build", step_index: 2, exit_code: 2,
      message: "Build the Metal engine failed. make exited with code 2 — the log shows a missing Metal header, which usually means the Xcode command line tools aren't installed. Run xcode-select --install, then retry this step.",
    },
    steps: [
      { step_index: 0, step_name: "Clone repository", command: "git clone --recurse-submodules https://github.com/janishar/h3c-studio", state: "succeeded", started_at: at(295), finished_at: at(247) },
      { step_index: 1, step_name: "Check toolchain", command: "git make cc go", state: "succeeded", started_at: at(247), finished_at: at(245.8) },
      { step_index: 2, step_name: "Build the Metal engine", command: "cd h3c && make mps", state: "failed", exit_code: 2, started_at: at(245), finished_at: at(84) },
      { step_index: 3, step_name: "Build the studio server", command: "go build -o dist/h3studio .", state: "pending" },
      { step_index: 4, step_name: "Download weights", command: "hf download MiniMaxAI/MiniMax-H3", state: "pending" },
    ],
  },
  "iris-studio": {
    id: "01JB9CCCCCCCCCCCCCCCCCCCC2", kind: "download", studio_id: "iris-studio", state: "running",
    progress_num: 9_200_000_000, progress_den: 21_400_000_000, created_at: at(260), started_at: at(258),
    steps: [],
  },
};

export const models = [
  {
    id: "01JB9MMMMMMMMMMMMMMMMMMMM1", hf_repo: "MiniMaxAI/MiniMax-H3", revision: "main",
    source: "managed", state: "ready", dest: "models/minimax-h3", path: "~/.helmstudio/models/minimax-h3",
    total_bytes: 60_000_000_000, bytes_on_disk: 60_000_000_000, ref_count: 1,
    studios: ["h3-studio"], created_at: at(86400 * 3), verified_at: at(7200),
  },
  {
    id: "01JB9MMMMMMMMMMMMMMMMMMMM2", hf_repo: "Lightricks/LTX-2.5", revision: "main",
    source: "linked", state: "linked", dest: "models/ltx-2-5", path: "~/.helmstudio/models/ltx-2-5",
    external_path: "/Volumes/Models/LTX-2.5",
    total_bytes: 21_400_000_000, bytes_on_disk: 0, ref_count: 1,
    studios: ["ltx-studio"], created_at: at(86400), verified_at: at(30),
  },
  {
    id: "01JB9MMMMMMMMMMMMMMMMMMMM3", hf_repo: "tencent/AuK-Flash", revision: "main",
    source: "managed", state: "ready", dest: "models/auk-flash", path: "~/.helmstudio/models/auk-flash",
    total_bytes: 4_100_000_000, bytes_on_disk: 4_100_000_000, ref_count: 0,
    studios: [], created_at: at(86400 * 12), verified_at: at(86400 * 12),
  },
  {
    id: "01JB9MMMMMMMMMMMMMMMMMMMM4", hf_repo: "black-forest-labs/FLUX.2-Klein", revision: "main",
    source: "managed", state: "downloading", dest: "models/flux-2-klein", path: "~/.helmstudio/models/flux-2-klein",
    total_bytes: 21_400_000_000, bytes_on_disk: 9_200_000_000, ref_count: 1,
    studios: ["iris-studio"], created_at: at(260),
  },
];

export const hfToken = { present: true, added_at: "2026-09-14T09:31:00Z" };

/** The build output the install golden shows, ANSI and a progress line kept. */
export const LOG = [
  "==> cd h3c && make mps",
  "clang -std=c17 -O3 -march=native -c h3_metal.m",
  "\u001b[33mwarning: unused variable 'scratch' [-Wunused-variable]\u001b[0m",
  "clang -std=c17 -O3 -march=native -c h3_ffmpeg.c",
  "linking  25%|\r",
  "linking  50%|\r",
  "linking  75%|\r",
  "linking 100%|\r",
  "\u001b[31mfatal error: 'Metal/Metal.h' file not found\u001b[0m",
  "make: *** [h3_metal.o] Error 2",
  "==> step failed after 2m 41s (exit 2)",
];

const event = (name, data, id) => ({ id, name, data: JSON.stringify(data), json: () => data });

/** A finite log, so a golden is a settled terminal rather than a race. */
async function* logStream() {
  yield event("step", { step_index: 2, step_name: "Build the Metal engine", command: "cd h3c && make mps" }, "1");
  for (const [i, text] of LOG.entries()) yield event("line", { text, step_index: 2 }, String(i + 2));
  yield event("end", { state: "failed", last_error: { code: "step_failed", message: "Build the Metal engine failed. make exited with code 2." } }, "90");
}

/** A client that answers from the canned data and refuses to be surprising. */
export function fakeClient() {
  const page = (items) => Promise.resolve({ items, next_cursor: null });
  return {
    studios: { list: () => page(studios), processLog: () => logStream() },
    models: { list: () => page(models) },
    jobs: {
      get: (id) => Promise.resolve(Object.values(jobs).find((j) => j.id === id)),
      logs: () => logStream(),
    },
    settings: {
      theme: () => Promise.resolve({ theme: "system" }),
      setTheme: (b) => Promise.resolve(b),
      huggingFaceToken: () => Promise.resolve(hfToken),
    },
  };
}
