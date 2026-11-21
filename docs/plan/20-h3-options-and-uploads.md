# helmstudio-next plan — MiniMax H3 options and optimised uploads

[← Plan index](../README.md)

Status: a plan, not in a milestone yet: it becomes one in [08-milestones.md](08-milestones.md) when the owner
schedules it. Design only: nothing here is built. Two independent parts that ship apart:

- **A. MiniMax H3 options** the owner picked on 2026-11-22 from the h3c-studio parity audit: quality presets, chaining
  from a take's frame, Mac speed controls, replacing a video reference's sound, NVIDIA quality modes and flow shift.
  Not picked (not planned): live preview, keep-loaded, more aspects, memory options, reference image size, prompt
  history.
- **B. Optimised uploads**: the browser shrinks and re-encodes every media upload before it goes to storage.

Facts come from a read-only scout on 2026-11-22 of:
- h3c-studio (`static/app.js`, `static/canvas.js`, `static/index.html`, `server/params.go`, `h3c/README.md`);
- the vendored h3.c (`runner_service/vendor/h3c`, 4cafd17: `main.c`, `h3.c`);
- the `minimax_h3` pack (`model.yaml`, `native.py`, `sgl.py`, `GOTCHAS.md`, the three workflows);
- SGLang 0.5.23's MiniMax H3 code (the source copy used for plan 17, `sglang/multimodal_gen`);
- the model folder's `FL2VA/model_index.json` and `Ref2VA/model_index.json`;
- `web_service` (`lib/takes.ts`, `components/create/refs.tsx`, `components/gallery/{gallery,picker}.tsx`,
  `components/timeline/shot.tsx`), `files_service/api/file/controller.go`, `kong/kong.yml`, `contracts/`.

Boards: [H3Options](../mock/H3Options.dc.html) (A) and [UploadOptimise](../mock/UploadOptimise.dc.html) (B), drawn
before any code (owner rule: boards first). Short button and chip text throughout.

## 24. MiniMax H3 options and optimised uploads

### 24.1 Corrections from the scout

- **h3c-studio's 75 % and 62.5 % render sizes often fail in h3.** h3.c accepts an internal size only when it has
  exactly the canvas's shape on the 32 px grid (`render_width × height == render_height × width`, `h3.c:500-510`).
  h3c-studio picks an approximate size (`internalRenderSize` → `solveCanvas`), so on 864 × 480 its 75 % asks for a
  size h3 refuses. Its labels come from the 512² validation points (384 and 320 of 512). helmstudio picks an exact
  size instead (24.3, A3).
- **864 × 480 has only two smaller same-shape sizes**: 576 × 320 and 288 × 160 (27 × 15 cells share a factor of 3
  only). So on the default canvas 75 % and 62.5 % both land on 576 × 320. 1344 × 768 (factor 6) and 512² (factor 16)
  have fine steps.
- **SGLang's `quality: high` cannot run on our NVIDIA host.** It fails closed unless the server is the audited
  deployment (4 GPUs, sequence parallel 4, no quantization, no weight overrides, no layerwise offload) and the request
  is the audited workload (t2va, 1344 × 768 × 124, 50 steps, flow shift 12 / 3) (`configs/pipeline_configs/minimax_h3.py`
  `validate_quality_deployment`, `release_metadata.py`). Our VM is one card with FP8 weight overrides and an offloaded
  text encoder, so every `high` request is refused. Only **exact** and **lossless** (SGLang's default, which we send
  today by sending nothing) are usable.
- **h3.c has no flow shift option**: the shifts (video 12.0, audio 3.0, from `model_index.json`
  `_minimax_h3.sigma_shift_scales`) are fixed on the Mac. Flow shift is NVIDIA only.
- **Replacing a soundtrack works on NVIDIA by muxing.** SGLang reads a video reference's sound whenever the file has an
  audio stream (`prequeue.py`, `reference_encoding.py` `has_audio`; there is also a `video_audio` type that requires
  one). h3.c's `--ref-video-audio VIDEO AUDIO` is the same as a `--ref-video` whose file carries that sound (both are
  cut to the clip's frames, `h3.c:1229-1250`). So the pack muxes the new sound into the cut clip and sends what it
  sends today on both hosts. Unverified on the VM, like every SGLang path.
