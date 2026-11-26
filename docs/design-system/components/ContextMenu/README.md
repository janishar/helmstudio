# ContextMenu

Our own right-click menu (context-menu.tsx, docs/mock/ContextMenus.dc.html): one shared menu in the app's menu look, opened from a thing's `onContextMenu` or its ⋯ button. Every new thing you act on (a card, a row, a block, a clip, a node) ships with its menu, and its board shows it.

- Box `cm`: `size-menu` wide, `space-menu` padding, 1px `s5` border, `s2`, `radius-menu` with a `radius-menu-tail` bottom-left, `shadow-menu`. Opens at the pointer, kept on screen.
- Header (`hd`): the thing's name, 12px/600 `t2`, one line.
- Row (`mi`): `size-seg` tall, `radius-menu-row`, a 16px lucide icon in `t2`, the label (`menu-row`), then a shortcut (`kbd`, 11px mono `t2`), a tick (`a1`) for a toggle, or › for a submenu. Hover, focus and an open submenu's row: `s4`, icon `t1`.
- Separators: 1px `s5`, 4px 6px margin; never first, last or twice.
- Greyed row (`aria-disabled`): 0.55 opacity, the reason in its `title` on hover ("Reading what it fits").
- Danger row (Delete): `bad` text and icon, always last after a separator.
- Submenu (`cm sub`): `size-submenu` wide, beside its row, 4px off; flips left near the edge.
- Keys: ↑ ↓, → ← for submenus, Enter, Esc, a typed letter jumps to the next row starting with it. Shift + right-click is the browser's own menu. Text fields get Cut, Copy, Paste, Select all, Insert @… (and the Mac app's spelling); empty space gets none.

Board only, not built (CreateMusicSound 3d, 4b, 5c, 5d), new rows on audio takes: a music take adds Repaint…, Cover…, Extend… after Use in, then Download; a sound variation has Repaint…, Vary…, Extend…; a sound made from a video adds Save with the video; the video made with its sound adds Sound only. Rows a model cannot do are greyed with "Not on <model>".

The consumer provides a title and items `{label, icon, keys, onPick, off, danger, checked, items}`; labels are verbs or short noun phrases with "…" when they need more input.
