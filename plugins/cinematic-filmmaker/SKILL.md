# Cinematic filmmaker

A guide for making a short film that looks live action, from an idea to an exported MP4, in helmstudio. Read it as
craft advice and an order of work; the screen and the workflow list are the truth for this install (a setting that
is greyed stays greyed, and a workflow not `downloaded` can't run).

Extra files you can read with read_skill_file:
- `script-template.md`: logline, treatment, scenes and dialogue, with an example.
- `shot-list-template.md`: the shot list format, lengths in frames, and how to save it with save_shots.
- `character-sheet.md`: prompts for a turnaround and expressions, and how to save a character with a voice.
- `camera-and-lighting.md`: shot sizes, camera moves, lens, light and colour words, coverage and the 180-degree rule.
- `edit-checklist.md`: the Timeline pass, continuity checks and export.

## The order of work

Work in this order. Each stage feeds the next, and fixing something early is much cheaper than fixing it in a
finished shot.

1. Script and shot list. Checkpoint: the user says yes to the shot list.
2. Characters: a sheet each, saved as an element with a voice. Checkpoint: the user picks each character.
3. Places, props and the look. Checkpoint: the user picks the look.
4. Shots: a keyframe, a draft, then the final. Checkpoint: the user picks a keyframe and a take per shot.
5. Sound: dialogue lines, then ambience. Checkpoint: the user hears the lines.
6. Edit on the Timeline and export. Checkpoint: the user watches the cut before export.

At each checkpoint show what you made, say which one you'd pick and why, and wait for the user's pick. Between
checkpoints, choose and say why; ask only when a choice really matters (a long run, a missing model).

## 1. Idea to script

- Turn the idea into a logline (one sentence: who wants what, and what stands in the way), then a short treatment
  (a paragraph per act), then scenes. Keep a first film to 30 to 90 s: 6 to 15 shots.
- Write the dialogue as `@name: line` (or `@name (style): line`). That is the script format the Dialogue workflows
  read, so it goes straight in later. Keep lines short: one thought each.
- Break each scene into a numbered shot list: shot size, camera move, action, length, the line spoken (if any), and
  the workflow. Use `shot-list-template.md`.
- One shot = one action. If a shot has "and then", split it.
- Show the user the whole shot list and wait for a yes before any generating. A change here costs nothing; a change
  after ten shots costs ten runs.
- Keep it: save the shot list with save_shots (the film's title; each shot's number, what happens, length in
  seconds and the line spoken) as soon as you write it, and again when it changes. It shows under Memory, Projects,
  and every chat on every screen reads it, so the next chat picks up at the right shot. When the user picks a
  shot's final take, set that shot's take_id.
- Working on "shot 3" or "the next shot", read it from the saved list. If the shot is not there, don't guess:
  ask with pick-one choices (the shots left, plan it first, or describe it).
- Keep a short project note too with remember (kind `project`: the cast handles, the look, where you are), in
  plain words.

## 2. Characters

- Make a character sheet per character first: one image, plain light-grey background, the same person in a front,
  three-quarter, side and back view, plus a row of expressions. See `character-sheet.md` for the prompt.
- Use Qwen Image (recipe `character-sheet`) for most sheets; MiniMax H3 Image (recipe `character-sheet-h3`) for a
  larger, sharper one when the host can take it. With Qwen Image, draft with Turbo, then Base for the final;
  MiniMax H3 Image has one variant (Base, 6 fixed steps) and no draft mode.
- Make 2 to 4 takes (seeds), look at them with the user (look_at can compare them), and let the user pick.
- Save the pick with saveAsElement (handle like `mara`). From then on write `@mara` in every prompt with that
  character, written the same way each time. Use only handles the user wrote, the form holds or a library read
  returned.
- Voice: design one in words (designVoice, the Qwen3-TTS Voice design dialog: age, pitch, pace, texture) or clone
  one from a clip the user has the right to use (cloneVoice, 3 to 30 s, ZONOS3). Let the user hear it (hear), then
  save it. Link it to the element with editItem, so `@mara:` lines speak in that voice.

## 3. Places, props and the look

- Locations and props that appear in more than one shot become elements too (`@diner`, `@red_car`): make a clean
  still of each, pick one, saveAsElement.
- The film's look: make one still that has the grade you want (light, colour, film stock words), then saveAsLook
  (name like `Night diner grade`). A look goes only where the form has a look tile: Qwen Image keyframes (every
  group) and LTX Text / Image to video (film, camera and lighting only, no colour). Save the grade as a film or
  lighting look so it fits both, and put it on every keyframe: the LTX shot starts from that frame. MiniMax H3 and
  LTX Audio to video have no look tiles: repeat the look's words in the prompt. Looks nudge; the words in each
  prompt still matter.
- Elements are strong only where the family takes references (Qwen Image edit, MiniMax H3 Image, MiniMax H3
  References); with LTX they are prompt-only. That is why every LTX shot starts from a keyframe.

## 4. Shots

For each shot, in shot-list order:

1. Keyframe. Qwen Image with the shot's @elements and look (recipe `keyframe-draft` to try framing, then
   `keyframe-still`). The prompt describes the frame: shot size, subject, action at that moment, place, light,
   lens. Make 2 to 4 seeds; the user picks.
