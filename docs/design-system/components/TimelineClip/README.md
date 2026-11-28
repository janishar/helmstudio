# TimelineClip

A clip on a Timeline lane (timeline/lanes.tsx, `tl-clip`): a box placed at its time, its title and a mono sub-line, its look from what it holds.

- Box: `tl-clip`, 8px corners, 6/8px padding, 11/14 text, a 3px `s1` outline drawn 1px inside so neighbours read apart; `a1` outline on keyboard focus. Title 12px 600, sub-line `mono` 10px, both one line.
- Looks (the consumer picks one): image `img` (1px `a1`, 14% `a1` fill), video `vid` (1px `a2`, a 14% coral fill `rgba(255,122,89,.14)`), audio `aud` (1px sky `#86cdea`, a 12% cyan fill), a shot being generated `shot` (1px `a2` on `s2`, with a 4px `a2` bar on `s5` and "Generating n / m"), no take yet `empty` (dashed `dash`, `t2` text), offline `off` (dashed `bad`, `bad` text). The sky and coral values are literals in the code, not tokens.
- Selected: `on`, a 2px `t1` border.
- A V1 clip with a filmstrip is `fs` (text at the top, 4/7px padding); an audio take's clip is `wv` (its text on an 82% `s1` plate over the waveform).
- Trim handles: two 7px `tl-edge` strips at the left and right edges with the ew-resize cursor; drawn only while trimming is possible.

The consumer provides the position and width in pixels (time × pixels per second), the height (56 on video lanes, 36 on A1), the look and the text. The filmstrip and waveform canvases are not shown here. Every clip has its right-click menu (ContextMenu).

- **Fades** (API 1.106, AudioFades): an audio clip with a take draws its fade curve over the waveform, its fade handles at the top corners under the title row (hollow on hover, filled when selected), a blend tail and the crossfade badge at its end, and on a music clip the ducked stretches; see AudioFades. Its inspector gets Fade in and Fade out tiles beside Gain ("1.5 s", typed; "0 to 10 s each. Drag a top corner of the clip, or type here.").
