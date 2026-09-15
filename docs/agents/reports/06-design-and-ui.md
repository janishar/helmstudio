# M6a — Design and UI: tokens, helm-css, the theme path, h3 — implementation report

## What was built

M6 stopped at kickoff on 22 questions (below, under "History"). The human
answered "recommendations for all". Q4 split the milestone, so this report
covers **M6a** only; M6b (components and screens) waits for M6a's review.

**The contract is whole.** `docs/design/03-design-system.md` now carries
§6–§18, which existed only in the reading copy. It also has three new sections:
- **§2a, tokens.** Every name and value.
- **§2b, contrast.** The pairs the gate checks.
- **§2c, hues.** Accent injection and the ramp.

**helm-css** (`packages/helm-css/`):
- tokens, base, layout and components, with both themes in the three-state
  pattern
- `helm.css`, `helm.min.css` and `tokens.json`, built and drift-checked
- IBM Plex under the OFL

**A launcher theme a running studio follows.** It is stored in schema v5 and
set with `PUT /launcher/settings/theme`. A studio's page follows it through a
tokenless stream and the runtime SDK's browser `themeBridge()`, with no reload.
The page never holds a token: the Go, Python and Node runtime SDKs each ship
the same same-origin proxy, which a studio mounts at `/helm/`.

**Checks:**
- **`helm validate -theme`** lints a studio's stylesheets.
- **Visual regression in both themes is in `make gate`.** It drives the
  installed Chrome over its DevTools pipe from Go's standard library.

**h3's stylesheet is migrated** on a `helm-tokens` branch in its checkout. 94 of
101 colour literals and all 45 non-Plex font uses are gone. The 7 left are a
missing token, raised below.

## Gate

    $ rm -rf bin && go clean -testcache && make gate
    fmt: clean
    vet: clean
    vet-linux: clean
    boundaries: clean
    deps: go.mod unchanged since 37f6b5c
    drift: generated clients match api/openapi.yaml
    ?   	github.com/janishar/helmstudio/api/gen	[no test files]
    ok  	github.com/janishar/helmstudio/cmd/helm
    ?   	github.com/janishar/helmstudio/cmd/helmstudio	[no test files]
    ok  	github.com/janishar/helmstudio/internal/api
    ok  	github.com/janishar/helmstudio/internal/api/studioapi
    ?   	github.com/janishar/helmstudio/internal/chrome	[no test files]
    ok  	github.com/janishar/helmstudio/internal/css
    ok  	github.com/janishar/helmstudio/internal/install
    ok  	github.com/janishar/helmstudio/internal/manifest
    ok  	github.com/janishar/helmstudio/internal/media
    ok  	github.com/janishar/helmstudio/internal/platform
    ok  	github.com/janishar/helmstudio/internal/store
    ok  	github.com/janishar/helmstudio/internal/supervisor
    ok  	github.com/janishar/helmstudio/internal/theme
    ok  	github.com/janishar/helmstudio/internal/themelint
    ok  	github.com/janishar/helmstudio/internal/weights
    ok  	github.com/janishar/helmstudio/packages/helm-css
    ok  	github.com/janishar/helmstudio/packages/helm-runtime-sdk/go
    ok  	github.com/janishar/helmstudio/test/conformance
    ok  	github.com/janishar/helmstudio/test/visual
    studios/auk-studio.yaml: ok
    studios/h3-studio.yaml: ok
    studios/iris-studio.yaml: ok
    studios/ltx-studio.yaml: ok
    gate: green

The same result came from a second clean run after every planted bug (below) was restored.

This is macOS on Apple Silicon only, as decided in M1. Chrome 152 is at the
standard application path; Node 26 and Python 3.9 are on `PATH`.

Outside the gate:
- **Race detector.** `go test -race -count=1` over `internal/theme`,
  `internal/api`, `internal/api/studioapi`, `internal/css`,
  `internal/themelint`, the Go runtime SDK, `test/visual`, and
  `test/conformance -run Proxies` is clean.
- **The real daemon binary.** Run with a temporary `HELMSTUDIO_HOME`:
  - `/sdk/v1/helm.css` answers 200 `text/css` with
    `Access-Control-Allow-Origin: *` to a studio page's `Origin`.
  - `PUT /api/v1/launcher/settings/theme {"theme":"light"}` answers 200.
  - `GET /api/v1/theme` from a studio page's origin answers 200 with `*`.
  - The launcher theme route from that origin answers 403.
  - The shelf serves the theme control.
  - After a restart, `GET /api/v1/theme` still says `light`.
- **h3's branch.** `helm validate -theme ~/GenAI/minimax-h3-mlx/h3c-studio`
  reports 7 colour literals and 0 font families, all 7 in the timing chart.
  `go build` in the checkout succeeds.

### The Definition of Done, item by item

