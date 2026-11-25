# Qwen Image 2.1: assistant guide

## Card

Qwen Image 2.1 (workflow `qwen-image`, title "Qwen Image") makes stills from text, and edits or combines up to 10
reference images (any reference makes the run an edit). Variants: Base (40 steps, for finals), Turbo (8 fixed
steps, much faster: drafts), Base fp8 / Turbo fp8 (NVIDIA only). It can make a transparent (RGBA) image, takes LoRAs
and looks, and has an optional prompt enhancer. Pick it for most stills, edits, cut-outs and fast drafts; pick
MiniMax H3 Image only for very large, detailed stills or edits with more than 10 references.

## Good at

Tags: [pack] = this pack's files and tests, [card] = the makers' pages (Sources), [takes] = the owner's own takes.

- Text to image and image edit in one model; the mode follows the references [card] [pack].
- Edits with up to 10 references: combine, restyle, change a part; keeps identity of people and products [card].
- Native transparency: makes RGBA images, edits transparent layers, cuts subjects out of photos [card].
- Text inside the picture (typography), portrait lighting, fine detail at its native 2K size [card].
- Fast drafts: Turbo runs 8 steps instead of 40 [card] [pack].
- Low memory for its class: int8 roughly halves memory; Turbo int8 holds about 16.7 GB of weights [pack].
- LoRAs: up to 8 rows, strength 0 to 2, on the transformer [pack].
- Answering questions about takes with its own text encoder (the Ask workflow), with no extra load after a still [pack].

## Weak at, or not known

