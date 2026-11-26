# MMAudio: notes for the Assistant

## Card
MMAudio large 44k v2 makes sound effects: from a short description, or for a video (its picture read and kept in
sync, steered by the text). Two workflows: Sound and Sound from a video, which gives two takes, the sound and the
video with that sound. 44.1 kHz mono, about 5 GB loaded; 8 s of sound in about 9 s on a Mac. Pick it for foley on a
video, or a quick effect from words.

## Good at
- Sound for a video: the picture (8 fps for the content, 25 fps for the timing) drives what is heard and when, so
  hits, steps and engines line up with the frames. [card] [pack]
- Short concrete effects from text: rain, footsteps, a door, an engine, crowds. [card] [pack]
- Repeats bit for bit: the same text, settings and seed on the same machine give the same sound. [pack]
- Fast: 8 s of sound in about 9 s cold, about 2 s for a 2 s effect once loaded. [pack]

## Weak at
- Trained on 8 s clips: longer sounds (up to 30 s here) work but hold less detail and drift. [card]
- No music and no speech: use a music or voice model. [card]
- Mono only, and levels are not normalised: loud subjects can clip. [pack]
- Weights are for non-commercial use (CC BY-NC 4.0); the CLIP helper for research only. [card]

## Modes and tasks
- **MMAudio Sound** (`mmaudio-sound`): a text in, one sound out (Length seconds). Studio makes 4 by default (up to 8)
  for picking a variation. [pack]
- **MMAudio Sound from a video** (`mmaudio-video`, action "Add sound"): a video take in (1 to 30 s), the sound and the
  video with it out. Length Auto takes the whole video; a Length cuts both to its first seconds. Any sound the
  video had is replaced. [pack]

## Settings that matter
- **Text** (`prompt.text`): name the source and the action in a few words ("old wooden door creaking open"). For a
  video it is optional and only steers. [pack]
- **Avoid** (`sound.avoid` / `foley.avoid`): what must not be heard (the negative prompt), e.g. "music, speech". [pack]
- **Length** (`sound.seconds` 1-30, `foley.seconds` Auto or 1-30). [pack]
- **Guidance** (cfg 4.5): higher follows the text more closely with less variety. **Steps** 25: more is slower and
  rarely better. [card] [pack]

## Sources
- The model card and README of hkchengrex/MMAudio [card]; the pack's code, goldens and the sample run on the
  owner's Mac [pack]. Owner's takes: not read yet.
