# SDK and Custom Studios

> Frozen design, 15 Sep 2026. This file is the contract.
> Do not edit to make an implementation pass — raise a finding instead.

The SDK is an API client and a test harness — nothing that draws pixels. Studios own their own interfaces, the framework owns the timeline, and the SDK's entire job is to let someone build and test a studio in isolation, without helmstudio running and without a 60 GB checkpoint on disk.

## 1 · What the SDK is

Superseded

An earlier revision of this page argued for an API-only SDK with no UI. That is now split three ways instead: `helm-css` for consistency, `helm-runtime-sdk` for communication and state, and `helm-ui-sdk` for prebuilt gallery, player, terminal and timeline — with `helm-ui-sdk` consuming the runtime rather than talking HTTP itself. The package architecture document is the authority on the boundaries; this page covers the runtime, isolated development, and custom studios.

| Package                               | Gives a studio                                                                                                                          | Depends on                                       |
|---------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------|--------------------------------------------------|
| `helm-css`                            | Tokens, base, layout and component classes in both themes. No JavaScript.                                                               | Nothing.                                         |
| `helm-runtime-sdk` · go, python, node | Typed client for state, records, assets, gallery, jobs, timeline and events; provider selection; the dev and test tooling in §4 and §5. | The API contract.                                |
| `helm-ui-sdk`                         | `helm-gallery`, `helm-player`, `helm-terminal`, `helm-timeline` as custom elements.                                                     | `helm-css` and the browser build of the runtime. |

Two boundaries hold the rest together. **Only the runtime SDK knows the wire format** — a component receives a client and calls methods on it, never constructs a URL — so an API change regenerates one package and leaves the components untouched. And **nothing is required**: a studio may take all three, only css, or none at all and still install, launch and run. That last case is the proof helmstudio is still a launcher rather than a framework people must buy into.

What stays out of every package, permanently: prompt builders, parameter panels, model and LoRA pickers, scheduler controls. Those are where studios differ from one another and where their authors' judgement lives; the generic version would be worse than every specific one. A component earns its place only by being genuinely identical everywhere — a log stream, a video transport, a list of past outputs, a sequence.

## 2 · Two providers, one contract

Every capability is declared once as an interface with two implementations. A studio picks one at construction and never branches again.

Because the schema and the directory layout are identical, a studio that ran alone for months is *adopted* rather than migrated when helmstudio arrives: the daemon hardlinks the blobs and replays the rows. Keep the embedded provider a separate package — `helmsdk/embedded` in Go, `helmsdk[embedded]` in Python — so a studio that only ever runs hosted never pulls SQLite into its dependency tree.

## 3 · API surface

| Endpoint                                                                                                           | Does                                                                                                                  |
|--------------------------------------------------------------------------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------|
| GET PUT PATCH /kv/{ns}/{key} | Small JSON documents — settings, session state. `ETag` and `If-Match`.                                                |
| POST GET /records/{collection}                            | The studio's own domain documents, queried with a closed filter language.                                             |
| POST /assets · /assets:adopt                                                          | Register media. Adopt hardlinks a file already written to the stage directory — no copy, no HTTP body.                |
| GET /assets/{id} · /thumb                                                              | Bytes with `Range`; thumbnails, posters and waveforms generated once by the framework.                                |
| POST GET /gallery/items                                   | Record an output with params and `inputs[]` provenance; query at studio or global scope.                              |
| POST /timeline · /timeline/{id}:open                                                  | Create a sequence from clips and hand it to the framework's editor. See §6.                                           |
| POST GET /jobs                                            | Long work with progress and a log file — the studio's own renders can use it too, so a studio need not build a queue. |
| POST /handoff · GET /inbox                                | Send an item to another studio; drain what was sent here.                                                             |
| GET /events                                                                            | SSE: theme changed, inbox arrived, job progressed, shutting down.                                                     |
| GET /me                                                                                | Studio id, capabilities, quota, paths, provider, SDK version.                                                         |

    c := helm.FromEnv()                       // remote if HELM_API is set, else embedded
    as, _ := c.Assets.Adopt(ctx, outPath, helm.Video)
    c.Gallery.Add(ctx, helm.Item{Kind: helm.Video, Asset: as.ID, Params: p,
        Inputs: []helm.Input{{Asset: firstFrame, Role: "first_frame"}}})