- No negative prompt and no CFG: say what you want, never what to avoid [pack].
- The makers list no limitations or failure cases [card]. Which subjects it gets wrong: not known yet.
- Turbo vs Base quality: the makers give no comparison [card]; only the 8-step schedule is validated for Turbo [card].
- fp8 vs int8 quality on NVIDIA: not measured for this model yet.
- int8 vs bf16: the picture changes (about 22 dB apart in the old studio's test), not known to be worse [pack].
- Languages the image model reads well: not stated [card]; the enhancer takes a prompt in any
  language and returns English [card].
- The owner's takes could not be read for this guide, so there are no [takes] notes yet.

## Modes and tasks

- Workflow `qwen-image` ("Qwen Image"): text to image when `encode.refs` is empty; an edit when it holds 1 to 10
  images [pack]. The mode is not a setting; it is a read-only badge (docs/plan/01-concepts.md) [pack].
- Workflow `qwen-image-ask` ("Ask Qwen Image (Qwen3-VL 8B)"): a text answer about up to 8 takes (a video is sent as
  3 keyframes), temperature 0, thinking off, one retry when the answer misses its schema [pack].
- Enhance: one node, two add-ons: the Text enhancer (PE-T2I) without references, the Edit enhancer (PE-I2I) with
  them. The host must hold both to run Enhance [pack].

## Settings that matter

- `model.variant`: `base` | `turbo` | `base-fp8` | `turbo-fp8` [pack]. Turbo for drafts and quick looks; Base for
  the final. fp8 variants only on NVIDIA hosts [pack]. Turbo ignores Steps [pack] [card].
- `model.precision`: `int8` (default) | `bf16` [pack]. int8 uses about half the memory; bf16 only when memory is
  plenty and you want the reference pixels [pack].
- `sample.steps`: 1 to 200, default 40; only for Base variants [pack]. 40 is what the makers use [card]. Time grows
  with steps (one transformer pass each) [pack]. What fewer steps cost in quality: not known yet.
- `canvas.mode`: `auto` | `fixed`; `canvas.width`, `canvas.height`: 256 to 4096, step 32, default 1024 x 1024 [pack].
  auto with references: about 1 MP at the LAST reference's aspect; without references: width x height [pack].
  Sizes are floored to multiples of 32 [pack].
  The makers' recommended sizes are about 4 MP: 2048 x 2048, 2400 x 1792, 1792 x 2400, 2528 x 1696, 1696 x 2528,
  2752 x 1536, 1536 x 2752 [card]. The pack defaults to 1 MP for speed and memory [pack].
  Memory grows fast with size: sample about 0.9 + 5.1 GB per MP above the weights [pack].
- `decode.tiled`: `auto` (default) | `on` | `off` [pack]. auto tiles above 2.5 MP. Tiling cuts decode memory
  (27 GB vs 65 GB at 4 MP) but changes pixels, so a seed does not repeat across tiled and untiled [pack].
  Leave it on auto.
- `encode.transparent`: bool, default false [pack]. Turn on for cut-outs, logos, product on alpha. It wraps the prompt
  in the makers' transparency sentences, so do not write them yourself [pack] [card].
- `encode.refs`: 0 to 10 images, named `<image1>`, `<image2>` ... in order [pack]. Each reference adds about 1.5 GB to
  encode and about 2 GB to sample [pack].
- `enhance.bypass`: true by default (label "No enhance") [pack]. Off: the enhancer rewrites the prompt into a long
  English one and picks an aspect for an auto canvas [pack] [card]. Use it for short or vague prompts.
- `enhance.think`: bool, default false [pack]. On: better prompts, minutes slower [pack]. The makers say both
  enhancers were trained to think and degrade without it [card]; the pack keeps it off by default, as the old
  studio did [pack]. Turn it on for a final when time allows.
- `lora.rows`: at most 8 rows, strength 0 to 2, default 1 [pack].
- `seed.seed`: -1 random; the same seed, settings and host repeat a take [pack].
- Presets in the workflow: Plain (bypass on), Enhanced (bypass off, think off), Enhanced, think [pack].
- Quick tiles: elements, looks (film, camera, colour, lighting), variant, No enhance, seed [pack].

## Prompt style

- The makers' examples are long and specific: subject, layout, palette, light, camera, where each element sits
  [card]. Their README points short prompts to the enhancer [card].
- The makers' examples quote on-image text, with its place and style, for example a header that reads
  "Topper's Notes" [card].
- Name references by position: `<image1>`, `<image2>` ... in the order of `encode.refs` [pack]. An @element mention
  becomes its reference image(s) [pack].
- For an edit, the makers' example spells out structure, colours, camera position and light [card]; whether to
  name what to keep is not stated. A short
  instruction also works, for example "Change the background to a sunset beach." [card].
- With references and canvas auto, the output takes the LAST reference's aspect: put the one whose frame you want
  last, or set a fixed size [pack].
- For transparency use the Transparent switch, not words [pack].
- No negative prompt: describe the wanted result in positive words [pack].
- Short or rough prompt, any language: turn Enhance on; it returns a long English prompt [card].
- Looks add fixed words to the prompt, for example "shot on 35mm film, fine grain, slight halation" (35mm grain) or
  "soft daylight from a single window, deep falloff into shadow" (Window); they nudge, not guarantee [pack].

## Speed and memory

- Weights: Base 33.1 GB to download, Turbo 14.2 GB more (shares the text encoder and VAE), Base fp8 17.2 GB, Turbo
  fp8 7.1 GB more, each enhancer 18.8 GB [pack].
- Resident int8 (CUDA): text encoder 9.28 + transformer 6.76 + VAE 0.63 GB [pack].
- NVIDIA peaks (RTX PRO 6000, int8 Turbo): 22.0 GB at 768 x 768, 25.5 GB at 1024 x 1024; Base 768 x 768 with both
  transformers resident 29.3 GB; bf16 Turbo 62.9 GB [pack].
- Mac stage peaks above the weights: encode 2.5 + 1.5 GB per reference; sample 0.9 + 5.1 GB per MP + 2 GB per
  reference; decode about 12 GB per MP untiled, about 6 GB tiled [pack]. A whole decode at 4 MP is 65 GB [pack].
- Old studio, 64 GB Mac, Turbo int8 at 2048 x 2048 tiled: 166 s, 22.1 GB peak [pack].
- Enhance on a Mac, think off: about 42 s for text to image (445 new tokens) [pack]; about 2.5 GB above its
  17.5 GB [pack]; think on: minutes, not measured [pack].
- Ask on a Mac: 7.7 s for 4 takes at 512 px, 12.8 s at 1024 px, about 2.3 to 3.5 GB above the text encoder [pack].
- Seconds per run for Base or Turbo at 1 MP on a Mac: not recorded in the pack yet.

## Pitfalls

- Turbo ignores Steps; changing it does nothing [pack].
- Enhance needs both enhancer add-ons on the host, even for a run that uses one; else Enhance is greyed [pack].
- The enhancers are under the Qwen Research License: non-commercial only. The user accepts it per download [pack].
- Licence of the image weights: the makers' pages name the Qwen Research License for this repo [card]; the pack says
  Apache 2.0 and the repo notes this as a known mismatch to fix [pack]. Do not promise commercial use.
- Enhance may change the canvas: with canvas auto it sets the aspect from its answer [pack]. A fixed canvas is kept.
- Re-editing a take with its own seed does not reuse the noise that made it: the edit seed mixes in the references'
  pixels [pack].
- The same seed on a Mac and on NVIDIA gives the same picture, not the same pixels [pack].
- Tiled and untiled decodes differ in pixels; keep `decode.tiled` the same when you re-run a seed [pack].
- Same seed, Turbo then Base: the same starting noise, but a different transformer; how close the final stays to the
  draft is not known yet [pack].
- Reference transparency: the vision encoder sees references over white; the VAE keeps their alpha [pack].
- Large sizes cost memory fast; if a run does not fit, lower the size, use int8, or another host [pack].

## Recipes

The exact values are the recipes sent with this guide (model.yaml `recipes`); set each param with change. When to use each:

- fast-draft (2 to 4 seeds), then final or final-enhanced with the liked seed; native-2k for the makers' size.
- portrait, wide-keyframe (a video keyframe), character-sheet, product-cutout (transparent).
- edit-draft, then edit-final; the references go in first, the frame-setting one last.
- judge-takes: the Ask workflow, up to 8 takes.
- Not settings: multi-reference work names each `<imageN>` in the prompt, up to 10 references [pack].

## Sources

- Pack: runner_service/packs/qwen_image/model.yaml, GOTCHAS.md, torch.py (module docstring and comments),
  workflows/qwen-image.json, workflows/text-qwen-image-ask.json, looks/*.yaml, licences/*.md;
  runner_service/core/nodes.yaml (canvas); runner_service/tests/test_qwen_golden.py;
  docs/reference-notes/qwen.md (old studio, "Measured"); docs/plan/01-concepts.md (mode badge); docs/plan/17-nvidia-quantized.md (licence note);
  deploy/gpu-host/README.md (fp8 sizes); plugins/cinematic-filmmaker/plugin.json, plugins/animator/plugin.json.
- Card (read 2026-11-25; the current pages, not pinned to the pack's revisions):
  https://huggingface.co/Qwen/Qwen-Image-2.1, https://huggingface.co/Qwen/Qwen-Image-2.1-Turbo,
  https://github.com/QwenLM/Qwen-Image-2.1, https://github.com/QwenLM/Qwen-Image-2.1/tree/main/prompt_rewrite.
- Takes: not readable (the CLI session expired; no login allowed).
