# Build plan

Frozen 15 September 2026. Ten phases, roughly sixteen weeks of focused solo
work. Ordered by **what could still prove the design wrong**, not by
architectural layer.

| # | Phase | Effort | Gate demo |
|---|---|---|---|
| 0 | Prove the manifest — four manifests, no daemon | 2–3d | All four express without a Go special case |
| 1 | Supervision — walking skeleton | 8–10d | Launch h3, `kill -9` the daemon, restart, studio re-adopted |
| 2 | Install, weights, linked models | 8–10d | Clean machine to generated video; interrupt and resume; link an existing model |
| 3 | OpenAPI contract, SDK, embedded provider | 10–12d | Conformance green against both providers; `helm dev` with no daemon |
| 4 | ltx and AuK — the second caller | 6–8d | Three studios, independent environments, switch dialog with real memory arithmetic |
| 5 | helm-css, real UI, helm-ui-sdk | 10–12d | h3's take list replaced by `<helm-gallery>`; theme toggle re-themes running studios (note, M6 Q20: h3 stays at level 2 with its own take list, per 07 §5 and 08; `<helm-gallery>` is shown in a fixture studio) |
| 6 | iris, manifest library, editor | 6–8d | Wrap a repo that never heard of helmstudio; export the manifest as a pull request |
| 7 | Timeline and export | 10–12d | A 14-second sequence from three studios; stream-copy verified bit-identical (note, M8 Q21: two demos — a run of h3 takes by the fast path, its picture verified bit-identical, and a four-clip sequence from three studios through conform) |
| 8 | Mac app and release | 7–9d | Notarised `.dmg` on a clean Mac; quit with a studio alive in the menu bar |
| 9 | SDK docs and helmstudio.in | 8–10d | A stranger follows the quickstart on a clean Mac and reaches a generated output without asking anything |

## Ordering principles

**Risk first.** The biggest design risk is not the supervisor or the database.
It is whether one schema expresses four genuinely different studios — Go with
Metal C, Python with MLX, Python with PyTorch and FastAPI — without a special
case. That costs two days and no code, so it is phase 0.

**Walking skeleton before depth.** Phase 1 ships a deliberately ugly HTML page.
Nothing is polished until something works end to end, because the gaps between
components are where designs fail.

**One real studio at a time.** h3 carries phases 1–3 alone. The second caller
arrives in phase 4, which is when the interfaces stop being a refactor of one
studio.

**Every phase ends in a demo.** No demo means no definition of done, which
means drift.

**Docs and the site come last, deliberately.** An API still changing gets
documented twice, screenshots of a half-built UI age in days, and a quickstart
is only worth writing once it runs against a shipping binary.

## Phase 9 in detail

**SDK documentation** — a quickstart (nothing to a scaffolded studio recording
its first gallery item, under ten minutes; if it takes longer, fix the tooling,
not the prose); concepts (the manifest, process groups, managed versus linked
weights, capabilities, the two providers); guides (wrap a repo with no
manifest, record with provenance, sessions, timeline hand-off, theming,
isolated development and test); a **generated reference** — API pages from the
OpenAPI document, manifest pages from the JSON Schema, never hand-written,
regenerated in CI; a CLI reference; publishing (the fifteen criteria,
certification levels, what a registry pull request contains); and **the four
launch manifests annotated line by line** — they exist from phase 0 and will be
the most-read page in the documentation.

**helmstudio.in** — a one-sentence thesis naming the problem; the problem made
concrete (the submodule you did not clone, the Python mismatch, the 60 GB with
no resume, two studios both wanting port 8720); the launch studios with runtime
badges so a visitor knows what runs on their hardware; **a real screen
recording of an install and a generation**, which is the highest-value asset on
the site and the reason this phase cannot come earlier; an honest install
section naming platform support rather than implying it; "add your own studio"
pointing at the docs; the repository. No signup, no waitlist, no pricing.

**Both built with `helm-css`.** A design system that works outside the app it
was drawn for is a design system. One that does not is a stylesheet.

## Cross-cutting, from day one

CI — build, tests, `helm validate` on all manifests, generated-client drift,
conformance from phase 3, visual regression from phase 5. **A Linux build from
the first commit**: a case-insensitive Mac filesystem hides path bugs, and it
keeps the eventual port cheap. Studio pull requests in parallel. An append-only
decision log, one line per decision with the reason. **Documentation capture**
— write each phase's demo down as you perform it, and keep the generated
reference wired from phase 3, so that phase 9 is assembly rather than
archaeology.

## Changes needed in the four studios

Independent of the daemon, and each one blocks a phase:

- **h3: a dumb `GET /healthz`** returning 200 unconditionally — *not*
  `/api/config`, which fails when a model directory is missing. **Blocks phase
  1.**
- h3: nothing else structural. `--port`, `--root`, SIGTERM handling and
  `Setpgid` are already correct.
- h3: one adopt-and-record call in `finishTake` (blocks the phase 3 demo); a
  token pass over `style.css` — 45 variables mapped, 64 raw literals cleared
  (blocks phase 5).
- ltx and AuK: a health endpoint and an assigned-port flag where missing; a
  `tiny` test profile (blocks phase 4).
- iris: confirm the assigned port; add health (blocks phase 6).
- All four: add `helmstudio.yaml` at the repository root once phase 0's schema
  settles.

## Week one, by day

1. The manifest JSON Schema and the h3 manifest, then iris.
2. ltx and AuK manifests — note every field you wished existed rather than
   working around it.
3. `helm validate` passing on all four; the OpenAPI outline; open the
   `/healthz` pull request on h3.
4. Repository skeleton, the `platform` package interfaces, the directories
   helper, SQLite pragmas, the migration runner.
5. Schema v1 and store methods; CI green on macOS **and** Linux.

By the end of week one nothing generates anything, which is correct. What
exists is the answer to the only question that could force a redesign, plus the
scaffolding.

## Deferred deliberately

Reverse proxy and iframe embedding, until all four studios take `--base-path` ·
the update flow (v1 is uninstall plus install; weights stay cached) · Windows,
after Linux and after v1, since it needs Job Objects and a shell story ·
registry automation, since with four studios a pull request is the process ·
multi-GPU and VRAM scheduling, which is not a macOS problem · colour grading,
keyframes, titles and effects, possibly never.

## Schedule risk

Phases 3 and 7 carry it. An API you will live with for years deserves more care
than a plan admits, and conform-and-concatenate is always wrong twice before it
is right.

**If something gives, cut scope inside phases 6 and 7 — never compress phase
3.** A rushed contract is paid for every week afterwards. **Phase 9 is the one
place shipping less is genuinely fine:** a quickstart, the generated reference
and the four annotated manifests are worth more than a complete site, and the
rest can follow.

**Decide before starting:** ffmpeg licensing. It gates phase 7 and determines
what can be bundled in a signed `.dmg`. (Closed, M8 Q4: an LGPL build with
videotoolbox.)