- **Core reuse and denoiser reuse cannot be combined** (`h3.c:574`), core reuse is 1–6, layers 35–50, reuse 1–3.
- **A known bad combination**: token reduction with layers 40 or fewer and reuse 3 gave colour ringing and ghosted
  limbs (h3.c README). Token reduction should also stay off at 256².
- **Users cannot upload video today.** Uploads are images (png, jpeg, webp) and audio (wav, mp3) at most 50 MB;
  `video/mp4` is accepted only on an import key (`u/<owner>/imports/<take>.mp4`). Optimising video therefore means
  adding video uploads (24.5).
- **Uploads are not presigned S3 links.** Studio answers a signed (HMAC) `PUT /files/<key>` link; the browser PUTs
  through Kong (2 GB cap on `/files`) to files_service, which checks the signed size and type and streams to the
  object store, never overwriting.
- **The derived image cap is 8192 px, not ~4096.** The largest Canvas side in the registry is MiniMax H3 Image's
  8192 (its canvas is 256–8192 at ≤ 20 MP); Qwen's is 4096, LTX 1920, H3 1344 (24.5, open point 1).

### 24.2 What comes in

| # | Item | Mac | NVIDIA |
|---|---|---|---|
| A1 | Quality presets Draft, Fast, Default, Reference | all four set steps, layers, reuse, token reduction | steps only (24.3) |
| A2 | Chain from a take's last or first frame | yes | yes |
| A3 | Speed controls in an Advanced section: layers, reuse, core reuse, token reduction, render size | yes | hidden |
| A4 | Replace a video reference's sound | yes | yes (muxed) |
| A5 | Quality mode (Lossless, Exact), flow shift, audio flow shift | — | yes |
| B | Optimise every media upload in the browser | — | — |

### 24.3 A. MiniMax H3 options

All of A is data in `model.yaml` and the workflow files, as today; the UI draws it from the node schemas.

**A1. Presets.** The three H3 workflows (`1-…text-to-video`, `2-…image-to-video`, `3-…references`) get
`studio.presets` (with `preset_order`), as Qwen and LTX have:

| Preset | steps | layers | reuse | token reduction |
|---|---|---|---|---|
| Draft | 4 | 50 | 1 | off |
| Fast | 20 | 45 | 2 | on |
| Default | 20 | 45 | 2 | off |
| Reference | 50 | 50 | 1 | off |

- The `speed` enum (exact / fast) goes: it was layers 50 / reuse 1 and 45 / 2, which the presets and the new params
  now say directly. It leaves the Quick strip; Quick keeps Variant and Seed; the Preset pills sit at the top of the
  inspector, as on LTX.
- The templates open on **Default** (20 steps with reuse 2 at 45 layers: about 10 full passes, so about 1.6× the time of today's 6
  exact steps; open point 3).
- **On NVIDIA (Base fp8)** layers, reuse, core reuse, token reduction and render size are hidden
  (`when: "model.variant == 'base'"`) and the SGLang path ignores them, as Qwen's steps are ignored on Turbo. So a
  preset sets only **steps** there: Draft 4, Fast 20, Default 20, Reference 50. Fast and Default run the same on
  NVIDIA; the preset's help says so. `check_generate`'s "speed fast is h3's" refusal goes with `speed`.
- A preset sets only the keys it names; changing Steps after picking one shows no preset selected, as today. Every
  preset also sets `core_reuse: 1`, so a core reuse left from an earlier run never turns Draft or Reference into a
  rougher run (Render is left alone: it is a size choice, shown in the stage tag).

