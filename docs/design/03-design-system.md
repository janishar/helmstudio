# Design System

> Frozen design, 15 Sep 2026. This file is the contract.
> Do not edit to make an implementation pass — raise a finding instead.

helmstudio is the room you launch *from*. It has to read as the same family as ltx, h3, iris and AuK — near-black chrome, hairline borders, monospace for anything machine-shaped — while never being mistaken for any one of them. Every screen below is live HTML in this page: switch the theme in the header and the mockups switch with it.

## 1 · Principles

**Show the engineering, don't hide it.** Every install step, exit code, byte count and port number is visible on the surface that caused it. No spinner stands in for a log we already have.**

**The machine's real constraint is the interface's main job.** Unified memory means one heavy studio at a time. That trade is stated before it is enforced, never discovered after a crash.**

**Hairlines separate; shadows don't.** Structure comes from 1px borders and ground steps. A shadow appears in two places only — dialogs and the app window.**

**One accent, used once per screen.** Exactly one primary action is accent-filled in any view. Status colour is a separate vocabulary and never borrows the accent's hue.**

**Monospace means machine.** Paths, commands, seeds, sizes, durations, ports and logs are mono with tabular figures.**

**Every state has words.** A coloured dot is always paired with a label. Colour confirms; it never carries the message alone.**

## 2 · Colour

The accent is **signal yellow**. It is the highest-energy hue available and it earns its place here because this is a tool where one action per screen matters — Install, Launch, Render, Export — and everything else is information. Yellow also has a property no other accent has: **a yellow fill needs dark text in every theme**, so the primary button is the same colour in light and dark, which keeps the brand steady where a blue or teal would have to shift.

Two collisions came with it and both were resolved rather than tolerated. Amber was the semantic warning colour, so **warning moved to orange** — far enough round the wheel to read as a different vocabulary. And h3 studio's identity hue is amber, adjacent to the accent, so the rule is enforced by role: **identity hues appear only as a 3px stripe or a dot, never as a fill**, and the accent appears only as the single primary fill. A brighter lemon against h3's softer amber keeps them apart at a glance.

#### Dark theme — the primary design

ground-page

\#0d0c0a

ground-panel

\#161512

ground-raised

\#1f1d18

ground-inset

\#090804

border-hairline

\#292620

border-strong

\#3b372e

text-primary

\#f1ede4

text-secondary

\#a8a294

text-muted

\#716b5e

accent-base

\#ffc700

accent-hover

\#ffd633

accent-subtle

\#2b2206

#### Light theme — designed, not inverted

ground-page

\#faf8f3

ground-panel

\#ffffff

ground-raised

\#fbf8f1

ground-inset

\#1c1a15

border-hairline

\#e4dfd2

border-strong

\#c9c2b0

text-primary

\#1a1813

text-secondary

\#534f45

text-muted

\#837d70

accent-base

\#f5b800

accent-text

\#7a5600

accent-subtle

\#fff3d1

The yellow rule

Yellow is a **fill** colour, never a text colour on a light ground — `#f5b800` as type on white is about 1.9:1 and unreadable. So the token set splits them: `accent-base` is the fill in both themes and always carries `#1a1400` text, while `accent-text` is a deep amber `#7a5600` in light and a soft gold `#ffd24a` in dark. Every link, active label and accent icon uses `accent-text`; every primary button uses `accent-base`. Mixing them is the one mistake this palette makes easy.

The terminal stays dark in both themes. Log output is a machine surface, and a light terminal in a light window reads as a document rather than a stream.

#### Semantic status — a separate axis

| Status  | Dark      | Light     | Used for                                           | Always with words                                  |
|---------|-----------|-----------|----------------------------------------------------|----------------------------------------------------|
| idle    | `#716b5e` | `#837d70` | Not installed, stopped                             | Not installed              |
| running | `#4caf7d` | `#1f7a4d` | Healthy, step done                                 | Running · 12:04         |
| info    | `#4a9fe0` | `#1c6fb8` | Downloading, building                              | Downloading 43%       |
| warning | `#f0883e` | `#a85410` | Update available, unverified, marginal requirement | Update available      |
| error   | `#e05c52` | `#c0342a` | Failed, requirement not met                        | Install failed · build |

#### Studio identity hues

