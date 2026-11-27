# Field

A text field or select (`in`): `size-field` tall, full width, `s4`, `radius-field`, 13px `t1`, `space-chip-x` side padding, no border.

- A `label` (12px `t2`) sits above it.
- The theme select (`ThemeSelect`) is a native select in this style listing the seven themes by their names.
- Only fields and code select text; the rest of the app does not.
- A multi-line text box keeps a fixed height that suits it and scrolls inside; it never grows the page. Its Expand (the prompt's: a 28px `s3` square, top right, shown while the text overflows or on focus) opens the same text in the big editor dialog (owner, 2026-11-27: "check all the prompt and text section in create"). In Create: the prompt and every composer text 2 lines (44px); Advanced texts (the negative prompt) 2 lines; the Lyrics 6 lines (126px); the dialogue script 8 lines (176px) in both its views; an element's and a look's Text 3 and 2 lines; Describe the voice 3 lines; a take's Cover / Vary style 3 lines. In Magic's request: Prompt, Line and Lyrics 66px. None of them can be dragged taller.

- A list's search is the SearchField (the Field look with a glyph, count, × and the `/` hint), not a plain field.

The consumer provides the label, value and placeholder.
