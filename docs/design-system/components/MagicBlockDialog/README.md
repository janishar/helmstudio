# MagicBlockDialog

The Magic board's block dialog and the small parts the gaps work added (built, alpha.6, plan 39; boards `docs/mock/MagicBoard.dc.html`). It is a Modal with a Request form; the parts below are the new ones. All sit in the `.mg` scope of `components/magic/magic.css`.

- **Kind badge** (`kb k-<kind>`): in the dialog's title, beside the name, via the Modal's `heading` option (the dialog's accessible name stays the title). 10px/600, 5px corners, black text on the kind's fill (image `#b8abff`, video `#ff9a6e`, voice, music, sound the `kind-*` tokens). A dialogue block says "Dialogue".
- **Model select**: the Model field is a select (`in`, 32px) with the other models as a hint beside it, 11px `t2` ("or MiniMax H3"). No model on any host: "No model for this kind on any host".
- **Seed**: the field's value reads "42 · 2 seeds" when the block holds more than one run.
- **Length on board**: a read-only mono value. **Generate** carries its estimate as a mono sub-label, "≈ 0:12" (`est`).
- **Loop**: a Segmented On/Off (`seg`) on the Sound form, for a sound made from a prompt. Greyed 0.5 with "Not with a shot's video" under it. The same toggle is in the sound block's menu (a tick row "Loop").
- **Run on**: Auto, This Mac and the other hosts, in the block dialog only; greyed by the model's environment. Two columns with Model when shown.
- **Tied to … · Unlink** (`nr`): a row with the link glyph (`lkm`), "Tied to Shot 2" and a chip "Unlink" (`chip bevel`, `s4`) at the right. A voice or music block made under a shot is tied to it; Unlink drops the tie. The Inspector's "Tied" list shows the same row with "moves with it".
- **Dialogue lines editor**: a Lines · N field, one row per line: a speaker select (110px), the words (`in`, 32px), the turn's status (`mst`: done, running with %, out of date, failed) and a 28px ⋯ icon button. Under it "Give an asset a voice to make it a speaker." and a chip "Add line". The row's right-click menu (ContextMenu): Move up, Move down, then a danger "Remove line". The lane shows "N turns".
- **Compare takes**: a Modal with two takes side by side. Each side has a select of the block's takes, its player and a "Pin" chip. Opened from the block dialog's Compare (two takes or more, not a dialogue) and the shot menu's "Compare takes" (greyed "Only one take yet").
- **Text boxes** (Prompt, Line, Lyrics, the frame's What happens): a fixed 66px (the board's `.pr`), `in`, 13/22, that scrolls inside and cannot be dragged taller; the shared Expand at the top right opens the text big, as Create's prompt (owner, 2026-11-27).
- **Morph prompt**: an optional Prompt textarea (`in`) in the Morph dialog, saved on leaving it.
- **Make views model**: a select beside the Make views key when there is more than one image model; the key's label carries the estimate.
- **Stop** (alpha.6, owner 2026-11-27: "yes build it"): while a block runs, a chip "Stop" (`chip bevel`, `s4`, the lucide `Square` at 13px) right after its running status (`mst run`, "62%") in the block and frame dialogs; the block's right-click menu has "Stop" with its % on the right where Run (a frame's Redraw) was. The board toolbar shows "Stop runs (N)" (`chip bevel`, Square) after Run board's estimate while anything on the board is queued or running; it asks first in a `warn` row: "Stop N runs?", "Queued ones drop at once; running ones stop at their next safe point. Takes already made stay.", chips "Stop runs" and "Not now".
- **Section markers on a music block**: the RangeBar's `cue` look over the block on its lane (verse `a1`, chorus `a2`, intro and outro sound, bridge music; a block's `[tag]` lyrics spread over it are titled "about here"). A click seeks. Their right-click menu: Play from here (Space), Playhead here.

The consumer provides every label, the hints and the greying reasons.
