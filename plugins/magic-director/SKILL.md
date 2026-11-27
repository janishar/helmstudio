# Magic director

How to plan and run a whole Magic board, from a brief to a sequence on the Timeline. This guide is the craft and the
order of work; your Magic tools and the board are the truth for this install (show_board, magic_kinds and plan_board
say what is there, what can run and what it costs).

Who does what:
- Magic director (this guide): planning and running a board.
- Animator: cartoon craft (style lines, model sheets, timing, squash and stretch). Use it when the film is a cartoon.
- Cinematic filmmaker: camera and framing (shot sizes, moves, lens, light, the 180-degree rule).
Read those for their craft; this guide does not repeat it. On a board, things live where this guide says, whatever
those guides say about Create, Elements or save_shots.

Extra files you can read with read_skill_file:
- `sample-plans.md`: a 30 s ad, a 1 min story and a music video: lanes, block counts, lengths and a GPU budget.
- `consistency.md`: how a character stays the same: asset, then views, then frame, then the shot's first frame.

## 0. The order (always this order)

1. Idea and style line
2. Characters
3. Props
4. Settings (places)
5. Beats with lengths
6. Storyboard frames
7. Shots from the liked frames
8. Voice, music and sound tied to the shots

Never skip ahead. A frame with no character asset makes a stranger; a shot with no liked frame wastes GPU time; a
style change after frames exist redoes every frame (the Animator says the same about keyframes).

On a board these are board things, not Create things:
- Characters, props and places are the board's assets (Character, Prop, Place) with an @handle. An Element comes in
  with Import, as a copy. Don't make Elements or save_shots for a board.
- The board is the shot list: the Storyboard lane holds the beats as frames, the Shots lane the shots in time.
- Send to Timeline assembles it (always a new sequence). Don't build the cut by hand on the Timeline.

## 1. The plan first (steps 1-5), then the user's OK

Before any run, write the plan in chat and wait for the user's OK. No GPU time before that, not even a frame.

The plan, short:
- Idea: one or two sentences (who wants what, what stands in the way, how it ends) and the target length.
- Format: the board's size and fps (16:9 1280 × 720 at 24 fps; 9:16 720 × 1280 for vertical). create_board passes
  it as target; it can't change later.
- Style line: one line every frame and shot reuses, word for word. "2D cartoon, bold clean outlines, flat colours"
  or "Live action, 35 mm, soft window light, muted teal and amber". For a cartoon, take it from the Animator.
- Characters: per character the look, outfit, age and voice, and its @handle (`@mara`). Only those who appear.
- Props: only the ones that recur or matter to the story (`@red-kite`). Skip set dressing.
- Settings: each place, empty, with its time of day and light; note the wide and the reverse angle you will need.
- Beats: one moment each, with a length; the lengths add up to the target. Most beats are 2 to 5 s.

Ask with a short options block: Go ahead, Change something. Change it until the user says yes. A change here costs
nothing; a change after ten shots costs ten runs.

## 2. Build the board from the plan

Once the user says yes:
- No board yet: create_board, then navigate /magic with b (its board_id). One fits already: show_board first.
- First edit, assets and lanes only (try_board_edit, fix what it refuses, then edit_board with the same ops): the
  assets (asset_add, each with its note holding the look words and the style line) and the lanes (Storyboard,
  Shots, Voice, Music, Sound as the plan needs). No frames yet.
- The user applies the change on the board. Never say it is there before edit_board's result.

## 3. Characters, props and settings first (views)

- Characters, props and places never get a lane or blocks of their own: their pictures are always make_views onto
  the asset's sheet.
- Make the views before any frame: make_views for each character (Front, Side, Back; an expression or two if the
  story needs them), each recurring prop (Front), each place (a Wide, and the Reverse if a scene cuts both ways).
- Show the sheets; let the user pick or redo a view. Only then a second edit adds the Storyboard frames (§4).
- A voice for each speaking character goes on the asset (voice_id) before any dialogue. No voice yet: say so and
  point at Create's voice design or clone; then set it on the asset.

## 4. Storyboard frames

- One frame per beat, at its time and length. Each frame @ picks its characters, props and place (their sheets
  are its reference); its words say only the action, never a look again (the asset carries it); its framing (size and angle: Wide shot, from a low angle) and its move for the shot to come (The camera
  slowly pushes in). Framing and moves: the Cinematic filmmaker's words.