Stripe, dot and timeline clip only — never a button fill in launcher chrome. A studio whose manifest declares no `hue` is assigned one from a generated ramp, so a fifth studio is never colourless.

| Studio                                  | Dark      | Light     | Kinds         |
|-----------------------------------------|-----------|-----------|---------------|
| ltx studio  | `#5b8def` | `#2f5fc4` | video + audio |
| h3 studio   | `#e0a33c` | `#a06a10` | video + audio |
| iris studio | `#d8558f` | `#b12f68` | image         |
| AuK studio  | `#8b7cf0` | `#5c4bc4` | audio         |

## 3 · Type

**IBM Plex Sans and IBM Plex Mono.** Not Inter, the default of every AI product since 2022; not Space Grotesk, whose wide caps fight a dense rail. The decisive argument is the *matched* mono: a metadata line and the label above it share a skeleton, so the switch to mono reads as "this is machine output", not "this is a different app".**

page title 20/600<span style="font:600 20px/28px var(--sans);letter-spacing:-.01em">Studios

section 11/600<span style="font:600 11px/16px var(--sans);letter-spacing:.09em;text-transform:uppercase;color:var(--tx3)">Requirements

card title 14/600<span style="font:600 14px/20px var(--sans)">h3 studio

body 13/400<span style="font:400 13px/20px var(--sans);color:var(--tx2)">MiniMax-H3 video and audio through a native Metal engine.

mono 12/400<span style="font:400 12px/18px var(--mono);font-variant-numeric:tabular-nums;color:var(--tx3)">800×448 · 124f · 12 steps · seed 42 · 6m 11s

log 12/400<span style="font:400 12px/17px var(--mono);color:var(--logtx)">h3 profile: video VAE decoder wall= 85.487s peak= 9.365GiB

micro 11/500<span style="font:500 11px/15px var(--sans);color:var(--tx3)">4 studios · 2 installed · 38.4 GB on disk

Tabular numerals everywhere numbers stack or tick — a timer that jitters as it counts is a bug. Uppercase labels take 0.09em tracking, never exceed 11px or three words, and sit in muted text. Sentence case everywhere else, buttons included.

## 4 · Space and form

Base unit 4px; scale 4, 8, 12, 16, 20, 24, 32, 40, 56. Rails take 16px of internal padding, cards 14px, dense control rails 12px. Radii are 4px for buttons, chips, inputs and bars; 6px for cards, panels, terminals and dialogs; 0 for table rows, log lines and the identity stripe. Borders are always exactly 1px — the only 2px line is the focus ring, an outline at 2px offset.

The catalogue grid is `repeat(auto-fill, minmax(320px, 1fr))` with a 16px gap. The studio detail page is `280px | 1fr | 320px` above 1180px, dropping the right column below the centre at 1180px and stacking entirely below 900px — requirements first, terminal last but never under 240px tall. Minimum supported width is 380px.

## 5 · helm-css, and how studios wear it

The system ships as a package so studios can look like family without importing a component library. Five files: `helm-tokens.css`, `helm-base.css`, `helm-layout.css`, `helm-components.css`, and `helm.css` concatenating them. Every class is prefixed `helm-`, nothing is `!important`, and specificity stays at a single class so a studio overrides by writing one rule rather than fighting.

| Concern     | Rule                                                                                                                                                                                                                                                |
|-------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Identity    | `--helm-studio-accent` is set from the manifest's `hue`, so h3's Render button stays amber and AuK's stays violet while every ground, border, radius and typeface matches. Consistent, not uniform.                                                 |
| Theme       | The daemon passes the theme at launch and pushes a change over SSE; the studio sets `data-theme` on its root. Toggling the launcher re-themes every running studio. Standalone falls back to `prefers-color-scheme`.                                |
| Components  | `helm-ui-sdk` renders in Shadow DOM, so class rules do not reach inside — but custom properties inherit straight through. Tokens theme the components; classes style the studio's own markup; neither breaks the other. `::part()` covers the rest. |
| Enforcement | `helm validate` fails on raw hex outside the vendored token file, on non-Plex families, and on any token pair below 4.5:1 in either theme. Advisory locally, blocking in registry CI.                                                               |
| Stability   | Token names are API within a major. A renamed token silently restyles four studios — the worst kind of break, because nothing errors.                                                                                                               |

