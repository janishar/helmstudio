# LyricsBox

The Lyrics box under Music's style prompt, with section tag chips that insert at the caret and an Instrumental switch.

- Box `lyr`: `s1`, 1px `s4` border, `radius-field`, 8px 10px padding, parts 6px apart.
- Bar `lyr-bar`: the `heading` "Lyrics", then one tag chip `tg` per section the model knows ([verse], [chorus], [bridge], [intro], [outro]): 26px, 1px dashed `dash`, 7px corners, `mono` 11px `t2`. A click inserts the tag at the caret.
- Instrumental: 13px `t2` and a `sw a2` switch at the end of the bar. On hides the lyrics text and greys the Language chip.
- Text `lyr-text`: 14/21 `t1`; a tag line reads `lyr-tag` (`mono` 12px `a2`).
- The text grows with its words up to 10 lines (210px), then scrolls; the tag colours scroll with it. The Expand button (the prompt's: 28px `s3` square, top right, shown while the words overflow or on focus) opens them in the big editor dialog, titled "Lyrics" (owner, 2026-11-27).

The consumer provides the tags (from the model's `tags`), the text and the Instrumental state. Not every model has tags: no tags, no chips.
