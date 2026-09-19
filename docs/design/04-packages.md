# Package Architecture

> Frozen design, 15 Sep 2026. This file is the contract.
> Do not edit to make an implementation pass — raise a finding instead.

Three packages with a strict dependency direction: a CSS framework that knows nothing, a runtime SDK that knows the wire, and a UI SDK that knows neither — it consumes the runtime and wears the CSS. A studio takes as many layers as it wants and never inherits the ones it does not.

## 1 · Three packages, one direction

| Package            | Ships as                                                                                      | Contains                                                                                      | Depends on                                          |
|--------------------|-----------------------------------------------------------------------------------------------|-----------------------------------------------------------------------------------------------|-----------------------------------------------------|
| `helm-css`         | A CSS file, a minified one, per-layer files, `tokens.json`. npm and a CDN path on the daemon. | Tokens, base, layout primitives, components, both themes.                                     | Nothing. It is text.                                |
| `helm-runtime-sdk` | Go module, PyPI package, npm package (with a browser ESM build).                              | Typed client for state, records, assets, gallery, jobs, timeline, events. Provider selection. | The OpenAPI contract. Optional embedded module (Go only; amended M5, Q14). |
| `helm-ui-sdk`      | npm package plus a prebuilt ESM bundle the daemon serves.                                     | `helm-gallery`, `helm-player`, `helm-terminal`, `helm-timeline`.                              | `helm-css` and the Node/browser `helm-runtime-sdk`. |

## 2 · Why this split and not one package

**None of the four launch studios has a front-end toolchain.** h3 studio and iris studio are Go stdlib with hand-written JS and no build step; ltx studio serves HTML from a Python stdlib server; AuK runs FastAPI. A single bundle would force npm on all four. With the split, a Go studio takes `helm-runtime-sdk` and `helm-css` and never sees a package.json.**

**Release cadences genuinely differ.** A player bug fix is a patch on one package; adding a field to the gallery API is a minor on another; a spacing change is a third. Coupled, every fix forces every studio to re-test everything. Split, a studio pins `ui@^1` and `runtime@^1` and moves them independently.**

**One package knows the wire format.** This is the rule that makes the rest safe: `helm-ui-sdk` never constructs a URL, never sets a header, never parses a response. It receives a runtime client and calls methods on it. An API change touches the runtime SDK and regenerates; the components do not care.**

The dependency arrow points one way, always.

css knows nothing. runtime knows the API. ui knows css and runtime. Nothing points back up: the runtime SDK must never import a component, and `helm-css` must never assume a component's markup exists. The day one of those arrows reverses, the packages have become one package with extra steps.

## 3 · helm-css — the framework

Not a utility framework and not a copy of Tailwind. A small, semantic, class-based system shaped around what these tools actually contain: dense control rails, log panels, take lists, parameter forms and status. Plain CSS with custom properties, no preprocessor, no build step.

| Layer      | File                  | Holds                                                                                                                                            |
|------------|-----------------------|--------------------------------------------------------------------------------------------------------------------------------------------------|
| Tokens     | `helm-tokens.css`     | Colour for both themes, spacing scale, radii, borders, type scale, motion durations. Also emitted as `tokens.json` for anything that is not CSS. |
| Base       | `helm-base.css`       | Reset, root theming, focus-visible ring, selection, scrollbars, `prefers-reduced-motion`, safe-area padding.                                     |
| Layout     | `helm-layout.css`     | App shell, top bar, rails, panels, the three-column working layout, responsive collapse.                                                         |
| Components | `helm-components.css` | Button, chip, field, select, panel, card, table, step row, progress, dialog, toast, tabs, metadata line, terminal surface.                       |
| Everything | `helm.css`            | The four concatenated, plus `helm.min.css`.                                                                                                      |

Render

Queue 3 seeds

History

Stop

idle running 12 steps  seed 42 · 6m 11s

Rendered from the same tokens this page uses. Toggle your OS theme and it follows.

