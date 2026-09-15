# h3 studio SDK readiness

> Frozen design, 15 Sep 2026. This file is the contract.
> Do not edit to make an implementation pass — raise a finding instead.

Read against the working tree at `~/GenAI/minimax-h3-mlx/h3c-studio`. The conclusion is better than expected: the hard part — the process contract the daemon depends on — is already satisfied, because the studio was written with the same instincts. The remaining work is four changes, none structural.

**5,224**lines of Go, zero third-party dependencies**

**1**call site needed for asset + gallery**

**45**CSS variables already defined**

**64**raw colour literals still outside them**

## Verdict

Fits

Every daemon-side requirement that would have been expensive to retrofit — port flag, loopback bind, SIGTERM handling, process groups, a relocatable data root, no build step, gitignored state — is already there. The studio can be launched, supervised, stopped and re-adopted by helmstudio today with no code change at all.

Four changes

Add a health endpoint; make `--root` mandatory in the manifest; add one adopt-and-record call in `finishTake`; rename CSS variables to helm tokens. Roughly a day's work, most of it the CSS pass.

One design finding

h3 studio holds no model at rest in one-shot mode but *does* in interactive mode. The one-heavy-studio-at-a-time rule cannot be evaluated from process liveness alone — this is real evidence for the `busy` probe that was left as an open decision.

## What already fits, verified in the code

| Requirement                                   | Evidence                                                                                              |      |
|-----------------------------------------------|-------------------------------------------------------------------------------------------------------|------|
| R22 · accepts an assigned port                | `flag.Int("port", 8710, …)`                                                                           | pass |
| R31 · binds loopback only                     | `flag.String("host", "127.0.0.1", …)`, warns when bound wider                                         | pass |
| Criterion 7 · clean SIGTERM exit              | `signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)` → `srv.Shutdown(5s)` → `runner.Shutdown()`      | pass |
| R26 · process-group teardown                  | `cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}` on *every* child — render, interactive, shell | pass |
| Criterion 6 · writes only under its own roots | `--root` flag controls the `sessions/` location; nothing writes elsewhere                             | pass |
| R36 · update survives `git pull`              | `.gitignore` has `sessions/**`, `/dist/` — no untracked-path conflict                                 | pass |
| Zero-build front end                          | `//go:embed static` with a `--dev` disk mode                                                          | pass |
| Events for the SDK stream                     | `GET /api/events`, an existing SSE broker                                                             | pass |
| Host-header validation                        | `--allow-host`, names rejected by default — correct, since DNS rebinding needs a name                 | pass |
| Light and dark already designed               | `:root`, `prefers-color-scheme` and `[data-theme]` blocks all present                                 | pass |

The last two are the pleasant surprises. The theme structure already matches the three-state pattern the design system specifies, so that migration is a rename rather than a rewrite. And the process-group handling being right on all three spawn sites means the supervisor's hardest guarantee — no orphans after a stop — holds without touching the studio.

## What needs changing

| \#  | Change                                                         | Why                                                                                                                                                                                                                                                         | Size          |
|-----|----------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|---------------|
| 1   | Add `GET /healthz` returning 200 unconditionally               | The manifest declares it and there is no such route. It must be *dumb* — not `/api/config`, which can fail on a missing model directory and would make a recoverable state look like a dead process.                                                        | ~8 lines      |
| 2   | Pass `--root` explicitly from the manifest                     | Default discovery puts `sessions/` inside the checkout. It is gitignored so updates are safe, but a reinstall or uninstall would take the user's takes with it. Point it at `~/.helmstudio/studios/h3-studio/data` and the studio's data outlives its code. | manifest only |
| 3   | One adopt-and-record call in `finishTake`                      | The single place a finished take is moved into `outputs/`. See below — the metadata is already assembled.                                                                                                                                                   | ~15 lines     |
| 4   | Map 45 CSS variables to helm tokens; clear 64 raw literals     | `helm validate` fails on literals outside the vendored token file. Mostly mechanical.                                                                                                                                                                       | half a day    |
| 5   | Keep `--allow-shell` off, and never declare it in the manifest | `POST /api/shell` runs `/bin/sh -c` in the work directory. It is off by default and should stay a local-development flag; a studio that shipped it enabled would fail review.                                                                               | policy        |

## The adopt point is a single function

`finishTake` already moves the produced file from a private job directory into `outputs/` under a unique name. That is exactly one call site, and everything the platform needs is in scope at that moment:

    // server/runner.go — after the existing move into outputs/
    final := uniquePath(outputs, stem, ".mp4")
    // … existing rename …

    if r.helm != nil {                                  // nil when running standalone
        as, err := r.helm.Assets.Adopt(ctx, final, helm.Video)
        if err == nil {
            r.helm.Gallery.Add(ctx, helm.Item{
                Kind: helm.Video, Asset: as.ID, Session: job.Session,
                Title: job.Params.Label, Params: job.Params,   // already a struct
                Inputs: inputsFrom(job.Params.Refs),           // refs[] → first_frame / reference
            })
        }
    }

