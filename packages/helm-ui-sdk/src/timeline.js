// <helm-timeline timeline="tl_01J…" editable>
//
// The editor for a framework-owned sequence (04 §5, §7; docs/decisions.md M8
// Q7–Q13, Q17–Q20): tracks, clips coloured by the studio that made them, drag
// and trim snapping to frames, the dissolve and gain, undo through revisions,
// the export's plan, and export with progress and cancel.
//
// It owns none of the data. Every edit is a write to the daemon, and what comes
// back — snapped to the target's grid, laid out and checked — replaces what the
// editor drew while it waited. That is what lets the same sequence be opened in
// a studio and later in the launcher and stay one thing.
//
// Structural interface (04 §5, M6 Q12). Required:
//
//   timeline.get(id)                              → Timeline
//   timeline.update(id, { tracks }, { ifMatch })  → Timeline   (editable only)
//
// Optional — a missing one is a smaller editor, never a broken one (04 §9):
//
//   timeline.revert(id, { revision }, { ifMatch }) → Timeline      Undo, Redo
//   timeline.plan(id, { preset })                 → ExportPlan    the chip
//   timeline.export(id, { preset })               → Job           Export
//   timeline.exports(id, { limit })               → { items }     progress
//   timeline.cancelExport(id, job)                               Cancel
//   timeline.append({ asset_id, track, timeline_id }) → Timeline  el.append()
//   assets.read(id, { range })                    → Response      the preview
//   me.get()                                      → { studio_id }  own hue
//
// What is added to the sequence is the studio's to decide (04 §11 rule 5):
// "Add" emits `add-request`, and the page calls `el.append(assetId)` with
// whatever its own picker chose.

import { HelmElement, define, el, kindOf as errorKind, message, timecode } from "./base.js";
import * as seq from "./sequence.js";
import { Preview } from "./preview.js";

const PRESET = "h264";
/** How close, in pixels, a drag has to come to a cut to land on it. */
const MAGNET_PX = 6;
/** How wide a clip's trim handles are. */
const EDGE_PX = 7;
/** How often a running export is asked how far it has got. */
const EXPORT_POLL_MS = 700;
/** Room after the sequence's end, for dropping a clip past it. */
const TAIL_PX = 24;

