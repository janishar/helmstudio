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
7. **No vendoring for three of the four models.** The runner's locked diffusers 0.43.0 (`uv.lock`) already holds
   `AceStepPipeline` (`pipelines/ace_step`: text2music, repaint, cover, extract, lego, complete; `src_audio`,
   `reference_audio`, `repainting_start/end`, `audio_cover_strength`, `audio_codes`), MiniMax Music 3
   (`modular_pipelines/minimax_music3`, its transformer, vocoder and RVQ depth decoder) and
   `StableAudio3Pipeline` / `StableAudio3AudioToAudioPipeline` / `StableAudio3InpaintPipeline`
   (`pipelines/stable_audio_3`). They run on transformers 5.23 and torch 2.15, so ACE-Step's own cap
   (transformers < 4.58) never applies and nothing writes into the checkpoint folders. The packs build these classes on
   meta, as qwen_image and minimax_h3_image do. ACE-Step's and Stable Audio's files on disk are the original layout:
   `rename()` maps the keys, ported from `~/GenAI/model-samples/src/diffusers-minimax/scripts/`
   (`convert_ace_step_to_diffusers.py`; SA3 checked against the StableAudio3 classes, since `convert_stable_audio.py`
   may cover SA1 only). Only MMAudio is vendored (not in diffusers), from `~/GenAI/model-samples/src/MMAudio` at
   974010a.
8. **The board's memory figures are off for two models**: MiniMax Music 3 measured 26.6 GB peak (board 20 GB), MMAudio
   8 GB text only and 15.1 GB with a video, 16.2 GB with the load (board 12 GB; item 27). The board's figures follow
   the measured `peak_gb` (fixed in P4 with the UI; the drawer readout comes from the registry, so it cannot drift
   again).
9. **ACE-Step forces fp32 on MPS.** The pack sets dtype itself: XL in bf16 measured 24 GB peak against 35.6 GB, and
   twice as fast in diffusion. The 2B stays fp32 (23 GB, measured), XL runs bf16 on the Mac (owner's brief).
10. **The planner LM is not reproducible by seed upstream** (the same seed gave a different key). The pack seeds the LM
    sampling from the run's seed, or the node cache and the goldens break.
11. **Section markers come from ACE-Step's lyric timestamps** (`core/generation/handler/lyric_timestamp.py`, the
    "LRC" pass), not from the model output itself: only ACE-Step takes with tagged lyrics show them.
12. **No new runner env.** All four packs run in the shared torch venv (one more env costs a NATS user, secrets,
    Makefile targets, health ports and `desktop/stack.json`). The `sgl.py` child-venv pattern is the fallback only.
13. **The design system's local copy** is in `docs/design-system/` (commit 7f788b54). Its `kind-music` and
    `kind-sound` tokens are marked "Board only, not built". P0 completes the artifact and the copy from the approved
    board before any UI is built (CLAUDE.md: boards and code use the system), and P6 marks them built; P4 and P5 never
    edit them, so no two phases write the system at once.
14. **`extend_room` is LTX's rule on every `*/extend` node** (`check.py`, studio `api/studio/media.go`): its 321-frame
    room would refuse or mis-fill an audio extend. The rule is limited to video sources; audio extends cap at the
    600 s Audio limit.
15. **An offer needs exactly one unwired placed media input.** A Cover with a second audio input (a reference song)
    would never be offered: that reference sits on an `optional` node outside the studio block, or is left out.
16. **Timeline clip actions offer only the same family's workflows** (README Take actions), so a MiniMax clip would get
    no Repaint there. Review: today an audio-track clip answers Make shot only (`api/studio/timelines.go`
    `ClipActions`, `api/timeline/makeshot.go`), so the rule bites only if audio clips get Repaint, Cover and Extend on
    the Timeline, which the board does not show ("Timeline clips keep the waveform"). Left out of this plan; backlog
    "Timeline: music and sound clip actions by class" (44.10.13).
17. **Audio inputs are decoded in each pack today** (zonos3 `native.py`, ltx `common.py` call ffmpeg themselves). One
    shared `pcm(data, rate, channels)` and `mux(mp4, wav)` (H.264 copied, AAC added, facts from `core.media.probe`) go
    in `core/media.py`.
