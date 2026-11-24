# Timeline edit and export

## Before you start

- Every shot has a picked final clip (starred helps the user find them in the Gallery).
- Every clip has the same size (896 x 512) and 24 fps. A sequence takes its size and rate from its first take; a
  clip that differs gets resized. Same size and rate are needed for export to copy clips without re-encoding, but
  not enough: a trimmed clip, a dissolve, or clips from different families (LTX and H3 can differ in profile or
  codec header) make the export re-encode. A trimmed cut will re-encode; that is normal.
- Every line is voiced with the character's saved voice.

## Assemble

1. Go to the Timeline (navigate). Make a new sequence (newSequence), named after the film.
2. Add the finals in shot order on V1 (the edit action, op `add` with the take ids; times are in seconds).
3. Put the dialogue on audio tracks under their shots (A2 and up; one track per character keeps it tidy). newLine
   voices a new line in place.
4. Music or ambience, if any, on its own track.

## Cut

- Trim each clip's head and tail to the action (op `trim`, edge in / out). Leave a few frames of hold where a pose
  must read; cut the rest.
- Cut on movement: end a shot during a turn or a jump, start the next one in the same move.
- Use a dissolve only for a time or place change (op `set`, transition_in, frames). Cartoon cuts are mostly hard cuts.
- Lower a clip's own sound under dialogue (op `set`, gain_db) or mute it (mute).
- A wrong shot: use the clip's shot actions (shot: retake, extend, redo, replace) instead of starting over. After
  changes, rerender re-renders the changed shots.
- Edits are saved, and each one can be undone.

## Check

- Ask the user to watch the whole cut before export. Note fixes per shot number.
- Look for: a character changing design between shots, a style jump (a shot turning photo-real), a line out of sync,
  a pop in the sound at a cut.

## Export

- export (an MP4 take). It shows a card first; the result is a take in the Gallery named after the sequence.
- A blocked export says why (an offline clip after a take was deleted, an empty V1): fix that and export again.
