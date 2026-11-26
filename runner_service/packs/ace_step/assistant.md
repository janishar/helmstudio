# ACE-Step 1.5: notes for the Assistant

## Card
ACE-Step 1.5 makes songs and instrumentals from a style and lyrics, 10 s to 10 min, 48 kHz stereo, and edits them:
Repaint (a range made again), Cover (the same song in a new style) and Extend (time added at the end or start). Two
DiTs: 2B turbo (fast drafts) and XL turbo (better, bf16), each about 24 GB. A planner rewrites the style and picks
length, BPM, key and language when they are Auto. About 16 s for 30 s of song. Pick it for fast drafts, edits, set
BPM or key; MiniMax Music 3 sounds best but is slow and cannot edit.

## Good at
- Fast: about 16 s of work for 30 s of song on a 64 GB Mac (2 min: about 50 s), most of it the planner; Repaint,
  Cover and Extend take 1-2 s for a short song (no planner). [pack]
- Edits: Repaint, Cover and Extend work on any music take, also MiniMax Music 3's. [pack]
- Exact settings: BPM, key, time and length can be set; Auto leaves them to the planner. [pack]
- Instrumentals and cues: Instrumental on, with instruments and mood in the style. [pack]
- Section markers: a song with [verse] / [chorus] tags gets its sections on the stage. [pack]
- Repeats: the same settings and seed give the same song on the same machine. [pack]

## Weak at
- The planner rewrites the style: the song can drift from the words (genre, voice). Turn the plan off (No rewrite)
  to keep the style as written; then set Length, or it is 30 s. [pack]
- Vocals and lyrics are less clean than MiniMax Music 3's. [samples]
- Memory: about 24 GB either DiT for 30 s, 26 GB for 2 min, all of it kept loaded between runs. [pack]

## Modes and tasks
- **ACE-Step Music** (`ace-step-music`): style (prompt) + lyrics, with the plan. [pack]
- **ACE-Step Repaint** (`ace-step-repaint`): a song and a range (span); only the range changes. [pack]
- **ACE-Step Cover** (`ace-step-cover`): a song in a new style; Keep the song (0..1) is how close to stay. [pack]
- **ACE-Step Extend** (`ace-step-extend`): Add seconds at the End or Start; the song may last at most 600 s. [pack]

## Settings
- Style: genre, instruments, mood, voice, tempo in words ("warm lo-fi pop, Rhodes, soft female vocal"). [pack]
- Lyrics: one section tag per line in square brackets ([intro], [verse], [chorus], [bridge], [outro]), the sung lines
  under it. Empty lyrics make an instrumental. [pack]
- Steps: 8 (the turbo DiTs are distilled to 8). [pack]

## Pitfalls
- Repaint, Cover and Extend keep the source's length; Length on them does nothing. [pack]
- Extend past 600 s in all is refused. [pack]

## Recipes
- `song-with-plan`, `quick-instrumental`, `exact-tempo`, `close-cover`, `loose-cover` (model.yaml). [pack]

## Sources
- [pack] runner_service/packs/ace_step (model.yaml, torch.py, GOTCHAS.md).
- [samples] ~/GenAI/model-samples/ace-step-1.5*/results.md (measured on the M5 Pro 64 GB).
