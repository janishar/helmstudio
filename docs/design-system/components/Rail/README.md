# Rail

The app's left rail (shell.tsx `AppShell`): two stacked floating panels, the logo above the menu, `space-rail` apart and from the page edge.

- Width `size-rail`; each panel is a `panel` with `radius-rail` on all corners and 14px 4px padding; items `space-rail` apart.
- Logo panel: the mark in `on1` at 24px on a `size-tile` tile with the 155deg `a1` to `a2` gradient, `radius-tile` with a `radius-tile-tail` bottom-left. It links to Create.
- Menu: Create, Pipelines, Gallery, Timeline, Magic, Models (as every board shows; Magic is board only, not built: its tile is `tile-magic-from` to `tile-magic-to` with the wand-sparkles glyph); then, for admins only, Hub and System in a group on a 55% `s3` tint; a 1px `s4` rule; Assistant (when ready, with a dot when a card waits); Settings last.
- A dock item: `size-dock-w` by `size-dock-h`, a 36px gradient tile (`tile`, white lucide glyph at 20px, stroke 1.75, `shadow-tile`) over a 10px/500 label. Each section has its own fixed gradient (`tile-*-from` to `tile-*-to`, 155deg). Not current: the tile is desaturated (saturate 0.55, brightness 0.8) and the label is `t2`. Current (`aria-current="page"`): full colour, a 2px white 85% ring 2px out, label `t1`.
- A window shorter than 880px scrolls the whole rail. On a phone the menu is a sideways bar with no logo.

The consumer provides the current path; labels are one word.