**Rules that keep it usable.** Every class is prefixed `helm-` so it cannot collide with a studio's own stylesheet. Nothing is `!important`. Specificity stays at a single class so a studio overrides by writing one rule, not by fighting. There are no utilities beyond a handful for spacing and flex, because a utility framework would end up dictating markup, which is exactly what a studio is entitled to own.**

How it coexists with Shadow DOM

Components in `helm-ui-sdk` render inside Shadow DOM, so class-based rules do not reach them — but **custom properties inherit straight through the boundary**. That is the whole trick: tokens theme the components, `helm-css` classes style the studio's own markup around them, and neither can break the other. Components expose `::part()` hooks for the handful of overrides that turn out to be necessary.

## 4 · helm-runtime-sdk — go, python, node

One OpenAPI document generates all three. Identical method names, identical semantics, identical errors, so an author moving between a Go studio and a Python one is not learning a second product.

    // go
    c := helm.FromEnv()
    as, _ := c.Assets.Adopt(ctx, path, helm.Video)
    c.Gallery.Add(ctx, helm.Item{Kind: helm.Video, Asset: as.ID, Params: p})

    # python
    c = helm.from_env()
    a = c.assets.adopt(path, kind="video")
    c.gallery.add(kind="video", asset=a.id, params=p)

    // node / browser
    const c = helm.fromEnv()
    const a = await c.assets.adopt(file, {kind: 'video'})
    await c.gallery.add({kind: 'video', asset: a.id, params: p})

| Concern                 | How                                                                                                                                                                                                                                                                   |
|-------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Provider selection      | `fromEnv()` returns the remote client when `HELM_API` is set, the embedded one otherwise. Same interface either way. (Amended 2026-09-15, M5, Q14: in Go; the Python and Node packages have no embedded provider, and `fromEnv()` without `HELM_API` fails as `Unavailable`, naming `helm dev`.) |
| Embedded stays optional | A separate module in Go (`helm-runtime-sdk/embedded`), so a hosted-only studio never pulls SQLite in. The Python and Node packages are remote-only; a Python or Node studio running standalone runs under `helm dev`, and constructing a client without `HELM_API` fails as `Unavailable`, naming it. (Amended 2026-09-15, M5, Q14: no `helm-runtime-sdk[embedded]` extra.) |
| Browser build           | The Node package ships an ESM browser entry with no Node built-ins, because this is precisely what `helm-ui-sdk` consumes.                                                                                                                                            |
| Errors                  | One typed error shape across languages, chosen by status: `Invalid` (400, 422), `Unauthenticated` (401), `Forbidden` (403), `NotFound` (404), `Conflict` (409 — an etag mismatch, or a state conflict named by its code), `QuotaExceeded` (429, and 507 when the disk is full: out of room, so retrying will not help), `Unsupported` (501, an endpoint the current provider cannot serve), `Unavailable` (503, or no connection), and `Internal` for any other status. 410 is `NotFound`; 413 and 416 are `Invalid`; 421 is `Forbidden` (amended by the M4 first review, #8 and #16, and the second review, #9: 507 moved from `Unavailable`, which components read as "retry"). Every body is `{error, message, details?}`. Components branch on these, so they must mean the same thing everywhere. (Amended 2026-09-15, M4, Q4: the first five kinds had nothing for 400, 401, 403 or 422.) |
| Testing                 | `helm dev` and the fixtures ship here, since they are the runtime's job. The conformance suite is written once, in Go, against the generated Go interface, and runs against both providers: the daemon over HTTP and the embedded provider in process. The Python and Node clients run a smoke test against the daemon. (Amended 2026-09-15, M4, Q25: there is one embedded provider, in Go; a second one in Python would duplicate the enforcement the suite exists to hold still. Python's `[embedded]` extra is decided with the Python studios — M5 Q14: there is none.) |

## 5 · helm-ui-sdk — the prebuilt components

Four components, each solving something every studio otherwise rebuilds badly. They are custom elements — no React, no framework, no build step required to use them.

