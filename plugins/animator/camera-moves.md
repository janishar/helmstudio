# Camera moves

Name the move first in the prompt, in plain words, one move per shot. Add the speed (slow, steady, fast).

| Move | Prompt words | Use it for |
|---|---|---|
| Static | "Static shot", "locked-off camera" | dialogue, close-ups, moments that must read clearly |
| Dolly in | "Slow dolly in toward ..." | building tension, moving into a feeling |
| Dolly out | "Slow dolly out from ..." | revealing the place, an ending |
| Pan | "Slow pan left / right across ..." | following a look, showing a wide place |
| Tilt | "Tilt up / down from ... to ..." | showing height, a reveal top to bottom |
| Tracking | "Tracking shot following ... from the side / behind" | someone walking or running |
| Crane | "Crane up / down over ..." | opening or closing a scene, scale |
| Handheld | "Handheld camera, slight shake" | urgency, documentary feel |
| Orbit | "Camera slowly orbits around ..." | showing a subject from all sides |
| Zoom | "Slow zoom in on ..." | drawing the eye without moving the camera |
| Whip pan | "Fast whip pan to ..." | a comic surprise, a quick change of place (cut inside the blur) |

## Tips

- Cartoons favour a static camera: it lets poses and timing read. Save moves for reveals, chases and feelings.
- One move per shot: "slow dolly in" or "pan left", not both.
- Say what the camera starts on and ends on when it matters: "Tilt up from her boots to her face."
- The subject's move and the camera's move are separate sentences.
- To land a move exactly, fix the frames:
  - LTX-2.5 Image to video: Frames at 0 s and at a later time (At, in seconds) hold the start and a later point.
  - MiniMax H3 Image to video: First frame and Last frame (not with references).
- LTX-2.5 prompt beats (up to 8 timed lines, played in order) can time a move. Each beat's value is its length, not
  its start time; 0 shares what the others leave: 1 s "static", 0 s "slow dolly in".
- The model card has no camera-specific settings for either family: the move comes from the prompt words and the
  frames you give.