**A2. Chain from a frame.** The frame endpoint already exists (`POST /v1/studio/takes/{id}/frame {at}` → an image
take, instant for run takes, a frame job for uploads and imports).
- **Gallery, Create strip, Timeline clip menu**: a video take's menu gets **Last frame in…** and **First frame in…**.
  Each makes the frame take, then shows that image take's "Use in…" offers (H3 Image to video: First frame; LTX Image
  to video; references). Web only, two existing calls; no contract change.
- **The Last frame tile** of H3 Image to video (optional node, so "Use in…" never fills it) gets the same pick from its
  gallery picker: a video take there offers **Use last frame** / **Use first frame**.
- **Timeline New shot**: "Start from last frame of …" and "End on first frame of …" work for H3 Image to video too.
  Today `Workflow.keyframes` only finds an LTX-style rows param (Image + `position: seconds`). H3's frames are two
  image params, so the workflow file declares them: `studio.keyframes: {first: "first.image", last: "last.image"}`
  and `Workflow.keyframes` gains that shape (24.6). Re-render binds `first.image` ← `clip_<other>.last_frame` and
  `last.image` ← `clip_<other>.first_frame`, as it binds LTX rows today.
- Works on Mac and NVIDIA alike: the frame is an ordinary image input (SGLang fl2va takes any aspect).

**A3. Speed controls (Mac).** New `generate` params, all `when: "model.variant == 'base'"`, in a last inspector
section titled **Advanced**:
- `layers` int 35–50 (default 50 in the schema; presets set it), help "DiT blocks: 50 exact, 45 fast, 40 aggressive;
  fewer also hold less of the transformer".
- `reuse` int 1–3, "1 close, 2 fast, 3 aggressive; at 20 steps 20, 11 or 8 fresh passes".
- `core_reuse` int 1–6, default 1, shown only when Reuse is 1 (`when: "… && generate.reuse == 1"`): "runs the costly
  core every n steps; 4 fast, 6 a rough preview".
- `token_reduction` bool, "pairs video tokens in the middle blocks: about 25 % faster, can shift the composition".
- `render` enum `Full`, `75%`, `62.5%`, "renders smaller and scales up after decoding: faster, less fine detail".
- The pack's argv: `--steps`, `--layers`, `--reuse`, `--core-reuse` only when reuse is 1 and core reuse > 1,
  `--token-reduction` when on, `--render-width/--render-height` when not Full, `--use-int8-row-fc2` as today.
- **The render size rule** (pack, one function, unit tested): with g = gcd(w/32, h/32), k = g × scale rounded half up,
  capped at g − 1, the size is (w × k / g, h × k / g). 864 × 480 → 576 × 320 for both; 1344 × 768 → 1120 × 640 at 75 %,
  896 × 512 at 62.5 %. When k would be 0 or the canvas has no smaller same-shape size (g = 1) the run is refused
  before h3 starts: `generate: the canvas 832 × 480 has no smaller size of the same shape; use Full`. The run's first
  progress line names the size ("renders 576 × 320"); the Create stage tag shows it. No size maths in the web.
- **Refusals** (`generate_error`, mirrored in `contracts/check.py`; Base only, since Base fp8 ignores these params):
  token reduction with layers ≤ 40 and reuse 3 (`… gives colour ringing; use reuse 2 or more layers`); token
  reduction when h3 renders at 256 × 256, i.e. the render size, not only the canvas (384² at 62.5 % renders 256²;
  h3.c README "Keep token reduction off at this size").
- h3's own warning for reuse > 1 at ≤ 7 steps stays a warning (Draft uses reuse 1).

