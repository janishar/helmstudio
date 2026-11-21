# Backlog — things to consider later

Notes from the owner while using the app. Not scheduled; each is picked up when its area is next worked on.

## Model packs

- **ZONOS3 and Qwen3-TTS voices**: became M8 "Voices" (owner, 2026-11-21); see [plan 15](plan/15-voices.md).
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
  - screens left as built until the owner decides: the Gallery "This project only" chip and search placeholder; the
    Pipelines list extras (New pipeline / Rename / Delete; four batches with "All batches"); no landing page (Landing
    board, not in a milestone).

## Product

- **Live preview clip while a video runs** (owner, 2026-11-20: backlog): 8 frames from the middle, refreshed each
  step, with sound and a scrubber, as the TextToVideo board draws. Needs the runner to send preview clips (contract and
  runner change) and costs a little GPU time per step.

- **Generate ETA from size and length** (M7). The estimate under Generate is the last run's time, so after a long
  run it overstates a small one and the other way round. Scale it by megapixels × frames × steps.
- **MiniMax H3 image-input runs are not bit-repeatable** (M7 pack 3). With the same seed, first/last frame and
  reference runs differ by 37–41 dB (a silent video reference by about 30 dB); text to video repeats exactly. The
  cause is inside h3.c, not found; the goldens use PSNR bars.
- **Create header wraps with a long session title** (M7 pack 3). The memory chip drops to a second line; the board
  shows one row. Let the title and the top label truncate (web_service/components/create/create.tsx).
- **Enter a remote provider's API key in Admin › Model providers** (owner, 2026-11-21: "add to the todo"). Today the
  form takes only a key file name, and the admin puts the key in `deploy/secrets/providers/` and restarts pipelines.
  Proposed: a write-only API key field (after saving it shows only "key set" and the last 4 characters, never the
  key, not even to an admin; a Replace key button), the key stored encrypted in the pipelines database under a master
  key file in `deploy/secrets/`, never logged; the key-file option stays. Needs the owner's yes on relaxing the
  secrets-are-files rule (keys would then also be in database backups), a write-only field in the contract, the
  board updated first, and tests that the key never comes back through the API or logs. About half a day to a day.
