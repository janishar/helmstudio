# ContextMenu

Our own right-click menu (context-menu.tsx, docs/mock/ContextMenus.dc.html): one shared menu in the app's menu look, opened from a thing's `onContextMenu` or its ⋯ button. Every new thing you act on (a card, a row, a block, a clip, a node) ships with its menu, and its board shows it.

- Box `cm`: `size-menu` wide, `space-menu` padding, 1px `s5` border, `s2`, `radius-menu` with a `radius-menu-tail` bottom-left, `shadow-menu`. Opens at the pointer, kept on screen.
- Header (`hd`): the thing's name, 12px/600 `t2`, one line.
- Row (`mi`): `size-seg` tall, `radius-menu-row`, a 16px lucide icon in `t2`, the label (`menu-row`), then a shortcut (`kbd`, 11px mono `t2`), a tick (`a1`) for a toggle, or › for a submenu. Hover, focus and an open submenu's row: `s4`, icon `t1`.
- Separators: 1px `s5`, 4px 6px margin; never first, last or twice.
- Greyed row (`aria-disabled`): 0.55 opacity, the reason in its `title` on hover ("Reading what it fits").
- Danger row (Delete): `bad` text and icon, always last after a separator.
- Section heading inside the menu (`hd`, `heading: true`): names the rows under it, after a separator; not an item, the keys skip it (the model card's "Copies on mac" with Mac `used` and T9 `spare`, ModelFolders 5c).
- Magic menus (built): a dialogue line's row (Move up, Move down, danger Remove line), a section marker (Play from here, Playhead here), a sound block (Loop, a tick row) and a shot (Compare takes, greyed "Only one take yet"). Voice, music and sound blocks (API 1.101) have a level group after their kind's rows: "Louder (+3 dB)" (lucide `Volume2`, the current level on the right when not 0; greyed "At +12 dB"), "Quieter (−3 dB)" (`Volume1`; greyed "At −60 dB") and "Reset level" (`RotateCcw`; greyed "At 0 dB").
- Beside (`beside: true`, alpha.6): a menu opened from a button in a panel opens 8px past that panel's right edge, its bottom level with the button, not under the button over its neighbours (the rail's folded Setup tile). Kept on screen as every menu.
- Submenu (`cm sub`): `size-submenu` wide, beside its row, 4px off; flips left near the edge.
- Keys: ↑ ↓, → ← for submenus, Enter, Esc, a typed letter jumps to the next row starting with it. Shift + right-click is the browser's own menu. Text fields get Cut, Copy, Paste, Select all, Insert @… (and the Mac app's spelling); empty space gets none.

New rows on audio takes: a music take adds Repaint…, Cover…, Extend… after Use in, then Download; a sound variation has Repaint…, Vary…, Extend…; a sound made from a video adds Save with the video; the video made with its sound adds Sound only. Rows a model cannot do are greyed with "Not on <model>".

The consumer provides a title and items `{label, icon, keys, onPick, off, danger, checked, items}` (and the menu's `title`, `beside`); labels are verbs or short noun phrases with "…" when they need more input.

A row can open a **seconds popover** beside the menu instead of a submenu (`seconds: {title, value, picks, min, max, unit, note, onSet}`, API 1.106, AudioFades frame 4): the title, the value typed (`secs-in`, 34px mono on `s3`, its unit after it), the quick picks as chips, a 12px `t2` line ("0 to 10 s. Enter sets it."); Enter or a pick sets it and closes the menu. Used by Fade in… / Fade out… (picks 0.5, 1, 2, 4 s; the row shows the current length on the right), Crossfade 2 s… (1, 2, 4 s) and Duck by Typed… (dB). The fade group follows the level rows: Fade in…, Fade out…, Crossfade into next (music and sound blocks; a Timeline audio clip; greyed "No block after" / "No clip after" with that on the right), Remove fades (greyed "No fades").
