// The lanes' detail, made in the browser (plan 23 §27.4, §27.8; owner point 8): filmstrip tiles (a take's frame at a
// tile's source time: a hidden <video> seeks, a canvas keeps the frame) and waveform peaks (decodeAudioData into a
// mono 8 kHz OfflineAudioContext, reduced to a max-|x| pyramid, ×2 per level). At most 2 decoders of each kind run,
// on idle time; frames are an LRU of 300 tiles per take and frame bucket, peaks are kept per take, a take over 20 min
// is skipped (the stripe stays). Only what a drawn strip still wants is decoded: a strip that scrolls away or
// unmounts drops its wants. Listeners hear every new tile or peaks (and a theme change) and redraw.

const MAX_TILES = 300;
const DECODERS = 2;
const PEAK_RATE = 8000;
const PEAK_BUCKET = 64; // samples per level-0 bucket (8 ms)
const MAX_PEAK_S = 20 * 60;
/** A tile's pixels: twice the board's 26 px strip, 16:9. */
const TILE_H = 52;
const TILE_W = 92;

type Listener = () => void;
const listeners = new Set<Listener>();
let queued = 0;
/** Redraws on the next animation frame, once for any number of updates. */
function changed() {
  if (queued || typeof requestAnimationFrame === "undefined") return;
  queued = requestAnimationFrame(() => {
    queued = 0;
    for (const l of [...listeners]) l();
  });
}
let themeWatch: MutationObserver | null = null;
export function followDetail(l: Listener): () => void {
  listeners.add(l);
  if (!themeWatch && typeof MutationObserver !== "undefined") {
    themeWatch = new MutationObserver(changed);
    themeWatch.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
  }
  return () => void listeners.delete(l);
}

const idle = (f: () => void) =>
  typeof requestIdleCallback !== "undefined" ? requestIdleCallback(f, { timeout: 200 }) : setTimeout(f, 0);

// ------------------------------------------------------------------ frames
type Tile = HTMLCanvasElement;
const tiles = new Map<string, Tile>(); // insertion order is the LRU's
const failed = new Set<string>(); // urls that would not load
/** strip id → the tile keys it wants now, with what decoding each needs */
const wants = new Map<string, Map<string, { url: string; t: number }>>();
const busy = new Set<string>();
const players: { el: HTMLVideoElement; url: string; busy: boolean }[] = [];

const tileKey = (take: string, frame: number) => `${take}:${frame}`;

/** A take's tile at a frame bucket, if made (and now the most recent in the LRU). */
export function tile(take: string, frame: number): Tile | undefined {
  const k = tileKey(take, frame);
  const t = tiles.get(k);
  if (t) {
    tiles.delete(k);
    tiles.set(k, t);
  }
  return t;
}

/** What strip `id` wants now (replacing what it wanted before): each a take's frame at time `t` from `url`. */
export function want(id: string, list: { take: string; frame: number; url: string; t: number }[]) {
  const m = new Map<string, { url: string; t: number }>();
  for (const w of list) {
    const k = tileKey(w.take, w.frame);
    if (!tiles.has(k) && !failed.has(w.url)) m.set(k, { url: w.url, t: w.t });
  }
  if (m.size) wants.set(id, m);
  else wants.delete(id);
  if (m.size) idle(pump);
}
export const unwant = (id: string) => void wants.delete(id);

function next(prefer: string | undefined): [string, { url: string; t: number }] | null {
  let any: [string, { url: string; t: number }] | null = null;
  for (const m of wants.values())
    for (const e of m) {
      if (busy.has(e[0]) || tiles.has(e[0]) || failed.has(e[1].url)) continue;
      if (e[1].url === prefer) return e;
      any ??= e;
    }
  return any;
}

function pump() {
  if (typeof document === "undefined") return;
  while (players.length < DECODERS) {
    const el = document.createElement("video");
    el.muted = true;
    el.preload = "auto";
    el.playsInline = true;
    players.push({ el, url: "", busy: false });
  }
  for (const p of players) {
    if (p.busy) continue;
    const job = next(p.url);
    if (!job) return;
    p.busy = true;
    busy.add(job[0]);
    void grab(p, job[0], job[1]).finally(() => {
      p.busy = false;
      busy.delete(job[0]);
      idle(pump);
    });
  }
}

/** The event, false on an error or after 8 s (a seek that never lands must not hold a decoder). */
const once = (el: HTMLVideoElement, ok: string) =>
  new Promise<boolean>((res) => {
    const timer = setTimeout(() => done(false), 8000);
    const done = (v: boolean) => {
      clearTimeout(timer);
      el.removeEventListener(ok, yes);
      el.removeEventListener("error", no);
      res(v);
    };
    const yes = () => done(true);
    const no = () => done(false);
    el.addEventListener(ok, yes);
    el.addEventListener("error", no);
  });

