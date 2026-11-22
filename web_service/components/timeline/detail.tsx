"use client";
// The lanes' canvases (plan 23 §27.4, §27.8; board docs/mock/TimelineTools.dc.html, "Zoomed in to frames" and
// "Zoomed out to fit"): the ruler's ticks, a V1 clip's filmstrip and an audio clip's waveform, each drawn only over
// the virtual window (the screen and one each side), redrawn on scroll, zoom, a new tile or peaks, or a theme change.
// Not laid out yet (no window): nothing is drawn.
import { useEffect, useRef, useState } from "react";
import { Grid } from "@/lib/timeline";
import { bucketFrame, rulerTicks, tileWidth } from "@/lib/timeline-tools";
import { followDetail, peakAt, peaksOf, poster, tile, unwant, want } from "@/lib/timeline-detail";

type Win = [number, number];
const laidOut = (w: Win) => Number.isFinite(w[0]);

/** A canvas `w` × `h` CSS px, sharp on this screen; its 2d context (null: none, e.g. a test's DOM). */
function paint(c: HTMLCanvasElement, w: number, h: number) {
  const r = window.devicePixelRatio || 1;
  c.width = Math.max(1, Math.round(w * r));
  c.height = Math.round(h * r);
  c.style.width = `${w}px`;
  c.style.height = `${h}px`;
  const x = c.getContext("2d");
  x?.setTransform(r, 0, 0, r, 0, 0);
  return x;
}
const css = (el: Element, name: string) => getComputedStyle(el).getPropertyValue(name).trim();

/** The ruler (the board's 22 px `.ru`): a tick per step with its label, 4 minors between seconds' steps. */
export function RulerCanvas({ pps, fps, win, width }: { pps: number; fps: number; win: Win; width: number }) {
  const ref = useRef<HTMLCanvasElement>(null);
  const a = Math.max(0, win[0]);
  const b = Math.min(width, win[1]);
  useEffect(() => {
    const c = ref.current;
    if (!c || !laidOut(win) || b <= a) return;
    const draw = () => {
      const x = paint(c, b - a, 22);
      if (!x) return;
      const font = css(c, "--font-mono") || "monospace";
      x.font = `10px ${font}`;
      x.textBaseline = "top";
      x.fillStyle = css(c, "--s4");
      x.fillRect(0, 21, b - a, 1);
      const g = new Grid({ fps, sample_rate: 48000 });
      for (const k of rulerTicks(a / pps, b / pps, pps, g)) {
        const px = Math.round(k.t * pps - a) + 0.5;
        x.fillStyle = css(c, k.label ? (k.major ? "--dash" : "--ln") : "--s5");
        if (k.label) x.fillRect(px - 0.5, 0, 1, 21);
        else x.fillRect(px - 0.5, 16, 1, 5);
        if (k.label) {
          x.fillStyle = css(c, k.major ? "--t1b" : "--t2");
          x.fillText(k.label, px + 3.5, 1);
        }
      }
    };
    draw();
    return followDetail(draw);
  }, [a, b, pps, fps, win]);
  if (!laidOut(win) || b <= a) return null;
  return (
    <canvas ref={ref} aria-hidden className="pointer-events-none absolute top-0" style={{ left: a }} />
  );
}

/** The part of a clip at track px [x0, x0 + w) inside the window, clip-local: [left, width]. */
const part = (x0: number, w: number, win: Win): [number, number] => {
  const a = Math.max(x0, win[0]);
  const b = Math.min(x0 + w, win[1]);
  return [a - x0, b - a];
};

/** A V1 clip's filmstrip (the board's `.fs`, 26 px at the clip's foot): a tile per `tileWidth` on the sequence's
 * grid, each its take's frame at the tile's source time (a still: its picture; loading: the poster). */
