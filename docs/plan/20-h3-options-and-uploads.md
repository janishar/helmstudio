# helmstudio-next plan — MiniMax H3 options and optimised uploads

[← Plan index](../README.md)

Status: a plan, not in a milestone yet: it becomes one in [08-milestones.md](08-milestones.md) when the owner
schedules it. Design only: nothing here is built. Three parts that ship apart (C after A1):

- **A. MiniMax H3 options** the owner picked on 2026-11-22 from the h3c-studio parity audit: quality presets, chaining
  from a take's frame, Mac speed controls, replacing a video reference's sound, NVIDIA quality modes and flow shift.
  Not picked (not planned): live preview, keep-loaded, more aspects, memory options, reference image size, prompt
  history.
- **B. Optimised uploads**: the browser shrinks and re-encodes every media upload before it goes to storage.
- **C. Turbo** (added 2026-11-22, after the spike in `docs/reference-notes/h3-turbo-spike`): lightx2v's 8-step
  Turbo LoRAs as two optional add-ons of MiniMax H3 (one for text, image and first-last to video, one for
  References) and a **Turbo** preset beside A1's four (24.11).

Facts come from a read-only scout on 2026-11-22 of:
- h3c-studio (`static/app.js`, `static/canvas.js`, `static/index.html`, `server/params.go`, `h3c/README.md`);
- the vendored h3.c (`runner_service/vendor/h3c`, 4cafd17: `main.c`, `h3.c`);
- the `minimax_h3` pack (`model.yaml`, `native.py`, `sgl.py`, `GOTCHAS.md`, the three workflows);
- SGLang 0.5.23's MiniMax H3 code (the source copy used for plan 17, `sglang/multimodal_gen`);
- the model folder's `FL2VA/model_index.json` and `Ref2VA/model_index.json`;
- `web_service` (`lib/takes.ts`, `components/create/refs.tsx`, `components/gallery/{gallery,picker}.tsx`,
  `components/timeline/shot.tsx`), `files_service/api/file/controller.go`, `kong/kong.yml`, `contracts/`.

Boards: [H3Options](../mock/H3Options.dc.html) (A, and C's Turbo frames), [Models](../mock/Models.dc.html) (C's
two add-on cards) and [UploadOptimise](../mock/UploadOptimise.dc.html) (B), drawn
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

**Decided by the owner, 2026-11-22** (one by one): 1. image cap **8192, as derived**; 2. render size **keeps the
Full / 75% / 62.5% labels**; 3. templates open on **Default**; 4. saved H3 workflows with `speed` are **re-saved**
(no mapping); 5. Advanced is a **plain section**; 6. the original's size shows **only while uploading**; 7. **no**
"upload original" option; 8. **video uploads: later (backlog)** — part B covers images and audio only, so no
`video/mp4` upload type, no `poster_url`, no Mediabunny now; 9. **voice clips stay WAV**: audio uploaded for voice
cloning (the voice picker's "clone from a clip", ZONOS3 and Qwen3-TTS) is never converted; other audio (MiniMax H3
audio references, LTX audio to video sources) becomes AAC. The board's video-upload frames are not built.

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

### 24.11 C. MiniMax H3 Turbo (8-step LoRA)

Design approved by the owner (2026-11-22, decisions in 24.12), with one change folded in here: References use
lightx2v's separate ref2v 8-step v1.1 file as their own add-on (+1.4 GB), not the FL2V file. Built against the
shared optional add-on rule (API 1.36, contracts/README "Optional add-ons"; plan 19 23.11 "Changed"), which a
concurrent build is implementing now; C adds one small
extension to it (C3: an add-on picked by a param value, and swapped when references are wired). Board:
[H3Options](../mock/H3Options.dc.html) frames 10-13, [Models](../mock/Models.dc.html)
under MiniMax H3 (two add-on cards). The 4-step v1.3 stays in the [backlog](../backlog.md).

**C1. What the spike found** (`docs/reference-notes/h3-turbo-spike`, `docs/mock/checks/h3-turbo/grid8.png`):