| DoD | Evidence |
|---|---|
| **The contract is whole** | 03 has §6–§18 with mockups marked in place, §2a tokens, §2b pairs and §2c hues. 02 has the v5 DDL. 01 R39, 04 §5/§8/§10, 07 §3/§6, 08 and the plan are amended. Nothing M6a implements is cited only from `docs/artifacts/`. |
| **Tokens are the API**: 03's table = `helm-tokens.css` = `tokens.json` | `packages/helm-css` `TestTokensMatchTheDesignTable` parses 03 §2a (both tables, spacing row expanded) and diffs names and values against both files, both ways. `TestLayersReadOnlyTokensThatExist` fails on a `var(--helm-…)` that is not a token. `TestDerivedFilesAreBuiltFromTheLayers` fails on a hand edit or a missed `make css`. A planted rename of `--helm-text-muted` failed six assertions. |
| **Contrast** in both themes, in the gate; a darkened `text-secondary` fails | `TestEveryDeclaredPairMeetsItsContrastInBothThemes` parses 03 §2b (55 pairs per theme). `TestTheContrastCheckCatchesADarkenedToken` darkens `text-secondary` in each theme and expects failures. `TestContrastMatchesKnownRatios` anchors the formula (21:1, 4.48:1, 1.79:1). |
| **helm-css rules**: `helm-` prefix, no `!important`, one class | `TestLayersKeepTheOverrideRules` computes specificity per Selectors 4 (`:where` counts nothing; `:is`/`:not`/`:has` count their argument), checks class prefixes, `!important`, keyframe names, and that no `helm-*` element is selected (04 §11 rule 1). `TestSpecificityCountsAsCSSDoes` pins the calculator. Planted `!important` and a two-class selector were both caught. `TestLayersUseOnlyTokens` runs the studio lint over the layers. |
| **Visual regression in both themes is in `make gate`**; one token change fails; a missing Chrome fails | `make visual` is a gate step. 11 exact goldens: tokens dark, light and system-light; components dark and light; layout dark and light at 1280, 1000 and 380 px. `TestTheComparisonCatchesAOneTokenChange` moves `border-strong` by 1/255 in one channel, in each theme, and requires a failure. A missing browser `t.Fatal`s unless `HELM_ALLOW_MISSING_BROWSER` is set. A Chrome major that differs from `golden/CHROME_MAJOR` fails. A planted 1px change to `.helm-chip` was caught. |
| **The token never reaches a page** | `test/conformance` `TestProxiesForwardOnlyTheStudioAPIAndTheTheme`, for Go, Python and Node proxies: every operation in `api/openapi.yaml` is requested with the page's `Authorization`, `Cookie` and `Origin`. For every `launcher` path: 404, never reaching the upstream. For every `studio-api` and `public` path: forwarded once, with the studio's token and none of the page's headers. No response contains `hs_live_` or `Set-Cookie`. Dot, encoded and unknown paths: 404. SDK files: forwarded without a token. `TestProxiesAgainstTheDaemon`: `/me` works through each proxy against the real daemon, and the theme stream follows a change. `TestProxiesServeTheSameAccentCSS`: byte-identical `accent.css`. |
| **Theme reaches a running studio**, in a real browser, no reload; a no-capability studio too | `test/visual` `TestTheLauncherThemeReachesARunningStudioPage`: the daemon built as `cmd/helmstudio` builds it; a fixture studio whose environment comes from the real `LaunchEnv`, mounting the Go proxy; its page in Chrome. dark → light → system → dark set on the daemon; the page's `data-theme` follows each step (a MutationObserver log reads `dark,light,system,dark`); body background is the token ground; one page load (`sessionStorage` counter and navigation entries); `--helm-studio-accent` is the manifest's hue; no `hs_live_` in the DOM. For the studio with capabilities, `connect().me.get()` from the page succeeds; for the one without, it is `Unauthenticated`. Both follow the theme. |
| **Injection**: `HELM_THEME`, accents, `HELM_SDK_BASE`; ramp stable; unserved major refuses | `studioapi` `TestLaunchInjectsThemeAccentAndSDKBase`: a real launch dumps its env. A studio with a hue gets it lower-cased; one without gets `theme.RampFor(id)`. `TestAnUnservedSDKMajorRefusesBeforePreemption`: `sdk.ui "^2"` is 422 `not_launchable` naming it, and the running heavy studio keeps running. `internal/theme` `TestAccentIsTheHueOrAStableRampEntry`, `TestRampMatchesTheDesignAndKeepsItsDistances` (03 §2c's table equals `Ramp`; every entry ≥ 30° from the accent and launch hues and ≥ 3:1 on its panel), `TestSDKMajor`. |
| **h3**: `-strict` reports zero findings; baseline recorded; every literal left is a token or a finding | **Not met as written: 7 findings remain**, all the timing chart's series colours, raised as a missing token (below). The baseline at `a6eb54f` is 101 colour literals (51 in theme blocks, 50 outside) and 45 font families, recorded in 08 and the decision log. Every other literal is mapped to a token or a `color-mix()` of tokens. |
| **`make gate` is green** | Above. |
| **Machine-bound** | **Not verified: machine-bound.** Script under "Could not verify". |

### Planted bugs

Each was applied alone to the file named, the related tests run, and the file
restored from a copy. A final `make gate` from a clean cache ran after all of
them.

| # | Bug | Caught by |
|---|---|---|
| 1 | Go proxy allowlists `studios` | `TestProxiesForwardOnlyTheStudioAPIAndTheTheme` |
| 2 | Python proxy forwards `Cookie` | same |
| 3 | Node proxy forwards the page's `Authorization` instead of the token | same |
| 4 | Go proxy skips path safety | `TestProxyRefusesEverythingElseWithoutReachingTheDaemon` |
| 5 | `/theme` not exempt from the Origin check | `TestOnlyTheThemeAndSDKFilesAcceptAStudioPagesOrigin` |
| 6 | Every GET exempt from the Origin check | same |
| 7 | The launch check skipped before preemption | `TestAnUnservedSDKMajorRefusesBeforePreemption` |
| 8 | A theme change not published on `/events` | `TestThemeSettingIsReadSetAndPublished` |
| 9 | The bridge leaves `data-theme` set for `system` | `TestTheLauncherThemeReachesARunningStudioPage` |
| 10 | One light block differs from the other | `packages/helm-css` (`ParseTokens` refuses the file) |
| 11 | A token renamed in all three blocks (**re-planted**; the first attempt matched three places and was not applied) | `TestTokensMatchTheDesignTable`, `TestLayersReadOnlyTokensThatExist` |
| 12 | `!important` in a component | `TestLayersKeepTheOverrideRules` |
| 13 | A two-class selector | same |
| 14 | The lint ignores named colours | `TestColourLiteralsAreFoundInValuesOnly` |
| 15 | `HELM_THEME` not injected | `TestLaunchInjectsThemeAccentAndSDKBase` |
| 16 | A ramp entry drifts from 03 | `TestRampMatchesTheDesignAndKeepsItsDistances` |
| 17 | `.helm-chip` height 20px → 21px | `TestHelmCSSMatchesItsGoldensInBothThemes` |
| 18 | Python's on-accent choice inverted | `TestProxiesServeTheSameAccentCSS` |
| 20 | `/sdk/v1/` serves any name (**not caught, and not a bug**) | The embedded FS holds only the listed files, so `helmcss.go` or `index.js` still answer 404. That makes it a bad plant rather than a gap; the protection is structural, and `TestSDKFilesAreServed` asserts those 404s. |

Two things the first visual run taught:
- **A tolerance of 4 per channel** let a token moved by 2/255 through.
- **Three runs rendered identically**, so the comparison is now exact.

## Design contradictions raised

- **At kickoff: 22 questions**, below under "History". All were answered
  "recommendations for all".
- **While building: three, recorded as open in `docs/decisions.md`.** None
  blocked, and none was resolved in code:
  1. **The token set has no colours for data series.** h3's profile timing
     chart (`static/style.css` `.c0`–`.c6`) stacks seven categorical colours.
     - 03 §1: "Status colour is a separate vocabulary". The identity ramp is
       for studios.
     - The milestone: "a literal left behind is a token missing."
     - I left the seven literals rather than invent tokens or borrow status
       colours. `helm validate -theme -strict` on h3's branch therefore reports
       7.
     - **I would add `--helm-series-1`…`-7` per theme to 03 §2a**, after the
       human's sign-off on values.
  2. **Nothing names borders and raised surfaces on the always-dark terminal
     ground.**
     - 03 §2: "The terminal stays dark in both themes." It gives text colours
       for it (log) but no edges.
     - h3's console in the light theme put light-theme text and borders on a
       dark ground (screenshot checked).
     - I rescoped the text tokens to log tokens inside `.console` and derived
       its borders and chips with `color-mix()`.
     - **I would add `--helm-log-border`**, and perhaps `--helm-log-raised`,
       so each studio does not derive its own.
  3. **05 §4 and §9 criterion 12 still say "contrast ≥ 4.5:1"** for theme
     conformance, against 03 §2b's pairs (Q6). 05 is not in M6a's file list,
     so I did not amend it.

**Two figures in my kickoff report were wrong,** and are corrected in the
history below:
- The light focus ring on a white panel is **1.8:1**, not 1.9:1.
- The dark ring on the dark panel is **10.2:1**, not 12.9:1.

Neither changes Q6's answer.

## Judgement calls

All are in `docs/decisions.md` under "2026-09-16 · M6 design and UI", after
the Q1–Q22 entries. Read these first:

- **Tokens beyond Q5's list, needing sign-off:**
  - `--helm-on-studio-accent`, for text on a studio's own hue. #1a1400 fails
    on AuK's light violet.
  - The fallbacks when nothing is injected: studio accent = `accent-text`,
    on-studio-accent = `ground-page`. A studio with no proxy shows a soft
    yellow near the accent.
  - `--helm-log-muted` at `#7a7467`, because the reading copy's `#57534e`
    fails 3:1 on both terminal grounds.
  - Every value marked *proposed* in 03 §2a.
- **The accent is CSS, not script.** The proxy serves `/helm/accent.css`,
  switching with `data-theme` like the tokens. The bridge only sets
  `data-theme`, and marks the root `data-helm-theme-follows="launcher"` while
  it follows; that mark is how a studio hides its own control (Q19). Q9's
  wording had the bridge set the accent.
- **`public` is a third OpenAPI tag** for `/theme` and `/theme/events`. It
  keeps them out of the generated SDK and names the only operations exempt
  from the Origin check.
- **The proxy's exact allowlists:** first path segments, methods, request
  headers, response headers, and path safety. They are listed in the decision
  log and checked against every tagged path in `api/openapi.yaml`, for all
  three languages. **Accepted cost:** script injected into a studio's page can
  act as that studio through the proxy while the page is open. It cannot take
  the token away.
- **`supervisor.LaunchChecker`** is an optional interface asked before
  preemption. `LaunchEnv` runs after a preempted studio is stopped, so the SDK
  major check had to move earlier.
- **Class names are provisional** until this review, then API within a major
  like the tokens. 03 says nothing about class stability.
- **Visual goldens are exact**, bound to this machine's Chrome 152. They are
  about 800 KB of PNG, and `make test` leaves `test/visual` to `make visual`.
- **h3.**
  - It vendors the whole `helm.css`, not just the tokens.
  - `#000` media grounds became `ground-inset`, a warm near-black rather than
    black, against h3's comment "neutral surround so nothing biases how frames
    read".
  - The Render gradient mixes the studio accent with `log-text`.
  - `go.mod` requires the runtime SDK at `v0.4.0` with a `replace` to this
    checkout, and Go's tooling bumped h3's `go` line to 1.27.1.

## Could not verify

Nothing here touched a model, Metal or real weights.

- **The machine-bound demo: a running h3 re-themed mid-render.** On an Apple
  Silicon Mac with h3's weights:
  1. In `~/GenAI/minimax-h3-mlx/h3c-studio` on `helm-tokens`: `go build -o
     dist/h3studio .` (the `replace` needs this helmstudio checkout at
     `../../helmstudio`).
  2. In this repository: `go build -o bin/helmstudio ./cmd/helmstudio`. Point
     h3's installation at the branch (a `local_path` manifest, or install and
     check out `helm-tokens` in `<data>/studios/h3-studio/src`), then
     `./bin/helmstudio`.
  3. Launch h3 from the shelf and open it. Expect Plex, warm grounds, and the
     Render button in `#e0a33c`. h3's own theme switch should be absent.
  4. Start a render.
  5. On the shelf, set Light, then Dark, then System. Expect h3's page to
     change each time within a frame, with no reload (the prompt text you
     typed stays), and the terminal to stay dark in Light.
  6. Expect the render to continue: `GET /api/queue` still shows it, and the
     terminal keeps streaming.
  7. Stop helmstudio and run h3 standalone (`./dist/h3studio --port 8710`).
     Expect the vendored helm-css, and h3's own theme switch back and working.
