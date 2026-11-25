# MiniMax H3: what the Assistant should know

## Card

MiniMax H3 (33B) makes video clips with stereo sound, up to about 15 s at 24 fps. Modes: Text to video, Image to
video (a First frame and / or a Last frame) and References (up to 9 images, 3 videos and 3 sounds named in the
prompt). Pick it over LTX-2.5 for higher quality, for keeping characters and places from reference images, for
landing a move exactly between two frames, and for dialogue in 11 languages. It is slow: about 5-6 minutes for a
5 s take at 864 x 480 on a Mac (Fast preset); Default and Reference take longer. Draft with the Draft preset first.

## Good at / weak at

Tags: [pack] = this pack's files, [card] = MiniMax's model card and prompt guides, [takes] = the owner's takes (none
read yet: the Gallery was not readable, so there are no [takes] claims).

Good at:
- Video with native stereo sound (32 kHz) in one pass. [pack] [card]
- References: up to 9 images, 3 videos (2-15 s each, 15 s in all) and 3 sounds (2-15 s each, 15 s in all), 12 files
  in all; it keeps people, objects, places and styles from them. [pack] [card]
- First-and-last-frame animation: a continuous move from one frame to the other. [pack] [card]
- Dialogue in 11 languages (Arabic, Chinese, English, French, German, Italian, Japanese, Korean, Portuguese,
  Russian, Spanish), with speaker voices and timing written in the prompt. [card]
- Multi-shot clips with timed cuts inside one generation. [card]
- Many aspect ratios: 21:9, 16:9, 4:3, 1:1, 3:4, 9:16 and others. [card]
- Turbo LoRA: 8 steps near the 50-step look on a Mac (measured sharper than the Fast preset: 59 against 39 at
  864 x 480 x 124). [pack]
- Text to video repeats bit for bit with the same seed and settings. [pack]

