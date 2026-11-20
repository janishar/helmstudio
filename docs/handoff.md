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
| M8 (in progress) | Voices: Qwen3-TTS and ZONOS3 packs, saved voices, dialogue, audio takes on the Timeline ([plan](plan/15-voices.md)) |

Last full contract suite on the final M7 code: see the last M7 commit message (pack 3 step 2, API 1.22).

## M8 notes

- The owner said "build it" on 2026-11-21 after reviewing [15-voices.md](plan/15-voices.md) and the voice boards
  (CreateAudio, VoicePicker, TimelineAudio and their ZONOS3 boards). The plan's recommended answers (19.9) are
  decided; it is built in the five steps of 19.7, one commit or more per step.
- ZONOS3 weights: `~/GenAI/models/ZONOS3-Mini/gguf` (the whole 9.2 GB folder, copied from the SSD; the owner keeps
  it). Weights Apache-2.0, not gated. The 27 default voices have no stated licence or consent; the owner decided
  (2026-11-21, Q11) that ZONOS3 includes them as starter presets.
- Qwen3-TTS: CustomVoice and VoiceDesign on disk; the Base 1.7B (cloning) stays undownloaded.
- GPU test runs for voices: at most 4 s of audio, clips 3 s.

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
- **GCP**: no VM or disk (helm-gpu was deleted 2026-11-19). Rules are in the project memory `gcp-for-cuda.md`: spot
  only; say before starting one; never stop or delete one without the owner's explicit yes each time.
- Old-studio folders (`~/GenAI/ltx-2-studio`, `~/GenAI/helmstudio`, `~/.helmstudio`, …) and
  `~/ORALAB` are read-only references.

## Owner decisions (answered 2026-11-20)

1. **MLX version**: stay on 0.32.4 (0.32.2 is bit-exact for LTX, but the vision library needs 0.32.4 or newer).
2. **Default judge model**: Qwen Image's 8B text encoder.
3. **LoRA strength slider**: 0 to 2, as the Create board draws (to build).
4. **LTX first-frame quirk**: fix it (add the keyframe marker at frame 0) and remake the LTX goldens (to build).
5. **Mock differences** (Graph, Pipelines review, video Create, Timeline, the Gallery "This project only" chip, the
   Pipelines list extras): build each to its board (to build).
6. **Import of the old gallery**: not now; `helm import ~/.helmstudio --user <email>` stays ready (90 items).
7. **The GCP VM and disk**: gone (deleted 2026-11-19; the project lists no VM or disk). A new spot VM may be created
   when a task needs NVIDIA.
8. **The empty Create screen**: built to the Direct board (12caab1).
9. **The MiniMax H3 video goldens**: 25 dB (first + last frame) and 27 dB (silent video) accepted.
10. **Next**: the screens to their boards (with 3 and 4), then the Landing page. First pass committed as c09031f.

### Second round (2026-11-20, after c09031f), to build

- Q1 capitals: none anywhere (as built now).
- Q2 pipeline Studio view: build now; steps are ordinary runs, picked take passed to the next step; no API change.
  Studio|Graph toggle wired.
- Q3 memory controls in that aside: admins only; others read-only.
- Q4 title menu (editor + execution): Rename + All pipelines; drop back arrow and always-editable title.
- Q5 judge panel: canvas wires set takes/pass/memory; Backspace removes node; title edited on the card; emptying a check
  removes it; drop those panel controls.
- Q6 judge tiles: On retry = retry target node (rerun step or a Write node that rewrites the prompt); Then Ask me = park
  for pick.
- Q7 Test on takes: build now (contract, studio, web).
- Q8 judge lines in execution: one line per decision, expand on click to inner checkpoints (retry still possible).
- Q9 wording: 'attempt 1' everywhere.
- Q10 execution layout: saved positions; auto layout only when overlapping/off-screen.
- Q11 Pipelines list New pipeline button: keep.
- Q12 Rename/Delete: move into a ⋯ menu next to the title; panel shows Run, Open graph, Duplicate.
- Q13 'Open collection': build a batch filter for takes (contract first); Gallery shows a clearable 'Batch · <title>'
  chip.
- Q14 'This project only': the session last opened in Create (remembered in the browser).
- Q15 Gallery search: widen q to element handles and model/family names (contract first); placeholder 'Search prompts,
  elements, models'.
- Q16 Elements tile empty word: 'Add' everywhere.
- Q17 LTX 'Recipe · One-stage/Two-stage' tile: build (skip upscale + stage 2).
- Q18 optional help text on settings sections: add (contract change); packs write it; web shows (i).
- Q19 presets: keep pack order (ordered list in the contract).
- Q20 Generate shows planned peak memory: add an API field; button shows '≈ time · N GB'.
- Q21 Extend: combined 'CFG · STG' tile.
- Q22 Audio to video composer: Audio + 'Frames · No keyframes' tiles; picking a frame turns keyframes on, clearing all
  turns them off; Elements via @ button.
- Q23 Compare (A/B, seed grid) and Explain run drawer: build both.
- Q24 live preview clip while running: backlog.
- Q25 Timeline shot names: fixed shot number given when a clip is added, stays when moved: 'Shot 4 · birches' (clip
  field, API change).
- Q26 clip Mute: keep, in the Gain tile.
- Q27 New shot extra seeds: as built (extra takes in the session).
- Q28 Qwen 'Enhance (bypassed)' on the Graph board: board-only, not built.
- Q29 Graph inspector '… uses the <component>': add (contract change).
- Q30 'Encode prompt (guided)' -> 'Encode (guided)'.
- Q31 Regenerate audio pressed look: keep.
- Q32 Landing: signed-in users at /welcome go to Create; phone layout as built.

Built 2026-11-21 (API 1.29.0), all but Q24 (backlog) and Q28 (board-only). Left open, for the owner to say:
- Component words chosen by the builder: 'whole model' (H3 generate), 'language model' (llm ask), 'prompt enhancer'
  (H3 Image enhance), 'vae encoder' / 'audio encoder' (LTX encode-video / encode-audio).
- LTX One-stage decodes stage 1's half-size latent (clip is half the canvas each way); a full-size one-stage clip
  needs a new sample role and goldens. The 'Stage 2 steps' tile still shows in One-stage.
- Done batches show only 'Open collection' (no link to the run view); the execution title menu keeps a third item,
  'The batch · <title>'.
- Only the Judge panel lost its title field and Remove button; other node panels keep both.
- Pipeline Studio view: step form edits live in memory only; write/decide/memory/agent binds stay plain fields.
- Shot numbers are highest + 1 (a removed top number is reused); migration 00023 numbered dev sequences by position.

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
- Q23 / Q17 memory per stage: the Explain run drawer shows the whole run's planned peak, not the planned memory per
  stage that plan 05-ui.md 8.2 asks for, and LTX One-stage's Generate estimate reads "up to N GB" (the two-stage
  figure as a ceiling). The registry has no measured figure per stage; either measure one per stage and add it to
  the contract (variant memory per stage, RunPlan.stages[] {node, peak_gb}), or the owner accepts the total. Open.
- Web: component tests and a few end-to-end tests (`make e2e`) exist, not full coverage; gallery thumbnails are originals; several screens were checked against
  their boards at the browser pane's size, not side by side at 1440×960.