## 4 · Developing in isolation

The test of an SDK is whether someone can build against it on a laptop with no model weights, no daemon and no network. Everything here exists to make that true.

| Tool                                     | What it does                                                                                                                                                                                                                             |
|------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `helm studio init <name>`                | Scaffolds a studio: manifest, vendored tokens, SDK wired, a health endpoint, one working adopt-and-record call, and a smoke test that passes on a clean checkout.                                                                        |
| `helm dev`                               | Runs the embedded provider behind the real HTTP surface on a local port and exports `HELM_API`, so the studio runs exactly as it would under helmstudio — same endpoints, same tokens, same capability scoping, no daemon.               |
| `helm dev --fixtures`                    | Seeds a gallery with sample images, clips and audio, plus a fake sibling studio and a populated inbox. A gallery view can be built before the model is downloaded, which is the difference between a pleasant SDK and one nobody adopts. |
| `helm dev --fail=assets:500,gallery:429` | Fault injection. The paths studios get wrong are quota rejections, etag conflicts and a daemon that vanishes mid-render; this makes those reproducible instead of theoretical.                                                           |
| `helm validate`                          | Manifest schema, capability sanity, theme lint, contrast check. Advisory locally, blocking in registry CI.                                                                                                                               |
| `helm doctor --sdk`                      | Prints which provider would be selected here and why — the first question every integration bug asks.                                                                                                                                    |

The whole loop is: `helm studio init`, write your generation code against the client, `helm dev --fixtures`, build your UI against real data, then run under the real daemon once and expect no surprises — because it is the same HTTP surface either way.

## 5 · Testing in isolation

Two implementations of one interface will drift, and a studio built against a mock that lies is worse than no mock at all. Three layers keep that honest.

- **The contract.** One OpenAPI document in the helmstudio repo generates the Go, Python and JS clients, so an endpoint cannot exist in one language and not another, and a renamed field breaks a build rather than a user.
- **The conformance suite.** A few hundred cases covering etag conflicts, pagination cursors, quota rejections, asset dedup, provenance queries and adoption semantics. It runs twice in CI: once against the daemon over HTTP, once against the embedded provider in-process. "The two are substitutable" is worthless asserted and cheap to prove.
- **The studio harness.** `helm test` spins an ephemeral embedded provider in a temp directory, runs the studio's own smoke test against it, and asserts the outcomes the framework cares about: the process came up and answered health inside its budget, at least one asset was adopted, at least one gallery item was recorded with non-empty params, nothing was written outside the studio's roots, and the process group exited cleanly on SIGTERM. That same harness is what certification runs in §9, so passing locally means passing there.

<!-- -->

    # a studio's CI, with no weights and no daemon
    helm validate
    helm test --smoke ./test/smoke.yaml --timeout 120s
    # → ✓ health in 4.2s  ✓ 1 asset adopted  ✓ 1 item recorded  ✓ clean exit  ✓ no stray writes

A studio whose real work needs a 60 GB checkpoint declares a `tiny` profile in its manifest — a smaller model, two denoising steps, a 64-frame clip — so that the smoke test exercises the real code path in two minutes on a CI runner. Without that, nothing about a generative studio is testable in CI and every regression is found by a user.

## 5a · From SDK-only to helmstudio, without a rewrite

A studio must be buildable with the SDKs alone — no helmstudio installed, no daemon, no registry — and then join helmstudio without changing a line. That is not a nice-to-have; it is what makes a studio an independent repo rather than a plugin. Four things make it true, and the first is the one that carries the rest.

