# Edit checklist

## Assemble
1. Timeline: newSequence with the film's title.
2. edit with an `add` op of the final takes in shot order (V1). Their sound comes with them.
3. Voice lines: newLine (text and the character's voice) puts a line on an audio track, or add a dialogue take on
   an audio track (A2 to A9). Place each where the face speaks (`move` with `at` and `track`).

## Cut
- `trim` (edge in or out, delta in seconds) heads and tails: start each shot just before its action, end just after.
- `split` (blade) at a time to cut a clip in two; `remove` with `ripple: true` to drop a part and close the gap;
  `lift` to leave the gap.
- `roll` moves a cut between two clips; `slip` changes which part of a clip shows; `slide` moves a clip between its
  neighbours.
- `set` on a clip: `transition_in` for a dissolve (use few: a time jump or the end), `gain_db` or `mute` for sound.
- `marker` to note a problem at a time for the user.
- Each edit is one undo step; the last 100 revisions are kept.

## Watch for
- Same faces, clothes and hair in every shot (the @elements).
- Same light direction and time of day within a scene; same look.
- Eyelines and the 180-degree line hold; screen direction is kept across cuts.
- Lines land on the right face; no line cut off; levels even between shots.
- No shot holds too long after its action; cuts on action feel smooth.
- The film's length is what the user asked for.

## Fix in place
- A weak shot: shot on its clip, kind `retake` (a span), `extend` (more time), `replace` or `redo`. Its own Generate
  card makes the new take in the slot.
- rerender re-runs chosen clips with their own settings; select them first, or it re-renders the whole sequence.
- A missing shot: shot kind `new` with `at_s` at the time it goes (or on an empty slot's clip), or kind `insert` on
  the clip it should follow.
- After a run of edits that went wrong, the user can bring back a kept revision from the sequence's history; you
  can't do it yourself.

## Export
- Watch the whole cut with the user first.
- export, with no preset (it exports at the sequence's size). The card shows the mode, size and fps, the length
  and the machine. It makes an MP4 take: stream copy when every clip matches,
  else re-encode. Keeping one size and frame rate (24 fps) for every shot keeps it fast.
- Update the project note: done, the sequence name, the export. The shot list's take_ids are the cut's takes.
