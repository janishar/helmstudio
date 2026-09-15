# M6 — Design and UI

**Effort:** 10–12 days. **Machine-bound demo:** the theme toggle re-themes running studios.

Expanded at kickoff from the questions in `docs/agents/reports/06-design-and-ui.md`,
answered "recommendations for all" (`docs/decisions.md`, "2026-09-16 · M6 design
and UI", Q1–Q22). Two briefs, reviewed separately (Q4): **M6a** is built and
reviewed first; **M6b** is built on a reviewed M6a.

## Reads

- `docs/design/03-design-system.md` (all of it, once task 0 lands), `04-packages.md`,
  `07-platform-services.md` §3, §5, §6, `08-h3-dry-run.md`
- `docs/design/01-prd.md` R18, R39, R41, R43, R50–R53
- `api/openapi.yaml`: events, assets, launcher
- `docs/decisions.md` in full; M4's and M5's reports
- h3 studio's `static/style.css`, `index.html` and `app.js` at `a6eb54f`

## Known at freeze

- Three packages, one-way dependency: `helm-css` → `helm-runtime-sdk` →
  `helm-ui-sdk`. **The arrow never reverses.**
- **Only the runtime SDK knows the wire format. Components receive a client;
  they never construct one.**
- Accent yellow `#ffc700` dark, `#f5b800` light. Warning is orange.
- IBM Plex Sans and IBM Plex Mono.
- h3's literals migrating onto tokens is the proof that the token layer is
  sufficient; a literal left behind is a token missing.

---

## M6a — tokens, helm-css, the theme path, h3

### Tasks

0. **The contract.**
   - Convert `docs/artifacts/03-design-system.html` §6–§18 into 03, text and
     tables verbatim, mockups marked in place (Q3).
   - Add the token table (Q5), the contrast pairs and focus ring (Q6), and the
     hue rule and ramp (Q7) to 03.
   - Write the `settings` DDL into 02 (Q8).
   - Amend 04 §5, §8, §10, 07 §3, §6, 08, 01 R39 and the plan's notes per the
     answers.
1. **helm-css** (`packages/helm-css/`).
   - `helm-tokens.css`, `helm-base.css`, `helm-layout.css`,
     `helm-components.css`; `helm.css`, `helm.min.css` and `tokens.json` built
     from them by a Go step and drift-checked by a test.
   - Plex fonts with the release's licence (Q15).
   - Both themes, in the three-state pattern: `:root`, `prefers-color-scheme`
     without `data-theme`, `[data-theme]`.
2. **Theme and accent.**
   - Schema v5 `settings`.
   - `GET`/`PUT /launcher/settings/theme`; tokenless `GET /theme` and
     `GET /theme/events` (Q8, Q9); `theme` emitted on `/events`.
   - `HELM_THEME`, `HELM_ACCENT_DARK`, `HELM_ACCENT_LIGHT` and `HELM_SDK_BASE`
     injected at spawn; the id-derived hue ramp (Q7, Q14).
   - A System/Light/Dark control on the plain shelf, so the demo can run before
     M6b's screens.
3. **Browser access.**
   - The same-origin proxy in the Go, Python and Node runtime SDKs (Q10).
   - The browser ESM build of `@helmstudio/runtime` with `themeBridge()`.
   - `/sdk/v1/*` served with the Origin exemption (Q14).
4. **Enforcement.** `helm validate -theme <dir> [-strict]` (Q17); helm-css's
   contrast pairs checked in the gate (Q6).
5. **Visual regression** (`test/visual/`): headless Chrome over the DevTools
   pipe from the standard library; goldens for helm-css in both themes;
   `make golden`; in `make gate` (Q16).
6. **h3.**
   - The migration on a `helm-tokens` branch in the h3 checkout (Q18, Q19),
     uncommitted there.
   - h3's hue in `studios/h3-studio.yaml` (Q7).

### File list

- `packages/helm-css/**` (new)
- `packages/helm-runtime-sdk/{go,python,node}/**`: hand-written proxy, browser
  build and bridge; generated files only through `make generate`
- `api/openapi.yaml`: launcher theme operations, the theme stream, the
  asset-access text from M4 first review #12, the `/events` theme text;
  `api/gen/**` if generation needs it
- `internal/api/**`, `internal/store/**` (v5), `internal/supervisor/**`
  (injection), `internal/manifest/**` (reading `hue` and `sdk`), `internal/theme/**`
  and `internal/themelint/**` (new)
- `cmd/helm/**` (`validate -theme`, `helm dev` serving the theme),
  `cmd/helmstudio/**`, `web/**`
- `test/visual/**` (new), `test/conformance/**` (theme and proxy cases),
  `Makefile`
- `studios/h3-studio.yaml` (`hue`, later `ref`)
- `docs/design/01-prd.md`, `02-data-model.md`, `03-design-system.md`,
  `04-packages.md`, `07-platform-services.md`, `08-h3-dry-run.md`;
  `docs/plan/01-build-plan.md` and `02-milestones.md` (notes only);
  `docs/agents/gate.md`
- `docs/decisions.md`, the report
- **Outside this repository, approved at kickoff (Q18):** a `helm-tokens` branch
  in `~/GenAI/minimax-h3-mlx/h3c-studio`

### Definition of Done

- **The contract is whole.** 03's markdown carries §6–§18, the token table and
  the contrast pairs. No rule M6 implements is cited only from the reading copy.
- **Tokens are the API.** Every name in 03's token table exists in
  `helm-tokens.css` and `tokens.json`, with the same values, and nothing else
  does. A test diffs the three; a renamed or removed token fails the gate.
- **Contrast.** Every declared pair meets its threshold in both themes, checked
  in the gate; a deliberately darkened `text-secondary` fails it.
- **helm-css rules.** Every class starts `helm-`; no `!important`; every
  selector's specificity is at most one class. A test reads the files.
- **Visual regression in both themes is in `make gate`.** A one-token change
  fails it; a missing Chrome fails it unless explicitly allowed.
- **The token never reaches a page.** Through each of the three proxies: a
  studio-api call succeeds with the token added; no response the page can read
  contains `hs_live_`; every `launcher` path in `api/openapi.yaml`, and any path
  outside the two prefixes, is 404 without reaching the daemon; the page's own
  `Authorization` and cookies are not forwarded.
- **Theme reaches a running studio.** End to end in a real browser: a fixture
  studio behind the proxy, its page open, `PUT /launcher/settings/theme`, and
  the page's `data-theme` follows without a reload. A studio with no
  capabilities follows it too.
- **Injection.** `HELM_THEME`, `HELM_ACCENT_DARK`, `HELM_ACCENT_LIGHT` and
  `HELM_SDK_BASE` reach a spawned process; a studio without `hue` gets its ramp
  entry, the same one every launch; an unserved `sdk` major refuses the launch.
- **h3.** `helm validate -theme -strict` on h3's `helm-tokens` branch reports
  zero findings, and the baseline at `a6eb54f` is recorded. Every literal left
  behind is a token added to 03, or a finding.
- **`make gate` is green.**
- **Machine-bound.** On the Mac with the real daemon:
  1. Launch h3 from the `helm-tokens` branch and start a render.
  2. Toggle the shelf System → Light → Dark.
  3. h3's page follows each change without a reload.
  4. The render continues.

### Review focus

- The milestone's own four:
  - Does any component import the wire format, construct a client, or know a
    URL?
  - Did any token get renamed?
  - Are both themes covered by visual regression, and does the gate run it?
  - Does the toggle reach a running studio, or only a reload?
- **The token.** Can a page, a log line, a proxy error or a served bundle ever
  contain a studio token? Can a page reach a launcher operation through its
  proxy?
- **The Origin exemption.** Does `/sdk/` answer anything but static `GET`s, and
  does the tokenless theme stream carry anything but the theme?
- **The tests.** Would the visual and contrast checks fail on a deliberate
  one-token change, or do they only prove a file rendered? Plant bugs.
- **One code path.** Any special case for a specific studio, in Go, in the lint
  or in helm-css, means the contract is wrong.
- **Stated as verified?** Is anything machine-bound stated as verified?

---

## M6b — components and screens

Built on a reviewed M6a. Its file list and DoD are confirmed at M6b's kickoff
against what M6a's review changed.

### Tasks

7. **helm-ui-sdk** (`packages/helm-ui-sdk/`): `helm-terminal`, `helm-gallery`,
   and `helm-player` with its ffmpeg parts degraded (Q13), against structural
   client interfaces (Q12). Goldens in both themes.
8. **The launcher browser client.** Generated from `launcher` operations into
   `web/` (Q12); drift-checked.
9. **Screens** (Q2):
   - the shell
   - catalogue
   - detail and install
   - process group
   - launch and switch (Q22)
   - models and disk
   - settings, with the Hugging Face token (Q21)

   Goldens in both themes at 1280, 1000 and 380 px.

### Definition of Done (draft)

- **The dependency arrow.**
  - `grep -rE 'fetch\(|XMLHttpRequest|EventSource|/api/v1|Authorization' packages/helm-ui-sdk`
    finds nothing.
  - No component constructs a client.
  - `helm-runtime-sdk` imports nothing from `helm-ui-sdk`, and `helm-css`
    references no component markup.
- **Screens and components** as Q2 and Q13 settle, each with goldens in both
  themes. An unmapped state renders its raw name in an idle chip, never blank.
- **`make gate` is green.**