#### helm-terminal

\<helm-terminal job="jb_01J…" follow\>

Virtualised log view over a ring buffer, so a diffusion loop emitting thirty lines a second never accumulates a hundred thousand DOM nodes. ANSI colour parsed to spans, carriage-return progress lines rewriting in place rather than printing four hundred rows, follow that releases when the user scrolls, copy, wrap, and a jump-to-latest affordance. Subscribes through the runtime client's event stream and reconnects with `Last-Event-ID`.

#### helm-player

\<helm-player asset="as_01J…" fps="24"\>

Frame-accurate transport built on `requestVideoFrameCallback`: arrow keys step one frame, the frame counter is authoritative and the timecode derived. Filmstrip scrubbing from a generated sprite sheet, waveform under the scrubber, A/B compare between two assets on one transport, loop and speed. **Extract frame** draws the current frame to a canvas, posts it through the runtime client and emits the new asset id — which is what turns "find the right frame" into a two-click anchor for FL2VA and L2VA instead of a file hunt. Plays the framework's h264 proxy when the source codec is not browser-decodable, and says so rather than letting anyone judge quality from it.

#### helm-gallery

\<helm-gallery scope="self" kind="video" picker\>

Paginated, filterable grid or list over `/gallery/items`, with thumbnails, params, star and tag, cursor paging and live insertion when a new item lands on the event stream. `scope="all"` renders the cross-studio view where the capability allows; `picker` mode turns it into a chooser that resolves to an asset, which is how a studio offers "use one of my earlier renders as a reference" without building a browser.

#### helm-timeline

\<helm-timeline timeline="tl_01J…" editable\>

The editor for a framework-owned sequence: tracks, clips coloured by the studio that produced them, drag and trim with snapping, gapless preview across cuts, and export that creates a Job. It edits the framework's timeline document through the runtime client and owns none of the data — which is why the same sequence can be opened in the launcher or inside any studio and stay one thing.

(Amended 2026-09-16, M8 Q11, Q20.) "Inside any studio" is the studio that made the sequence, or one holding `gallery.read_all` — otherwise a shared document would hand every studio the ids, lengths and origins of work it may not read. A clip whose studio the caller cannot learn is drawn neutral and labelled "another studio". The component ships with M8b; the timeline document and its API ship with M8a, and the launcher's own screen with M9.

The rule that keeps this package honest

A component **receives** a runtime client; it never constructs one. `el.client = helm.fromEnv()`, or the page sets `window.helm` once and components pick it up. Otherwise every component re-implements provider selection, auth and base-URL handling, and testing a component against a mock becomes impossible.

    <!-- zero-build path: what h3 studio would actually write -->
    <link rel="stylesheet" href="/sdk/v1/helm.css">
    <script type="module">
      import { fromEnv } from '/sdk/v1/helm-runtime.js'
      import '/sdk/v1/helm-ui.js'
      window.helm = fromEnv()
    </script>

    <helm-gallery scope="self" kind="video"></helm-gallery>
    <helm-player id="v"></helm-player>
    <script type="module">
      document.querySelector('helm-player')
        .addEventListener('frame-extracted', e => setFirstFrame(e.detail.asset))
    </script>

#### The structural interfaces (2026-09-16, M6 Q12; built in M6b)

A component depends on the few methods it calls, not on the runtime client's
whole type. Each is listed with its component, so that anything implementing it
— the runtime SDK's browser build, the launcher's own client, a fake in a test
— can drive that component.