const styles = `
  /* The lanes are sized from the component's width, so the component's width
     must never come from the lanes: in a grid or flex parent an item is as
     wide as its content, and the two would feed each other until the page is
     tens of thousands of pixels wide. inline-size containment, and a zero
     minimum, keep the width the page's. */
  :host { display: block; min-width: 0; contain: inline-size layout style; }
  .panel { height: 100%; }
  .name { font: var(--helm-type-title); color: var(--helm-text-primary); }
  .facts { font: var(--helm-type-mono); color: var(--helm-text-muted); font-variant-numeric: tabular-nums; }
  .head { flex-wrap: wrap; }
  .chip {
    display: inline-flex; align-items: baseline; gap: var(--helm-space-1);
    font: var(--helm-type-micro);
    padding: 2px var(--helm-space-2);
    border: 1px solid var(--helm-border-hairline);
    border-radius: var(--helm-radius-sm);
    color: var(--helm-text-secondary);
    /* The reason is the point of this chip (03 §11), so it wraps rather than
       ending in an ellipsis: "a copy cuts at packets, not frames, so o…"
       teaches nothing. The head wraps, so it takes a line of its own when the
       row is narrow. */
    max-width: min(100%, 72ch);
    min-width: 0;
  }
  .chip-text { min-width: 0; }
  .chip::before {
    content: ""; width: 7px; height: 7px; border-radius: 50%; align-self: center;
    background: var(--_chip, var(--helm-status-idle)); flex: 0 0 auto;
  }
  .chip[data-mode="copy"] { --_chip: var(--helm-status-running); }
  .chip[data-mode="conform"] { --_chip: var(--helm-status-info); }
  .chip[data-mode="unavailable"] { --_chip: var(--helm-status-warning); }
  button.primary {
    color: var(--helm-on-accent);
    background: var(--helm-accent-base);
    border-color: var(--helm-accent-base);
  }
  button.primary:hover { background: var(--helm-accent-hover); border-color: var(--helm-accent-hover); color: var(--helm-on-accent); }

  /* The picture keeps its room. The panel is a flex column, and a page that
     gives this component a height of its own — a dialog, a pane — would
     otherwise squeeze the stage away, which a portrait sequence suffers first:
     448x768 at 480 wide asks for 823px of height, and what is left after the
     tracks is a few pixels of it. It never shrinks, and it is capped, so the
     tracks below it are always in view. */
  .stage-wrap {
    flex: none;
    display: flex; justify-content: center;
    background: var(--helm-ground-inset);
    border-bottom: 1px solid var(--helm-border-hairline);
    padding: var(--helm-space-2);
  }
  .stage {
    position: relative;
    width: min(100%, 480px);
    max-height: 320px;
    background: var(--helm-ground-inset);
    outline: 1px solid var(--helm-border-hairline);
    overflow: hidden;
  }
  .stage video, .stage img {
    position: absolute; inset: 0; width: 100%; height: 100%;
    object-fit: contain;
  }
  .stage-empty {
    position: absolute; inset: 0;
    display: flex; align-items: center; justify-content: center;
    color: var(--helm-log-text); font: var(--helm-type-micro);
    text-align: center; padding: var(--helm-space-3);
  }

  .transport {
    display: flex; align-items: center; gap: var(--helm-space-2);
    padding: var(--helm-space-2) var(--helm-space-3);
    border-bottom: 1px solid var(--helm-border-hairline);
    background: var(--helm-ground-raised);
    flex-wrap: wrap;
  }
  .tc { font: var(--helm-type-mono); font-variant-numeric: tabular-nums; color: var(--helm-text-primary); }

  /* Two columns: the track heads, which never scroll, and the lanes, which do.
     The heads are a column of their own rather than sticky cells inside the
     scroller — a sticky element is a layer Chrome composites on its own, and
     it made the corners of whatever sat near it rasterise one way or another
     from run to run. Every row therefore has one explicit height, set in both
     columns, so a head always lines up with its lane. */
  .body {
    display: grid; grid-template-columns: var(--_head) minmax(0, 1fr);
    flex: 1 1 auto; min-height: 0;
  }
  .heads {
    background: var(--helm-ground-raised);
    border-right: 1px solid var(--helm-border-hairline);
  }
  .scroller { position: relative; overflow-x: hidden; overflow-y: hidden; min-width: 0; }
  .grid { position: relative; }
  .row { position: relative; box-sizing: content-box; }
  .row + .row { border-top: 1px solid var(--helm-border-hairline); }
  .row.ruler-row { height: 22px; }
  .row.video-row { height: 56px; }
  .row.audio-row { height: 44px; }
  .row.add-row { height: 32px; }
  .track-head {
    display: flex; align-items: center; gap: var(--helm-space-1);
    padding: 0 var(--helm-space-2);
    font: var(--helm-type-mono); color: var(--helm-text-secondary);
  }
  .track-head input { flex: 1 1 auto; width: 0; min-width: 0; }
  .ruler {
    position: relative; height: 100%;
    background: var(--helm-ground-raised);
    cursor: pointer;
  }
  .tick {
    position: absolute; top: 0; bottom: 0;
    border-left: 1px solid var(--helm-border-hairline);
    font: var(--helm-type-mono); color: var(--helm-text-muted);
    padding-left: 3px; line-height: 22px;
    font-variant-numeric: tabular-nums;
    pointer-events: none;
  }
  .lane { position: relative; height: 100%; }
  .clip {
    position: absolute; top: 6px; bottom: 6px;
    display: flex; flex-direction: column; justify-content: center;
    min-width: 2px;
    padding: 0 var(--helm-space-2) 0 calc(var(--helm-space-2) + 3px);
    background: var(--helm-ground-panel);
    border: 1px solid var(--helm-border-strong);
    border-radius: var(--helm-radius-sm);
    color: var(--helm-text-primary);
    overflow: hidden;
    cursor: grab;
    touch-action: none;
    user-select: none;
  }
  .clip::before {
    content: ""; position: absolute; left: 0; top: 0; bottom: 0; width: 3px;
    background: var(--_hue, var(--helm-status-idle));
  }
  .clip[aria-selected="true"] { border-color: var(--helm-focus-ring); background: var(--helm-accent-subtle); }
  .clip[data-dragging="true"] { cursor: grabbing; }
  .clip .who { position: relative; z-index: 1; font: var(--helm-type-micro); color: var(--helm-text-secondary); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .clip .what { position: relative; z-index: 1; font: var(--helm-type-mono); color: var(--helm-text-muted); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; font-variant-numeric: tabular-nums; }
  .clip[aria-selected="true"] .what { color: var(--helm-text-secondary); }
  .edge { position: absolute; top: 0; bottom: 0; width: ${EDGE_PX}px; cursor: ew-resize; }
  .edge.in { left: 0; }
  .edge.out { right: 0; }
  /* The dissolve: the half of it this clip pays for, drawn under the label. */
  .dissolve {
    position: absolute; top: 0; bottom: 0; left: 0; z-index: 0;
    background: linear-gradient(to right, var(--helm-border-strong), transparent);
    border-right: 1px dashed var(--helm-border-strong);
    pointer-events: none;
  }
  .playhead {
    position: absolute; top: 0; bottom: 0; width: 0;
    border-left: 2px solid var(--helm-accent-base);
    pointer-events: none; z-index: 3;
  }
  .add-track { display: flex; align-items: center; height: 100%; padding: 0 var(--helm-space-2); }

  .inspector, .exports, .notice {
    display: flex; align-items: center; gap: var(--helm-space-2); flex-wrap: wrap;
    padding: var(--helm-space-2) var(--helm-space-3);
    border-top: 1px solid var(--helm-border-hairline);
    background: var(--helm-ground-raised);
    font: var(--helm-type-micro); color: var(--helm-text-secondary);
  }
  .inspector label { display: inline-flex; align-items: center; gap: var(--helm-space-1); }
  /* A field sits on the page's ground, as helm-css's .helm-input does. The
     inset ground is near-black in both themes, and dark text on it vanishes
     in light. */
  input[type="number"] {
    width: 9ch;
    font: var(--helm-type-mono);
    height: var(--helm-control-sm);
    padding: 0 var(--helm-space-1);
    color: var(--helm-text-primary);
    background: var(--helm-ground-page);
    border: 1px solid var(--helm-border-strong);
    border-radius: var(--helm-radius-sm);
  }
  input:disabled { opacity: 0.5; }
  .notice[data-tone="error"], .exports[data-tone="error"] { color: var(--helm-status-error); }
  .bar { flex: 1 1 160px; height: 4px; background: var(--helm-border-hairline); border-radius: var(--helm-radius-sm); overflow: hidden; }
  .fill { height: 100%; background: var(--helm-accent-base); width: var(--_fill, 0%); }
  @media (prefers-reduced-motion: reduce) {
    .clip { transition: none; }
  }
`;

export class HelmTimeline extends HelmElement {
  static get observedAttributes() {
    return ["timeline", "editable"];
  }

  constructor() {
    super(styles);
    this.doc = null;          // the daemon's last answer
    this.shown = null;        // what is drawn: the answer, or an edit waiting on one
    this.selected = null;     // { track, index }
    this.zoom = 1;
    this.me = null;
    this.redoStack = [];
    this.base = 0;            // the revision whose content is showing
    this.queue = Promise.resolve();
    this.exportJob = null;
    this.labelFor = null;     // (clip) => string, set by a page that knows its files
    this.build();
  }

  get editable() {
    return this.hasAttribute("editable");
  }

  // ---------------------------------------------------------------- skeleton