2. Draft. useIn the keyframe into LTX-2.5 Image to video (recipe `ltx-quick-draft`: the final's size and length,
   One-stage in Quality, so it is fast). Check the action, the camera move and the timing, not the detail.
3. Final. Same seed, prompt, size and length, recipe `ltx-final-shot` (Two-stage). Only Quality changes, so the
   timing holds. Use Detail for a final close-up (sharper skin,
   hair, edges; Mac only; needs the Detailer add-on).

Prompting a shot:
- Camera move first, plainly: "Slow dolly in. @mara sits at the counter ...". One camera move per shot.
- Then the action with its speed, then the sound: "Rain hisses on the window; a fridge hums."
- LTX prompt beats (up to 8 timed lines) time the parts: 0 s "@mara holds still", 1.5 s "she looks up", 3 s "she
  smiles". Leave a short hold at the start and end of a shot so it cuts cleanly.
- Lengths: LTX on the 8k + 1 grid at 24 fps (49 about 2 s, 73 about 3 s, 97 about 4 s, 121 about 5 s; up to 321).
  MiniMax H3 is 5 + 17k at 24 fps (73 about 3 s, 124 about 5 s, up to 362).

When to use MiniMax H3 instead:
- A character must match closely, or two characters share a shot: MiniMax H3 References with the elements' images as
  <Picture n> (up to 9 images, 3 videos, 3 sounds). Draft with `h3-references-draft` (4 steps), final with
  `h3-references-shot` (20 steps, same seed, same 124 frames).
- A move must land exactly: MiniMax H3 Image to video with a First frame and a Last frame (two keyframes), recipe
  `h3-first-last-shot`. References never go with first / last frames.
- With LTX, Frames (up to 8 images with At, Strength, Binding) can also pin a later moment.

Fixing a shot without starting over:
- A bad part: LTX-2.5 Retake regenerates a span of the clip (useIn the video).
- Too short: LTX-2.5 Extend adds 0.1 to 10 s after (before needs Dev).
- To continue a shot from where it ended: frameIn its last frame into the next shot's input.

## 5. Sound

- Dialogue: for one character's line, ZONOS3 Voice-over (recipe `voice-line`) with the saved voice; for an exchange,
  ZONOS3 Dialogue (recipe `dialogue-scene`) with the script's `@name: line` lines, every @name in the Cast.
  Qwen3-TTS Dialogue works with its preset speakers (a designed voice drifts between lines there).
- When switching to a voice workflow, rewrite the box: Voice-over takes only the words to say, never the scene
  description, or it reads the description aloud.
- Delivery: (style) words per line (whisper, calm, tense, angry, slow ...) or ZONOS3's Emotion, Energy, Whisper and
  Speaking rate. Let the user hear each scene's lines before you make the shots that use them.
- Lip movement: LTX-2.5 Audio to video (recipe `ltx-dialogue-shot`) with the line (up to 13.375 s) and the
  keyframe; the mouth follows the sound, but it is not a lip-sync model, so keep talking faces medium or wider when
  you can, or cut to the listener.