18. **Long LM loops need cancel points and their own limits.** MiniMax's LM runs 750 frames at 0.11 s: a cancel check
    per frame, `max_seconds` and `stall_s` per stage, and `peak_gb` from the stage's own params (duration).

From the plan review (2026-11-27, against the code and the board):

19. **The voice rule has two more homes**: studio `api/studio/looks.go` (spoken text is never expanded, beside
    `mentions.go`), and the web's own copy of `workflowMedia` and `spoken` in `web_service/lib/mentions.ts` (the graph
    editor computes a user workflow's media in the client). Both re-scope with the class in P1.
20. **Takes are inserted in five places**, not two: `api/gallery/record.go` (run outputs), `upload.go`, `imports.go`,
    `plugin_takes.go` and `api/studio/media_jobs.go` (frame, and now mux); the DTO is `api/gallery/dto/dto.go`, the scan
    `api/gallery/model/model.go`. Imports and plugin audio takes take an optional `sound` like uploads (unset is voice).
21. **Every pack touches shared registry files.** `tests/test_packs.py` `test_published_nodes_equal_the_canonical_palette`
    needs each published node kind in `contracts/fixtures/graphs/palette.json` (one file, a group per family), and
    `check.py` `CANONICAL` (one line) and the `test_real_torch_packs_match_the_fixtures` list (one line) name every
    torch family. Four packs built in one working tree would edit these files at once, and a pathspec commit would carry
    another builder's half-done lines: the packs build in their own git worktrees and merge one at a time (44.7). The
    Python dependencies of all four packs land once, in P1, so `uv.lock` is never merged.
22. **Download entries on the Models page** come from each variant's `source` / `sources` (pinned revision,
    `allow_patterns`). The MiniMax-Music3 repo also holds the SGLang-Omni layout (qwen_7B, `flowmatching_vae.pth`,
    `dav.pth`, 28.7 GB) the runner never reads: its patterns list the diffusers folders only, or a download is twice the
    bytes. MMAudio's list `weights/mmaudio_large_44k_v2.pth` and `ext_weights/` only, ACE-Step 1.5's the turbo DiT, LM,
    text encoder and VAE folders, XL's its 4 shards; DFN5B and BigVGAN are `sources` with their own `dir`.
23. **The Assistant's gallery filter is a screen action too**: `contracts/tools/actions.json` screen `gallery` action
    `filter` has its own `kind` enum beside `catalog.json`'s; both gain the three classes. The Create screen's `MEDIA`
    label map is `web_service/components/create/assistant.ts`, which plan 39 phase F also edits: it changes in P1, never
    in P4.
24. **Web styles are one file** (`web_service/styles/tokens.css`; components style inline), and
    `tests/components/context-menus-places.test.tsx` covers both Create and the Gallery menus: each has one owner phase.
25. **e2e is not part of `make test`**: the journeys run with `make -C web_service e2e` on `helmstudio-test`.
26. **The mux job needs no pack**: it is ffmpeg in the media worker (beside `frame`, `thumbs`, export), so it builds as
    phase M beside the packs, not after them. Kong needs no change (`/v1/studio` is one prefix route).
27. **MMAudio's load peak**: 14 GB while the fp32 checkpoints are read on the CPU, 16.2 GB whole process with a video
    (`/usr/bin/time`); `peak_gb` takes the larger of load and run, so the board and the drawer show 16 GB with a video.
28. **Shared core files were split across parallel phases** (`core/nodes.py` and `core/media.py` in P2a, P2d and P3):
    stereo `_wav`, `pcm()` and `mux()` land in P1, with the echo fakes that need them; the packs only call them.

### 44.4 Facts (measured, M5 Pro 64 GB, torch 2.15, MPS)

