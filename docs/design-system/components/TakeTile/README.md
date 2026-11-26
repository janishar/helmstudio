# TakeTile

A take in Create's take strip (create.tsx `TakeTile`): small, labelled by its seed, the current one ringed.

Built:
- Image or video: `size-take-tile-w` by `size-take-tile-h`, `radius-small`, `s1`, 1px `s5` border, `shadow-bevel`; the picture fills it; a mono 11px label bottom left on 90% `s2` ("seed 7", "seed 8 · 5 s"). Current: 2px `a1` border. An image tile drags into a reference slot.
- Sound: `size-sound-tile-h` tall, the `wave` over "seed 12 · 0:07". Current: 2px `a2` border.
- A starred take shows a 12px `a1` star top right. Its ⋯ (Take actions) opens the take's right-click menu.

Board only, not built (CreateMusicSound frame 7): a music tile is a small cover with the disc peeking from the left; sound and voice tiles carry a 26px orb in `kind-sound` / `kind-voice`; the label stays mono "seed 31 · 2:10"; current is a 2px `a2` border.

The consumer provides the take, whether it is current, and the pick handler; the aria-label reads "Take, seed 42, 0:06, starred".
