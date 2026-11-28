# PhoneDock

The rail on a phone (shell.tsx below 768px): one sideways bar across the top, no logo.

- Bar `hs-dock`: a `panel` with 30px corners, 14px in from the sides and top, 4/12px padding, items 14px apart, scrolls sideways.
- Items are the Rail's `dock-item`s, 58 × 64 here (56 on the desktop rail), with the rail's 32px tile and 18px glyph, 10/500 label; current `t1` with the white ring, others dimmed: the six tools, a 1px × 32px `s4` rule, the Assistant; they scroll sideways. Pinned at the right end (sticky, on `s1`, after its own rule): the Setup tile with the wrench (admins: Hub, System, Settings in the shared menu, current ticked) or, for a non-admin, Settings (Rail.dc.html frame 5, alpha.6).
- The Assistant item shows a 10px amber `#f2c14e` dot top right (ringed in `s1`) when a card waits for you; open, its tile is lit and ringed like the current page.
- Every tap target is at least 44px; sheets open from the bottom.

The consumer provides the sections the person may see. Icons and tiles match the desktop Rail.