| Model | Output | Precision | Peak footprint | Speed | Notes |
|---|---|---|---|---|---|
| ACE-Step 1.5 2B turbo | 48 kHz stereo | DiT fp32, VAE fp16, LM fp32 | 23 GB (25 brief) | 30 s in ≈ 16 s; LM 11.6 s of it, diffusion 1.6 s (8 steps), decode 2.1 s | load 20 s warm, 27 s cold; DCW needs setuptools 80 (pkg_resources) |
| ACE-Step 1.5 XL turbo | 48 kHz stereo | DiT bf16 (cast from fp32 on disk), LM fp32 | 22.5 GB (24 brief) | 30 s in ≈ 16.5 s | fp32 was 32–35.6 GB; load 45 s from the T9 |
| MiniMax Music 3 | 44.1 kHz stereo | all bf16 | 26.6 GB (28.5 brief) | ≈ 4.9× real time (30 s in 148 s) | text to music only; same seed repeats bit for bit; instrumental by prompt plus `[instrumental]` lyrics; may stop early |
| Stable Audio 3 Small-SFX | 44.1 kHz stereo | fp32 | 4.4 GB | 6 s in 0.5 s | same seed repeats; HF gated; levels not normalised |
| MMAudio large 44k v2 | 44.1 kHz mono | bf16 | 8 GB text, 15.1 GB with a video (16.2 GB with the load) | 8 s in 3.8 s (text), 8.7 s (video) | load peak 14 GB (fp32 read on CPU); 25 fps sync, so trim the source 0.2 s long |

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
- Timeline: music and sound clip actions by class (44.10.13).

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
  - Upload, import and plugin take: optional `sound` (default applied: the Create tab or Gallery filter it comes
    from; unset is voice).
  - The gallery kind enums gain the three values: the tool catalog's (`contracts/tools/catalog.json`) and the screen
    action `gallery.filter`'s (`contracts/tools/actions.json`). No new screen actions: the Assistant reaches Repaint,
    Cover, Extend and Vary through `switchTo`, `change` and `generate` (44.10.15).
  - The voice rules re-scope to `sound = voice`: `check.py` (spoken-text expansion, mention refusal), studio
    `mentions.go` and `looks.go`, the web's `lib/mentions.ts`, pipelines `assistant.py` (`voice = media == "audio"`).
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
  job; 422 when the take is not audio or the other not video; 404 for another user's take; 409 when no live host
  can run ffmpeg (as frame); idempotent per pair.
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
    (Qwen3-Embedding-0.6B), vae. Built from diffusers' `AceStepPipeline` classes with `rename()` (44.3.7); `silence_latent.pt` read by the stage
    with `torch.load`. Stages `plan` (`uses [lm]`, `alone`; LM loaded with transformers, causal-LM head and audio-code
    tokens checked on 5.23: metas/caption then 5 Hz codes, handed on as a family Conditioning to
    `audio_codes`; `reports`; seeded; bypass = "Let it rewrite" off), `compose` (DiT, phases n/8; repaint, cover and
    extend by `task_type`), `decode` (VAE; lyric timestamps
    give sections). Workflows `ace-step-music`, `ace-step-repaint` ("Repaint"), `ace-step-cover` ("Cover", `strength`
    = Keep the song), `ace-step-extend` ("Extend", seconds, End / Start; a repaint past the end). Repaint, Cover and
    Extend have no plan node (upstream DIRECT_CONDITIONING_TASKS). The LM stays resident (75% of the time).
  - `packs/minimax_music3/`: family `minimax-music3`; components language_model, rvq_depth_decoder, condition_encoder,
    transformer (stored fp32, cast bf16), vocoder; uses diffusers 0.43's `modular_pipelines/minimax_music3` (check it
    matches PR commit dafe3733; vendor that module if not). Stages `lm` (phases by frame) and `flow`, `vocode`.
    Instrumental by the prompt rule and `[instrumental]` lyrics. One workflow `minimax-music3`.
  - `packs/stable_audio_3/`: family `stable-audio-3`; `model.safetensors` split into dit and autoencoder by rename
    prefixes, plus t5gemma; fp32; diffusers' StableAudio3 text, audio-to-audio and inpaint pipelines; `gated: true`, `must_accept`. Workflows `sa3-sound`,
    `sa3-repaint` (inpaint, mask = span), `sa3-vary` ("Vary", init audio, Change = noise strength), `sa3-extend`
    (continuation); From a sound = a rows param of one media, min 0, max 1.
  - `packs/mmaudio/`: family `mmaudio`; components net (`.pth`), ext autoencoder, synchformer, clip (`dir`, DFN5B),
    bigvgan (`dir`); sources with `{repo, dir}` for the helpers; bf16; vendored networks, flow_matching, ext,
    synchformer, features_utils (not gradio, hydra, tensorboard). Workflows `mmaudio-sound` (text) and
    `mmaudio-video` (a video in; two outputs: Audio, and Video muxed in-stage with ffmpeg: H.264 copied, AAC added, cut
    to the length; trims the source 0.2 s long for the 25 fps sync).
  - Core: `core/nodes.py` stereo `_wav`; `core/media.py` `pcm()` and `mux()` (44.3.17); `core/loaders_torch.py` a pack `load_component(component, model, ctx)` hook
    and `.pth/.bin/.pt` reads; `core/pack.py` size for non-safetensors (file bytes at the target dtype) and the `sound`
    key; `core/graph.py` span and `media_fit` for audio (all P1); the media worker's `mux` kind
    (`messaging/media.py`, phase M).
  - `packs/echo/`: fake music and sound stages (stereo), an Audio+Video stage, audio repaint and extend, the planner's
    reports, sections; so the contract suite runs on the fake runner.
  - `pyproject.toml` / `uv.lock`: open_clip_torch (with timm) for MMAudio; numpy stays >= 2 (MMAudio's < 2.1 pin is
    not needed); any other dependency only if the diffusers path proves it needs one (P2 checks; the samples' venvs
    needed setuptools 80 for ACE-Step's DCW, which is repo code); ruff excludes MMAudio's vendor folder. torch 2.15 stays the pin. `docs/plan/14-dependencies.md` owns the versions.
- **studio_service.** `graph.go` / `workflows.go` (sound, media filter), `projector.go` and `api/gallery/record.go`
  (sound, channels, sections), `api/gallery/service.go` (kind filter), `upload.go`, `imports.go`, `plugin_takes.go`,
  `dto/dto.go`, `model/model.go` (sound), `api/gallery/media.go` (offers by class, span on audio),
  `api/studio/media.go` (audio span source), `mentions.go` and `looks.go` (voice by class), `registry/registry.go`
  (models sound); phase M: the mux endpoint and job in `api/studio/media_jobs.go` beside the frame job, its route in
  `controller.go`.
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
  The Models page (`components/models/models.tsx`) gives music and sound models their kind colour on the cube; their
  download rows, sizes, licences and gated / must-accept come from the registry with no new UI.
- **Frame 7 (looks).** `media.tsx` Thumb: album cover (gradient from a hash of the prompt, in the client) and orb
  (sound sky, voice amber), shared by the Gallery, pickers and strip; Timeline clips unchanged.
- Untouched by the class approach: `components/timeline/*`, voices, dialogue, refs, `lib/timeline*` (the web's
  `lib/mentions.ts` changes only its voice rule, in P1).

### 44.7 Phases

Each phase ships alone and is committed with a pathspec (`git commit -m … -- <its files>`). Gates per CLAUDE.md on
every phase: the touched service's `make test lint`, `make contracts-check`, the contract tests the change touches
(`make test T="…"`; studio DB tests on `.tools/test-db`); the full `make test` once, in P6. **No two phases that run at
the same time own the same file**: the files per phase below are the whole list, and a builder needing a file another
running phase owns asks that phase's builder. Hours are size and risk, not a promise. Every commit names "alpha.6 row
6"; only P6 edits the release list (row 6 covers every change of this plan, the review fixes included).

| # | Phase | Needs | Runs beside | Hours | Tests and gates |
|---|---|---|---|---|---|
| P0 | **Design System**: every new element of the board in the artifact and `docs/design-system/` together (album cover, sound and voice orbs, kind tabs Voice / Music / Sound, BPM · Key · Time group chip, greyed chip "Not on <model>", Auto chip and switch, lyrics tag chips, range bar with section markers, variation card and grid, stage tabs Sound / Video with sound, paired strip tile, the new menu rows); `tokens.css` gains `kind-music` / `kind-sound` and their `on-` texts from `tokens.json` | nothing | P1 | 3 | web `make test lint`; the artifact read back after publish |
| P1 | **Contract and take classes**: 44.6 contract and data (the mux endpoint and `cmd.media.job` kind included); echo fake stages; core stereo `_wav`, `pcm()`, `mux()`; the four packs' Python dependencies; studio sound, filters, offers, span, voice rule; pipelines and CLI voice re-scope; web gen types, `lib/mentions.ts`, the Create `MEDIA` map; `make gen` everywhere | nothing | P0; the contract step first, then the service parts may split by service folder | 11 | `make contracts-check`; runner, studio, pipelines, cli, web `make test lint`; new `contract_tests/tests/test_music_sound.py`, plus `test_takes.py`, `test_voices.py`, `test_uploads.py`, `test_import.py`, `test_plugins.py`, `test_workflows.py`, `test_video_source.py`, `test_timeline_audio.py`, `test_take_isolation.py` (the class filter never shows another user's takes) |
| M | **Mux job**: the media worker's `mux` kind, studio's endpoint, job and its new video take (both parents), idempotent per pair | P1 | P2a–d, P4, P5 | 4 | runner `make test lint` (`test_media.py`), studio `make test lint`; new `contract_tests/tests/test_mux.py` (202, the same job again, 422 not audio / not video, 409 no ffmpeg host) and `test_take_isolation.py` (another user's take is 404) |
| P2a | **ACE-Step pack** (2B + XL as variants; XL bf16 on the Mac) on diffusers' `AceStepPipeline` with `rename()`; the planner stage; all four workflows | P1 | P2b–d, M, P4, P5 (own worktree) | 14 | `make test-torch` (`test_ace_step.py` CPU, `test_packs.py`), `make contracts-check`; `-m gpu` goldens (`record_ace_step.py`) on the M5 |
| P2b | **MiniMax Music 3 pack** (diffusers 0.43 modular pipeline; cancel per LM frame) | P1 | as P2a | 6 | as P2a for `minimax_music3` |
| P2c | **Stable Audio 3 pack** (diffusers StableAudio3 pipelines, `rename()` split of one safetensors, gated); its four workflows | P1 | as P2a | 6 | as P2a for `stable_audio_3` |
| P2d | **MMAudio pack with its helpers** (vendored); core torch `load_component` hook and non-safetensors size; its two workflows, the video one muxing in-stage with `core.media.mux` | P1 | as P2a | 10 | as P2a for `mmaudio`; `test_loaders_torch.py` (a tiny `.pth`; the safetensors packs' meta streaming unchanged) |
| P3 | **Join and offers on the real templates**: the canonical `uses` / `sources` cases on the four packs' templates (no Cover on a voice take, Vary only on sound, span on audio, MMAudio's video workflow classed as sound), the torch registry with all four families, NOTICE | P2a–d merged | M, P4, P5 | 3 | `make contracts-check`; runner `make test-torch` (`test_registry.py`, `test_packs.py`) |
| P4 | **Create UI** (frames 1–5): kinds, chips and popups, drawer blocks, stage markers and range, strip actions, variations grid, MMAudio's two takes, menus; board memory figures fixed to `peak_gb` | P0, P1 (echo fakes); M for Save with the video | P2, P3, M, P5 | 16 | web `make test lint` (its suites below); a look at the dev stack only at P6 |
| P5 | **Gallery and other surfaces** (frames 6–7): filters, cards, panel, menus, pickers, the gallery assistant map, Thumb cover and orb, the Models page cube colours | P0, P1 | P2, P3, M, P4 | 8 | web `make test lint` (its suites below) |
| P6 | **e2e, review, full suite, browser check, docs**: the e2e journeys (44.8); one combined review, then one fix commit; the full `make test` once; `-m gpu` goldens of all four packs on the M5; a browser walk of frames 1–7 against the board on the dev stack (announced first; screenshots to the owner); the Design System's new elements marked built, and any drift found fixed in the artifact and copy together; docs | P3, P4, P5, M | last | 7 | the full `make test`; `make -C web_service e2e` on `helmstudio-test`; goldens; browser check |

Order: P0 and P1 together → then M, P2a–d, P4 and P5 all at once → P3 when the four packs have merged → P6. The packs
(four builders, or two: ACE-Step and MiniMax / SA3 and MMAudio) each work in their own git worktree on a branch from
alpha.6 after P1 (`EnterWorktree`; each worktree syncs its own torch venv from the uv cache), because every pack adds a
line to the same three registry files (44.3.21). They merge into alpha.6 one at a time: rebase, resolve those one-line
lists, rerun the pack's gates, fast-forward, push. M, P4 and P5 work in the main tree (disjoint files). Plan 39 phase
C rebases on P1; its phase H waits for P3; its phase F waits for P1's `create/assistant.ts` change, and its UI phases
write the Design System only when P0 or P6 is not writing it (one writer at a time; the artifact refuses a stale
publish). About 88 hours in all.

Files per phase (each phase owns these and nothing else; "tests" means the named files only):
- **P0**: the Design System artifact; `docs/design-system/` (`tokens.json`, `design-system.json`, `README.md`, the new
  or changed `components/<X>/README.md` and `preview.html`, `components/bundle.css`); `web_service/styles/tokens.css`.
- **P1**:
  - contracts: `openapi.yaml`, `registry.schema.json`, `messages/{evt.run.output,cmd.media.job,defs}.schema.json`,
    `tools/catalog.json`, `tools/actions.json`, `README.md`, `check.py` (sound, span on audio, offers by class,
    `extend_room` on video only, the voice rule), `fixtures/registry/echo.*.valid.json` and `guides.valid.json` if the
    echo guide changes, `fixtures/graphs/palette.json` (the echo kinds), `fixtures/video/{uses,sources}.json`,
    `fixtures/tools/`, HTTP fixtures (Take, TakePage, Upload, UploadCreate, Workflow, the mux request and its 202 body).
  - runner: `packs/echo/` (model.yaml, fake.py, workflows, assistant.md), `packs/{qwen3_tts,zonos3}/model.yaml`
    (`sound: voice`), `core/{pack,graph,nodes,media}.py` (the `sound` key, span and `media_fit` on audio, `extend_room`
    on video only, stereo `_wav`, `pcm()`, `mux()`), `packs/README.md` (`sound`, stereo), `pyproject.toml`, `uv.lock`
    (open_clip_torch with timm; ruff excludes `packs/mmaudio/vendor`), `gen/`, tests `test_graph.py`,
    `test_media_inputs.py`, `test_media.py` (`pcm`, `mux`), `test_voices.py`, `test_packs.py` (echo only).
  - studio: `migrations/00055_take_sound.sql` (the next free number when it lands), `api/gallery/{service,upload,
    imports,plugin_takes,record,media}.go`, `api/gallery/dto/dto.go`, `api/gallery/model/model.go`,
    `api/studio/{projector,graph,workflows,media,mentions,looks}.go`,
    `registry/registry.go`, `gen/`, and the Go tests beside each.
  - pipelines: `assistant.py`, `assistant_knowledge.md`, `gen/`, its assistant tests. cli: `mcp.go`, `takes.go`,
    `gen/`, `mcp_test.go`.
  - web: `gen/api.ts`, `lib/mentions.ts`, `components/create/assistant.ts` (`MEDIA` labels), `tests/mentions.test.ts`.
  - docs: `docs/plan/14-dependencies.md` (open_clip_torch, timm; torch, transformers and diffusers unchanged).
  - contract_tests: `tests/test_music_sound.py` (new) and the touched tests in the table.
- **M**: `runner_service/messaging/media.py`, `runner_service/tests/test_media.py`;
  `studio_service/api/studio/media_jobs.go`, `controller.go` (the route), `api/studio/dto/` (the mux request), its Go
  test; `contract_tests/tests/test_mux.py` (new), `test_take_isolation.py`.
- **P2a** (worktree): `runner_service/packs/ace_step/` (model.yaml with `source` and `allow_patterns` per variant,
  torch.py, workflows/, assistant.md, licences/, GOTCHAS.md; no vendor/),
  `contracts/fixtures/registry/ace-step.{nodes,workflows,models}.valid.json`, `runner_service/tests/test_ace_step.py`
  (CPU stubs, the download plan on `tests/fake_hub.py`), `test_ace_step_golden.py`, `tests/golden/record_ace_step.py`,
  `tests/golden/ace_step/`; the one-line joins: `contracts/check.py` `CANONICAL`, `contracts/fixtures/graphs/palette.json`
  (the family's group), `runner_service/tests/test_packs.py` (the families list).
- **P2b** (worktree): the same set for `packs/minimax_music3/` and `minimax-music3.*` (allow_patterns: the diffusers
  folders only, 44.3.22).
- **P2c** (worktree): the same set for `packs/stable_audio_3/` and `stable-audio-3.*` (`gated`, `must_accept`; the
  fake hub test checks the token is asked for).
- **P2d** (worktree): the same set for `packs/mmaudio/` (with `vendor/`, `VENDORED.md`, sha256s; `must_accept`,
  44.10.17) and `mmaudio.*`, plus
  `runner_service/core/loaders_torch.py`, `core/pack.py` (non-safetensors sizes), `tests/test_loaders_torch.py`, and
  `packs/README.md` (the `load_component` hook).
- **P3**: `contracts/fixtures/video/{uses,sources}.json` (the real-template cases), `contracts/check.py` only if a
  case needs a rule, `runner_service/tests/test_registry.py`, `NOTICE` (the vendored MMAudio and each model's licence).
- **P4**: `web_service/lib/{media,workflow,runs,source,video}.ts`,
  `web_service/components/create/{create,form,prompt,audio,source,workflows,memory-chip}.tsx`; tests
  `tests/{media,source,video}.test.ts`, `tests/components/{audio-create,create-popups,model-chip,run-strip,
  video-create}.test.tsx`, `create-use-in.test.ts`, `tests/components/context-menus-create-sound.test.tsx` (new: the
  take, variation, sound and video menus of frames 3d, 4b, 5c, 5d); `docs/mock/CreateMusicSound.dc.html` (memory
  figures only). Not `components/media.tsx` (P5), `components/gallery/picker.tsx` (P5: the Video-filtered picker
  already filters by `accepts`), `styles/tokens.css` (P0), `create/assistant.ts` (P1).
- **P5**: `web_service/lib/takes.ts`, `web_service/components/gallery/{gallery,picker}.tsx`,
  `components/gallery/assistant.ts`, `components/media.tsx` (Thumb: cover and orb), `components/models/models.tsx`
  (cube colours); tests `tests/takes.test.ts`, `tests/components/{gallery-filters,gallery-use-in,thumbs,
  assistant-gallery,media-rows,context-menus-places}.test.tsx`, `models.test.ts`.
- **P6**: `web_service/e2e/music-sound-journeys.spec.ts` (new); fix files from the review (each owned by P6 then); the
  Design System artifact and `docs/design-system/` ("built" marks); `contracts/README.md` and `runner_service/packs/README.md`
  wording if the review finds gaps; `docs/reference-notes/music-sound.md` (new), `CHANGELOG.md`,
  `docs/release/0.1.0-alpha.6.md` row 6.

### 44.8 Tests

- **Goldens per new model path**, fixed seed, smallest size, `-m gpu` on the M5 against the sample scripts: ACE-Step 2B
  and XL text to music (10 s, plan on, seeded LM: the same seed twice gives the same file), Repaint, Cover, Extend;
  MiniMax Music 3 (10 s; bit-exact repeat as measured); SA3 sound, repaint, vary, extend (2 s); MMAudio text (2 s) and
  from a 2 s video (sound and muxed video). Metrics: RMS and peak within tolerance, length, channels, rate. Each case
  keeps its metrics JSON and a short WAV (the first 2 s), so a pack's goldens stay under about 5 MB in git (today's
  37 MB). Record scripts take the samples folder and the weights root as arguments, as `record_zonos3.py` does.
- **CPU tests per pack** (no weights, no GPU, so they run anywhere): stub folders and tiny tensors; registry values
  against the canonical fixtures (`test_packs.py`); the loader hook on a tiny `.pth`; stereo `_wav`, `pcm()` and
  `mux()` round trips (P1).
- **Downloads without the Hub**: each pack's test plans its download against `tests/fake_hub.py` (as
  `test_model_jobs.py` does): the bytes, the allow_patterns (no SGLang files for MiniMax), the helpers' own `dir`
  (MMAudio), XL's `shares`, a gated repo asking for the token (Stable Audio 3), `must_accept` (SA3, MMAudio). The
  Models page needs no new UI: its rows come from the registry (`models.test.ts` covers the new cube colours only).
- **Contract tests** (fake runner, `helmstudio-test`): the takes `kind` filter by class; Workflow `sound` and
  `?media=music`; uploads with `sound`; offers by class (no Cover on a voice take); span on an audio take; a run giving
  two takes (sound and video with sound); mux 202, 422, idempotent; sections and channels on the take; backfill to
  voice.
- **Isolation for new endpoints** (`contract_tests/tests/test_take_isolation.py`): another user's take in mux, as
  either input, is 404; the class filter never shows another user's takes; another user's workflow never classes into
  the `?media=` list.
- **Fake mode everywhere else**: the contract suite and e2e run on the echo pack (`env fake`), whose stages give
  stereo music and sound, sections, the planner's reports and an Audio + Video run; nothing in `make test` needs a GPU.
- **e2e journeys** (`web_service/e2e/music-sound-journeys.spec.ts`, `make -C web_service e2e` on `helmstudio-test`):
  make music, repaint a range, extend it; make 4 sounds, keep one, vary it; sound from a video, open Video with sound,
  Sound only back, Save with the video on an older sound take; Gallery filter Music shows the cover card; each new
  right-click menu opens with its items.
- **Browser check** of frames 1–7 against the board (P6), screenshots sent to the owner.
- Gates per CLAUDE.md (44.7); the full `make test` once at the end (P6).

### 44.9 Risks

1. **The `rename()` maps** from ACE-Step's and Stable Audio's original layouts to the diffusers classes are unproven,
   and Extend as a repaint past the source's end in `AceStepPipeline` is unverified. Fallbacks: the diffusers-format
   repos (a download), then a native child in its own venv (`sgl.py` pattern, cold-loading 20–56 s every run).
2. **MMAudio's `.pth` weights** against the safetensors-only loader: the hook must keep the meta-device streaming for
   the safetensors packs unchanged.
3. **Stereo Audio** across voice clone, the Timeline and LTX audio-to-video: downmix where mono is needed; contract
   tests cover each.
4. **Classing** of Sound and MMAudio-video workflows for the tabs and offers: a wrong class hides a model or offers the
   wrong action.
5. **Memory**: MiniMax Music 3 (26.6 GB) and XL beside another resident model can pass the 52 GB budget; the
   `peak_gb` values and the host memory plan unload first.
6. **MiniMax speed** (5× real time): a 2-minute song takes 10 minutes; the estimate on Generate says so.
7. **Clashes with plans 39 and 40** (both alpha.6, built at the same time). Contract and migrations: P1 lands 1.77.0
   first, Magic and Model folders (1.78.0) rebase and renumber. Shared files, one phase at a time across the plans:
   `runner_service/core/pack.py` (P1 and P2d here; plan 40 P2's folder resolver), `core/registry.py` and
   `core/download.py` (plan 40 P2; untouched here), `components/models/models.tsx` (P5 here, plan 40 P4: whichever runs
   second adds its lines), `components/create/assistant.ts` (P1 here, plan 39 F), `contracts/tools/{catalog,actions}.json`
   (P1 here, plan 39 F), the Design System and `docs/design-system/` (P0 and P6 here, plan 39's and plan 40 P4's UI
   phases: one writer at a time).

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
13. Timeline clip actions for music and sound clips (Repaint, Cover, Extend by class) are **not built**: the board
    keeps Timeline clips as they are, and audio-track clips answer Make shot today (44.3.16). **Applied** (review
    default; was "match by class"); backlog by name if the owner wants them.
14. ACE-Step, MiniMax Music 3 and Stable Audio 3 run on the locked diffusers 0.43 classes with `rename()` from the
    weights already on disk, not the diffusers-format repos. **Applied** (44.3.7); the repos are the fallback.
15. No new Assistant screen actions for Keep, Keep all or Save with the video: the Assistant makes and changes music
    and sound through `switchTo`, `change` and `generate`, and finds takes with the new filter values. **Applied**
    (review default); new actions would add rows to `contracts/tools/actions.json` screen `create`.
16. Imported and plugin audio takes default to voice when they name no class, as uploads do. **Applied** (review
    default).
17. MMAudio's weights are CC-BY-NC 4.0 and DFN5B's an Apple research licence: the MMAudio download also asks to accept
    them (`must_accept`, as Stable Audio 3). **Applied** (review default); licences stay declared, not a filter.
18. The Assistant's take cards (`components/assistant/media.tsx`) keep today's audio look; the board's cover and orb
    cover the Gallery, pickers and strip only. **Applied** (review default); one line in P5 if the owner wants them
    there too.