| Run, 864 × 480 × 124, seed 42, Mac M5 Pro | Denoise | Total | Sharpness |
|---|---|---|---|
| Base, 6 steps (today's draft) | 159 s | 279 s | 49 |
| **Turbo v1.2, 8 steps, strength 1** | 207 s | **340 s** | **65** |
| Base, 50 steps | 1390 s | 1505 s | 69 |

Close to the 50-step for 1.2× today's 6-step draft (4.4× faster than 50 steps; A1's Draft is 4 steps, so about
1.5× that). All three runs used `H3_ZERO_COPY_WEIGHTS=0` (the spike's `render124.sh`), so the base timings are of a
copied load, not today's mapped one. The recipe is h3's own defaults: Euler,
no CFG, shift 12 / audio shift 3, so on the Mac only the merge is new. Not yet checked: other prompts, image to
video, sound, 1344 × 768, references (C7), NVIDIA.

**The files** (HF API, 2026-11-22, rechecked: the repo's `main` is still that revision, last modified 2026-11-16;
not gated; licence the MiniMax H3 Community License, the model's own, `minimax-h3-community`; the API's
`license_name` is `minimax-h3-community-license-agreement`), both in `lightx2v/Minimax-h3-Turbo` @
`c2bf13b837e33bd851b370285fc7db99482d2ab2`:

| Add-on | File | Bytes | sha256 (LFS oid) | For |
|---|---|---|---|---|
| `turbo` | `minimax_h3_fl2v_turbo_8step_v1.2_768p_bf16.safetensors` | 1,543,291,302 | `4c94278f9207209c193efd489e89c992d225e3fb3986c3658cf992c1e51f7b39` | FL2VA: text, image and first-last to video |
| `turbo_ref` | `minimax_h3_ref2v_turbo_8step_v1.1_768p_bf16.safetensors` | 1,383,677,808 | `46c64fc94509489f18dd1f545172e30c8df7d4cc50a30e96744409b4288c937d` | Ref2VA: References |

The FL2V file: Diffusers / PEFT layout, 1089 tensors, a per-layer `.alpha` each; 363 modules merge (spike log). The
authors say it covers Ref2VA too, but the owner picked the ref2v file for References (24.12 point 1). The ref2v file
is not yet opened: its key layout and module count are checked in C0 (its size, the same as the FL2V 8-step v1.1,
suggests the same layout). Not used: the `_comfyui_` copies (ComfyUI keys) and the 4-step files (backlog).

**C2. The add-ons** (model.yaml, the 1.36 shape; no model names in studio or the web):

```yaml
components:
  turbo:                          # lightx2v's MiniMax-H3 Turbo FL2V v1.2 (8 steps): FL2VA runs
    dir: MiniMax-H3-Turbo         # its own folder: the same on a Mac (base) and NVIDIA (base-fp8) host
    addon:
      title: Turbo LoRA
      help: "lightx2v's 8-step Turbo LoRA for text, image and first-last to video (1.5 GB). The Turbo preset needs it; every variant works without it."
      source: {repo: lightx2v/Minimax-h3-Turbo, revision: c2bf13b837e33bd851b370285fc7db99482d2ab2,
               allow_patterns: [minimax_h3_fl2v_turbo_8step_v1.2_768p_bf16.safetensors],
               licence: minimax-h3-community, must_accept: true, gated: false, bytes: 1543291302}
    files: minimax_h3_fl2v_turbo_8step_v1.2_768p_bf16.safetensors
    dtype: stored
    precision: [int8]             # as the other components: a component without one counts as [bf16]
                                  # (pack.component_precisions) and would add bf16 to Base's Precision enum
  turbo_ref:                      # lightx2v's MiniMax-H3 Turbo ref2v v1.1 (8 steps): Ref2VA runs (References)
    dir: MiniMax-H3-Turbo         # the same folder; each add-on's presence and delete go by its own file
    addon:
      title: Turbo Ref LoRA
      help: "lightx2v's 8-step Turbo LoRA for References (1.4 GB). The Turbo preset needs it when references are wired."
      source: {repo: lightx2v/Minimax-h3-Turbo, revision: c2bf13b837e33bd851b370285fc7db99482d2ab2,
               allow_patterns: [minimax_h3_ref2v_turbo_8step_v1.1_768p_bf16.safetensors],
               licence: minimax-h3-community, must_accept: true, gated: false, bytes: 1383677808}
    files: minimax_h3_ref2v_turbo_8step_v1.1_768p_bf16.safetensors
    dtype: stored
    precision: [int8]
```

- Two add-ons of **both** variants: Base and Base fp8 are present without them; Models lists two cards under MiniMax
  H3's Optional add-ons, each with its hosts, its own **Download…** and **Delete** (the 1.36 model jobs, `addon:
  turbo` / `addon: turbo_ref`): **Turbo LoRA · v1.2 · 8 steps · 1.5 GB** ("For text, image and first-last to video")
  and **Turbo Ref LoRA · v1.1 · 8 steps · 1.4 GB** ("For References"). Either works alone: a host with only Turbo
  LoRA runs Turbo without references; one with only Turbo Ref LoRA runs Turbo with them.
- One folder for both, `MiniMax-H3-Turbo/`, not `MiniMax-H3/loras/` (the spike's place): the first variant's folder
  does not exist on an NVIDIA host. A delete removes only that add-on's file (`DELETE …?addon=` removes only its
  files). The build checks the 1.36 download marker is per add-on, not per folder; if it is per folder,
  `turbo_ref` takes its own `MiniMax-H3-Turbo-Ref/` instead (data only). The owner's Mac already has the FL2V file
  from the spike: the build moves it into `MiniMax-H3-Turbo/` (no download; presence checks the hash). The ref2v
  file is on no host yet: the owner downloads it in Models (Download…, accepting the licence there) before C0;
  nothing in this pass downloads it.

**C3. How a run carries Turbo** (data, contracts first):
- A new `generate` param on both variants, in the Quality section:
  `lora: {type: enum, label: LoRA, default: none, enum: [none, turbo], addons: {turbo: [turbo]},
  addons_with: {references: {turbo: [turbo_ref]}}, help: "turbo: lightx2v's 8-step Turbo LoRA (an add-on; References
  use their own); use 8 steps. Merged at load on a Mac, applied by SGLang on NVIDIA."}`.
  An enum leaves room for the 4-step (`turbo4`) without a new param.
- **The contract extension**: today an add-on is needed by a whole node (`nodeSchema.addons`, while the node is not
  bypassed). Turbo is needed by one **value** of a param of a node that is never bypassed, so an enum param gains
  `addons: {<value>: [<component>]}` (each an add-on of the node's family; `contracts/check.py` refuses others).
  Which file a run needs depends on its partition, and `generate` picks Ref2VA when any reference is wired (in any
  workflow, not only References), so the param may also carry `addons_with: {<input>: {<value>: [<component>]}}`:
  when that input of the node is wired, its list **replaces** the value's `addons` list (`check.py` refuses an
  `<input>` the node does not have, and the same values and components as `addons`). The run check (Several hosts:
  eligibility, step 2b) adds, for each such param, the add-ons its resolved value lists on the resolved graph: the
  same texts (`generate: Turbo LoRA not downloaded on host gcp`; with references `generate: Turbo Ref LoRA not
  downloaded on host gcp`; `… on any live host for env native`), the same `POST /v1/studio/runs/plan` reason, the
  same Run on rule. The UI greys the option and every **preset whose values pick it**, by the add-on the form needs
  now (no reference: `Turbo LoRA not downloaded`; with a reference: `Turbo Ref LoRA not downloaded`; per host in Run
  on, `… not downloaded on gcp`), and re-checks when a reference is added or removed. One API version (it can ride
  with A's).
- **Per add-on and host**, the greyed texts (board frames 11-13):

| Form | Host has | Turbo pill / turbo option | Run on, per host |
|---|---|---|---|
| no reference | Turbo LoRA | works | works |
| no reference | only Turbo Ref LoRA, or none | `Turbo LoRA not downloaded` | `Turbo LoRA not downloaded on <host>` |
| a reference wired (References) | Turbo Ref LoRA | works | works |
| a reference wired (References) | only Turbo LoRA, or none | `Turbo Ref LoRA not downloaded` | `Turbo Ref LoRA not downloaded on <host>` |

  The pill is greyed only when no live host has the add-on the form needs; per host it is the Run on menu's job.
- **A form that already holds `lora: turbo`** (a saved workflow, a take's Remix) where no host has it: the option
  shows picked and greyed and Generate is blocked with the reason (it never drops to `none` silently: 8 steps
  without the LoRA is a bad run; open point 2).
- **The Turbo preset**, in the three workflows' `studio.presets` (A1), order by time: **Draft · Turbo · Fast ·
  Default · Reference**:

| Preset | steps | lora | layers | reuse | core reuse | token reduction | flow shift / audio (A5) |
|---|---|---|---|---|---|---|---|
| Turbo | 8 | turbo | 50 | 1 | 1 | off | 12 / 3 |

  Every other preset sets `lora: none`, so leaving Turbo turns the LoRA off. On Base fp8 the Mac keys are ignored as
  for the others; the shifts are the model's own, set so a leftover shift never bends the distilled schedule (only
  if A5 is built). Picking `turbo` by hand at 50 steps is allowed (slower, no gain); no refusal.
- Lineage and Take info show `LoRA turbo` like any param.

**C4. Mac (h3.c)**:
- **A documented helmstudio patch** in `runner_service/vendor/h3c`: add `h3_lora.c` / `h3_lora.h` (the spike's code:
  env `H3_LORA` = the file, `H3_LORA_STRENGTH` 1; the PEFT → native key map; per-layer `.alpha`, never the metadata
  alpha; stochastic rounding seeded by the tensor name, so a merge repeats bit for bit and h3's int8 quantizer keeps
  the small delta), the 6-line hook in `h3_weights.c` `load_tensor` (bf16 2-D tensors under `/transformer/`, both
  partitions) and the one Makefile `LIB_C` word. The spike's `H3_VIDEO_SHIFT` hook is **left out** (the 8-step uses
  shift 12; only the 4-step needs it). Everything else stays the upstream copy: `VERSION` says "changed: the
  helmstudio LoRA patch (patches/h3-lora.diff)", the diff is kept beside it, and `THIRD_PARTY_NOTICES.md` notes that
  `h3_lora.*` are helmstudio's, not upstream. A re-vendor re-applies the diff.
- **Quiet**: the merge prints one line (`h3 lora: merged 363 modules in 17.2 s`), not one per module, and exits 1
  with `h3: cannot merge LoRA into <name>` on a bad file (`child.run` shows the last `h3:` line, as today). The
  spike's own failures (`h3_lora.c` init, `read_f32`, `add_module`) `exit(2)` after an `h3 lora:` line, which
  `child.run` does not keep (its prefix is `h3: `), so the run would say only "exited with status 2": the build makes
  them return 0 to the hook, which fails with h3's own line. The spike's debug knobs go (`H3_LORA_DUMP`,
  `H3_LORA_VERBOSE`, `H3_LORA_ROUND`, `H3_LORA_INT8`, `H3_LORA_ALPHA`); `H3_LORA_STRENGTH` stays (open point 5).
- **native.py**: with `lora: turbo` the child gets `H3_LORA=<the add-on's file>` (`turbo`'s on FL2VA, `turbo_ref`'s
  on Ref2VA: the add-on the run check resolved, so the runner never picks by name) and `H3_ZERO_COPY_WEIGHTS=0`; argv
  is unchanged (steps from the param). With `none` it gets `H3_LORA=` (empty): `child_env` passes every `H3_*` of the
  runner's own environment, so a stray one never merges. `core.child.run` gains an `env` argument (merged over
  `child_env`, through `_Child`), the one core change.
