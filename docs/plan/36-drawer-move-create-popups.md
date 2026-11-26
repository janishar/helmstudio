# helmstudio plan — Move the Assistant drawer; Create settings in popups; right-click menus

[← Plan index](../README.md)

Status: **asked by the owner (2026-11-26)**, release list
[0.1.0-alpha.5](../release/0.1.0-alpha.5.md) items 3, 10 and 17. Boards drawn; no code yet. Web only: no contract,
message or service change.

## 40. Move the Assistant drawer (item 3)

Owner: "ability to move the assitant ui around the screen".

Boards: [Assistant](../mock/Assistant.dc.html), frames 29 (floating over Create) and 30 (the handle, docking,
remembered).

### 40.1 Today

The drawer is `position: fixed`, 340 px wide, docked on the right (top, right and bottom 14 px). On Graph, the
pipeline editor and Studio view the side panel moves left beside it (`[data-drawer] .beside-drawer`). Full screen
and Minimize exist. On a phone it is a bottom sheet whose pill sets its height.

### 40.2 The design

- **Handle.** A pill (the phone sheet's `.grab`, 40 × 5) on top of the drawer, above the header. The pill and the
  empty parts of the header are the handle (`cursor: move`); the header buttons still click. No new header button:
  the header has no room (Memory, Chats, Hub, Full screen, Minimize, Close, and a chip).
- **Floating.** Dragged away from the right edge it floats: 340 × 500 (capped to the window), a deeper shadow and
  a 1 px ring. The page takes its full width (the `beside-drawer` margin goes).
- **Staying on screen.** It is clamped inside the window while dragging and when the window shrinks. Under 1024 px
  wide it docks (and the phone sheet is unchanged below that).
- **Docking.** Brought within 48 px of the right edge, the docked place shows as a dashed outline; let go there and
  it docks. A **Dock** pill button (next to the handle, floating only) or a double-click on the header docks too.
- **Keyboard.** The pill takes focus: arrow keys move it 16 px, Enter docks.
- **Remembered.** Docked or floating, and the position (as fractions of the window, so it survives a resize), are
  kept in `localStorage` under the user's id (like the theme): reopening (⌘J) or a reload brings it back there.
- **Full screen and Minimize** work from either place; leaving them returns to where it was.

### 40.3 Build notes

- `assistant.tsx`: `.ast.drw.float` with `left/top` from state; pointer events on the `.gr` row and `.dh` (ignore
  presses on buttons); `data-drawer` stays only while docked so `beside-drawer` falls back.
- Tests: drag sets floating and moves it; double-click and Dock dock it; the position survives a reload; clamped on
  resize; phone has no move.

### 40.4 Open questions

1. Resize the floating drawer too (a corner grip), or a fixed 340 × 500? Drawn fixed.
2. Remember per browser (drawn) or per user across devices (needs a user setting in the contract)?
3. Dock on the left edge as well, or the right only (drawn)?

## 41. Create settings in popups (item 10)

Owner: "allow the edits in popup for individual item in menu, one menu to open the left drawer for al lthe
setting", then "righ only": the full settings drawer stays on the right.

Boards: [Create · LTX text to video](../mock/TextToVideo.dc.html): the page is the drawer open; frames 1–6 below it.

### 41.1 Today

The chips under the prompt (`form.tsx`): model · precision opens a menu; Run on opens its menu
([Run on](../mock/RunOn.dc.html)); size and length · fps open the Inspector in the aside at that section; Audio is
a one-click switch; seeds have − / +. The Inspector (Preset, Canvas, Duration, Quality, Prompt beats, LoRA) is
always open on the right, or the Elements panel in its place.

### 41.2 The design

- **One button**, a sliders icon (`All settings`), at the end of the chips, after the seeds. It opens and closes
  the settings drawer on the right: today's Inspector, unchanged. Pressed look while open.
- **Open by default** (alpha.5 item 11, owner 2026-11-26: "keep the setting open by default"; it was closed by
  default when first built). Closed, the stage and composer take the page width. Open or closed is remembered per
  user in this browser (`localStorage`, `"0"` for closed).
- **A popup per chip**, anchored under it, editing only that setting. One at a time; Esc or a click outside closes
  it; a change shows on the chip at once. Each popup reuses the drawer's own controls:

| Chip | Popup holds | Reuses |
|---|---|---|
| Model · precision (`Distilled · int8`) | Model variants, Precision options | today's model menu, unchanged |
| Run on (`Auto`) | Auto and each machine, greyed with a reason | today's Run on menu, unchanged |
| Size (`704 × 448`) | Aspect and Size as options, the pixel line | the drawer's Canvas section |
| Length · fps (`5.0 s · 24 fps`) | Length slider, Auto length switch, Frame rate (only when the workflow has an fps setting) | the drawer's Duration section |
| Audio (`Audio on`) | no popup: stays a one-click switch | — |

- **All settings** (an underlined link at the foot of the model, size and length popups) opens the drawer scrolled
  to that section. Run on has none: the drawer has no Run on.
- Settings with no chip (Preset, Quality, Prompt beats, LoRA) stay in the drawer only.

### 41.3 Build notes

- `form.tsx`: the size and length chips open a popover (the model menu's `.menu` shell) rendering the same
  Canvas and Duration field components the Inspector uses, instead of `show(key)`. Image and audio workflows get the
  same: their size chip (`16:9 · 4 MP`) opens the Canvas popup.
- `create.tsx`: an `inspect` open state (remembered) behind the sliders button; the aside shows when it is open or
  the Elements panel is asked for.
- Tests: a chip opens only its popup; a change updates the chip; Esc closes; the button toggles the drawer; open
  on first load (closed until alpha.5 item 11).
- Follow-up (owner, 2026-11-26): the empty screen follows the drawer (the Inspector if it was left open, else no
  aside; the Elements panel left the aside, Elements open from the Elements tile or Insert element, as after a run).
  Size and Length use their popups on narrow windows and phones too (board frame 7b); a popup stays on screen
  (flips, else centred under the chip's row; width and height capped to the window, scrolling inside).

### 41.4 Open questions

1. Audio drawn as a switch, not a popup (it has nothing else to set). A popup anyway?
2. When the drawer is closed, does Insert element (@) still open the Elements panel on the right (as today),
   or only the @ menu in the prompt?
3. Phone and narrow windows: the drawer as a bottom sheet, and popups as small sheets?
4. A popup for Preset as well (a chip `Fast`), or Preset only in the drawer (drawn)?

## 43. Right-click menus (item 17)

Owner, 2026-11-26: "is it possible to have our own double click menu", then "right click in mean", then "App-wide".

Board: [Right-click menus](../mock/ContextMenus.dc.html), frames 0–6. Board drawn; no code yet. Web only: no contract,
message or service change.

- **Where.** Our menu replaces the browser's on media and editing surfaces: the Timeline's lanes, ruler, track headers
  and selections; Gallery take cards and a Select several selection; Create's stage, strip tiles and session menu.
  Text fields, selected text and Shift + right-click keep the browser's menu (copy, paste, spelling, Inspect).
- **One shared component** in the app's menu look (the Create chip popups and Run on): a header naming what it acts
  on; icon, label and shortcut key per item; groups with separators; the danger item last in red; greyed items keep
  their place with the reason on hover; submenus only for lists (Use in ›, Replace with another take ›, Move to
  track ›). Opens at the pointer and stays on screen; Esc or a click outside closes; arrows, Enter and a typed letter
  move and pick; ⇧F10 or the context-menu key opens it on the focused item. The ⋯ buttons stay and open the same menu.
- **Right-click selects first**: on an item outside the selection it selects that item alone; on the selection it
  acts on all of it. Time actions (New shot, Add marker, Mark in / out) act at the pointer's time.
- **Only existing actions**, the same calls the inspector, shortcut sheet, Gallery panel and Create stage make. New
  on the board: **Close gap** on an empty audio-track gap (no op closes a gap today). Magic's menus (frame 6) wait for
  Magic (plan 37) and add Delete block and Add and run….

### 43.1 Open

1. Close gap: worth a new op now, or drop it from the gap menu for alpha.5?
2. Right-click on lanes: keep the playhead where it is (drawn), or move it to the pointer first, as some editors do?
