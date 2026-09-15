# Product Requirements

> Frozen design, 15 Sep 2026. This file is the contract.
> Do not edit to make an implementation pass — raise a finding instead.

helmstudio is a local launcher and platform for open-weight generative models. It installs a studio's repo, builds it, fetches its weights, supervises its processes, gives it storage, a gallery and a timeline, and presents the whole shelf in one window. The studios stay independent repos; helmstudio never imports their code.

## 1 · Problem and opportunity

Open-weight video, image and speech models have reached the point where one Mac with 64 GB of unified memory generates a five-second video with synchronised audio, a 4K still, or a minute of expressive speech — entirely offline. The models are free. Running them is not.

The cost is paid in setup. Someone who wants to try MiniMax-H3 reads a README, clones a repo, discovers the `git submodule update --init --recursive` mentioned three sections later, installs a C toolchain, runs `make mps`, and watches it fail because Xcode command line tools are a version behind. Python studios fail differently: the repo wants 3.10, the system has 3.13, and last month's virtualenv has conflicting pins. Then the download — 16 GB for FLUX.2 Klein, 60 GB for a video checkpoint — with no resume when the Wi-Fi drops. Finally two studios both hard-code port 8720 and the second dies with an error the user has no context for.

The second cost is paid after setup, and it is the one nobody talks about. Every studio rebuilds the same things badly: a take list, a log panel, a video player, a place to put outputs, a timeline. Each one keeps its generations in its own folder in its own format, so the image you made in one cannot become the first frame of a video in another without a trip through Finder. helmstudio answers both: a launcher that removes the setup, and a platform that gives every studio storage, a gallery, media handling and sequencing so it never has to build them.

## 2 · Users and jobs to be done

**The creator** makes things and owns a Mac because it is a good computer, not because it is a GPU rig. Their job is "generate the thing without learning a toolchain." They measure success by whether the first generation happened on the first evening, and whether coming back a week later still works without re-reading anything. If helmstudio shows them a compiler error as the whole message, it has failed them.**

**The tinkerer** follows model releases. Their job is "try the new thing the week it drops, next to the last thing I tried." They measure success by time-to-first-output for a newly added studio, and by keeping four studios installed without a 200 GB disk bill. They will be first to ask *why* something failed, so the logs must be real logs.**

**The studio author** wrote one of these repos and wants people to run it. Their job is "make my repo installable by anyone with a Mac, and stop rebuilding infrastructure." They measure success by two things: adding a studio is one YAML file with no daemon changes, and the SDK saves them a gallery, a player and a store they would otherwise write.**

## 3 · Product principles

**Studios stay independent repos.** helmstudio never vendors, forks, patches or imports studio code. A studio must remain fully usable by someone who clones it directly and ignores helmstudio entirely.**

**The daemon is never a model runtime.** No inference, no tensors, no Metal, no Python in-process. Its job is lifecycle and platform: fetch, build, download, supervise, store, serve. The moment a feature requires understanding a model format, it belongs in a studio.**

**Nothing leaves the machine.** The only outbound calls are the ones a user action implies — GitHub, Hugging Face, and whatever a studio's build fetches. No telemetry, no analytics, no account. A reviewer can grep the daemon for every hostname it contacts.**

**A failed step leaves a recoverable state.** Every install, build and download is idempotent. A killed download resumes; a failed build keeps the clone and the logs. Retry is always offered.**

**One heavy studio at a time, by default.** Two 20 GB models do not coexist on a 64 GB machine. The daemon enforces it and states the arithmetic before enforcing it.**

**Everything the daemon starts is supervised the same way.** A web server, a model sidecar, a worker and a shared service are all processes in a declared group with health, ordering and teardown. There is no weaker second path for "extra" processes.**

**Every platform service is optional.** A studio that uses none of the SDK still installs, launches and runs. The day adoption becomes mandatory, this stopped being a launcher.**