  build() {
    this.nameText = el("span", { class: "name", part: "name" });
    this.factsText = el("span", { class: "facts", part: "facts" });
    this.chipText = el("span", { class: "chip-text" });
    this.chip = el("span", { class: "chip", part: "plan", hidden: true, role: "status" }, this.chipText);
    this.undoBtn = el("button", { text: "Undo", onclick: () => this.undo() });
    this.redoBtn = el("button", { text: "Redo", onclick: () => this.redo() });
    this.addBtn = el("button", { text: "Add clip…", onclick: () => this.requestAdd() });
    this.exportBtn = el("button", { class: "primary", part: "export", text: "Export", onclick: () => this.startExport() });

    this.stage = el("div", { class: "stage", part: "stage" });
    this.stageEmpty = el("div", { class: "stage-empty", part: "stage-empty" });
    this.stage.append(this.stageEmpty);

    this.playBtn = el("button", { part: "play", text: "Play", onclick: () => this.togglePlay() });
    this.tc = el("span", { class: "tc", part: "timecode", text: "00:00:00:00" });
    this.frameText = el("span", { class: "tc", part: "frame", text: "f 0" });
    this.zoomOut = el("button", { text: "−", "aria-label": "Zoom out", onclick: () => this.setZoom(this.zoom / 1.5) });
    this.zoomIn = el("button", { text: "+", "aria-label": "Zoom in", onclick: () => this.setZoom(this.zoom * 1.5) });

    this.heads = el("div", { class: "heads", part: "track-heads" });
    this.grid = el("div", { class: "grid", part: "tracks" });
    this.scroller = el("div", { class: "scroller" }, this.grid);
    this.bodyEl = el("div", { class: "body" }, this.heads, this.scroller);
    this.inspector = el("div", { class: "inspector", part: "inspector", hidden: true });
    // The export row says how an export is going and how it ended, so it is the
    // live region for it; the notice below is for edits.
    this.exportsRow = el("div", { class: "exports", part: "exports", hidden: true, role: "status", "aria-live": "polite" });
    this.notice = el("div", { class: "notice", part: "notice", role: "status", "aria-live": "polite", hidden: true });

    this.mountPanel(el("div", { class: "panel" },
      el("div", { class: "head" },
        el("span", { class: "label", text: "Timeline" }), this.nameText, this.factsText,
        el("span", { class: "spacer" }), this.chip, this.undoBtn, this.redoBtn, this.addBtn, this.exportBtn),
      el("div", { class: "stage-wrap" }, this.stage),
      el("div", { class: "transport" },
        this.playBtn,
        el("button", { text: "◀ Frame", "aria-label": "Back one frame", onclick: () => this.step(-1) }),
        el("button", { text: "Frame ▶", "aria-label": "Forward one frame", onclick: () => this.step(1) }),
        this.tc, this.frameText, el("span", { class: "spacer" }), this.zoomOut, this.zoomIn),
      this.bodyEl, this.inspector, this.exportsRow, this.notice));

    this.preview = new Preview({
      stage: this.stage,
      resolve: (id) => this.urlOf(id),
      onTime: (t) => this.drawTime(t),
      onState: (s) => this.drawPlaying(s),
    });
    this.onCleanup(() => this.preview.destroy());

    this.tabIndex = 0;
    this.addEventListener("keydown", (e) => this.onKey(e));
    this.resizer = typeof ResizeObserver === "function" ? new ResizeObserver(() => this.drawTracks()) : null;
  }

  connectedCallback() {
    if (this.resizer) this.resizer.observe(this);
    this.onCleanup(() => this.resizer && this.resizer.disconnect());
    this.reload();
  }

  attributeChangedCallback(name, before, after) {
    if (before !== after && this.isConnected) this.reload();
  }

  disconnectedCallback() {
    this.stopPolling();
    super.disconnectedCallback();
  }

  // -------------------------------------------------------------------- load

  async reload() {
    if (!this.needClient(this.region)) return;
    const id = this.getAttribute("timeline");
    const generation = (this.generation = (this.generation || 0) + 1);
    if (!id) {
      this.region.replaceChildren(el("div", { class: "empty", part: "empty" },
        el("p", { text: "No sequence" }),
        el("p", { class: "why", text: "Set the timeline attribute to the id of a sequence to edit." })));
      return;
    }
    try {
      const [doc, me] = await Promise.all([
        this.client.timeline.get(id),
        this.has("me.get") && !this.me ? this.client.me.get().catch(() => null) : Promise.resolve(this.me),
      ]);
      if (generation !== this.generation || !this.live) return;
      this.me = me;
      this.restore();
      this.accept(doc);
      this.base = doc.revision;
      this.resumeExport();
    } catch (err) {
      if (generation !== this.generation || !this.live) return;
      this.fail(this.region, err, errorKind(err) === "NotFound"
        ? "This sequence is gone, or it is not this studio's to open."
        : "The sequence could not be read.");
    }
  }

  /** has reports whether the client offers a method, by its dotted path. */
  has(path) {
    let cur = this.client;
    for (const part of path.split(".")) {
      if (!cur || !(part in Object(cur))) return false;
      cur = cur[part];
    }
    return typeof cur === "function";
  }

  /** accept takes the daemon's answer as the truth, and draws it. */
  accept(doc) {
    this.doc = doc;
    this.shown = doc;
    this.preview.setEnabled(this.has("assets.read"));
    if (this.selected) {
      const tr = doc.tracks[this.selected.track];
      if (!tr || !tr.clips[this.selected.index]) this.selected = null;
    }
    this.preview.setDocument(doc);
    this.draw();
    this.refreshPlan();
  }

  async urlOf(assetId) {
    if (!this.has("assets.read")) return null;
    const res = await this.client.assets.read(assetId, { range: "bytes=0-0" });
    if (res.body && typeof res.body.cancel === "function") {
      try {
        await res.body.cancel();
      } catch {
        // Already consumed.
      }
    }
    return res.url;
  }

  // ------------------------------------------------------------------- draw

  draw() {
    const doc = this.shown;
    if (!doc) return;
    const t = doc.target;
    this.nameText.textContent = doc.name || "";
    this.factsText.textContent = `${t.width}×${t.height} · ${t.fps} fps · ${t.sample_rate / 1000} kHz · ${clockText(seq.duration(doc))}`;
    this.stage.style.aspectRatio = `${t.width} / ${t.height}`;

    const hasVideo = doc.tracks.some((tr) => tr.kind === "video" && tr.clips.length);
    this.stageEmpty.hidden = hasVideo && this.has("assets.read");
    this.stageEmpty.textContent = !hasVideo
      ? "Nothing on the video track yet."
      : "Preview needs assets.read on this page's client. The sequence still edits and exports.";

    this.undoBtn.hidden = !this.editable || !this.has("timeline.revert");
    this.redoBtn.hidden = this.undoBtn.hidden;
    this.undoBtn.disabled = this.base <= 1;
    this.redoBtn.disabled = this.redoStack.length === 0;
    this.addBtn.hidden = !this.editable || !this.has("timeline.append");
    this.exportBtn.hidden = !this.has("timeline.export");
    this.exportBtn.disabled = !hasVideo || this.chip.dataset.mode === "unavailable" || !!this.exportJob;

    this.drawTracks();
    this.drawInspector();
    this.drawTime(this.preview.time);
  }

