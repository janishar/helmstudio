"use client";
// The rough cut (MagicBoard frame 7; plan 39 phase E): the board's frames in time order, each for its length, with
// its line under it and the voice takes under it played; Space plays or pauses, ← → step a frame. Nothing is stored:
// frames never go to the Timeline.
import { useEffect, useMemo, useRef, useState } from "react";
import { Pause, Play, SkipBack, SkipForward } from "lucide-react";
import { Modal } from "@/components/kit";
import { clock, end, shown, type Doc, type Takes } from "@/lib/magic";
import { roughCut } from "@/lib/magic-assets";

export function RoughCut({
  doc,
  takes,
  from,
  onClose,
}: {
  doc: Doc;
  takes: Takes;
  /** the frame it starts at; null: closed */
  from: string | null | undefined;
  onClose: () => void;
}) {
  return (
    <Modal open={from !== null} onClose={onClose} title="Rough cut" aside="frames and their lines" width="w-[min(94vw,820px)]">
      {from !== null && <Body doc={doc} takes={takes} from={from} />}
    </Modal>
  );
}

function Body({ doc, takes, from }: { doc: Doc; takes: Takes; from?: string }) {
  const fps = doc.target.fps;
  const cut = useMemo(() => roughCut(doc, from), [doc, from]);
  const total = cut.reduce((n, c) => n + c.block.length, 0);
  // where it is, in frames of the cut (not the board: gaps between frames are left out)
  const [t, setT] = useState(0);
  const [playing, setPlaying] = useState(cut.length > 0);
  const starts = cut.map((_, i) => cut.slice(0, i).reduce((n, c) => n + c.block.length, 0));
  const i = Math.max(0, starts.findLastIndex((s) => s <= t));
  const now = cut[i];
  const audio = useRef(new Map<string, HTMLAudioElement>());

  useEffect(() => {
    if (!playing) return;
    let last = performance.now();
    let raf = 0;
    const tick = (ms: number) => {
      const dt = ((ms - last) / 1000) * fps;
      last = ms;
      setT((x) => {
        const next = x + dt;
        if (next >= total) {
          setPlaying(false);
          return total;
        }
        return next;
      });
      raf = requestAnimationFrame(tick);
    };
    raf = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(raf);
  }, [playing, fps, total]);

  // the voice takes under the frame play at their place in it
  useEffect(() => {
    const on = new Set<string>();
    if (playing && now) {
      const boardAt = now.block.at + (t - starts[i]);
      for (const v of now.voices) {
        const url = shown(v, takes)?.url;
        if (!url || boardAt < v.at || boardAt >= end(v)) continue;
        on.add(v.id);
        let el = audio.current.get(v.id);
        if (!el) {
          el = new Audio(url);
          audio.current.set(v.id, el);
        }
        if (el.paused) {
          el.currentTime = (boardAt - v.at) / fps;
          void el.play().catch(() => undefined);
        }
      }
    }
    for (const [id, el] of audio.current) if (!on.has(id) && !el.paused) el.pause();
  });
  useEffect(() => {
    const all = audio.current;
    return () => all.forEach((el) => el.pause());
  }, []);

  const go = (k: number) => {
    const j = Math.min(cut.length - 1, Math.max(0, k));
    setT(starts[j] ?? 0);
  };
  if (!cut.length)
    return <p className="m-0 text-[13px] text-t2">No board frames yet: add a frame on a Storyboard lane.</p>;
  const take = shown(now.block, takes);
  return (
    <div
      className="flex flex-col gap-3"
      tabIndex={-1}
      onKeyDown={(e) => {
        if (e.key === " ") {
          e.preventDefault();
          setPlaying((p) => !p);
        } else if (e.key === "ArrowRight") go(i + 1);
        else if (e.key === "ArrowLeft") go(i - 1);
      }}
    >
      <div
        className="relative flex aspect-video items-end justify-center overflow-hidden rounded-xl bg-black bg-contain bg-center bg-no-repeat"
        style={{ backgroundImage: take?.art }}
        role="img"
        aria-label={`${now.block.name}${take ? "" : ", no picture yet"}`}
      >
        {!take && <span className="absolute inset-0 grid place-items-center text-[13px] text-t2">{now.block.name}</span>}
        {now.line && (
          <p className="m-3 max-w-[90%] rounded-lg bg-black/70 px-3 py-1.5 text-center text-[14px] leading-5 text-white" aria-live="polite">
            {now.line}
          </p>
        )}
      </div>
      <div className="flex items-center gap-2">
        <button type="button" className="chip bevel rounded-[10px] bg-s4" aria-label="Previous frame" onClick={() => go(i - 1)}>
          <SkipBack size={13} aria-hidden />
        </button>
        <button
          type="button"
          className="key flex h-9 items-center gap-1.5 rounded-xl px-3.5 text-[13px]"
          aria-keyshortcuts="Space"
          onClick={() => {
            if (!playing && t >= total) setT(0);
            setPlaying((p) => !p);
          }}
        >
          {playing ? <Pause size={13} aria-hidden /> : <Play size={13} aria-hidden />}
          {playing ? "Pause" : "Play"}
        </button>
        <button type="button" className="chip bevel rounded-[10px] bg-s4" aria-label="Next frame" onClick={() => go(i + 1)}>
          <SkipForward size={13} aria-hidden />
        </button>
        <span className="mono text-[12px]">
          {clock(Math.round(t), fps)}
          <span className="text-t2"> / {clock(total, fps)}</span>
        </span>
        <span className="grow" />
        <span className="text-[12px] text-t2">
          {now.block.name} · {i + 1} of {cut.length}
        </span>
      </div>
      <div className="flex gap-1" role="list" aria-label="Frames">
        {cut.map((c, k) => (
          <button
            key={c.block.id}
            type="button"
            role="listitem"
            aria-current={k === i}
            aria-label={c.block.name}
            className={`h-10 min-w-0 rounded-md bg-s3 bg-cover bg-center ${k === i ? "shadow-[inset_0_0_0_2px_var(--a1)]" : ""}`}
            style={{ flex: c.block.length, backgroundImage: shown(c.block, takes)?.art }}
            onClick={() => go(k)}
          />
        ))}
      </div>
    </div>
  );
}