- Ambience and effects: LTX-2.5 and MiniMax H3 make sound with the picture. Describe it in the prompt (room tone,
  rain, traffic). On LTX only, Audio off writes a silent MP4 when a shot will sit under a voice line. MiniMax H3
  has no audio switch: on the Timeline, mute that clip (edit, `set` with `mute`) or lower its `gain_db`.

## 6. Edit

- On the Timeline, newSequence with the film's title, then add every final take in shot order (edit, an `add` op).
- Trim heads and tails (`trim`), cut a clip in two (`split`), remove with `ripple: true` to close the gap, `lift`
  to leave it. Cut on action: end one shot mid-move, start the next on the same move.
- Voice lines: newLine with the text and the character's voice puts a line on an audio track; or place a dialogue
  take there. Line them up with the shot where the face speaks.
- A weak shot: shot (kind `retake`, `replace` or `redo`) on its clip. It opens its own Generate card, and that run
  makes the new take in the slot. The rest of the cut stays.
- rerender re-runs chosen clips with their own settings. Select those clips first: with nothing selected it
  re-renders the whole sequence.
- Watch it through with the user (`edit-checklist.md`), then export. Export makes an MP4 take.

## Keeping a film consistent

- The same @elements, written the same way, in every shot they appear in.
- One saved voice per character, linked on the element.
- One look per scene (usually one for the film), on every keyframe and LTX shot that has a look tile; its words in
  the prompt of every MiniMax H3 and Audio to video shot.
- One aspect: keyframes and finals at the same size (1344 x 768 in every recipe, MiniMax H3 included) so nothing is
  cropped or stretched and the export can stream copy.
- Keep the seed of a draft the user likes when moving to the final. Same seed and settings repeat a take.
- Light and time of day stay the same within a scene: say them in every shot's prompt ("late afternoon, warm low
  sun from the left").

## Many shots at once

When a film has many similar shots (keyframes for a whole scene, or every final once drafts are approved), a batch
saves clicking: Pipelines, New batch, one row per shot, the same looks, a few seeds. For a pick per row, run the
batch over a pipeline that has a step then a gate (the user saves one in Pipelines). A batch over a plain workflow
has no gate: its takes are picked afterwards in the Gallery (filter by the batch). Suggest it; the user starts it.

## Recipes

Only the ones this install can run are offered. Set them with the form's own actions (switchTo the workflow, then
change), then Generate waits for the user.
- keyframe-draft / keyframe-still: Qwen Image at 1344 x 768, Turbo then Base 40 steps.
- character-sheet: Qwen Image 1536 x 864, Base 40 steps. character-sheet-h3: MiniMax H3 Image 2560 x 1440.
- ltx-quick-draft: LTX Image to video 1344 x 768, 97 frames, pick One-stage. ltx-final-shot: same, Two-stage.
- ltx-dialogue-shot: LTX Audio to video at 1344 x 768.
- h3-references-draft (124 frames, 4 steps) and h3-references-shot (124 frames, 20 steps), both 1344 x 768.
- h3-first-last-shot: MiniMax H3 Image to video, 1344 x 768, 124 frames, 20 steps. 1344 x 768 is H3's largest
  canvas, so it needs more memory than the 864 x 480 default.
- voice-line: ZONOS3 Voice-over, neutral delivery. dialogue-scene: ZONOS3 Dialogue, 0.5 s between lines.

## A worked start

Ask: "a 45 s film about a night-shift waitress who finds a stranger's letter".
1. Logline, a 3-scene treatment, 9 shots with lines. Show them; save_shots; wait for yes.
2. Sheets for @june and @stranger; the user picks; save as elements; design two voices, link them.
3. A still of the diner, saved as `@diner`; a grade still saved as the look `Night diner`.
4. Shot 1: wide, static, 3 s. Keyframe "Static wide shot. @diner at night, @june wiping the counter alone,
   fluorescent light, rain on the windows." Draft, then final, both at 73 frames.
5. Lines in ZONOS3 Dialogue; the close-ups that speak through Audio to video.
6. All 9 finals on a sequence, trim, the lines placed, watched together, exported.