**Adding a studio is a data change.** If support requires editing the daemon, the manifest schema is wrong. "We'll special-case it in Go" is not available.**

## 4 · System shape

## 5 · One state vocabulary

Install state and process state are separate machines and are never merged. These spellings are used in this document, in the data model and in the UI; a state with no chip is a bug in the design, not licence to invent a synonym.

| Install state                                      | Meaning                                                      | Chip                       |
|----------------------------------------------------|--------------------------------------------------------------|----------------------------|
| `listed`                                           | In the catalogue, nothing on disk                            | Not installed              |
| `cloning` → `cloned`                               | Fetching repo and submodules                                 | Installing · cloning       |
| `building` → `built`                               | Running `build[]` in order                                   | Installing · step n of m   |
| `fetching_weights`                                 | Downloading declared artifacts                               | Downloading 43%            |
| `auth_required`                                    | A gated weight needs a Hugging Face token; nothing failed    | Needs a Hugging Face token |
| `ready`                                            | Installed and launchable                                     | Installed                  |
| `update_available`                                 | Upstream ref moved past the installed commit                 | Update available           |
| `failed_clone` · `failed_build` · `failed_weights` | Last attempt failed at that phase; work already done is kept | Install failed · \<phase\> |
| `removing`                                         | Uninstall in progress                                        | Removing…                  |

| Process state         | Meaning                                           | Chip                    |
|-----------------------|---------------------------------------------------|-------------------------|
| `queued`              | Waiting for a heavy slot or a dependency          | Queued                  |
| `starting`            | Spawned, health not yet passing                   | Starting · 0:24 of 4:00 |
| `running`             | Health passing                                    | Running · 12:04         |
| `stopping` → `exited` | SIGTERM to the group, SIGKILL after grace         | Stopping… / Stopped     |
| `failed`              | Exited before ready, health timed out, or crashed | Crashed · exit 137      |

## 6 · Launcher: registry, install, weights

#### Registry and discovery

- **R1** A studio's manifest — `helmstudio.yaml` — lives at the root of the studio's own repository, beside the code it describes. The bundled registry holds **pointers** (`id`, `repo`, `ref`, certification level), and the daemon resolves and caches the manifest from the repo at that ref. One copy, so nothing drifts, and a studio can announce a new build step without waiting for a helmstudio release.
- **R2** Each manifest is validated at load; an invalid one is not listed and its field and reason are logged. One bad manifest never blocks the others.
- **R2a** A manifest resolves from three sources, first match winning: a local file at `~/.helmstudio/studios/<id>.yaml`, then `helmstudio.yaml` in the repo at the pinned ref, then an inline manifest carried by the registry pointer for repos that have none. All three normalise to one shape, so a studio behaves identically whichever wrote it.
- **R3** **Anyone can author a manifest, not only a studio's author.** helmstudio provides an in-app editor — a form over the schema beside a live YAML pane — that validates as you type, shows the certification criteria updating, runs the smoke harness without installing, saves to the local directory, and exports the file to commit upstream or open as a pull request. One format means a manifest written to get a model running locally is, unchanged, the one a registry review accepts.
- **R3c** **A manifest can be imported as a file** — dropped onto the window, chosen from a picker, pasted as text, or fetched from a URL — and several at once. Import validates, reports what it found, and adds the entry to the library as Local with a note of where it came from. **Import is not install:** an imported manifest is a description until the user installs it, and installing it from a repository they do not own still passes through the approval screen with every build command shown.
- **R3d** Export is the mirror of import: any library entry can be written out as a file or copied as text, unchanged and ready to commit upstream, attach to an issue, or send to someone else. This is the sharing loop — a manifest that got a model running on one machine is the artefact that gets it running on another, with no marketplace in between.
- **R3a** A local manifest overrides a registry entry with the same `id`; the card says so and offers Revert. An import whose `id` already exists offers Override, Rename or Cancel rather than silently replacing anything. Duplicate-and-edit copies any entry to local under a new id, so forking a studio to point at different weights or flags breaks nothing.
- **R3b** The catalogue is a **library**: the set of studios this machine knows about, of which installed ones are a subset. Each entry carries source (local, repo, registry), certification level and install state as three independent facts, and the library is derived at startup from files — bundled pointers, the local directory, and fetched manifests cached on disk — so a user can hand-edit or version-control their own entries.
- **R4** A manifest may declare `local_path`; cloning is then skipped.
- **R5** `requires` is evaluated against the host. `os` and `arch` mismatches block Install; memory and disk shortfalls warn with the specific number and let the user proceed.
- **R5a** Every studio declares its **compute runtime** — the framework it runs inference through and the device backends it supports. The shelf shows it on the card and the detail page, and a studio whose backends the host cannot provide is **blocked, not warned**: nobody should download 60 GB before discovering a studio is CUDA-only.
- **R5b** Doctor reports the host's side of the same question — chip, unified memory, Metal version, and the detected versions of any framework a manifest names — so "why is this slower than the README says" has somewhere to start.
- **R6** `helmstudio validate <file>` checks a manifest offline and prints schema errors with line numbers.
- **R6a** **A studio is fully developable with the SDKs alone.** `helm dev` is the daemon restricted to one studio and a local path — the same supervisor, manifest parser and template substitution — so the manifest is exercised from the first day rather than first executed at install time, and no code changes when the studio later runs hosted.
- **R6b** `helm adopt` imports a studio's standalone data into the daemon: blobs hardlinked rather than copied, rows replayed, assets deduplicated by hash. It is idempotent, has a `--dry-run`, and refuses a schema newer than the daemon knows rather than dropping fields it cannot read.
- **R6c** `helm doctor --studio` scores the certification criteria against a working tree on demand, so readiness is a live number during development rather than a verdict at submission.

