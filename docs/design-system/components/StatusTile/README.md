# StatusTile

The studio's and the runners' state under the rail, with the same words as the Mac menu bar (components/status.tsx `StatusTile`; docs/mock/ErrorStates.dc.html g1, g2). It shows only when something is down; when all is well there is nothing to see. md and up only (a phone gets the banner).

- The tile (`rail-health`): its own small panel like the logo's, 76px wide, 24 / 10 corners, `s1`; a 36px coral gradient tile (`#ffb0a8` → `#d6453a`, `server-crash` in white) with a 9px `bad` dot, and the word under it, 10px 600 in `bad`: **Runner down** (a runner that was up in the last hour stopped), **No runner** (none is up), **Studio down** (with the banner). A host off for over an hour no longer raises it; it stays listed as Offline.
- The popover (`hpop`, 400px, 18 / 8 corners, `s2`): the heading "Status" with "checked 2 s ago" in mono, then one row (`hrw`) each: Studio, each runner (torch, mlx, native) per host, each NVIDIA host as one. A row is an 8px dot (`sd ok` `a1` with its glow, `sd bad`, `sd off` `t2`), the name over its host in 11px mono, and the state on the right: "Answering", "Ready", "Running", "Stopped", "Offline since 10:12".
- The footer (`hft`): Restart runners only in the Mac app (its bridge restarts this Mac's runners); in a browser the line "Runners on a Mac restart from helmstudio there." (`hint`) instead. Open Models opens Models.
- A row's right-click menu (ContextMenu): Restart runner and Show log (the Mac app, this Mac's runners), Open in Hosts (admins), else Open Models.

The consumer provides whether the viewer is an admin; the hosts poll and the studio's state come from the app.