async function grab(
  p: { el: HTMLVideoElement; url: string },
  key: string,
  job: { url: string; t: number },
) {
  const el = p.el;
  if (p.url !== job.url) {
    p.url = job.url;
    const loaded = once(el, "loadeddata");
    el.src = job.url;
    if (!(await loaded)) {
      failed.add(job.url);
      p.url = "";
      return;
    }
  }
  const seeked = once(el, "seeked");
  el.currentTime = Math.min(job.t, Math.max(0, (el.duration || job.t) - 0.01));
  if (!(await seeked)) return;
  const c = document.createElement("canvas");
  c.width = TILE_W;
  c.height = TILE_H;
  const x = c.getContext("2d");
  if (!x) return;
  // cover: the frame fills the tile, cut at the sides or the top
  const vw = el.videoWidth || TILE_W;
  const vh = el.videoHeight || TILE_H;
  const s = Math.max(TILE_W / vw, TILE_H / vh);
  x.drawImage(el, (TILE_W - vw * s) / 2, (TILE_H - vh * s) / 2, vw * s, vh * s);
  tiles.set(key, c);
  while (tiles.size > MAX_TILES) tiles.delete(tiles.keys().next().value!);
  changed();
}

/** A poster (plan 22's thumbnail), the tiles' stand-in while frames load and a still's every tile; undefined
 * until it has loaded. */
const posters = new Map<string, HTMLImageElement>();
export function poster(url: string): HTMLImageElement | undefined {
  let im = posters.get(url);
  if (!im) {
    im = new Image();
    im.onload = changed;
    im.src = url;
    posters.set(url, im);
  }
  return im.complete && im.naturalWidth ? im : undefined;
}

// ------------------------------------------------------------------ peaks
/** A take's peaks: level 0 holds max |x| per 64 samples at 8 kHz, each next level the max of two. */
export type Peaks = Float32Array[];
const peaks = new Map<string, Peaks | "loading" | "none">();
let decoding = 0;
const waiting: (() => void)[] = [];

/** A take's peaks if made; asks for them once (null: not yet, or never: offline, too long, undecodable). */
export function peaksOf(take: string, url: string | undefined, duration: number): Peaks | null {
  const have = peaks.get(take);
  if (have) return typeof have === "string" ? null : have;
  if (!url || duration > MAX_PEAK_S || typeof OfflineAudioContext === "undefined") {
    peaks.set(take, "none");
    return null;
  }
  peaks.set(take, "loading");
  // counted when scheduled, not when run: many strips ask in one render, before any idle callback runs
  const run = () => {
    void decode(url)
      .then((p) => peaks.set(take, p ?? "none"))
      .catch(() => peaks.set(take, "none"))
      .finally(() => {
        decoding--;
        waiting.shift()?.();
        changed();
      });
  };
  const start = () => {
    decoding++;
    idle(run);
  };
  if (decoding < DECODERS) start();
  else waiting.push(start);
  return null;
}

async function decode(url: string): Promise<Peaks | null> {
  const r = await fetch(url);
  if (!r.ok) return null;
  const buf = await new OfflineAudioContext(1, 1, PEAK_RATE).decodeAudioData(await r.arrayBuffer());
  return pyramid(Array.from({ length: buf.numberOfChannels }, (_, i) => buf.getChannelData(i)));
}

/** The pyramid of max |x| over the channels' samples (8 kHz). */
export function pyramid(channels: Float32Array[]): Peaks {
  const n = channels[0]?.length ?? 0;
  const l0 = new Float32Array(Math.ceil(n / PEAK_BUCKET));
  for (const ch of channels)
    for (let i = 0; i < n; i++) {
      const v = Math.abs(ch[i]);
      const b = (i / PEAK_BUCKET) | 0;
      if (v > l0[b]) l0[b] = v;
    }
  const out: Peaks = [l0];
  while (out[out.length - 1].length > 1) {
    const p = out[out.length - 1];
    const q = new Float32Array(Math.ceil(p.length / 2));
    for (let i = 0; i < q.length; i++) q[i] = Math.max(p[2 * i], p[2 * i + 1] ?? 0);
    out.push(q);
  }
  return out;
}

/** The loudest |x| over [a, b) seconds, from the coarsest level with at least 4 buckets in the span (the edges
 * reach past it by under a quarter). */
export function peakAt(p: Peaks, a: number, b: number): number {
  const span = (b - a) * PEAK_RATE;
  let l = 0;
  while (l + 1 < p.length && PEAK_BUCKET * 2 ** (l + 1) * 4 <= span) l++;
  const size = PEAK_BUCKET * 2 ** l;
  const lv = p[l];
  const i0 = Math.max(0, Math.floor((a * PEAK_RATE) / size));
  const i1 = Math.min(lv.length, Math.max(i0 + 1, Math.ceil((b * PEAK_RATE) / size)));
  let m = 0;
  for (let i = i0; i < i1; i++) if (lv[i] > m) m = lv[i];
  return m;
}
