"use client";
// The floating player (MagicBoard frame 1): small, dragged by its bar, resized at its corner, shrunk to a bar. It plays
// the board through the Timeline's player (lib/timeline-preview.ts) on the board as a sequence (lib/magic.ts
// toSequence); phase B's fake takes have no files, so the stage shows the shot's picture under the playhead.
import { useEffect, useRef, useState } from "react";
import { Pause, Play } from "lucide-react";
import { clock, duration, shown, type Doc, type Takes } from "@/lib/magic";
import { pictures, playable } from "@/lib/magic-play";
import { Preview } from "@/lib/timeline-preview";

export type PlayerApi = {
  play: (from?: number) => void;
  pause: () => void;
  seek: (f: number) => void;
};

/** The player full screen, or back. */
function toggleFull(el: HTMLElement | null) {
  if (document.fullscreenElement) void document.exitFullscreen().catch(() => undefined);
  else void el?.requestFullscreen?.().catch(() => undefined);
}

export function Player({
  doc,
  takes,
  frame,
  onFrame,
  onPlaying,
  apiRef,
}: {
  doc: Doc;
  takes: Takes;
  frame: number;
  onFrame: (f: number) => void;
  onPlaying: (p: boolean) => void;
  apiRef: React.RefObject<PlayerApi | null>;
}) {
  const stage = useRef<HTMLDivElement>(null);
  const root = useRef<HTMLDivElement>(null);
  const [full, setFull] = useState(false);
  // full screen (MagicBoard frame 1): ⛶ or F; Esc (the browser's) comes back
  useEffect(() => {
    const on = () => setFull(document.fullscreenElement === root.current);
    const key = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement | null;
      if (e.key !== "f" || e.metaKey || e.ctrlKey || e.altKey) return;
      if (t?.closest("input, textarea, select, [contenteditable=true], dialog")) return;
      e.preventDefault();
      toggleFull(root.current);
    };
    document.addEventListener("fullscreenchange", on);
    window.addEventListener("keydown", key);
    return () => {
      document.removeEventListener("fullscreenchange", on);
      window.removeEventListener("keydown", key);
    };
  }, []);
  const pv = useRef<Preview | null>(null);
  const [playing, setPlaying] = useState(false);
  const [box, setBox] = useState({ right: 14, bottom: 14, w: 220, h: 158 });
  const [small, setSmall] = useState(false);
  const fps = doc.target.fps;
  const total = duration(doc);

  // a seek is the board's (its playhead may sit past the last shot, where the engine would clamp it): the engine's
  // own frame is heard only while it plays
  const quiet = useRef(false);
  const seek = (f: number) => {
    quiet.current = true;
    pv.current?.seek(f);
    quiet.current = false;
    onFrame(f);
  };
  useEffect(() => {
    const p = new Preview(
      stage.current!,
      (f) => !quiet.current && onFrame(f),
      (s) => {
        setPlaying(s.playing);
        onPlaying(s.playing);
      },
    );
    pv.current = p;
    return () => p.destroy();
    // the engine lives as long as the player; its callbacks are the board's setters
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  useEffect(() => {
    const p = playable(doc, takes);
    if (p) pv.current?.setDoc(p.doc, p.facts, {});
  }, [doc, takes]);
  useEffect(() => {
    apiRef.current = {
      play: (from) => {
        if (from !== undefined) seek(from);
        pv.current?.play();
      },
      pause: () => pv.current?.pause(),
      seek,
    };
  });

  // the picture: the shot (or, with none, the Storyboard frame) under the playhead, its take's art
  const under = pictures(doc).find((b) => b.at <= frame && frame < b.at + b.length);
  const art = under ? shown(under, takes)?.art : undefined;

  const dragBar = (e: React.PointerEvent) => {
    if ((e.target as Element).closest("button")) return;
    const el = e.currentTarget as HTMLElement;
    el.setPointerCapture?.(e.pointerId);
    const start = { x: e.clientX, y: e.clientY, ...box };
    const move = (m: PointerEvent) =>
      setBox((b) => ({
        ...b,
        right: Math.max(0, start.right - (m.clientX - start.x)),
        bottom: Math.max(0, start.bottom - (m.clientY - start.y)),
      }));
    const stop = () => {
      el.removeEventListener("pointermove", move);
      el.removeEventListener("pointerup", stop);
    };
    el.addEventListener("pointermove", move);
    el.addEventListener("pointerup", stop);
  };
  const resize = (e: React.PointerEvent) => {
    const el = e.currentTarget as HTMLElement;
    el.setPointerCapture?.(e.pointerId);
    const start = { x: e.clientX, y: e.clientY, ...box };
    const move = (m: PointerEvent) =>
      setBox((b) => ({
        ...b,
        w: Math.max(180, start.w + m.clientX - start.x),
        h: Math.max(120, start.h + m.clientY - start.y),
        right: Math.max(0, start.right - (m.clientX - start.x)),
        bottom: Math.max(0, start.bottom - (m.clientY - start.y)),
      }));
    const stop = () => {
      el.removeEventListener("pointermove", move);
      el.removeEventListener("pointerup", stop);
    };
    el.addEventListener("pointermove", move);
    el.addEventListener("pointerup", stop);
  };

  return (
    <div
      ref={root}
      className="fp"
      role="region"
      aria-label="Player, floating"
      style={{ right: box.right, bottom: box.bottom, width: box.w, height: small ? 56 : box.h }}
    >
      <div className="bar" title="Drag to move" onPointerDown={dragBar}>
        <span>Player</span>
        <span className="grow" />
        <button
          type="button"
          aria-label={small ? "Show the picture" : "Shrink to a bar"}
          onClick={() => setSmall((s) => !s)}
        >
          {small ? "▭" : "–"}
        </button>
        <button
          type="button"
          aria-label="Larger"
          onClick={() =>
            setBox((b) => (b.w > 300 ? { ...b, w: 220, h: 158 } : { ...b, w: 440, h: 300 }))
          }
        >
          ▢
        </button>
        <button
          type="button"
          aria-label={full ? "Leave full screen" : "Full screen"}
          aria-keyshortcuts="F"
          title={full ? "Leave full screen (Esc)" : "Full screen (F)"}
          onClick={() => toggleFull(root.current)}
        >
          ⛶
        </button>
      </div>
      <div
        className="vid"
        hidden={small}
        style={{ backgroundImage: art }}
        aria-label="Board preview"
        role="img"
      >
        <div ref={stage} className="absolute inset-0" />
      </div>
      <div className="ctl">
        <button
          type="button"
          className="pb"
          aria-label={playing ? "Pause" : "Play"}
          aria-keyshortcuts="Space"
          onClick={() => (playing ? pv.current?.pause() : pv.current?.play())}
        >
          {playing ? (
            <Pause size={10} fill="currentColor" aria-hidden />
          ) : (
            <Play size={10} fill="currentColor" aria-hidden />
          )}
        </button>
        <span
          className="scrub"
          onPointerDown={(e) => {
            const r = e.currentTarget.getBoundingClientRect();
            seek(Math.round(((e.clientX - r.left) / r.width) * total));
          }}
        >
          <span style={{ width: `${total ? Math.min(100, (frame / total) * 100) : 0}%` }} />
        </span>
        <span className="mono">
          {clock(frame, fps)}
          <span className="text-t2"> / {clock(total, fps)}</span>
        </span>
      </div>
      {!small && <span className="rz" aria-label="Resize" onPointerDown={resize} />}
    </div>
  );
}
