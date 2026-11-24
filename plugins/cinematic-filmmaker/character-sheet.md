# Character sheets

A character sheet is the reference every later shot leans on. Get it right once, before any shot.

## The prompt
Plain words, the same description you will reuse in shots. A template:

> Character sheet of [name], [age], [build], [face: eyes, hair, skin], wearing [clothes with colours]. Four full-body
> views side by side on a plain light-grey background: front, three-quarter, side, back. Below, a row of four head
> close-ups: neutral, smiling, worried, angry. Even soft studio light, no shadows on the background, photorealistic,
> 50mm lens, sharp focus.

- Keep the clothes, hair and colours exact and short; you will paste the same words into every shot's prompt.
- A plain background keeps the reference clean: the element should carry the person, not a room.
- Even, flat light shows the face and clothes truly; the film's mood light comes later in the shots.
- One character per sheet.

## Making it
1. Qwen Image, recipe `character-sheet` (1536 x 864). Try Turbo first for the layout, then Base 40 steps.
   MiniMax H3 Image (recipe `character-sheet-h3`) gives a sharper, larger sheet when the host can take it (about
   39 GB peak on a Mac).
2. 2 to 4 seeds. Look at them with the user; look_at can check things like "are the four views the same person, same
   clothes?".
3. Fix a near miss with an edit instead of a new roll: put the take in as a reference (useIn Qwen Image) and say only
   what to change ("make the coat dark green, keep everything else").
4. The user picks. saveAsElement with a short lowercase handle (`june`). A portrait still of the face (a medium
   close-up, same light) is a good second element image if the dialog lets you add one.

## The voice
- Design: designVoice with words: age, gender, pitch, pace, texture, accent, mood ("a woman in her 30s, low, warm, a
  little dry, unhurried, American"). hear lets the user try it. Save it.
- Clone: cloneVoice from an audio take or a video's sound, 3 to 30 s of clean speech (ZONOS3), only from a voice the
  user has the right to use.
- Link: editItem on the element and set its voice, so `@june:` lines in a Dialogue script speak in it.
- ZONOS3 takes cloned voices and designed voices saved with their clip; Qwen3-TTS Dialogue works best with its preset
  speakers.

## Places and props
The same way, without a voice: a clean still of the place (wide, no people, the film's time of day) or the prop (on
a plain background), picked by the user, saved as an element (`@diner`, `@letter`).

## The look
One still that has the film's grade: light, colour, contrast, film stock words (see camera-and-lighting.md).
saveAsLook with a plain name ("Night diner"), as a film or lighting look so it fits LTX shots too (they take film,
camera and lighting looks, no colour). Put it on every Qwen Image keyframe and LTX shot of the scene; for MiniMax H3
and LTX Audio to video, which have no look tiles, repeat its words in the prompt.