#### Install

- **R7** Install runs `git clone --recurse-submodules` into `~/.helmstudio/studios/<id>/src` at `ref` when declared.
- **R8** Each `build[]` entry runs in declared order in its own `cwd`; a non-zero exit stops the sequence and sets `failed_build` with step index, exit code and log reference.
- **R9** stdout and stderr stream to the UI over SSE and append to the studio's log directory.
- **R10** Retry resumes from the first step that has not succeeded; completed steps are not re-run unless a full rebuild is requested.
- **R11** Tools in `requires.tools` are probed before the first step, failing fast with the tool name and the command that installs it.
- **R12** Python studios get a per-studio `uv` environment pinned to `python.version`; environments are never shared.
- **R13** Install can be cancelled: the running step's process group is killed, the phase marked failed, completed work kept.

#### Weights

- **R14** A weight is resolved one of two ways and the rest of the system cannot tell them apart. *Managed*: helmstudio downloads the Hugging Face repo into `~/.helmstudio/models/<dest>`. *Linked*: the user points at a directory they already have, and helmstudio creates `~/.helmstudio/models/<dest>` as a symlink to it rather than downloading anything. Either way `{models.<name>}` substitutes the resolved real path, and nothing is ever symlinked into the studio checkout, so `git pull` never meets an untracked path.
- **R14a** Any artifact can be linked instead of downloaded, from the weights panel on the install screen and from Models & disk. The user picks a directory; helmstudio checks it is readable and that the files the manifest declares are present at roughly the expected sizes, then records it as linked and `unverified` — launch is what proves it, and the UI says so rather than implying a check it did not perform.
- **R14b** Settings carries additional **model roots** — directories helmstudio scans for repos it recognises, such as an existing ComfyUI or LM Studio models folder. When a scan matches a declared weight, Install offers "use the copy you already have" beside the download, with both sizes stated.
- **R14c** A linked directory is treated as **read-only**. helmstudio never writes into it — no partial files, no repair, no permission changes — and a partially complete linked directory is reported with the missing files named, offering a managed download instead of completing in place.
- **R15** Downloads resume across restarts and interruptions, per file. Signed CDN URLs are re-resolved on every resume; a 403 on resume means re-resolve, not corruption; 429 backs off.
- **R16** An artifact already cached at the same repo and revision is not downloaded again; the second binding increments its reference count.
- **R17** Free disk is checked against declared size plus headroom before downloading, refusing with actual numbers.
- **R18** A gated repo returning 401/403 sets `auth_required` and prompts for a token rather than failing the install.
- **R19** The cache records size, source, revision and referencing studios; an artifact with no reference is reclaimable, and one in use cannot be deleted without a confirmation naming the dependents.
- **R19a** **Reclaim never follows a symlink.** Deleting a linked artifact removes the link and nothing else; the user's directory is never touched, and the action is labelled Unlink rather than Delete. Linked bytes are reported separately from cache bytes, because they are not helmstudio's to free.
- **R19b** A linked directory that disappears — an external drive unplugged, a folder moved — sets the artifact `missing` rather than failing obscurely at model load. Launch refuses with the path it expected and offers Relink or Download, and the studio's install state is untouched.

