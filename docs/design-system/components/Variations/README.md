# Variations

Board only, not built (CreateMusicSound frame 4): Sound's variations as a grid on the stage. Variations are cheap, so Sound starts at 4.

- Grid `vars`: two columns, 12px apart, up to the stage's 860px.
- Card `var`: `s1`, 14 / 6 corners, 12px padding, a 2px border that is clear, or `a2` when kept (`aria-pressed`). Top row `row1`: a 32px play disc `pb` (`t1` with a `g` glyph), the seed and length in `mono` 11px `t2`, "kept" at the end. Under it the waveform `wb`, 74px.
- Each card plays on its own; a click keeps it in the strip; Keep all (a `chip` on the stage) keeps every one. Its right-click menu: Play, Use in, Repaint…, Vary…, Extend…, Download, Delete (ContextMenu).

The consumer provides the variations (seed, length, audio), which are kept and the handlers.
