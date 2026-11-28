# StatusTile (the logo's status)

The studio's and the runners' state, carried by the rail's logo (owner, 2026-11-28: "logo willl reflect this error"; the separate status tile under the rail is gone), with the same words as the Mac menu bar (components/status.tsx `StatusLogo`; docs/mock/ErrorStates.dc.html g1, g2; docs/mock/Rail.dc.html 2e). When all is well there is nothing to see: the logo links to Create. md and up only (a phone gets the banner).

- The dot (`logo-dot`): 11px `bad` with a 2px `s1` ring, on the logo tile's top-right corner (3px out). All three states are red: **Runner down** (a runner that was up in the last hour stopped), **No runner** (none is up), **Studio down** (with the banner). Only a runner seen up since the app opened raises it: a runner already off (a remote machine you keep off, the fake runner), an NVIDIA host, and a host off for over an hour stay grey, listed as Offline (owner, 2026-11-28).
- The logo says it: label and tooltip "helmstudio · Runner down", `aria-haspopup=dialog`. A click or Enter opens the popover beside the logo (anchored right, spanning down); Esc or a click outside closes it.
- The popover (`hpop`, 400px, 18 / 8 corners, `s2`): the heading "Status" with "checked 2 s ago" in mono, then one row (`hrw`) each: Studio, each runner (torch, mlx, native) per host, each NVIDIA host as one (a Mac's runners first, NVIDIA hosts last). A row is an 8px dot (`sd ok` `a1` with its glow, `sd bad`, `sd off` `t2`), the name over its host in 11px mono, and the state on the right: "Answering", "Ready", "Running", "Stopped", "Offline since 10:12".
- The footer (`hft`): Restart runners only in the Mac app (its bridge restarts this Mac's runners); in a browser the line "Runners on a Mac restart from helmstudio there." (`hint`) instead. Open Models opens Models.
- A row's right-click menu (ContextMenu): Restart runner and Show log (the Mac app, this Mac's runners), Open in Hosts (admins), else Open Models.

The consumer draws the logo (it gets the state word, or null when all is well) and provides whether the viewer is an admin; the hosts poll and the studio's state come from the app.
