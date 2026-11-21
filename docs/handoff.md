# Handoff — state of the build on 2026-11-19

Written when the work moved from one Claude session to another. Read this, [CLAUDE.md](../CLAUDE.md),
[docs/README.md](README.md) and the plan's [milestones](plan/08-milestones.md) before doing anything.

## What is built

Milestones M0 to M8 are built, verified and committed on branch `main` (local git, no remote). `git log --oneline`
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
| M8 | Voices: Qwen3-TTS and ZONOS3 packs, saved voices, dialogue, audio takes on the Timeline ([plan](plan/15-voices.md)) |

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
- The owner went to sleep on 2026-11-21 saying to carry on and make the needed decisions. Decisions taken
  while building, for the owner to review (each: the choice, then why):
  - API split: 1.30 (step 2: audio takes, the voice param, saved voices), 1.31 (step 4: cloning, Save as
    voice), 1.32 (step 5: scripts, dialogue, Timeline audio), not one 1.30. Why: each step passes its own gates,
    as M7 bumped the API per pack.
  - Memory plans plan a speech stage at its `max_seconds`. Why: the default prompt is empty, so planning on it
    would plan 0 s and under-reserve every real run.
  - The size variable is `speech_seconds`, not the plan's `seconds`. Why: ltx/extend already has a `seconds`
    param, and the two collided in `peak_gb` (found in review).
  - A workflow's media with mixed outputs: video, else image, else audio. Why: one deterministic rule in Go,
    Python and TS (the Go map order made it random).
  - Elements (not only looks) do not fit audio workflows. Why: an `@` in spoken text would be read aloud;
    dialogue scripts (1.32) are the way characters speak.
  - ZONOS3 is built offline with its text normaliser trimmed to the library crate. Why: the full workspace
    wants pyo3 and other crates from crates.io; no downloads.
  - ZONOS3 runs `zonos3-server` as the run's child, not `zonos3-cli`. Why: only the server normalises text and
    speaks several lines per load (scout, reference-notes/voices.md part 4).
  - Step 3: `GraphNode.only` also narrows a `voice` param (a subset of its `accepts`), within API 1.30. Why: voice
    fit was per stage, so the voice-over template offered designed voices the runner then refused; `only` is
    the existing per-template narrowing.
  - Step 3: Design a voice's Hear it saves the description as a designed voice ("Untitled voice" without a name)
    and runs the design template with it; Cancel or Back deletes a voice made in the dialog and not saved, and
    cancels its run. Why: a voice param needs a saved voice; no contract change.
  - Step 3: a designed voice is only its description; Qwen's designs drift between lines, so the copy says so
    instead of pinning a seed. Making one exact is ZONOS3's job (save it with its clip, step 4).
  - Step 3: the speech tokenizer loads in bf16 (the reference casts it; byte-exact goldens), not fp32.
  - Step 3: long text is cut into sentence pieces of at most 200 characters, joined with 0.25 s of silence, each
    seeded with the run's seed; over 900 characters is refused.
  - Step 3: Generate is disabled and reads "Pick a voice" while the voice is empty.
  - Step 3, left as built: presets sorted by name (the contract's order, the board shows pack order); no
    "24 kHz" chip before a take exists.
  - Step 4: a cloned voice keeps its clip take instead of a copy: deleting the take hides it everywhere, but the
    sweep keeps its object while one of the owner's voices uses it. Why: the same guarantee as the plan's copy
    (a voice never breaks) with no copy job or pending state.
  - Step 4: Delivery is drawn as the boards' 0..1 sliders, mapped in the pack to ZONOS3 doses (emotion and energy
    −4..4, whisper ×1.5, speaking rate 15 × v bytes/s (step 5: always sent, 1 = the middle bucket), guidance = speaker cfg, default 2); presets Neutral, Warm, Tense,
    Whisper carry the boards' values.
  - Step 4: `zonos3-server` runs with two slots (`--batch 2`) because speaker cfg needs a second slot; seeds go
    as the run's seed mod 2^32 (the server takes 32-bit seeds).
  - Step 4: the vendored ZONOS3 source is about 20 MB (servers/ggml, ggml with only the CPU, Metal and BLAS
    backends, the trimmed textnorm crate, licences); `make zonos3` builds one static `zonos3-server` offline.
  - Step 4: the 27 preset previews are cut to 4 s mono 24 kHz in the pack (about 5 MB) instead of the full
    clips (30 MB).
  - Step 4: Clone from a clip's Hear it saves the voice first (consent ticked) and runs the ZONOS3 voice-over on
    a sample line, as Design a voice does; Save voice needs one try first.
  - Step 4: the clone golden's clip is cut from the Qwen3-TTS Uncle Fu preview (the Qwen goldens are under 3 s).
  - Step 4: files serves the starter voice previews (`packs/<family>/voices/<id>-<16 hex>.wav`) as it serves look
    thumbnails; it refused them (403), so no preset preview played since step 2. ZONOS3's `zh` reads "Mandarin"
    (its board), Qwen3-TTS's stays "Chinese"; the Voice section leads the inspector (the boards).
  - Step 5 (API 1.32): a script expands at create time into the node's `lines`; the runner gets the lines and an
    empty script, `made_with.typed` keeps it. A blank line adds one more pause (0.35 s each); the last line has
    none. Why: one checker (studio), and reuse shows the script as typed.
  - Step 5: dialogue workflow ids are `qwen3-tts-dialogue` and `zonos3-dialogue` (the `<family>-<name>` pattern;
    the numbered names are file names). Qwen dialogue takes presets only (19.4).
  - Step 5: "Use in…" offers into a rows param with one media field by the take's own kind only, so an audio
    take offers MiniMax H3 References; a video take of 2 s or more now offers it too. Why: a video with sound
    would otherwise match two inputs and be offered nowhere.
  - Step 5: Make shot lists the Audio to video templates of every family and fills an exact slot on V1 at the
    audio clip's time and length (capped 13.4 s). A cancelled New line removes its placeholder.
  - Step 5: audio in/out stay on the sequence's sample grid (48 kHz), not the take's own 44.1 kHz grid that the
    ZONOS3 Timeline board's note mentions. Why: one grid for every audio track, as the Timeline rules have it.
  - Step 5 integration: an audio clip's out clamps to its take's end on the sample grid in check.py too (Go and
    TS already did; a fixture added); ZONOS3 always sends the speaking-rate condition (1 = 15 bytes/s, the
    checkpoint's middle bucket of 30; the preset and clone goldens re-recorded from the reference build);
    `cmd.media.job` inputs may be kind `audio` (a WAV, checked by the runner, mixed by studio's filter graph);
    `take_ids[<i>]` counts from 0 in the README as in the code since 1.17. Why: each was a mismatch between the
    reference, the services or the README.
  - Step 5 web: a nested Modal's close no longer closes its parent (the voice picker closed the element editor
    unsaved); the Create strip's buttons had the shown take's key twice (duplicate keys piled up "Add to
    timeline"); the composer's kHz chip and Output show only a take of the chosen family; an audio clip's
    inspector leads with Model and Sound (TimelineAudioZonos3 board).
  - Step 5 suite fixes: a write node of form script may call `library.read` (its cast) though it declares no
    tools (studio refused it, so every script write failed); the fake ask answers `ECHO-RETRY:` on a pipelines
    re-ask too (README, the fake: a contract text change, since a valid answer failing a node's check could not
    be scripted "bad once, then good"); two new tests fixed (a `library.read` text is a name prefix; B's voice
    is deleted so test_elements' count of B's items holds).
  - Step 5 fixer pass: a fill's past-end cut never lengthens a clip already cut at the next one; a rows media
    offer is dropped when the part offered (a Timeline range) breaks the field's min_s / max_s; script_check makes
    expand_script's line checks (nothing to say, 600 / 200 characters) so the write is re-asked; the script cast
    pages through `library.read` (new arg `offset`, a contract change) for every element with a voice, not 20
    characters; Make shot's slot starts at the selection's sound (`at + start − in`); a `place` longer than its gap
    moves the audio tracks on with V1; a cue's text is the trimmed text spoken. Web: New line opens in Voice-over
    and shows "≈ time · N GB"; Use in… has the boards' header and family names (the audio note under it is not
    drawn). Track names ("A2 · Dialogue") are not built: the API has no track name, only its position.
  - The contract suite signs in again when a session's access token has expired (ACCESS_TTL 15 min; the suite
    takes about 15), so its last files no longer fail on 401.
  - The step 5 browser check used the owner's dev sequence "M5b UI check": a ZONOS3 dialogue take on A2 and a New
    line (Qwen, "The road was closed.") were added there; Undo in the Timeline, or removing the two A2 clips,
    takes them out. It also made elements Mara, Jon, Ada and Bo (dev library).
  - Keep-awake for the overnight run: denied once by the app's auto-mode check, then granted when the owner asked
    (until the session is idle for about 5 minutes).

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
