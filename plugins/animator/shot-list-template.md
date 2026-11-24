# Script and shot list

## Script

Keep it short and plain. Scene heading, action in present tense, dialogue as `@name: text` (the form the Dialogue
workflows read; `@name (style): text` for delivery: whisper, calm, warm, happy, sad, tense, angry, excited, slow,
fast).

```
SCENE 1. ORCHARD, GOLDEN AFTERNOON
@pip sneaks between the trees, eyes on one red apple.
@pip jumps and grabs it.
@pip (happy): Mine!
The apple's branch springs back. A second apple drops on his head.
@pip (sad): Ow.
```

## Shot list

One row per shot. One action per shot. Frames at 24 fps, on the family's grid.

| # | Framing | Camera | Action (start pose -> end) | Line | Length | Frames | Workflow |
|---|---|---|---|---|---|---|---|
| 1 | Wide | Static | @pip tiptoes left to right toward the tree | - | 3 s | 73 | LTX Image to video |
| 2 | Medium | Static | crouches, jumps, grabs the apple, lands | - | 2 s | 49 | LTX Image to video |
| 3 | Close-up | Slow dolly in | grins at the apple | "Mine!" | the line's length | set by the line | LTX Audio to video |
| 4 | Wide | Static | an apple drops on his head; he squashes down | "Ow." | 3 s | 73 | MiniMax H3 Image to video |

Fill in per shot as you go: the keyframe take, the seed, the picked clip.

## Frame grids (24 fps)

LTX-2.5 (8k + 1, 9 to 321): 25 = 1 s, 33 = 1.4 s, 41 = 1.7 s, 49 = 2 s, 57 = 2.4 s, 65 = 2.7 s, 73 = 3 s, 97 = 4 s, 121 = 5 s, 145 = 6 s,
193 = 8 s, 241 = 10 s, 321 = 13.4 s.

MiniMax H3 (5 + 17k, 5 to 362): 39 = 1.6 s, 56 = 2.3 s, 73 = 3 s, 90 = 3.75 s, 107 = 4.5 s, 124 = 5.2 s,
158 = 6.6 s, 243 = 10.1 s, 362 = 15 s.

Pick the grid length just above what the action needs; trim the rest on the Timeline. A few extra frames of hold at
each end make cutting easier.

## Lengths that feel right

- Gag or action beat: 1 to 2 s. Normal shot: 2 to 4 s. Reaction or reveal: 3 to 5 s.
- A line of dialogue: its spoken length plus about half a second each side.
- A talking shot (LTX Audio to video) is as long as its line: the sound sets the length, there is no Duration.
  Voice the line with silence before and after (or as part of a longer line) to reach the planned length, or make
  a short-line shot with Image to video and lay the line on an audio track.
- Establishing wide at a scene's start: 3 to 4 s.