- **Fonts and antialiasing on another Mac or another Chrome major.** The
  goldens are exact on this machine only. Another machine may need
  `make golden` and a look at the images.
- **ltx and AuK.** Neither mounts the proxy yet; that is upstream work in their
  repositories. The Python proxy is tested against the daemon, but not inside
  ltx's `http.server` or AuK's FastAPI.
- **Linux.** Visual regression was not run there, and `internal/chrome` does
  not know Linux Chrome's default paths beyond `PATH` names.

## Dependencies added

- **Go modules: none.** `go.mod` is unchanged, and `deps` reports so.
- **IBM Plex Sans 1.1.0 and IBM Plex Mono 2.5.0.** The releases' Latin-1
  woff2 splits, with the release licence (SIL OFL 1.1). Recorded with release
  names and archive sha256 in `docs/decisions.md` (Q15).
  - `IBMPlexSans-Regular-Latin1.woff2` — `b5ad7bd39f996144915f0ad9849a90183b27d8c28ad97ed98af5b1bebc51f6b1`
  - `IBMPlexSans-Medium-Latin1.woff2` — `b5610af04d0d4b5a14a621d96d974b993e945a065db1a8861918f69ef9321934`
  - `IBMPlexSans-SemiBold-Latin1.woff2` — `fff0ab3a88b0b4aa0b693e4f0201359a15183b08e3fa5696d1918d8f0ade8ad5`
  - `IBMPlexMono-Regular-Latin1.woff2` — `e8993d946649b9d01abb1ed06d574b19d8ea3e66b5c3948602db335c44c18e56`
  - `IBMPlexMono-Medium-Latin1.woff2` — `41201b658a328b9d00368215c2f1102770f80b15952ab82631e4006255e6365d`
- **Tools the gate now needs:** an installed Chrome, for visual regression.
  Node and Python were already needed, for the client smoke tests; the proxy
  tests use them too.

## Files touched outside the milestone's list

- **`internal/css/**` and `internal/chrome/**` (new).** The CSS reader shared
  by the helm-css build, its tests and the lint, and the DevTools-pipe driver
  the visual tests use. Neither was named; both are needed by tasks 1, 4 and 5.
- **`docs/agents/milestones/06-design-and-ui.md`.** Replaced with the approved
  expansion (Q1).
- **`internal/store/store_test.go`, an M1 test.** It now pins schema version 5
  and lists `settings`. Named in the decision log for sign-off.
- **Outside this repository, approved at kickoff (Q18):**
  `~/GenAI/minimax-h3-mlx/h3c-studio`, branch `helm-tokens`, uncommitted:
  - `go.mod`
  - `server/handlers.go`
  - `static/app.js`, `static/index.html`, `static/style.css`
  - `static/vendor/helm/` (new)

## Decision-log entries appended

In `docs/decisions.md`:
- **A section "2026-09-16 · M6 design and UI"** with:
  - one entry per kickoff answer, Q1–Q22
  - the kickoff defaults
  - fifteen judgement calls
  - the M1 test edit
- **Under "Open, not yet decided":** three "Raised in M6a" entries:
  - no data-series colours
  - no terminal edge tokens
  - 05's contrast wording

`git diff main -- docs/decisions.md` shows only added lines.

## Left undone

- **M6b, by design (Q4).** `helm-ui-sdk` (terminal, gallery, player), the
  launcher browser client, and the screens.
- **The machine-bound demo** (above).
- **The seven h3 series colours and the terminal edge tokens**, waiting for the
  human (raised).
- **Upstream, outside this repository:**
  - Committing and pushing h3's `helm-tokens` branch.
  - Replacing its `replace` with a published tag, once the runtime SDK with the
    proxy is tagged.
  - Moving `studios/h3-studio.yaml`'s `ref` after h3 merges.
  - Mounting the proxy in ltx and AuK.
- **Committing.** Per `implementer.md`, the work is uncommitted on
  `feat/design-and-ui`.

---

## History: the kickoff as it was put to the human

Work stopped before task 1, and the only change then was this report.
`make gate` on the untouched tree reported `gate: green`. The human answered
"recommendations for all".

**Corrections made afterwards, while building:**
- **Q6's focus-ring figures.** The light ring on a white panel is 1.8:1, and
  the dark ring on a dark panel is 10.2:1.
- **Q19's baseline.** It also counts 45 font-family findings, the studio's own
  `--mono` and `--sans`.

The questions follow as they were asked.

### A · The brief itself

#### Q1. Who writes M6's contract

This is the same question as M5's Q1, and the answer is recorded ("M5's
contract is the expansion drafted at kickoff and approved by the human").

**Recommendation:** same as M5.
- Treat the "Proposed expansion" at the end as a draft.
- The human edits it and puts it into the milestone document.
- Implementation starts only after that.

#### Q2. What the "fourteen screens" are

- 02-milestones: "helm-css, fourteen screens, ui-sdk components".
- 03-html names **ten** screen sections under "Screens":
  - 6 Catalogue
  - 7 Install
  - 8 Process group
  - 9 Launch & switch
  - 10 Gallery
  - 11 Timeline
  - 12 Models & disk
  - 13 Add a studio
  - 13a Manifest editor
  - 14 Mac app
- Four of them belong to other milestones by their own briefs, and a fifth
  needs API that does not exist yet:
  - **11 Timeline** is M8. The timeline API was removed in M4 Q1 "to return
    with the milestone that builds it (timeline: M8)".
  - **13 and 13a** are M7, which lists "the in-app manifest editor … the
    approval screen".
  - **14 Mac app** is M9.
  - **10 Gallery** in the launcher needs studio-api access the launcher does
    not have (Q11).