`helm dev` is the daemon restricted to one studio, not a separate tool.

Same supervisor, same manifest parser, same template substitution, same embedded store behind the same HTTP surface — it simply loads one manifest from a local path instead of a registry. Parity is then structural rather than something two codebases have to keep agreeing on, and the first real install stops being the first real test.

| What                                              | Why it closes the gap                                                                                                                                                                                                                                                        |
|---------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| **The manifest is exercised from day one**        | `helm dev` reads the studio's own `helmstudio.yaml`, spawns its `processes[]`, assigns a port, substitutes `{port}`, `{models.x}` and `{data}`, and health-checks it. A manifest written blind and first executed at install time is a manifest that breaks at install time. |
| **Weights are linked, not downloaded**            | An author already has the checkpoint. `helm dev` honours a linked directory exactly as the daemon does, so nothing is fetched and the same `{models.x}` path resolution is exercised.                                                                                        |
| **Absent framework surfaces behave as they will** | Dev mode does not stub what it lacks. `POST /timeline/{id}:open` returns `501` with a reason, exactly as it would under a daemon with no window open, so the author writes the degradation path while developing rather than discovering it after shipping.                  |
| **Readiness is a live number**                    | `helm doctor --studio` scores the fifteen criteria against the working tree on demand. Certification becomes a feedback loop during development instead of a surprise at submission.                                                                                         |

### The manifest lives in the studio repo

This is a change from the earlier design, and it is what makes the handover seamless. `helmstudio.yaml` sits at the root of the studio's own repository — the author edits it beside the code it describes, `helm dev` runs it, `helm validate` checks it, and CI gates on it. The helmstudio registry then holds **pointers**, not copies:

    # studios/h3-studio.yaml in the helmstudio repo — the whole registry entry
    id: h3-studio
    repo: https://github.com/janishar/h3c-studio
    ref: v1.4.2                  # the reviewed commit; the manifest that runs is the one reviewed
    certified: verified

Two copies of a manifest drift; one does not. It also removes the oddity where a studio shipping new code could not announce a new build step without a helmstudio release, and it means "add from a git URL" and "installed from the registry" read the identical file — the only difference is whether a human reviewed the ref.

### But the author is not the only person who can write one

Most repos worth wrapping will never carry a `helmstudio.yaml`.

If the in-repo manifest were the only source, adoption would be gated on upstream cooperation — the ComfyUI custom-node problem in reverse, where nothing runs until someone else does work. So a manifest is a **description anyone can write about a repo**, not a file only its author can provide. A user fills the fields in helmstudio, it joins the library beside the prebuilt entries, and it behaves identically from that moment on.

Three sources, one shape, first match wins:

| Precedence | Source                                                | Written by                      | Shown as                              |
|------------|-------------------------------------------------------|---------------------------------|---------------------------------------|
| 1          | `~/.helmstudio/studios/<id>.yaml`                     | You, in the app or in an editor | Local         |
| 2          | `helmstudio.yaml` at the repo root, at the pinned ref | The studio's author             | From repo |
| 3          | An inline manifest carried by the registry pointer    | helmstudio, for repos with none | Registry   |

A local manifest with the same `id` as a registry entry **overrides it**, and the card says so with a Revert action — which is how someone patches a broken upstream manifest, or adds a flag their machine needs, without waiting for anyone. Duplicate-and-edit on any entry copies it to local under a new id, so forking a registry studio to point at different weights costs one click and breaks nothing.

### The catalogue is a library, not an install list

Once manifests arrive from three places the shelf stops being "what ships with helmstudio" and becomes **the set of studios this machine knows about**, of which the installed ones are a subset. Each entry carries its source, its certification level and its install state, and the three are independent: a Local entry can be Verified if it passes the harness, and a Registry entry is still just a description until someone installs it.