Two details make this cheap. The file is already final and named before this point, so `Adopt` hardlinks rather than copies — a 20 MB take costs two inode operations. And the studio already writes a sidecar JSON next to every take, so nothing new has to be computed; the same structure goes into `params`.

## The sidecar maps to gallery params almost one-to-one

An existing take JSON from the `example` session already carries everything the gallery wants:

| In the sidecar today                                                           | Gallery field                                                                                                                                               |
|--------------------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `params.seed`, `steps`, `width`, `height`, `frames`, `layers`, `reuse`, `mode` | `params` verbatim                                                                                                                                           |
| `params.prompt`                                                                | `params.prompt` — and the FTS index, which is what makes "that café video" findable                                                                         |
| `probe.duration`, `width`, `height`, `fps`, `frames`, `has_audio`              | `assets` columns — no ffprobe call needed, it is already done                                                                                               |
| `duration_s`, `started`, `finished`                                            | job timing                                                                                                                                                  |
| `params.refs[]`                                                                | `item_inputs` with role `first_frame` / `last_frame` / `reference` — real provenance from day one                                                           |
| `profile[]` (per-component wall times)                                         | Keep in `params`. Nothing else models it, and it is the most interesting thing in the file.                                                                 |
| `command` / `command_display`                                                  | **Do not copy verbatim.** Absolute paths to the model directory and the checkout make it non-portable and leak the user's home directory into a shared row. |

The importer for existing takes is therefore straightforward, and it will produce a populated gallery on first launch rather than an empty one — which is the difference between the feature looking finished and looking hypothetical.

## Theme migration is a rename

The stylesheet already defines 45 variables in a `:root` block, redefines them under `prefers-color-scheme` guarded against an explicit choice, and again under `[data-theme="light"]` — the exact three-state pattern the design system requires. The work is mapping names, not restructuring:

| h3 studio                            | helm token                                                                          |
|--------------------------------------|-------------------------------------------------------------------------------------|
| `--bg` `#0b0e14`                     | `--helm-ground-page`                                                                |
| `--pane` / `--raised` / `--raised-2` | `--helm-ground-panel` / `-raised`                                                   |
| `--line` / `--line-hi`               | `--helm-border-hairline` / `-strong`                                                |
| `--ink` / `--ink-2` / `--ink-3`      | `--helm-text-primary` / `-secondary` / `-muted`                                     |
| `--amber` `#ffb454`                  | `--helm-studio-accent` — **h3 keeps its amber**, injected from the manifest's `hue` |
| `--bad` / `--ok`                     | `--helm-status-error` / `-running`                                                  |
| `--term-*` (4 vars)                  | `--helm-ground-inset` plus log tokens                                               |

The 64 remaining raw literals are the real task, and most are shadows and overlay tints — `rgba(0,0,0,.5)`, `rgba(255,180,84,.12)` — which become `color-mix()` over tokens. Note the studio's dark ground is blue-cast (`#0b0e14`) where helmstudio's is warm (`#0d0c0a`); adopting tokens shifts the studio slightly warmer, which is the point of a shared palette but is a visible change worth expecting rather than discovering.

## Timeline: the framework version is strictly better

h3 studio already has `CombineTimeline`, and it builds a `filter_complex … concat=n=N:v=1:a=1` graph. That always re-encodes, even when every clip is a take from the same session at identical settings — which is the overwhelmingly common case here. The framework pipeline takes the concat-demuxer stream-copy path in exactly that situation, so the same four-take sequence goes from a re-encode to a few seconds.

So this is a replacement, not a coexistence: `POST /api/timeline/render` gives way to `POST /timeline` plus `:open`, and the studio's clip model — paths relative to the sessions root — maps to asset ids through the importer. Worth doing in that order, though: keep the existing timeline until assets and the gallery are live, because the framework's version needs asset ids to exist first.

## The finding that changes a decision

Liveness is not occupancy

h3 studio's server is a thin wrapper — it starts in milliseconds and holds nothing. The model is loaded by the `h3` binary per render in one-shot mode, and *kept resident* in interactive mode via `POST /api/interactive/load`. So a running h3 studio process may be holding 21 GB or holding nothing, and the daemon cannot tell from the outside.

Two consequences. First, the health timeout of 240 s in the draft manifest is wrong for this studio — it answers instantly, and a generous timeout would only delay reporting a genuinely dead process. It should be 30 s here; the long budgets belong to studios that load at startup, like ltx and AuK.

Second, and more importantly, the one-heavy-studio-at-a-time rule needs the optional `busy` probe that was left open. Without it, switching studios either interrupts a render that is mid-flight or refuses when nothing is actually loaded. With it, the manifest declares an endpoint the daemon can ask — h3 studio would answer from its interactive state and its queue — and the switch dialog can say "h3 studio is 40 % through a render" instead of guessing. I would now close that open decision in favour of shipping the probe.

## The `sessions/` directory: what moves and what does not