"Fourteen" cannot be reproduced from any document. A count that nobody can
check is a DoD nobody can meet.

**Recommendation:** name the screens, not a number. M6 builds:
- the app shell: top bar with the running studio, nav, skip link, and the
  System/Light/Dark control (03 §17)
- **6 Catalogue**, with the full card state table
- **7 Studio detail and install**
- **8 Process group**
- **9 Launch & switch**: starting, and the switch dialog on M5's digest
- **12 Models & disk**, without Verify (still open)
- **Settings**: theme and the Hugging Face token (Q19)
- **10 Gallery**, only if Q11 gives the launcher a way to read it

Timeline, the add and editor screens, and the Mac app stay with M8, M7 and M9.
Doctor stays hidden in the nav until a milestone builds it (Q19).

#### Q3. The design system's markdown is truncated at §6

- `docs/design/03-design-system.md` is 219 lines. It ends inside §6 with the
  word "Open", partway through the first catalogue card.
- 03-html continues through **§7–§18**: studio detail, process group, launch
  and switch, gallery, timeline, models and disk, adding a studio, the manifest
  editor, the Mac app, **components (§15), motion (§16), accessibility (§17)
  and copy (§18)**.
- 00-index: the artifacts are "a snapshot, not the contract: where one differs
  from the markdown here, the markdown is right". It also says: "where only the
  picture existed, the reading copy in `docs/artifacts/` is the reference".
  Neither sentence covers twelve sections of prose, tables and rules that are
  missing from the markdown altogether.

Almost every rule M6 must build is in the missing part:
- the card state table
- button sizes and focus behaviour
- motion durations
- live regions, the contrast figures and the copy table

Right now none of it is in the contract.

**Recommendation:**
- M6's **task 0** converts 03-html §6–§18 into the markdown, text and tables
  verbatim. Mockups are marked in place, as 00-index does for diagrams.
- It lands as its own commit with a decision entry, so it is reviewed as a
  change to the contract and not as a side effect.
- Nothing is built until it is in.

#### Q4. Split the milestone

The machine-bound demo needs only four things:
- tokens
- `helm-css`
- the theme path from the launcher to a running studio
- h3 wearing the tokens

The screens and the three components are a second, larger body of work. It is
10–12 days in one brief, reviewed once.

**Recommendation:** two briefs, reviewed separately.
- **M6a: tokens, helm-css, theme propagation, h3's migration, visual
  regression.** This is the part that fixes contracts (token names, theme API,
  browser access), and a review should see it before screens are built on it.
- **M6b: screens and components** on top of M6a.

If the human prefers one brief, the expansion below still orders tasks this
way.

---

### B · Tokens: the names become API

#### Q5. Token names, and the tokens the design uses but never names

- 03 §2 names colour roles without a prefix: `ground-page`, `border-hairline`,
  `accent-base`, and so on.
- 08 and 07 §6 use prefixed custom properties: `--helm-ground-page`,
  `--helm-status-error`, `--helm-studio-accent`, `--helm-radius-md`,
  `--helm-on-accent`.
- 03 §5: "Token names are API within a major." The milestone asks: "Did any
  token get renamed to suit a component?"

The design has **values with no name** for:
- the spacing scale (4…56)
- radii (4px and 6px; only `radius-md` is ever named)
- the seven type roles
- the five motion rows
- button heights (22, 26, 30)
- log and terminal text (03 §3's "log" style uses a colour 03 never defines)
- the dialog shadow
- the scrim behind a dialog

It also has **names or roles with no value**:
- **Dark `accent-text`** is `#ffd24a`, but only in prose. The dark table omits
  it.
- **Light `accent-hover`** does not exist.
- **The danger button** (03 §9, "danger-filled") has no text colour. On the
  dark error fill, white text measures 3.61:1 and `#1a1400` measures 5.09:1.
- **`on-accent`** (`#1a1400`) is named only in 07 §6's example.
- **h3's overlay tints** have nothing to map to (Q17), for example
  `rgba(0,0,0,.5)` and `rgba(255,180,84,.12)`.

Whatever M6 writes first becomes the API four studios compile against.

**Recommendation:** a token table goes into 03 as part of task 0, and
`tokens.json` uses the same names.
- **Colour, per theme:** `--helm-` plus 03's role names verbatim
  (`--helm-ground-{page,panel,raised,inset}`,
  `--helm-border-{hairline,strong}`, `--helm-text-{primary,secondary,muted}`,
  `--helm-accent-{base,hover,subtle,text}`), plus:
  - `--helm-on-accent`
  - `--helm-status-{idle,running,info,warning,error}`
  - `--helm-on-danger`
  - `--helm-log-{text,muted,error,prompt}`
  - `--helm-scrim`
  - `--helm-shadow-dialog`
  - `--helm-focus-ring` (Q6)
  - `--helm-studio-accent` (injected; Q7)
- **Space:** `--helm-space-{1..9}` for 4, 8, 12, 16, 20, 24, 32, 40 and 56 px.
- **Radius:** `--helm-radius-sm` (4px) and `--helm-radius-md` (6px).
- **Control height:** `--helm-control-{sm,md,lg}` (22, 26, 30).
- **Type:** `--helm-font-{sans,mono}` and
  `--helm-type-{title,section,card-title,body,mono,log,micro}` as `font`
  shorthands.
- **Motion:** `--helm-duration-{hover,expand,dialog-in,dialog-out,progress,indeterminate}`
  and `--helm-ease-{standard,enter,indeterminate}`.
- **Values 03 does not give** are listed separately and marked *proposed*:
  light `accent-hover`, the log colours, the scrim, the shadow and on-danger.
  The human signs off on them, because a later change to one restyles every
  studio.

#### Q6. The contrast rule fails the design's own tokens

- 03 §5: `helm validate` fails "on any token pair below 4.5:1 in either theme".
  07 §6 and 05 §9 criterion 12 say the same.
- 03 §17 lets muted text through below that: muted "about 3.9:1 … permitted
  only at 11px/500 for non-essential labels".

Measured with the WCAG formula:

| Pair | Dark | Light |
|---|---|---|
| `text-muted` on `ground-page` | **3.69** | **3.86** |
| `text-muted` on `ground-panel` | **3.45** | 4.09 |
| `status-idle` on `ground-panel` | **3.45** | 4.09 |
| `accent-base` focus ring on `ground-panel` (non-text, needs 3:1) | 10.2 (corrected; first stated 12.9) | **1.8** (corrected; first stated 1.9) |

So:
- A literal "any pair" check fails helm-css itself.
- 03 §17's own figures (3.9 and 4.1) are slightly high.
- 03 §15's "2px accent outline" focus ring is close to invisible on a light
  panel.

**Recommendation:**
- **Pairs, not a matrix.** The contrast rule is a declared list of
  (foreground, ground, threshold) pairs:
  - 4.5:1 for primary, secondary, `accent-text`, status text and on-accent
  - 3:1 for muted, per 03 §17's exception, and for non-text (focus ring,
    borders that carry meaning)
- **The focus ring.** `--helm-focus-ring` is `accent-base` in dark and
  `accent-text` in light (6.6:1). This changes 03 §15 and needs sign-off.
- **§17's figures** are corrected to measured values.

#### Q7. h3's hue has two values in the design, and `HELM_ACCENT` has one slot

**Two hues:**
- 03 §2's table: h3 `#e0a33c` dark, `#a06a10` light. 01 §13's example and 07
  §3's `HELM_ACCENT=#e0a33c` agree.
