# M0 — Contracts — implementation report

This report was revised after a peer review (docs/agents/reviewer.md protocol)
found 4 BLOCKING, 12 SHOULD-FIX and 12 NOTE issues against the first pass —
verdict "does not land." Every finding was independently reproduced before
being acted on. All 4 BLOCKING and all 12 SHOULD-FIX are fixed or escalated
below; the NOTE items are addressed or explicitly left as acknowledged, minor
gaps. One thing in the review I want to be direct about: its single most
serious finding (BLOCKING, h3's build command) was right, and it falsified
this report's own first-draft claim that all four manifests were "written
from the real repositories, not from the design docs' examples." That
specific field wasn't. See below.

A second round of reviewer follow-ups (five items, raised against the fix
summary rather than the tree) changed this report again. In short: the
full manifests in `studios/` are now described as a placeholder, not as a
reading of the design. AuK's backends, the `format` assertion and the two
added rules each have their own decision-log entry. `TestRunSugarPointerPath`
is removed. The OpenAPI tag split is recorded as a proposal for M4. Each
change is marked "(follow-up)" where it appears below.

## What was built

`cmd/helm validate` and the `internal/manifest` package it's built on: schema
validation against `schema/manifest.json` (via a JSON Schema draft 2020-12
library, format assertions turned on) plus the seven semantic rules the
schema cannot express — plus two more in the same category the review
correctly said belonged there (unique process/weight names, a closed set of
`{...}` substitutions) — each with a specific-error test fixture, and each
error carrying a line number resolved against the manifest's own YAML node
tree. The four studio manifests were revised against the real repositories
after the review caught one field (h3's build command) that wasn't. `helm
validate`'s exit codes and `--json` output have their own tests now.
`api/openapi.yaml` was rewritten against `docs/design/05-sdk-and-custom-studios.md`
§3/§7 and `docs/design/07-platform-services.md` §3-4's literal paths, which
the first pass did not read.

## Gate

    $ make gate
    fmt: clean
    vet: clean
    ok  	github.com/janishar/helmstudio/cmd/helm	0.5s
    ok  	github.com/janishar/helmstudio/internal/manifest	1.0s
    studios/auk-studio.yaml: ok
    studios/h3-studio.yaml: ok
    studios/iris-studio.yaml: ok
    studios/ltx-studio.yaml: ok
    gate: green

Run on macOS Apple Silicon only. `bin/helm` was rebuilt by hand before every
`make gate` run in this session — `make gate`'s own `validate` target does
not depend on `build` (see "Left undone"), so this was necessary, not
automatic.

## Design contradictions raised

Ordered roughly by how much they block someone else's work.

**1. `schema/manifest.json`'s own `run` sugar is unusable — a hard
self-contradiction.** The schema's top-level `required` is unconditional:

    "required": ["id", "name", "kinds", "requires", "runtime", "processes"],

`run` is documented as an alternative to `processes[]`, mutually exclusive
with it via `allOf`'s `not`, but nothing waives `processes` from the
top-level `required` array when `run` is used instead. A manifest using only
`run:` always fails schema validation with "missing properties: 'processes'"
— confirmed against the built validator:

    $ ./bin/helm validate run-sugar-test.yaml   # run: {name: studio, cmd: ...}, no processes:
    run-sugar-test.yaml: invalid
      run-sugar-test.yaml: /: missing properties: 'processes'

What I would do: move `processes` out of the unconditional `required` and add
`"anyOf": [{"required":["processes"]}, {"required":["run"]}]` alongside the
existing mutual-exclusion `allOf`. Not fixed here — `schema/manifest.json` is
out of this milestone's file list. Not blocking: none of the four studios use
`run:`; all four are single-process and use one-element `processes[]`.

(follow-up) The `run:` branch in `effProcesses` (`rules.go`) has no test.
An earlier revision had `TestRunSugarPointerPath`, which built a `Manifest{}`
by hand and skipped the schema. The reviewer asked whether it would catch a
deliberate bug in anything a user can reach. It would not: while this schema
bug stands, no manifest that passes schema validation has `run` set, so the
test covered unreachable code, not the contract. I removed it. The gap belongs
to this contradiction: once the schema is fixed, cover the branch through
`Validate()` with a run-only fixture. A comment on `effProcesses` says so.

Softer, related: the same `run` description also implies an omitted
`name`/`role` default to `"studio"`/`"main"`. `role` has a schema default;
`name` does not — `$defs/process` requires it unconditionally, `run`
included. Not blocking for the same reason as above.

**2. `studios/*.yaml` holding full manifests, not registry pointers, contradicts
three other sources — and the schema can't currently support what they
describe instead.** `CLAUDE.md`'s layout table says `studios/` is "registry
pointers, one per studio." `docs/design/05-sdk-and-custom-studios.md` §5a
gives the literal shape of one:

    # studios/h3-studio.yaml in the helmstudio repo — the whole registry entry
    id: h3-studio
    repo: https://github.com/janishar/h3c-studio
    ref: v1.4.2                  # the reviewed commit; the manifest that runs is the one reviewed
    certified: verified

Four lines: `id`, `repo`, `ref`, `certified`. But `schema/manifest.json` has
no `certified` (or any certification-level) field at all — confirmed:

    $ ./bin/helm validate a-manifest-with-certified-verified-appended.yaml
    ...: /: additionalProperties 'certified' not allowed

and none of the four real repos has published its own `helmstudio.yaml`, so
per §5a's own precedence table, source 3 applies — "an inline manifest
carried by the registry pointer, for repos with none." What that combined
shape actually looks like (a pointer envelope with an embedded manifest? the
manifest itself carrying `ref` and nothing else pointer-shaped, since
`certified` has nowhere to go?) is not specified anywhere I can find, and this
milestone's own concrete DoD ("`helm validate studios/*.yaml` exits 0 on all
four," checked against the existing schema) can only be satisfied by
something schema-shaped — which a bare 4-line pointer is not, since the
schema has no pointer shape to validate it against.

The schema argues against full manifests too. Its own top-level
description says "The registry holds pointers to these, never copies."

What I did (follow-up, reworded): **the full manifests in `studios/*.yaml`
are a placeholder.** I did not read the design as calling for them. They are
full manifests only because the schema has no pointer shape to validate
against, and M0's gate runs `helm validate studios/*.yaml`. An earlier
revision of this report and of `docs/decisions.md` called them "the closest
schema-valid approximation" of §5a's inline-manifest source. No design text
supports that description; it has been withdrawn in `docs/decisions.md`. Each
of the four files now opens with a `PLACEHOLDER SHAPE` header naming the open
decision, so the files show that they depend on it. `ref` was added to all
four because the schema does support it. I did not invent a `certified` field
or a pointer-envelope wrapper. That would resolve an underspecified shape in
code. This needs a human decision: either
`schema/manifest.json` grows a certification field and `studios/*.yaml`
becomes genuinely thin once these four studios publish their own
`helmstudio.yaml`, or §5a's four-line example and `CLAUDE.md`'s layout table
are describing a later state (post-M7, once the three-source library and
certification levels exist) that M0 was never meant to produce.

**3. `docs/agents/milestones/00-contracts.md` rule 3 ("exactly one process has
role: main") contradicts `docs/design/05-sdk-and-custom-studios.md` §9
criterion 4 ("at least one main process").** `helm validate` implements the
milestone's literal text (`ruleExactlyOneMain`, `rules.go`). Not reconciled;
whichever is right should replace the other in whichever document is wrong.

**4. `docs/design/08-h3-dry-run.md`'s hue for h3 (`#ffb454` dark / `#b5721a`
light, matching `h3c-studio/static/style.css`'s actual `--amber` values)
disagrees with `docs/design/01-prd.md` §13 and `docs/design/07-platform-services.md`
§3's `HELM_ACCENT=#e0a33c` example — two documents on each side.** I used the
code-verified pair (ch08 + the real CSS) for `studios/h3-studio.yaml`'s `hue`,
on the theory that a value confirmed against the studio's actual stylesheet
outranks an illustrative example repeated in two other places — but two
documents said the other number, so this is a real disagreement, not a typo
in one place.

## Where the review found a real bug in this package (not a design gap)

- **Cycle detector left DFS state dirty across independent subtrees.**
  `visit()`'s two early-return paths (`return true` on finding a cycle, and
  the propagated return from a recursive call) skipped popping the node off
  `stack` and never marked it `done`. Two disjoint cycles — `a<->b` plus a
  separate `c->d->c` — were reported as one cycle, `a -> b -> c -> d -> c`,
  pulling `a` and `b` into a report about a cycle they aren't part of.
  Reproduced, fixed with a `defer` that always pops the frame and marks the
  node done regardless of which return path is taken, and covered by
  `TestCycleDetectorHandlesDisjointCycles`, which asserts both cycles are
  reported separately and that neither report crosses into the other.
- **Schema-error pointers had a doubled leading slash** (`//processes/0/port`)
  because the library's `InstanceLocation` already carries its own leading
  slash for non-root locations and the code prepended another one. Fixed
  (`pointerFrom`); every schema Pointer in this package's output is now `/`
  or `/a/b/c`, never `//a/b/c`.
- **Schema error messages for `not`/`oneOf` failures were the library's raw,
  context-free text** — "not failed" for both port modes set at once, "valid
  against schemas at indexes 0 and 1" for two health probes, two separate
  "missing properties" lines (reading as "both required") for neither `repo`
  nor `local_path`. Fixed: a small table (`knownKeywordMessages`) intercepts
  the four keyword locations in `schema/manifest.json` that produce these
  messages and substitutes hand-written text describing what the schema
  actually means at that point, sourced from the schema's own adjacent
  `description` fields where one exists.
- **No line numbers anywhere**, despite PRD R6 ("prints schema errors with
  line numbers"). Fixed: `internal/manifest/lines.go` parses the manifest a
  second time as a `yaml.Node` tree and walks a JSON pointer's segments
  through it to find the 1-based line a pointer resolves to (falling back to
  the containing mapping's line when the pointer names a property that
  doesn't exist — the common case for a "missing properties" error). Wired
  into both schema and semantic errors.
- **`format: uri` was declared but not enforced** — `repo: "not a uri at all"`
  passed, because draft 2020-12 treats `format` as an annotation unless a
  compiler asks it to assert. Fixed: `Compiler.AssertFormat = true`.
  (follow-up) An earlier revision said this "does not change" the contract.
  That was too quick. The schema file is untouched, but this validator now
  rejects manifests a default 2020-12 validator accepts (`repo` and
  `license_url` are the affected fields). Of everything in M0, this comes
  closest to a contract change. It has its own decision-log entry, which asks
  for sign-off and notes that M1's loader, registry CI and `helm dev` must
  make the same choice.
- **`--json` shrank the output array below the input count** when a file
  couldn't be read (the error went to stderr only, and the file was skipped
  entirely rather than getting an entry), and a clean manifest serialised
  `"errors": null` instead of `"errors": []`. Both fixed: every input file now
  gets exactly one `Result` (an unreadable file gets a `"rule": "read"`
  error inside it), and a nil error slice is normalised to `[]` before
  encoding. `cmd/helm` had no tests at all before this round; it now has
  `validate_test.go` covering exit codes, `--json` shape, and usage output —
  `runValidate` and `printText` now take `io.Writer` parameters instead of
  writing straight to `os.Stdout`/`os.Stderr`, which is what made that
  testable.
- **A file with more than one `---`-separated YAML document silently
  validated only the first**, dropping the second with no error —
  `yaml.Unmarshal`'s default behaviour. Fixed: `rejectMultiDocument` decodes
  twice and errors if a second document exists, before schema validation
  runs.
- **Test assertions checked presence, not exclusivity** — `strings.Contains`
  against any matching error in the slice, which is exactly permissive
  enough that the cycle-detector bug above would have passed a test only
  checking "is there a no-cycle error mentioning arrows." Fixed: a
  `exactlyOne` helper now asserts the fixture produced precisely one error
  with the expected rule and message. Also added: build-step cwd escape,
  absolute cwd, `import.cwd` escape (see next item), and the
  `TestSchemaErrorsHaveLineNumbers` / cmd-level tests above.
- **`import.cwd` was never checked for escaping the studio root** — `rules.go`
  had a `Process`/`BuildStep` case but no `Import` type existed in `types.go`
  at all, so `import: {cwd: "../../.."}` validated clean. Fixed: added
  `Import` to `types.go`, wired into `ruleCwdWithinRoot`.
- **Duplicate process or weight names passed validation.** `process.name`'s
  own schema description says "Unique within the studio," which JSON Schema's
  `uniqueItems` cannot express (it compares whole array items, not one field
  of each) — the same category as the seven listed rules, and the review is
  right that it should have been one of them from the start. Added as
  `ruleUniqueNames`. Without it, a weight literally named `selected` was
  accepted and then had its own `{models.selected}` reference — which means
  a different thing — rejected by rule 5, a genuinely confusing failure mode
  this closes off. (follow-up) Decision-log entry added. For weights, the
  uniqueness is inferred, not stated: the schema calls a weight's name "the
  placeholder key," and never says "unique."
- **Unknown `{...}` placeholders passed silently** — `{prot}`, `{dta}`, a bare
  `{models}` with no name. `process.cmd`'s schema description names a closed
  set: `{port} {ports.<name>} {models.<name>} {models.selected} {root} {data}
  {venv}`. Added `ruleKnownPlaceholders`, rejecting anything outside that set
  — a typo now fails validation instead of reaching a spawned process's argv
  as the literal string `{prot}`. (follow-up) Decision-log entry added. It
  records a cost the first revision didn't mention. The schema defines no
  escape, so a legitimate literal brace in a command, such as shell
  `${HOME}`, is rejected too (confirmed by hand: "references {HOME}"). M2's
  substitution must agree with this rule, or the rule is stricter than the
  runtime.

## Manifest corrections from the review

- **h3's build command was wrong.** `run: make mps` — copied from
  `docs/design/01-prd.md` §13's illustrative example, not read from
  `h3c/Makefile`, which has no `mps` target at all (`all`, `test`, `parity`,
  `real-parity`, `clean`; `all` is default). The real command, confirmed
  against both the Makefile and the README, is `make -j8` in `h3c`. This is
  the one place this report's original claim about method was simply false,
  and I'm not going to soften that. `ffmpeg` was also missing from
  `requires.tools` — `server/media.go` refuses to run without it
  (`"ffmpeg/ffprobe not found"`), and the README confirms it as a hard
  requirement neither design example mentions.
- **`ref` added to all four manifests**, pinned to the exact commit each was
  read at (see decisions.md for the four SHAs) — missing entirely on the
  first pass; see contradiction 2 above for the larger question this sits
  inside.
- **h3's weight is now two entries** (`fl2va`, required, 62 GB; `ref2va`,
  `optional: true`, 134 GB; both `dest: MiniMax-H3`) instead of one 196 GB
  blob. `server/model.go`'s own comment — "FL2VA always, Ref2VA for
  references" — says Ref2VA is genuinely optional; the single-entry version
  would force everyone to download 134 GB they might not need. This does not
  reproduce the README's manual dedup-via-symlink trick (~66 GB instead of
  ~196 GB if both are wanted) — the schema has no field for "these files are
  identical across two weights, hardlink them" — and it surfaces a real
  question about the dedup key logged in decisions.md.
- **ltx's weight gained a `files` allow-list and a second, optional entry**
  for the ~42 GB dev transformer (`ltx-2.5-22b-dev-transformer-bf16`,
  README's own table marks it optional) — the exact path for that file is
  inferred from the sibling distilled-transformer's naming pattern, not
  confirmed by a literal `hf download` line in the README; flagged as
  unverified rather than presented as read from the repo.
- **`peak_ram_gb` added to iris (30), ltx (21), AuK (25)**, each cited to the
  real README's own figure — iris and AuK's READMEs give ranges ("~16-30GB
  per model," "~15-25GB"), and I used the upper bound; ltx's README gives a
  validated default (21 GiB at a short clip, int8 quantize-on-load) with an
  explicit warning that larger renders reach ~76 GiB and swap on the 64 GB
  test machine. My first pass omitted all three rather than transcribe a
  hedge as a number; the review correctly pointed at the schema's own
  description — "an estimate is better than nothing" — as the reason that
  was the wrong call for a field whose whole purpose is switch-dialog
  arithmetic. `requires.ram_gb` for h3 and ltx now reads 64, matching each
  README's own "validated on a 64 GB MacBook Pro" line, rather than the 32
  the first pass copied from the design examples.
- **AuK's `runtime.backends` dropped `cuda`.** (follow-up, reworded) This is
  a choice between two defensible readings, not a correction. The code does
  support cuda (`web/server.py:53`, `_default_device`, and the README's peak-VRAM
  figures come from an NVIDIA A800). One option keeps cuda and widens
  `requires` to linux/amd64. The other keeps darwin/arm64 and matches
  `docs/design/01-prd.md` §13's table (`mps`, `cpu`). I took the table, because
  §14 makes "No Windows or Linux GPU support at launch" a v1 non-goal, and
  because a linux/amd64 `requires` would declare a host nobody has checked
  this studio on. An earlier revision gave the OS gate as the reason, but the
  gate was itself part of the choice. The reasoning and its cost (the
  manifest understates what the code does) are now in `docs/decisions.md`,
  which supersedes the earlier entry's reason.
- **`handoff.send` removed from iris and AuK's `capabilities`.** Added on the
  first pass by loose analogy with h3's set; no equivalent evidence exists
  for either studio, and `docs/design/07-platform-services.md` §5 describes
  the cross-studio "Use in…" action as launcher-initiated on a gallery item,
  which works even for a studio that never calls `POST /handoff` — so there's
  a real chance no studio needs this capability directly. Criterion 9 is
  Required: minimum capabilities and no more.

## api/openapi.yaml corrections from the review

The first pass built this from `docs/design/01-prd.md`'s R-numbered summary
alone; it never read `docs/design/05-sdk-and-custom-studios.md` §3/§7 or
`docs/design/07-platform-services.md` §3-4, which give literal paths and were
the actual source the milestone brief pointed at for the platform API. Fixed:

- `servers.url` now carries `/api/v1` (`07 §3`'s `HELM_API` value), with
  every path relative to it, matching how both documents' own endpoint
  tables write paths without the prefix.
- `/timeline:open` → `/timeline/{id}:open` (05 §3, §7's literal examples).
- `/timeline/{id}:export` → `/timeline/{id}/export` — a slash, not a colon,
  which is what 05 §7 literally writes, even though it's inconsistent with
  the colon-verb convention `:open`/`:append`/`:adopt` use elsewhere in the
  same document. Matched the literal text rather than "fixing" the design's
  own inconsistency.
- `/assets/{id}/derived/{variant}` (invented) → `/assets/{id}/thumb?w=320`
  (07 §3's literal path).
- Added: `GET /me` (05 §3, 07 §3), `POST /jobs` (05 §3 — a studio's own
  work, not just installs), `PATCH /gallery/items/{id}` (07 §3 — also now the
  only place `tags` are mutated; no separate `/tags` resource), `PATCH` and
  `DELETE /kv/{ns}/{key}`, `GET /kv/{ns}?prefix=` (07 §3's full method list
  for kv, of which the first pass had only GET/PUT).
- Removed `/inbox/{id}:consume` — 07 §3 says `GET /inbox` is itself "drained
  by the studio on read"; a separate consume action contradicts that line
  rather than implementing it.
- Added `tags: [studio-api]` / `tags: [launcher]` to every operation.
  (follow-up) **This is a proposal for M4's first review, not settled.** M0
  asked for an outline only, and the split shapes how M4 generates the SDKs.
  The file's header comment and a new "Open, not yet decided" entry in
  `docs/decisions.md` both say so. Supporting design text:
  `docs/design/04-packages.md` §1 scopes `helm-runtime-sdk` to "state,
  records, assets, gallery, jobs, timeline, events," with no install, launch
  or settings. `docs/design/01-prd.md` R51 and
  `docs/design/05-sdk-and-custom-studios.md` §5 generate all three clients
  from this one document, so without a marker every operation reaches every
  client. What the design text does not settle: whether launcher operations
  belong in this document at all, and where `/me`, `/sessions`, `/handoff` and
  `/inbox` go (04 §1 names none of them, and I tagged them `studio-api` on my
  own reading). It also does not settle what "launcher" means, since 05 §7
  keeps framework-only calls like `/timeline/{id}:open` in the SDK, returning
  501 when embedded.
- Added `PATCH /timeline/{id}` (revision-based edit, matching 05 §6's "undo
  is the previous revision"), `POST /assets:reclaim` (02 §8's reclaim query
  as a Job, not a one-at-a-time delete), `POST /studios/{id}/models:select`
  (nothing previously wrote `studio_model_bindings.selected`, so iris's
  `{models.selected}` had no endpoint that could ever set it before
  `:launch`).
- The header comment pointed at "docs/agents/reports/00-contracts.md" for
  resources without a data-model table without this report actually having
  that content — fixed by writing it here instead of promising it and not
  delivering: `certification_level` (on the `Studio` schema) has no
  `installations` column because 05 §9's four levels are evaluated live by
  `helm doctor --studio`, which matches data-model §1's own "derived, not a
  row" category rather than being a real gap; `step_runs` and a standalone
  `tags` resource still have no endpoint of their own (tags are folded into
  `PATCH /gallery/items/{id}` per 07 §3's own text, deliberately, not an
  oversight; step_runs remain unexposed — flagged, not resolved).

## Judgement calls

- **`internal/manifest`'s eighth and ninth rules (unique names, closed
  placeholder set) go beyond the milestone's literal seven.** I added them
  because the review correctly identified them as the same category of gap
  — something a schema-shaped check structurally cannot express — not a new
  policy decision. (follow-up) That undersold them. Each changes what
  `helm validate` accepts, in a way later milestones must match, so each now
  has its own `docs/decisions.md` entry giving its reason and cost. The same
  applies to the `format` assertion.
- (follow-up) **AuK's backends** and **the OpenAPI tag split** are judgement
  calls too. Both are covered above and in `docs/decisions.md`. The first is
  logged as a decision a human may overrule; the second as a proposal for M4.
- (follow-up) **The full manifests in `studios/`** are a placeholder that
  depends on an open decision, not a judgement call about the design. See
  contradiction 2.
- **`{ports.x}` not requiring `depends_on` on the same process, and
  `health: {tcp: false}` passing the health `oneOf`, are both left
  unenforced.** The first is a genuine open question about supervisor
  semantics (logged in decisions.md, not mine to answer from `helm
  validate`). The second is real but narrow — `oneOf`'s `required` checks
  presence, not value, so a `tcp: false` probe (which asserts nothing useful:
  the description says "a successful connect is enough," and `false` isn't
  that) passes — and I judged it not worth a tenth ad hoc rule for something
  this specific; noting it here is the deliberate alternative to quietly
  shipping it unremarked.
- **The symlink check for `cwd` escape is necessarily lexical, not
  filesystem-aware.** `helm validate` runs against a manifest file alone —
  there is no checked-out studio tree to call `EvalSymlinks` against at
  validate time. A checked-out repo with a symlink inside it that points
  outside the studio root would pass `helm validate` and could only be
  caught once there's a real filesystem to check against, which is a
  supervision-time (M2) concern, not this milestone's.
- **`docs/design/08-h3-dry-run.md`'s claim that "long [health] budgets belong
  to studios that load at startup, like ltx and AuK" doesn't match either
  studio's actual code** (ltx spawns a fresh process per render; AuK loads
  lazily on first request, not at startup) — but the *value* it recommends
  (30s is wrong, use a short budget) happens to still be right for both,
  since neither studio's own process blocks on loading a model before
  answering. I used 30s for both. Recording this as a finding against ch08's
  reasoning, not against the actual manifests.

## Where a manifest could not express what a studio does

Unchanged from the first pass, still accurate:

- **h3** has no `/healthz` (tcp probe substituted, per the milestone brief).
  Its `busy: { path: /api/queue }` only reflects an active render — h3's
  interactive mode can hold ~21 GB resident with an empty queue, which the
  manifest has no way to surface, and (per the review) `busy`'s response has
  no documented contract at all — see decisions.md.
- **iris** has no data-root flag at all (`sessions/` lives inside the
  checkout) and no `busy` endpoint of any kind.
- **ltx** has the same missing-data-root gap as iris. Its `--model` accepts
  any HF repo id or local path at runtime through its own UI, not just the
  declared weight.
- **AuK** has no data-root flag either, and — uniquely among the four — no
  way to wire *any* declared weight into its process `cmd` at all: `CKPTS` is
  a hardcoded relative path with no flag or environment variable reading it.

## Could not verify

- **Weight sizes for iris's five checkpoints and AuK's `auk`/`auk_flash`/`qwen`
  entries** — no network call was made to Hugging Face; README hedge ranges
  (iris: "~16-30GB per model," AuK: "~15-25GB") were used for `peak_ram_gb`
  where the schema's own description favors an estimate, but not transcribed
  as `size_gb` on the weights themselves, which I still have no real number
  for.
- **The Linux leg of the gate.** This session ran on macOS only.
- **iris-studio's own `LICENSE`** — no file exists at the repo root (README
  claims MIT; the vendored `iris.c` submodule has an actual file).
- **ltx's dev-transformer file path** — inferred from a naming pattern, not
  confirmed by a literal download command in the README (see above).
- **AuK's actual checkpoint sizes on disk** — the `ckpts/*` symlinks in this
  checkout resolve to 0 bytes here.

## Dependencies added

- **`github.com/santhosh-tekuri/jsonschema/v5` v5.3.1** — see
  `docs/decisions.md`. Also now running with `AssertFormat = true` (see
  above), which is a compiler option this package sets, not a new
  dependency.
- **`gopkg.in/yaml.v3` v3.0.1** — see `docs/decisions.md`. Now also used for
  line-number resolution (`yaml.Node`) and multi-document detection
  (`yaml.Decoder`), beyond the original generic-decode use.

## Files touched outside the milestone's list

None. Every change is under `studios/*.yaml`, `cmd/helm/**`,
`internal/manifest/**`, `api/openapi.yaml`, `go.mod`/`go.sum`, or is an
append to `docs/decisions.md`.

## Decision-log entries appended

See `docs/decisions.md`, sections "Open, not yet decided" (six new entries:
the pointer-vs-full-manifest question, the weights dedup-key question, the
`busy` response contract, the `{ports.x}`/`depends_on` question, the
exactly-one-vs-at-least-one-main contradiction, and the build-step venv
question) and "2026-09-15 · M0 contracts" (new entries: the two new
dependencies' use is unchanged; ref pinning; h3's weight split; h3's build
command fix; AuK's backends fix; capabilities trim — each written at the
point in that file where it belongs, not reproduced here verbatim a second
time).

(follow-up) Appended in the second round:

- "Open, not yet decided": a correction withdrawing "closest schema-valid
  approximation" from the `studios/` entry (the question stays open), and
  the OpenAPI tag split as a proposal for M4.
- "2026-09-15 · M0 contracts": AuK's backends as a choice between two
  readings (supersedes the earlier entry's reason), `format` assertion,
  `unique-names`, `known-placeholder`.

## Left undone

- **The `run:` sugar bug (contradiction 1) and the studios/-pointer question
  (contradiction 2) are both unresolved** — both are `schema/manifest.json`
  or cross-document questions outside this milestone's file list.
  (follow-up) The `run:` branch of `effProcesses` is untested as a result,
  on purpose. See contradiction 1.
- **`make gate`'s `validate` target does not depend on `build`**
  (`Makefile:15`, `validate:` at `Makefile:38`) — it only checks that
  `./bin/helm` exists and is executable, not that it reflects the current
  source. A stale binary from an earlier build would let `make gate` report
  green while validating with old code. `Makefile` is not in this
  milestone's file list; flagging for whoever owns it (`gate.md` attributes
  the gate's growth to M1). Every `make gate` run quoted in this report was
  preceded by a manual `go build -o bin/helm ./cmd/helm` to avoid exactly
  this.
- **`internal/manifest`'s schema-path resolution breaks under `-trimpath` and
  can silently point at the wrong worktree's schema** if a binary built in
  one checkout runs against manifests in another — confirmed by the review,
  documented in `docs/decisions.md`, not fixed (fixing it needs either a
  loader file inside `schema/`, outside this milestone's file list, or
  shipping `helm` as a properly distributed binary, which is later-milestone
  packaging work).