  /** laneWidth is what the lanes get at zoom 1: the body, less the head and tail. */
  laneWidth() {
    const available = this.scroller.clientWidth || (this.bodyEl.clientWidth || 640) - this.headWidth();
    return Math.max(160, available - TAIL_PX);
  }

  /** pps is pixels per second: the lane's width fits the sequence, times zoom. */
  pps() {
    const doc = this.shown;
    if (!doc) return 1;
    const d = Math.max(5, seq.duration(doc) || 0);
    return (this.laneWidth() / d) * this.zoom;
  }

  headWidth() {
    return 88;
  }

  setZoom(z) {
    this.zoom = Math.max(0.25, Math.min(40, z));
    this.drawTracks();
  }

  drawTracks() {
    const doc = this.shown;
    if (!doc) return;
    const pps = (this.scale = this.pps());
    const d = Math.max(seq.duration(doc), 1);
    // At zoom 1 the lanes are exactly the scroller's width, so fitting never
    // scrolls; zooming in is what makes them wider than the page.
    const width = Math.max(this.laneWidth(), Math.floor(d * pps)) + TAIL_PX;
    this.bodyEl.style.setProperty("--_head", this.headWidth() + "px");
    this.grid.style.width = `${width}px`;
    // A scroll container only when there is something to scroll: one is a
    // layer of its own under the panel's rounded clip, and at fit zoom it
    // would hold nothing but a chance to rasterise the corners differently.
    this.scroller.style.overflowX = width > this.laneWidth() + TAIL_PX ? "auto" : "hidden";

    const heads = [];
    const rows = [];
    const ruler = el("div", { class: "ruler", part: "ruler", onpointerdown: (e) => this.seekFromPointer(e, ruler) });
    const every = tickEvery(pps);
    for (let s = 0; s <= d + every; s += every) {
      ruler.append(el("span", { class: "tick", style: `left: ${s * pps}px`, text: rulerText(s) }));
    }
    heads.push(el("div", { class: "row ruler-row" }));
    rows.push(el("div", { class: "row ruler-row" }, ruler));

    doc.tracks.forEach((track, ti) => {
      const kind = track.kind === "video" ? "video-row" : "audio-row";
      const lane = el("div", {
        class: "lane", part: "lane", "data-kind": track.kind, role: "list",
        "aria-label": `${track.name || track.kind} — ${track.clips.length} clip${track.clips.length === 1 ? "" : "s"}`,
        onpointerdown: (e) => { if (e.target === lane) this.seekFromPointer(e, lane); },
      });
      track.clips.forEach((clip, ci) => lane.append(this.drawClip(track, ti, clip, ci, pps)));
      const head = el("div", { class: `row ${kind} track-head`, part: "track-head", "data-track": track.name || "" },
        el("span", { text: track.name || "" }));
      if (track.kind === "audio") {
        head.append(el("input", {
          type: "number", step: "0.5", min: "-60", max: "12", value: track.gain_db ?? 0,
          "aria-label": `${track.name} gain in dB`, disabled: !this.editable,
          onchange: (e) => this.edit((tracks) => seq.setTrackGain(tracks, ti, numberOrNull(e.target.value, 0)), `${track.name}'s gain changed.`),
        }));
      }
      heads.push(head);
      rows.push(el("div", { class: `row ${kind}`, "data-track": track.name || "" }, lane));
    });

    if (this.editable && doc.tracks.filter((tr) => tr.kind === "audio").length < 8) {
      heads.push(el("div", { class: "row add-row" }));
      rows.push(el("div", { class: "row add-row" },
        el("div", { class: "add-track" }, el("button", {
          text: "+ Audio track",
          onclick: () => this.edit((tracks) => seq.addAudioTrack(tracks), "Added an audio track."),
        }))));
    }

    this.playhead = el("div", { class: "playhead", part: "playhead" });
    const focusKey = this.focusKey;
    this.heads.replaceChildren(...heads);
    this.grid.replaceChildren(...rows, this.playhead);
    this.drawTime(this.preview.time);
    if (focusKey) {
      const target = this.grid.querySelector(`[data-key="${focusKey}"]`);
      if (target) target.focus();
    }
  }

  /**
   * who is the clip's studio in words, and the hue it wears.
   *
   * The caller's own clips wear its identity hue. Everyone else's are neutral:
   * the studio API serves no other studio's hue, and a guessed one would be
   * someone else's identity colour (03 §11). A clip whose studio the caller may
   * not learn says "another studio" — in words as well as colour (03 §17).
   */
  who(clip) {
    if (clip.studio_id === null || clip.studio_id === undefined) {
      return { name: "another studio", hue: "var(--helm-status-idle)" };
    }
    const own = this.me && this.me.studio_id === clip.studio_id;
    return { name: clip.studio_id, hue: own ? "var(--helm-studio-accent)" : "var(--helm-status-idle)" };
  }