Weak at:
- Slow: about 24-29 s per step at 864 x 480 x 124 on an M5 Pro, plus about 104 s of silent video decoding. [pack]
- Image to video and References do not always repeat exactly: the same seed can differ slightly. [pack]
- Locally there is no Context-IR (MiniMax's hosted prompt rewriter, which the card calls critical to quality) and no
  2K regenerate step: write the structured prompt yourself; the largest canvas here is 1344 x 768. [card] [pack]
- No audio switch: the clip always has sound (mute it on the Timeline). [pack: plugins/cinematic-filmmaker]
- No looks tiles: repeat the look's words in the prompt. [pack: plugins/cinematic-filmmaker]
- Only M5 Macs are validated; another Mac takes an untested bf16 path (~36 GB transformer). [pack]
- Token reduction can shift the composition; low Render sizes lose fine detail. [pack]

## Modes and tasks

Workflows (id, title):
- `minimax-h3-text-to-video`, "MiniMax H3 Text to video": prompt only (T2VA, the FL2VA weights). [pack]
- `minimax-h3-image-to-video`, "MiniMax H3 Image to video" (action Animate): `first.image` and optional
  `last.image` (FL2VA). First only: start from it; both: land on the last. [pack] [card]
- `minimax-h3-references`, "MiniMax H3 References": `generate.refs`, `generate.videos`, `generate.audios` (Ref2VA
  weights). [pack]

How to choose:
- The mode follows the inputs: any reference row switches to Ref2VA; references never go with a first or last
  frame. [pack]
- A character or place must match closely, or two characters share a shot: References with the elements' images.
  [pack]
- A move must end on an exact frame: Image to video with First and Last frame. [pack]
- A sound must drive the clip (a voice, a beat): References with `<Audio n>`; it also needs an image or video
  reference. [pack]
- Speed and keyframes at set times, retake or extend: use LTX instead. [pack]

## Settings that matter

- `model.variant`: base (Mac; int8 transformer on M5) or base-fp8 (NVIDIA, SGLang). [pack]
- `canvas.width` / `canvas.height`: 256-1344, step 32, at most 1,032,192 pixels in all (1344 x 768). Default
  864 x 480 (16:9). The card's base resolution is 768 on the short side. Larger is much slower. [pack] [card]
- `duration.frames`: 5-362 on the 5 + 17k grid at 24 fps: 56 (about 2.3 s), 73 (3 s), 90 (3.75 s), 124 (about 5 s,
  default), 362 (about 15 s). The card's range is 4-15 s. `duration.fps` is 24 only. [pack] [card]
- `generate.steps`: 2-50 (template 20). 4 a draft, 20 normal, 50 reference quality; 8 with the Turbo LoRA. Time
  grows linearly with steps; memory does not. [pack]
- `generate.lora`: none | turbo. Turbo needs the Turbo LoRA add-on (References: the Turbo Ref LoRA add-on); use 8
  steps; adds about 4.5 GB on a Mac; strength fixed at 1. [pack]
- Mac only (Base), the Advanced section [pack]:
  - `generate.layers`: 35-50 (50 exact, 45 fast, 40 aggressive); fewer is faster and holds less memory.
  - `generate.reuse`: 1-3 (1 every step fresh, 2 fast, 3 aggressive). At 7 steps or fewer, reuse above 1 leaves very
    few fresh passes (a warning).
  - `generate.core_reuse`: 1-6, only with reuse 1 (4 fast, 6 a rough preview).
  - `generate.render`: Full | 75% | 62.5%: renders smaller and scales up; faster, softer. Needs a canvas that has a
    smaller size of the same shape (864 x 480 drops to 576 x 320 for both; 832 x 480 has none: use Full).
  - `generate.token_reduction`: about 25 % faster, can shift the composition; refused with layers 40 or fewer and
    reuse 3 (colour ringing) and when h3 renders 256 x 256.
- NVIDIA only (Base fp8): `generate.quality` lossless | exact (exact is bit-identical and slower);
  `generate.flow_shift` 1-20 (12), `generate.audio_flow_shift` 1-10 (3). Leave the shifts at the model's values
  unless asked; their effect is not measured here. [pack]
- References: `generate.refs` up to 9 images (`<Picture n>`); `generate.videos` up to 3 {video 2-15 s with in / out,
  sound keep | silent | replace, soundtrack for replace} (`<Video n>`); `generate.audios` up to 3 (`<Audio n>`). A
  video whose sound is used needs a part of at least 56 frames (about 2.34 s) and a clip of at least 56 frames.
  [pack]

## Prompt style

- MiniMax's guides use a structured prompt; follow it, since no Context-IR rewrites prompts here. [card]
- Text and image to video: three parts after any instruction line [card]:
  - `integrated_multimodal_description`: the timeline of picture, actions, shots, speech and sound in the scene.
  - `overall_soundscape`: 1-4 English sentences of ambience, physical sounds, breathing; no dialogue. N/A only for
    total silence.
  - `non_diegetic_music`: 1-3 sentences on instruments, tempo, rhythm, dynamics (no mood words), or N/A.
- Open Shot 1 with a visual style (live-action, cinematic, 3D CG, claymation, watercolor). [card]
- Shots: Shot 1 has no time; later cuts as `[Shot 2] At 00:03.500,` with times rising inside the clip. Use a cut
  only for new information; small reframes are camera moves. [card]
- Camera: move type (push in, pull out, pan, truck, tilt, pedestal, arc, tracking, static, shake, POV, roll, zoom),
  with `small` / `large` amplitude and `slow` / `fast` speed only when not medium / normal; write it into the
  sentence. [card]
- Dialogue: speaker ids `(S1)`, `(S2)`; describe the voice on first appearance; only the language tag and the exact
  words inside the tag: `<d>[English] Where were you?</d>`. Voiceover: "says in an off-screen voiceover" and the
  on-screen lips stay closed. `<scenetrans>` for speech across a cut, `<cutoff>` for speech cut at the end. [card]
- On-screen text: in English double quotes, verbatim. [card]
- First and last frame: one continuous move from the first frame to the last, one shot unless asked; keep identity,
  clothes and layout of the frames. The guide's instruction line mapping `<Picture N>` to times (0.00 and the end in
  seconds) is for MiniMax's pipeline; whether h3 here needs it is not known yet (the pack passes the frames as
  flags). [card] [pack]
- References: the composer inserts `<Picture n>`, `<Video n>`, `<Audio n>`; numbering is per kind. [pack] The ref
  guide defines what appears as `<Subject N>` lines that cite the files (a character "from <Picture 1>"), uses a
  standalone `<Picture N>` only for a frame anchor, binds a voice as `<Subject N> (S1)`, and aims at 350-500 English
  words for a generation task. [card]
- Use the same `<Picture n>` / `@element` words in every shot of a character. [pack: plugins]

## Speed and memory

Mac (M5 Pro 64 GB, int8, measured) [pack]:
- 864 x 480 x 124: about 24-29 s per step, video decode about 104 s, encode about 12 s; 6 steps took 303 s.
  Fast preset (20 steps, 45 layers, reuse 2, token reduction): 317-345 s. Turbo (8 steps): 413-420 s.
- 1344 x 768 x 124: about 130 s per step, decode about 282 s (4 steps: 846 s). 1344 x 768 x 243: about 500 s per
  step, decode about 560 s. A 15 s 768p clip is likely over 1.5 hours at 6 steps, much longer at 20 (not run).
