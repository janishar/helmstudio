# SetupSteps

Setup steps on a settings page (`apage`; assistant/page.tsx, hub.tsx): numbered steps `stp2`, the status pill `stp`, the switch `sw` and the summary `sum2`.

- Step `stp2`: 12px 600 `t2` with its number in a 20px `s4` disc (`t1`, 11px), 8px apart; each step's line under it is `line`.
- Status `stp`: a 2/9px pill, 11px 600, one line, in the state's tone (the consumer sets the colour and a tinted ground: ready, needs setup, off).
- Mark `lgp` (Hub): 32px, 10px corners, the green gradient `#d8f58c` → `#3c9a2e`, white glyph, raised.
- Switch `sw`: see SettingsSection.
- Summary `sum2`: 12/18px padding, 14 / 6 corners, `s1`, display 14px 700 with a 12px `t2` sans line after it.

The consumer provides the steps, the state and its tone. Words are examples.