  drawClip(track, ti, clip, ci, pps) {
    const kind = seq.kindOf(track, clip);
    const who = this.who(clip);
    const len = seq.length(clip);
    const key = `${ti}:${ci}`;
    const selected = this.selected && this.selected.track === ti && this.selected.index === ci;
    const label = this.labelFor ? this.labelFor(clip) : "";
    const what = [label, kind === "image" ? `hold ${len.toFixed(2)}s` : `${len.toFixed(2)}s`,
      clip.gain_db ? `${clip.gain_db > 0 ? "+" : ""}${clip.gain_db} dB` : "",
      clip.audio === false ? "muted" : ""].filter(Boolean).join(" · ");

    const node = el("div", {
      class: "clip", part: `clip clip-${kind}`, role: "listitem", tabindex: "0",
      "data-key": key, "data-kind": kind,
      "aria-selected": String(!!selected),
      "aria-label": `${track.name} clip ${ci + 1}: ${kind} from ${who.name}, ${len.toFixed(2)} seconds, at ${seq.start(clip).toFixed(2)} seconds${clip.transition_in ? `, dissolving in over ${clip.transition_in.duration.toFixed(2)} seconds` : ""}`,
      style: `left: ${seq.start(clip) * pps}px; width: ${Math.max(2, len * pps)}px; --_hue: ${who.hue}`,
      onpointerdown: (e) => this.beginDrag(e, node, ti, ci),
      onfocus: () => { this.focusKey = key; },
    },
      el("span", { class: "who", text: who.name }),
      el("span", { class: "what", text: what }));

    if (clip.transition_in && clip.transition_in.duration) {
      node.append(el("span", { class: "dissolve", part: "dissolve", style: `width: ${Math.max(2, (clip.transition_in.duration / 2) * pps)}px` }));
    }
    if (this.editable) {
      node.append(el("span", { class: "edge in", "data-edge": "in" }), el("span", { class: "edge out", "data-edge": "out" }));
    } else {
      node.style.cursor = "default";
    }
    return node;
  }

  drawTime(t) {
    if (!this.shown) return;
    const fps = this.shown.target.fps;
    const f = seq.frames(t, fps);
    this.tc.textContent = timecode(f, fps);
    this.frameText.textContent = "f " + f;
    if (this.playhead) this.playhead.style.left = `${t * (this.scale || this.pps())}px`;
  }

  drawPlaying({ playing, unplayable, loud }) {
    this.playBtn.textContent = playing ? "Pause" : "Play";
    if (unplayable && unplayable.length) {
      this.say(`This browser cannot play ${unplayable.length === 1 ? "one clip" : `${unplayable.length} clips`}, so the preview leaves a gap there. The export still includes it.`);
    } else if (loud) {
      this.say("Gain above 0 dB previews at 0 dB. The export applies it.");
    }
  }

  drawInspector() {
    const sel = this.selected && this.shown.tracks[this.selected.track];
    const clip = sel && sel.clips[this.selected.index];
    if (!clip) {
      this.inspector.hidden = true;
      return;
    }
    const ti = this.selected.track;
    const ci = this.selected.index;
    const track = this.shown.tracks[ti];
    const kind = seq.kindOf(track, clip);
    const ro = !this.editable;
    const fields = [el("span", { class: "label", text: `${track.name} · clip ${ci + 1}` }),
      el("span", { text: `${kind} from ${this.who(clip).name}` })];

    const num = (labelText, value, onChange, attrs) => el("label", {},
      document.createTextNode(labelText),
      el("input", { type: "number", value: value ?? "", disabled: ro, onchange: (e) => onChange(e.target.value), ...(attrs || {}) }));

    if (kind === "image") {
      fields.push(num("Hold (s)", round3(clip.hold), (v) => this.edit((tracks) => seq.setClip(tracks, ti, ci, { hold: Math.max(0.001, Number(v)) }), "Hold changed."), { step: "0.1", min: "0" }));
    } else {
      fields.push(num("In (s)", round3(clip.in), (v) => this.edit((tracks) => seq.setClip(tracks, ti, ci, { in: Math.max(0, Number(v)) }), "In point changed."), { step: "0.04", min: "0" }));
      fields.push(num("Out (s)", round3(clip.out), (v) => this.edit((tracks) => seq.setClip(tracks, ti, ci, { out: Number(v) }), "Out point changed."), { step: "0.04", min: "0" }));
    }
    fields.push(num("Gain (dB)", clip.gain_db ?? 0, (v) => this.edit((tracks) => seq.setClip(tracks, ti, ci, { gain_db: numberOrNull(v, 0) }), "Gain changed."), { step: "0.5", min: "-60", max: "12" }));
    if (kind === "video") {
      fields.push(el("label", {},
        el("input", { type: "checkbox", checked: clip.audio !== false, disabled: ro,
          onchange: (e) => this.edit((tracks) => seq.setClip(tracks, ti, ci, { audio: e.target.checked ? null : false }), e.target.checked ? "Its sound is back on." : "Its sound is off.") }),
        document.createTextNode("Sound")));
    }
    if (track.kind === "video" && ci > 0) {
      fields.push(num("Dissolve (s)", clip.transition_in ? round3(clip.transition_in.duration) : "", (v) => {
        const d = Number(v);
        this.edit((tracks) => seq.setClip(tracks, ti, ci, { transition_in: v === "" || !(d > 0) ? null : { type: "dissolve", duration: d } }),
          v === "" || !(d > 0) ? "The dissolve is gone." : "Dissolve set.");
      }, { step: "0.04", min: "0", placeholder: "none" }));
    }
    if (this.editable) {
      fields.push(el("span", { class: "spacer" }), el("button", { text: "Remove", onclick: () => this.removeSelected() }));
    }
    this.inspector.replaceChildren(...fields);
    this.inspector.hidden = false;
  }

  // ---------------------------------------------------------------- editing

  select(ti, ci) {
    this.selected = { track: ti, index: ci };
    this.focusKey = `${ti}:${ci}`;
    const clip = this.shown.tracks[ti].clips[ci];
    this.drawTracks();
    this.drawInspector();
    this.dispatchEvent(new CustomEvent("select", { detail: { track: this.shown.tracks[ti].name, index: ci, clip }, bubbles: true, composed: true }));
  }

  removeSelected() {
    if (!this.selected) return;
    const { track, index } = this.selected;
    this.selected = null;
    this.edit((tracks) => seq.remove(tracks, this.shown.target.fps, track, index), "Clip removed.");
  }