## 7 · Processes and supervision

- **R20** A studio declares one or more processes. The daemon starts them in dependency order, health-gates each before its dependents, and stops them in reverse.
- **R21** Each process is spawned in its own process group with `{port}`, `{ports.<name>}`, `{models.<name>}`, `{root}` and `{venv}` substituted, the studio root as cwd, and manifest `env` applied.
- **R22** Ports are assigned from a configurable range (default 8701–8799); a manifest port is advisory. A process declared `port: fixed` refuses to start when that port is taken, naming the occupant.
- **R23** After spawn the daemon polls `health` every `interval_s` (default 2) until `timeout_s` (default 180), showing elapsed against the budget. Loading 20 GB before answering is normal and must not read as a hang.
- **R24** Health probes support `path`, `tcp` and `exec`, because a worker has no HTTP endpoint.
- **R25** Only one group marked `heavy` runs at a time. Starting a second states the memory arithmetic and, on confirm, stops the first and waits for its ports to clear.
- **R26** Stop sends SIGTERM to each group in reverse dependency order, escalating to SIGKILL after grace. Install state is untouched.
- **R27** A `main` process that exits unexpectedly fails the group, surfaces the last 200 log lines and offers Restart. It is never auto-restarted, and **never restarted on a failed health check alone** — an eight-minute generation must survive a slow probe. A `sidecar` with `restart: on-failure` retries with backoff, at most three times in ten minutes.
- **R28** On clean shutdown children are terminated. On restart, survivors are re-adopted after verifying pid, process start time and process group, so a recycled pid is never signalled.
- **R29** `services/*.yaml` declares long-lived processes belonging to no studio, supervised by the same code path.
- **R30** Log retention is bounded: the last five run logs per studio, each capped with head-and-tail truncation, with the cap stated in the UI.

## 8 · Platform services

Every service below is optional to a studio and reached through the same capability-scoped API. A studio declares what it uses; the user sees it before install.

