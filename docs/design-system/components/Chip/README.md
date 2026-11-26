# Chip

A chip is a short setting or action: 36px tall, 13px/500 text, `space-chip-x` side padding and `space-chip-gap` between icon and label.

- Plain (`chip`): pill, `s3`. Used for small actions ("Retry", "Undo", "Done") and as the base of every other chip.
- Composer chip (`chip sq bevel`): `radius-field` corners, `s4`; `s5` while its popup is open (`aria-expanded`). Its text is the value alone ("16:9", "5 s", "Seed 42"), never "Aspect: 16:9".
- Warned (`chip warn`): a choice that cannot run now (Run on a machine that went offline): `bad` text and 1px ring on a 10% `bad` tint over `s4`.
- All settings (`chip sq icon drawer`): opens the settings drawer; while open, `s5` with a 1px `a2` inset ring.
- Disabled: 0.55 opacity.
- Auto (`chip sq bevel` with a 14px cpu glyph): where a run goes; "Run on: Auto picks a free machine" on hover.
- Board only, not built (CreateMusicSound 2b): a chip the picked model cannot use stays in place, greyed (`aria-disabled="true"`, 0.45, not-allowed), with "Not on <model>" in its `title` ("Not on MiniMax Music 3"). It is not removed, so the row never jumps when the model changes. The group chip "90 BPM · A minor" opens TempoKey.

The consumer provides the value text, `aria-expanded` when it opens something, and an `aria-label` when it is icon only.
