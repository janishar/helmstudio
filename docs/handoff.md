# Handoff — state of the build on 2026-11-16

Written when the work moved from one Claude session to another. Read this, [CLAUDE.md](../CLAUDE.md),
[docs/README.md](README.md) and the plan's [milestones](plan/08-milestones.md) before doing anything.

## What is built

Milestones M0 to M6 are built, verified and committed on branch `main` (local git, no remote). `git log --oneline`
has one or more commits per stage. The working tree was clean at handoff.

| Milestone | What it added |
|---|---|
| M0, M1 | Skeleton (Kong, auth, studio, files, web, NATS, Postgres, S3), Qwen Image on torch/MPS, batches on LangGraph |
| M2 | Graph editor and user workflows, saved pipelines with gates and loops, elements with `@` |
| M3 | LoRA library, adapter and bake; looks; the shared visual picker |
| M4 | MLX runner, LTX-2.5 text/image-to-video with keyframes, auto length, beats; hand-over between runners on one GPU |
| M4b | `llm` pack, Judge / Write / Decide / Memory nodes, the director agent with chat, fan-out, model providers, custom nodes |
| M5a | Models screen, download and delete jobs, Admin Hosts (pins, after-run), API keys, `helm import` |
| M5b | Timeline: sequences, editing with revisions, preview, New shot, Replace, Re-render, export jobs |
| M6 | CUDA runner in an NVIDIA container, a second GPU host joining the stack, Qwen Image validated on a GCP spot VM |

Last full contract suite on the final M6 code: 481 passed, 34 skipped. Restart checks: 17 pass, 1 skipped by design.

## What is next: M7, more model packs

The owner ranked the packs on 2026-11-16:

1. **LTX-2.5 retake, extend and audio-to-video** (MLX; weights already under `~/GenAI/models/ltx-2.5`; they run on the
   dev transformer plus the distilled LoRA, so check they fit the 52 GB host budget before generating).
2. **MiniMax H3 Image** (weights on the external drive, 64 GB plus a 17 GB PE folder).
3. **MiniMax H3 video** (weights on the external drive, 196 GB).

Pack 1 has NOT been started: a workflow for it was launched and stopped by the owner within a minute; no files
changed. Its workflow script (scout → contracts → runner, studio, web, tests builders) can be reused or rewritten.
Pack 1's web work should also add a way to remove an audio track in the Timeline (see [backlog.md](backlog.md)).

Weights not under `~/GenAI/models` are on the external drive `/Volumes/ALI T9/models` (read-only; use in place; do
not copy tens of GB to the internal disk without asking; nothing is downloaded without the owner's say).

## How each stage has been built

Per stage: one agent writes the contracts; builders work in parallel per service, coding only against the contracts;
an integrator proves it on the live stacks; two reviewers (security, correctness) report findings; a fixer fixes
them; then the main session runs the gates itself (`make contracts-check`, each service's `make test lint`,
`make test`, `make resilience`), checks screens in the browser against the mock boards in `docs/mock/`, publishes
new boards to the UI artifact, and commits. Ports from the old studios are proven against goldens recorded from the
old studio's own code. Reports of recent stages are in the old session's scratchpad and are not needed to continue.

Lessons that cost time:
- Agents are shown the owner's latest typed message. If it is a question, they decline build work. Get a typed
  go-ahead ("build pack 1") before launching a build workflow.
- Long single agents leave the task list stale. Use short workflows (build / integrate / review+fix) and keep the
  pinned task list detailed; include a text checklist in status replies.
- Tell fixers to run gates in the foreground and to end with their report; a fixer once ended mid-run without one.
- `make -n` on targets that call `$(MAKE)` executes them. Never use it.
- The full contract suite takes about 14 minutes; run it in the background or in parts.

## Machine state at handoff

- **Dev stack** (compose project `helmstudio`, Kong on 127.0.0.1:8300): up and healthy, with `STACK_TLS=on` in
  `deploy/.env` (needed for a remote GPU host). Use `http://127.0.0.1:8300`, not `localhost` (separate sessions).
  Test admin: the fake-mode admin in `deploy/compose/fake.yml`.
- **Native runners** on the Mac (`make runners`, health on 8091 torch and 8092 mlx): running and idle.
- **Test stack** (`helmstudio-test`): up; it has two fake hosts and NATS TLS.
- **GCP**: one spot VM `helm-gpu` in us-central1-b, STOPPED, disk kept (Qwen weights, the built CUDA image, that
  host's credentials). It bills for storage only. Rules are in the project memory `gcp-for-cuda.md`: spot only; say
  before starting it; never stop or delete it without the owner's explicit yes each time.
- Old-studio folders (`~/GenAI/ltx-2-studio`, `~/GenAI/helmstudio`, `~/.helmstudio`, …) and
  `~/ORALAB` are read-only references.

## Decisions waiting for the owner

1. **MLX version**: stay on 0.32.4 (LTX output close to the old studio's, not identical) or pin 0.32.2 (bit-exact,
   but the plan needs ≥ 0.32.4 for the vision library).
2. **Default judge model**: Qwen Image's 8B text encoder (no extra load; it passed everything in the demonstration)
   or the 27B `llm` model (sensible verdicts; more model swaps).
3. **LoRA strength slider**: −4…4 as built, or 0…2 as the Create board draws.
4. **LTX first-frame quirk**: the old studio gives no keyframe marker for an image at frame 0; copied to stay
   bit-exact.
5. **Mock differences left as built** (Graph, Pipelines review, video Create, Timeline): listed in the stage reports;
   the rule is that the mock leads, so each is either built to the board or the board is changed with the owner's
   say.
6. **Import of the old gallery**: `helm import ~/.helmstudio --user <email>` is ready; a dry run found 90 items.
7. **The GCP disk**: keep (small storage bill) or delete.

## Known gaps

- M5b: frame jobs share a queue lane with exports; in a Re-render, clips after a failed one are released but do not
  run in the same batch.
- M6: four checklist items unproven on the GPU host (a local model server reserving memory; a returning runner's
  late result being dropped; two smaller ones); a remote host can flood its own NATS subjects (no per-host quota);
  its S3 identity can get, put and delete any object in the bucket but not list.
- Web: no component or end-to-end tests; gallery thumbnails are originals; several screens were checked against
  their boards at the browser pane's size, not side by side at 1440×960.
