# 41 · Audio fades, crossfades and ducking

alpha.6 row 82 (owner, 2026-11-28: "Yes, after refactor"; decided one by one the same day). Builds on the
Timeline's clip and track gain and Magic's sound lanes (plan 39).

## Where it stands

- The Timeline has clip and track gain but no sound fades. Its preview already fades sound inside a picture
  dissolve (`frame_at`'s `fade`), so the player has the hook.
- Magic's lane fade in and out are picture only; a sound lane's fade is skipped on Send ("the Timeline has no sound
  fades"), and so is a transition between two sound blocks (the Assistant already adds these).

## Decided by the owner (2026-11-28)

1. **Fades per clip** ("Per clip"): every Timeline audio clip and every Magic voice, music or sound block has a fade
   in and a fade out, 0 to 10 s, set by a handle at the clip's top corners or the right-click menu. Magic sends a
   block's fades onto its Timeline clips; a sound lane's fade in and out become its first and last block's fades.
   The player applies them live, Export with ffmpeg.
2. **Crossfades by longer cues** ("Make cues longer"): a music or sound block with a crossfade after it runs with
   that much extra at the end, so the outgoing take overlaps the next block across the cut. A take made before,
   without the extra, falls back to a dip (each side fades over half the length), with a warning.
3. **Ducking, auto, on in Magic** ("Auto, on in Magic"): one setting per board and per sequence: music dips by an
   amount (default −12 dB) wherever a voice clip plays, easing over 0.3 s, worked out from the voice clips' times
   so the player and Export match. On by default in Magic, off by default on the Timeline.
4. **Into alpha.6** ("Into alpha.6"): built now, boards first, then alpha.6 is released with it.

Every part also reaches the Assistant (tool args, the director skill) and helm mcp, and the right-click menus and
the design system get the new handles and settings.

## Next

Boards (fade handles on a clip, the crossfade between two music blocks, the ducking setting) for the owner's OK,
then the contract (clip and block `fade_in` / `fade_out`, the ducking setting, the crossfade on sound lanes), then
the build in phases.
