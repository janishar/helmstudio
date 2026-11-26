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

## Deployment

- **Production deployment (plan 26)** (owner, 2026-11-23: "add in tasks"). Production on this Mac is built
  ([plan/26-production.md](plan/26-production.md): `make prod`, no fakes); what is left is the server deployment. Plan it
  with the owner's answers first (who uses it: just the owner, a team or the internet; where it runs: this Mac, a home
  server or a cloud VM, with the Mac runners and GCP as GPU hosts). Shape: the base compose without `fake.yml` (a
  check that no fake service or echo workflow is offered), fresh production secrets from `.tools/make-secrets`, a
  domain and HTTPS in front of Kong (NATS and S3 private or tunnelled), the admin account, releases by tag with
  migrate, health checks and rollback, logs. The owner's `oralab-deploy` skill covers a one-VM compose beta.
- **Windows support** (owner, 2026-11-25: "Windows support add to future features"). Today: Apple Silicon Macs and
  Linux NVIDIA hosts only; the Mac app is macOS only. Likely path: the stack in Docker on WSL2 (the Makefiles and
  shell scripts run inside WSL2), and a Windows PC with an NVIDIA card as a GPU runner inside WSL2 (CUDA in WSL), set
  up like a Linux NVIDIA host. Needs: amd64 images (see Multi-arch images below), a test run on a real Windows
  machine, fixes for what breaks (paths, line endings, host.docker.internal, ports), and a short Windows setup page
  in the docs. A native Windows app is a later step, if ever.

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

## Plugins (later kinds)

- **Library packs** (owner, 2026-11-25: "i like Library packs only for now add in future features"): installable
  content plugins: starter voices, looks (film, camera, colour, lighting presets), element presets, e.g. a
  "Film noir looks" pack. A new plugin kind after v1 (model providers and Assistant tools, alpha.2 item 33). Other
  kinds discussed and not taken for now: model packs, a dev and test pack (Echo and the fakes), pipeline nodes,
  provider presets, UI plugins.
- **Effects packs** (owner, 2026-11-27: "note future feature: effects pack (plugins) on a video generated/uploaded,
  effects like depth map, background change, cloth change, character swap, etc"): installable effects that act on a
  video take, made or uploaded: a depth map, background change, clothing change, character swap and more. Each effect
  makes a new take (the source stays), offered from the take's Use in and right-click menu, in Create, the Gallery,
  the Timeline and Magic (effects are already badges on Magic blocks, plan 37 decision 6). Not scheduled; needs its
  own plan, models and board.

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
- **Exact Timeline frames for agents** (owner, 2026-11-23: backlog; plan 24 open point 8). `POST …/timelines/{id}/frame`
  and the MCP `timeline_frame` answer a video clip's nearest stored frame (its poster or last frame), not the frame at
  that time. Exact: the media job `frame` with `at` widened to `first | last | index`, the runner's media worker
  (ffmpeg, no GPU), idempotent per (take, index), its image a frame take; about 5 h (plan 24 §28.5).
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

## Skills (decided 2026-11-25; now alpha.2 item 36)

A plugin kind `skill`: text that teaches the Assistant a craft (an animator, a cinematographer, a voice director)
and how to do it in helmstudio. `plugins/<name>/plugin.json` (kind skill, `"format": 1`, title, when to use) plus
`SKILL.md`, optional extra files read on demand and recipes as data (checked against the install's settings). The
prompt holds one line per skill that is on; the full guide is read into the chat when an ask fits or the user types
@<skill> (a "Using skill" line). Text only: actions come from Assistant tools plugins with their cards.
Added without an app update (owner: "i would want to add a skill into the plugin without upgrading the app"): drop a
folder or a .zip into `~/.helm/plugins/` (owner: "use ~/.helm as the outside dir"); the app picks it up on start,
Rescan or by watching the folder, off until turned on. Built-in plugins (shipped with the app) and the user's both
show on the Plugins page; the user's copy of the same name wins ("replaces built-in"). An older app refuses a newer
`format` with "needs a newer helmstudio". Later: upload a zip on the Plugins page for servers without folder access,
skills in pipeline agent nodes.

## Multi-arch images (noted 2026-11-25)

The published images (ghcr.io/janishar/helmstudio-*) are linux/arm64 only (.tools/push-images): right for the Mac app's
VM and Apple Silicon. x86 (amd64) Linux servers, most NVIDIA machines, build from source with `make up` today. Before a
Linux-focused release: build both platforms into one multi-arch image per service (buildx `--platform
linux/arm64,linux/amd64`), including the media-tools image, and test a clean amd64 install.

## alpha.3: the subscription bridge in the Mac app (owner, 2026-11-25)

Owner: ship the Claude Code bridge in the public app ("its not signed app plus enthusiatic project with local use
only"; "we are using their built in claude code"), in 0.1.0-alpha.3. Stay inside Anthropic's "Can customers offer
Claude Code in their products?" conditions (code.claude.com/docs/en/legal-and-compliance): the unmodified binary, each
user installs and signs in to Claude Code themselves through Anthropic's own flow, helmstudio never collects, stores or
intermediates credentials and never pays for or resells usage. Build: rename the plugin (no "Claude Code" or
"Anthropic" in its name, e.g. "Your Claude subscription"; plain text may say it runs Claude Code), off by default and
only-you, keep the disclaimer, `run` on host mac (starts itself; Couldn't start · "install Claude Code and sign in"
when missing), bundle it in the .dmg, check the app VM reaches it, docs page. The owner confirmed
Anthropic's Commercial Terms are accepted (2026-11-25; the page says products running Claude Code fall under them).

