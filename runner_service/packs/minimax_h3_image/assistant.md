# MiniMax H3 Image: assistant guide

## Card

MiniMax H3 Image (workflow `minimax-h3-image`) makes large, detailed stills from text, and edits or combines up to
16 reference images (any reference makes the run an edit). One quality level: 6 fixed steps, no steps, CFG or
negative prompt. Variants: Base (int8 at load) and Base fp8 (NVIDIA only). Heavy: about 39 GB peak and 2 to 2.5 min a
run on a 64 GB Mac. Pick it over Qwen Image for big final stills (4 to 16 MP) or edits with more than 10
references; use Qwen Image for drafts, transparency and LoRAs.

## Good at

Tags: [pack] = this pack's files and tests, [card] = the makers' pages (Sources), [takes] = the owner's own takes.

- Very large stills without an upscaler: about 16 MP at every aspect, 4096 x 4096 up to 6272 x 2688 (21:9) [card].
- Best results near 16 MP; it works from about 4 MP to 20 MP [card].
- Following instructions: the makers call it the strongest at this among open models [card].
- Edits with many references: up to 16; the makers show a room furnished from ten references [card] [pack].
- Text inside the picture: the makers show Chinese and English text on a board [card].
- Repeatable: Euler with the same seed gives the same PNG bytes on the same stack [pack].

## Weak at, or not known

- No transparency: references lose their alpha and the output is RGB [pack].
- No LoRAs, no looks tiles, no steps, no CFG, no negative prompt [pack] [card].
- No fast draft: one quality level, 6 fixed steps; under 4 MP it still renders 4 MP, so smaller is not faster [pack].
- Heavy and slow next to Qwen Image: its three big models never fit together, each loads in turn [pack].
- The makers list no limitations section [card]. Which subjects it gets wrong: not known yet.
- Languages it reads well: it expects a detailed English description [card]; other languages: not known yet.
- fp8 vs int8 quality on NVIDIA: not measured for this model yet. Base on NVIDIA is untested [pack].
- The owner's takes could not be read for this guide, so there are no [takes] notes yet.

## Modes and tasks

- Workflow `minimax-h3-image` ("MiniMax H3 Image"): text to image when `encode.refs` is empty; an edit when it holds
  1 to 16 images [pack]. The mode is not a setting [pack].
- Enhance: the optional Enhancer add-on (MiniMax-H3-Image-PE, 27B) rewrites the prompt and picks an aspect for an
  auto canvas; without the add-on Enhance stays off [pack].
- There is no Ask workflow for this model [pack].

## Settings that matter

- `canvas.mode`: `auto` | `fixed`; `canvas.width`, `canvas.height`: 256 to 8192, step 32, default 2048 x 2048 [pack].
  auto with references: the FIRST reference's aspect (clamped 1:4 to 4:1) at the area of width x height; without
  references: width x height [pack]. At most 20 MP [pack]. Under 4 MP the model renders 4 MP and resizes after
  decode [pack]. Raise toward 16 MP for a hero still [card]; each step up costs time and a few GB (below).
- `sample.sampler`: `euler` (default) | `lcm` [pack]. Euler repeats exactly with the same seed. LCM re-noises each
  step from the device's RNG, so it does not repeat on another device [pack]. Which looks better: not known yet.
  Keep euler unless the user wants to try a different texture.
- `model.variant`: `base` | `base-fp8` (NVIDIA only) [pack].
- `model.precision`: Base is always int8 at load (bf16 would be 66 GB per model); Base fp8 is fp8 as stored [pack].
  Nothing to choose here in practice.
- `encode.refs`: 0 to 16 images, named `<Picture 1>`, `<Picture 2>` ... in order; alpha dropped [pack] [card]. Each
  adds about 1.25 GB to encode and to sample [pack].
- `enhance.bypass`: true by default (label "No enhance") [pack]. Off: a short prompt in any language becomes a
  detailed English description [card], and an auto canvas gets an aspect (1:1, 5:4, 4:3, 3:2, 16:9, 21:9, 4:5, 3:4,
  2:3, 9:16; from docs/reference-notes/minimax-h3-image.md, the old studio; the pack accepts any W:H) [pack].
  Costs about 2 min more for text to image, about 4 min for an edit [pack].
- `enhance.think`: bool, default false [pack]. On: reasoning effort xhigh instead of low; better prompts, slower
  [pack]. Thinking is always on in some form [pack].
