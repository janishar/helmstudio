# SettingsSection

A section on a settings page: Hub, Assistant, the providers and agents pages (`apage` with `panel sec`; assistant/page.tsx, admin/agents.tsx, hub.tsx).

- Page `apage`: title `pt` (display 22px 700, 0.01em), a two-column grid `pgg` from 1024px wide (one below), columns `pgc` 16px apart.
- Section `sec`: a `panel`, 16/18px padding, parts 12px apart.
- Head `pgh`: the title `h2` (display 16px 700), a link at 12px `t2`, underlined. (`byadm`, an 11px 600 `t2` pill on `s3`, is styled in the code but not used yet.)
- Lines: `line` 12px `t2`; `sub` a 12px 600 0.04em `t2` sub-head; `fine` 11/15 `t2`; `warn` 12/17 amber `#f2c14e` (a literal in the code).
- Fields `fl`: label 12px `t2` over a `.in` field, 6px apart; `g2` puts two side by side 8px apart, `g4` four from 768px.
- Save row `sv`: Cancel (a 34px bevel chip) and Save (the `key` at 34px, 0 18px, 13px), pushed right.
- Switch `sw` (Hub): 44×26, a 20px `t2` knob on `s1` with a 1px `ln` ring; on is `a1` with an `on1` knob; 0.55 disabled.

The consumer provides the fields and the save handler. Words follow the content rules: say what it does, then where it sends.
