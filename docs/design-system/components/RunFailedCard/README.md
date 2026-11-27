# RunFailedCard

A failed run on Create's stage, in plain words first (create/create.tsx `FailCard`; docs/mock/ErrorStates.dc.html c2): `fpanel`, 560px, 18 / 8 corners, `s2`, `role="alert"`.

- The title "This run stopped" (display 18 / 700 in `bad`), then one sentence chosen from studio's raw error (lib/runs.ts `failWords`):
  - gave up after 2 attempts: "The runner on mac-studio stopped twice. Retry starts it again."
  - no other live host for env mlx: "The runner on mac-studio stopped and no other runner for mlx is on. Start helmstudio on your Mac, then Retry."
  - a run set to a host that went offline: "mac-studio went offline. A run set to Mac never moves to another machine. Pick Auto, or Retry when it’s back."
  - no live host for env mlx: "No runner for mlx was on when this run started. Start helmstudio on your Mac, then Retry."
  - anything else: "This run stopped with an error."
- Show details (an outline chip, `aria-expanded`) opens `raw`: the raw error in 11px mono `t2` on `in`, selectable ("run 7f3c2a · attempt 2 of 2" over studio's text), with a Copy chip.
- Retry (an `a1` chip with `refresh-cw`) makes a new run with the same request and seed; its tile takes the failed one's place in the strip.
- The strip tile (`rtile failed`): "Failed" over its seed in mono, `bad` on 8% `bad`; its tooltip has the same words. Its right-click menu (ContextMenu, new rows): Retry, Show details, Copy error, then Delete.
- The same words show in the Timeline shot's status (plain words over the raw text, with Try this shot again) and on a Magic block's failed run row.

The consumer provides the run, Retry and how a host is named (Run on's label, "Mac").