export function Filmstrip(p: {
  id: string;
  take: string;
  url?: string;
  posterUrl?: string;
  still: boolean;
  x0: number;
  w: number;
  /** the clip's source in and length (s) */
  src: [number, number];
  pps: number;
  fps: number;
  win: Win;
}) {
  const ref = useRef<HTMLCanvasElement>(null);
  const { id, take, url, posterUrl, still, x0, w, src, pps, fps, win } = p;
  const [left, width] = part(x0, w, win);
  const [s0, len] = src;
  useEffect(() => {
    const c = ref.current;
    if (!c || !laidOut(win) || width <= 0) return;
    const draw = () => {
      const x = paint(c, width, 26);
      if (!x) return;
      const tw = tileWidth(pps, fps);
      const v0 = x0 + left;
      const list: Parameters<typeof want>[1] = [];
      const gap = css(c, "--s1");
      const blank = `color-mix(in srgb, ${css(c, "--t1")} 13%, transparent)`;
      const pic = posterUrl ? poster(posterUrl) : undefined;
      for (let i = Math.floor(v0 / tw); i * tw < v0 + width; i++) {
        const tx = i * tw - v0;
        let im: CanvasImageSource | undefined = pic;
        if (!still && url) {
          const at = Math.min(s0 + len - 1 / fps, Math.max(s0, s0 + (i * tw + tw / 2 - x0) / pps));
          const f = bucketFrame(at, tw / pps, fps);
          const t = tile(take, f);
          if (t) im = t;
          else list.push({ take, frame: f, url, t: (f + 0.5) / fps });
        }
        if (im) cover(x, im, tx, 0, tw - 2, 26);
        else {
          x.fillStyle = blank;
          x.fillRect(tx, 0, tw - 2, 26);
        }
        x.fillStyle = gap;
        x.fillRect(tx + tw - 2, 0, 2, 26);
      }
      want(id, list);
    };
    draw();
    const off = followDetail(draw);
    return () => {
      off();
      unwant(id);
    };
  }, [id, take, url, posterUrl, still, x0, left, width, s0, len, pps, fps, win]);
  if (!laidOut(win) || width <= 0) return null;
  return (
    <canvas
      ref={ref}
      aria-hidden
      className="pointer-events-none absolute bottom-0"
      style={{ left }}
    />
  );
}

function cover(x: CanvasRenderingContext2D, im: CanvasImageSource, dx: number, dy: number, dw: number, dh: number) {
  const iw = (im as HTMLImageElement).naturalWidth || (im as HTMLCanvasElement).width;
  const ih = (im as HTMLImageElement).naturalHeight || (im as HTMLCanvasElement).height;
  if (!iw || !ih) return;
  const s = Math.max(dw / iw, dh / ih);
  const sw = dw / s;
  const sh = dh / s;
  x.drawImage(im, (iw - sw) / 2, (ih - sh) / 2, sw, sh, dx, dy, dw, dh);
}

/** An audio clip's waveform (the board's `.wv`: a 2 px bar every 4 px, the loudest |x| under it, centred), more
 * columns of sound as it zooms; until the peaks come (or never: too long, undecodable) `fallback` shows. */
export function Waveform(p: {
  take: string;
  url?: string;
  duration: number;
  x0: number;
  w: number;
  h: number;
  /** the clip's source in (s) */
  src: number;
  pps: number;
  win: Win;
  fallback?: React.ReactNode;
}) {
  const ref = useRef<HTMLCanvasElement>(null);
  const { take, url, duration, x0, w, h, src, pps, win } = p;
  const [left, width] = part(x0, w, win);
  const [ready, setReady] = useState(false);
  useEffect(() => {
    if (!laidOut(win) || width <= 0) return;
    const draw = () => {
      const pk = peaksOf(take, url, duration);
      setReady(!!pk);
      const c = ref.current;
      if (!pk || !c) return;
      const x = paint(c, width, h);
      if (!x) return;
      x.strokeStyle = css(c, "--t2");
      x.globalAlpha = 0.7;
      x.lineWidth = 2;
      x.lineCap = "round";
      x.beginPath();
      for (let cx = 2 + 4 * Math.ceil((left - 2) / 4); cx < left + width; cx += 4) {
        const a = src + (cx - 2) / pps;
        const v = Math.min(1, peakAt(pk, Math.max(0, a), a + 4 / pps));
        const half = Math.max(1, (v * (h - 6)) / 2);
        x.moveTo(cx - left, h / 2 - half);
        x.lineTo(cx - left, h / 2 + half);
      }
      x.stroke();
    };
    draw();
    return followDetail(draw);
  }, [take, url, duration, x0, left, width, h, src, pps, win, ready]);
  if (!laidOut(win) || width <= 0) return <>{p.fallback}</>;
  return (
    <>
      {!ready && p.fallback}
      <canvas
        ref={ref}
        aria-hidden
        hidden={!ready}
        className="pointer-events-none absolute top-0"
        style={{ left }}
      />
    </>
  );
}