| Component | What it calls | Optional |
|---|---|---|
| `helm-terminal` | `logs(ref, { lastEventId }) → async iterable of { id, name, data, json() }`, with events named `line`, `step`, `gap` and `end`. Taken from the client's `jobs` group for a `job` attribute, or from a `source` object the page sets for anything else. | — |
| `helm-gallery` | `gallery.query({ scope, studio, kind, q, limit, cursor }) → { items, next_cursor }` | `gallery.update(id, patch)` for star and tag; `assets.thumb(id, { w })` for thumbnails; `events.subscribe({ lastEventId })` for live insertion |
| `helm-player` | `assets.read(id, { range }) → a fetch Response` | `assets.upload(blob, contentType, { kind, filename, width, height })` for Extract frame |
| `helm-timeline` | `timeline.get(id) → Timeline`; with `editable`, `timeline.update(id, { tracks }, { ifMatch }) → Timeline` | `timeline.revert(id, { revision }, { ifMatch })` for Undo and Redo; `timeline.plan(id, { preset })` for the chip; `timeline.export(id, { preset })`, `timeline.exports(id, { limit })` and `timeline.cancelExport(id, job)` for Export; `timeline.append({ asset_id, track, timeline_id })` for `el.append()`; `assets.read(id, { range })` for the preview; `me.get()` for the caller's own hue |

**A missing optional method is a reduced component, never a broken one** (§9):
no thumbnails, no live insertion, no star, no Extract frame — the rest works.

**How a component reaches bytes without knowing a URL.** `Asset.url` tells a
*page* to prefix its proxy, and a component may not know a proxy exists (rule
2). So a component asks the client for the bytes and takes the address off the
response the client returns: `assets.read(id, { range: "bytes=0-0" })`, whose
body it cancels at once, gives the `<video>` an address the client resolved. A
thumbnail is read as a blob and shown through an object URL, released when the
element is disconnected.

**What a component does not decide.** What can be done with a selected item is
the studio's (rule 5), so `helm-gallery` emits `select` and `pick` and takes a
studio's own buttons in its `actions` slot. For the same reason `helm-timeline`
does not embed a picker: its Add emits `add-request`, and the page calls
`el.append(assetId)` with whatever its own picker chose (M8b).

(Amended 2026-09-19, by the human's decision: **looking at an item is the one
thing `helm-gallery` does decide.** Activating an item in a gallery that is not
a picker opens it in a `helm-player` in a dialog of the component's own, with a
full-screen control. Rule 5 still holds for everything else — `select` and
`pick` are emitted as before and the `actions` slot is still where a studio's
own buttons go — and the exception is drawn narrowly on purpose: a gallery of
video whose items cannot be watched is not a gallery, and every studio that
mounted one would otherwise write the same dialog around the same component.
What a studio gains by this rule elsewhere, it loses nothing of here: the
viewer is opened by an activation the studio can already see, and a studio that
wants its own may keep the item and open one.)

(Amended 2026-09-19, by the human's decision: **which of its own sequences to
edit is the one thing `helm-timeline` does decide.** With `chooser` it lists
the sequences the caller may read and opens the one picked, setting its own
`timeline`. Rule 5 is untouched for what goes *into* a sequence — Add still
emits `add-request` and the page still answers with its own picker — because
that is a choice about the studio's own material. Which sequence to open is
not: it is the component's own list, read with the same client, and every page
that mounted the editor was writing the same `<select>` over `timeline.list()`
to supply it. `timeline.list` joins the optional methods: without it the
chooser is absent and the editor is the smaller one §9 describes, not a broken
one.)

**Errors.** A component branches on the `kind` in §4's table and on nothing
else — never on a status code, never on an error string.

**The launcher's client.** It is generated from the `launcher`-tagged
operations of `api/openapi.yaml` into `web/launcher.js` by the same generator,
carries operations only, and takes its transport and error shape from the
runtime SDK's own, so there is one mapping of status to kind rather than two
that drift. It is served with the launcher's page and never under `/sdk/`.