- 08's manifest and `studios/h3-studio.yaml`: `#ffb454` and `#b5721a`, "its own
  amber, unchanged". 08's mapping table: "h3 keeps its amber".
- 03 §2 gives the reason for its value: "A brighter lemon against h3's softer
  amber keeps them apart at a glance."

**One slot:**
- `hue` has `dark` and `light`. 07 §3 injects one `HELM_ACCENT`. M4 Q28 noted
  this and deferred it to M6.
- 03 §2 also assigns a studio without `hue` a colour "from a generated ramp",
  without saying who generates it or how.

**Recommendation:**
- **The value.** 03's table wins: it is the palette document, and its value
  was chosen against the accent. `studios/h3-studio.yaml` changes to
  `#e0a33c`/`#a06a10`, with a decision entry and a note in 08.
- **The slot.** Inject `HELM_ACCENT_DARK` and `HELM_ACCENT_LIGHT` instead of
  `HELM_ACCENT`. The theme bridge sets `--helm-studio-accent` from whichever
  matches the resolved theme. 07 §3 is amended.
- **The ramp.** The daemon picks from the studio id, deterministically: a fixed
  list of dark/light pairs, each checked against the accent and against the
  four launch hues. It is written into 03.

---

### C · The theme reaching a running studio: the demo

#### Q8. Where the theme setting lives and how the launcher sets it

- **Nothing stores it.** 07 §6: "pushes a `theme` event over SSE when the user
  toggles it". 03 §17 wants a System/Light/Dark control.
