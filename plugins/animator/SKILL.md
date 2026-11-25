# Animator

A guide for making a short animated (cartoon) film in helmstudio, from an idea to an exported MP4. Read it as craft
advice; the screen, the workflows list and the form are the truth for this install (a setting that is greyed stays
greyed, a workflow that is not `downloaded` can't run).

Extra files you can read with read_skill_file:
- `style-guide.md`: how to hold one cartoon style across every generator (style words, a look, a style image, LoRAs).
- `model-sheet.md`: character design for animation and the model sheet prompts (turnaround, expressions).
- `shot-list-template.md`: the script and shot list format, with frame counts.
- `camera-moves.md`: camera moves and the prompt words for them.
- `edit-checklist.md`: Timeline assembly, sound and export.

## 0. The order of work

Do the steps in this order. Each one feeds the next, and a mistake found early is cheap.

1. Idea and style (ask the user)
2. Script and shot list (ask the user to approve)
3. Characters: design, model sheet, Save as element (ask the user to pick)
4. Voices: one per character, saved (ask the user to listen)
5. Backgrounds and props (elements for places)
6. Keyframes: one still per shot, drafts then finals (ask the user to pick)
7. Animation: one clip per shot, drafts then finals (ask the user to pick)
8. Dialogue and sound
9. Timeline edit and MP4 export (ask the user to watch before export)

Stop at the checkpoints marked "ask". Show the takes, say which one you would keep and why, and let the user pick.
Never Generate a long or final run the user has not seen planned. Generate always shows the user a card first.

## 1. Idea and style

- Get the story in one or two sentences: who wants what, what stands in the way, how it ends. A short film is 30 s
  to 2 min: 8 to 30 shots.
- Pick one cartoon style for the whole film and write it down as a style line: medium, line, colour, shading.
  For example: "2D cartoon, bold clean black outlines, flat colours, simple cel shading, soft pastel palette".
  See `style-guide.md` for style lines that work and what to avoid.
- Ask the user to confirm the style before any generation. Changing style later means redoing every keyframe.

## 2. Script and shot list

- Write a short script: scene headings, action in plain words, dialogue lines as `@name: text`.
- Break it into shots. One shot = one action. "She opens the door" is a shot; "she opens the door, walks in and
  sits down" is three. A clip that tries several actions blurs them or drops some.
- For each shot write: number, framing, camera move, the action, the line (if any), length in seconds and frames,
  workflow. Use `shot-list-template.md`.
- Most shots are 2 to 4 s. Comedy and action cut faster (1 to 2 s); a reaction or a reveal can hold longer.
- Start wide to show the place, then go closer for feelings. Cut on an action (a turn, a jump) so the next shot
  picks it up.
- Ask: show the shot list and the total length; change it until the user approves.
- Save it with save_shots (the film's title; per shot the number, the action, the length in seconds, the line) as
  soon as you write it, and again when it changes: it shows under Memory, Projects, and any chat reads it. When the
  user picks a shot's final clip, set that shot's take_id.
- For "shot 3" or "the next shot", read the saved list. A shot that isn't in it: ask with pick-one choices, never
  guess.

## 3. Characters

- Design for animation: simple shapes, a clear silhouette, few colours (3 to 5 main ones), one or two features
  that make the character readable at a glance (big ears, a red scarf). Detail flickers when it moves.
- Make a model sheet first: the character in the film's style, a turnaround (front, three-quarter, side, back)
  and a row of expressions. `model-sheet.md` has the prompts.
- Use Qwen Image (Turbo for drafts, then Base) or MiniMax H3 Image for a large, detailed sheet. Make 2 to 4 seeds.
- Ask: let the user pick a design. Then make a clean single front view (full body, plain background) of the pick:
  an edit of the sheet with it as a reference keeps the design.
- Save it with saveAsElement (take_id of the clean view, handle like `@pip`, group Character). Elements carry text and reference
  images; the text should hold the design words (shapes, colours, outfit) and the style line.
- Write the handle the same way in every prompt. Use only @handles the user wrote, that the form holds, or that
  list_library returned.

## 4. Voices

- Give each speaking character one voice and keep it for the whole film.
- Cartoon voices are a bit bigger than life: brighter, clearer, more energy. Do it with delivery, not by shouting.
- Ways to get a voice:
  - Design one from words: designVoice with a description ("a small, bright, quick voice of a young fox, playful,
    slightly raspy") and a name. It opens Qwen3-TTS Voice design; the user hears tries and saves.
  - Clone one: cloneVoice from a clip of 3 to 30 s (ZONOS3).
  - A preset speaker (Qwen3-TTS has 9, ZONOS3 has 27), with style words.
- A saved voice with a clip (cloned, or designed and saved) works in ZONOS3 Voice-over and ZONOS3 Dialogue, so
  every line uses the very same voice. Qwen3-TTS Dialogue takes presets only (a designed voice drifts between
  lines).
- Put the voice on the character's element so the cast picks it up. Only an element saved with group Character
  can carry a voice (places are Location, props are Prop).
- Ask: let the user listen to one test line per character before you voice the script.

## 5. Backgrounds and props

- Make each place once, in the film's style, with no characters in it: "empty", "no people". Wide 1344 x 768.
- Save each place as an element, group Location (`@kitchen`), and props that recur, group Prop (`@red-kite`).
- Keep the light the same within a scene: name the time of day and the light in the element's text.

## 6. Keyframes (stills)

- Animate from stills: the look is set before any motion runs, and the still is the shot's first frame.
- One keyframe per shot, at the film's one shape: stills 1344 x 768 (Qwen Image, fixed size), video 896 x 512.
  Both are 1.75 : 1, so nothing gets cropped. See the recipes.
- Build the prompt in this order: style line, framing, the @character doing the start pose of the action, the
  @place, light. Put the character's start pose, not the action's end.
- With Qwen Image, put the model sheet or the element's image in as references; the prompt names them `<image1>`,
  `<image2>`. With MiniMax H3 Image they are `<Picture 1>`, `<Picture 2>`. References make the character match.
- Draft with Turbo (8 fixed steps), 2 to 4 seeds a shot. Ask: let the user pick per shot. Then run the pick again
  with Base (40 steps) and the same seed and prompt.
- For a shot that ends somewhere clear (a jump that lands), make an end keyframe too.

## 7. Animation (video)

- One clip per shot, from its keyframe: useIn with the keyframe's take_id and the video workflow (it goes in as the
  first frame), then write the motion prompt.
- The motion prompt: camera move first, then who moves, how and how fast, then a hold. "Static shot. @pip crouches,
  then jumps up and grabs the apple, and lands softly. Holds." Keep the style line at the end so it stays a cartoon.
- Pick the workflow:
  - LTX-2.5 Image to video (Animate): fast, keyframes at set times, sound in the same pass. Distilled, 8 steps.
    Recipe One-stage for a quick look (half size), Two-stage (default) for normal work, Detail for a final
    close-up (Mac only, Detailer add-on).
  - MiniMax H3 Image to video: higher quality, First frame and Last frame to land a move exactly. Slower.
  - MiniMax H3 References: the character from images (up to 9 `<Picture n>`), no first or last frame.
- Draft first: LTX One-stage, or the H3 Draft preset (4 steps). Check the action and the camera, not the detail.
  Then the final with the same seed: LTX Two-stage, or H3 Default (20 steps); Reference (50) for a hero shot.
- Ask: show the drafts of a scene together; let the user pick before finals.
- If one part of a good clip is wrong, fix that part: LTX Retake regenerates a span; LTX Extend adds 0.1 to 10 s
  after (before needs Dev).

## 8. Timing and animation craft

- Both video families run at 24 fps. Seconds x 24 = frames. Keep 24: LTX was trained there and H3 takes only 24.
- LTX-2.5 lengths are 8k + 1, 9 to 321 frames: 25 (1 s), 49 (2 s, default), 73 (3 s), 97 (4 s), 121 (5 s).
- MiniMax H3 lengths are 5 + 17k, 5 to 362 frames: 56 (about 2.3 s), 73 (3 s), 90 (3.75 s), 124 (5 s, default).
- Anticipation: a small move the opposite way before a big one (crouches, then jumps; leans back, then throws).
- Follow-through: parts keep moving after the body stops (ears, tail, scarf settle).
- Squash and stretch, in words: "squashes as it lands, stretches as it leaps". Cartoon models follow it best on
  simple round shapes.
- Easing: "slowly turns", "eases to a stop", "snaps quickly". Cartoon timing likes contrast: a slow wind-up and a
  fast snap.
- Holds: a still moment at the start and the end makes a shot easy to cut and lets a pose read. "Holds still,
  then ...", "... and holds."
- One subject move plus at most one camera move per shot.
- LTX prompt beats: up to 8 timed lines that steer picture and sound in order. Each value is that beat's length,
  not its start time; 0 shares what the others leave equally: 0.5 s "holds still", 0.5 s "crouches", 0.8 s "jumps
  and grabs the apple", 0 s "lands and holds".
- LTX Frames: up to 8 images with At (seconds), Strength (0 to 1) and Binding (auto, replace, guide). An image at
  0 s starts the clip; one later is a pose it passes through. Use an end pose to land a jump.
- See `camera-moves.md` for camera moves.

## 9. Keep it consistent across shots

- Style: the same style line, in the same words, at the start of every image prompt and the end of every video
  prompt; the same saved look (style line only, group film, camera or lighting) on Qwen Image and LTX Text or
  Image to video shots (H3 and LTX Audio to video have no looks); the same LoRA and strength where the family takes LoRAs.
- Characters: the same @handles, the element's image as a reference in every keyframe.
- Places: the same @place element per scene, the same light words.
- Voices: one saved voice per character, on its element.
- Size and rate: stills 1344 x 768, clips 896 x 512 at 24 fps, on every shot, so no clip gets resized. Export
  copies clips without re-encoding only when they also are untrimmed, have no dissolve and come from one family;
  a trimmed cut re-encodes.
- Seeds: keep the seed of a draft the user picked when you run its final. Same seed and settings repeat a take.

## 10. Dialogue and sound

- Voice each line with ZONOS3 Voice-over (one line) or a Dialogue workflow (a scene's lines). Dialogue takes a
  script, one `@name: text` or `@name (style): text` line each, every @name in the Cast. Write the script yourself:
  switching keeps the box's text, and a picture prompt would be read aloud.
- Lip movement: LTX Audio to video makes a clip that follows a sound (up to 13.375 s). It is not a lip-sync model,
  but a talking shot looks better made from its line. Turn No keyframes off (change keyframes.bypass to false),
  then add the keyframe in Frames at 0 s. The line sets the shot's length (there is no Duration): voice it with
  silence before and after to reach the planned length, or make a very short line's shot with Image to video and
  lay the line on an audio track. An H3 `<Audio n>` needs 2 to 15 s. MiniMax H3 References can also take the line as `<Audio n>`
  (with an image or video reference).
- On the Timeline, newLine voices a new dialogue line in place.
- Clips from LTX and H3 come with sound (effects, ambience). Keep it for effects; turn LTX Audio off for a shot
  whose sound you will replace.

## 11. Timeline edit and export

- Put the picked finals on a sequence in shot order, the lines on audio tracks under their shots. Shot actions on a
  clip (retake, extend, redo, replace) fix a single shot without leaving the Timeline.
- Trim heads and tails to the action; cut on movement; keep holds only where a pose must read.
- Ask: let the user watch the cut, then export (an MP4 take). `edit-checklist.md` has the steps.

## 12. Recipes

This skill suggests settings as recipes (only the ones this install can run are offered). Apply one with the form's
own actions (switchTo the workflow, then change each field); Generate still waits for the user's Yes.
- cartoon-sheet-draft / cartoon-sheet: Qwen Image model sheet, 1344 x 768, Turbo / Base 40 steps.
- cartoon-sheet-h3: MiniMax H3 Image model sheet, 2688 x 1536.
- cartoon-key-draft / cartoon-key: Qwen Image keyframe, 1344 x 768, Turbo / Base 40 steps.
- ltx-animate-2s / ltx-animate-4s: LTX Image to video, 896 x 512, 49 / 97 frames at 24 fps.
- ltx-text-3s: LTX Text to video, 896 x 512, 73 frames.
- ltx-talk: LTX Audio to video, 896 x 512 (the line sets the length; turn No keyframes off to add the keyframe).
- h3-draft-3s: MiniMax H3 Text to video, 896 x 512, 73 frames, 4 steps, no LoRA.
- h3-first-last-5s: MiniMax H3 Image to video, 896 x 512, 124 frames, 20 steps.
- h3-references-5s: MiniMax H3 References, 896 x 512, 124 frames, 20 steps.
- cartoon-line: ZONOS3 Voice-over, lively delivery.
- cartoon-line-qwen: Qwen3-TTS Voice-over, Lively variation, playful style.
- cartoon-dialogue: ZONOS3 Dialogue, lively delivery.

## 13. A worked plan

Ask: "a 20 s cartoon of @pip the fox stealing an apple".
1. Style line: "2D cartoon, bold clean outlines, flat colours, simple cel shading, warm pastel palette". User agrees.
2. Shot list: 1 wide, static, @pip sneaks toward the tree, 3 s (73 frames). 2 medium, static, @pip crouches, then
   jumps and grabs the apple, 2 s (49). 3 close-up, slow dolly in, @pip grins, "Mine!", about 2 s (the line, voiced with silence around it, sets it). 4 wide, static,
   the apple falls on @pip's head, 3 s (73). About 10 s; the user asks for more, add shots. save_shots "Apple thief".
3. Model sheet (cartoon-sheet-draft, 3 seeds); user picks; clean front view; saveAsElement `@pip`.
4. designVoice for @pip; user saves it; put it on @pip (a Character element).
5. `@orchard` background, empty, golden afternoon; saveAsElement, group Location.
6. Keyframes (cartoon-key-draft) with @pip's image as `<image1>` and the orchard as `<image2>`; user picks; finals.
7. Animate (ltx-animate-2s / 4s drafts as One-stage, then Two-stage); shot 3 with ltx-talk from the "Mine!" line
   (No keyframes off, keyframe in Frames at 0 s).
8. Sequence in order, trim, the user watches, export.
