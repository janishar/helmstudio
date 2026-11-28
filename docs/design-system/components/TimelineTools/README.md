# TimelineTools

The Timeline's tool row (timeline/tools.tsx, `tl-bar`) and its shortcuts sheet (`tl-ks`).

- Tools: a radiogroup `tl-seg` (3px padding, 12px corners, `s3`) of five `tl-tb` buttons: Select A, Trim T, Blade B, Range R, Overwrite P. Each is 32px, 9px corners, its board icon (16px, stroke 1.75), its name 13/500 and its key in a `mono` 10px `kbd` on `s2`. Off: `t2` on nothing. On: `s5`, `t1` text, the icon in `a2`, a small raised shadow, its `kbd` on `s3`. Arrow keys move the choice.
- Toggles `tl-tg`: 36px squares, 10px corners, `s4`, `t2`; pressed is a 22% `a1` tint with `a1` glyph. Snapping (N) and Hover scrub (⇧S); "?" opens the sheet.
- `tl-sep`: a 1px × 24px `s5` rule.
- Zoom `tl-zf`: one 36px `s4` field, 10px corners, − and + around the value (`tl-zv`, `mono` 12px, 52px wide) and Fit behind a 1px `s5` rule (⇧Z).
- The sheet `tl-ks`: four columns 20px apart, each a `heading` then key rows: a 96px key column (`mono` 11/18 `kbd` on `s3`, `t1`) and the action at 13px.

The consumer provides the current tool, the two toggles, the zoom text and the handlers. Names and keys are the app's.