- **R31** **All studio state is written through the SDK** and stored centrally in SQLite. A studio does not maintain its own store, config directory or settings files. The provider decides where rows land — the daemon's database when hosted, the studio's own `./.helm/helm.db` standalone — so one backup, one migration path and one crash-safe write implementation serve every studio instead of one per author.
- **R31a** **Sessions are a platform concept.** Create, rename, duplicate, activate, list and soft-delete are SDK calls, with the studio's own settings carried in an opaque `state` document. Every studio has this idea today and each implements it differently; centralising it also lets the launcher show session counts and last-opened times on a card without parsing anyone's format.
- **R31b** `/kv/{ns}/{key}` holds singleton state — UI preferences, the last opened session, panel widths — with `ETag` and `If-Match` so two open tabs cannot silently overwrite each other.
- **R32** **Records.** `/records/{collection}` is a per-studio document store for domain data helmstudio does not model, queried through a closed filter language (`eq ne lt lte gt gte in contains exists`, at most eight clauses, always parameterised). **No SQL passthrough from a studio, ever.**
- **R33** Indexes and quotas are declared in the manifest; the daemon creates scoped expression indexes at install and enforces per-studio row and byte caps, refusing with the number rather than filling the disk.
- **R34** **Assets.** `POST /assets:adopt` hardlinks a file the studio already wrote into the store — no copy, no HTTP body — and `POST /assets` uploads bytes. Both are idempotent on content: dedup is a uniqueness constraint on the hash, not an algorithm.
- **R35** Media bytes live in directories, never in the database. The blob is named by hash; a human-readable hardlink under `library/<studio>/<month>/` is what a person browses in Finder. Paths are stored relative to the assets root so moving the library is a settings change.
- **R36** The daemon generates thumbnails, poster frames, waveforms and browser-playable proxies once per asset, cached as derived files no studio implements.
- **R37** `GET /assets/{id}` streams with `Range`, so a studio can scrub a video without downloading it.
- **R38** **Gallery.** `POST /gallery/items` records an output with its params and an `inputs[]` provenance list; `GET /gallery/items` queries by kind, tag, session, date and text, scoped to the caller unless `scope=all` and the capability is held.
- **R39** **Handoff.** `POST /handoff` sends an item to another studio's inbox; `GET /inbox` lists what is pending, and `POST /inbox/{id}:consume` marks an entry handled. The launcher's "Use in…" delivers a file into a studio's stage directory for a studio that has not implemented an inbox; how it does so without exposing a blob's inode is decided with the launcher UI in M6. (Amended 2026-09-15, M4, Q22 and Q23, approved in the M4 first review, #15: a GET that drains loses a handoff to a retry, a prefetch or a crash, and no manifest field says whether a studio has an inbox.)
- **R40** **Jobs.** Long work — installs, downloads, exports and a studio's own renders — is a Job with progress, cancellation and a log file, so no studio builds a second queue.
- **R41** **Events.** `GET /events` is an SSE stream carrying theme changes, job progress, inbox arrivals and shutdown.
- **R42** **Reconciliation.** `helmstudio fsck` reconciles rows and files both ways: a row with no file marks the asset `missing` and keeps the gallery item intact with its params; a file with no row is reported as reclaimable and never deleted silently.
- **R43** Secrets are never in the store: a Hugging Face token lives in the macOS Keychain and the database records only that one exists and when it was added.

## 9 · Timeline

Sequencing is inherently cross-studio — a sequence made from an h3 clip, an ltx clip, an iris still and an AuK voice line is nobody's studio document — so the framework owns it and must survive uninstalling the studio that made half of it.

- **R44** A timeline is a document: a target (resolution, fps, sample rate) and tracks of clips referencing assets with in and out points. It is non-destructive; sources are never modified or copied.
- **R45** `POST /timeline` creates a sequence from clips, `:append` adds to the open one without stealing focus, and `:open` asks the framework to show its editor. When no framework is present, `:open` returns `501` with a reason and the studio hides the button — **the data operation always works, only the presentation degrades**.
- **R46** Export creates a Job, reusing install's progress, cancellation and log rather than a second progress system.
- **R47** Every clip is conformed to the declared target; nothing is inferred per clip at render time. When all clips already match and there are no transitions or gain changes, export is a concat stream copy with no re-encode, and the UI says so.
- **R48** An exported sequence becomes a gallery item whose `inputs[]` are its clips, so lineage survives the edit. A timeline referencing an asset counts as a reference for reclaim.
- **R49** ffmpeg is bundled at a pinned version rather than expected on the machine, since "install ffmpeg first" is the friction helmstudio exists to remove.

## 10 · SDK packages

Three packages with a strict one-way dependency: `helm-css` knows nothing, `helm-runtime-sdk` knows the API, `helm-ui-sdk` knows both.

- **R50** `helm-css` ships tokens, base, layout and component layers as separate files and one bundle, plus `tokens.json`. Classes are prefixed, single-specificity and never `!important`, so a studio overrides with one rule.
- **R51** `helm-runtime-sdk` ships for Go, Python and Node, generated from one OpenAPI document so the three cannot drift. Provider selection is automatic: remote when `HELM_API` is set, embedded otherwise.
- **R52** The embedded provider is a separate module or extra, so a studio that only runs hosted never pulls SQLite into its dependency tree.
- **R53** `helm-ui-sdk` ships `helm-gallery`, `helm-player`, `helm-terminal` and `helm-timeline` as custom elements. **It never constructs a URL or sets a header** — it receives a runtime client — so an API change regenerates one package and leaves components untouched.
- **R54** All three are distributed three ways: package registries, prebuilt bundles served by the daemon for studios with no build step, and a vendored copy for standalone.
- **R55** A manifest pins majors separately (`sdk: { runtime, ui, css }`) and the daemon injects matching bundle URLs at launch, so updating helmstudio never jumps a studio across a major.
- **R56** Components degrade rather than explode: an unknown attribute is ignored, and an endpoint the provider cannot serve renders an empty state with a one-line reason.
- **R57** `helm dev` runs the embedded provider behind the real HTTP surface and exports `HELM_API`; `--fixtures` seeds a gallery with sample media and a populated inbox; `--fail=` injects quota, conflict and outage errors so failure paths are reproducible.
- **R58** `helm test` runs a studio's smoke test against an ephemeral embedded provider and asserts: health inside budget, at least one asset adopted, at least one gallery item with non-empty params, no writes outside the studio's roots, clean exit on SIGTERM.
- **R59** A conformance suite runs in CI twice — against the daemon over HTTP and against the embedded provider in-process — so "the two are substitutable" is proved rather than asserted.
- **R60** No package ever contains model-specific UI: prompt builders, parameter panels, model or LoRA pickers and scheduler controls stay in studios.

## 11 · Custom studios

- **R61** A user adds a studio from a local folder, a git URL, or the registry — three routes, one code path.
- **R62** Before anything executes, an approval screen shows the repo and ref, **every build command verbatim**, the weights and sizes, the capabilities requested as plain sentences, the declared network hosts and the certification level.
- **R63** Checks run before install: manifest validity, host requirements, capability review, theme conformance and the smoke harness. Required failures warn loudly and block a registry merge; expected failures never block a user installing their own work.
- **R64** A studio carries a certification level shown as a card chip — Draft, Unverified, Verified, Registry — which labels but never gates what a user runs on their own machine.
- **R65** Custom studios install at a pinned commit; auto-update is off below Registry level, and an update shows a command diff when build steps changed.
- **R66** There is no URL scheme or one-click web install. The add flow starts inside the app, every time.
- **R67** Uninstall is complete: no files outside the studio's roots, no orphaned processes, with cached weights left for the user to reclaim explicitly.
- **R68** `helm studio init` scaffolds a studio that passes the required criteria on a clean checkout: manifest, vendored css, SDK wired, health endpoint, one adopt-and-record call, a passing smoke test.

## 12 · The Mac app

- **R69** `helmstudio.app` bundles the daemon; the shell starts it, adopts an existing one if present, and on quit shuts it down cleanly or leaves it running when the user chose to keep studios alive in the menu bar.
- **R70** Native affordances: folder pickers for the models root, Reveal in Finder, a menu bar item showing the running studio with a Stop, notifications on install complete and failed, and Dock progress during downloads.
- **R71** Hardened runtime, Developer ID signature and notarisation for the app and the embedded binary, so first launch is not a quarantine dialog.
- **R72** Updates ship the shell, daemon and bundled registry together, which makes adding a studio to the catalogue a release.
- **R73** The headless binary stays first-class; no feature is reachable only from the app, and the Electron layer contains no business logic.

## 13 · The manifest

    id: h3-studio
    name: h3 studio
    kinds: [video, audio]
    hue: { dark: "#e0a33c", light: "#a06a10" }
    repo: https://github.com/janishar/h3c-studio
    submodules: true
    peak_ram_gb: 21
    license: MIT
    requires: { os: [darwin], arch: [arm64], tools: [git, make, cc, go], ram_gb: 32, disk_gb: 120 }
    runtime:
      framework: native-kernel        # native-kernel | mlx | pytorch | ggml | onnx | jax
      backends: [metal]              # metal | mps | cuda | rocm | cpu
      precision: [bf16, int8]
      language: c                    # the engine's own stack, shown as "h3.c"
    capabilities: [kv, assets, gallery, timeline]
    sdk: { runtime: "^1", ui: "^1", css: "^1" }
    network: [huggingface.co, cdn-lfs.huggingface.co]
    storage:
      collections: [{ name: takes, index: [seed, model, session], fts: [prompt] }]
      quota: { records: 100000, kv_bytes: 8388608 }
    build:
      - { name: Build the Metal engine, cwd: h3c, run: make mps }
      - { name: Build the studio server, cwd: ., run: go build -o dist/h3studio . }
    weights:
      - { name: h3, repo: MiniMaxAI/MiniMax-H3, dest: MiniMax-H3, size_gb: 60,
          files: [config.json, "*.safetensors"] }   # also the linked-directory check
    processes:
      - name: studio
        role: main
        heavy: true
        cmd: ./dist/h3studio --h3 ./h3c/h3 --model {models.h3} --port {port}
        port: { prefer: 8710 }
        health: { path: /healthz, timeout_s: 240, interval_s: 2 }
        ui: /
    test: { profile: tiny, smoke: ./test/smoke.yaml }

**The other three fit the same schema.** ltx and AuK declare `python.version`, which makes the daemon create a `uv` environment before the build; their run shape is the repo's own launcher. iris lists both checkpoints with `selectable: true` and references `{models.selected}`, so the shelf asks which to launch with and records the choice on the installation. A studio with an engine sidecar and an on-demand worker adds two more `processes[]` entries with `depends_on` and nothing else changes.**

### What the four launch studios declare

The values differ in every case, which is the argument for the field existing at all.

| Studio      | framework                | backends     | precision  | Reads as                 |
|-------------|--------------------------|--------------|------------|--------------------------|
| h3 studio   | `native-kernel` (h3.c)   | `metal`      | bf16, int8 | Native Metal · no Python |
| iris studio | `native-kernel` (iris.c) | `metal`      | bf16       | Native Metal · no Python |
| ltx studio  | `mlx`                    | `metal`      | bf16, int8 | MLX · quantised on load  |
| AuK studio  | `pytorch`                | `mps`, `cpu` | bf16       | PyTorch · mps            |

The distinction the field has to carry is not simply "Mac or not". A native Metal kernel and MLX are both Apple-native but behave differently; PyTorch on `mps` runs on the same machine as either but typically holds far more memory for the same model, which is the comparison ltx studio's own README makes. And PyTorch on `cuda` — the same framework, a different backend — will not run here at all. That is why `framework` and `backends` are separate fields rather than one label, and why the host check is against `backends`.

## 14 · Non-goals for v1

- No cloud or remote execution, no hosted mode, no proxying to anything that is not loopback.
- No multi-user, sessions, roles or sharing. One person, one machine.
- No account, sign-in or settings sync.
- No fine-tuning, LoRA training or dataset management at the launcher level.
- No cross-studio pipelines or orchestration. Handoff moves an asset; it does not chain generations.
- No Windows or Linux GPU support at launch; `requires.os`, `runtime.backends` and the directories helper keep the door open without a rewrite. Linux is a short hop — POSIX signals and process groups are identical, leaving packaging, the secret store and the video encoder. Windows is a genuine port: no process groups or signals (teardown becomes Job Objects), and `build[].run` assumes a POSIX shell.
- No plugin marketplace or third-party manifest hosting. Registry manifests land by pull request.
- No path-prefixed embedding of studios until they accept `--base-path`; a shared origin means a shared cookie jar and localStorage across every studio.
- No colour grading, keyframing, titles or effects in the timeline. Cuts, holds, one transition type, audio gain.
- No model-specific UI in any SDK package, permanently.

## 15 · Open decisions

| Decision            | Options                                                               | Lean                                                                                                                                                                     |
|---------------------|-----------------------------------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| ffmpeg licensing    | LGPL build with videotoolbox, or a GPL build with x264                | LGPL plus videotoolbox — also the fast encoder on Apple Silicon, and it avoids a GPL question in a signed MIT app. Decide before the first release.                      |
| Dedup headline      | File-level content addressing with hardlinks, or drop the claim       | Check whether the four manifests share a single Hugging Face repo before promising a number.                                                                             |
| Proxy               | Add `--base-path` to all four studios, or open each at its own origin | Own origin now; the flag is a week spread across four repos you already own, versus an HTML-rewriting proxy.                                                             |
| Vendoring `uv`      | Pinned binary in the bundle, or detect and instruct                   | Ship it in the app, detect for the headless binary.                                                                                                                      |
| Weight verification | Per-file SHA256, or size and etag                                     | Size and etag on download; SHA256 behind an explicit Verify, since hashing 60 GB costs minutes.                                                                          |
| Preemption          | Stop on confirm, or queue until the user stops                        | Stop on confirm, with an optional `busy` probe so a studio can report a generation in flight.                                                                            |
| Accent collision    | Yellow accent sits near h3 studio's amber identity hue                | Keep them separable by role — identity hues only as stripes and dots, the accent only as the single primary fill — and keep the accent a brighter lemon than h3's amber. |

## 16 · Milestones

**M0 — Contract and registry.** The manifest schema and the OpenAPI document are written, all four studios have manifests, and the daemon loads, validates and lists them. *Demo:* the shelf shows four cards with correct requirement badges; a malformed manifest is rejected by field name while the others still list; `helmstudio validate` prints the same error from the command line.**

**M1 — Supervision, proven with h3 studio.** Process groups, port assignment, health with a real timeout, stop, crash detection, re-adoption after a daemon restart, log streaming. *Demo:* launch h3 studio, generate a video, kill the daemon with `-9`, restart it, and watch the running studio be re-adopted rather than orphaned.**

**M2 — Installer and weights.** Clone with submodules, ordered build steps with resume-from-failed-step, resumable downloads into the shared cache, disk pre-checks, the cache view. *Demo:* h3 studio goes from `listed` to a generated video on a machine that has never seen the repo, with the build interrupted once and resumed.**

**M3 — Store, assets and gallery.** SQLite, the asset store with hardlink adoption and a readable library tree, derived thumbnails and proxies, the gallery with provenance, `fsck`. *Demo:* h3 studio records takes through the runtime SDK; the launcher shows them; an image from one becomes the first frame of another via handoff.**

**M4 — The Python path and the packages.** ltx and AuK with per-studio `uv` environments; `helm-css`, `helm-runtime-sdk` for all three languages, `helm-ui-sdk` with gallery, player and terminal; `helm dev` and `helm test`. *Demo:* all four studios installed and themed consistently; a studio's take list replaced by `<helm-gallery>` in an afternoon.**

**M5 — Timeline and export.** The timeline document, the editor, the conform-and-concat pipeline with the stream-copy fast path, export as a Job. *Demo:* a fourteen-second sequence assembled from three studios with an AuK voice line, exported and appearing in the gallery with its clips as inputs.**

**M6 — Custom studios and the Mac app.** The add flow with the approval screen, the criteria checks and certification levels, `helm studio init`; the Electron shell, signing, notarisation and the update feed. *Demo:* a notarised `.dmg` on a clean Mac — install a studio from a git URL after reading exactly what it will run, quit the app with the studio still alive in the menu bar, then take an update that ships a fifth studio.**
