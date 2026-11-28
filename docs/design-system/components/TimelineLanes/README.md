# TimelineLanes

The Timeline's lanes (timeline/lanes.tsx): a ruler, then V1 (the picture), A1 (the clips' own sound) and the added tracks, under one playhead.

- The block is a `panel` with 28 / 12 corners (`radius-panel` / `radius-panel-tail`), 12/14/14px padding; rows 6px apart and scroll sideways together.
- Lane heads: a sticky 72px column on `s1`, the name in `mono` 11px `t2`; a track's head adds 22px icon buttons on `s3` (mute, solo). A muted track's name is struck through.
- Ruler: 22px, `mono` 10px. In the app a canvas draws it: a 1px `s4` base line, labelled ticks full height in `ln` (`dash` when major) with their time in `t2` (`t1b` when major), small 5px ticks in `s5`. The preview draws the same with gradients.
- Lanes: V1 and tracks 56px, A1 36px (its clips 1px `ln` on `s2`).
- Playhead `tl-ph`: a 2px `t1` line through every lane with a 12×10 triangle head on the ruler, above the clips (z 15).
- Markers: 10×14 flags on the ruler in their colour (`#ff6b6b`, `#ff9f43`, `#f2c14e`, `#8bd17c`, `#6cb6ff`, `#c9a7ff`, or `a1`); a picked one gets a 2px `t1` outline.
- A range (I and O): 2px `a2` sides over a 12% `a2` wash, from under the ruler down, with `mono` 10px `on2` times on `a2` above each side.
- "Track" adds a track: a bevel chip, 32px, 10px corners.

The consumer provides the clips (TimelineClip), the pixels per second, the playhead frame, markers and the range.
