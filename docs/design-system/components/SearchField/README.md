# SearchField

One search field for every list that can grow (components/search.tsx `SearchField`, `useSearch`; docs/mock/Search.dc.html frame 0, owner 2026-11-27 "approved"). It is the Field look with a search glyph, the match count, a spinner, a clear × and the `/` hint.

- `sf`: 38px (44px on a phone), `s4`, 10px corners, 13px `t1`, 12px left and 8px right padding. The `search` glyph 16px `t2` at the left. Focused: a 1px `a1` ring inside (`:focus-within`).
- The placeholder is its label and says what it searches: "Search sessions", "Search boards", "Search sequences", "Search chats", "Search pipelines", "Search batches", "Search elements", "Search looks", "Search voices", "Search users", "Search keys".
- `sfh`: the matches so far, mono 11px `t2`, only while something is typed. A `loader-circle` spinner (14px `t2`) while the server is asked.
- `sfx`: the clear ×, 22px, `s5`, 6px corners, once something is typed. `k`: the `/` hint (mono 11px, `s5`, 5px corners) only while empty and not focused.
- Matching: every typed word, in any order, case-insensitive; each word found is marked in the row (`mark`: `a1` 28% under `t1`, 3px corners). The ringed row (`sf-on`, a 1px `a1` ring inside) is the one Enter opens.
- No match: "No sessions match." 13px `t2` and Clear search (`sf-none`). Matches the server finds that are not loaded come under "From older sessions" (`sf-older`, its heading `oh` 12px 600 `t2`; the pickers say "From your library"), with "Searching older sessions…" while asked and Load more matches.
- Keys: `/` focuses the open popover's or dialog's field, else opens the screen's closed list (Create's sessions, Magic's boards), else the list in view; never while typing in a field. Esc clears, then closes the popover or dialog. Enter opens or picks the ringed row (the first match). ↑ ↓ move the ring; focus stays in the field. Enter never submits a form the field sits in.
- When it shows: once a list has more than 8 rows, or the server has more pages; it stays while a query is typed.
- Loaded rows filter at once; the server is asked 300 ms after the last key and a late answer for an older query is dropped.

The consumer provides the label, the rows and, for a capped or paged list, the server's search.
