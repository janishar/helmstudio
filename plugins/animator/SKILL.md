# Animator

A guide for planning and making animated shots in helmstudio. Read it as craft advice; the screen and the workflow
list are the truth for this install (a setting that is greyed stays greyed).

Extra file you can read with read_skill_file: `camera-moves.md` (camera moves and the prompt words for them).

## 1. Plan the shots first

- One shot = one action. "She opens the door" is a shot; "she opens the door, walks in and sits down" is three.
  A clip that tries several actions blurs them or drops some.
- Write a shot list before generating: number, what happens, camera move, length, workflow. For example:
  1. Wide, static. Mara stands at the window, rain outside. 3 s.
  2. Medium, slow dolly in. She turns her head toward the door. 2 s.
  3. Close-up, static. Her eyes widen. 2 s.
- Keep shots short. Most shots in a film are 2 to 5 s. LTX-2.5 clips go to 321 frames (about 13 s) and MiniMax H3
  to 362 frames (about 15 s), but longer clips cost more time and memory and drift more.
- Start wide to show where we are, then go closer for feelings and details. Cut on an action (a turn, a step) so
  the next shot can pick it up.
- Ask the user only when a choice really matters (a long run, a missing model); otherwise pick and say why.

## 2. Timing

- Both video families here run at 24 fps. Seconds x 24 = frames.
- LTX-2.5 lengths are on an 8k + 1 grid: 9 to 321 frames; 49 (default) is about 2 s, 73 about 3 s, 97 about 4 s,
  121 about 5 s. Keep the frame rate at 24 (it was trained there; far values drift). Auto length lets the model
  predict 1 to 20 s from the prompt.
- MiniMax H3 lengths are 5 + 17k frames at 24 fps only: 73 about 3 s, 124 about 5 s (default), 362 about 15 s.
- Give the action room: a hold (a still moment) at the start and end makes a shot easier to cut. Say it in the
  prompt: "holds still, then ...", "... and holds".
- Anticipation: a small move the opposite way before a big move (crouches, then jumps). Follow-through: parts keep
  moving after the body stops (hair, coat, tail settle). Describe both in words; the model follows plain motion
  words better than technical terms.
- Easing: real moves start slow and end slow. "Slowly turns", "gently settles", "eases to a stop".
- LTX prompt beats: up to 8 timed lines that steer picture and sound in order (0 s shares the rest equally). Use
  them to time a shot's parts: 0 s "holds still", 1 s "turns toward the door", 2 s "smiles".

## 3. Motion

- Keep motion simple per shot: one subject move plus at most one camera move.
- Say who moves, how and how fast: "the fox trots left to right, steady pace". Name the speed (slowly, quickly,
  suddenly).
- Fix the start and the end when you can:
  - LTX-2.5 Image to video (Animate): Frames, up to 8 images with At (seconds), Strength (0 to 1) and Binding
    (auto, replace, guide). An image at 0 s starts the clip from it; later ones are passed through.
  - MiniMax H3 Image to video: First frame and / or Last frame. Give both to land a move exactly.
- Make the still first (Qwen Image or MiniMax H3 Image), then animate it: the look is set before any motion runs.

## 4. Camera

- Put the camera move first in the prompt, named plainly: "Slow dolly in. A woman at a rainy window ..."
- One camera move per shot. Two moves (a pan and a zoom) fight each other.
- Static is a choice: use it for dialogue, close-ups and moments that need to read clearly.
- See `camera-moves.md` for the moves and the words that work.

## 5. Keep characters and places the same across shots

- Use elements (@name: character, location, prop) in every shot's prompt, written the same way each time. Use only
  @handles the user wrote, that the form holds, or that a library read returned.
- Elements are strong only where the family takes references (MiniMax H3, Qwen Image edit); with LTX they are
  prompt-only. For a character that must match, use MiniMax H3 References or start each LTX shot from a still made
  with that character.
- MiniMax H3 References: up to 9 images (<Picture n>), 3 videos (<Video n>) and 3 sounds (<Audio n>), 12 in all.
  References never go with first / last frames.
- Looks (film, camera, colour, lighting) nudge the style; use the same looks on every shot of a scene.
- Same seed and settings repeat a take. Keep the seed of a draft the user likes when moving to the final.

## 6. Choosing the workflow

- LTX-2.5 (fast, keyframes, retake / extend, audio to video):
  - Text to video and Image to video run Distilled only (8 steps, about 10x faster than Dev).
  - Size 128 to 1920 px on a 64 px grid, default 704 x 448.
  - Recipe: One-stage for a quick look, Two-stage (default) for normal work, Detail for a final close-up (sharper
    hair, edges, texture; about a third slower; Mac only; needs the Detailer add-on).
  - Retake regenerates a span of a clip; Extend adds 0.1 to 10 s after (or before, with Dev).
  - Audio to video: a sound up to 13.375 s sets the length; lip movement follows the sound (not a lip-sync model).
- MiniMax H3 (higher quality, first-last frame, references; slower):
  - Size sides 256 to 1344, step 32, default 864 x 480 (16:9).
  - Presets: Draft (4 steps), Fast, Turbo (8 steps, needs the Turbo LoRA add-on), Default (20 steps), Reference
    (50 steps, every layer).
- Draft fast, then the final: a short, small draft (LTX One-stage, or H3 Draft) to check the action and camera,
  then the same seed with the final settings.

## 7. Recipes

This skill suggests settings as recipes (only the ones this install can run are offered):
- ltx-animate-2s: LTX Image to video, 49 frames at 24 fps.
- ltx-animate-4s: LTX Image to video, 97 frames at 24 fps.
- ltx-text-3s: LTX Text to video, 73 frames at 24 fps.
- h3-first-last-5s: MiniMax H3 Image to video, 124 frames, 20 steps.
- h3-draft-3s: MiniMax H3 Text to video, 73 frames, 4 steps, no LoRA.
- h3-references-5s: MiniMax H3 References, 124 frames, 20 steps.

Set them with the form's own actions (switchTo the workflow, then change), then Generate waits for the user.

## 8. A worked plan

Ask: "a 3-shot scene of @mara finding a letter".
1. Make or pick a still of @mara in the room (image workflow).
2. Shot 1, wide, static, 3 s: LTX Image to video from that still, 73 frames. "Static shot. @mara walks to the
   desk and stops."
3. Shot 2, medium, slow dolly in, 2 s: 49 frames. "Slow dolly in. @mara picks up a letter from the desk."
4. Shot 3, close-up, static, 2 s: "Static close-up. @mara's eyes move across the letter, then widen."
5. Same looks on every shot; put the takes on the Timeline in order.