Measured on the working tree — four sessions, 74 MB total, of which **55 MB is media across 48 files and only 984 KB is state**. That ratio is the whole answer in miniature: the bytes worth centralising are the outputs, and the state is small enough that where it lives barely matters.

It also writes safely already. `writeJSONAtomic` does `os.CreateTemp` in the same directory followed by `os.Rename` — the identical write-temp-then-rename the SDK's `kv` uses. So there is no crash-safety argument for moving anything, which removes the usual reason to migrate a store.

| What's there                         | Size              | Where it belongs                                                                                                                                                                                                                     |
|--------------------------------------|-------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `sessions/h3.json`, `model.json`     | tiny              | **Becomes redundant.** These remember the binary and model paths so later runs can omit the flags — which is precisely what the daemon now supplies from the manifest and the resolved weight. Hosted, they are written and ignored. |
| `sessions/last_session.json`         | tiny              | Genuine UI state → `kv/ui/last_session` if it ever needs to be, but nothing needs it yet.                                                                                                                                            |
| `<session>/setting.json`             | 1.9 KB × n        | **Moves** to a `sessions` row, its 23 keys carried in `state`. The studio's four session handlers — activate, save, delete, duplicate — become SDK calls.                                                                            |
| `<session>/outputs/*.mp4` + `.json`  | most of the 55 MB | **Mirrored** to `assets` + `items`, with the sidecar becoming `params` and `refs[]` becoming `item_inputs`. Files stay where they are; adoption hardlinks. (Amended 2026-09-15 by the M4 first review, #4 and #15: this holds because `outputs/` is under `--root {data}`, which adoption accepts; an adopted take becomes read-only, since it shares the blob's inode.) |
| `<session>/inputs/`                  | —                 | Assets with `pinned = 1`. These are things the user dragged in; reclaim must never offer to delete them.                                                                                                                             |
| `<session>/timeline/*.json` + `.mp4` | —                 | Framework `timelines` rows; the rendered file becomes an asset and an item with its clips as inputs.                                                                                                                                 |
| `<session>/previews/`                | —                 | Derived, regenerable. Not adopted, not backed up, safe to delete — the same category as thumbnails.                                                                                                                                  |
| `<session>/terminal.log`             | 6 KB, unbounded   | A log, not state. Falls under the retention policy: last five per studio, head-and-tail truncated.                                                                                                                                   |

The rule this settles

**All state goes through the SDK; bytes stay in directories.** An earlier draft of this page argued that form state should stay on disk because moving it would break standalone — that was wrong, because the embedded provider writes to `./.helm/helm.db` with the same schema and the same API, so a studio that always calls the SDK is standalone-safe by construction. What survives on disk is what was always meant to: media, logs and regenerable previews. h3 studio's own store is careful and correct, and it still goes, because being the only studio that got it right is not a property that scales to ten.**

Two consequences worth acting on. First, centralising state solves the `--root` problem structurally rather than by flag: rows live in the database and media is adopted into the asset store, so nothing the user cares about sits in a checkout a reinstall would delete. `--root` still matters for the working directory during the transition, and for standalone runs. Second, the studio calls `cfg.SetH3()` and `cfg.SetModel()` on every start, so running under helmstudio quietly rewrites `h3.json` and `model.json` with whatever the daemon passed. That is benign and even self-healing when a weight is relinked to another drive, but it means the studio's remembered paths follow helmstudio — worth knowing before someone reports it as a bug.

## The manifest this implies

    id: h3-studio
    name: h3 studio
    kinds: [video, audio]
    hue: { dark: "#ffb454", light: "#b5721a" }      # its own amber, unchanged
    repo: https://github.com/janishar/h3c-studio
    submodules: true
    peak_ram_gb: 21
    requires: { os: [darwin], arch: [arm64], tools: [git, make, cc, go], ram_gb: 32, disk_gb: 120 }
    capabilities: [kv, assets, gallery, timeline]
    sdk: { runtime: "^1", css: "^1" }                 # no ui-sdk: it has its own interface
    build:
      - { name: Build the Metal engine, cwd: h3c, run: make mps }
      - { name: Build the studio server, cwd: ., run: go build -o dist/h3studio . }
    weights:
      - { name: h3, repo: MiniMaxAI/MiniMax-H3, dest: MiniMax-H3, size_gb: 60 }
    processes:
      - name: studio
        role: main
        heavy: true
        cmd: ./dist/h3studio --h3 ./h3c/h3 --model {models.h3} --port {port} --root {data}
        port: { prefer: 8710 }
        health: { path: /healthz, timeout_s: 30, interval_s: 1 }   # instant, not 240s
        busy:   { path: /api/queue }                              # liveness ≠ occupancy
        ui: /
    test: { profile: tiny, smoke: ./test/smoke.yaml }

Two new template variables fall out of this: `{data}` for the studio's persistent data root, which the manifest schema does not currently have, and `busy` as a sibling of `health`. Both are small additions and both are better discovered here than after four studios have shipped.
