# Rail

The app's left rail (shell.tsx `AppShell`): floating panels, the logo above the menu, `space-rail` apart and from the page edge, and the Setup panel pinned to the rail's foot (docs/mock/Rail.dc.html option 4, built alpha.6; owner, 2026-11-28).

- Width `size-rail`; each panel is a `panel` with `radius-rail` on all corners and 14px 4px padding; items `space-rail` apart.
- Logo panel: the mark in `on1` at 24px on a `size-tile` tile with the 155deg `a1` to `a2` gradient, `radius-tile` with a `radius-tile-tail` bottom-left. It links to Create. While something is down it carries the studio's state instead of a status tile (owner, 2026-11-28; StatusTile): a red `logo-dot` on its corner, the label "helmstudio · Runner down", and a click or Enter opens the status popover; Esc closes it. Same tile sizes as before (the compact rail, Rail.dc.html 2b to 2d, not chosen).
- Menu (top panel, `Sections`): Create, Magic, Gallery, Timeline, Pipelines, Models (owner, 2026-11-27; Magic's tile is `tile-magic-from` to `tile-magic-to` with the wand-sparkles glyph); a 1px `s4` rule; Assistant (when ready, with a dot when a card waits). The Assistant stays in the rail for now (owner, 2026-11-28).
- Setup (bottom panel, `Setup`, `mt-auto`): Hub and System (admins only), then Settings; a non-admin's holds Settings only. The gap above it grows with the window.
- Setup gear (`rail-gear`): on a window shorter than 944px an admin's Setup panel folds into one Setup tile (the Settings tile and glyph, label "Setup", `aria-haspopup=menu`, current on a setup page). A click opens the shared ContextMenu titled Setup: Hub, System, Settings with their icons, the current page ticked; arrows, Enter, Esc as every menu. No right-click menu of its own.
- A dock item: `size-dock-w` by `size-dock-h`, a 36px gradient tile (`tile`, white lucide glyph at 20px, stroke 1.75, `shadow-tile`) over a 10px/500 label. Each section has its own fixed gradient (`tile-*-from` to `tile-*-to`, 155deg). Not current: the tile is desaturated (saturate 0.55, brightness 0.8) and the label is `t2`. Current (`aria-current="page"`): full colour, a 2px white 85% ring 2px out, label `t1`.
- A window shorter than 800px (the folded rail's height, 787px) scrolls the whole rail. On a phone the menu is a sideways bar with no logo.

The consumer provides the current path; labels are one word.
- Size (owner, 2026-11-28: "just reduce the size by 10%", "icon"): the rail's tiles are 32px (11px corners), 10% under the shared 36px tile, with 18px glyphs; the items' boxes and gaps are unchanged, so the fold heights stay.
- One panel (owner, 2026-11-28: "rail is broken in two parts", "Yes, one panel"): the logo panel, then ONE menu panel from the tools down to the window's foot: the tools, the rule and the Assistant at its top, a thin rule pushed to its foot, then Hub, System and Settings (or the Setup gear on a window under 930 px).
- Spacing (owner, 2026-11-28: "the menu items have a lot of verrical spacing balance it"): on the desktop rail each item is 56px tall (64 on the phone bar) with 10px between items (14 on the phone); Setup folds to the gear under about 805px.
- The folded Setup tile shows a wrench (lucide Wrench) on the Settings tile's grey, so it never looks like the Settings gear its menu holds (owner, 2026-11-28: "chane icon of setup").
