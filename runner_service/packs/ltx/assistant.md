# LTX-2.5: what the Assistant should know

## Card

LTX-2.5 (Lightricks, 22B) makes short video clips with matching sound in one pass. Modes: Text to video, Image to
video (Frames: up to 8 pinned images), Retake (redo a span of a clip), Extend (add seconds after or before) and
Audio to video (the picture follows a sound). Pick it over MiniMax H3 for speed (a 2 s draft in about half a minute
on a Mac), for keyframes at set times, timed prompt beats, fixing part of a clip, and video that follows a voice
line. Pick H3 when quality, close character match from reference images, or an exact first-to-last-frame move matter.

## Good at / weak at

Tags: [pack] = this pack's files, [card] = Lightricks' model card or docs, [takes] = the owner's takes (none read
yet: the Gallery was not readable, so there are no [takes] claims).

Good at:
- Fast drafts: Distilled runs 8 steps without guidance, about 10x faster than Dev. [pack]
- Picture and sound generated together; the picture is the same with Audio off. [pack] [card]
- Image to video with up to 8 keyframe images at chosen times, each with a strength and a binding. [pack]
- Timing inside one clip: up to 8 prompt beats steer picture and sound in order (Prompt Relay). [pack]
- Fixing a clip without starting over: Retake a span, Extend after (or before on Dev). [pack]
- Audio to video: the clip follows a source sound; the output keeps that sound exactly. [pack]
- Multishot: LTX-2.5 can make 2 to 4 connected shots in one clip, keeping characters, place, light and voice across
  cuts. [card]
- Complex prompts with several characters, camera moves and lighting (the Gemma 4 12B text encoder). [card]
- Auto length: a duration head predicts 1 to 20 s from the prompt. [pack] [card]
- LoRAs: up to 8 rows (Mac only); most LoRAs made for LTX-2.3 work on 2.5, a few do not: test one first. [pack] [card]

Weak at:
- Not a lip-sync model: in Audio to video the mouth follows the sound only loosely. [pack]
- Elements are prompt-only (no reference images): a character holds only through a keyframe image. [pack: plugins/cinematic-filmmaker]
- One-stage is softer, and its clip is half the canvas. [pack]
- Crowded frames, chaotic motion and mixed light sources in one shot give artifacts. [card]
- On-screen text is not reliably spelled or kept steady; add critical text in the edit. [card]
- Frame rates far from 24 drift. [pack]
- Prompt following depends heavily on how the prompt is written. [card]
- Cartoon style from text alone holds less well than animating a keyframe. [pack: plugins/animator recipe note]
- Dev is about 10x slower than Distilled (Retake 262 s against 31 s at 448x256x49). [pack]

## Modes and tasks

Workflows (id, title):
- `ltx-text-to-video`, "LTX-2.5 Text to video": Distilled only (distilled on Mac, distilled-fp8 on NVIDIA). [pack]
- `ltx-image-to-video`, "LTX-2.5 Image to video" (action Animate): as text to video plus `keyframes.images`. [pack]
- `ltx-retake`, "LTX-2.5 Retake" (Retake a section): a source video + `retake.span`; Distilled or Dev. [pack]
- `ltx-extend`, "LTX-2.5 Extend": a source video + `extend.seconds` and `extend.direction`; Distilled or Dev. [pack]
- `ltx-audio-to-video`, "LTX-2.5 Audio to video": a sound (`audio.source`) sets the length; optional keyframes
  (`keyframes.bypass` true by default: set it false, then add a frame at 0 s); Distilled or Dev. [pack]

How to choose:
- No input media: Text to video. A keyframe or several poses: Image to video. [pack]
- A good clip with one bad stretch: Retake that span. Too short: Extend. Before the clip: Extend with Dev. [pack]
- A talking shot from a voice line, or motion to a beat: Audio to video with the keyframe at 0 s. [pack]
- Recipe (Quality section of text, image and audio to video): Two-stage (default), One-stage (quick look, t2v / i2v
  only), Detail (final close-up). [pack]
- Variant: Distilled for nearly everything; Dev only for Extend before, or when guided steps / CFG are wanted on
  Retake, Extend or Audio to video (Audio to video on Dev needs the Distilled LoRA add-on). [pack]

## Settings that matter

- `model.variant`: distilled | dev (Mac), distilled-fp8 | dev-int8 (NVIDIA). Distilled ~10x faster. [pack]
- `model.precision`: int8 (default, transformer ~19 GB), int4 (low memory), bf16 (~38 GB transformer, the Quality
  preset). Retake, Extend and Audio to video allow int8 / int4 only. [pack]