The library is still derived at startup from files — bundled pointers, the local directory, and fetched manifests cached under `~/.helmstudio/cache/manifests/<id>@<ref>.yaml`. No table, no sync, and a user can hand-edit or version-control their local manifests like any other config.

### Four ways a manifest arrives

| Route             | How                                                                                                                | Good for                                                                              |
|-------------------|--------------------------------------------------------------------------------------------------------------------|---------------------------------------------------------------------------------------|
| Registry          | Ships with helmstudio as a pointer; the manifest is fetched from the repo at the reviewed ref.                     | The curated set.                                                                      |
| From a repository | Paste a git URL; the manifest is read from `helmstudio.yaml` at the root, or written by hand if the repo has none. | Anything on GitHub.                                                                   |
| **Import a file** | Drop a `.yaml` on the window, pick it, paste the text, or give a URL. Several at once is fine.                     | Someone sent you one; you keep yours in a dotfiles repo; you are moving to a new Mac. |
| Write it here     | The editor below.                                                                                                  | A repo nobody has wrapped yet.                                                        |

Import is deliberately symmetric with export, and that symmetry is the whole community loop. Someone gets a new model running on their Mac, exports the manifest, posts it in an issue or a gist; the next person drops that file onto helmstudio and has the same studio — no marketplace, no plugin format, no registry round trip. If it turns out to be good, the same file becomes a pull request.

Two rules keep it safe. **Import is not install** — an imported manifest joins the library as a description, and installing it from a repository the user does not own still goes through the approval screen with every build command shown verbatim. And an import whose `id` already exists offers Override, Rename or Cancel rather than quietly replacing an entry someone depends on.

### Writing one by hand, in the app

The editor is a form over the schema with a raw YAML pane beside it, both live and both editable, because anyone writing a build step wants the text and anyone filling in requirements wants the fields. Validation runs as you type — the same `helm validate` the registry gates on — and the criteria checklist updates underneath, so the author can see thirteen of fifteen passing and what the other two are. **Test** runs the smoke harness without installing; **Save** writes the local file; **Export** hands back the file to commit to the repo or open as a pull request.

That export is the whole contribution path, and it works because there is only one format. A manifest someone wrote to get a model running on their own Mac is, unchanged, the manifest the repo can adopt and the registry can review. "It works on my machine" becomes a pull request with no translation step.

What this does and does not change about trust

Authoring a manifest is not the risk — you wrote it, and you can read every command in it. Running an unfamiliar repo is. So the approval screen still appears when installing from a repository you do not own, and it reports the *repo's* standing, not the manifest's origin. A hand-written manifest pointing at your own checkout needs no ceremony; the same manifest pointing at a stranger's repository needs all of it.

### Adoption: the data comes too

Everything `helm dev` wrote is already in the shape the daemon expects, because it is the same schema and the same directory layout. `helm adopt` — run explicitly, or offered on first hosted launch — imports it: blobs are hardlinked into the shared store rather than copied, rows are replayed, and assets deduplicate by hash so running it twice changes nothing. It is idempotent, it has a `--dry-run` that prints what would move, and it refuses a schema newer than the daemon knows rather than silently dropping fields it cannot read.

    # the whole lifecycle of a studio, with helmstudio entering only at step 5
    helm studio init wan-studio          # scaffold: manifest, css, SDK, health, smoke test
    helm dev --fixtures                  # supervise it; seeded gallery; real HTTP surface
    helm doctor --studio                 # 13 of 15 criteria pass, two named
    helm test                            # smoke: health, adopt, record, clean exit
    # ── ship it ──────────────────────────────────────────
    git push                             # helmstudio.yaml goes with the code
    # in helmstudio: Add studio → From a repository → approval screen → install
    helm adopt --dry-run                 # optional: bring six months of local work across

Nothing in the studio changes between step 2 and step 5. The provider swaps behind the SDK, the supervisor is the same code, and the manifest is the same file.

## 6 · The timeline belongs to the framework

