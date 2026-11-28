# TransitionMarker

Built in Magic (alpha.6; owner picked B, the bowtie set): how a join between two blocks on a lane shows its transition.

- **Dissolve**: a 26px chip with a bowtie (two triangles meeting) in `transition` on `s2`, a 1.5px `transition` border, corners 7px, ringed in `s1`. The length shows in the Inspector ("Dissolve 0.5 s").
- **Cut**: the same chip with a single vertical line in `t2` and a `t2` border.
- **Fade in / Fade out**: at a lane's start or end, a small pill (`.jl.fi`, `s5`, 10px semibold) with a half bowtie and the length ("1 s").
- A join on hover shows "+": pick the transition and its length.

The consumer gives the kind and the length. Each marker has its right-click menu (ContextMenu).

Source: board `docs/mock/Magic.dc.html` (`.jn`, `.jn.cut`, `.jl.fi`).

- **Crossfade** (API 1.106, AudioFades): a join of two music or sound blocks plays as a crossfade, its chip sky `#86cdea` (`tj xf`) with the bowtie and the length; amber `#f2c14e` with the warning glyph (`tj dip`) when the outgoing take was made before it. A click opens the length popover (1, 2, 4 s or typed, Remove). Its menu offers Cut and Crossfade only. Joins sit above the playhead (z 9).
