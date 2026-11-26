# Info

The small "i" button whose popover holds an explanation, so the screen itself stays short (kit.tsx `Info`). This is the app's tooltip; plain hover hints use the native `title`.

- Button (`info`): `size-info` round, `ib` fill, `it` glyph, 600 11/18, 6px after the text it explains.
- Popover: a `pop`, at most 288px wide, 13px `t1`, anchored under the button and spanning right, 6px below.

The consumer provides the explanation (one or two plain sentences) and, when it differs, the button's label (default "More information"). Wrap it in a span-safe place: it may sit inside a paragraph or heading.
