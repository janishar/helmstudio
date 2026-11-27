# RunWaiting

What a run waits on, and why Generate is greyed, said in place (create/create.tsx, create/run-on.tsx; docs/mock/ErrorStates.dc.html b, c1, d, e, f).

- The waiting piece (`step wait`, amber `#f2c14e` at 12% with a 70% ring) leads the progress line: "Runner on mac-studio stopped · trying again (2 of 2)" with a spinning `loader-circle` after its runner was lost (Run.attempt 2); "Waiting · 2 ahead on mac-studio" (Run.position) with `triangle-exclamation-point` while others are ahead; "Waiting for mac-studio" when nothing is ahead but a model unloads first. The stage says "Waiting for a runner…" while it tries again.
- The tile (`rtile wait`, amber): "Trying again" over "2 of 2", "Waiting" over "2 ahead".
- Busy machines: the Run on chip says "Mac · busy" ("Auto · busy" only when every machine that can take it is busy); its rows "busy" or "free"; Generate's estimate "≈ 2:40 · after 2". Generate stays on: queueing is allowed.
- Live updates paused (`live`, a 28px amber pill with `wifi-off`): "Live updates paused · retrying" once the event stream dropped and its polling has failed for 5 s; tooltip "Progress may be behind. It catches up when the connection is back."; a click tries the stream now. Hidden while the banner shows.
- Why Generate is greyed (`why`, 12px `t1b`, a `bad` warning glyph), a visible line under the composer, never hover-only: "No runner for mlx is on. Start helmstudio on your Mac, or pick another model." (the key: "No runner for mlx"; the Run on chip turns `warn`, its rows "no runner for mlx", "mlx runner stopped", "mlx runs on a Mac only"); "On T9 · not connected · Models" for a model whose copies are all on an unplugged drive (the Run on row: "on T9 · not connected"). The same line sits under a Magic block's Generate and the Timeline's Generate shot.

The consumer provides the run, the hosts and the reason; the words come from lib/runs.ts and lib/run-on.ts.
