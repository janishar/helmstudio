# StatusBanner

One banner for the whole app while the studio isn't answering (components/status.tsx `Banner`; docs/mock/ErrorStates.dc.html a1, a2): `ban`, at the top of the page column, pushing the page down so nothing is hidden. 44px high, 14 / 6 corners, `bad` at 11% on `s2` with a 1px `bad` ring at 55%, 13px, `role="status"`.

- Down: a spinning `loader-circle` in `bad`, **"Studio isn’t answering. Retrying…"**, then in mono `t2` "next try in 8 s" counting down, and a Retry now chip (32px, `s4`, `refresh-cw`).
- Back online (`ban ok`): `a1` instead of `bad`, a `circle-check` and **"Back online"**, fading out over 3 s; no buttons.
- What raises it: a 502, 503 or 504 or a call that reached nothing (lib/api.ts), and one quick health check right after that fails too, so a single blip shows nothing. A 4xx keeps its own detail where it happened. Checks back off 2, 4, 8, 15, 30 s, then every 30 s; paused while the tab is hidden.
- While it shows: an ErrorNote that says the same ("The studio isn’t answering.") is held back (one message, not five), the Generate keys grey with "Studio isn’t answering", and the rail logo shows its red dot, "helmstudio · Studio down" (StatusTile).

The consumer provides nothing: the shell mounts it once; every call reports to it through the shared client.

When the app itself can't load (Kong has no healthy upstream, e.g. web is being rebuilt) the banner can't show; Kong serves its own page to browsers instead (kong/error.html, ErrorStates a4): Forest ink `g` ground, the h mark, **"helmstudio is starting"**, "The app is restarting or not reachable. This page retries by itself." in `t2`, a Retry button in `a1` / `on1` counting down 3 s and reloading by itself, and Kong's message and request id small and muted at the bottom. Self-contained (inline CSS, no fetches); other Kong errors (404, 429) read "Something went wrong" with no auto retry.