## 6 · Catalogue

*[Mockup: the launcher shell — top bar with `helmstudio`, `127.0.0.1:8700`, the running studio "ltx studio · 12:04 :8720" with Stop and "1 running"; nav Studios, Gallery, Timeline, Models & disk, Doctor; page title "Studios" with "4 studios · 2 installed · 38.4 GB on disk" and Add studio; four cards:*
- *ltx studio (video+audio): "LTX-2.5 video with synchronised audio, MLX on Apple Silicon." · MLX · Metal · 21.4 GB · peak ~20 GB · Running · 12:04 · Stop · Open*
- *h3 studio (video+audio): "MiniMax-H3 video and audio through a native Metal engine." · Metal kernel · ~60 GB to download · peak ~21 GB · Not installed · Details · Install*
- *iris studio (image): "FLUX.2 Klein and Z-Image-Turbo still image generation." · Metal kernel · 9.2 / 21.4 GB · 43% · 41 MB/s · ~4m left · Downloading 43% · Cancel · View progress*
- *AuK studio (audio): "Zero-shot and instruct speech generation, editing and enhancement." · PyTorch · mps · 4.1 GB · 2 of 5 steps done · Install failed · build · View log · Retry]*

Card anatomy: 3px identity stripe, title with a kind badge, one clamped description line, a mono facts line, then state chip and primary action. During a download only the lower two rows swap — the title and stripe never move, so the grid does not reflow while four cards tick.

| State | Chip | Primary | Secondary |
|---|---|---|---|
| `listed` | Not installed | Install | Details |
| `cloning` · `building` | Installing · step 3 of 5 | View progress | Cancel |
| `fetching_weights` | Downloading 43% | View progress | Cancel |
| `ready` | Installed | Launch | Details |
| `update_available` | Update available | Launch | Update |
| `failed_*` | Install failed · &lt;phase&gt; | Retry | View log |
| process `starting` | Starting · 0:24 of 4:00 | View progress | Cancel |
| process `running` | Running · 12:04 | Open | Stop |
| process `failed` | Crashed · exit 137 | Restart | View log |

Every state in the canonical vocabulary has a row here. An unmapped state falls back to its raw name in an idle chip rather than rendering blank — a state with no chip is a bug, not a blank space.

## 7 · Studio detail and install

*[Mockup: h3 studio's detail page, above 1180px.*
- *Header: github.com/janishar/h3c-studio · main · a4f91c2, chip "Install failed · build", Open folder, "Retry from failed step".*
- *Left column:*
  - *Requirements, one row each, required against found: macOS 14 or later / 14.6; Apple Silicon / arm64; 32 GB memory / 64 GB; Command line tools / not found, with the sentence "h3.c needs the Xcode command line tools to compile its Metal kernels. Run `xcode-select --install`, then retry this step."; 120 GB free disk / 211 GB free.*
  - *Runtime: Engine h3.c · native Metal; Backend metal · supported; Precision bf16 · int8; Toolchain go 1.27 · clang 16.*
  - *Weights: MiniMax-H3 60.0 GB, Ready, Shared; t5-v1_1-xxl 9.2 GB at 6.3 / 9.2 GB · 68% · 38 MB/s; "2 of 3 · cache ~/.helmstudio/models".*
- *Centre: Install, step 3 of 5 · 4m 12s.*
  - *Clone repository, `git clone --recurse-submodules …`, 48s.*
  - *Check toolchain, git, make, cc, go, 1.2s.*
  - *Build the Metal engine, `cd h3c && make mps`, exit 2 — expanded with the failure sentence and Retry step, Retry from here, View log, Open folder.*
  - *Build the studio server, `go build -o dist/h3studio .`*
  - *Download weights, `hf download MiniMaxAI/MiniMax-H3`, in parallel.*
- *Right: Output, 1,284 lines, Follow, Copy, with the make log ending "==> step failed after 2m 41s (exit 2)".]*

Step glyphs are distinct shapes — hollow circle, ring, check, cross, dash — so the checklist survives greyscale. A failed row takes a 2px error bar and expands in place; recovery buttons sit inside the failure, not in a toolbar elsewhere.

## 8 · Process group

*[Mockup: AuK studio, group run 01JB9…c4 · 0:38, "Group starting", Stop group. Processes: 3 declared · 1 running.]*

