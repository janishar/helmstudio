# GraphNode

A node on the Graph canvas (graph/graph.css `gn`; Graph board): a card with ports, wired by type.

- Canvas: a `panel` painted `in`, at least 620px high; double-click or A adds a node, dragging from a port onto nothing opens the search there.
- Card `gn`: 172px, 20 / 8 corners, a 1px 8% white border, an `s3` → `s1` gradient, an inner top light and a deep drop; 12/16 `t1`. Running: an `a1` border. Bypassed: 0.55 and dashed. Broken (`data-bad`): a `bad` border and a `gn-p` line in `bad` 11/14. Selected or focused: a 2px `t2` outline 2px out. Added by the Assistant: a 2px `a2` ring.
- Head `gn-h`: 36px, 13/600, a 1px `s5` rule under it, the state on the right in 11/500 `t2` (an `a1` tick when done, "step / of" while running).
- Rows `gn-r`: 26px, 14px side padding, the port name in `t1b`, a set value `mono` `t2`; `gn-t` a text value on one line in `t2`.
- Ports: 10px dots with a 2px `s2` ring and a glow in their colour, 6px outside the card; dimmed to 0.2 when they cannot take the wire being drawn.
- Wire colours by type (graph.tsx): Text `t1b`, numbers and seeds `#86cdea`, Model `a1`, Conditioning `#c1a8f5`, Latent `#f2c14e`, Image `#f3a0bd`; 2px, dashed 4 4 when bypassed, 3.5px when picked.

The app draws nodes and wires with React Flow; the preview places them by hand. Nodes and wires have right-click menus (ContextMenu).
