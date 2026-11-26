# helmstudio plan — Create: Music and Sound effects

[← Plan index](../README.md)

Status: **approved, building (owner, 2026-11-27: "approved, build it")**, release list
[0.1.0-alpha.6](../release/0.1.0-alpha.6.md) item 6. The board is approved; this file is how it is built. Section §44
(§45 is plan 39, Magic, which waits for this plan's contract phase and packs).

## 44. Create: Music and Sound effects

### 44.1 Boards

The approved board is the spec: [CreateMusicSound](../mock/CreateMusicSound.dc.html). The look is the helmstudio Design
System (CLAUDE.md), the chips, popups and settings drawer of [TextToVideo](../mock/TextToVideo.dc.html), and the shared
menu of [ContextMenus](../mock/ContextMenus.dc.html).

| Frame | What | Built in |
|---|---|---|
| 1 | Music with ACE-Step 1.5: kind tabs, composer (Style, Lyrics with section tags, Instrumental), chips, stage with section markers, run steps Plan / Compose / Decode / Saving, strip with Repaint · Cover · Extend, drawer (Song, Reference, Advanced, Output) | P4 |
| 2a–2f | Model menu; MiniMax picked (unsupported chips greyed in place); Length with Auto; BPM · Key · Time group; Vocal language; Reference mini-player | P4 |
| 3a–3d | Repaint range on the stage; Cover popup (Keep the song); Extend popup (Add, End / Start); the take's right-click menu | P4 |
| 4, 4a, 4b | Sound with Stable Audio 3: 4 variations as a grid, Keep / Keep all, Avoid, From a sound, Match a video; model menu; a variation's menu (Vary in place of Cover) | P4 |
| 5a–5d | MMAudio from a video: pick a video; two takes from one run (Sound, Video with sound); the sound take's menu (Save with the video); the video take's menu (Sound only) | P4 |
| 6a–6c | Gallery: Voice / Music / Sound filters, a music card, a sound card, a video with its sound; panel rows Song and Lyrics; menus | P5 |
| 7a | Card looks: music as an album cover, sound and voice as an orb (sound sky, voice amber), in the Gallery, pickers and strip; Timeline clips keep the waveform | P5 |

### 44.2 Sources

- Owner's picks: [plan 37](37-magic-concept.md) decision 14 (ACE-Step 1.5 2B turbo as the default and 1.5 XL turbo,
  MiniMax Music 3, Stable Audio 3 Small-SFX, MMAudio large 44k v2; the latest PyTorch pin, never a model repo's older
  pin).
- Measured runs on the owner's M5 Pro 64 GB: `~/GenAI/model-samples/<model>/results.md` and `run_samples.py` (the
  exact working code paths), venv overrides in `~/GenAI/model-samples/.venvs/*-overrides.txt`, upstream clones in
  `~/GenAI/model-samples/src/`. Packs port from these scripts, not from the upstream READMEs.
- Weights (never written to): `~/GenAI/models/ACE-Step-1.5`, `ACE-Step-1.5-XL-Turbo` (link to the T9 drive),
  `MiniMax-Music3` (link; diffusers layout only), `Stable-Audio-3-Small-SFX`, `MMAudio`, plus MMAudio's helpers
  `DFN5B-CLIP-ViT-H-14-384` and `bigvgan_v2_44khz_128band_512x`.
- The pack contract: `runner_service/packs/README.md`. Precedents: LTX's Retake and Extend workflows
  (`packs/ltx/workflows/3-ltx-retake.json`, `4-ltx-extend.json`), qwen3_tts's vendored modules, qwen_image's `enhance`
  stage (`reports` texts), MiniMax H3's optional `audios` rows param, the `frame` media job.

### 44.3 Corrections from the scout

1. **No new take kind.** The owner's tabs read Image, Video, Voice, Music, Sound, but `TakeKind` stays
   `image | video | audio`. Splitting it would ripple through about 24 web files (Timeline audio tracks, voices,
   mentions, pickers), media params `accepts: [audio]`, media jobs, uploads, the CLI and `check.py`. Music, Sound and
   Voice are a **sound class** on audio takes and workflows (44.6).