Amendments (2026-09-16, M6):
- **Browser access (Q10).** A page never holds a token. The studio's server mounts the runtime SDK's same-origin proxy at `/helm/`: `helm.Proxy` in Go, `helm_runtime_sdk.proxy.Proxy` in Python, and `createProxy` from `@helmstudio/runtime/proxy` in Node. The proxy forwards `/helm/api/v1/<studio-api path>` with the studio's token added, `/helm/sdk/v1/*` without one, and serves `/helm/accent.css`. Anything else, launcher operations included, is 404.
  - **The client.** The browser build's `connect()` takes a base URL (default `/helm/api/v1`) and no token; it replaces `fromEnv()` in a page.
  - **The example above.** Its paths become `/helm/sdk/v1/helm.css`, `/helm/sdk/v1/helm-runtime.js` and `/helm/sdk/v1/helm-ui.js`.
  - **Level 0 stays viable.** Mounting the proxy is optional.
- **Components receive interfaces (Q12).** A component depends on the few methods it calls, documented with the component in M6b, not on the runtime client's whole type. The runtime SDK's browser client provides them for studio operations. A launcher browser client, generated from `launcher` operations and served only to the daemon's own page, provides them for install and process logs.
- **Which components ship when (Q13).** `helm-timeline` ships with the timeline API in M8. `helm-player` ships in M6b without filmstrip, waveform and h264 proxy, which render `Unsupported` until the ffmpeg decision.

## 6 · Four adoption levels

**Level 0 · nothing**

A studio ignores all three. Installs, launches, runs. No gallery, no shared cache. This must stay viable or helmstudio is not a launcher.

**Level 1 · css**

Takes `helm-css` only. Looks like it belongs to the family, keeps its own identity hue, stores nothing centrally. Cheapest possible conformance.

**Level 2 · css + runtime**

Assets, gallery, records and jobs, with the studio's own UI. This is where the four launch studios land, because none wants a front-end toolchain.

**Level 3 · all three**

Prebuilt gallery, player, terminal and timeline. Fastest path for a new studio, and the one `helm studio init` scaffolds.

Levels are a choice, not a ladder to climb. A studio with an unusual interface may deliberately sit at level 2 forever, and that is a success, not a gap.

## 7 · The timeline, settled

Two earlier decisions look contradictory and are not, once the layers are named.

| Part                               | Owner         | Why there                                                                                                                                                                      |
|------------------------------------|---------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| The timeline document and its rows | Framework     | A sequence made of an h3 clip, an ltx clip, an iris still and an AuK voice line is nobody's studio document, and it must survive uninstalling the studio that made half of it. |
| The export pipeline and ffmpeg     | Framework     | One conform-and-concat implementation, one licensing decision, one place the stream-copy fast path is correct.                                                                 |
| The editor UI                      | `helm-ui-sdk` | A component like any other, so a studio can embed sequencing inline instead of bouncing the user to the launcher.                                                              |
| Opening the launcher's own editor  | Framework API | `POST /timeline/{id}:open` stays, for studios at level 2 that would rather hand off than embed.                                                                                |

Both paths edit the same document through the same endpoints, so a sequence opened in the launcher and one opened inside a studio are the same sequence, mid-edit.

## 8 · Distribution

| Path                  | What it is                                                                                                                 | For                                                                                                                           |
|-----------------------|----------------------------------------------------------------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------|
| Package registries    | `go get github.com/janishar/helmstudio/packages/helm-runtime-sdk/go` (and `…/go/embedded`), `pip install helm-runtime-sdk`, `npm i @helmstudio/runtime @helmstudio/ui @helmstudio/css` (amended 2026-09-15, M4, Q2: nobody serves `helmstudio.in`) | Studios with a toolchain, and CI.                                                                                             |
| Daemon-served bundles | `/sdk/v1/helm.css`, `/sdk/v1/helm-runtime.js`, `/sdk/v1/helm-ui.js` — prebuilt ESM, no bundler                             | The zero-build path. What the four launch studios use.                                                                        |
| Vendored copy         | The same files committed under `vendor/helm/` in the studio repo                                                           | Standalone. The page links the vendored file first and the daemon's second, so the live version wins the cascade when hosted. |

All three must exist. Registries alone exclude the studios that refuse a build step; served bundles alone break standalone; vendoring alone goes stale.