Sequencing is inherently cross-studio, so it cannot live inside any one studio.

A sequence built from an h3 clip, an ltx clip, an iris still and an AuK voice line is not h3 studio's document. Putting the editor in the framework means one implementation, one export pipeline, one set of ffmpeg decisions, and a sequence that survives uninstalling the studio that made half of it. Studios do not embed it — they hand clips to it and get back an asset.

café sequence 1920×1080 · 24 fps · 48 kHz · 00:14.16  stream copy · no re-encode

Add from gallery

Export

0s2s4s6s8s10s12s14s

V1*video*

**take-0915-052505***0–5.04s · h3***

**ltx-drift-04***5.04–9.1s · ltx***

**klein-still-11***hold 2.2s***

**take-0915-053255***11.3–14.16s***

A1*dialogue*

A2*ambience*

A framework screen. Clips carry the identity hue of the studio that produced them, so a sequence assembled from three studios reads as exactly that.

### The document

    { "id": "tl_01JB9…", "name": "café sequence",
      "target": { "width": 1920, "height": 1080, "fps": 24, "sample_rate": 48000 },
      "tracks": [
        { "kind": "video", "clips": [
          { "asset": "as_01JB7…", "in": 0, "out": 5.04, "at": 0 },
          { "asset": "as_01JB8…", "in": 1.20, "out": 5.26, "at": 5.04,
            "transition_in": { "type": "dissolve", "duration": 0.25 } },
          { "asset": "as_01JBA…", "hold": 2.2, "at": 9.10 } ]},
        { "kind": "audio", "gain_db": -3, "clips": [ … ] } ] }

**Non-destructive by construction.** A clip is a reference with in and out points; sources are never modified or copied. Every edit is a patch, so undo is the previous revision, and a timeline referencing an asset counts as a reference for reclaim — the disk page can never offer to delete footage a sequence is using. Export creates a Job, which means it reuses install's progress, cancellation and log file rather than inventing a second progress system, and the result becomes a gallery item whose `inputs[]` are every clip that went into it, so lineage survives the edit.**

### Export, stated precisely

Clips from four studios differ in resolution, frame rate, pixel aspect, colour range and sample rate, and a naïve concat produces something subtly broken — a green first frame, half a second of wrong-pitch audio. The timeline declares a target up front and every clip is conformed to it; nothing is inferred per clip at render time. When every clip already matches the target and there are no transitions or gain changes, export is a concat demuxer **stream copy** — seconds, no re-encode, bit-identical picture — which for a run of takes from one studio is the common case, and the UI says so, so people learn which edits are cheap. Otherwise it is one filter graph: scale with pad, `fps`, `setsar=1`, explicit colour range, `aresample` with `async`, then `concat` — one pass, no intermediate files.

ffmpeg becomes load-bearing, and needs a licensing decision

Thumbnails, posters, waveforms and playback proxies already needed it; the timeline makes it essential. Bundle a pinned build rather than hoping one is installed — "install ffmpeg first" is exactly the friction helmstudio exists to remove. And the build matters: linked against x264 it is GPL, which is a real question for an MIT app shipping a signed `.dmg`. The clean answer is an LGPL build using videotoolbox for h264, which on Apple Silicon is also the fast one. Decide before the first release.

## 7 · The launch API

A studio hands clips to the framework and gets back a sequence. It never renders a track, parses a codec or shells out to ffmpeg.

    # create a sequence from what the studio just made
    POST /api/v1/timeline
    { "name": "café sequence", "target": { "fps": 24, "width": 1920, "height": 1080 },
      "clips": [ { "asset": "as_01JB7…" }, { "asset": "as_01JB8…", "in": 1.2, "out": 5.26 } ] }
    → { "id": "tl_01JB9…", "duration_s": 9.30 }

    # ask the framework to open it — a launcher window, or a tab the daemon focuses
    POST /api/v1/timeline/tl_01JB9…:open
    → { "opened": true, "surface": "app" }

    # append to whatever sequence the user has open, without stealing focus
    POST /api/v1/timeline:append   { "asset": "as_01JBB…", "track": "V1" }

    # exports are Jobs, so progress and logs come back on the channel that already exists
    POST /api/v1/timeline/tl_01JB9…/export  { "preset": "h264-1080p24" }
    → { "job": "jb_01JBC…" }        # poll /jobs/{id} or subscribe on /events