| Process | Role | Port | Health | State | |
|---|---|---|---|---|---|
| engine | sidecar · heavy | :8781 | /ready · 24s of 300s | Running · 0:38 | Log |
| studio | main · needs engine | :8782 | /healthz · 0s of 180s | Starting | Log |
| batch | worker · on demand | — | exec probe | Stopped | Start |

One group, one row per declared process, in dependency order. The heavy member carries the memory badge, because that is the process the one-at-a-time rule watches. Stop walks the list in reverse.

## 9 · Launching and switching

*[Mockup: "Starting ltx studio" — "Loading LTX-2.5 into memory. The first start after an install usually takes about a minute." — `:8720 · 0:24 of 4:00`, an indeterminate bar, Cancel.]*

Elapsed against the budget, never a check count. If it exhausts, this becomes "ltx studio didn't come up" with the last 40 log lines inline.

*[Mockup: dialog "Stop ltx studio to launch h3 studio?" — "Only one studio can hold a model in memory at a time. Your Mac has 64 GB of unified memory; LTX-2.5 needs about 20 GB and MiniMax-H3 about 21 GB, and both sets of activations do not fit alongside each other." — "ltx studio will be stopped. Anything it has written to disk is kept; anything mid-generation is lost." — checkbox "Don't ask again this session" — Cancel, and the danger-filled "Stop ltx studio and launch h3 studio".]*

The confirm is danger-filled, not accent-filled, because it destroys running work — the one place a status colour becomes a button.

## 10 · Gallery

*[Mockup: Gallery, 1,284 items · 38.4 GB, filters ltx h3 iris, All kinds, "Search prompts"; a grid of items — café window drift (h3 · 5.0s · seed 42), rain street plate (ltx · 4.2s · seed 7), klein still 11 (iris · 1024² · s 991), line 04 — arshi (AuK · 0:03 · zero-shot), take-053255 (h3 · 5.2s · seed 42), café sequence (timeline · 14.2s); a selected item with the provenance chip "from take-052505" and actions Use as first frame, Send to…, Add to timeline.]*

One query serves both views: the launcher's cross-studio gallery and a studio's own panel differ only by scope. Selecting an item exposes what can be done with it — the provenance chip on the left says where it came from, which is the thing only a central store can know.

## 11 · Timeline