  /**
   * edit applies a change now and sends it. Edits queue, and each is applied to
   * the latest answer rather than to what was drawn when it was made, so three
   * quick nudges are three frames rather than one.
   */
  edit(change, said) {
    if (!this.editable || !this.doc) return Promise.resolve();
    const before = this.shown;
    const next = change(before.tracks);
    this.shown = { ...before, tracks: next };
    this.draw();

    this.queue = this.queue.then(async () => {
      const doc = this.doc;
      const tracks = change(doc.tracks);
      try {
        const answer = await this.client.timeline.update(doc.id, { tracks: seq.request(tracks) }, { ifMatch: doc.etag });
        if (!this.live) return;
        this.redoStack = [];
        this.base = answer.revision;
        this.accept(answer);
        if (said) this.say(said);
        this.dispatchEvent(new CustomEvent("changed", { detail: { timeline: answer }, bubbles: true, composed: true }));
      } catch (err) {
        if (!this.live) return;
        await this.refused(err, doc);
      }
    });
    return this.queue;
  }

  /**
   * refused puts the daemon's answer back on screen. A conflict means someone
   * else changed the sequence: it is read again and the edit is not replayed,
   * because replaying it onto a document nobody here has seen would be the
   * silent overwrite the ETag exists to stop.
   */
  async refused(err, doc) {
    const kind = errorKind(err);
    if (kind === "Conflict") {
      try {
        const fresh = await this.client.timeline.get(doc.id);
        this.base = fresh.revision;
        this.redoStack = [];
        this.accept(fresh);
      } catch {
        this.accept(doc);
      }
      this.say("This sequence changed somewhere else, so your last change was not applied. It has been read again.", "error");
      return;
    }
    this.accept(doc);
    this.say(message(err, "That change was not made."), "error");
  }

  async undo() {
    if (!this.editable || !this.doc || this.base <= 1 || !this.has("timeline.revert")) return;
    await this.revertTo(this.base - 1, "undo");
  }

  async redo() {
    if (!this.editable || !this.redoStack.length) return;
    await this.revertTo(this.redoStack[this.redoStack.length - 1], "redo");
  }

  /**
   * revertTo writes an earlier revision back as the newest (M8 Q10). Undo walks
   * back from what is showing; Redo walks forward through what this editor
   * undid, and any new edit ends that.
   */
  revertTo(revision, direction) {
    // Undo writes a revision, so a read-only editor does not, however it is
    // asked — the key, the button, or a script calling this.
    if (!this.editable) return Promise.resolve();
    this.queue = this.queue.then(async () => {
      const doc = this.doc;
      try {
        const answer = await this.client.timeline.revert(doc.id, { revision }, { ifMatch: doc.etag });
        if (!this.live) return;
        if (direction === "undo") this.redoStack.push(this.base);
        else this.redoStack.pop();
        this.base = revision;
        this.accept(answer);
        this.say(direction === "undo" ? `Undone: showing revision ${revision}.` : `Redone: showing revision ${revision}.`);
        this.dispatchEvent(new CustomEvent("changed", { detail: { timeline: answer }, bubbles: true, composed: true }));
      } catch (err) {
        if (!this.live) return;
        await this.refused(err, doc);
      }
    });
    return this.queue;
  }

  /** append adds an asset to the end of a track, for the page's own picker. */
  async append(assetId, track) {
    if (!this.editable || !this.doc || !this.has("timeline.append")) return null;
    await this.queue;
    try {
      const answer = await this.client.timeline.append({ asset_id: assetId, track: track || undefined, timeline_id: this.doc.id });
      this.redoStack = [];
      this.base = answer.revision;
      this.accept(answer);
      this.say("Added to the end of the sequence.");
      this.dispatchEvent(new CustomEvent("changed", { detail: { timeline: answer }, bubbles: true, composed: true }));
      return answer;
    } catch (err) {
      this.say(message(err, "That could not be added."), "error");
      return null;
    }
  }

  requestAdd() {
    const track = this.selected ? this.shown.tracks[this.selected.track].name : "V1";
    this.dispatchEvent(new CustomEvent("add-request", { detail: { track }, bubbles: true, composed: true }));
  }

  // ------------------------------------------------------------- drag, trim

  /**
   * beginDrag moves or trims a clip under the pointer. The pointer is captured
   * by the grid, which is never replaced: a drag redraws every lane as it goes,
   * and a clip element holding the capture would be detached by the first
   * redraw, taking the rest of the drag with it.
   */
  beginDrag(e, node, ti, ci) {
    if (e.button !== 0) return;
    if (!this.editable) {
      this.select(ti, ci);
      return;
    }
    e.preventDefault();
    const track = this.shown.tracks[ti];
    const clip = track.clips[ci];
    const edge = e.target.dataset && e.target.dataset.edge;
    const mode = edge ? `trim-${edge}` : "move";
    const pps = this.pps();
    const startX = e.clientX;
    const original = this.shown;
    const fps = original.target.fps;
    const cutList = seq.cuts(original).filter((c) => c !== seq.start(clip) && c !== seq.end(clip));
    let moved = false;
    let result = null;
    const surface = this.grid;
    try {
      surface.setPointerCapture(e.pointerId);
    } catch {
      // A synthetic pointer has nothing to capture; the listeners still work.
    }
    this.selected = { track: ti, index: ci };
    this.focusKey = `${ti}:${ci}`;

    const magnet = (t) => {
      for (const c of cutList) if (Math.abs(c - t) * pps <= MAGNET_PX) return c;
      return seq.snap(t, fps);
    };

    const onMove = (ev) => {
      const dx = ev.clientX - startX;
      if (!moved && Math.abs(dx) < 3) return;
      moved = true;
      const delta = dx / pps;
      if (mode === "move" && track.kind === "video") {
        const middle = seq.start(clip) + seq.length(clip) / 2 + delta;
        const to = track.clips.filter((c, i) => i !== ci && seq.start(c) + seq.length(c) / 2 < middle).length;
        result = { change: (tracks) => seq.move(tracks, fps, ti, ci, to), said: `Moved to position ${to + 1}.`, to };
      } else if (mode === "move") {
        const to = magnet(seq.start(clip) + delta);
        result = { change: (tracks) => seq.move(tracks, fps, ti, ci, to), said: `Moved to ${to.toFixed(2)}s.` };
      } else {
        const which = mode === "trim-in" ? "in" : "out";
        const edgeAt = which === "in" ? seq.start(clip) : seq.end(clip);
        const by = magnet(edgeAt + delta) - edgeAt;
        result = { change: (tracks) => seq.trim(tracks, original.target, ti, ci, which, by), said: which === "in" ? "Trimmed the start." : "Trimmed the end." };
      }
      this.shown = { ...original, tracks: result.change(original.tracks) };
      this.drawTracks();
    };
    const onUp = () => {
      surface.removeEventListener("pointermove", onMove);
      surface.removeEventListener("pointerup", onUp);
      surface.removeEventListener("pointercancel", onUp);
      if (!moved || !result) {
        this.select(ti, ci);
        return;
      }
      this.shown = original;
      if (result.to !== undefined) {
        this.selected = { track: ti, index: result.to };
        this.focusKey = `${ti}:${result.to}`;
      }
      this.edit(result.change, result.said);
    };
    surface.addEventListener("pointermove", onMove);
    surface.addEventListener("pointerup", onUp);
    surface.addEventListener("pointercancel", onUp);
  }

