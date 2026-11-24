# helmstudio plan — Thumbnails: small pictures in lists, the full file only where it is viewed

[← Plan index](../README.md)

Status: **decided by the owner (2026-11-22), built (2026-11-22)**. API **1.48.0** (the next free number when built:
main was at 1.47.0; plan 23 reserved 1.46 and is unbuilt; numbers below that say 1.45 mean 1.48).

The owner's ask, 2026-11-22: "thumbnail support so the loading image in gallery view or elsewhere is memory and
network efficient, full size on view area".

Boards: none must change. Every list already draws a fixed-shape tile (`aspect-[16/10]`, `object-cover`) and every
viewer already draws the full file; this plan only changes which bytes fill the tile. One optional note on the
[Main](../mock/Main.dc.html) board's viewer ("shows the thumbnail at once, the full image when it arrives") if the owner
takes open point 7; no board is edited by this plan.

Facts come from a read of the code on 2026-11-22: `runner_service/consumer.py` (`output`), `core/nodes.py`
(`output`), `core/ask.py` (the judge's own thumbnails), `messaging/media.py` (frame and export jobs),
`studio_service/api/gallery` (`record.go`, `upload.go`, `imports.go`, `service.go` `TakeDTO`, `sweep.go`,
`model/model.go` `ThumbKey`), `api/studio` (`media_jobs.go` frames and exports, `library.go`, `memory.go`,
`batches.go`, `pipeline_rows.go`, `pipeline_review.go`, `decisions.go`, `timelines.go`, `runs.go`, `voices.go`,
`model/model.go`), `files_service/api/file/controller.go`, `cli/import.go`, `web_service` (`components/media.tsx`
`Thumb` and `Media`, `lib/media.ts` `thumbOf`, `lib/optimise.ts`, the gallery, Create, refs, source, pickers,
pipelines, timeline), `contracts/openapi.yaml` (`Take`, `RunOutput`, uploads), `contracts/messages/`
(`evt.run.output`, `cmd.media.job`), `contracts/README.md` (Object store, Uploads, Optimised uploads), and a
read-only look at the dev stack's `takes` table and six of its largest images.

## 26. Thumbnails

### 26.1 What the code does today

- **One key per picture.** `gallery/model.ThumbKey` is `coalesce(t.video->'poster'->>'key', t.key)`: an image take's
  thumbnail is the image itself (API 1.2, "the original serves as its own thumbnail"), a video's is its poster (frame 0
  as a PNG at the video's full size), an audio take's is the file (the web draws a sound tile and never loads it).
  `TakeDTO` signs the same key twice for `url` and `thumb_url`. Five more queries copy the same `coalesce(...)` inline
  instead of using `ThumbKey` (`library.go` from-take thumbs, `memory.go`, `studio/model.go` batch take keys,
  `pipeline_review.go`, `pipeline_rows.go`); `library.go` signs each look/element image's key as its own `thumb_url`
  (`libraryCols` builds the images with `t.key` only), and `lora/service.go` `thumbs` signs the oldest image take's
  `t.key` as a LoRA's `thumb_url`. `timelines.go` and the gallery's parents (`TakeRef`) already use `ThumbKey`; the
  judge's candidates (`decisions.go`) go through `pipeline_rows.go` `takeKeys`.
- **`RunOutput` has no `thumb_url`.** Create's strip draws a run's outputs with `Thumb take={out}`, and
  `thumbOf` falls back to `url` for an image: the full PNG. A `RunOutput` carries its `take_id` (set by `Record`).
- **The web already asks for `thumb_url` in lists.** `Thumb` (gallery grid, Create strip, compare strip, graph,
  pipelines execution and studio) and the tiles in refs, source, prompt mentions, elements, looks, batch, review,
  memory, run table and the timeline clips (CSS `background-image`) all use `thumb_url`. `Thumb` and the run tile
  already set `loading="lazy" decoding="async"`; the visual picker sets `loading="lazy"`; the rest set neither. The
  viewers (`Media`, compare, the full screen, the source player) use `url`, as they should. Also on `thumb_url`
  and missed by a first read: the gallery picker (`gallery/picker.tsx`), the source's frame markers
  (`create/rows.tsx`), the LoRA pictures (`create/loras.tsx`, `models/loras.tsx`) and pipelines studio's tiles
  (`grid-cols-2` when not small: up to about 400 CSS px wide).
- **Two viewers draw `thumb_url` as the video poster.** `Media` (`components/media.tsx`) and compare's video
  (`create/compare.tsx`) set `poster={thumbOf(t).src}`, which is `thumb_url` for a take. Today that is the full
  poster; once `thumb_url` is a 512 px WebP the view area would show a soft poster until play. Only the source player
  uses `video.poster_url`.
- **So every list loads full-size files.** The dev stack today (read-only query, live ready takes): 158 Create images
  average 1.46 MB (max 18.2 MB), 202 pipeline images 0.78 MB, 105 video posters 0.14 MB (PNG, small because the test
  videos are small; a 1280×720 LTX poster is about 1.2 MB).
- **Who writes takes and their objects** (every path a thumbnail must cover):

  | Path | Where | Object key | Thumbnail today |
  |---|---|---|---|
  | Run output, image | runner `nodes.output` → `consumer.output` → `evt.run.output` → studio `gallery.Record` | `u/<o>/runs/<run>/<attempt>/<n>.png` | the PNG |
  | Run output, video | same; poster and last frame written beside it | `…/<n>.mp4`, `<n>.poster.png`, `<n>.last.png` | the poster PNG |
  | Run output, audio | same | `…/<n>.wav` | the file (never drawn) |
  | Browser upload | web `uploadFile` (`optimise.ts`: WebP 0.9 at most `image_max_side`) → files PUT → studio `uploads/complete` | `u/<o>/uploads/<take>.<png\|jpg\|webp>` | the upload |
  | CLI import | `cli/import.go` → studio imports (video: `poster_upload_url`) | `u/<o>/imports/…` | the file, a video's poster |
  | Frame take (Use last frame, a frame source) | studio `frameOf`: server `Copy` of the stored poster or last frame, else a `frame` media job on the runner | `u/<o>/frames/<take>.png`, `u/<o>/frames/<job>/frame.png` | the PNG |
  | Timeline export | `export` media job on the runner | `u/<o>/exports/<job>/export.mp4` + `.poster.png` + `.last.png` | the poster |
  | Voice clip | a voice keeps an audio take | — | the WAV (never drawn) |
  | Judge thumbnails | `core/ask.py`, for the model, not for people | `u/<o>/llm/thumbs/<sha256>.jpg` (1024 px JPEG) | — (kept as is) |

- **Signing and serving.** studio signs `GET /files/<key>?exp=&sig=` with the expiry rounded up to a whole day, so a
  link is stable for a day; files_service answers `Cache-Control: private, max-age=31536000, immutable` and the
  object's `ETag`, with Range. A thumbnail needs nothing new here: it is one more key under `u/<owner_id>/`.
- **Deletes.** `DELETE /v1/studio/takes/{id}` is soft; `gallery.Sweeper` removes the objects an hour later (the key,
  a video's poster and last frame, the judge thumbnails no live decision shows), refusing any key outside
  `u/<owner_id>/`, then the rows. Session delete leaves takes; nothing else removes take objects.
- **Runner write policy** (README, Object store): `PutObject` only on `u/*/runs/*`, `u/*/exports/*`, `u/*/frames/*`,
  `u/*/llm/thumbs/*` and the packs' looks and voices; the runner also writes only under its command's
  `output_prefix`.
- **Media jobs** (`media_jobs.go`): one durable per host env with `max_ack_pending 1`, so jobs on an env run one
  after another; `insertMediaJob` refuses an owner's fifth open job (`MaxOpenMediaJobs = 4`, "unfinished exports or
  frames"); a job's inputs are `{key, kind}` only (no take ids); the projector checks every result key is inside the
  job's prefix. The runner holds a PIL image only in `nodes.output`; `consumer.output` receives encoded PNG bytes.
- **Found on the way, not caused by this plan:** a `frame` job at `last` stores the same object as the frame take's
  `key` and as the video's `video.last` (`media_jobs.go`, `UPDATE takes SET video = jsonb_set(video, '{last}', …)`),
  while the sweep's shared-key check compares only `t.key`: sweeping either take deletes the other's file. Open
  point 9.

### 26.2 What comes in, what does not

In: one real thumbnail per image and video take, made where the take is made when that is cheap, else by a runner
media job; `thumb_url` signs it; a backfill for existing takes; the sweep removes it; `RunOutput.thumb_url`; every
list uses `thumb_url` with lazy, async decoding. Viewers, the lightbox, compare, export and every model input keep
`url` (the full file).

Not in: several sizes and `srcset` (open point 2), audio waveforms (open point 6), animated or scrubbing video
previews, thumbnails of starter looks and voices (the pack files are already small), changing the judge's own
thumbnails, a CDN.

### 26.3 The thumbnail

- **Size: one size, short side 512 px, long side at most 1024 px, never upscaled.** Lists draw 16:10 tiles that
  cover their box (`object-cover`); the gallery grid is `minmax(190px, 1fr)`, about 190–260 CSS px wide, so 380–520
  device px on a 2× screen. A long-side rule (512 long) would leave a portrait or square take soft in a landscape
  tile; a short-side rule fills any tile up to 256 CSS px wide at 2×. The 1024 cap keeps a panorama small. The
  biggest list tiles (pipelines studio's two columns, about 400 CSS px) are a little soft at 2×; accepted.
- **No thumbnail when it saves nothing**: when the original's short side is at most 360 px (2026-11-23; was 512), or the WebP is not
  smaller than the original, none is stored and the original stays the thumbnail (no wasted object, nothing to sweep).
- **Format: WebP quality 80** (PIL `method=4`), alpha kept. JPEG quality 82 only where WebP cannot be written (a
  browser without a WebP encoder, if open point 4 picks the browser). Measured on six dev takes (PIL, Lanczos):

  | Original | Original file | Decoded (RGBA) | WebP 512 long | JPEG 512 long | WebP 256 long |
  |---|---|---|---|---|---|
  | 5344×3008 PNG | 19.1 MB | 64 MB | 30 KB | 40 KB | 10 KB |
  | 3808×2144 PNG | 13.5 MB | 33 MB | 44 KB | 50 KB | 13 KB |
  | 3808×2144 PNG | 11.3 MB | 33 MB | 31 KB | 39 KB | 10 KB |
  | 2048×2048 PNG | 7.9 MB | 17 MB | 64 KB | 77 KB | 18 KB |
  | 1024×1024 PNG | 2.6 MB | 4 MB | 74 KB | 86 KB | 17 KB |
  | 1024×1024 PNG | 2.4 MB | 4 MB | 61 KB | 74 KB | 15 KB |

  The short-side-512 rule has up to 1.8× the pixels of "512 long" for a 16:9 take: about 55–130 KB each. WebP is
  15–25 % smaller than JPEG at the same look, and every browser the app supports decodes it.
- **Estimate for one gallery screen of 60 tiles** (dev mix, about 1.1 MB average original): network ~66 MB today →
  ~5 MB; decoded memory ~60 × 4–64 MB (one 5344×3008 take alone is 64 MB) → ~60 × 1.9 MB (910×512 RGBA) ≈ 110 MB
  worst case, and lazy loading only decodes what is on screen. A video tile today loads a full-size PNG poster
  (~1.2 MB at 1280×720) → ~60 KB.
- **Made from**: an image take: the image upright (EXIF applied, through `images.decode`, which also refuses more
  than `MAX_PIXELS` on the header before decoding; a JPEG uses PIL `draft` to decode at a reduced scale); a video: its
  poster (frame 0; an export's poster is a tenth in, as the export makes it; an upload's or import's is the client's);
  a video with no poster: none; an audio take: none (open point 6).

### 26.4 Where each thumbnail is made

| Path | Maker | Key | How |
|---|---|---|---|
| Run output, image or video | runner `nodes.output`, which holds the PIL image or poster (`consumer.output` only gets PNG bytes; decoding them again would cost up to a second and 64 MB for a large take) | `<output_prefix><n>.thumb.webp` | `nodes.output` passes `thumb=` WebP bytes beside `poster=` / `last=`; `consumer.output` PUTs it before the main object; `evt.run.output` carries `thumb {key, width, height, bytes}` |
| Export | runner `messaging/media.py` export, from the poster it already writes | `u/<o>/exports/<job>/export.thumb.webp` | same; the media result carries `thumb` |
| Frame job | runner `frame` job, from the PNG it writes | `u/<o>/frames/<job>/frame.thumb.webp` | same |
| Frame copy | studio `frameOf`, which already copies the poster or last frame | `u/<o>/frames/<take>.thumb.webp` | a run video's poster (frame 0) whose `thumb` exists: a server `Copy` of that thumbnail object, no decode; otherwise (the last frame, no video thumb yet): none yet, the thumbs job makes it |
| Upload | the thumbs job after `complete` (open point 4: or the browser beside the optimised upload) | `u/<o>/thumbs/<job>/<i>.webp` (`i` = the input's index) | see below |
| CLI import | the thumbs job after the import completes; the CLI does not change | same | see below |
| Existing takes | the backfill: the thumbs job | same | see below |

Keys sit beside the object the maker writes, not at `u/<owner_id>/thumbs/<take_id>-<size>.webp`: the runner does not
know the take id when it writes a run output (studio assigns it in `Record`), and the claim-check rule lets it write
only under its command's `output_prefix`. Only the thumbs job uses a new prefix, `u/<owner_id>/thumbs/<job_id>/`,
which needs one more `PutObject` prefix, `u/*/thumbs/*`, in the runner policy (`.tools/make-secrets`, README Object
store). With one size there is no size in the key.

**The thumbs media job** (one new kind of `cmd.media.job`, run by the runner's media env like `frame` and
`export`, Python with PIL and ffmpeg that it already has):

- studio's `Thumbs` loop (beside the take sweeper, as System) picks ready, live image and video takes whose
  `thumb` is null and whose `thumb_tries` < 3, newest first (built: so a new upload never waits behind the backfill), one owner at a time, at most 20 per job, and only when no
  thumbs job is queued or running (one at a time for the stack). Because each env's media durable has
  `max_ack_pending 1`, a frame or export queued behind a running thumbs job waits for it: 20 inputs keep that to about
  20–40 s (a large PNG takes about a second to fetch and decode). Takes whose key (or poster key) is outside
  `u/<owner_id>/`, and videos without a poster, are never picked.
- A thumbs job does not count toward `MaxOpenMediaJobs` (the count gains `AND kind <> 'thumbs'`), so a running
  backfill never makes a user's export or frame a 409. It has no endpoint of its own; `GET` and `cancel` by job id
  answer 404 for kind `thumbs`.
- `inputs`: each take's key (a video: its poster key) as `{key, kind: image}`, all under that owner's
  `u/<owner_id>/`; studio keeps the take ids in order in a new `media_jobs.take_ids uuid[]` (the message stays keys
  only); `output_prefix`: `u/<owner_id>/thumbs/<job_id>/`; the runner writes `<i>.webp` per input index and answers
  `result.thumbs [{index, key, width, height, bytes} | {index, error}]` (an input that needs no thumbnail, by the
  rule above: `{index, skip: true}`).
- The projector maps each index to `take_ids[i]`, stores `thumb` (only a key equal to `<prefix><i>.webp`), sets
  `thumb_tries = 3` for a skip and bumps it for an error; a take swept meanwhile leaves an object nobody points at:
  the projector deletes it at once.
- A thumbs job that ends failed, cancelled or lost bumps `thumb_tries` of every take it held, so an input that crashes
  the runner (or a host that keeps dying) is tried at most 3 times, never in a loop.
- **Resumable by design**: the state is `takes.thumb` itself. A job lost with its host is ended by the media job
  sweep as today and the takes are picked again; a redelivery starts over and rewrites the same keys; a restart of
  studio just starts the loop again. No host with a media env: nothing is queued, the fallback stays.
- New takes from uploads, imports and last-frame copies reach the same loop within seconds; there is no separate
  "on complete" trigger.
- **The backfill on the dev data** is this loop meeting the existing takes (about 465 image and video takes today:
  about 24 jobs). It is safe by construction: it reads originals and posters and never writes, moves or deletes them;
  it writes only new keys under `u/<owner_id>/thumbs/<job_id>/`, the owner of the take it reads; it updates only
  `takes.thumb` and `thumb_tries` of rows whose `thumb` is null; running it twice, or after a crash, changes nothing
  already done. Before the first run on the dev stack: a `pg_dump` of `takes` (the only table it changes) and a count
  of takes with `thumb IS NULL`; after it, the same count and a look at a few tiles. Undo is
  `UPDATE takes SET thumb = NULL, thumb_tries = 0` (the objects can stay or be listed and removed under `thumbs/`).

### 26.5 Storage and the contract (1.48.0)

- **studio migration** (next free number when built): `takes.thumb jsonb` (`{key, width, height, bytes}`, null until
  made), `takes.thumb_tries smallint NOT NULL DEFAULT 0`, `media_jobs.take_ids uuid[]` (thumbs only);
  `media_jobs.kind` gains `thumbs`. No `run_outputs` column: a run output's thumbnail is its take's. No index beyond a
  partial one for the loop: `(created_at) WHERE thumb IS NULL AND deleted_at IS NULL AND ready`.
- **`ThumbKey`** becomes `coalesce(t.thumb->>'key', t.video->'poster'->>'key', t.key)`, and the six places that sign
  their own key use it: the five inline copies and `lora/service.go` `thumbs` (today `t.key`); `TakeDTO` signs
  `row.Thumb.Key` when present. `libraryCols` adds `'thumb', <ThumbKey>` to each image's `jsonb_build_object`, and
  `itemDTO` signs it as the image's `thumb_url` (its `url` stays `t.key`).
- **`RunOutput.thumb_url`**: `runs.go` signs the `ThumbKey` of the output's take (`take_id`, the caller's, not
  deleted), so a backfilled take's output gets it too; no take: absent.
- **The fallback** while a thumbnail is missing is exactly today's: the original image, the full poster. No client
  must know whether a thumbnail exists.
- **openapi** (1.45.0):
  - `Take.thumb_url`: "from 1.45 a WebP of the image or the poster, short side 512 px (never upscaled), long side at
    most 1024; until it is made (or when it cannot be) the original image or the poster as before; an audio take:
    the file". Same note on `TakeRef`, `TimelineTake`, `ReviewCandidate`, `LibraryImage.thumb_url`,
    `LibraryItem.thumb_url`, `BatchRow`/step attempt `thumb_url`, LoRA `thumb_url`; `VideoInfo.poster_url` stays the
    full-size poster.
  - `RunOutput.thumb_url` (new, optional, signed): its take's `thumb_url`; absent without a take (the web then uses
    `url` as now).
  - `uploads/{take_id}/complete`: drop "the original serves as its own thumbnail"; say the thumbs job makes it.
  - No size fields: the take's `width` / `height` already give the shape, and every tile has a fixed aspect.
- **messages**: `evt.run.output` `data.thumb {key, width, height, bytes}` (optional; key
  `<output_prefix><index>.thumb.webp`); `cmd.media.job` kind `thumbs` with its inputs (kind `image`, at most 20) and
  `output_prefix`; `evt.media.status` `result.thumb` (export, frame) and `result.thumbs[]` (thumbs, by input index;
  the `result.key` pattern gains nothing, a thumbs result has no `key`). `contracts/check.py` learns the new key
  shapes.
- **README**: Object store (the `u/*/thumbs/*` prefix), Uploads, Video (posters), Media jobs (the thumbs job).

### 26.6 Deletes and sweeps

- The take sweep adds `t.thumb->>'key'` to the keys it removes, with the same `u/<owner_id>/` check (a key outside
  it leaves the take alone and logs, as today). A thumbnail is never shared between takes (a frame copy makes its own
  object), so no "another live take points at it" check is needed for it.
- A run that fails after writing a thumbnail leaves it under `runs/…` like its other partial objects (today's
  behaviour for partial outputs, unchanged).
- A failed, cancelled or lost thumbs job may have written some of its objects: studio knows every key it could have
  written (`<prefix><i>.webp` for each index of `take_ids`), so ending it deletes those no take points at (a delete
  of a missing key is not an error). No leak, no listing.
- A run output's thumbnail is its take's object, so the take sweep removes it; `RunOutput.thumb_url` is absent once
  the take is gone (as its take is).
- Not in this plan, but next to it (open point 9): the frame job's `last` object shared by the frame take and the
  video (26.1). The thumbnails do not make it worse: each take's thumbnail is its own object.

### 26.7 Web

- Lists keep `thumb_url` and gain `loading="lazy" decoding="async"` everywhere they lack it (refs, source, prompt
  mentions, elements, looks, batch, review, memory, run table, the source's frame markers, the LoRA pictures; the
  visual picker gains `decoding`). Tiles already
  have a fixed aspect, so no `width` / `height` attributes are needed to stop layout shift; images drawn at their own
  shape (none in lists today) would take the take's `width` / `height`.
- `thumbOf` prefers `RunOutput.thumb_url` before `url`, so Create's strip stops loading full PNGs.
- Viewers, the full screen, compare, the source player, export and every model input keep `url`. Every video
  player's `poster` is `video.poster_url` (full size: it is the view area): `Media` and compare change from
  `thumbOf(t).src` to it (26.1), or the view area would show the 512 px thumbnail.
- Optional (open point 7): the viewer paints the thumbnail (already in the cache) under the full image while it
  loads.
- No change to `optimise.ts` unless open point 4 picks the browser.

### 26.8 Services and rules

- **runner_service**: a `thumb(img) -> (bytes, w, h)` helper in `core/images.py` (beside `decode`), used by
  `nodes.output` (on the PIL image and poster it holds), the export and frame jobs and the thumbs job, run off the
  event loop (`asyncio.to_thread`) in the media worker; `consumer.output` only PUTs the bytes it is given. Pillow is
  already pinned (no new dependency).
- **studio_service**: migration, `Record` and the media projector store `thumb`, `ThumbKey` and the six queries
  (five inline copies and LoRA thumbs), `TakeDTO`, `RunOutput` (from its take), library images, the `Thumbs` loop,
  `MaxOpenMediaJobs` leaving thumbs out, the media job `GET` / `cancel` 404 for thumbs, the sweep, ending a thumbs
  job deleting its unused keys, `frameOf` copying the video's thumb.
- **files_service**: none (thumbnails are ordinary `u/` keys; caching is already immutable).
- **web_service**: the attributes above, `thumbOf`, and the video `poster` of `Media` and compare. **cli**: none. **pipelines_service**: none (it signs nothing;
  studio fills `thumb_url`). **deploy**: the runner policy prefix in `.tools/make-secrets`.
- Owner scoping as everywhere: the loop runs as System but every key it sends and every key it accepts is checked to
  be under the take owner's `u/<owner_id>/`; another user's take is never touched. No bytes or URLs in messages, and
  no thumbnail key ever reaches a client: only signed `thumb_url`s.

### 26.9 Tests

- **runner**: `thumb()` sizes (short side 512, cap 1024, no upscale, a portrait, a panorama, alpha kept, EXIF
  rotated, a small original skipped, a JPEG drafted); `nodes.output` passes `thumb=` and `consumer.output` writes
  `<n>.thumb.webp` before the object and reports it; the thumbs job writes only `<prefix><i>.webp`, reports a broken
  input as an error and goes on, never writes an input key; a key outside the owner is refused (dead letter).
- **studio** (Go DB tests on `.tools/test-db`): `Record` stores `thumb`; `TakeDTO` signs it, falls back without it;
  each list endpoint returns the thumb key's link (gallery, refs, library, batches, review, rows, decisions,
  memory, timeline); the loop picks only ready live takes without a thumb, one owner per job, stops at 3 tries, never
  two jobs, skips keys outside the owner and posterless videos; a thumbs job does not count toward
  `MaxOpenMediaJobs` and its id is 404 on `GET` / `cancel`; the projector maps results by index and refuses a key
  other than `<prefix><i>.webp`; a failed or lost job bumps every take's tries and deletes its keys; a swept take's
  late thumbnail is deleted; the sweep removes the thumbnail; a frame copy copies the thumbnail; LoRA and library
  image `thumb_url`s use the thumbnail; `RunOutput.thumb_url` follows its take.
- **contract suite** (`helmstudio-test`, fake mode): a run's take has a `thumb_url` that differs from `url` and
  serves `image/webp`; an upload's take gets one after the job; `RunOutput.thumb_url`.
- **web** (vitest): `thumbOf` prefers `RunOutput.thumb_url`; lists render `loading="lazy"`; `Media` and compare
  set the video `poster` from `video.poster_url`, not `thumb_url`.
- **browser pass**: the gallery network panel shows WebP tiles of tens of KB; the viewer loads the full file.

### 26.10 Phases

1. **Design**: this plan, decided by the owner (done when decided).
2. **Contract** 1.45.0, messages, README, `check.py` (2 h).
3. **runner**: `thumb()`, run outputs, export and frame results, the thumbs job (4 h).
4. **studio**: migration, `Record` and projector, `ThumbKey` everywhere, `RunOutput`, library, LoRA, the loop and
   its job rules, sweep, frame copy (7 h).
5. **web**: attributes, `thumbOf`, the optional viewer placeholder (2 h).
6. **deploy**: runner policy prefix; on the dev stack a `pg_dump` of `takes`, then the backfill watched to the end
   (counts before and after, 26.4) (1 h).
7. **Gates**: each service's `make test lint`, `make contracts-check`, `make test` on `helmstudio-test`, the browser
   pass (2 h).

About 18 h. Phases 3–4 for run outputs alone (no thumbs job) ship most of the win for new work in about 7 h; the
backfill, uploads and imports follow. The web's poster change ships with the first phase that makes a video
thumbnail.

### 26.10a As built (2026-11-22)

- API 1.48.0. Runners advertise `media.thumbs: true` in the hosts key and studio sends thumbs jobs only to them (an
  older runner, such as a native Mac runner not yet restarted, would fail every job and spend each take's tries).
- The loop picks the **newest** takes first, not the oldest: a new upload never waits behind a backfill.
- No `width` / `height` attributes on list images (26.7: every tile has a fixed aspect).
- Dev stack backfill (studio, runner and web rebuilt): 480 live image and video takes, 45 thumbs jobs, none failed;
  231 got a thumbnail, 249 needed none (short side at most 512 px); no take over 512 px is left without one. Image
  originals average 1.78 MB, their thumbnails 32 KB (largest 119 KB). A `pg_dump` of `takes` was taken before.
- Browser pass (dev stack, studio rebuilt with the review fixes): Gallery, the gallery picker, the Create strip and
  the Timeline draw `thumb_url`; tiles are lazy WebP of 7–119 KB; the details panel, the full screen and compare load
  the original, the details panel painting the thumbnail under it until it arrives. One Gallery page (60 takes, 35
  picture tiles, 21 with a thumbnail) fell from 91.8 MB of originals to 5.3 MB. No web fix needed. Noted: 864×480
  PNG video posters and uploads (short side 480) keep their ~250–400 KB original as the tile under the "short side at
  most 512" rule; a WebP would be ~30 KB (a rule change, not made).
- Left: a remote GPU host's S3 identity needs `make gpu-host-reload` to get the
  `u/*/thumbs/*` prefix (local runners use the admin key); the native Mac runners advertise `media.thumbs` after their
  next restart.
- **Owner, 2026-11-23 ("Lower to 360 px")**: a thumbnail once the short side is over 360 px (was 512); size rules
  unchanged (an 864×480 still gives an 864×480 WebP). Runner `THUMB_MIN`; studio migration 00032 gives the skipped
  takes with a short side of 361–512 their tries back, so the loop picks them again on any stack.

### 26.11 Open points for the owner

**Decided by the owner, 2026-11-22 (one by one, all the recommended options):** 1. short side 512, long side at
most 1024. 2. One size. 3. WebP q80. 4. Uploads and imports by the runner's thumbs job. 5. Automatic backfill, 20 at
a time. 6. Audio keeps the CSS sound tile. 7. The viewer shows the thumbnail while the full image loads. 8. Video
tiles use the poster's thumbnail. 9. Fix the shared frame-key sweep bug in this build.


Each with the recommended option first.

1. **Size rule**: short side 512, long side at most 1024 (crisp in any tile up to 256 CSS px at 2×) · 512 on the
   long side (smaller files, soft portraits in landscape tiles) · 640 on the long side.
2. **How many sizes**: one (simplest; tiles are all about the same size) · two, 256 and 512, with `srcset` for the
   dense pickers and refs chips (a second object per take for ~10–40 KB saved per tile).
3. **Format**: WebP q80 · JPEG q82 (a little larger, alpha lost) · AVIF (smaller, slow to encode, uneven support).
4. **Who makes upload thumbnails**: the runner's thumbs job (one maker; no contract or browser change; needs a media
   host, the upload shows the optimised original meanwhile) · the browser beside the optimised upload (instant, but a
   second PUT, a `thumb` field on `UploadCreate`, a `thumb_upload_url` and a server check).
5. **Backfill**: automatic, one thumbs job of 50 at a time until every existing take has one · only on an admin's
   button · none (old takes keep full-size tiles).
6. **Audio takes**: keep the CSS sound tile (no thumbnail) · a waveform PNG made by the runner (another job kind and
   a board change).
7. **Viewer placeholder**: show the thumbnail at once under the full image while it loads (one small web change, an
   optional board note) · nothing until the full image arrives, as today.
8. **Video tiles**: the poster's thumbnail · also a short muted preview clip on hover (out of this plan; bigger).
9. **The shared last-frame object** (found in review, 26.1; not caused by thumbnails): fix it separately before this
   plan, by having the frame job's `last` result copied to `frames/<take>.png` for the frame take as `frameOf`
   already does (one copy, small) · make the sweep also check `video->'last'->>'key'` against other live takes ·
   leave it (deleting a video can delete its last-frame take's file, and the other way round).
