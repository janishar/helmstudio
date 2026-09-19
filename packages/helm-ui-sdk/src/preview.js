// The timeline's preview (04 §5's "gapless preview across cuts"; decided at
// M8b's kickoff, docs/decisions.md).
//
// It plays the sequence from each clip's own bytes, in the browser, against one
// clock. No ffmpeg, no render and no new operation: every clip M8's demos use
// is something a browser plays as it is — h3's and ltx's H.264 in MP4, iris's
// PNG stills, AuK's WAV — and creating and editing a sequence never needs
// ffmpeg (M8 Q5), so a preview that did would leave editing blind without it.
//
// What that costs, said where the code is:
//   - It is a preview, not a render. A cut lands within a frame of where the
//     export puts it, colour is not converted, and the export's letterbox is
//     the component's inset ground rather than black.
//   - A codec this browser cannot decode plays as a gap with a note. The export
//     still includes it.
//   - Gain is an element's volume, which cannot go above the file's own level,
//     so a clip at +6 dB previews at 0 dB and the editor says so. Web Audio
//     could play it louder, but routing a node is `node.connect(…)`, and
//     helm-ui-sdk's rules forbid that word in a component (see M8b's report).
//
// The decisions are all frameAt's (sequence.js). This only makes a handful of
// media elements agree with it: the clips under the playhead, and the next one
// in each track, loaded and parked on its first frame so the cut does not wait
// for a network read.

import { el } from "./base.js";
import { duration, frameAt, kindOf, start } from "./sequence.js";

/** How far ahead a clip is loaded and parked on its first frame. */
const LOOKAHEAD_S = 2;
/** How far a playing element may drift before it is put back. */
const DRIFT_S = 0.12;

export class Preview {
  /**
   * stage is the box pictures are drawn into. resolve(assetId) answers
   * { url } for an asset — the component asks its client, so this never
   * builds an address. onTime(t) and onState({ playing, unplayable }) report.
   */
  constructor({ stage, resolve, onTime, onState }) {
    this.stage = stage;
    this.resolve = resolve;
    this.onTime = onTime || (() => {});
    this.onState = onState || (() => {});
    this.doc = null;
    this.t = 0;
    this.playing = false;
    this.elements = new Map(); // key -> { el, kind, clip }
    this.urls = new Map(); // asset id -> Promise<url>
    this.unplayable = new Set();
    this.loud = false;
    // Without a way to read bytes there is nothing to play. The clock still
    // runs, so the playhead and the timecode work; no media element is made
    // that could never load.
    this.enabled = true;
  }

  /** setEnabled turns the media off or on; the clock is unaffected. */
  setEnabled(on) {
    if (this.enabled === on) return;
    this.enabled = on;
    if (!on) for (const [key, entry] of this.elements) this.drop(key, entry);
    this.apply(!this.playing);
  }

  setDocument(doc) {
    this.doc = doc;
    // Elements belong to clips, and an edit can change which clip a key names;
    // anything the new document does not hold goes.
    const keep = new Set();
    for (const [ti, tr] of (doc.tracks || []).entries()) {
      tr.clips.forEach((c, ci) => keep.add(keyOf(ti, ci, c)));
    }
    for (const [key, entry] of this.elements) {
      if (!keep.has(key)) this.drop(key, entry);
    }
    // Parked unless the preview is already playing. A document arrives when a
    // sequence is opened and again after every edit, and `false` here told the
    // media elements to run: opening a sequence started it playing, and so did
    // dragging a clip.
    this.apply(!this.playing);
  }

  get time() {
    return this.t;
  }

  get length() {
    return this.doc ? duration(this.doc) : 0;
  }

  /** seek puts every element where time t says, paused. Deterministic. */
  seek(t) {
    this.t = Math.max(0, Math.min(t, this.length));
    if (this.playing) this.clock = { base: this.t, at: performance.now() };
    this.apply(!this.playing);
    this.onTime(this.t);
  }

  play() {
    if (!this.doc || this.playing) return;
    if (this.t >= this.length) this.t = 0;
    this.playing = true;
    this.clock = { base: this.t, at: performance.now() };
    const tick = () => {
      if (!this.playing) return;
      this.t = this.clock.base + (performance.now() - this.clock.at) / 1000;
      if (this.t >= this.length) {
        this.t = this.length;
        this.pause();
        this.onTime(this.t);
        return;
      }
      this.apply(false);
      this.onTime(this.t);
      this.raf = requestAnimationFrame(tick);
    };
    this.raf = requestAnimationFrame(tick);
    this.report();
  }

  pause() {
    this.playing = false;
    if (this.raf) cancelAnimationFrame(this.raf);
    this.raf = null;
    for (const { el: media } of this.elements.values()) {
      if (typeof media.pause === "function") media.pause();
    }
    this.report();
  }

  report() {
    this.onState({ playing: this.playing, unplayable: [...this.unplayable], loud: this.loud });
  }

  destroy() {
    this.pause();
    for (const [key, entry] of this.elements) this.drop(key, entry);
  }

