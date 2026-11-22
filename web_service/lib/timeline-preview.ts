// The sequence preview (plan 15.2: plays across cuts in the browser without rendering): each clip's own file in a
// <video>, <img> or <audio>, against one clock. What shows and plays at a frame is frameAt's (lib/timeline.ts); this
// only makes a handful of media elements agree with it: the clips under the playhead and those starting within
// LOOKAHEAD_S, parked on their first frame so a cut does not wait for a read. Ported from the old helmstudio's
// preview.js (MIT, same owner); gain goes through Web Audio (a GainNode) so +dB plays louder, as the export does.
import { Grid, frameAt, kindOf, type Norm, type Takes } from "@/lib/timeline";

/** How far ahead (seconds) a clip is loaded and parked. */
const LOOKAHEAD_S = 2;
/** How far a playing element may drift (seconds) before it is put back. */
const DRIFT_S = 0.12;

type Media = HTMLVideoElement | HTMLImageElement | HTMLAudioElement;
type Entry = { el: Media; kind: "video" | "image" | "audio"; gain?: GainNode; take: string };

export type PreviewState = { playing: boolean; unplayable: string[] };

export class Preview {
  private doc: Norm | null = null;
  private takes: Takes = {};
  private urls: Record<string, string | undefined> = {};
  private g = new Grid({ fps: 24, sample_rate: 48000 });
  private els = new Map<string, Entry>();
  private unplayable = new Set<string>();
  private raf = 0;
  private clock = { base: 0, at: 0 };
  private ctx: AudioContext | null = null;
  frame = 0;
  playing = false;
  /** the shuttle's speed (J / K / L, plan 23 §27.3): 1, 2, 4, 8 forward, negative backward */
  rate = 1;

  constructor(
    private stage: HTMLElement,
    private onFrame: (f: number) => void,
    private onState: (s: PreviewState) => void,
  ) {}

  /** A new document (every edit): elements of clips it no longer has go. `urls`: take id → its signed link. */
  setDoc(doc: Norm, takes: Takes, urls: Record<string, string | undefined>) {
    this.doc = doc;
    this.takes = takes;
    this.urls = urls;
    this.g = new Grid(doc.target);
    const keep = new Set(doc.tracks.flatMap((t) => t.clips.map((c) => keyOf(c))));
    for (const [k, e] of this.els) if (!keep.has(k)) this.drop(k, e);
    if (this.frame > doc.frames) this.frame = doc.frames;
    this.apply(!this.playing);
  }

  seek(f: number) {
    this.frame = Math.max(0, Math.min(Math.round(f), this.doc?.frames ?? 0));
    if (this.playing) this.clock = { base: this.g.s(this.frame), at: performance.now() };
    this.apply(!this.playing);
    this.onFrame(this.frame);
  }

  /** Hover scrub (plan 23 §27.3): shows frame `f` without moving the playhead (null: back to it); silent, not while
   * playing. */
  peek(f: number | null) {
    if (this.playing || !this.doc) return;
    const keep = this.frame;
    this.frame = f === null ? keep : Math.max(0, Math.min(Math.round(f), this.doc.frames));
    this.apply(true);
    this.frame = keep;
  }

  /** Plays at `rate` (default 1×); a playing preview changes speed in place. Forward sets every element's
   * playbackRate; backward is a timed seek each frame, parked (paused, so silent): browsers do not play backward. */
  play(rate = 1) {
    if (!this.doc || !this.doc.frames || !rate) return;
    if (this.playing) {
      this.clock = { base: this.g.s(this.frame), at: performance.now() };
      this.rate = rate;
      if (rate < 0) for (const e of this.els.values()) if ("pause" in e.el) e.el.pause();
      return;
    }
    this.rate = rate;
    if (rate > 0 && this.frame >= this.doc.frames) this.frame = 0;
    if (rate < 0 && this.frame <= 0) return;
    // Web Audio needs a user gesture: play() is one
    if (!this.ctx && typeof AudioContext !== "undefined") this.ctx = new AudioContext();
    void this.ctx?.resume();
    this.playing = true;
    this.clock = { base: this.g.s(this.frame), at: performance.now() };
    const tick = () => {
      if (!this.playing || !this.doc) return;
      const t = this.clock.base + (this.rate * (performance.now() - this.clock.at)) / 1000;
      const f = Math.floor((t * this.g.num) / this.g.den + 1e-9);
      if (f >= this.doc.frames || f < 0) {
        this.frame = f < 0 ? 0 : this.doc.frames;
        this.pause();
        this.onFrame(this.frame);
        return;
      }
      if (f !== this.frame) {
        this.frame = f;
        this.onFrame(f);
      }
      this.apply(this.rate < 0);
      this.raf = requestAnimationFrame(tick);
    };
    this.raf = requestAnimationFrame(tick);
    this.report();
  }

  pause() {
    this.playing = false;
    cancelAnimationFrame(this.raf);
    for (const e of this.els.values()) if ("pause" in e.el) e.el.pause();
    this.report();
  }

  destroy() {
    this.pause();
    for (const [k, e] of this.els) this.drop(k, e);
    void this.ctx?.close();
  }

  private report() {
    this.onState({ playing: this.playing, unplayable: [...this.unplayable] });
  }

