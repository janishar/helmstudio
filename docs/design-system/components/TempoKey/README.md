# TempoKey

The BPM · Key · Time group chip and its popup, and the Auto switch that hands a value to the plan.

- The chip is one composer chip (`chip sq bevel`) for three settings; it shows the values alone: "90 BPM · A minor" (time when not 4/4). On a model without them (MiniMax Music 3, the default) it greys in place as "BPM · key" (Chip), so the preview shows it on ACE-Step 1.5 · 2B, as in 2d.
- Its popup is a `pop` (`radius-pop` with a `radius-pop-tail`), 320px: BPM with its value in `mono` and a slider (`accent-color` `a2`), then Key and Time as choices (`opt a2`; pressed rings `a2`), More… for the rest.
- Auto row (`sw-row`): "Auto" and a 12px `t2` line ("The plan picks all three") beside a `sw a2` switch. Length's popup has the same row ("The plan picks it from the lyrics").
- The last row is the "All settings" link (`hs-link`).

The consumer provides the values, which Auto is on and the handlers.
