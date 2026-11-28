# Keeping things the same across a board

Only one chain keeps a character, a prop or a place the same from shot to shot:

asset -> views -> frame -> the shot's first frame

1. The asset: a board asset (Character, Prop or Place) with an @handle and a note that holds its look words (shape,
   colours, outfit, age) and the style line. One asset per thing; never two handles for one character.
2. Its views: make_views makes the sheet (Front, Side, Back, an expression). The user picks; redo a view that is off
   before any frame uses it. A view made later does not change frames made before.
3. The frame: the storyboard frame @ picks the asset (refs), so the asset's sheet goes in as the reference. Name the
   asset with its @handle in the frame's words; the same handle, the same way, every time. The frame's words say the
   action, framing and move only, never the asset's look again: the asset carries it.
4. The shot's first frame: a shot made from the frame (from_frame) starts from the frame's picture. A shot that
   continues the one before starts from that shot's last frame instead.

Breaking any link breaks the chain: a shot from text alone, a frame with no @ pick, a shot whose first frame is a
new picture. Then the character drifts.

Also keep the same:
- The style line, word for word, in every frame and every shot.
- The light per place and scene: the time of day and the light words in the place's note.
- The board's size and fps (from its first shot): every shot at the board's target, so nothing is resized on Send.
- The seed of a picked draft for its final.

When something changes upstream (an asset's view, a frame's words or take), the blocks made from it go out of date.
That is the chain telling you what to run again: run those, not the whole board.
