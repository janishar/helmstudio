# Shot list template

One row per shot, in film order. Show it to the user and wait for a yes before generating.

| # | Scene | Size | Camera | Action (one) | Line | Length | Workflow |
|---|---|---|---|---|---|---|---|
| 1 | 1 | Wide | Static | @june wipes the counter alone in @diner | none | 3 s (73 f) | LTX Image to video |
| 2 | 1 | Medium | Slow dolly in | @stranger puts money and @letter on the counter | @stranger: Keep the change. | 3 s (73 f) | LTX Audio to video |
| 3 | 1 | Close-up | Static | @june looks up from the counter | @june: We're closed, you know. | 2 s (49 f) | LTX Audio to video |
| 4 | 1 | Over the shoulder | Static | @stranger walks out into the rain | none | 3 s (73 f) | MiniMax H3 References |
| 5 | 2 | Insert | Slow tilt down | @letter in @june's hands, handwriting | none | 2 s (49 f) | LTX Image to video |

Columns:
- Size: extreme wide, wide, full, medium, medium close-up, close-up, extreme close-up, insert, over the shoulder.
- Camera: one move or static (see camera-and-lighting.md).
- Action: one thing happens. "and then" means two shots.
- Line: the exact `@name: line` from the script, or none.
- Length in seconds and frames. Pick on the family's grid:
  - LTX-2.5: 8k + 1 at 24 fps. 25 (1 s), 49 (2 s), 73 (3 s), 97 (4 s), 121 (5 s), 145 (6 s), 169 (7 s), 193 (8 s);
    up to 321 (about 13 s).
  - MiniMax H3: 5 + 17k at 24 fps. 56 (about 2.3 s), 73 (3 s), 90 (3.75 s), 107 (4.5 s), 124 (5.2 s), 141 (5.9 s);
    up to 362 (15 s).
  - A dialogue shot through LTX Audio to video takes its length from the line (up to 13.375 s).
- Workflow: LTX-2.5 Image to video for most; LTX-2.5 Audio to video where a face speaks; MiniMax H3 References where a
  character must match closely or two share the shot; MiniMax H3 Image to video to land a move between two frames.

Add the lengths: the total is the film's length before trims (trims usually take 10 to 20 % off).

## The project note (remember, kind project, at most 500 characters)
Keep it short so the next chat can resume. For example:
> Night diner film: 9 shots, 3 scenes. Cast @june, @stranger (voices linked), places @diner, prop @letter, look
> "Night diner", 1344 x 768. Shots 1-4 final, 5 keyframe picked (seed 4812), 6-9 to do. Lines made in ZONOS3
> Dialogue. Sequence "Night diner".
Update it at each checkpoint (a new remember; forget the old one after the user's yes).
