# TimelineOverlays

What the Timeline draws while you trim, snap, blade or slip (timeline/lanes.tsx, tools.tsx; board TimelineTools).

- Tag `tl-tag`: a 20px label above the ruler, 6px corners, `mono` 11/500. A trim delta or a time: `a2` with `on2`. Snapped to something: `ok`, `a1` with `on1` and a magnet. A limit (end of take, a neighbour): `warn`, `t1` with `g`.
- Snap line `tl-snapl`: a 2px `a1` line with an 8px `a1` glow, through the lanes.
- Blade line `tl-bl`: a 2px dashed `a2` line where Blade would cut; the cursor badge `tl-cur` beside it is a 24px `a2` square, 8px corners, the tool glyph in `on2`.
- Trim edge `tl-edgebar`: a 6×56px `t1` bar on the edge being dragged; `warn` turns it `bad` when the drag is clamped.
- Ghost `tl-ghost`: the 56px space a trim frees or takes, dashed `dash`, 8px corners, 135° `t2` hatching at 12%.
- Slip source `tl-src`: the clip's In and Out frames (`tl-pv`, 150px 16:9, dashed `dash`) either side of the take's bar (`tl-take`, 16px, striped `s3`/`s2`) with the used part `tl-used` (2px `a2` border on an 18% `a2` wash), on `in`, 16 / 8 corners.

All overlays ignore the pointer and sit above the clips (z 20; the ghost z 5). The consumer places them in lane pixels.
