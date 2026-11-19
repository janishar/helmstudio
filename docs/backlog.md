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

- **Robust check pass: done 2026-11-19** (after M7). Security review across M7, 79 take-isolation contract tests,
  every golden on MLX 0.32.4 and 0.32.2, the MiniMax GPU paths, web component and end-to-end tests (`make e2e` in
  web_service, test stack only), every screen against its board, and the M6 hardening (a NATS account per host with
  connection, subscription, payload and consumer caps; an S3 policy per host with no delete or list). Left open:
  - a host's S3 key can still read every owner's inputs and overwrite outputs: close with per-run credentials or
    presigned URLs from studio;
  - NATS has no publish rate limit, so a host can still flood its own event subjects within the caps; a hostile host
    can fill the 1000-consumer cap on KV_cancels until an admin deletes its consumers;
  - small runner notes: ffmpeg on user files runs with the runner's full environment (NATS and S3 secrets); the h3
    prompt is visible to other local accounts through ps; a media take is held in memory (up to 1 GiB);
  - GPU paths not run: MiniMax H3 Image with 16 references or large references; H3 video references at 1 MP or 15 s;
  - screens left as built until the owner decides: the empty Create screen (Direct board: composer under the title,
    looks grid, Elements aside); the Gallery "This project only" chip and search placeholder; the Pipelines list
    extras (New pipeline / Rename / Delete; four batches with "All batches"); no landing page (Landing board, not in a
    milestone).

## Product

- **Generate ETA from size and length** (M7). The estimate under Generate is the last run's time, so after a long
  run it overstates a small one and the other way round. Scale it by megapixels × frames × steps.
- **MiniMax H3 image-input runs are not bit-repeatable** (M7 pack 3). With the same seed, first/last frame and
  reference runs differ by 37–41 dB (a silent video reference by about 30 dB); text to video repeats exactly. The
  cause is inside h3.c, not found; the goldens use PSNR bars.
- **Create header wraps with a long session title** (M7 pack 3). The memory chip drops to a second line; the board
  shows one row. Let the title and the top label truncate (web_service/components/create/create.tsx).