In a studio's UI this is one button — "Add to timeline" — and a toast. When the provider is embedded and no framework is present, `:open` returns `501` with a clear reason and the studio simply hides the button; the sequence is still created and still exports, because export is the embedded media engine doing the same work headlessly. That is the pattern for every framework-only surface: the data operation always works, the presentation is what degrades.

## 8 · Adding a custom studio

Someone builds a studio for a model nobody has wrapped yet and wants it in their own helmstudio. Three routes, one code path.

| Route        | How                                                                                                                                              | For                                              |
|--------------|--------------------------------------------------------------------------------------------------------------------------------------------------|--------------------------------------------------|
| Local folder | **Add studio → From folder**, or drop a manifest into `~/.helmstudio/studios/`. Uses `local_path`, so nothing is cloned.                         | Development. Edit, restart, iterate.             |
| Git URL      | **Add studio → From a repository**. The manifest is read from the repo at a pinned ref, validated, then shown for approval before anything runs. | Sharing with a few people, or your own machines. |
| Registry     | A pull request adding `studios/<id>.yaml` to the helmstudio repo. CI runs validation and the smoke harness.                                      | Everyone. Ships with the next release.           |

The add flow is deliberately slow at one point: after fetching the manifest and before executing anything, helmstudio shows what it is about to do — the repo and ref, every build command verbatim, the weights and their sizes, the capabilities requested, the declared network hosts, and the certification level. That screen is the whole security model made visible, and it is the one place the product should not optimise for clicks.

Unverified · from a repositorygithub.com/someone/wan-studio · v0.3.1 · 9f2c41a

View manifest

Cancel

Run checks

✓

**Manifest valid**schema v1 · 3 build steps · 1 process · 2 weights**

✓

**Host requirements met**darwin/arm64 · 64 GB ≥ 32 GB · 211 GB free ≥ 90 GB**

!

**Will run these commands on your Mac**uv sync --all-extras · bash scripts/build_metal.sh · uv run python -m wan.setup**

!

**Requests capability: gallery.read_all**can read items produced by your other studios**

✕

**Theme conformance failed**14 raw colour literals outside vendored tokens · light theme contrast 2.9:1 on 3 pairs**

✓

**Smoke test passed**health 6.1s · 1 asset · 1 item · clean exit · no stray writes**

Checks run before install, not after. A failure is informative, not necessarily blocking — the user decides, having been told exactly what they are deciding.

## 9 · The criteria a studio must meet

"Meets the criteria of the default studios" needs to be a list a machine can check, or it is a matter of taste that nobody can satisfy. These are the checks; `helm validate` and `helm test` run them locally, the add flow runs them before install, and registry CI runs them on a clean machine.