*[Mockup: café sequence — 1920×1080 · 24 fps · 48 kHz · 00:14.16, chip "stream copy · no re-encode", Add from gallery, Export; a ruler 0s–14s; track V1 video with clips take-052505 (0–5.04s), ltx-drift-04 (5.04–9.1s), klein-11 (hold 2.2s), take-053255 (11.3–14.16s), each in its studio's hue; tracks A1 dialogue and A2 ambience.]*

Clips carry the identity hue of the studio that produced them, so a sequence assembled from three studios reads as exactly that. The stream-copy chip tells the user this export is seconds rather than minutes — it teaches which edits are cheap.

## 12 · Models and disk

*[Mockup: Models & disk — 38.4 GB cache · 211 GB free, Verify, "Reclaim orphaned (4.1 GB)".]*

| Artifact | Size | Used by | Last used | |
|---|---|---|---|---|
| MiniMaxAI/MiniMax-H3 | 60.0 GB | h3 studio | 2h ago | Reveal · Delete |
| Lightricks/LTX-2.5 | 21.4 GB | ltx studio | just now | Reveal · Delete |
| tencent/AuK-Flash | 4.1 GB | Orphaned | 12d ago | Reveal · Delete |

"Used by" is the reference count made visible. An artifact used by two studios is not deletable without a confirmation naming both; one used by none is chipped orphaned and is the only thing Reclaim touches.

## 13 · Adding a studio

The one screen deliberately not optimised for clicks. Installing a studio runs someone else's code on your Mac; the product's obligation is to make that visible before it happens, not to dress it up.

*[Mockup: "Unverified · from a repository" — github.com/someone/wan-studio · v0.3.1 · 9f2c41a, View manifest, Cancel, Install anyway. Checks:*
- *✓ **Manifest valid** — schema v1 · 3 build steps · 1 process · 2 weights*
- *✓ **Host requirements met** — darwin/arm64 · 64 GB ≥ 32 GB · 211 GB free ≥ 90 GB*
- *! **Will run these commands on your Mac** — uv sync --all-extras · bash scripts/build_metal.sh · uv run python -m wan.setup*
- *! **Asks to read everything you have ever made** — capability: gallery.read_all*
- *✕ **Theme conformance failed** — 14 raw colour literals outside vendored tokens · 3 pairs below 4.5:1 in light*
- *✓ **Smoke test passed** — health 6.1s · 1 asset · 1 item · clean exit · no stray writes]*

Checks run before install, not after. Required failures warn loudly and block a registry merge; expected failures never block someone installing their own work. The capability line is written as a sentence, because "gallery.read_all" tells a person nothing.

*[Levels: Draft · Unverified · Verified · Registry — "a label on the card, never a gate on your own machine".]*

## 13a · Describing a studio yourself

Most repos worth running will never ship a manifest, so writing one has to be a first-class act rather than a fallback. Form on the left for the fields, YAML on the right for the parts that are really text, both live and both editable, with the criteria scoring underneath as you type.

*[Mockup: New studio — ~/.helmstudio/studios/wan-studio.yaml, "13 of 15 criteria", Import file, Export, Test, Save to library.*
- *Form:*
  - *Identity & source: id wan-studio, kinds video, repo github.com/someone/wan, ref v0.3.1.*
  - *Runtime: framework pytorch, backends mps · supported, peak memory 18 GB.*
- *YAML pane: "valid · 2 warnings", with build, weights and processes blocks and the comment "# warning: no `test.profile` — the smoke test will run a full generation".*
- *Criteria, updating as you type:*
  - *✓ Accepts an assigned port — {port} found in the run command*
  - *✓ Health probe with a realistic timeout — /health · 180s*
  - *! No tiny profile — expected · smoke test will take several minutes in CI*
  - *! Theme conformance not checked — expected · no vendored helm-css found in the repo]*

Required failures block a registry merge; expected ones never block you saving your own work. **Export** hands back the same file to commit upstream, and **Import** takes one back — a dropped file, a picker, pasted text or a URL, several at once. That symmetry is the sharing loop: someone gets a model running, exports the manifest, posts it; the next person drops it in and has the same studio.

*[Mockup: Import manifests — Paste text, From URL, Choose files…*
- *✓ **wan-studio.yaml** — valid · video · pytorch/mps · 3 build steps · new entry*
- *! **ltx-studio.yaml** — valid · id already in your library from Registry — Override, Rename or Skip*
- *✕ **old-notes.yaml** — not a manifest · missing required fields: id, repo, processes*

*"2 of 3 will be added to the library. Nothing is installed by importing." Cancel, Add 2 to library.]*

Import reports before it acts, names a collision rather than resolving it silently, and states plainly that adding to the library is not installing — the approval screen still stands between a stranger's manifest and anything executing.

| Library source | Chip | Means |
|---|---|---|
| Local | Local | You wrote it. Overrides a registry entry of the same id, with Revert offered. |
| From repo | From repo | The studio's author ships `helmstudio.yaml`. |
| Registry | Registry | Curated and reviewed at a pinned ref. |

Source, certification level and install state are three independent facts and the card shows all three — a Local entry can be Verified if it passes the harness, and a Registry entry is still only a description until someone installs it.

## 14 · The Mac app

*[Mockup: the macOS menu bar item "ltx studio · 12:04 | Stop | Open helmstudio"; the app window titled helmstudio, "daemon 1.2.0 · adopted pid 4821", "1 running", "Update 1.3.0 ready".]*

| What the shell adds | |
|---|---|
| Native folder picker for the models root | `dialog.showOpenDialog` |
| Reveal a checkout or an asset in Finder | `shell.showItemInFolder` |
| Menu bar item with the running studio and a Stop | Tray |
| Notification when a 60 GB install finishes or fails | Notification |
| Dock progress while weights download | `setProgressBar` |
| Signed, notarised update of shell + daemon + registry | `electron-updater` |

The window frame is the only place the product gains a shadow it did not draw itself. Everything inside is the daemon's own UI, so a bug fixed in the browser path is fixed in the app.

## 15 · Components

*[Specimens: primary — Install, Install (disabled): yellow fill, near-black label. Secondary — View progress, View log: secondary and ghost. Danger — Uninstall, Stop and switch: outline, and solid for dialogs only. Chips — Stopped, Running, Downloading 43%, Update available, Crashed · exit 137. Progress — 13.2 / 21.4 GB · 62%. Step glyphs — pending, running, done, failed, skipped. Metadata — 800×448 · 124f · 12 steps · seed 42 · 6m 11s.]*

Buttons are 26px by default, 22px compact inside card rows and panel headers, 30px for a dialog primary; 4px radius, 13px medium label. Focus is a 2px accent outline at 2px offset on `:focus-visible`, never suppressed. A progress bar never carries text inside it; the number sits in the mono line beneath. A state chip is never dot-only. A card is not a button — the primary action is — so the expensive action is never where a keyboard user lands first.

## 16 · Motion

| What | Duration | Easing |
|---|---|---|
| Hover and focus colour, border | 120ms | `cubic-bezier(.2,0,.2,1)` |
| Row or panel expand (failure block) | 180ms height + opacity | `cubic-bezier(.2,0,0,1)` |
| Dialog in / out | 160ms rise / 100ms fade | `cubic-bezier(.2,0,0,1)` / linear |
| Determinate progress fill | 200ms width | linear |
| Indeterminate segment | 1400ms loop | `cubic-bezier(.4,0,.6,1)` |

**Never animates:** log lines appending — a fade makes fast output unreadable; elapsed timers and byte counters, which swap tabular digits rather than tweening; the identity stripe; card position in the grid; a timeline clip while dragging another; and a progress bar going backwards. Under `prefers-reduced-motion` every duration goes to zero except the progress fill, which is information rather than decoration.

## 17 · Accessibility

**Contrast.** Dark: primary text on the page ground is about 16:1, secondary on a panel about 7.5:1, muted about 3.9:1 — so muted is permitted only at 11px/500 for non-essential labels and never as the sole statement of a status. Light: about 15.4:1, 8.1:1 and 4.1:1 under the same rule. The accent fill carries `#1a1400` text at roughly 11:1 in both themes, which is the property that let yellow be the accent at all.

**Focus order** runs top bar, nav, page actions, then content in DOM order; a skip link is first. Within a card: title, secondary, primary.

**Live regions.** Polite for step completion, studio state changes and toasts; assertive for failures only. Download progress announces at 0, 25, 50, 75 and 100 percent — never every frame — while the bar updates continuously through `aria-valuenow`.

**Never colour alone.** Every status dot has an adjacent label in the same element, every step glyph is a distinct shape, every progress state has a mono sentence, and a timeline clip carries its studio's name as well as its hue. A greyscale screenshot of any screen above stays fully readable. Theme follows the OS by default with an explicit three-state override — System, Light, Dark — as a labelled control, never an unlabelled icon.

## 18 · Copy

Name the thing that failed and the thing to do. State the machine's limit as a fact, not an apology. Use the machine's names exactly — a step is called what its command is called, and there is no friendly synonym for `make mps`.

| Instead of | Write |
|---|---|
| `Error: exit status 2` | Build the Metal engine failed. `make mps` exited with code 2 — the log shows a missing Metal header, which usually means the Xcode command line tools aren't installed. Run `xcode-select --install`, then retry this step. |
| Something went wrong downloading. | Download stopped at 9.2 of 21.4 GB. The connection to huggingface.co dropped. Retrying resumes from where it stopped. |
| Unsupported platform. | This studio runs through CUDA, which needs an NVIDIA GPU. It won't run on Apple Silicon, so installing it would download 34 GB you can't use. |
| Insufficient memory! | This Mac has 16 GB of unified memory and LTX-2.5 needs about 20 GB. You can install it, but generation will likely fail. |
| This studio requests gallery.read_all. | This studio asks to read everything you have ever made, in every studio. It needs that to offer your past renders as references. |
| Are you sure? | Stop ltx studio to launch h3 studio? Only one studio can hold a model in memory at a time. |
| File not found. | The video for this take isn't where helmstudio left it — it may have been moved or deleted in Finder. The prompt and settings are still here, so you can render it again. |
| Invalid token. | Hugging Face rejected the stored token. It may have expired or lack access to `Lightricks/LTX-2.5`. Replace it in Settings. |
| Done! | ltx studio installed in 6m 11s. 21.4 GB in the cache. |

