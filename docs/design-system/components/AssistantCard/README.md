# AssistantCard

A card in the Assistant's thread that waits for your OK (assistant/assistant.tsx `Card`, `cf`), and its Pick one choices (`opts`).

- Card `cf`: stretches the thread's width, `s2`, 18 / 8 corners, 12/14/14px padding, parts 10px apart, a 1px inner ring. `danger` turns the ring and the go key coral `#ff8a80` (text `#1a0d0c`).
- Title `h3`: 14px 600, an optional 16px icon (Cpu for work on the GPU) and tags `gpu` (10px 600 pills, `a2` text and ring).
- Rows `dl`: two columns 12px apart, names 12px `t2`, values `mono` 11/16 `t1`.
- Estimate `est`: `mono` 11px `t1b` on `s3`, 10px corners, a clock. Quote `qt`: 12/17 `t1b` with a 2px `ln` rule on its left. Thumbnails `thumbs`: four 16:10 columns, 7px corners.
- Keys `cfb`: go `go` (40px, 14 14 14 6 corners, `a1` with a gloss, `on1`; 0.45 disabled) and Not now `no` (40px, 12px corners, `s4`). Fine print `fine`: 11/15 `t2`.
- Pick one `opts`: 8px apart; each `opt` at least 44px, 14 / 6 corners, its number `kbd`, a 600 title and an 11px `t2` line; the recommended one has a 1px `a1` ring and an `a1` tag; picked is a 16% `a2` tint with a 2px `a2` ring; the others fade to 0.4 after a pick.

The consumer provides the title, rows, estimate, go label and handlers. Cards only run what they name; anything that uses the GPU always gets one.