- References 512 x 512 x 62, one image: Turbo 128 s, Base 50 steps 490 s.
- Peak (the h3 process): 18.5 GB at 512 x 512 x 22, 20.7 at 864 x 480 x 124, 24.2 at 1344 x 768 x 124, 30.3 at
  1344 x 768 x 243. The plan budgets 24 + 0.05 x MP x frames GB (+4.5 with Turbo; a video reference counts as about
  1 MP of frames). Steps do not change memory.
- Each run reads about 118 GB from disk unless the page cache holds the shards.

NVIDIA (RTX PRO 6000 96 GB, Base fp8, 864 x 480 x 124, including the server start) [pack]: 8 steps 77 s, 20 steps
97 s, Turbo 8 steps 117 s; References with Turbo 206 s; GPU peak 32-37 GB. Base fp8 is already clean at 8 steps
without the LoRA. Host RAM peak and per-step times: not known yet.

## Pitfalls

- References and first / last frames cannot be mixed. Audio references need an image or video reference. [pack]
- At most 12 references, and all reference sound together at most 15 s with at most 3 sound inputs. [pack]
- A video reference with Sound keep or replace needs at least about 2.34 s (56 frames) in its part. [pack]
- `<Audio n>` numbers audio rows first, then videos whose sound is kept. [pack]
- Render 75% / 62.5% is refused on a canvas with no smaller same-shape size (for example 832 x 480). [pack]
- Token reduction with 40 layers or fewer and reuse 3 gives colour ringing (refused). [pack]
- Reuse 2 or 3 with 7 steps or fewer barely helps (h3 warns); Draft uses reuse 1. [pack]
- Image to video and References may differ slightly on a re-run with the same seed; Text to video repeats. A macOS
  update can change bits. [pack]
- Long runs hold the host; the video decode prints no progress for minutes. [pack]
- Turbo on References uses the Turbo Ref LoRA (another add-on). On NVIDIA, Turbo leaves out the LoRA's AdaLN part,
  and saves little time there. [pack]
- Licence: MiniMax H3 Community License; users in the USA, EU, UK and South Korea must apply through a form. [card]

## Recipes (draft)

- Draft: `generate.steps` 4, `generate.layers` 50, `generate.reuse` 1, `generate.core_reuse` 1,
  `generate.token_reduction` false, `generate.lora` none; the final's canvas and frames so timing carries. [pack]
- Fast: steps 20, layers 45, reuse 2, token_reduction true, core_reuse 1, lora none. [pack]
- Turbo: steps 8, layers 50, reuse 1, token_reduction false, core_reuse 1, lora turbo (add-on). [pack]
- Default: steps 20, layers 45, reuse 2, token_reduction false, core_reuse 1, lora none. [pack]
- Reference (hero shot): steps 50, layers 50, reuse 1, token_reduction false, core_reuse 1, lora none. [pack]
- Quick preview: Draft with `generate.render` 62.5% on 864 x 480. [pack]
- Film shot: canvas 1344 x 768, `duration.frames` 124, steps 20 (filmmaker `h3-references-shot`,
  `h3-first-last-shot`); draft at 4 steps with the same seed. [pack]
- Cartoon shot: canvas 896 x 512, frames 124, steps 20 (animator `h3-first-last-5s`, `h3-references-5s`). [pack]
- Portrait / vertical: canvas 480 x 864 (9:16), frames 124. [pack] [card]
- Long clip: `duration.frames` 362 (about 15 s) at 864 x 480 with the Fast preset; expect a long run. [pack]
- Talking character: References with the character's image as `<Picture 1>` and the voice line as `<Audio 1>`
  (2-15 s), dialogue in `<d>[English] ...</d>`. [pack] [card]

## Sources

- runner_service/packs/minimax_h3/model.yaml, GOTCHAS.md, workflows/1-3 JSON.
- docs/reference-notes/minimax-h3.md; pipelines_service/assistant_knowledge.md.
- plugins/cinematic-filmmaker/SKILL.md, plugins/animator/SKILL.md and their plugin.json recipes.
- Model card: https://huggingface.co/MiniMaxAI/MiniMax-H3 (pinned revision 1cc7549f2cf0af8ff2f09b24f3c57303c8aa6317;
  read at main, 2026-11-25).
- Prompt guides: https://huggingface.co/MiniMaxAI/MiniMax-H3/blob/main/docs/VIDEO_PROMPT_WRITING_GUIDE_base_en.md and
  https://huggingface.co/MiniMaxAI/MiniMax-H3/blob/main/docs/VIDEO_PROMPT_WRITING_GUIDE_ref_en.md
- Turbo LoRA: https://huggingface.co/lightx2v/Minimax-h3-Turbo (not read; facts from the pack).