- Start wide to show the place, then go closer for feelings; one action per frame.
- Frames are cheap and never ask: run them as sketches first (the frame's style Sketch), 2 seeds where the beat
  matters. Then compare, pin the one you like, say why, and let the user pick.
- Run a frame as rough or full only once the board reads well as a whole (play the rough cut).

## 5. Shots from the liked frames

- Make a shot only from a frame the user liked: Generate from board (from_frame) adds it under the frame and starts
  from its picture. Its prompt is the frame's framing, words and move unless you give it one.
- One subject move and at most one camera move per shot. Hold a beat at the start and the end so it cuts well.
- A shot that continues the one before (same place, same action): its first frame is that shot's last frame
  (first {block, part: last}), not a new picture.
- Model fit (magic_kinds says what this install holds and what each greys):
  - LTX: fast, sound in the same pass, keyframes at set times. Lengths are 8k + 1 frames at 24 fps (49 = 2 s,
    97 = 4 s, 121 = 5 s). Good for drafts and for most shots.
  - MiniMax H3: higher quality, a first and a last frame to land a move exactly; slower. Lengths are 5 + 17k
    (73 = 3 s, 124 = 5 s). References keep a look but take no first or last frame.
  - A last frame is for a shot that must end somewhere exact (a jump that lands, a door that closes).
- Joins between shots: a cut by default; a dissolve for time passing; a fade at the start or the end; a morph when
  one picture should turn into the next (a morph is a block with its own prompt and runs with run_blocks). Fade and
  wipe are baked on Send; keep them short.

## 6. Sketches first, finals only for picks

- Every stage: the cheap way first, the final only for what the user picked.
  - Frames: Sketch, then rough or full for the picked ones.
  - Images (views, frames): a fast variant first (set the request's Advanced `model.variant`, for example
    turbo), then the picked one again on the workflow's own variant with the same seed.
  - Shots: a short or fast draft (LTX, or H3's draft steps) to check the action and the camera, then the final with
    the same seed and length so the timing carries over.
- Keep the seed of a picked draft when you run its final.

## 7. The GPU limit

- run_blocks and make_views plan first. Storyboard frames, and other runs within the user's limit (Magic settings,
  5 GPU minutes by default), start at once; above it, or with no estimate yet, the user says yes on a card.
- Run in small groups the user can review (a scene's frames, then its shots), not the whole board at once.
- Before a big step, plan_board and say the cost in plain words: "7 shots, about 12 min".
- A variant with no runs of the user's yet has no estimate, so it asks; say why.
- With Auto on, the hour's caps still hold; past them a run asks once.
- Stopping runs: when the user says stop, or a run is plainly wrong (the wrong frame, the wrong model), stop it at
  once with cancel_runs (those blocks, a lane, the runs run_blocks started, or all), then say what stopped. The user
  has the block's Stop and Stop runs in the board's toolbar too. Takes made before the stop stay.

## 8. Sound with the picture

- Lines: a voice block under its shot, tied to it (link) so it moves with the shot. A conversation is one dialogue
  block with turns (each speaker an asset with a voice), not an "@name:" script.
- Music: one cue per scene or one for the film, tied to its first shot. Put its sections ([verse], [chorus]) on the
  beats: the chorus lands on the turn of the story.
- Sound: ambience per place (a sound that loops to fill its block), effects on the shots that need them. A sound
  made from a shot's video (MMAudio) follows that shot; make it once the shot's final is pinned.
- LTX shots come with sound; keep it for effects, or make the block's own sound when you want to replace it.

## 9. Review like a director

- After runs finish (await), compare the takes (show_board, look_at, board_frame at a time) and pin the best one
  with a block op. Say in a few words why: "Take 2: her face reads, the light matches Shot 3".
- Watch for: the character drifting (face, outfit, colours), the light changing within a scene, a move that
  fights the cut, a beat too long or too short.
- Fix the part that is wrong, not the board: one frame's words, one shot's seed, one view of an asset.
- Before Send: read the board's statuses. List any block out of date (its frame, asset or request changed after its
  take) or with no take (it becomes a gap) and ask: run them, or send as is. Send only when the user asks.

## 10. A worked plan

Brief: "a 30 s ad for a lemonade stand run by @mara, a girl of ten".
1. Plan: idea in one line; style line "Live action, 35 mm, warm summer afternoon light, soft pastel colours";
   @mara (ten, freckles, yellow dress, bright voice), @stand (a wooden lemonade stand, hand-painted sign), @street
   (a quiet suburban street, wide and reverse); 7 beats adding up to 30 s. The user says yes.
2. First edit: 3 assets, lanes Storyboard, Shots, Voice, Music, Sound.
3. make_views: @mara Front, Side; @stand Front; @street Wide, Reverse. The user picks.
4. Second edit: 7 frames at their times, each @ picking its assets ("@mara hands a cup over @stand, medium shot").
   Run them as sketches, 2 seeds on the opening and the end card; pin, say why; the user picks; rough cut.
5. 7 shots from the frames (LTX drafts), compare, then finals with the same seeds (about 10 to 15 GPU minutes:
   a card).
6. A voice-over line tied to Shot 1 and Shot 7, one music cue from Shot 1, street ambience under it all.
7. Check statuses, the user watches the board, then Send to Timeline.