  seekFromPointer(e, surface) {
    const rect = surface.getBoundingClientRect();
    this.seek((e.clientX - rect.left) / this.pps());
  }

  // --------------------------------------------------------------- playback

  /** seek moves the playhead, and parks the preview on that frame. */
  seek(t) {
    if (!this.shown) return;
    const fps = this.shown.target.fps;
    this.preview.seek(seq.snap(Math.max(0, t), fps));
  }

  step(by) {
    if (!this.shown) return;
    if (this.preview.playing) this.preview.pause();
    const fps = this.shown.target.fps;
    this.seek(seq.seconds(seq.frames(this.preview.time, fps) + by, fps));
  }

  togglePlay() {
    if (this.preview.playing) this.preview.pause();
    else this.preview.play();
  }

  onKey(e) {
    const path = e.composedPath();
    const inField = path.some((n) => n && (n.tagName === "INPUT" || n.tagName === "SELECT" || n.tagName === "TEXTAREA"));
    if (inField) return;
    const mod = e.metaKey || e.ctrlKey;
    const clipNode = path.find((n) => n && n.classList && n.classList.contains("clip"));

    if (mod && e.key.toLowerCase() === "z") {
      e.preventDefault();
      if (e.shiftKey) this.redo();
      else this.undo();
      return;
    }
    if (mod && e.key.toLowerCase() === "y") {
      e.preventDefault();
      this.redo();
      return;
    }
    if (clipNode && this.editable) {
      const [ti, ci] = clipNode.dataset.key.split(":").map(Number);
      const track = this.shown.tracks[ti];
      const clip = track.clips[ci];
      const fps = this.shown.target.fps;
      const oneFrame = seq.seconds(1, fps);
      if (e.key === "Delete" || e.key === "Backspace") {
        e.preventDefault();
        this.selected = { track: ti, index: ci };
        this.focusKey = null;
        this.removeSelected();
        return;
      }
      if (e.key === "[" || e.key === "{") {
        e.preventDefault();
        const d = e.shiftKey ? -oneFrame : oneFrame;
        this.edit((tracks) => seq.trim(tracks, this.shown.target, ti, ci, "in", d), "Trimmed the start.");
        return;
      }
      if (e.key === "]" || e.key === "}") {
        e.preventDefault();
        const d = e.shiftKey ? oneFrame : -oneFrame;
        this.edit((tracks) => seq.trim(tracks, this.shown.target, ti, ci, "out", d), "Trimmed the end.");
        return;
      }
      if (e.altKey && (e.key === "ArrowLeft" || e.key === "ArrowRight")) {
        e.preventDefault();
        const dir = e.key === "ArrowLeft" ? -1 : 1;
        if (track.kind === "video") {
          const to = Math.max(0, Math.min(track.clips.length - 1, ci + dir));
          if (to === ci) return;
          this.focusKey = `${ti}:${to}`;
          this.selected = { track: ti, index: to };
          this.edit((tracks) => seq.move(tracks, fps, ti, ci, to), `Moved to position ${to + 1}.`);
        } else {
          const by = (e.shiftKey ? 1 : oneFrame) * dir;
          this.edit((tracks) => seq.move(tracks, fps, ti, ci, seq.start(clip) + by), "Moved.");
        }
        return;
      }
      if (e.key === "Enter") {
        e.preventDefault();
        this.select(ti, ci);
        return;
      }
    }
    if (e.key === " ") {
      e.preventDefault();
      this.togglePlay();
    } else if (e.key === "ArrowLeft" || e.key === "ArrowRight") {
      e.preventDefault();
      const dir = e.key === "ArrowLeft" ? -1 : 1;
      const fps = this.shown ? this.shown.target.fps : 24;
      this.step(dir * (e.shiftKey ? Math.round(fps) : 1));
    } else if (e.key === "Home") {
      e.preventDefault();
      this.seek(0);
    } else if (e.key === "End") {
      e.preventDefault();
      this.seek(this.shown ? seq.duration(this.shown) : 0);
    }
  }

  // ------------------------------------------------------------------ export

  /**
   * refreshPlan asks the daemon which path an export would take (M8 Q13). The
   * chip is the daemon's answer, because only it has probed the files: "video
   * stream copy", or the first reason the copy cannot be taken — which is what
   * teaches which edits are cheap (03 §11).
   */
  async refreshPlan() {
    if (!this.has("timeline.plan") || !this.doc) {
      this.chip.hidden = true;
      return;
    }
    const revision = this.doc.revision;
    try {
      const plan = await this.client.timeline.plan(this.doc.id, { preset: PRESET });
      if (!this.doc || this.doc.revision !== revision) return;
      const reasons = plan.reasons || [];
      if (plan.mode === "copy") {
        this.chip.dataset.mode = "copy";
        this.chipText.textContent = "video stream copy";
        this.chip.title = "The picture is copied, not re-encoded. The sound is always re-encoded.";
      } else {
        this.chip.dataset.mode = "conform";
        this.chipText.textContent = reasons.length ? `conform · ${reasons[0].message}` : "conform";
        this.chip.title = reasons.map((r) => (r.track && r.clip ? `${r.track} clip ${r.clip}: ` : "") + r.message).join("\n");
      }
    } catch (err) {
      if (!this.doc || this.doc.revision !== revision) return;
      if (errorKind(err) === "Unsupported") {
        this.chip.dataset.mode = "unavailable";
        this.chipText.textContent = "export unavailable";
        this.chip.title = message(err, "Export needs ffmpeg on the daemon.");
      } else {
        this.chip.dataset.mode = "";
        this.chipText.textContent = "plan unknown";
        this.chip.title = message(err, "The plan could not be read.");
      }
    }
    this.chip.hidden = false;
    this.exportBtn.disabled = this.chip.dataset.mode === "unavailable" || !!this.exportJob
      || !this.doc.tracks.some((tr) => tr.kind === "video" && tr.clips.length);
  }