- **Zero copy**: h3 maps the bf16 transformer shards copy-on-write; merging into them dirties those pages, which the
  spike saw stay resident and swap the Mac. So a Turbo run copies the weights (`H3_ZERO_COPY_WEIGHTS=0`). Copying
  only the merged tensors is a second, larger h3 patch (open point 4), and may save little: the int8-quantized
  linears' bf16 copies are freed after quantizing (`h3_dit.c` `quantize_block_*`), so what stays mapped is the bf16
  h3 keeps (adaLN and the like), which the LoRA largely touches too. Unmeasured.
- **Memory** (`peak_gb`): a copied load adds about 4.4 GB to the child's footprint (GOTCHAS: 20.7 → 25.0 GB at
  864 × 480 × 124); the merge itself adds nothing on top (the spike's copied-load peaks by `/usr/bin/time`: Turbo
  27.2 GB, base 6 steps 28.3, base 50 steps 28.9). So `peak_gb` gains `+ (4.5 if lora == 'turbo' else 0)` (a stage's
  own params are formula variables; `pack.compile_formula` allows `a if c else b` and `==`): about 31 GB at
  864 × 480 × 124 on the Generate key. The stage is shared, so Base fp8 also gets the +4.5 on its host-RAM budget
  (unmeasured there anyway; harmless). Ref2VA takes the same +4.5: its file is 0.16 GB smaller and the copied load is
  the same transformer; Ref2VA's own Turbo peak is unmeasured (references add their own encode). Both measured with
  the runner's own measure in the build (one FL2VA, one Ref2VA), recorded in GOTCHAS.