- `model.compute`: float32 (default) | float16: 10-20 % faster on a Mac, the picture changes slightly; NVIDIA ignores
  it. [pack]
- `canvas.width` / `canvas.height`: 128-1920, step 64, default 704 x 448 (about 0.1-2.1 MP). Stage 1 renders at
  half and stage 2 refines at full size. Larger costs time and much more decode memory (see Speed). [pack]
- `duration.frames`: 9-321 on the 8k+1 grid (25 = 1 s, 49 = 2 s default, 73 = 3 s, 97 = 4 s, 121 = 5 s at 24 fps);
  Auto lets the model pick 1-20 s and reports it. The card's examples use 121 frames. [pack] [card]
- `duration.fps`: 1-60, default 24. fps is a model input: it changes the motion, not only playback. Keep 24. [pack]
- Recipe (sets `upscale.bypass`, `sample2.bypass`, `detail.bypass`, `sample1.detail`, `sample2.detail`):
  - Two-stage: upscale and sample2 on, detail off. Normal work. [pack]
  - One-stage: upscale and sample2 bypassed: faster, softer, the clip is half the canvas. [pack]
  - Detail: detail on, both `detail` flags true: sharper hair, edges and texture, about 10-20 % slower (measured; the UI says about a third); Distilled
    and Mac only; needs the Detailer add-on. `detail.strength` 0-2 (template 0.5). [pack]
- `sample1.steps` / `sample2.steps`: fixed at 8 and 3 (the distilled schedules end at zero only in full). Never
  change them. [pack]
- `encode.beats`: up to 8 rows {text 1-500 chars, seconds 0-20}. Seconds is the beat's length, not its start; 0
  shares what is left equally. The main prompt holds for the whole clip. Mac only (NVIDIA refuses beats). [pack]
- `encode.negative` (t2v, i2v): up to 500 chars, applied with NAG, about 10 % slower, Mac only; empty is off. On
  Retake / Extend / Audio to video (`encode` is encode-guided) it feeds Dev's CFG; Distilled ignores it. [pack]
- `keyframes.images`: up to 8 rows {image, seconds 0-320, strength 0-1 (1 holds the image), binding auto | replace |
  guide}. Auto: 0 s replaces the start, later frames guide. Replace pins exactly but needs a frame on the latent grid
  (0 or 1 + 8k frames). NVIDIA: auto only. [pack]
- `decode.audio`: true | false. Off writes a silent MP4 (for a shot that will sit under a voice line). [pack]
- `lora.rows`: up to 8, strength 0-2 (default 1), mode adapter (strength change needs no reload) or bake. Mac
  only. [pack]
- Retake: `retake.span` (snapped to 8-frame steps), `retake.audio` (false keeps the source sound). [pack]
- Extend: `extend.seconds` 0.1-10 (default 2, rounded up to 8-frame steps), `extend.direction` after | before
  (before: Dev only). Distilled extend reads the clip's last 25 frames as context; sides on the 64 px grid. [pack]
- Dev only (Retake, Extend, `sample1` of Audio to video): `steps` 1-60 (30), `cfg` 1-10 (3), `stg` 0-3 (1, 0 off).
  Each step runs the model 4 times over the whole clip, so time grows with steps and clip length. [pack]
- Audio to video: `audio.source` up to 13.375 s (321 frames at 24 fps), trimmed by in / out; fps 24 only. [pack]

## Prompt style

- One flowing paragraph, present tense, about 4-8 descriptive sentences for a single shot. [card]
- Order: the shot (framing, genre), the scene (light, palette, textures, atmosphere), the action as a sequence, the
  characters, the camera move, then the sound. [card] Camera move first plainly is the plugins' house style. [pack]
- Every sentence has a verb that does something. Keep motion simple and plausible. [card]
- Camera: say how and when it moves relative to the subject, and what the frame looks like after. [card]
- Sound: describe ambience, effects, music; dialogue in quotation marks, with language or accent if needed. [card]
- Emotion through physical cues, not labels ("her shoulders drop", not "sad"). [card]
- Timing: with Auto length the clip is timed to the action as written; it adds no pauses you did not write. Write
  holds ("holds still, then ...") or set a length. [card] For exact timing use `encode.beats`. [pack]
- Multishot: 2-4 shots in one chronological paragraph; name each cut (hard cut, match cut), re-establish framing,
  reuse the same names, say whether the sound continues. No shot lists or numbered beats. [card]