2. **The Audio value is mono only today** (`core/nodes.py` `_wav` refuses anything but 16-bit PCM mono). ACE-Step
   (48 kHz), Stable Audio 3 and MiniMax Music 3 (44.1 kHz) are stereo: Audio gains `channels` 1|2.
3. **MMAudio's video workflow computes as "video"** (Video wins in `workflowMedia`) because it has an Audio and a Video
   output. The sound class must win, or it never shows under the Sound tab.
4. **Offers match on `accepts` only**, so a voice take would be offered "Cover · ACE-Step". Offers become class-aware.
5. **`span` works only on a video source** (contracts/README.md Media inputs; studio `api/studio/media.go`; the
   gallery's `offerInputs` returns early for non-video takes). Repaint and Extend need it on audio.
6. **MMAudio's weights are not safetensors** (`.pth`, `.bin`, `.pt`); the torch loader and `core/pack.py` sizes are
   safetensors only. A small core change (a torch pack `load_component` hook, as `loaders_mlx` has, and a size for
   other files) beats converting 6 GB of weights.
7. **ACE-Step caps transformers < 4.58**; the runner is on 5.23. Vendor its modules (as qwen3_tts did) and run them on
   5.23. It also "syncs" its modeling `.py` files into the checkpoint folder: the vendored copy never writes there.
8. **The board's memory figures are off for two models**: MiniMax Music 3 measured 26.6 GB peak (board 20 GB), MMAudio
   8 GB text only and 15.1 GB with a video (board 12 GB). The board's figures follow the measured `peak_gb` (fixed in P4
   with the UI; the drawer readout comes from the registry, so it cannot drift again).
9. **ACE-Step forces fp32 on MPS.** The pack sets dtype itself: XL in bf16 measured 24 GB peak against 35.6 GB, and
   twice as fast in diffusion. The 2B stays fp32 (23 GB, measured), XL runs bf16 on the Mac (owner's brief).
10. **The planner LM is not reproducible by seed upstream** (the same seed gave a different key). The pack seeds the LM
    sampling from the run's seed, or the node cache and the goldens break.
11. **Section markers come from ACE-Step's lyric timestamps** (`core/generation/handler/lyric_timestamp.py`, the
    "LRC" pass), not from the model output itself: only ACE-Step takes with tagged lyrics show them.
12. **No new runner env.** All four packs run in the shared torch venv (one more env costs a NATS user, secrets,
    Makefile targets, health ports and `desktop/stack.json`). The `sgl.py` child-venv pattern is the fallback only.
13. **The design system's local copy** `docs/design-system/` named in CLAUDE.md is not in the repo yet; P5 updates the
    Design System artifact and adds the copy.

### 44.4 Facts (measured, M5 Pro 64 GB, torch 2.15, MPS)

| Model | Output | Precision | Peak footprint | Speed | Notes |
|---|---|---|---|---|---|
| ACE-Step 1.5 2B turbo | 48 kHz stereo | DiT fp32, VAE fp16, LM fp32 | 23 GB (25 brief) | 30 s in ≈ 16 s; LM 11.6 s of it, diffusion 1.6 s (8 steps), decode 2.1 s | load 20 s warm, 27 s cold; DCW needs setuptools 80 (pkg_resources) |
| ACE-Step 1.5 XL turbo | 48 kHz stereo | DiT bf16 (cast from fp32 on disk), LM fp32 | 22.5 GB (24 brief) | 30 s in ≈ 16.5 s | fp32 was 32–35.6 GB; load 45 s from the T9 |
| MiniMax Music 3 | 44.1 kHz stereo | all bf16 | 26.6 GB (28.5 brief) | ≈ 4.9× real time (30 s in 148 s) | text to music only; same seed repeats bit for bit; instrumental by prompt plus `[instrumental]` lyrics; may stop early |
| Stable Audio 3 Small-SFX | 44.1 kHz stereo | fp32 | 4.4 GB | 6 s in 0.5 s | same seed repeats; HF gated; levels not normalised |
| MMAudio large 44k v2 | 44.1 kHz mono | bf16 | 8 GB text, 15.1 GB with a video | 8 s in 3.8 s (text), 8.7 s (video) | load peak 14 GB (fp32 read on CPU); 25 fps sync, so trim the source 0.2 s long |