| \#  | Criterion                                                                        | Checked by                     | Level       |
|-----|----------------------------------------------------------------------------------|--------------------------------|-------------|
| 1   | Manifest validates against the schema; `id` is unique and stable                 | `helm validate`                | Required    |
| 2   | Declares `requires` (os, arch, tools, ram_gb, disk_gb) and `peak_ram_gb`         | `helm validate`                | Required    |
| 3   | Build steps are non-interactive, deterministic, and never prompt or require sudo | Smoke run in a clean sandbox   | Required    |
| 4   | At least one `main` process with a health probe and a realistic timeout          | `helm validate` + smoke        | Required    |
| 5   | Accepts an assigned port; does not hard-code one                                 | Smoke on a random port         | Required    |
| 6   | Writes only under its own roots and the stage directory                          | Smoke with filesystem watch    | Required    |
| 7   | Exits cleanly on SIGTERM within the grace period, leaving no children            | Smoke                          | Required    |
| 8   | Declares a licence and any weight licences it accepts on the user's behalf       | `helm validate`                | Required    |
| 9   | Requests the minimum capabilities it uses, and no more                           | Static check against SDK calls | Required    |
| 10  | Declares outbound hosts; the smoke run flags any others                          | Smoke with a proxy             | Required    |
| 11  | Records outputs as assets and gallery items with non-empty params                | `helm test`                    | Expected    |
| 12  | Theme conformance: tokens only, both themes, contrast ≥ 4.5:1                    | `helm validate`                | Expected    |
| 13  | Ships a `tiny` profile so the smoke test runs in under two minutes               | `helm test`                    | Expected    |
| 14  | Uninstalls completely — no files outside its roots, no orphaned processes        | Smoke                          | Expected    |
| 15  | Pins an SDK major and an upstream ref rather than tracking a branch              | `helm validate`                | Recommended |

**Required** failures block a registry merge and warn loudly in the add flow. **Expected** failures are shown but never block a user installing their own work — a studio you wrote this afternoon should be installable before it is polished. **Recommended** is advice.**

**Draft**

A local folder. No checks enforced; the card says so. This is where every studio starts.

**Unverified**

From a repository, manifest valid, but checks not all passing or not run on a clean machine.

**Verified**

All required criteria pass, and the smoke harness ran from a clean state on this machine.

**Registry**

Merged into the helmstudio catalogue; verified in CI on every release.

The level is a chip on the card, never a gate on the user's own machine. The point is that someone can always tell whether what they are running was checked by anyone.

## 10 · Trust and safety, stated plainly

What installing a studio actually is

Cloning a repository and running its build steps is executing someone else's code on your machine with your permissions. A pretty installer does not change that. helmstudio's obligation is not to pretend otherwise but to make the decision visible and reversible.

- **Show the commands before running them.** Verbatim, in the approval screen, not buried in a log afterwards.
- **Pin by default.** A custom studio installs at a specific commit. Auto-update is off for anything below Registry level, and an update shows a command diff when build steps changed.
- **Capabilities are least-privilege and visible.** A studio asking for `gallery.read_all` is asking to read everything you have ever made; that sentence, not the capability name, is what the screen shows.
- **Constrain what you can.** Build and run under the studio's own roots with a restricted environment, no inherited secrets, no ambient Hugging Face token unless the manifest declares it needs one. Full sandboxing of a process that must reach the GPU and tens of gigabytes of weights is not realistic on macOS, and claiming it would be worse than admitting it.
- **Uninstall must be complete.** Criterion 14 exists so that "I regret this" is one click and leaves nothing behind but cached weights the user can reclaim.
- **Never auto-install from a link.** No URL scheme that installs a studio, no one-click from a web page. The add flow starts inside the app, every time.

## 11 · Order of work

| Step                                                 | Why here                                                                                                             |
|------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------|
| 1 · OpenAPI document and the conformance skeleton    | Written before either provider exists, or they diverge in week one.                                                  |
| 2 · Remote client, Go and Python                     | The smallest useful slice. h3 studio records its first gallery item.                                                 |
| 3 · Embedded provider, `helm dev`, fixtures          | Built once there is a second caller, so the interface is an interface and not a refactor of one studio.              |
| 4 · `helm validate`, `helm test`, `helm studio init` | Turns the criteria into something checkable, which is a prerequisite for custom studios rather than a follow-up.     |
| 5 · Add-a-studio flow with the approval screen       | Needs 4. The flow is mostly presentation once the checks exist.                                                      |
| 6 · Timeline and export                              | Last. It depends on assets, jobs and the media engine, and it is the piece most likely to want a second design pass. |
