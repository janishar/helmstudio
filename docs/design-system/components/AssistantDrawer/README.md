# AssistantDrawer

The Assistant drawer (assistant/assistant.tsx, `ast drw`): a 340px panel on the right, its header, what it sees, the thread and the composer. On a phone it is a bottom sheet (`ast sheet`).

- Box: fixed 14px from the top, right and bottom, 340px, 16px padding, 34 / 14 corners, `s1` with a top sheen and a deep left shadow; parts 10px apart. Full screen (`full`) starts at 104px from the left and centres a 720px column. Its buttons carry the drawer's bevel (light top, dark bottom, small drop) except `x` and `un`.
- Header `dh`: the Assistant mark (26px, 9px corners, the green gradient `#d8f58c` → `#3c9a2e`, white glyph), then 32px icon buttons `ib` (10px corners, `s3`, `t2`; open is `s5` with `t1`): Memory, Chats, Hub, Full screen, Minimize, and the 28px Close `x`. A Working or Waiting chip takes the title's place.
- `where`: "Sees" and pills (`s3`, `t1b`, 11px). `rem`: one 11px line, amber `#f2c14e` for a remote model, `t2` with `local`.
- Thread `thr`: a column 10px apart that fades in over its top 40px and sticks to the end. Its words `msg`: `s2`, 18 / 6 corners, 13/19, up to 94% wide. Yours `msg me`: right, `s4`, 18 18 6 18. A steer waits dimmed (`steer`). A notice `msg note`: amber text and 1px ring. Working `msg think`: `mono` 12px `t2` with a spinning `a2` loader.
- Stop its runs too (alpha.6, owner 2026-11-27: "yes build it"): after Stop, when the Assistant's runs on the screen still go (a Magic board's runs by Assistant), a `msg think` row says "Its N runs are still going." with chips "Stop its runs too" and "Leave them"; a new message takes it away.
- What it did `al`: a list on `s1`, 12px corners, 28px rows in `t1b`; icons `ok` `a1`, `run` `a2` (spins), `bad` `#ff8a80`, `wait` amber, `skip` `t2`.
- Composer `cmp`: `s3`, 18 / 8 corners, 8px padding; a borderless 13/19 textarea; the row `cmpb`: Attach (`att`, 30px), Auto (`aut`, a 26px pill on `s4`; on is a 22% `a1` tint with a 1px `a1` ring), a hint line with `kbd` keys (`mono` 10px on `s4`), Stop (`no`) while it works, and Send `snd` (34px, 12 12 12 5 corners, `a2` with `on2`, 0.55 when disabled).
- The phone sheet: from 25% of the height to the bottom, 28px top corners, a 40×5 grab bar in `ln`, every tap target 44px.

The amber, coral and green values are literals in the code, not tokens. The consumer provides the chat entries, the screen's name and the handlers; Enter sends, Shift+Enter breaks the line.