Durations up to 600 s are already allowed. Licences (declared, not a filter): ACE-Step MIT; MiniMax Music 3 Community
License; Stable Audio 3 Stability AI Community plus Gemma terms (`gated`, `must_accept`); MMAudio weights CC-BY-NC 4.0;
DFN5B Apple research licence; BigVGAN MIT. Revisions come from each folder's `.cache/huggingface/download/*.metadata`
(ACE-Step-1.5 19671f40…, XL d4a0b288…, MiniMax-Music3 1b525ccb…, SA3 05ca3857…, MMAudio eb13a1a9…, DFN5B 01b771ed…,
BigVGAN 95a9d1dc…); repo ids are confirmed from the same sidecars in P2.

### 44.5 Scope

In:
- Create kinds Music and Sound beside Image, Video, Voice (today's Audio shown as Voice).
- Music: ACE-Step 1.5 (2B, XL) text and lyrics to music with the planner ("Let it rewrite"), Repaint, Cover, Extend;
  MiniMax Music 3 text and lyrics to music (its Repaint, Cover and Extend are ACE-Step's, offered on any music take).
- Sound: Stable Audio 3 text to sound, Repaint, Vary, Extend, from a sound; MMAudio from text, and from a video giving
  two takes (the sound, and the video with that sound); Save with the video on an older sound take (a `mux` job).
- Stereo Audio values end to end.
- Gallery filters Voice / Music / Sound, cards (cover, orb), panel rows, menus, pickers; the Design System update.
- Every new actionable thing (strip tile, variation card, stage take, gallery card) with its right-click menu.

Not in (backlog by name):
- Magic's Music and Sound lanes (plan 39 phase H, after P2).
- MLX paths for ACE-Step (the repo has them; skipped as in the samples).
- Stem separation, a lyrics writer by an LLM, mastering or loudness normalisation (levels stay as the model gives them).
- CUDA runs: the packs declare `devices: [mps, cuda]`, but the CUDA goldens wait for a GCP slot.
- MiniMax Music 3's own repaint or cover (the model has none).

### 44.6 Design

#### Contract (API 1.77.0; contracts first, `make contracts-check`, then `make gen` per service)

- **Sound class.** `SoundClass: music | sound | voice`.
  - Registry: pack-level `model.yaml` key `sound`, published as `models[].sound` (`registry.schema.json`). qwen3-tts,
    zonos3 and echo's voice stages are `voice`; ace-step and minimax-music3 `music`; stable-audio-3 and mmaudio `sound`.
  - `Workflow.sound` (openapi), computed by studio (`graph.go`) and `check.py` from the family of the node feeding the
    Audio output, like `media`. A workflow with an Audio output whose source has a class computes `media: audio`
    (the class wins over a second Video output; only MMAudio's video workflow has both today).
  - `GET /v1/studio/workflows?media=` also accepts `music | sound | voice` (matching `sound`).
  - `Take.sound` for audio takes; the projector sets it from the run's workflow. `GET /v1/takes?kind=` also accepts
    `music | sound | voice` (kind audio and that class).
  - Upload: optional `sound` (default applied: the Create tab or Gallery filter it comes from; unset is voice).
  - The tool catalog's gallery kind enum (`contracts/tools/catalog.json`) gains the three values.
  - The voice rules re-scope to `sound = voice`: `check.py` (spoken-text expansion, mention refusal), studio
    `mentions.go`, pipelines `assistant.py` (`voice = media == "audio"`).
- **Stereo.** Audio value `channels` (1|2, default 1); `evt.run.output` `audio.channels`; `AudioInfo.channels`.
  contracts/README.md Voices drops "mono". Voice clone and LTX audio-to-video downmix a stereo source.
- **Sections.** `AudioInfo.sections: [{label, start_s, end_s}]`, beside `cues`; from the run output (ACE-Step's lyric
  timestamps); absent otherwise.
- **Span on audio.** `span` may sit on a media param that accepts `audio` (snaps to ms). README Media inputs,
  `check.py`, studio `api/studio/media.go`, and gallery `offerInputs` fill every span with the whole audio take.
- **Offers by class.** A template whose workflow has a `sound` is offered only on takes of that class (Repaint · ACE-Step
  only on music takes; Vary · Stable Audio only on sound takes). Reference params (`accepts: [audio]` in a rows param)
  still take any class. `take_uses` in `check.py` and `api/gallery/media.go`.
- **Auto values.** int and float params gain `auto: true`: the value 0 means Auto (the chip and slider show "Auto" and a
  switch). Enums use a value `auto`. BPM int, Key enum, Time enum, Length float seconds. No change to `resolved.params`.
  The planner's picks come back as `reports` texts (`resolved.texts`, the existing field: the rewritten style and a
  line "90 BPM · A minor · 4/4 · 2:10 · en"); the drawer's Plan block shows them.
- **Chip groups and count.** Registry `studio.quick` gains a group tile (`{group: "BPM · key", params: [bpm, key,
  time]}`), `studio.count {default, max}` (Sound 4 / 8; today's fixed 4 elsewhere), and a text param hint
  `tags: [verse, chorus, bridge, intro, outro]` for the lyrics tag chips. A quick chip whose param the picked workflow
  lacks shows greyed in place with "Not on <model>".
- **Mux job.** `cmd.media.job` kind `mux` (inputs a video take and an audio take; output a new video take with both as
  parents). `POST /v1/studio/takes/{take_id}/mux` with `{video_take_id}` (the frame endpoint's shape): 202 with the
  job; 422 when the take is not audio or the other not video; 404 for another user's take; idempotent per pair.
  "Sound only" needs nothing new: the sibling take of the same `run_id`.

#### Data

- `studio_service/migrations/00055_take_sound.sql` (next free number at build time; plan 39 also adds migrations):
  `ALTER TABLE takes ADD COLUMN sound text CHECK (sound IN ('music','sound','voice'))`; backfill every existing audio
  take to `voice`; an index on `(owner_id, kind, sound)` for the filter.
- `channels` and `sections` live in the existing take media JSON (no column).

#### Services

- **runner_service.** Four packs in the torch env, each `model.yaml`, `torch.py`, `workflows/*.json`, `assistant.md`,
  `licences/`, `GOTCHAS.md`, `vendor/` with `VENDORED.md` and sha256s:
  - `packs/ace_step/`: family `ace-step`; variants `turbo-2b` (default, fp32) and `xl-turbo` (bf16, `shares: [lm,
    text_encoder, vae]` from ACE-Step-1.5; `files` the 4 shards). Components transformer, lm (Qwen3 1.7B), text_encoder
    (Qwen3-Embedding-0.6B), vae. Stages `plan` (LM: metas/caption then 5 Hz codes; `reports`; seeded; bypass = "Let it
    rewrite" off), `compose` (DiT, phases n/8; repaint, cover and extend by params), `decode` (VAE; lyric timestamps
    give sections). Workflows `ace-step-music`, `ace-step-repaint` ("Repaint"), `ace-step-cover` ("Cover", `strength`
    = Keep the song), `ace-step-extend` ("Extend", seconds, End / Start; a repaint past the end). Repaint, Cover and
    Extend have no plan node (upstream DIRECT_CONDITIONING_TASKS). The LM stays resident (75% of the time).
  - `packs/minimax_music3/`: family `minimax-music3`; components language_model, rvq_depth_decoder, condition_encoder,
    transformer (stored fp32, cast bf16), vocoder; uses diffusers 0.43's `modular_pipelines/minimax_music3` (check it
    matches PR commit dafe3733; vendor that module if not). Stages `lm` (phases by frame) and `flow`, `vocode`.
    Instrumental by the prompt rule and `[instrumental]` lyrics. One workflow `minimax-music3`.
  - `packs/stable_audio_3/`: family `stable-audio-3`; `model.safetensors` split into dit and autoencoder by rename
    prefixes, plus t5gemma; fp32; vendored `stable_audio_3`; `gated: true`, `must_accept`. Workflows `sa3-sound`,
    `sa3-repaint` (inpaint, mask = span), `sa3-vary` ("Vary", init audio, Change = noise strength), `sa3-extend`
    (continuation); From a sound = a rows param of one media, min 0, max 1.
  - `packs/mmaudio/`: family `mmaudio`; components net (`.pth`), ext autoencoder, synchformer, clip (`dir`, DFN5B),
    bigvgan (`dir`); sources with `{repo, dir}` for the helpers; bf16; vendored networks, flow_matching, ext,
    synchformer, features_utils (not gradio, hydra, tensorboard). Workflows `mmaudio-sound` (text) and
    `mmaudio-video` (a video in; two outputs: Audio, and Video muxed in-stage with ffmpeg: H.264 copied, AAC added, cut
    to the length; trims the source 0.2 s long for the 25 fps sync).
  - Core: `core/nodes.py` stereo `_wav`; `core/loaders_torch.py` a pack `load_component(component, model, ctx)` hook
    and `.pth/.bin/.pt` reads; `core/pack.py` size for non-safetensors (file bytes at the target dtype) and the `sound`
    key; `core/graph.py` span and `media_fit` for audio; `core/media.py` and the media worker: `mux`.
  - `packs/echo/`: fake music and sound stages (stereo), an Audio+Video stage, audio repaint and extend, the planner's
    reports, sections; so the contract suite runs on the fake runner.
  - `pyproject.toml` / `uv.lock`: vector-quantize-pytorch, pytorch-wavelets, setuptools 80 (pinned below 81 for
    `pkg_resources`; DCW silently turns off otherwise), soundfile, einops-exts, open_clip_torch; ruff excludes the
    vendor folders. torch 2.15 stays the pin. `docs/plan/14-dependencies.md` owns the versions.
- **studio_service.** `graph.go` / `workflows.go` (sound, media filter), `projector.go` (sound, channels, sections),
  `api/gallery/service.go` (kind filter), `upload.go` (sound), `api/gallery/media.go` (offers by class, span on audio),
  `api/studio/media.go` (audio span source), `mentions.go` (voice by class), the mux endpoint and job (beside the frame
  job), `registry/registry.go` (models sound).
- **pipelines_service.** `assistant.py` and `assistant_knowledge.md`: voice means `sound = voice`; the assistant can
  make music and sound through the same run request.
- **cli.** `mcp.go`, `takes.go`: show the sound class; the `kind` filter values.

#### UI (web_service), per board frame

- **Frame 1 (Music).** `lib/media.ts` mediaKinds and sides (Voice, Music, Sound on the sound side), `workflowFor` per
  class; `create.tsx` kind tabs; `prompt.tsx` lyrics tag chips (insert at the caret, from `tags`); Instrumental hides
  Lyrics (`when`) and greys Language; `audio.tsx` WavePlayer section markers (click seeks); `runs.ts` Saving step for
  audio; `form.tsx` drawer Plan block (from `resolved.texts`) and the Output / Memory / Speed block (registry facts);
  take badge with stereo; strip buttons Repaint · Cover · Extend from the offers' `action`.
- **Frame 2.** Model chip lists the variants and the other templates of the class; greyed-in-place chips; the Length
  chip with Auto; the BPM · Key · Time group chip; the language enum chip; the reference mini-player (max_s "First 30 s
  used").
- **Frame 3.** `source.tsx` SourceStage and RangeBar on the stage waveform (a section click takes its range; readout
  "0:48 – 1:05 · 17 s"; Cancel / Repaint with the estimate); Cover and Extend as popups anchored to the strip button
  (the offer's form, prefilled); the take menu adds Repaint…, Cover…, Extend…, Download.
- **Frame 4 (Sound).** Per-workflow count (`studio.count`); the variations grid (four WavePlayer-lite cards, each
  plays; a click keeps one; Keep all); the Avoid text area; Match a video switches to MMAudio's video template; the
  variation menu with Vary….
- **Frame 5 (MMAudio).** The video tile and the Video-filtered picker; Duration "From the video" (seconds auto from the
  source); stage tabs Sound / Video with sound over the run's two outputs; strip pairs them ("▶ with sound"); sound
  take menu Save with the video (the mux endpoint); video take menu Sound only (sibling by `run_id`).
- **Frame 6 (Gallery).** KINDS Voice / Music / Sound, `lib/takes.ts` filter and `fits`, the gallery assistant's KIND
  map; card badge with class and length; panel rows Song and Lyrics (disclosure); menus as frames 3d, 4b, 5c, 5d.
- **Frame 7 (looks).** `media.tsx` Thumb: album cover (gradient from a hash of the prompt, in the client) and orb
  (sound sky, voice amber), shared by the Gallery, pickers and strip; Timeline clips unchanged.
- Untouched by the class approach: `components/timeline/*`, voices, dialogue, refs, mentions, `lib/timeline*`.

### 44.7 Phases

Each phase ships alone; gates per CLAUDE.md on every phase, the full `make test` once at the end (P6). Hours are size
and risk, not a promise.

| # | Phase | Needs | Parallel | Hours | Tests and gates |
|---|---|---|---|---|---|
| P1 | **Contract and take classes**: 44.6 contract and data; echo fake stages; studio sound/filters/offers/span/mux; pipelines and CLI voice re-scope; `make gen` everywhere; web gen types only | nothing | first; plan 39 C rebases on it | 10 | `make contracts-check`; runner, studio, pipelines, cli `make test lint`; new `contract_tests/tests/test_music_sound.py` plus `test_takes.py`, `test_voices.py`, `test_uploads.py`, `test_workflows.py`, `test_video_source.py`; studio DB tests on `.tools/test-db` |
| P2a | **ACE-Step pack** (2B + XL as variants; XL bf16 on the Mac), vendored on transformers 5.23; core stereo `_wav` | P1 | with P2b–d | 14 | `test_packs.py`, `test_ace_step.py` (CPU), `-m gpu` golden (`record_ace_step.py`), `make test-torch` |
| P2b | **MiniMax Music 3 pack** (diffusers 0.43 modular pipeline) | P1 | yes | 6 | as P2a for `minimax_music3` |
| P2c | **Stable Audio 3 pack** (vendored, split safetensors, gated) | P1 | yes | 6 | as P2a for `stable_audio_3` |
| P2d | **MMAudio pack with its helpers**; core torch `load_component` hook and non-safetensors size; in-stage mux | P1 | yes (core edit owned by P2d only) | 10 | as P2a for `mmaudio`; core loader tests |
| P3 | **Workflows and actions**: generate, Repaint, Cover, Extend, Vary, video → sound and the video-with-sound take; the `mux` media worker; offers on real packs; registry fixtures and guides | P2a–d | after P2 | 8 | `make contracts-check`; `test_guides.py`, `test_registry.py`; contract tests for offers by class, audio span, mux 202/422/404 |
| P4 | **Create UI** (frames 1–5): kinds, chips and popups, drawer blocks, stage markers and range, strip actions, variations grid, MMAudio two takes, menus; board memory figures fixed to `peak_gb` | P1 (fake runner); real packs for the last check | can start with P2 on echo | 16 | web `make test lint` (`audio-create`, `create-popups`, `model-chip`, `run-strip`, `create-use-in`, `context-menus-*`, `video-create` suites); e2e journeys (44.8) |
| P5 | **Gallery and other surfaces** (frames 6–7): filters, cards, panel, menus, pickers, assistant maps; the Design System artifact and `docs/design-system/` copy (cover, orb, group chip, lyrics tags, variation card, greyed chip) | P1 | with P4 | 8 | `gallery-filters`, `gallery-use-in`, `thumbs`, `assistant-gallery`, `media-rows` suites; e2e gallery journey |
| P6 | **Review, full suite, browser check**: one combined review then a fix commit; the full `make test`; `-m gpu` goldens on the M5; a browser walk of frames 1–7 against the board on the dev stack (announced first); docs (packs/README.md, contracts/README.md, reference note, CHANGELOG, NOTICE, release row) | P3–P5 | last | 6 | the full `make test` once; goldens; browser check |

Order: P1 → P2a–d in parallel (four builders, or two: ACE-Step and MiniMax / SA3 and MMAudio) → P3; P4 and P5 run
beside P2–P3 on the echo fakes. Plan 39 phase C rebases on P1; its phase H waits for P3. About 84 hours in all.

Files per phase:
- **P1**: `contracts/openapi.yaml`, `contracts/registry.schema.json`, `contracts/messages/evt.run.output.schema.json`,
  `contracts/messages/cmd.media.job.schema.json`, `contracts/messages/defs.schema.json`, `contracts/tools/catalog.json`,
  `contracts/README.md`, `contracts/check.py`, `contracts/fixtures/registry/echo.*.valid.json`,
  `contracts/fixtures/video/{uses,sources}.json`, HTTP fixtures for Take, Upload, Workflow;
  `runner_service/packs/echo/` (model.yaml, fake.py, workflows), `runner_service/packs/{qwen3_tts,zonos3}/model.yaml`
  (`sound: voice`), `runner_service/core/{pack,graph,nodes}.py`, `runner_service/gen/`;
  `studio_service/migrations/00055_take_sound.sql`, `studio_service/api/gallery/{service,upload,media}.go`,
  `studio_service/api/studio/{projector,graph,workflows,media,mentions}.go`, the mux route and job beside the frame
  job, `studio_service/registry/registry.go`, `studio_service/gen/`; `pipelines_service/assistant.py`,
  `assistant_knowledge.md`; `cli/mcp.go`, `cli/takes.go`, `cli/gen`; `web_service/gen/api.ts`;
  `contract_tests/tests/test_music_sound.py` and the touched tests.
- **P2a**: `runner_service/packs/ace_step/` (model.yaml, torch.py, vendor/, assistant.md, licences/, GOTCHAS.md),
  `contracts/fixtures/registry/ace-step.{nodes,workflows,models}.valid.json`, `contracts/check.py` CANONICAL,
  `runner_service/tests/{test_packs,test_ace_step,test_ace_step_golden}.py`, `tests/golden/record_ace_step.py`,
  `tests/golden/ace_step/`, `runner_service/pyproject.toml`, `uv.lock`.
- **P2b**: the same set for `packs/minimax_music3/` and `minimax-music3.*` fixtures.
- **P2c**: the same set for `packs/stable_audio_3/` and `stable-audio-3.*` fixtures.
- **P2d**: the same set for `packs/mmaudio/` and `mmaudio.*` fixtures, plus `runner_service/core/loaders_torch.py`,
  `core/pack.py`, `core/media.py`.
- **P3**: `runner_service/packs/*/workflows/*.json` of the four packs, `runner_service/messaging/` media worker,
  `runner_service/core/media.py` (`mux`), `contracts/fixtures/registry/guides.valid.json`,
  `runner_service/tests/{test_guides,test_registry}.py`, `studio_service` mux projector, contract tests.
- **P4**: `web_service/lib/{media,workflow,runs,source,video}.ts`,
  `web_service/components/create/{create,form,prompt,audio,source,workflows,memory-chip,assistant}.tsx|ts`,
  `web_service/styles/*.css`, `web_service/tests/**`, `web_service/e2e/create-gallery-journeys.spec.ts`,
  `docs/mock/CreateMusicSound.dc.html` (memory figures only).
- **P5**: `web_service/lib/takes.ts`, `web_service/components/gallery/{gallery,picker}.tsx`,
  `components/gallery/assistant.ts`, `web_service/components/media.tsx`, `web_service/styles/*.css`, tests, the Design
  System artifact and `docs/design-system/`.
- **P6**: fix files from the review; `runner_service/packs/README.md`, `contracts/README.md`,
  `docs/reference-notes/music-sound.md`, `docs/plan/14-dependencies.md`, `CHANGELOG.md`, `NOTICE`,
  `docs/release/0.1.0-alpha.6.md` row 6.

### 44.8 Tests

- **Goldens per new model path**, fixed seed, smallest size, `-m gpu` on the M5 against the sample scripts: ACE-Step 2B
  and XL text to music (10 s, plan on, seeded LM: the same seed twice gives the same file), Repaint, Cover, Extend;
  MiniMax Music 3 (10 s; bit-exact repeat as measured); SA3 sound, repaint, vary, extend (2 s); MMAudio text (2 s) and
  from a 2 s video (sound and muxed video). Metrics: RMS and peak within tolerance, length, channels, rate.
- **CPU tests per pack**: stub folders and tiny tensors; registry values against the canonical fixtures
  (`test_packs.py`); the loader hook on a tiny `.pth`; stereo `_wav` round trip.
- **Contract tests** (fake runner, `helmstudio-test`): the takes `kind` filter by class; Workflow `sound` and
  `?media=music`; uploads with `sound`; offers by class (no Cover on a voice take); span on an audio take; a run giving
  two takes (sound and video with sound); mux 202, 422, idempotent; sections and channels on the take; backfill to
  voice.
- **Isolation for new endpoints**: another user's take in mux is 404; the filter never shows another user's takes.
- **e2e journeys** (fake mode): make music, repaint a range, extend it; make 4 sounds, keep one, vary it; sound from a
  video, open Video with sound, Sound only back; Gallery filter Music shows the cover card.
- **Browser check** of frames 1–7 against the board (P6), screenshots sent to the owner.
- Gates per CLAUDE.md; the full `make test` once at the end.

### 44.9 Risks

1. **ACE-Step on transformers 5.23** (the vendored port, never the repo's pin). Fallback: a native child in its own
   venv (`sgl.py` pattern), cold-loading 20–56 s every run.
2. **MMAudio's `.pth` weights** against the safetensors-only loader: the hook must keep the meta-device streaming for
   the safetensors packs unchanged.
3. **Stereo Audio** across voice clone, the Timeline and LTX audio-to-video: downmix where mono is needed; contract
   tests cover each.
4. **Classing** of Sound and MMAudio-video workflows for the tabs and offers: a wrong class hides a model or offers the
   wrong action.
5. **Memory**: MiniMax Music 3 (26.6 GB) and XL beside another resident model can pass the 52 GB budget; the
   `peak_gb` values and the host memory plan unload first.
6. **MiniMax speed** (5× real time): a 2-minute song takes 10 minutes; the estimate on Generate says so.
7. **Contract clash with plan 39**: both edit `openapi.yaml` and migrations; P1 lands first and plan 39 rebases.

### 44.10 Open points (defaults applied; the owner may change them)

1. Kind tabs Image, Video, Voice, Music, Sound with today's Audio shown as Voice. **Applied** (board default).
2. Gallery's Audio filter splits into Music, Sound, Voice. **Applied**.
3. Sound takes use Vary where music uses Cover; both use Repaint and Extend. **Applied**.
4. A video's sound picked as audio shows as a sound orb. **Applied**.
5. A sound class on `audio`, not new take kinds. **Applied** (44.3.1).
6. Unkept variations: they stay ordinary takes in the Gallery (nothing deleted); the strip shows the kept ones, kept
   ids held per session in the browser until the next Generate. **Applied**. Cost: a kept mark is not shared across
   devices; a take field later if wanted.
7. The three music models on one model chip: ACE-Step 2B and XL as variants of one pack, MiniMax Music 3 as another
   template of the class in the same menu. **Applied**.
8. Save with the video as a `mux` media job behind its own endpoint (not a re-run: the node cache is not durable).
   **Applied**.
9. The album-cover gradient computed in the client from a hash of the prompt (no contract field). **Applied**.
10. Uploads of audio default to the class of the tab or filter they come from; unset is voice. **Applied**.
11. Count per workflow from the registry (Sound 4, max 8; others unchanged at 4). **Applied**.
12. Section markers only on ACE-Step takes with tagged lyrics (lyric timestamps); none on MiniMax or instrumental takes.
    **Applied**.
