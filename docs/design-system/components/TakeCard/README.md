# TakeCard

A take in the Gallery grid (gallery.tsx `TakeCard`): its picture, its title and a mono sub-line. Cards fill an auto grid of `size-card-min` columns, `space-rail` apart.

Built:
- The card is a button with `shadow-bevel`, `radius-field`, `space-card` between parts; disabled at 0.5 opacity.
- Picture: 16:10, `radius-field`, on `s1`, cover-fit. Image: its thumbnail. Video: its poster with a length badge (`hs-len`: play mark + mono 10px on 90% `s2`, top left). Audio: the `wave` tile with its length. Other kinds: a dashed `dash` box with a file icon and the kind.
- Selected (`aria-pressed`): a 2px `a1` outline inside the picture. Starred: a 14px `a1` star top right. While selecting, a ticked `a1` disc top left.
- Title: `take-title`, one line. Sub-line: `mono-sub` in `t2`.

Board only, not built (CreateMusicSound frame 7, picked 2026-11-27):
- Music: an album cover. The gradient comes from the prompt's mood; a vinyl disc peeks from the right; the title in `wordmark` white top left; a Music badge (`kind-music`) and the length bottom left.
- Sound and voice: one glowing orb on `s1` with a soft halo in the kind's colour: sky `kind-sound` with the audio-lines glyph, amber `kind-voice` with the mic glyph; a badge top right and the length centred under it in `t1b`. Colour and badge tell them apart.
- Hover on an audio card shows a round play mark (`t1` disc, `g` glyph, `shadow-play`).

The consumer provides the take (kind, title, thumbnail, length, starred), `on`, `selecting` and the sub-line. Pictures here are neutral stand-ins; the cover gradient is the board's example.