**A4. Replace a video reference's sound.** The `videos` row's `sound` enum becomes `keep | silent | replace` and the
item gets a second media field `soundtrack` (accepts audio, trim, `min_s` 2.34, empty unless Sound is Replace).
- **What is read**: the soundtrack's in..out, cut to the video part's length (a shorter soundtrack is fine down to
  2.34 s; h3 truncates to the clip's decoded frames, `h3.c:1240`).
- **The sound rules count it like a kept sound**: it is one of the 3 sound inputs; it needs a clip of ≥ 56 frames and
  a video part of ≥ 56 frames; its seconds (min of the soundtrack part, the video part and frames / 24) count toward
  the 15 s of all reference sound. `<Audio n>` numbering is unchanged: audio rows first, then videos with sound (kept
  or replaced), in row order (GOTCHAS "Audio numbering spans kinds").
- **Texts**: Replace without a soundtrack: `generate.videos[2].soundtrack: Replace needs a soundtrack`; a soundtrack
  with Keep or Silent: `generate.videos[2].soundtrack: a soundtrack is read only with Sound: Replace`.
- **The pack**: `native.cut` takes the soundtrack's part as a second ffmpeg input and maps it as the clip's PCM track
  (`-map 0:v:0 -map 1:a:0 -shortest`), then passes `--ref-video` (Mac) or a `video` condition with sound (SGLang) as
  for a kept sound. One code path on both hosts.
- **Lineage**: the soundtrack take is a parent, after the video (input order).

**A5. NVIDIA quality and shift.** New `generate` params, all `when: "model.variant == 'base-fp8'"`, in the Quality
section:
- `quality` enum `lossless | exact`, default lossless, "Lossless: SGLang's default fast paths, the same maths. Exact:
  bit-identical to the reference path, slower. High is not offered: SGLang allows it only on its audited 4-GPU setup".
- `flow_shift` float 1–20 step 0.5, default 12, and `audio_flow_shift` float 1–10 step 0.5, default 3: "how the
  noise schedule spends its steps; the model's values are 12 and 3". Sent in the request only when they differ from
  the defaults (`flow_shift`, `audio_flow_shift`), so a default run's request is unchanged.
- Measure on the VM: one run each of exact and lossless at 864 × 480 × 124, 20 steps (s/step, VRAM), and one with
  shift 8 / 3 to see it takes. Record in GOTCHAS.

### 24.4 What the user sees (board H3Options)

1. Inspector on Base: **Preset** pills Draft · Fast · Default · Reference; **Quality**: Precision, Steps;
   **Advanced** last: Layers, Reuse, Core reuse (only at Reuse 1), Token reduction switch, Render (Full / 75% /
   62.5%). The Generate key's estimate follows the preset.
2. A run at 75 %: the stage tag "renders 576 × 320 → 864 × 480".
3. Inspector on Base fp8: the same pills (help: "NVIDIA: steps only"), Quality: Precision, Steps, Mode (Lossless /
   Exact), Flow shift, Audio flow shift; no Advanced.
4. A video reference row with Sound Keep · Silent · **Replace**, its soundtrack row (pick, trim, part), and the sound
   count "5.0 s of 15.0 s sound".
5. The two refusals in red under their inputs (render size, token reduction combination) and Replace without a
   soundtrack.
6. A video take's menu: Use in… · **Last frame in…** · **First frame in…**, the offers menu after it, and the Last
   frame tile's picker with **Use last frame** / **Use first frame** on a video.
7. Timeline New shot on H3 Image to video with "Start from last frame of Shot 2" and "End on first frame of Shot 3".

### 24.5 B. Optimised uploads

**Every media upload path** goes through one function, `uploadFile` in `web_service/lib/takes.ts`:
- Create references and image params (`useRefUploads` in `components/create/refs.tsx`: drop, paste, Upload; shared by
  Create, the pipeline Studio and New batch);
- the gallery picker's Add audio (`components/gallery/picker.tsx`), used by media params and rows;
- Gallery's Upload and drop (`components/gallery/gallery.tsx`).
Not media, left alone: LoRA uploads (`lib/loras.ts uploadLora`), New batch's CSV. Not browser, left as they are:
`helm upload` and `helm import` (CLI, `cli/takes.go`; they upload files as they are, within the same server limits).

So the optimiser is one step inside `uploadFile`, before `POST /v1/studio/uploads`: `optimise(file, limits,
onProgress)` → the Blob to upload (or the original). Every caller gets it with no change but the tile's two states.
`checkFile` splits: the type is checked on the file dropped (now also m4a and, with B4, video), the 50 MB (video
2 GB) cap on the Blob that uploads, so a 120 MB wav or a 70 MB PNG that used to be refused now uploads once
optimised. `uploadFile` takes an `AbortSignal`: the pending tile's × (today it only dismisses a failed tile) stops the
conversion or the PUT; a PUT stopped midway leaves a pending take, as a closed tab does today.

**Limits come from the registry, not constants.** Studio computes them from every node schema in the registry and
serves `GET /v1/studio/uploads/limits` (new, cached by the web per session):
- `image_max_side`: the largest `max` of a `width` or `height` param on any node with a `Canvas` output. Today 8192
  (MiniMax H3 Image); 4096 without it (Qwen).
- `video_max_side`: the same over the video families' Canvas nodes (workflow `media: video`): 1920 (LTX).
- `video_max_fps`: the largest `max` of a frames param's fps param on those nodes: 60 (LTX).
- `video_grid`: the largest `grid` of a media param accepting video: 32 (LTX retake / extend), so an upload fits every
  video input.

**Images**:
1. Decode with `createImageBitmap(file, {imageOrientation: "from-image"})` (EXIF applied, as studio does).
2. If the long side is over `image_max_side`, scale down (OffscreenCanvas, high-quality smoothing).
3. Keep alpha when the image has any transparent pixel (png / webp sources only): Qwen reads references as RGBA
   (the Transparent option and its edits), so alpha is never flattened. WebP keeps alpha losslessly; where the
   browser cannot encode WebP (Safari), alpha images stay PNG.
4. Encode WebP at 0.9, else JPEG at 0.9 (no alpha).
5. Upload the original instead when nothing was scaled and the new file is not at least 10 % smaller (an already small
   jpeg, a tiny png).

**Audio**: wav → AAC-LC in an `.m4a` (256 kb/s stereo, 192 mono) via WebCodecs `AudioEncoder`, at 44.1 or 48 kHz as
the source; any other rate (a 16 or 24 kHz voice clip) is resampled to 48 kHz first, since browsers' AAC encoders take
only those two. mp3 and m4a are uploaded as they are (lossy to lossy gains little; `typeOf` maps `audio/x-m4a` and
`audio/mp4`). New upload type `audio/mp4`.

**Video** (new: video uploads, 24.6): any file the browser can decode (mp4, mov, webm) → H.264 + AAC MP4 via
WebCodecs, the long side capped at `video_max_side`, fps capped at `video_max_fps`, sides scaled then cropped to
`video_grid` (1920 × 1080 → 1920 × 1056, 12 px off top and bottom), about 8 Mb/s at 1080p (quality-based where the
encoder supports it). The browser also writes the poster (frame 0, WebP) and reads frames, fps, duration and size for
the create call. **The library**: WebCodecs encodes but does not read or write containers, so this needs a demuxer and
muxer: **Mediabunny** (MPL-2.0, from the mp4-muxer author; one `Conversion` API over WebCodecs with progress). Pinned
in [14-dependencies.md](14-dependencies.md) when built; no ffmpeg.wasm (30 MB, slow).

**Fallbacks** (`isConfigSupported` false, a decode or encode error):
- an image or wav: upload the original (it is already an allowed type);
- a video: upload the original only when it is an MP4 with H.264 (+ AAC) within the caps, read by Mediabunny without
  decoding; anything else is refused on the tile: "This browser can't convert this video. Use Chrome, or convert it
  to MP4 (H.264)."

**What the user sees** (board UploadOptimise): the pending tile shows **Optimising…** (images: no percent, under a
second; video and audio: a percent and a bar), then **Uploading 48%**, with a mono line under it, original → upload,
for example `12.4 MB → 1.9 MB · WebP` or `4K 60 fps → 1920 × 1056`. When it falls back: `Uploading as is`. The take
afterwards is an ordinary upload; the Gallery info row shows its stored type, size and pixels. The original is not
kept and its size is not stored (open point 6).

**Timing**: images under a second; a 1-minute 4K clip about real time on an M-series Mac (hardware encode); the tab
must stay open, as for any upload today. Cancel stops the conversion.

### 24.6 Contract changes (contracts first, then `make contracts-check`)

Part A (one API version):
- `Workflow.keyframes` (`WorkflowKeyframes`) gains a second shape `{first: StudioPath, last: StudioPath | null}` for
  image params, taken from a workflow's `studio.keyframes` (registry workflow studio block, new key);
  `check.py shot_keyframes` returns it; `rerender_steps` binds `first` / `last` paths; README Timeline: New shot
  options.
- `itemParam`: a `media` item field may be empty (`default: null`, new key `optional: true`); README Rows of media:
  "rows with exactly one media field" (`max_total_s`, offers) become "the item's first required media field"; lineage
  adds every item media take in item order.
- `fixtures/video/minimax-h3.json` and `check.py h3_generate_error`: replaced sound, render size, token reduction
  refusals; the canonical minimax-h3 node schemas and workflows (`speed` gone; the new params; presets;
  `studio.keyframes`).

Part B (one API version):
- `GET /v1/studio/uploads/limits` → `UploadLimits {image_max_side, video_max_side, video_max_fps, video_grid}`
  (computed from the registry; 401 only).
- `UploadCreate.content_type` adds `audio/mp4` (requires `duration_s`) and `video/mp4` (requires `duration_s` ≤ 600,
  `width`, `height`, `frames`, `fps`, `has_audio`, `poster_type`); `bytes` up to 2 GB for video (Kong already passes
  2048 MB on `/files`), 50 MB otherwise.
- `Upload.poster_url` (video only): a second signed PUT for `u/<owner>/uploads/<take_id>.poster.webp|png|jpg`.
- files_service `uploadLimit`: `…/uploads/<id>.m4a` → `audio/mp4` ≤ 50 MB; `…/uploads/<id>.mp4` → `video/mp4` ≤ 2 GB.
  The poster key needs no rule: any other key already takes an image ≤ 50 MB.
- Complete: m4a and mp4 headers (`ftyp` box: M4A brand for audio; `isom`/`mp42`/`avc1` for video) and the poster's
  image header; stores `kind: video` with the declared facts (as imports do: studio has no ffmpeg; the runner reads
  the file itself and a lie fails the run). The README's "uploads stay images" goes. A video upload's last frame comes
  from a frame job, as for imports.

### 24.7 Memory (peak_gb)

- Steps, reuse, core reuse and token reduction do not raise the peak (the denoise working set depends on the tokens,
  not on how often they run). Layers below 50 hold fewer blocks (slightly lower); the formula keeps 50.
- **Render size** shrinks the denoise term and the video VAE decode, not the ~18 GB fixed part. The formula's `mp`
  becomes `mp * r`, r = 1 (Full), 0.75 (75 %), 0.6 (62.5 %): upper bounds of (k / g)² for every grid factor g (the
  worst cases are 0.69 and 0.56). Expected real peaks (the measured fit 18.2 + 0.048 mp × frames at the real size):
  1344 × 768 × 243 measured 30.3 GB → about 27 GB at 75 % (1120 × 640), 24 GB at 62.5 % (896 × 512). The Generate
  key shows the formula, not the fit: at 864 × 480 × 124 about 27 GB at Full and 26 GB at 75 % (the formula's
  24 GB floor dominates small canvases). Measure one run each and record in GOTCHAS.
- A replaced soundtrack is audio only: no change.
- NVIDIA: unchanged (VRAM is not budgeted, plan 17).

### 24.8 Tests (lean)

- **Pack unit** (runner, no GPU): argv for each preset; the render size rule (864 × 480, 1344 × 768, 512², a canvas
  with g = 1); `generate_error` cases mirrored from `fixtures/video/minimax-h3.json`; the SGLang body with quality and
  shifts (sent only when not default); `cut` with a soundtrack (ffmpeg on a tiny fixture).
- **Golden**: one new Ref2VA golden with a replaced soundtrack at 512 × 512 × 56, 2 steps (30 dB, as the other Ref2VA
  goldens); one T2VA at the Fast preset at 512² × 22 (token reduction on). Existing goldens are explicit about steps,
  layers and reuse, so they do not re-record.
- **VM** (manual, plan 17's spot VM): lossless, exact, a shifted run, a replaced soundtrack.
- **Contract suite**: keyframes on H3 image-to-video and re-render's binds; an item media field left empty; the limits
  endpoint; m4a and mp4 uploads (create, PUT 204 / 415 / 411, complete, header refused, poster).
- **Web**: one vitest over the optimiser's pure decision (`plan(meta, limits)` → scale, type, keep original); one
  Playwright run uploading a large transparent PNG, a WAV and a short MOV (Chromium), checking type and size on the
  take. Board parity for both boards.

### 24.9 Phases and effort

| # | Phase | Hours |
|---|---|---|
| 0 | Boards H3Options and UploadOptimise, approved by the owner | done in this pass |
| A1 | Presets, `speed` → layers / reuse / token reduction, the three workflows, argv, fixtures | 4–5 |
| A3 | Advanced: core reuse, render size rule and refusal, the stage tag, peak formula, two measurements | 4–6 |
| A2 | Last / First frame in… (web), the Last frame tile pick; `studio.keyframes` (contract, studio, re-render, New shot) | 7–9 |
| A4 | Replace soundtrack: optional item media (contract, studio rows, offers wording), sound rules, cut mux, golden | 8–10 |
| A5 | NVIDIA quality and shifts, VM runs, GOTCHAS | 4–6 |
| | **A total** | **about 27–36** |
| B1 | Limits endpoint (contract, studio from the registry) | 2–3 |
| B2 | Image optimiser in `uploadFile`, tile states | 4–5 |
| B3 | Audio: m4a upload type (contract, files, studio complete, runner read check), AAC encode | 4–5 |
| B4 | Video uploads (contract, files, studio, gallery and media inputs) and the video optimiser with Mediabunny | 14–18 |
| | **B total** | **about 24–31** |

Order: A1 before A3 (same params); A2, A4, A5 independent. B1 before B2–B4; B3 and B4 independent. A and B are
separate API versions and can ship in either order.

### 24.10 Open points for the owner

1. **Image cap**: the derived value is **8192** (MiniMax H3 Image's canvas), not ~4096. Take 8192 (no model ever
   loses pixels), or cap at what a model *reads* from an input image (Qwen ~1 MP, video first frames at the canvas),
   which needs a new per-input `reads_max_side` in model.yaml? Recommended: 8192.
2. **Render size labels**: keep Full / 75% / 62.5% (on 864 × 480 both pick 576 × 320), or offer the canvas's real
   same-shape sizes (needs studio to serve them per canvas)? Recommended: keep the labels; the stage tag shows the
   size.
3. **Template default**: open on Default (about 1.6× today's time) or Draft (4 steps)? Recommended: Default.
4. **Saved H3 workflows** with `generate.speed` show "unknown param" after the change; map it (exact → Reference
   values, fast → Default) once in studio, or let the owner re-save? Recommended: re-save (few, the owner's own).
5. **Advanced folded**: a plain last section (no contract change), or folded by default (a new studio section key
   `collapsed`)? Recommended: plain.
6. **Original size**: show "12.4 MB → 1.9 MB" only while uploading (nothing stored), or keep `original {bytes,
   width, height}` on the take for the Gallery info? Recommended: only while uploading.
7. **A way to skip optimising** (a lossless PNG for an edit): none, a per-upload "Keep original", or a setting?
   Recommended: none for now.
8. **Video upload caps**: 2 GB and 10 min, the 32 px crop (vs scaling to 1920 × 1088), and whether videos come in now
   or only audio and images (B4 is most of B's hours).
9. **Voice clips on AAC**: ZONOS3 cloning reads the take; a WAV clip becomes AAC 256 kb/s. Keep WAV for audio
   uploads instead? Recommended: AAC, check one clone against the WAV.
