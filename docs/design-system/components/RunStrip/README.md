# RunStrip

The Graph's run strip (graph/graph.tsx): the buttons, the wire legend and the live preview floating over the canvas's bottom, 16px in.

- Buttons: Add node and Tidy layout are 40px bevel buttons, 10px corners, 1px `ln` border on `s2`, 13px. Generate is the `key` at 40px, 0 16px, 13px; it reads "Sending…", "Add to queue" while others run, or "n problems" when the graph cannot run (disabled, with the problems listed in the side panel).
- Legend: 12px `t2`, an 8px dot per wire type in its colour (see GraphNode); "ringed = added by the assistant" in `mono` when it added nodes.
- Preview: 280px, 8px padding, 1px `ln` border, 12px corners, `s2`, a soft drop. The picture is 16:9 contained on `in`; before there is one, a dashed `dash` box with an eye and "Live preview" (or a triangle and "Failed"). Under it a `mono` 11px `t2` line: the node and step while running, else Queued, Starting, Done, Failed or Stopped.

The consumer provides the run's state, the preview and the problems count. ⌘ or Ctrl + Enter generates.
