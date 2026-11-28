# AssistantMinBar

The minimized Assistant (assistant/assistant.tsx `MinBar`, `ast-min`): a 44px bar bottom right that keeps the chat's state in view.

- Bar: fixed 14px from the right and bottom (above the safe area), 44px high, 22 / 8 corners, `s2`, `t1`, an inner top light and a deep drop, 6px gap.
- Its main button opens the drawer: the mark `lg` (28px, 9px corners, the green gradient) with a 10px dot top right, amber `#f2c14e` (`ask`) when a card waits, `a1` (`new`) for a new answer; then the text `tx` (13/500, at most 170px) and a sub-line 11px `t2`.
- Stop `no`: 32px, 12px corners, `s4`, 12/500, only while it works; "Stopping…" while it stops.

The consumer provides the state line and its sub-line.