- `seed.seed`: -1 random [pack]. A new seed re-runs sample and decode only, unless Enhance is on [pack].
- Presets in the workflow: Plain (bypass on), Enhanced (bypass off, think off), Enhanced, think [pack].
- Quick tiles: elements, No enhance, canvas mode, variant, seed [pack].

## Prompt style

- Write a detailed English description of the whole image, not a short instruction [card].
- The prompt is read as given, up to 4096 tokens; more is cut [card].
- Name references `<Picture 1>`, `<Picture 2>` ... (a space before the number), by their order in `encode.refs`
  [card] [pack]. An @element mention becomes its reference image(s) [pack].
- With references and canvas auto, the output takes the FIRST reference's aspect: put the frame-setting one first
  (the opposite of Qwen Image) [pack].
- No negative prompt and no guidance: describe the wanted result in positive words [card] [pack].
- A short prompt or a non-English one: turn Enhance on [card] [pack].
- Exact text for the picture: write it out in the prompt; how it handles quotes is not stated [card].

## Speed and memory

All Mac numbers: 64 GB M-series, torch 2.15 MPS, int8 [pack].

- Text to image at 4 MP (measured with a 1024 x 1024 request, rendered at 4 MP): 112 to 145 s in all [pack]. Per node: encode 21 to 24 s (30.6 GB peak),
  sample 84 to 87 s cold at 9.4 s a step (38.8 GB), decode 6.3 s (37.4 GB) [pack].
- Edit with one 768 x 768 reference: encode 24 s, sample 114 s, decode 6.2 s [pack].
- Enhance, text to image: 117 to 122 s, 36.2 GB peak [pack]; an edit adds about 4 min [pack].
- Old studio on an M5 Pro: end to end 115 s (4 MP text to image), 218 s (4 MP edit), 403 s at 16 MP with a 41.9 GB
  peak; a seed or size re-roll with the model warm 76 s [pack].
- The plan's peak is 38.9 GB at 4 MP [pack].
- References: about 1.1 to 1.25 GB each; past about 10 small references the sample stage no longer fits a 52 GB
  budget and the run fails with "does not fit in memory" [pack].
- Weights: enhancer 27.1 GB int8, text encoder 25.7, transformer 31.0 + VAE 2.2 [pack]. Downloads: Base fp8 about
  49.8 + 20.3 + 27.1 GB of files, enhancer 17.9 GB [pack].
- NVIDIA times and peaks: not recorded yet.

## Pitfalls

- Heavy: it needs a host with about 39 GB free at 4 MP and more at 16 MP; small Macs cannot run it [pack].
- Smaller sizes do not save time: anything under 4 MP is rendered at 4 MP [pack].
- Too many references on a Mac (about 10+ small ones) fail with "does not fit in memory": use fewer or smaller [pack].
- The first reference sets the aspect on auto; Qwen Image uses the last [pack].
- Transparent references lose their alpha; for a cut-out use Qwen Image [pack].
- LCM takes do not repeat on another device; Euler does [pack].
- With Enhance on, a new seed re-runs the enhancer too (minutes) [pack].
- Licence: MiniMax-H3-Image Community License, accepted per download [pack]. Its Applicable Territory leaves out the
  EU, the UK, South Korea and the US (repo notes), and it sets conditions on hosted services [pack]; some uses need
  MiniMax's written authorization (section IV) [card]. Point the user to the licence; do not judge it for them.
- The enhancer is under the same licence [card].

## Recipes

The exact values are the recipes sent with this guide (model.yaml `recipes`); set each param with change. When to use each:

- standard (4 MP), enhanced (a short prompt), hero-16mp, wide-16mp, portrait-16mp, wide-4mp (a sheet), edit.
- Not settings: up to 16 references named `<Picture N>`; on a 64 GB Mac keep it under about 10 small ones [pack].

## Sources

- Pack: runner_service/packs/minimax_h3_image/model.yaml, GOTCHAS.md, torch.py (module docstring and constants),
  workflows/minimax-h3-image.json, licences/minimax-h3-image-community.md; docs/reference-notes/minimax-h3-image.md
  (old studio, "How it fits in 64 GB"); docs/plan/17-nvidia-quantized.md (licence territory);
  plugins/cinematic-filmmaker/plugin.json, plugins/animator/plugin.json.
- Card (read 2026-11-25; the current page, not pinned to revision 8ced600):
  https://huggingface.co/MiniMaxAI/MiniMax-H3-Image.
- Takes: not readable (the CLI session expired; no login allowed).
