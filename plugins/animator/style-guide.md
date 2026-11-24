# Holding one cartoon style

The models here lean toward photo-real by default. A cartoon look holds only when every image and every clip is told
the same thing in the same way. Use as many of these layers as the family takes; more layers, less drift.

## 1. The style line (every family)

Write one line for the film and paste it, word for word, into every prompt: first in image prompts, last in video
prompts. Name the medium, the line, the colour and the shading.

Style lines that work:
- "2D cartoon, bold clean black outlines, flat colours, simple cel shading, bright saturated palette"
- "hand-drawn 2D animation, soft pencil line, watercolour backgrounds, muted pastel palette"
- "3D cartoon render, rounded soft shapes, smooth plastic materials, soft studio light, bright colours"
- "flat vector cartoon, no outlines, geometric shapes, limited palette of five colours"
- "paper cut-out animation, layered coloured paper, visible paper texture, flat light"
- "claymation look, sculpted clay characters, fingerprints in the clay, miniature set, soft light"

Tips:
- Name what it is not when it drifts: the LTX Negative prompt (NAG, Mac only) takes "photorealistic, live action,
  realistic skin, film grain". Use it on video shots that turn real.
- Don't mix styles in one line ("anime and claymation") and don't change the words between shots.
- Keep backgrounds a little simpler and less saturated than characters, so characters read.

## 2. A saved look (where the form shows looks)

- Looks are words (sometimes a LoRA) added to the prompt; they nudge, not guarantee. The shipped looks are film,
  camera, colour and lighting looks, mostly photographic: skip the film looks (35mm grain and the like) for cartoons.
  "Muted pastel" and "High contrast" colour looks can suit a cartoon.
- Once the user picks a keyframe that nails the style, saveAsLook from that take (a name like "Pip cartoon"). The
  dialog fills the look's text with the take's whole prompt (framing, action, place): cut it down to the style line
  only. Pick group film, camera or lighting, not colour: LTX shows only film, camera and lighting looks, Qwen Image
  shows all four.
- Looks apply only on Qwen Image and LTX Text or Image to video. MiniMax H3 (all video workflows and H3 Image) and
  LTX Audio to video have no looks: there the style line in the prompt carries the style.

## 3. A style reference image (families that take references)

- Qwen Image: references make the run an edit (up to 10). Put the style image in first and say it: "Draw <image2>
  in the exact style of <image1>: same outlines, flat colours and cel shading." Use the approved model sheet or a
  picked keyframe as the style image.
- MiniMax H3 Image: up to 16 references, `<Picture 1>`, `<Picture 2>`.
- MiniMax H3 References (video): up to 9 images; give the character's element image and one style keyframe.
- LTX takes no references: its style comes from the first frame (your keyframe), the prompt and LoRAs. That is why
  you animate from a styled keyframe instead of Text to video.

## 4. LoRAs (where the family takes them)

- LoRAs are the user's files in the LoRA library (Models, tab loras). Use list_library to see what they have; never
  name a LoRA the library does not list.
- Qwen Image takes LoRA rows on any host; LTX-2.5 takes them on a Mac only (the NVIDIA path refuses LoRA rows).
  MiniMax H3 and MiniMax H3 Image take no library LoRAs (H3's LoRA setting is only none or turbo, a speed LoRA).
- At most 8 rows, strength 0 to 2 (1 by default). A style LoRA at 0.6 to 1 is a good start; too high breaks faces.
- A LoRA is made for one model: a Qwen Image LoRA does not work in LTX. Use one per family with the same style.
- Use the same LoRA and strength on every shot. Add the LoRA's trigger words (the user knows them) to the style line.

## 5. One style per film

- Decide the style before the model sheet and don't change it mid-film. If the user wants to change it, say which
  takes must be redone (all keyframes and clips made with the old style).
- One family per stage when you can: all keyframes from Qwen Image (or all from MiniMax H3 Image), all clips from one
  video family. Mixing families mixes looks; if you must, cut the change at a scene break.
- Check drift: look_at can compare a new keyframe with the model sheet ("Is the character the same design and the
  same drawing style?"). Ask the user before running it (it starts an ask run).