- **Load time**: the CPU merge adds about 17 s per run (h3 is a fresh process each run; nothing is cached); about the
  same with the ref2v file (a slightly smaller file, unmeasured). Shown in the Encode chip.
  ponytail: CPU sgemm per tensor; a Metal merge if it ever matters (merged files on disk are a
  no-go: the spike's route B).

**C5. NVIDIA (sgl.py)**:
- SGLang 0.5.23's `MiniMaxH3Pipeline` is a `LoRAPipeline`: with `lora: turbo`, `argv` adds `--lora-path <the add-on's
  file>` to the run's server for its partition (fl2va gets `turbo`'s file, ref2va `turbo_ref`'s: one server per run,
  so no `/set_lora`). Checked in
  the v0.5.23 source (2026-11-22): `--lora-path` is a server arg; the default `--lora-merge-mode auto` keeps an
  adapter unmerged when a merge would round away over half of it (its comment names MiniMax-H3 Turbo: 75 %), so it
  runs dynamically on the FP8 weights with no extra flag, at some cost per step (SGLang's note: up to +50 % on small
  models; the VM render measures it); the pruned Comfy-Org DiT is fine: `prepare_lora_adapter` projects a released
  checkpoint's AdaLN LoRA onto the pruned coordinates; `guidance_scale` is fixed at 1 (no CFG, as the recipe). It
  refuses an AdaLN LoRA only with an AdaLN cache (`--minimax-h3-adaln-cache-path`), which sgl.py never sets.
- The request carries `num_inference_steps` 8; `flow_shift` / `audio_flow_shift` only when not 12 / 3 (A5's rule),
  so a Turbo run sends none. Unverified on the VM, like every SGLang path: one render when the VM is next on (FL2VA;
  a Ref2VA render too if time allows).

**C6. Tests** (lean):
- **Pack unit** (no GPU): the Turbo preset's argv and child env (`H3_LORA`, `H3_ZERO_COPY_WEIGHTS=0`, `--steps 8`),
  none without it; with an image reference `H3_LORA` is the ref2v file; `sgl.argv` has `--lora-path` only with
  `turbo`, the ref2v file on ref2va; the peak formula with and without.
- **Contract**: `check.py` refuses a param `addons` value not in `enum`, a component that is not an add-on, and an
  `addons_with` input the node lacks; a dispatch fixture (`place.addon-param.json`): `lora: turbo` on a host without
  Turbo LoRA → the reason, `none` → eligible; with `references` wired on a host with only Turbo LoRA → the Turbo Ref
  LoRA reason, on one with only Turbo Ref LoRA → eligible; the contract suite creates a Turbo run set to that host →
  422 with the text.
- **Web**: one vitest over the greyed option and preset from the hosts' `addons`, without and with a reference
  (beside `run-on.test.ts`).
- **Golden** (Mac): one T2VA at the Turbo preset, 512 × 512 × 22, 8 steps (T2VA and the merge repeat bit for bit,
  so it compares hashes as the other T2VA golden); skipped where the add-on is missing.
- **VM** (manual): one Turbo T2VA at 864 × 480 × 124 when the VM is next on: s/step, VRAM, a look beside the Mac's.
- Board parity for frames 10-13 and the Models row's two cards.

**C7. Ref2VA check** (first in the build, once the owner has downloaded Turbo Ref LoRA in Models): open the ref2v file
(its key layout must map with the FL2V key map; a new map is a C5 change), then one Mac render of H3 References (an
image reference, 864 × 480 × 124, seed 42) at Turbo with the ref2v file vs Base 50 steps, graded like grid8. If it
holds, Turbo goes in all three workflows' presets. If not, Turbo is left out of workflow 3's presets (data only) and
the owner decides on the add-on; the FL2V file is not tried for References (owner, 24.12 point 1).

**C8. Phases and effort**

| # | Phase | Hours |
|---|---|---|
| 0 | Boards (frames 10-13, the Models row's two cards), approved by the owner | done (v70 adds frame 13 and the ref2v card) |
| C0 | The ref2v file's keys and the Ref2VA render (C7) | 1-2 |
| C1 | Contract: param `addons` and `addons_with`, run check 2b, plan reason, check.py, fixtures, README | 4-5 |
| C2 | Studio: the run check and plan reason for param add-ons, swapped by a wired input | 2-3 |
| C3 | Web: greyed option and preset by the add-on the form needs, Run on reason, Generate blocked | 3-4 |
| C4 | Runner: the two add-ons, `lora` param, Turbo in the three workflows, child `env`, native env, `sgl.argv`, peak | 3-4 |
| C5 | h3.c patch (quiet log), VERSION / THIRD_PARTY / the diff, `make h3`, the golden, one memory measure, GOTCHAS | 3-4 |
| C6 | The VM render | 1-2 |
| | **C total** | **about 17-24** |

Order: after the 1.36 add-ons land and after A1 (Turbo sits among A1's presets). C0 first; C1 before C2-C4.

### 24.12 Open points for the owner (C)

**Decided by the owner, 2026-11-22 (one by one); design approved for build:**
1. References: **add the ref2v 8-step file** (its own add-on, +1.4 GB), not the FL2V file. The plan and boards
   gain it before the build.
2. Turbo picked without the add-on: **block Generate** with the reason.
3. Preset order **by time**: Draft · Turbo · Fast · Default · Reference (swap Turbo and Fast if Fast measures quicker).
4. **Copy every weight** (`H3_ZERO_COPY_WEIGHTS=0`, +4.4 GB), no second h3 patch.
5. Strength **fixed at 1**.
6. **Keep both** Draft and Fast.

The options as put to the owner (kept for the record; point 1's recommendation was not taken):

1. **References**: use the FL2V file for Ref2VA if C7's render holds (recommended, the authors' word, one file), else
   add the ref2v 8-step v1.1 file (+1.4 GB) or leave Turbo off References.
2. **A form holding `lora: turbo` without the add-on**: block Generate with the reason (recommended) or drop to `none`
   silently with a note.
3. **Preset order**: Draft · Turbo · Fast · Default · Reference, by time (recommended), or Turbo last. By time is
   an estimate: Fast (20 steps, 45 layers, reuse 2, token reduction) is about 7.5 full passes, Turbo 8 plus the
   17 s merge, so the two may be close; the build measures Fast once and swaps them if Fast is quicker. The templates
   still open on Default (Turbo is greyed where it is missing).
4. **Zero copy**: a Turbo run copies every transformer weight (`H3_ZERO_COPY_WEIGHTS=0`, +4.4 GB, no new h3 code;
   recommended) or a second h3 patch that copies only the merged tensors.
5. **Strength**: fixed at 1 (recommended; the authors' value) or a Strength param (0-1.5) under LoRA.
6. **Draft and Fast**: keep both (recommended: no add-on, every host) though Turbo is far sharper for 1.2× the time
   of today's 6-step draft and may be about as quick as Fast.