  async startExport() {
    if (!this.doc || !this.has("timeline.export")) return;
    this.exportBtn.disabled = true;
    try {
      const job = await this.client.timeline.export(this.doc.id, { preset: PRESET });
      this.exportJob = job;
      this.drawExport(job);
      this.pollExport();
    } catch (err) {
      this.exportBtn.disabled = false;
      this.say(message(err, "The export did not start."), "error");
    }
  }

  /** resumeExport picks up an export still running when the editor opened. */
  async resumeExport() {
    if (!this.has("timeline.exports")) return;
    try {
      const page = await this.client.timeline.exports(this.doc.id, { limit: 1 });
      const job = (page.items || [])[0];
      if (job && (job.state === "queued" || job.state === "running")) {
        this.exportJob = job;
        this.drawExport(job);
        this.pollExport();
      }
    } catch {
      // Progress is a convenience; the export runs whether or not it is watched.
    }
  }

  pollExport() {
    this.stopPolling();
    if (!this.has("timeline.exports")) return;
    const id = this.doc.id;
    this.poll = setInterval(async () => {
      try {
        const page = await this.client.timeline.exports(id, { limit: 1 });
        const job = (page.items || []).find((j) => this.exportJob && j.id === this.exportJob.id) || (page.items || [])[0];
        if (!job || !this.live) return;
        this.exportJob = job;
        this.drawExport(job);
        if (job.state !== "queued" && job.state !== "running") this.finishExport(job);
      } catch {
        // The next poll tries again.
      }
    }, EXPORT_POLL_MS);
  }

  stopPolling() {
    if (this.poll) clearInterval(this.poll);
    this.poll = null;
  }

  drawExport(job) {
    // Only what moved is redrawn. A poll that rebuilt the row every 700 ms
    // would take keyboard focus off Cancel as someone reached for it.
    const drawn = `${job.id}|${job.state}|${job.progress_num}|${job.progress_den}`;
    if (drawn === this.exportDrawn && !this.exportsRow.hidden) return;
    this.exportDrawn = drawn;
    const running = job.state === "queued" || job.state === "running";
    const pct = job.progress_den ? Math.min(100, Math.floor((job.progress_num / job.progress_den) * 100)) : 0;
    const row = [el("span", { class: "label", text: "Export" })];
    if (running) {
      row.push(
        el("span", { text: job.state === "queued" ? "Waiting to start" : `${pct}%` }),
        el("div", { class: "bar", role: "progressbar", "aria-valuemin": "0", "aria-valuemax": String(job.progress_den || 0), "aria-valuenow": String(job.progress_num || 0) },
          el("div", { class: "fill", style: `--_fill: ${pct}%` })),
        el("span", { class: "facts", text: job.progress_den ? `${job.progress_num} of ${job.progress_den} frames` : "" }),
        this.has("timeline.cancelExport") ? el("button", { text: "Cancel", onclick: () => this.cancelExport(job) }) : null);
    } else {
      row.push(el("span", { text: exportResult(job) }));
    }
    this.exportsRow.replaceChildren(...row.filter(Boolean));
    this.exportsRow.hidden = false;
  }

  async cancelExport(job) {
    try {
      await this.client.timeline.cancelExport(this.doc.id, job.id);
    } catch (err) {
      this.say(message(err, "The export could not be cancelled."), "error");
    }
  }

  finishExport(job) {
    this.stopPolling();
    this.exportJob = null;
    this.drawExport(job);
    this.exportBtn.disabled = false;
    const done = job.state === "succeeded";
    this.exportsRow.dataset.tone = done || job.state === "cancelled" ? "" : "error";
    this.dispatchEvent(new CustomEvent(done ? "exported" : "export-stopped", { detail: { job }, bubbles: true, composed: true }));
  }

  say(text, tone) {
    this.notice.textContent = text;
    this.notice.dataset.tone = tone || "";
    this.notice.hidden = !text;
  }
}

function exportResult(job) {
  switch (job.state) {
    case "succeeded":
      return "Exported. The file is in the gallery, labelled timeline.";
    case "cancelled":
      return "Cancelled. Nothing was kept.";
    case "interrupted":
      return "Interrupted when the daemon stopped. Nothing was kept; export again.";
    default:
      return job.last_error && job.last_error.message ? `Failed: ${job.last_error.message}` : "The export failed.";
  }
}

/** clockText is 03 §11's 00:14.16: minutes, seconds and hundredths. */
export function clockText(t) {
  const s = Math.max(0, t || 0);
  const m = Math.floor(s / 60);
  const rest = s - m * 60;
  return `${String(m).padStart(2, "0")}:${rest.toFixed(2).padStart(5, "0")}`;
}

/** rulerText is 03 §11's ruler: whole seconds, then minutes and seconds. */
function rulerText(s) {
  if (s < 60) return `${Number.isInteger(s) ? s : s.toFixed(1)}s`;
  return `${Math.floor(s / 60)}m${String(Math.round(s % 60)).padStart(2, "0")}s`;
}

/** tickEvery chooses a ruler step that leaves room for its labels. */
function tickEvery(pps) {
  for (const step of [0.5, 1, 2, 5, 10, 15, 30, 60, 120, 300]) {
    if (step * pps >= 48) return step;
  }
  return 600;
}

function round3(v) {
  return v === undefined || v === null ? "" : Math.round(v * 1000) / 1000;
}

function numberOrNull(v, zeroMeans) {
  if (v === "" || v === null || v === undefined) return null;
  const n = Number(v);
  if (!Number.isFinite(n)) return null;
  return n === zeroMeans ? null : n;
}

define("helm-timeline", HelmTimeline);
