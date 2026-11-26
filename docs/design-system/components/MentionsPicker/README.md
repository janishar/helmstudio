# MentionsPicker

The @ pickers: the Assistant's (assistant/media.tsx `pop at`) for skills, elements and takes, and Create's (create/prompt.tsx) for elements. Both open above the box, keep the focus in it and pick with ↑↓ and Enter.

Assistant (`ast .pop.at`):
- Opens above the composer at its width, 6px up, at most 340px or half the window, scrolls. The box is the drawer's menu: `s3`, 18 / 8 corners, 6px padding, a 1px `s5` border.
- Query line `atq`: 30px, `s2`, 10px corners, a search glyph, the typed text in `t1` ("Type to search" until then).
- Group heads `h4`: 10px 600 `t2` (Skills, Elements, "Takes · newest first", Uploads).
- Rows `pr`: 36px, 10px corners, 12px text; the picture `i` 40×26 (26 wide for an element), 6px corners, on `ln`; a skill's is a book in `a2` on `s3`; the sub-line `mono` 10px `t2`. The current row (`on`) is `s5`. "Nothing matches" when empty.

Create (`hs-at`): a 300px `pop` with a 1px `s5` border, 8px padding, rows 6/8px, 10px corners, the current on `s4`; a 32px picture (6px corners) or a dashed `dash` box on `s1` with the group's glyph (person, map pin, package); the handle `mono` 13/500, its line 12px `t2`.

A picked element becomes a `mention-chip` in the text (PromptBox). The consumer provides the matches and the active index.
