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
- **LTX studio features not built now** (owner, 2026-11-22; plan 19 §23.11): Deblur and Refine details
  (control-video IC-LoRAs; Deblur already on the Mac), SDR to HDR (HLG master, EXR, preview; needs an API version),
  the diffusion decode for Detail, spatial upscalings 2, temporal upscalings (48 fps) and distilled extend's anchors.
- **MiniMax H3 options not picked** (owner, 2026-11-22, from the h3c-studio parity audit; the picked ones are being
  designed): live preview while rendering (h3 `--show --preview-mode estimate`, all-frames preview, saved previews to
  scrub; Mac only, about 2-4 days); keeping H3 loaded between runs (h3 REPL or a warm SGLang; against today's unload
  rule); more aspects (4:3, 3:4, 2:3, 21:9), Match input and the first-frame aspect warning (every family); memory
  options (SSD streaming, zero-copy off for external drives, Qwen prefetch); `--ref-image-size match|max`; prompt
  scaffold and history, a per-phase timing view, browser notifications when a run ends.
- **Video uploads from the browser** (owner: later, 2026-11-22; plan 20 §24.5): upload a video, converted in the
  browser to H.264 MP4 (≤ 1920 px, ≤ 60 fps, ≤ 2 GB, ≤ 10 min) with Mediabunny + WebCodecs, a poster, `video/mp4` in
  `UploadCreate`. Today video only enters through runs, imports and `helm import`.
- **LTX on NVIDIA from Create** (owner: backlog, 2026-11-22). The NVIDIA path works (VM renders 2026-11-22:
  distilled-fp8 t2v 91 s, dev-int8 a2v 216 s; docs/mock/checks/m9/ltx/), but the LTX workflows are `env: mlx`, the
  torch runner publishes none of them, and studio only sends a run to a host with the workflow's env, so Run on
  NVIDIA cannot run LTX from Create. Fix: a workflow runs on whichever env a host serves its pack in (env picked per
  host by the family's runtime), with the MLX-only nodes (ltx/lora-stack) spliced out on torch, as the registry's
  memory plan already does (e4a32e8). Contract change first (Several hosts eligibility, Workflow.env). The VM check
  used two owner-data copies set to env torch.
- **MiniMax H3 Turbo 4-step LoRA** (owner: backlog, 2026-11-22; the 8-step v1.2 is the one checked for a build: plan 20 24.11).
  lightx2v/Minimax-h3-Turbo `minimax_h3_fl2v_turbo_4step_v1.3_768p_bf16` (and `ref2v_turbo_4step`): shift 6 (h3.c
  needs an override), alpha 8/128 from metadata, separate FL2V and Ref2V files, regression reports on v1.3. Mac spike
  at 864x480: denoise 31 s vs 45 s for the 6-step draft, same total (the CPU merge adds ~14 s), a little softer.
  Spike notes: route B (h3.c merges at load with stochastic rounding; SGLang `--lora-path`), merged files are a no-go.
- **LTX Enhance prompt** (owner: "Not now", 2026-11-22). LTX has no enhance step. On NVIDIA the vendored ltx-core
  has a Gemma 4 enhancer for 2.5 (`enhance_t2v` / `enhance_i2v`, gemma4 system prompts), no new download; check the
  DmitryDB int8 text encoder can generate. On the Mac the ltx-2-mlx port enhances only 2.3 (Gemma 3).

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
- **Flaky contract test** `tests/test_queue.py::test_round_robin_between_users` (seen 2026-11-21 and -22, passes on
  rerun): a queued run's `position` can go back up by one (…5, 4, 5) while runs are released, so the count races with
  release or retry. Unrelated to the work it failed under; fix the position count, then drop this line.

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
- **Evaluate cloud vendors such as fal and Magnific** (owner, 2026-11-21: "add to todo only for evaluation not
  build"). Not to build yet: weigh whether and how. The shape considered: a "cloud" runner env (no GPU, a container in
  the stack) whose packs call the vendor's API instead of loading weights, so a vendor model is one more model in
  Create, pipelines, batches, the Timeline and the assistant, and its result a normal take. Per vendor a small adapter
  (submit, poll, fetch) mapping its models onto the canonical params, so a vendor can be swapped without UI changes.
  To evaluate: money per run (cost recorded on the take, daily spend caps, the price on confirm cards, an idempotency
  key so a retry never pays twice); egress (references and LoRAs uploaded to the vendor, shown before a run, under
  `REMOTE_MODELS` and a per-vendor switch; keys as files in `deploy/secrets/vendors/`); results downloaded at once
  (vendor links expire); seeds that may not repeat, queue waits, content-policy refusals, each vendor's terms.
  Magnific fits as an Upscale take action and a pipeline step; fal as a short curated list of models. Rough size if
  built: 2–4 days for the runner and the first vendor, about a day per further vendor or model family.