(Amended 2026-09-16, M6 Q14.)
- **How the bundles are served.** The daemon serves `/sdk/v1/*` as static files, `GET` and `HEAD` only. They are exempt from its Origin check and answered with `Access-Control-Allow-Origin: *`, because a module script or font from another origin is fetched in CORS mode.
- **What is there.** helm-css is served at the root: its four layers, `helm.css`, `helm.min.css`, `tokens.json` and `fonts/`. The browser runtime is `helm-runtime.js`, which re-exports `runtime/browser.js`.
- **How a studio finds it.** `HELM_SDK_BASE` (for example `http://127.0.0.1:8700/sdk/v1`) is injected at spawn from the manifest's `sdk` majors. A pinned major the daemon does not serve refuses the launch as `not_launchable`, before anything is stopped for it.
- **The vendored copy.** It is linked before the daemon's `/helm/sdk/v1/` copy, reached through the proxy.

## 9 · Versioning across three packages

- **Independent semver**, with one published compatibility matrix. `GET /me` reports the framework's API major and the SDK majors it serves, so a component can decide at runtime rather than guess.
- **A studio pins majors separately** in its manifest — `sdk: { runtime: "^1", ui: "^1", css: "^1" }` — and the daemon injects the matching bundle URLs at launch. Updating helmstudio never silently jumps a studio across a major.
- **ui depends on a runtime range, never an exact version**, so a runtime patch does not force a ui release.
- **Degrade, never explode.** An unknown attribute is ignored; an endpoint the provider cannot serve returns `Unsupported` and the component renders its empty state with a one-line reason. A studio built against a newer SDK than the host should look reduced, not broken.
- **css is additive within a major.** Tokens may be added, never removed or repurposed — a renamed token silently restyles four studios, which is the worst kind of breaking change because nothing errors.

## 10 · One repo, three release trains

    helmstudio/
    ├── api/openapi.yaml            # the contract — everything else is downstream
    ├── cmd/helmstudio/             # the daemon
    ├── packages/
    │   ├── css/                    # helm-css
    │   ├── runtime-go/  runtime-py/  runtime-node/
    │   └── ui/                     # helm-ui-sdk
    ├── studios/*.yaml              # the registry
    └── test/conformance/           # run against both providers

(Amended 2026-09-16, M6 defaults: the directories are `packages/helm-css/`, `packages/helm-runtime-sdk/{go,python,node}/` and `packages/helm-ui-sdk/`, as CLAUDE.md's layout has them, not `css/`, `runtime-*/` and `ui/`.)

A monorepo because the contract and its three clients must move together; three release trains because their consumers must not have to. CI gates: the conformance suite against daemon and embedded provider, generated-client drift (regenerate and fail on a diff), visual regression on `helm-css` and every component in **both themes**, and a contrast check on every token pair. The visual regression matters more than it sounds — a CSS framework consumed by four studios has no other way to notice it broke one of them. (Amended 2026-09-16, M6 Q6 and Q16: the contrast check covers the declared pairs in 03 §2b, not every pair. Visual regression runs in `make gate` against an installed Chrome, driven over its DevTools pipe from Go's standard library. Its goldens are exact, tied to the Chrome major they were made with, and regenerated only by `make golden`.)

## 11 · The rules that stop this rotting

1.  **The dependency arrow never reverses.** runtime must not import a component; css must not assume a component's markup.
2.  **Only the runtime SDK knows the wire.** No URL, header or response shape appears anywhere in `helm-ui-sdk`.
3.  **Components receive a client, never build one.**
4.  **Level 0 stays viable.** The day a studio must adopt a package to be installable, this stopped being a launcher.
5.  **No model-specific UI, ever.** Prompt builders, parameter panels, LoRA pickers and scheduler controls stay in studios. The generic version would be worse than every specific one, and shipping it turns four independent studios into four skins.
6.  **A component earns its place by being identical everywhere.** A log stream, a video transport, a list of past outputs and a sequence are the same problem in every studio. That is the test — if two studios would want it meaningfully different, it does not belong in the package.