- Image to video: describe one continuous take from the first frame unless you deliberately write a cut. [card]
- Do not paste prompts written for other models (tag syntax, shot lists, `<Picture n>`): rewrite as prose. [card]
- Names: @element handles are plain words to LTX (no reference images); describe the look in words too. [pack]
- The prompt and all beats together must fit 1024 tokens or the run is refused. [pack]

## Speed and memory

Mac (64 GB M-series, int8, mlx 0.32.2-0.32.4), measured [pack]; the t2v/i2v times and peaks on 0.32.4, the
Retake / Extend / Audio to video and Detail lines on 0.32.2:
- Text to video 448x256x49: 26-38 s cold, about 30 s for a re-roll with a new seed (no encode, no loads).
- Text to video 704x448x97: 62.7 s; process peak 39.8 GB.
- 448x256x49 process peak about 28 GB (t2v and i2v).
- Detail at 448x256x49: 32.4 s against Two-stage's 27.0 s; 512x320x57: 41.7 s against 38.7 s.
- Distilled: Retake 30.7 s, Extend (+2 s) 34.2 s, Audio to video 32.3 s (all 448x256x49).
- Dev: Retake 262 s, Extend 283 s (30 steps), Audio to video 137 s; process peaks 26.8 / 27.5 / 32.8 GB.
- NAG adds about 10 %; float16 compute 10-20 % faster.
- Encode (Gemma load) about 9 s; it is cached, so a re-roll with the same prompt skips it.

Memory grows mostly in the decode: about 7.7 + 25.2 x MP x frames / 49 GB above the ~21 GB that stays loaded (the
plan's formula; measured 10.6 GB at 448x256x49 and 23.4 at 704x448x97). At 1344 x 768 x 97 that formula gives about
59 GB above the resident: likely too much for a 64 GB Mac; admission refuses what does not fit. [pack]
NVIDIA (distilled-fp8, dev-int8): times and peaks not known yet. [pack]

## Pitfalls

- Never change stage steps (8 and 3): fewer leave noise in the clip. [pack]
- Keep 24 fps: other rates change the motion and drift. Audio to video takes 24 only. [pack]
- Lengths snap to 8k+1 and sizes to 64 px; a source for Retake / Extend needs sides that are multiples of 32 and at
  most 321 frames. [pack]
- One-stage halves the output size: draft at the final's canvas and length so timing carries to Two-stage. [pack]
- Detail, NAG, beats, LoRA rows and non-auto bindings are Mac only. [pack]
- Extend before needs Dev, which is about 10x slower. [pack]
- Audio to video on Dev needs the Distilled LoRA add-on; Detail needs the Detailer add-on. [pack]
- The same seed and settings repeat a take on the same software; an app update (mlx) can move details while the
  composition holds. [pack]
- Auto length can land one grid step off between versions. [pack]
- A GPU watchdog crash (`MTLCommandBufferErrorInternal`) can happen on long runs; the hint is to relaunch with
  `AGX_RELAX_CDM_CTXSTORE_TIMEOUT=1`. [pack]
- The weights are gated (a Hub token and the LTX-2 Community licence). Free for commercial use under $10M yearly
  revenue; above that a paid licence. [pack] [card]

## Recipes

The exact values are the recipes sent with this guide (model.yaml `recipes`); set each param with change. When to use each:

- quick-look (One-stage), normal-shot (Two-stage), final-close-up (Detail, Mac only), quality (bf16), low-memory
  (int4), under-a-voice-line (no sound of its own).
- animate-keyframe (Image to video), talking-shot (Audio to video), fix-a-span (Retake), longer-shot and prepend
  (Extend).
- Not settings: land a pose with a second keyframe row at the end time, strength 1, binding guide (or replace on a
  grid frame); a timed action with `encode.beats`, e.g. 0.5 s "holds still", 0.5 s "crouches", 0.8 s "jumps", 0 s
  "lands and holds" [pack].

## Sources

- runner_service/packs/ltx/model.yaml, GOTCHAS.md, torch.py (header), workflows/1-5 JSON.
- docs/reference-notes/ltx.md; docs/plan/17-nvidia-quantized.md; pipelines_service/assistant_knowledge.md.
- plugins/cinematic-filmmaker/SKILL.md, plugins/animator/SKILL.md and their plugin.json recipes.
- Model card: https://huggingface.co/Lightricks/LTX-2.5 (pinned revision 5e6e71018ee1756ed329b697a7b4aedc934dfce9;
  the card was read at main, 2026-11-25).
- Prompting guide: https://docs.ltx.io/open-source-model/usage-guides/prompting-guide
- Detailer: https://huggingface.co/Lightricks/LTX-2.5-22b-IC-LoRA-Pixel-Spatial-Upscaler (not read; facts from the
  pack).