  /**
   * apply makes the elements agree with frameAt at the current time. parked
   * means seeking rather than playing: every element is paused on its frame.
   */
  apply(parked) {
    if (!this.doc || !this.enabled) return;
    const f = frameAt(this.doc, this.t);
    const active = new Map();

    for (const p of f.picture) {
      const key = keyOf(p.track, p.index, p.clip);
      active.set(key, { local: p.local, opacity: p.opacity, gain: null });
    }
    for (const s of f.sound) {
      const key = keyOf(s.track, s.index, s.clip);
      const existing = active.get(key);
      if (existing) existing.gain = s.gain;
      else active.set(key, { local: s.local, opacity: 0, gain: s.gain });
    }

    // Park what comes next on each track, so a cut does not wait on a read.
    const upcoming = new Map();
    (this.doc.tracks || []).forEach((tr, ti) => {
      tr.clips.forEach((c, ci) => {
        const s = start(c);
        if (s > this.t && s - this.t <= LOOKAHEAD_S) upcoming.set(keyOf(ti, ci, c), { track: tr, clip: c });
      });
    });

    // Louder than a file's own level is something the preview cannot play.
    const loud = [...active.values()].some((a) => a.gain !== null && a.gain > 1.0001);
    if (loud !== this.loud) {
      this.loud = loud;
      this.report();
    }

    const wanted = new Set([...active.keys(), ...upcoming.keys()]);
    for (const [key, entry] of this.elements) {
      if (!wanted.has(key)) this.drop(key, entry);
    }

    const byKey = new Map();
    (this.doc.tracks || []).forEach((tr, ti) => tr.clips.forEach((c, ci) => byKey.set(keyOf(ti, ci, c), { track: tr, clip: c })));

    for (const [key, state] of active) {
      const { track, clip } = byKey.get(key);
      const entry = this.ensure(key, track, clip);
      if (!entry) continue;
      this.show(entry, state, parked);
    }
    for (const [key, { track, clip }] of upcoming) {
      if (active.has(key)) continue;
      const entry = this.ensure(key, track, clip);
      if (!entry) continue;
      this.park(entry, clip);
    }
  }

  show(entry, state, parked) {
    const media = entry.el;
    if (entry.kind !== "audio") {
      media.style.opacity = String(state.opacity);
      media.hidden = state.opacity <= 0;
    }
    if (entry.kind === "image") return;

    const want = Math.max(0, state.local);
    this.setGain(entry, state.gain === null ? 0 : state.gain);
    if (parked) {
      if (!media.paused) media.pause();
      if (Math.abs((media.currentTime || 0) - want) > 1e-3) media.currentTime = want;
      return;
    }
    if (Math.abs((media.currentTime || 0) - want) > DRIFT_S) media.currentTime = want;
    if (media.paused) {
      const p = media.play();
      if (p && typeof p.catch === "function") p.catch(() => {});
    }
  }

  park(entry, clip) {
    const media = entry.el;
    if (entry.kind !== "audio") media.hidden = true;
    if (entry.kind === "image") return;
    if (!media.paused) media.pause();
    const first = Math.max(0, clip.in ?? 0);
    if (Math.abs((media.currentTime || 0) - first) > 1e-3) media.currentTime = first;
  }

  /** ensure finds or makes the element a clip plays in. */
  ensure(key, track, clip) {
    if (this.elements.has(key)) return this.elements.get(key);
    if (this.unplayable.has(clip.asset_id)) return null;
    const kind = kindOf(track, clip);
    const media = kind === "image"
      ? el("img", { alt: "", part: "preview-image", draggable: "false" })
      : el(kind === "audio" ? "audio" : "video", { preload: "auto", playsinline: true, part: kind === "audio" ? "preview-sound" : "preview-video" });
    media.dataset.clip = key;
    if (kind !== "audio") {
      media.hidden = true;
      this.stage.append(media);
    }
    const entry = { el: media, kind, clip };
    this.elements.set(key, entry);

    media.addEventListener("error", () => {
      // A codec this browser cannot decode. The clip becomes a gap in the
      // preview and a note beside it; the export still includes it.
      this.unplayable.add(clip.asset_id);
      this.drop(key, entry);
      this.report();
    });
    this.urlFor(clip.asset_id).then((url) => {
      if (this.elements.get(key) !== entry || !url) return;
      media.src = url;
    }).catch(() => {
      this.unplayable.add(clip.asset_id);
      this.drop(key, entry);
      this.report();
    });
    return entry;
  }

  urlFor(assetId) {
    if (!this.urls.has(assetId)) this.urls.set(assetId, Promise.resolve(this.resolve(assetId)));
    return this.urls.get(assetId);
  }

  drop(key, entry) {
    const media = entry.el;
    if (typeof media.pause === "function") media.pause();
    media.removeAttribute("src");
    media.remove();
    this.elements.delete(key);
  }

  /**
   * setGain applies a linear gain as the element's volume. A volume cannot go
   * above 1, the file's own level, so anything louder plays at 0 dB and the
   * editor is told (see `loud`).
   */
  setGain(entry, g) {
    const media = entry.el;
    media.muted = g <= 0;
    media.volume = Math.max(0, Math.min(1, g));
  }
}

/**
 * keyOf names the element a clip plays in. It includes the source window, so a
 * trim is a new element parked on the new first frame rather than an old one
 * playing from the wrong place.
 */
export function keyOf(track, index, clip) {
  return `${track}:${index}:${clip.asset_id}:${clip.in ?? ""}:${clip.hold ?? ""}`;
}
