# Handoff — state of the build on 2026-11-19

Written when the work moved from one Claude session to another. Read this, [CLAUDE.md](../CLAUDE.md),
[docs/README.md](README.md) and the plan's [milestones](plan/08-milestones.md) before doing anything.

## What is built

Milestones M0 to M7 are built, verified and committed on branch `main` (local git, no remote). `git log --oneline`
has one or more commits per stage.

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
| M7 | Three packs: LTX-2.5 retake / extend / audio to video; MiniMax H3 Image (torch); MiniMax H3 video through the native `h3` binary (text, first/last frame, image / video / audio references) |

Last full contract suite on the final M7 code: see the last M7 commit message (pack 3 step 2, API 1.22).

## M7 notes

- The owner ranked the packs (2026-11-16) and moved ZONOS3, Qwen3-TTS, FLUX.3 Klein and Wan 2.2 to the
  [backlog](backlog.md) (2026-11-19).
- All M7 weights are on the internal disk under `~/GenAI/models` (the external drive was ejected).
- MiniMax H3 video runs the vendored h3.c (`runner_service/vendor/h3c/`, built by `make h3`) as a child process in
  the **native** runner env (health on 8093). Cancel kills the child's process group. Pack facts and traps:
  `runner_service/packs/minimax_h3/GOTCHAS.md` and `docs/reference-notes/minimax-h3.md`.
- M7 ran lean checks at the owner's request; the robust check pass followed on 2026-11-19 (see the backlog for what
  it covered and what is left).
- Test runs on the GPU stay small and short (owner): at most 512×512, 22 frames, 2 steps, reference media 2–3 s.

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
- **Native runners** on the Mac (`make runners`, health on 8091 torch, 8092 mlx, 8093 native): running and idle.
- **Test stack** (`helmstudio-test`): up; it has two fake hosts and NATS TLS.
- **GCP**: one spot VM `helm-gpu` in us-central1-b, STOPPED, disk kept (Qwen weights, the built CUDA image, that
  host's credentials). It bills for storage only. Rules are in the project memory `gcp-for-cuda.md`: spot only; say
  before starting it; never stop or delete it without the owner's explicit yes each time.
- Old-studio folders (`~/GenAI/ltx-2-studio`, `~/GenAI/helmstudio`, `~/.helmstudio`, …) and
  `~/ORALAB` are read-only references.

## Decisions waiting for the owner

1. **MLX version**: stay on 0.32.4 (LTX output close to the old studio's, not identical) or pin 0.32.2 (bit-exact,
   but the plan needs ≥ 0.32.4 for the vision library). The robust pass ran every LTX golden on both: all pass.
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
8. **The empty Create screen**: decided 2026-11-20, built to the Direct board (title, composer under it, looks grid
   of the open tile's group, Elements aside). Inspector-only settings show when a composer setting is clicked; after the
   first run the screen is as before.
9. **The MiniMax H3 video first + last frame golden** passes at 25 dB (two runs differ by 26-27 dB), the silent
   video one at 27 dB, not 30.

## Known gaps

- M5b: frame jobs share a queue lane with exports; in a Re-render, clips after a failed one are released but do not
  run in the same batch.
- M6: four checklist items unproven on the GPU host (a local model server reserving memory; a returning runner's
  late result being dropped; two smaller ones); a remote host's NATS account caps its connections (8),
  subscriptions (100 each) and message size (1 MB) but not its publish rate (NATS has none), so it can still flood
  its own event subjects, and fill the KV_cancels consumer cap (1000) so other runners' runs fail until an admin
  deletes those consumers; its S3 identity cannot delete or list and writes only under the runner prefixes, but for
  every owner (per-owner keys need per-run credentials).
- M7: the Generate ETA uses the last run's time, not the size and length; MiniMax H3 runs that take an image input
  are not bit-repeatable (two runs differ at 37–41 dB; text to video is exact); a 56-frame Ref2VA run with a sound
  video took 329 s against about 40 s at 22 frames, not looked into; the Create header wraps the memory chip to a
  second line when the session title is long.
- Web: component tests and a few end-to-end tests (`make e2e`) exist, not full coverage; gallery thumbnails are originals; several screens were checked against
  their boards at the browser pane's size, not side by side at 1440×960.