- **No table.** There is no `settings` table (Open: "`settings` stores a root
  only when the user overrides it — but schema v1 has no `settings` table").
- **No endpoint.** M4 Q1 removed `/settings` from the API.

**Recommendation:**
- **Storage.** Schema v5 adds `settings(key TEXT PRIMARY KEY, value TEXT NOT
  NULL, updated_at INTEGER NOT NULL)`, with DDL written into 02 first. It
  serves the theme now, and the M1 entry's roots later.
- **The API.** New launcher operations `GET /launcher/settings/theme` and
  `PUT /launcher/settings/theme {theme: system|light|dark}`. The default is
  `system`.
- **The event.** A change emits `theme` on every event stream (Q9).
- **At spawn.** `HELM_THEME` is injected, as 07 §3 says.

#### Q9. How a running studio's page actually receives the change

The chain in 07 §6: the daemon emits `theme` on `/events`; "the SDK's two-line
theme bridge sets `data-theme` on the studio's root element". What that chain
needs is not there:
- **`/events` needs a studio token** (M4 Q7), and a browser page has none (M4
  Q27, open until M6).
- **A studio with no `capabilities` gets no token at all** (07 §3). A level-1
  studio (css only, 04 §6) therefore cannot subscribe to `/events` from its
  server either, and by design never follows the theme.
- **M2's Origin rule refuses cross-origin calls.** A page on `127.0.0.1:8710`
  calling port 8700 gets 403.

The review focus asks: "Does the theme toggle reach a studio that is already
running, or only a reload?"

Options:
- **(a) The studio's server relays.** It subscribes to `/events` with its token
  and forwards `theme` to its page on its own stream (h3 has one). This fails
  every studio without capabilities, and every studio implements the relay.
- **(b) A theme stream that needs no token.** `GET /api/v1/theme` and
  `GET /api/v1/theme/events` carry only `{theme}`. The theme is not a secret.
  The page reaches them through the same-origin proxy in Q10, or directly.
- **(c) At load only.** The studio renders `HELM_THEME` into its page. A running
  studio then follows only on reload, which fails the demo.

**Recommendation: (b), delivered through Q10's proxy.** `theme` also stays on
`/events` for server-side consumers, as the contract already says. The
browser build of `helm-runtime-sdk` exports the bridge:
- `themeBridge()` reads the stream, sets `data-theme` and
  `--helm-studio-accent`, and reconnects.
- **Standalone** (no stream), it leaves `data-theme` unset, so
  `prefers-color-scheme` applies (03 §5).

#### Q10. How a studio's page gets a client (M4 Q27, open until M6)

- 04 §5: "`el.client = helm.fromEnv()`, or the page sets `window.helm` once".
- M4 Q27: "A browser has no environment holding `HELM_API` or `HELM_TOKEN`", and
  "Giving a page the token would also expose it to every script on that
  origin."
- **Asset URLs.** The contract says `/assets/{id}` and `/thumb` "need the
  Bearer header, which `<img>` and `<video>` cannot send" and that M6's
  decision "may change the security" of them (M4 first review, #12).
- **The script paths.** 04 §5's zero-build example imports `/sdk/v1/helm-ui.js`
  as a **relative path on the studio's own origin**, which reaches the studio's
  server, not the daemon. The reverse proxy is open (01 §15, deferred in the
  build plan).

Options:
- **(a) CORS plus a token in the page.** The daemon allows each running
  studio's assigned origin, and the studio's server writes its token into the
  page. Every script and every XSS in that page holds a token that can adopt,
  delete and read. Assets still cannot load in `<img>`.
- **(b) The studio's server proxies.** `helm-runtime-sdk` (Go, Python, Node)
  ships a same-origin handler mounted at `/helm/`:
  - It forwards `/helm/api/v1/*` to `HELM_API`, adding `Authorization` from
    `HELM_TOKEN`.
  - It forwards `/helm/sdk/v1/*` without a token.
  - The browser client takes a base URL and never a token.
- **(c) A short-lived page cookie.** This is M9's `SameSite=Strict` cookie,
  scoped to 8700, so a studio's origin cannot use it.

**Recommendation: (b).**
- **The token never enters a page.** This matches M9's posture ("the renderer
  never sees it").
- **Media loads.** `<img src="/helm/api/v1/assets/…">` and `<video>` work,
  which closes M4 #12 for studio pages without weakening the asset endpoints.
- **The example works.** 04 §5's relative `/sdk/v1/…` becomes `/helm/sdk/v1/…`
  and resolves.
- **No new CORS or Origin exceptions** on the daemon.
- **Level 0 stays viable.** The handler is opt-in.

Costs:
- A level-3 studio mounts one handler.
- ltx (stdlib `http.server`) and AuK (FastAPI) need the Python handler to fit
  both. Python's is written against `wsgiref`-style environ plus a plain
  `http.server` adapter, stdlib only.
- The proxy must refuse any path outside those two prefixes, and must not
  forward the page's own cookies or `Authorization`.

The rule change goes into 04 §5 and 07 §3: "the browser client never holds a
token". This is security posture and needs the human's decision.

#### Q11. The launcher's own Gallery screen

- 03 §10: "One query serves both views: the launcher's cross-studio gallery
  and a studio's own panel differ only by scope."
- M4 Q7: "the launcher has no studio-api access in M4 … A launcher principal
  comes with the UI that needs it (M6) or the cookie (M9)."
- **The hole M4's first review closed (#2).** Shared routes answering a
  tokenless request let any local process read what they should not.

Options:
- **(a)** A read-only `launcher`-tagged `/launcher/gallery/items` (and
  `/launcher/assets/{id}`, `/thumb`), like `/launcher/jobs`, under M2's
  Host/Origin rules. Any local process with `curl` can then read every studio's
  items and bytes. The same is already true of launch and stop (Open: "the API
  has no authentication until M9"), but reading everything a user has made is
  more sensitive.
- **(b)** No launcher Gallery until M9's cookie.

**Recommendation:** the human's call, because it widens what an
unauthenticated local process can read.
- **My lean is (b).** M6 builds the gallery as a component (Q13) inside
  studios, and M9 adds the launcher screen with the cookie.
- **If (a) is chosen,** it is recorded next to the M2 "no authentication until
  M9" entry as a known widening.

#### Q12. Components against launcher operations

- 04 §11 rule 2: "No URL, header or response shape appears anywhere in
  `helm-ui-sdk`." Rule 3: "Components receive a client, never build one."
- M4 Q1: only `studio-api` operations are generated into `helm-runtime-sdk`.
  Install logs (`/launcher/jobs/{id}/logs`) and process logs
  (`/studios/{id}/processes/{name}/logs`) are `launcher` operations.
- 03 §7 and §8 show the launcher's own install and process screens with
  exactly the terminal `helm-terminal` is (03-html §7: "Output 1,284 lines
  Follow Copy").

So the launcher cannot use `helm-terminal` through a runtime client. It would
either build a second terminal, against rule 6 ("A component earns its place by
being identical everywhere"), or hand a component a URL, against rule 2.

**Recommendation:**
- **Components depend on a small structural interface**, documented in 04 §5,
  rather than on the runtime client's full type. For `helm-terminal`, that is
  `logs(ref, {lastEventId}) → async iterator of {id, event, data}`.
- **The runtime SDK's browser build implements it** for studio jobs.
- **A launcher browser client** implements it for launcher jobs and process
  logs:
  - It is generated by the existing generator from `launcher`-tagged
    operations into `web/`.
  - It is served only to the daemon's own page, never under `/sdk/`.
- Only generated clients know the wire, and the arrow still points one way.

---

### D · Components

#### Q13. Which components M6 ships, and how far

- **`helm-timeline`.** 04 §5 lists it. M8's brief: "`helm-ui-sdk` ships the
  editor". The timeline API does not exist until M8 (M4 Q1).
- **`helm-player`.** It needs, per 04 §5:
  - filmstrip scrubbing "from a generated sprite sheet"
  - a waveform
  - an "h264 proxy when the source codec is not browser-decodable"

  All three need ffmpeg, which is open and gates M8. `/thumb` answers 501 for
  video and audio (M4 Q13).
- 04 §9: "an endpoint the provider cannot serve returns `Unsupported` and the
  component renders its empty state with a one-line reason."

**Recommendation:**
- **`helm-terminal`:** in full.
- **`helm-gallery`:** in full, with `scope="all"` and `picker`. `scope="all"`
  answers 403 without `gallery.read_all` and renders its reason.
- **`helm-player`:**
  - **In M6:** transport on `requestVideoFrameCallback`, frame stepping, the
    authoritative frame counter, A/B on one transport, loop and speed, and
    extract frame (canvas → `POST /assets`).
  - **Degraded until ffmpeg:** filmstrip, waveform and proxy render
    `Unsupported` with a one-line reason.
- **`helm-timeline`:** moves to M8. 02-milestones and 04 §5 get a note.

#### Q14. How the SDK bundles are served and found

- 04 §8: the daemon serves `/sdk/v1/helm.css`, `/sdk/v1/helm-runtime.js` and
  `/sdk/v1/helm-ui.js`. 04 §9 and the schema's `sdk`: "The daemon injects
  matching bundle URLs at launch". No variable is named.
- **Modules and fonts fail cross-origin.** A `<script type="module">` from
  another origin is fetched in CORS mode with an `Origin` header, which M2
  refuses. `@font-face` from another origin needs CORS too.

**Recommendation:**
- **Serving.** The daemon serves `/sdk/v{major}/…` as static files, `GET`
  only. It is **exempt from the Origin check**, with
  `Access-Control-Allow-Origin: *`: it is public text, the same bytes as the
  npm package.
- **Finding it.** `HELM_SDK_BASE` is injected (for example
  `http://127.0.0.1:8700/sdk/v1`), resolved from the manifest's `sdk` majors.
- **Through the proxy.** Q10's proxy also serves `/helm/sdk/v1/` same-origin.
- **Unsatisfiable majors.** A manifest pinning a major the daemon does not
  serve is refused at launch as 422 `not_launchable`, naming it.

This adds one exception to the M2 Origin rule, so it needs the human.

#### Q15. IBM Plex: bundling fonts

- 03 §3 requires IBM Plex Sans and Mono. The product is loopback-only and must
  work offline, so the fonts ship with `helm-css` and the daemon serves them.
- Plex is SIL OFL 1.1. 03-delegation: "Never delegated … licensing, including
  any new dependency".

**Recommendation:**
- **What ships.** woff2 files for Plex Sans 400/500/600 and Plex Mono 400/500,
  Latin plus Latin-1 (roughly 5 files, about 250 KB).
- **Where from.** The pinned IBM/plex GitHub release, with sha256 recorded, and
  `OFL.txt` shipped beside them.
- **Recorded as a dependency** in `docs/decisions.md` (name, version, licence,
  checksums).
- **The fallback stack** after Plex is `ui-sans-serif, system-ui` and
  `ui-monospace, Menlo`, so a missing font degrades rather than breaks.

---

### E · The gate

#### Q16. Visual regression in both themes

- gate.md: "visual regression, both themes | M6". 04 §10 covers "`helm-css` and
  every component in **both themes**". The review focus: "does the gate run
  it?"
- **It needs a browser.** This host has Google Chrome, Playwright's cached
  Chromium, and Node 26.
- **Where the gate runs.** M1: the gate runs on the host only.

Options:
- **(a) Playwright:** an npm dependency plus a browser download, and Node in
  the gate.
- **(b) chromedp:** a new Go module that drives Chrome over CDP.
- **(c) Standard library only.** Go tests run
  `chrome --headless=new --screenshot --window-size=… --virtual-time-budget=…`
  on fixture pages (one per component and state, per theme) and compare the PNG
  with `image/png`, pixel by pixel within a tolerance.
  - Behaviour tests run the same way: a test page writes its results into the
    DOM, and `--dump-dom` reads them back.
  - Interactive states (focus, hover, dialog open, a failed step expanded) are
    fixture pages that set them, since `--screenshot` cannot interact.

**Recommendation: (c).** No module is added, and the gate does not grow a
package manager. Rules:
- **Finding Chrome.** `HELM_CHROME`, else the standard application path.
- **When it is missing,** the check **fails**, unless
  `HELM_ALLOW_MISSING_BROWSER` is set. This is the M4 second review #10
  precedent.
- **Goldens** are PNGs under `test/visual/golden/`, regenerated only by
  `make golden`.
- **What they bind to.** They are tied to Chrome's major version, recorded
  beside them. A different major fails with a message saying to regenerate
  deliberately, not with a thousand pixel diffs.

The costs are that goldens are host-bound (true of the gate already) and that
(c) cannot test real pointer interaction. Picking (a) or (b) instead is a
dependency decision, the human's.

#### Q17. Theme conformance in `helm validate`

- 03 §5: "`helm validate` fails on raw hex outside the vendored token file, on
  non-Plex families, and on any token pair below 4.5:1 … Advisory locally,
  blocking in registry CI."
- 07 §6: the contrast check "renders the studio's own stylesheet against both
  token sets". That needs a browser.
- 05 §9 criterion 12 is "Expected", not "Required". R63 runs it at the approval
  screen (M7).
- `helm validate` takes manifest files today, not a repository tree.
- **What counts as a literal is undefined:** named colours (`white`),
  `transparent`, `currentColor`, `color-mix()`, inline `style=""` in HTML, and
  colours set from JS. 08 expects h3's tints to "become `color-mix()` over
  tokens".

**Recommendation:**
- **A new mode.** M6 adds `helm validate -theme <dir>`.
- **Where it looks:** `.css` files, and `<style>` and `style=""` in `.html`,
  outside `vendor/helm/`.
- **What it flags:**
  - hex, `rgb()`, `rgba()`, `hsl()`, `hsla()`, `hwb()`, `lab()`, `lch()`,
    `oklab()`, `oklch()`
  - CSS named colours other than `transparent`, `currentColor` and `inherit`
  - any `font-family` that does not resolve to `var(--helm-font-*)`
- **What it allows:** `color-mix()` whose arguments are tokens, keywords or
  percentages.
- **Exit status:** 0 with warnings (advisory); `-strict` exits 1, which is what
  registry CI would run.
- **Contrast** of helm-css's own token pairs (Q6) runs in the gate. Rendering a
  studio's stylesheet for contrast is M7's harness.
- **Out of scope:** colours set from JS are not found, and the report says so.

---

### F · h3's migration

#### Q18. The work is in a repository outside this one

- The milestone: "h3's migration … Migrating them is the milestone's proof that
  the token layer is sufficient."
- h3 studio is `~/GenAI/minimax-h3-mlx/h3c-studio`, pinned at `a6eb54f`. That
  is outside this working directory and outside any file list.
- M5 set the precedent: Q10 and Q16 left changes in other repositories out of
  the DoD, as machine-bound scripts.

Here, though, the migration **is** the proof. Leaving it out removes the one
check that the tokens are enough.

**Recommendation:**
- **Where it is done.** M6 makes the change on a branch `helm-tokens` in the h3
  checkout; the human approves writing there at kickoff. It commits nothing
  there and pushes nothing.
- **The proof, in this repository.**
  - `helm validate -theme -strict` on that branch reports zero literals.
  - Visual fixtures render h3's `index.html` against helm-css in both themes.
- **Afterwards.** Once the human merges upstream, the `ref` in
  `studios/h3-studio.yaml` moves.

#### Q19. 08's counts do not reproduce at the pinned ref, and the mapping has gaps

Measured in `static/style.css` at `a6eb54f`:

| | 08 | Measured |
|---|---|---|
| Custom properties | "45 CSS variables" | **20 distinct** (54 declarations across the dark, system-light and explicit-light blocks) |
| Raw colour literals outside them | "64" | **50** (26 hex, 24 `rgb`/`rgba`) |

08's mapping table has no row for:
- `--amber-d`
- `--term-text`, `--term-prompt`
- `--r` (8px, against 03's 6px)
- `--mono`, `--sans`

`--pane`, `--raised` and `--raised-2` go to two tokens.

h3's own design also disagrees with 03 in three places:
- **The terminal.** h3's goes light in light theme (`--term-bg: #fbfaf7`),
  against 03 §2: "The terminal stays dark in both themes."
- **The success colour.** `--ok` is lime `#aad94c`, against `status-running`
  green.
- **The theme switch.** h3 keeps its own System/Light/Dark switch in
  `localStorage` (`static/app.js:1052`).

**Recommendation:**
- **The baseline** is the lint's output at `a6eb54f`, recorded in the report.
  08 gets a note beside its figures.
- **Proposed rows for the missing mappings:**
  - `--amber-d` → `color-mix(in srgb, var(--helm-studio-accent) 45%, var(--helm-ground-page))`
  - `--term-*` → `--helm-ground-inset` and `--helm-log-*`
  - `--r` → `--helm-radius-md`
  - fonts → `--helm-font-*`
- **Visible changes are listed and accepted up front:** warmer grounds (08
  expects this), a dark terminal in light theme, a 6px radius, Plex, and green
  for success.
- **Hosted, the launcher's theme wins.** h3's own switch is hidden while a
  theme stream answers. Standalone it keeps working. The design is silent on a
  studio's own theme control, and this rule would apply to every studio, so it
  goes into 07 §6.

#### Q20. The build plan's demo against 07 and 08

- 01-build-plan phase 5 demo: "h3's take list replaced by `<helm-gallery>`".
- 07 §5: at level 2 a studio "renders its own panel from `GET /gallery/items` —
  which is what h3 studio would do". 08's manifest: `sdk: { runtime: "^1", css:
  "^1" }  # no ui-sdk: it has its own interface`.
- The M6 milestone's demo is only the theme toggle.

**Recommendation:**
- The design wins: h3 stays at level 2 and keeps its take list.
- The build-plan line gets a note.
- `<helm-gallery>` is shown working in a fixture studio, not in h3.

---

### G · Launcher behaviour the screens need decided

#### Q21. What the screens need that the API does not have

**Hugging Face token.**
- R18 "prompts for a token"; 03 §18: "Replace it in Settings". Open: "there is
  no way to set the Hugging Face token."
- **Recommendation:** launcher `PUT` and `DELETE /launcher/settings/huggingface-token`
  through the existing Keychain seam. The value is write-only: `GET` answers
  only `{present, added_at}` (R43).

**Doctor.**
- It is in the nav (03 §6) and R5b. `/doctor` was removed (M4 Q1), and no
  milestone names it.
- **Recommendation:** hidden in M6. The human names a milestone.

**Verify.**
- 03 §12 shows it; weight verification is open.
- **Recommendation:** not shown.

**"Use in…".**
- R39 (amended in M4): delivering into a stage directory "without exposing a
  blob's inode is decided with the launcher UI in M6". A hardlink shares the
  blob's inode; a copy of a 2 GB take is slow; `clonefile(2)` on APFS gives a
  new inode with no data copied.
- **Recommendation:**
  - M6 delivers with `clonefile` behind a `platform.CloneFile` seam, falling
    back to a copy on another volume or OS.
  - "Use in…" shows only once Q11 gives the launcher gallery access, so with
    (b) it moves to M9 with the gallery screen.
  - If the human prefers M6 not to touch this, the entry stays open.

#### Q22. The switch dialog's copy against M5's confirmation

- 03 §9 offers "Don't ask again this session".
- M5 Q13: a confirmation is a digest over a snapshot, "refused when the running
  studio changed after the user read it". A remembered consent is a confirmation
  nobody read.
- 03 §9's body says "both sets of activations do not fit alongside each other".
  M2: the rule applies "whether or not the sum fits", so for two small heavy
  studios that sentence is false.

**Recommendation:**
- **"Don't ask again this session"** is offered and honoured only while the
  running studio reports `idle`. `busy` and `unknown` always ask. Even when
  remembered, the page fetches a fresh conflict and sends that digest.
- **The body** states the arithmetic from `details.heavy` and the rule. It
  claims "do not fit" only when the sum exceeds host memory.

---

### Defaults I will take unless told otherwise

- **Package directories:** `packages/helm-css/` and `packages/helm-ui-sdk/`.
  This is CLAUDE.md's layout, which matches the existing
  `packages/helm-runtime-sdk/`, not 04 §10's `packages/css/` and `ui/`. 04 §10
  gets a note.
- **No bundler and no npm dependencies.**
  - `helm-ui-sdk` is hand-written ES modules, one per component, plus an index
    module. The daemon serves them as they are, embedded with `go:embed`.
  - `helm.css` and `helm.min.css` are produced by a small Go step and
    drift-checked like generated clients.
  - Publishing to npm is left to the human.
- **The launcher UI** stays in `web/`, embedded, with no build step. `shelf.js`
  is replaced.
- **Card states** follow 03-html §6's table, plus:
  - `auth_required` → "Needs a Hugging Face token", primary "Add token"
  - a studio re-adopted without its manifest → "Running · manifest not loaded",
    Stop only
  - `env_failed` reads as `failed_build` with the failure's message
- **Models & disk:** the "Orphaned" label stays as the UI word for an artifact
  with no bindings. M3 removed the stored state, not the idea.
- **Live regions and motion** as 03-html §16–§17, including announcing progress
  only at 0/25/50/75/100 %.
- **Tests:** fixture pages use a fake client object. Components receive a
  client, so no daemon is needed for component tests. The theme path gets one
  end-to-end test in Go: daemon → theme stream → a studio page through the
  proxy, read with `--dump-dom`.
- **Machine-bound:** h3 studio really re-themed while running and mid-render,
  under the real daemon; ltx and AuK only if their servers mount the proxy
  (upstream).

### Notes, no decision needed

- **Existing open entries this kickoff would resolve:**
  - browser clients (M4 Q27)
  - `HELM_THEME`/`HELM_ACCENT` injection (M4 Q28)
  - R39's stage delivery, if Q21 is taken
  - the Hugging Face token setting
  - the `settings` table, for the theme only
- **Open entries it leans on without resolving:** ffmpeg (the player's degraded
  parts), weight verification (Verify), and the reverse proxy and `--base-path`
  (Q10 avoids needing it).
- **03-html §6's catalogue card** says "~60 GB to download" for h3. M0
  measured 62 GB required plus 134 GB optional. The screen shows what the
  manifest declares, not the mockup's figure.

---

### Proposed expansion (draft, for Q1)

Written as one brief ordered for the Q4 split. If Q4 is taken, tasks 0–6 are
M6a and 7–10 are M6b.

#### Reads

- 03 (markdown, and 03-html §6–§18 until task 0 lands), 04, 07 §3 and §5–§6,
  08, 01 R18, R39, R41, R43 and R50–R53
- `api/openapi.yaml` (events, assets, launcher)
- `docs/decisions.md` in full
- M4's and M5's reports
- h3's `static/style.css`, `index.html` and `app.js` at `a6eb54f`

#### Tasks

0. **The contract.**
   - Convert 03-html §6–§18 into 03 (Q3).
   - Add the token table (Q5), the contrast pairs and focus ring (Q6), and the
     hue rule and ramp (Q7).
   - Amend 04 §5, §8 and §10, 07 §3 and §6, and 01 R39 per the answers.
   - Each is its own reviewed change with a decision entry.
1. **helm-css.**
   - The files: `helm-tokens.css`, `helm-base.css`, `helm-layout.css`,
     `helm-components.css`, `helm.css`, `helm.min.css`, `tokens.json`.
   - Plex fonts with `OFL.txt` (Q15).
   - Both themes, in the three-state `:root` / `prefers-color-scheme` /
     `[data-theme]` pattern.
2. **Theme and accent.**
   - Schema v5 `settings`.
   - The launcher theme operations and the tokenless theme stream (Q8, Q9).
   - `HELM_THEME`, `HELM_ACCENT_DARK`/`_LIGHT` and `HELM_SDK_BASE` injected at
     spawn (Q7, Q14).
   - The id-derived hue ramp.
3. **Browser access.**
   - The same-origin proxy handler in the Go, Python and Node runtime SDKs.
   - The browser ESM build with `themeBridge()` (Q10).
   - `/sdk/v1/*` served with the Origin exemption (Q14).
4. **Enforcement.** `helm validate -theme [-strict]` (Q17), and the helm-css
   contrast check in the gate (Q6).
5. **Visual regression.** `test/visual`, headless Chrome from the standard
   library, goldens for helm-css in both themes, and `make golden`. It is in
   `make gate` (Q16).
6. **h3.** The migration on a branch in the h3 checkout (Q18, Q19); h3's hue in
   `studios/h3-studio.yaml` (Q7).
7. **helm-ui-sdk.** `helm-terminal`, `helm-gallery`, and `helm-player` with
   degraded ffmpeg parts (Q13), against structural client interfaces (Q12).
   Goldens in both themes.
8. **Launcher browser client.** Generated from `launcher` operations into
   `web/` (Q12); drift-checked.
9. **Screens** (Q2): shell, catalogue, detail and install, process group,
   launch and switch (Q22), models and disk, settings (Q21). Goldens in both
   themes at 1280, 1000 and 380 px.
10. **Gallery and "Use in…",** only as Q11 and Q21 decide.

#### File list

- `packages/helm-css/**`, `packages/helm-ui-sdk/**` (new)
- `packages/helm-runtime-sdk/{go,python,node}/**`: hand-written proxy, browser
  build and bridge; generated files only through `make generate`
- `api/openapi.yaml`: launcher theme and settings operations, the theme stream,
  and the asset-access text from M4 #12; `api/gen/**`
- `internal/api/**`, `internal/store/**` (v5), `internal/supervisor/**`
  (injection), `internal/platform/**` (`CloneFile`, if Q21)
- `cmd/helm/**` (`validate -theme`), `web/**`
- `test/visual/**` (new), `test/conformance/**` (a theme-event case), `Makefile`
- `studios/h3-studio.yaml` (`hue`, and later `ref`)
- `docs/design/01-prd.md`, `02-data-model.md`, `03-design-system.md`,
  `04-packages.md`, `07-platform-services.md`, `08-h3-dry-run.md`;
  `docs/plan/01-build-plan.md` and `02-milestones.md` (notes only)
- `docs/decisions.md`, this report
- **Outside this repository, with approval:** a `helm-tokens` branch in
  `~/GenAI/minimax-h3-mlx/h3c-studio`

#### Definition of Done

- **The contract is whole.** 03's markdown carries §6–§18 and the token table.
  No rule M6 implements is cited only from 03-html.
- **Tokens are the API.**
  - Every name in 03's table exists in `helm-tokens.css` and `tokens.json`, and
    nothing else does.
  - A test diffs the three.
  - A renamed or removed token fails the gate.
- **Contrast.** Every declared pair meets its threshold in both themes, checked
  in the gate, and a deliberately darkened `text-secondary` fails it.
- **helm-css rules.** Every class starts `helm-`; no `!important`; every
  selector has single-class specificity. Checked by a test over the files.
- **Visual regression in both themes is in `make gate`.**
  - A one-token change fails it.
  - A missing Chrome fails it, unless explicitly allowed.
- **The dependency arrow.**
  - `grep -rE 'fetch\(|XMLHttpRequest|EventSource|/api/v1|Authorization'
    packages/helm-ui-sdk` finds nothing.
  - No component constructs a client.
  - `helm-runtime-sdk` imports nothing from `helm-ui-sdk`, and `helm-css`
    references no component markup.
- **The token never reaches a page.** A test loads a studio page through the
  proxy and finds no `hs_live_` in the DOM, the scripts or the responses the
  page can read. The proxy refuses paths outside its two prefixes.
- **Theme reaches a running studio.** An end-to-end test: launch a fixture
  studio, open its page, `PUT` the theme, and the page's `data-theme` changes
  without a reload. A studio with no capabilities follows it too.
- **h3.** `helm validate -theme -strict` on h3's branch reports zero literals,
  and the baseline at `a6eb54f` is recorded. Every literal left behind is a
  token added to 03, or a finding.
- **Screens and components,** as Q2 and Q13 settle, each with goldens in both
  themes and an unmapped-state test (a raw state name renders in an idle chip,
  never blank).
- **`make gate` is green.**
- **Machine-bound.** On the Mac with the real daemon:
  1. Launch h3 and start a render.
  2. Toggle the launcher System → Light → Dark.
  3. h3's page follows each change within a frame, with no reload.
  4. The render continues.

#### Review focus (carried forward)

- **The milestone's four questions:** wire format in components; renamed
  tokens; both themes in the gate; running studio versus reload.
- **The token.** Can a page, a log line, a proxy error or a served bundle ever
  contain a studio token? (M4's cross-studio lesson; M9's renderer rule.)
- **The Origin exemption.** Does `/sdk/` answer anything but static `GET`s, and
  does the tokenless theme stream carry anything but the theme?
- **The tests.** Would the visual and contrast checks fail on a deliberate
  one-token change, or do they only prove a file rendered? (M4 and M5 both
  found gaps by planting bugs.)
- **One code path.** Any special case for a specific studio, in Go, in the
  lint or in helm-css, means the contract is wrong (M5).
- **Stated as verified?** Is anything machine-bound stated as verified?
