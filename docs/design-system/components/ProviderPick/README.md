# ProviderPick

A provider or model to pick on a settings page (`apage .pks` / `.pk`; assistant/page.tsx), and the note bar `bar2`.

- Grid `pks`: auto-fill columns at least 240px, 8px apart.
- Choice `pk`: a radio, at least 64px, 10/12px padding, 16 / 6 corners, `s3`, 13/17 text; its mark (`lg`, see PeopleRow), the name 13px 600 and a line 11/15 `t2`. Picked: a 16% `a2` tint with a 2px `a2` ring. Unavailable (`aria-disabled`): 0.4.
- State `pst`: 11px 600 in the state's colour with an 8px dot `dt` (ready `a1`, needs something amber `#f2c14e`, failed `bad`).
- Note `bar2`: 16 / 6 corners, `s2`, 10/12px padding, 12/17, an icon then the words.

The consumer provides the choices, which is picked and each one's state.
