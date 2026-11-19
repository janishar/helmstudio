# Backlog — things to consider later

Notes from the owner while using the app. Not scheduled; each is picked up when its area is next worked on.

## Model packs

- **ZONOS3 sound design: dialog and voices for the studio** (asked 2026-11-18). A pack for speech: lines of dialog
  in chosen voices (preset voices, a cloned voice from a reference clip, voice edits), landing in the Gallery as audio
  takes and on Timeline audio tracks under a shot. Weights already on the host: `~/GenAI/models/ZONOS3-Mini/gguf`
  (9.2 GB of GGUF: backbone, speaker encoder, codec encoder/decoder, steer, `edit/`, `voices/`); no model card in the
  folder, so the runtime (llama.cpp-style GGUF, which env, MPS or CPU) is the first thing to scout. Builds on M7
  pack 1's audio takes, audio uploads and media inputs (contracts 1.19). Not in a milestone (owner, 2026-11-19); if
  taken up, after the three M7 packs. To decide when picking it up: whether dialog is written per clip in the Timeline, per
  line in Create, or by the director agent; lip-sync to video (e.g. through LTX audio-to-video) in or out of scope;
  how it shares voice nodes and screens with the Qwen3-TTS entry below.
- **Voice generation with the Qwen3-TTS models** (asked 2026-11-19; not in a milestone). Text to speech as audio
  takes for dialog and narration, usable on Timeline audio tracks and as audio-to-video input. Weights on the Mac:
  `~/GenAI/models/Qwen3-TTS-12Hz-1.7B-CustomVoice` (preset and cloned voices) and
  `~/GenAI/models/Qwen3-TTS-12Hz-1.7B-VoiceDesign` (a voice described in words), 4.2 GB each with their speech
  tokenizer; each folder has a README to scout first. Small enough to sit beside other models in the host budget.
  Builds on M7 pack 1's audio takes and media inputs. Overlaps the ZONOS3 entry above: decide when picking either up
  whether they are two voice families behind the same voice nodes and screens.
- **FLUX.3 Klein** (moved out of M7 by the owner, 2026-11-19). An image family on torch; plan §13 risk 6 notes it
  needs torch 2.15 and diffusers git-main, which moves with the torch env's pins (docs/plan/14-dependencies.md).
  Alternative source: the old iris studio (`~/GenAI/iris-studio`) runs FLUX.2 Klein, FLUX.3 Image Klein and Z-Image
  natively through the `iris` binary (iris.c, Metal); if M7 pack 3 adds a runner path for a native binary (h3.c),
  iris can reuse it.
- **Wan 2.2 on MLX** (moved out of M7 by the owner, 2026-11-19). A second video family in the mlx env.

## Checks and hardening

- **A more robust check pass, later** (owner, 2026-11-19). To save time, the M7 packs run lean checks from pack 1's
  review on: one combined correctness reviewer (no separate security reviewer), no contract critics, isolation tests
  only on new endpoints, the full contract suite once per pack at commit (touched test files in between), resilience
  only when messaging or the dispatcher changes, the GPU proof on one case per pipeline, the browser check on new
  screens only. To pick up afterwards:
  - a security review across M7 (media inputs, audio uploads, take uses, clip actions) and isolation tests on every
    path that takes a take (run, batch row, saved workflow, pipeline bind, use, clip action);
  - the M6 hardening left open: a per-host NATS quota (a remote host can flood its own subjects) and an S3 identity
    per host scoped to its own keys (today it can get, put and delete any object);
  - the full contract suite twice and make resilience on the final M7 code;
  - every golden case of each pack on both MLX versions, plus the earlier packs' regression cases;
  - MiniMax H3 Image paths not run on the GPU in pack 2: LCM, Think on, an edit with the enhancer on, several
    references, more than 4 MP and the 20 MP refusal, and the `MINIMAX=1` contract tests. The enhancer's per-reference
    memory term (`11 + images` GB) is an estimate: measure one enhanced edit with 8–16 references;
  - MiniMax H3 video paths not run on the GPU in pack 3: a run that mixes a sound video with an audio row (checks
    the `<Audio n>` order), several references of one kind and the 3-video / 15 s caps, peak_gb with references above
    512×512, and parts right at the 56-frame edge from a 25 or 30 fps take. One 56-frame run with a sound video took
    329 s against about 40 s at 22 frames: measure its peak and find why;
  - web: component and end-to-end tests, and every screen side by side with its board at 1440×960.

## Product

- **Generate ETA from size and length** (M7). The estimate under Generate is the last run's time, so after a long
  run it overstates a small one and the other way round. Scale it by megapixels × frames × steps.
- **MiniMax H3 image-input runs are not bit-repeatable** (M7 pack 3). With the same seed, first/last frame and
  reference runs differ by 37–41 dB (a silent video reference by about 30 dB); text to video repeats exactly. The
  cause is inside h3.c, not found; the goldens use PSNR bars.
- **Create header wraps with a long session title** (M7 pack 3). The memory chip drops to a second line; the board
  shows one row. Let the title and the top label truncate (web_service/components/create/create.tsx).
