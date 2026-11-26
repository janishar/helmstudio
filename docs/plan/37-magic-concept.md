# 37. Magic: concept (parked for version 2)

Owner, 2026-11-26: "plan an switch ui for this timeline which opens a node editor … this will be a real playground for the
assistant", then "first lets have a concept discussion with me". This file keeps what was decided in that discussion. Not
scheduled: not alpha.5; a release is picked when the concept is done. The wireframe board
[Magic](../mock/Magic.dc.html) now shows the lane concept (owner, 2026-11-26: "yes redraw the wireframe"): the Magic page,
Add, a block opened, move and join, the Assistant's proposal, Send to Timeline. The first node-canvas board (44a93115,
e227a7ee) is replaced.

## Decided

1. **A recipe, not a mirror.** Magic is where a sequence is composed and generated; the result is a normal sequence
   (owner: "B, the recipe graph").
2. **One standard generation request per kind**: Image, Video, Voice, Music. Strict: only the shared fields show, a model's
   extras sit under Advanced. Each model declares what it supports; the form greys what the chosen model can't do. Adapters
   turn the request into a local pack's job (LTX, Qwen, Zonos3, H3) and later into an outside service's API. A new addition:
   it removes nothing from Create or Pipelines, and for now only powers Magic (owner: "a, strict standard, but it will not
   remove current features … right now only be powering the magic screen").
3. **The whole main area**, with a floating player, small by default, resizable (owner: "b. The whole main area"; "a,
   floating player resizable but small by default").
4. ~~From n8n~~: dropped (owner, 2026-11-26: "drop n8n image as per our framework"). Magic is designed on its own lane
   framework; nothing is copied from n8n's look or model.
5. **Lanes, not a free canvas** (owner, 2026-11-26: "imagine a lines in parallel, i drop at any point it opens a dialog to add
   elemnt like audio, image, video, dialog etc and i select the duration and options … don't copy timeline its a new screen
   with preview of each node. i will be able to add things in parallel and serial. the charecter etc will be in the @
   selection in nodes. this ui also allows to create a storyboard charecter and alogign them with the shots"):
   - parallel lanes in time; drop at any point opens a dialog: pick the kind, then its duration and options;
   - every block is a node with a live preview and a status (draft, running, done, out of date);
   - serial along a lane, parallel across lanes; free lanes (any kind on any lane), named by the user;
   - characters, places and other elements are @ picks inside a block, not blocks; links like a first frame are @ picks too
     (`@Shot 1 · last frame`);
   - storyboard: a block can start as a board (a quick image, its @characters, a note) lined up with the shots, then
     Generate from board.
   - a storyboard frame, opened (2026-11-26): the picture large in a style (Sketch, Rough, Full) with its takes, Redraw
     or drop an image; who and where as @ picks whose pictures feed it; a note; framing (shot size, angle, move); the
     length of the slot the shot fills. Generate from board makes the shot under it (picture as first frame, the rest as
     its prompt and settings, editable before it runs) and stays tied: a change marks the shot out of date. Frames never
     go to the Timeline; the Storyboard lane can be hidden.
6. **Move and join** (owner: "i should be able to drag and move nodes around, transition etc will be new point in that
   view"): drag blocks in time and across lanes, snapping to edges, the playhead and the ruler; insert or overwrite; move
   several together. Transitions are points where two blocks meet (cut, dissolve, fade, wipe, a generated morph), and at a
   lane's start and end (fade in, fade out). Effects are badges on a block.
7. **Separate from the Timeline** (owner: "b, separate with send to timeline, timeline can strart either with standard or
   magic mode"): a Magic board is its own thing; **Send to Timeline** makes or updates a normal sequence for the final cut
   and export. A new sequence starts in **Standard** (today's Timeline) or **Magic**. Superseded by 9.
8. **The Assistant does it all** (owner: "this entire flow should be extensively supported by assitant"): build a board from a
   brief, storyboard first, edit by words, fill the standard request and run blocks, compare and pick takes, look at frames,
   Send to Timeline. Its changes show as a proposal on the board (Apply or Discard), land in Run history and the undo marked
   "by Assistant"; notes on the board guide it. New tools follow its timeline tools (try, then apply).
9. **Its own feature** (owner: "lets do this make this separate feature than timeline no conflict or movement"; confirmed
   "Yes, exactly"): Magic is its own page in the left rail. The Timeline is unchanged: no mode switch, no link back. The only
   bridge is **Send to Timeline**, which makes a new sequence every time and never touches an existing one, so there is nothing
   to merge.
10. **The Assistant runs small things by itself** (owner: "free for small things, ask above the limit"): storyboard frames
    and short drafts run without asking; anything over a GPU-time limit the user sets asks first, showing the estimate
    ("3 shots, about 6 min").
11. **Voice and music move with their shot** (owner: "handle the voice and music to align with a shot", then "a, move with
    the shot"): a line or music cue dropped under a shot is tied to it (a small link mark); moving the shot moves them,
    shortening it warns when a line no longer fits; unlink to place one freely.
12. **Assets row** (owner, 2026-11-26: "think one more line above storyboard Asset it will have the ability to create characters,
    and props, reference them in story boards"; then "b will be good else it will be a lot. open option to imprt from the
    create gallery"): a row above Storyboard, off the time ruler, of the board's own assets: Character (a sheet of views and
    expressions, a voice), Prop, Place. + Asset makes one (describe and generate, or drop images). They are the board's own,
    not Create's Elements; **Import** brings a take from the Create gallery (or an Element) in as a copy. Blocks pick them with
    @; hovering an asset lights the frames and shots that use it ("used in 3 frames · 2 shots"); changing one marks them out of
    date. The Assistant can make the cast and props from the brief first.

## Open

- What a block can do in the first version (open it, run it, pin a take, @ references, run history, notes), in Magic's own
  terms.
- Where the standard request lives (contract, adapters in the runner) and the first models it covers.

## Parked for version 2

Owner, 2026-11-26: "we will revisit then in version 2". Nothing is built for alpha.5 or the alphas after it. Suggestions to
weigh then:

1. Play the storyboard as a rough cut (frames in order with their lines and music) before making any video.
2. Say plainly which models keep a character the same (today: an asset feeds the storyboard frame, which becomes the shot's
   first frame).
3. Build the standard generation layer first, on its own: Qwen Image, LTX, Qwen3-TTS or Zonos3; Music needs a model or a
   service first.
4. Show the GPU time up front: the whole board, each lane, the run order.
5. Board snapshots to compare or go back.
6. List a board's sends to the Timeline and name them clearly.
7. Reuse the Timeline's player.
8. The Assistant's tools in the first version.
9. A small first version: Assets, Storyboard with the rough cut, Shots, Voice tied to shots, dissolve and cut, Send to
   Timeline, the Assistant; music, effects, morphs, sound effects and outside services later.
10. Start a board from a brief.