  /** Makes the elements agree with frameAt at the current frame; parked = paused on their frame (seeking). */
  private apply(parked: boolean) {
    const doc = this.doc;
    if (!doc) return;
    const { pictures, sounds } = frameAt(doc, this.frame, this.takes);
    const byId = new Map(doc.tracks.flatMap((t, ti) => t.clips.map((c) => [c.id, { c, ti }])));
    const want = new Map<string, { t: number | null; opacity: number; z: number; gain: number }>();
    pictures.forEach((p, z) => {
      if (!p.offline) want.set(p.clip, { t: p.t, opacity: p.opacity, z, gain: 0 });
    });
    for (const s of sounds) {
      const gain = 10 ** (s.gain_db / 20) * s.fade;
      const w = want.get(s.clip);
      if (w) w.gain = gain;
      else want.set(s.clip, { t: s.t, opacity: 0, z: 0, gain });
    }
    const keys = new Set<string>();
    for (const [id, w] of want) {
      const { c, ti } = byId.get(id)!;
      const e = this.ensure(c, ti);
      if (!e) continue;
      keys.add(keyOf(c));
      this.show(e, w, parked);
    }
    // park what starts within the lookahead on each track
    const ahead = this.g.f(LOOKAHEAD_S);
    for (const [, { c, ti }] of byId) {
      const k = keyOf(c);
      const a = this.g.f(c.at!);
      if (keys.has(k) || a <= this.frame || a - this.frame > ahead || c.offline || !c.take_id)
        continue;
      const e = this.ensure(c, ti);
      if (!e) continue;
      keys.add(k);
      this.park(e, c.in ?? 0);
    }
    for (const [k, e] of this.els) if (!keys.has(k)) this.drop(k, e);
  }

  private show(
    e: Entry,
    w: { t: number | null; opacity: number; z: number; gain: number },
    parked: boolean,
  ) {
    if (e.kind !== "audio") {
      e.el.style.opacity = String(w.opacity);
      e.el.style.zIndex = String(w.z);
      e.el.hidden = w.opacity <= 0;
    }
    if (e.kind === "image" || w.t === null) return;
    const m = e.el as HTMLMediaElement;
    this.setGain(e, w.gain);
    const at = w.t + this.g.s(0.5); // the middle of the frame, never the edge before it
    if (parked) {
      if (!m.paused) m.pause();
      if (Math.abs(m.currentTime - at) > 1e-3) m.currentTime = at;
      return;
    }
    if (m.playbackRate !== this.rate) m.playbackRate = this.rate;
    if (Math.abs(m.currentTime - at) > DRIFT_S * this.rate) m.currentTime = at;
    if (m.paused) void m.play().catch(() => undefined);
  }

  private park(e: Entry, first: number) {
    if (e.kind !== "audio") e.el.hidden = true;
    if (e.kind === "image") return;
    const m = e.el as HTMLMediaElement;
    if (!m.paused) m.pause();
    const at = first + this.g.s(0.5);
    if (Math.abs(m.currentTime - at) > 1e-3) m.currentTime = at;
  }

  /** The element a clip plays in, made when first needed; null for a take this browser cannot play. */
  private ensure(c: Norm["tracks"][number]["clips"][number], ti: number): Entry | null {
    const k = keyOf(c);
    const have = this.els.get(k);
    if (have) return have;
    const take = c.take_id!;
    const url = this.urls[take];
    if (!url || this.unplayable.has(take)) return null;
    const kind = ti > 0 ? "audio" : kindOf(c, this.takes) === "image" ? "image" : "video";
    const el = document.createElement(kind === "image" ? "img" : kind);
    el.className = "absolute inset-0 size-full object-contain";
    if (el instanceof HTMLMediaElement) {
      el.preload = "auto";
      (el as HTMLVideoElement).playsInline = true;
    } else el.alt = "";
    el.hidden = true;
    if (kind !== "audio") this.stage.append(el);
    const e: Entry = { el, kind, take };
    this.els.set(k, e);
    el.addEventListener("error", () => {
      // a dropped element's emptied src errors too (Chrome): only a live one says the take cannot play
      if (this.els.get(k) !== e) return;
      this.unplayable.add(take);
      this.drop(k, e);
      this.report();
    });
    el.src = url;
    return e;
  }

  /** Linear gain: a GainNode once Web Audio is up (so +dB is louder), else the element's volume (at most 1). */
  private setGain(e: Entry, g: number) {
    const m = e.el as HTMLMediaElement;
    if (this.ctx && !e.gain) {
      try {
        e.gain = this.ctx.createGain();
        this.ctx.createMediaElementSource(m).connect(e.gain).connect(this.ctx.destination);
      } catch {
        e.gain = undefined;
      }
    }
    if (e.gain) {
      e.gain.gain.value = g;
      m.volume = 1;
      m.muted = false;
    } else {
      m.muted = g <= 0;
      m.volume = Math.max(0, Math.min(1, g));
    }
  }

  private drop(k: string, e: Entry) {
    if (e.el instanceof HTMLMediaElement) e.el.pause();
    e.gain?.disconnect();
    e.el.removeAttribute("src");
    e.el.remove();
    this.els.delete(k);
  }
}

/** An element belongs to a clip's source window: a trim makes a fresh one parked on the new first frame. */
const keyOf = (c: { id: string; take_id: string | null; in?: number; hold?: number }) =>
  `${c.id}:${c.take_id}:${c.in ?? ""}:${c.hold ?? ""}`;
