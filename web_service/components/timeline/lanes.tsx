"use client";
// The tracks (docs/mock/Timeline.dc.html, the lanes panel): the ruler, V1's clips, A1 (V1's own sound), the extra
// audio tracks and the playhead. Drag a clip to reorder (V1, by midpoint) or move it (audio: snapping, another audio
// lane); drag an edge (7 px) to trim, snapping to frames and cuts within 6 px; click the ruler to seek; Ctrl/⌘ +
// wheel zooms. Every drag is an Op handed to the editor, which applies and saves it.
import { useRef, useState } from "react";
import { X } from "lucide-react";
import type { S } from "@/lib/api";
import { safeUrl } from "@/lib/format";
import { actionShot } from "@/components/timeline/actions";
import {
  Grid,
  cuts,
  dropIndex,
  kindOf,
  lenOf,
  snapPoint,
  shortTime,
  type Clip,
  type Norm,
  type Op,
  type Takes,
  shotBusy,
} from "@/lib/timeline";

type TTake = S<"TimelineTake">;

const HEAD = 72; // the lane name column (px)
const EDGE_PX = 7;
const DRAG_PX = 3;

/** A V1 clip's shot number as a name ("Shot 4", API 1.29), else its take's title. */
export function shotName(c: Clip, title?: string): string {
  return c.number ? `Shot ${c.number}` : (title ?? "Clip");
}

/** A clip's title and its mono line (the board's "Shot 2 · fox trots in" / "LTX · 5.0 s · dissolve in 12 f"). */
export function clipText(
  c: Clip,
  audio: boolean,
  takes: Record<string, TTake>,
  facts: Takes,
  g: Grid,
  progress?: string,
): { title: string; line: string } {
  const len = `${g.s(lenOf(c, kindOf(c, facts), g, audio)).toFixed(1)} s`;
  const t = c.take_id ? takes[c.take_id] : undefined;
  if (c.shot) {
    const st = c.shot.status ?? "queued";
    // a take action's shot: "Retaking Shot 3", "Extending", "Retake failed" (docs/mock/TakeActions.dc.html)
    const a = actionShot(c, shotName(c, t?.title));
    if (a?.failed) return { title: a.title, line: c.shot.error ?? "the run failed" };
    return {
      title:
        a?.title ??
        (c.shot.batch_id ? "Re-rendering" : t ? `Redoing ${shotName(c, t.title)}` : "New shot"),
      line: st === "failed" ? `failed: ${c.shot.error ?? "the run failed"}` : (progress ?? st),
    };
  }
  if (!c.take_id) return { title: "Empty", line: `${len} · new shot here` };
  if (c.offline) return { title: "Offline", line: `take deleted · ${len}` };
  if (audio) {
    const db = c.gain_db ?? 0;
    return {
      title: t?.title ?? "Clip",
      line: c.mute ? "muted" : `${db > 0 ? "+" : db < 0 ? "−" : ""}${Math.abs(db)} dB`,
    };
  }
  const maker = (t?.workflow_title ?? (t?.kind === "image" ? "Image" : "Video")).split(" ")[0];
  const fade = c.transition_in ? ` · dissolve in ${c.transition_in.frames} f` : "";
  return {
    title:
      t?.kind === "image"
        ? `Still · ${t.title}`
        : c.number && !audio
          ? `Shot ${c.number} · ${t?.title ?? "clip"}`
          : (t?.title ?? "Clip"),
    line: `${maker} · ${len}${fade}`,
  };
}

type Drag = {
  id: string;
  track: number;
  mode: "in" | "out" | "move";
  x0: number;
  started: boolean;
  span: [number, number];
};

export function Lanes({
  doc,
  takes,
  facts,
  pps,
  frame,
  sel,
  track,
  progress,
  onSelect,
  onTrack,
  onSeek,
  onPreview,
  onCommit,
  onZoom,
  onAddTrack,
  onRemoveTrack,
  range,
}: {
  doc: Norm;
  takes: Record<string, TTake>;
  facts: Takes;
  /** pixels per second */
  pps: number;
  frame: number;
  sel: string[];
  /** the selected audio track's name */
  track: string | null;
  /** clip id → its shot's progress ("3 / 8") */
  progress: Record<string, string>;
  onSelect: (id: string, add: boolean) => void;
  onTrack: (name: string) => void;
  onSeek: (frame: number) => void;
  /** a drag in progress (null: none): drawn at once, not saved */
  onPreview: (op: Op | null) => void;
  onCommit: (op: Op) => void;
  onZoom: (pps: number) => void;
  onAddTrack?: () => void;
  /** an audio lane's ×: remove that track */
  onRemoveTrack?: (name: string) => void;
  /** the marked range (sequence seconds), drawn as a band with its I / O flags */
  range?: [number, number] | null;
}) {
  const g = new Grid(doc.target);
  const drag = useRef<Drag | null>(null);
  const [shift, setShift] = useState<{ id: string; dx: number; track?: number } | null>(null);
  const suppress = useRef(false);
  const end = g.s(doc.frames);
  const width = Math.max(end * pps + 160, 600);
  const steps = [1, 2, 5, 10, 15, 30, 60, 120, 300];
  const step = steps.find((s) => s * pps >= 80) ?? 600;
  const ticks = Array.from({ length: Math.floor(width / pps / step) + 1 }, (_, i) => i * step);
  const v1 = doc.tracks[0];

  /** The op a drag makes so far, or null (nothing changes). */
  function opOf(d: Drag, dx: number, x: number, y: number): Op | null {
    const dt = dx / pps;
    const c = doc.tracks[d.track].clips.find((x) => x.id === d.id)!;
    const list = cuts(doc, facts, d.id);
    if (d.mode === "move") {
      if (d.track === 0) {
        const index = dropIndex(doc, d.id, (d.span[0] + d.span[1]) / 2 + dt, facts);
        return index === v1.clips.findIndex((x) => x.id === d.id)
          ? null
          : { op: "move", clip: d.id, index };
      }
      const at = Math.max(0, snapPoint(c.at! + dt, list, pps, g));
      const lane = laneAt(x, y);
      const name = lane > 0 ? doc.tracks[lane]?.name : undefined;
      return { op: "move", clip: d.id, at, ...(name && { track: name }) };
    }
    const edge = d.mode === "in" ? d.span[0] : d.span[1];
    const delta = snapPoint(edge + dt, list, pps, g) - edge;
    return g.f(delta) ? { op: "trim", clip: d.id, edge: d.mode, delta } : null;
  }
  const laneAt = (x: number, y: number) => {
    const el = document
      .elementsFromPoint(x, y)
      .find((e) => e instanceof HTMLElement && e.dataset.lane !== undefined);
    return el ? Number((el as HTMLElement).dataset.lane) : -1;
  };

  function down(e: React.PointerEvent<HTMLButtonElement>, c: Clip, ti: number) {
    if (e.button !== 0 || shotBusy(c)) return;
    const r = e.currentTarget.getBoundingClientRect();
    const x = e.clientX - r.left;
    const mode = x < EDGE_PX ? "in" : r.width - x < EDGE_PX ? "out" : "move";
    const a = c.at!;
    drag.current = {
      id: c.id,
      track: ti,
      mode,
      x0: e.clientX,
      started: false,
      span: [a, g.s(g.f(a) + lenOf(c, kindOf(c, facts), g, ti > 0))],
    };
    e.currentTarget.setPointerCapture(e.pointerId);
  }
  function move(e: React.PointerEvent) {
    const d = drag.current;
    if (!d) return;
    const dx = e.clientX - d.x0;
    if (!d.started && Math.abs(dx) < DRAG_PX) return;
    d.started = true;
    if (d.mode === "move") setShift({ id: d.id, dx });
    else onPreview(opOf(d, dx, e.clientX, e.clientY));
  }
  function up(e: React.PointerEvent) {
    const d = drag.current;
    drag.current = null;
    if (!d?.started) return;
    suppress.current = true;
    setShift(null);
    onPreview(null);
    const op = opOf(d, e.clientX - d.x0, e.clientX, e.clientY);
    if (op) onCommit(op);
  }

  const seekAt = (e: React.PointerEvent<HTMLElement>) => {
    const r = e.currentTarget.getBoundingClientRect();
    onSeek(g.f(Math.max(0, (e.clientX - r.left) / pps)));
  };

  const clipButton = (c: Clip, ti: number, height: number) => {
    const audio = ti > 0;
    const { title, line } = clipText(c, audio, takes, facts, g, progress[c.id]);
    const t = c.take_id ? takes[c.take_id] : undefined;
    const kind = kindOf(c, facts);
    const w = g.s(lenOf(c, kind, g, audio)) * pps;
    const on = sel.includes(c.id);
    const look = c.shot
      ? "border border-a2 bg-s2"
      : !c.take_id
        ? "border border-dashed border-dash bg-transparent text-t2"
        : c.offline
          ? "border border-dashed border-bad bg-transparent text-bad"
          : audio
            ? "border border-[#86cdea] bg-[rgba(62,224,255,0.12)]"
            : kind === "image"
              ? "border border-a1 bg-[color-mix(in_srgb,var(--a1)_14%,transparent)]"
              : "border border-a2 bg-[rgba(255,122,89,0.14)]";
    const pct = c.shot && progress[c.id]?.match(/(\d+) \/ (\d+)$/);
    const bg = !audio && t && !c.shot && !c.offline ? safeUrl(t.thumb_url) : undefined;
    return (
      <button
        key={c.id}
        type="button"
        data-clip={c.id}
        aria-pressed={on}
        aria-label={`${title}, ${line}${on ? ", selected" : ""}`}
        onPointerDown={(e) => down(e, c, ti)}
        onPointerMove={move}
        onPointerUp={up}
        onPointerCancel={() => {
          drag.current = null;
          setShift(null);
          onPreview(null);
        }}
        onClick={(e) => {
          if (suppress.current) {
            suppress.current = false;
            return;
          }
          onSelect(c.id, e.shiftKey || e.metaKey || e.ctrlKey);
        }}
        className={`tl-clip absolute top-0 ${look} ${on ? "border-2! border-t1!" : ""} ${shift?.id === c.id ? "z-10 opacity-80" : ""}`}
        style={{
          left: c.at! * pps,
          width: Math.max(w, 6),
          height,
          transform: shift?.id === c.id ? `translateX(${shift.dx}px)` : undefined,
          backgroundImage: bg
            ? `linear-gradient(90deg, var(--s1) 30%, color-mix(in srgb, var(--s1) 40%, transparent)), url("${bg}")`
            : undefined,
          backgroundSize: bg ? "auto, auto 100%" : undefined,
          backgroundPosition: bg ? "0 0, right center" : undefined,
          backgroundRepeat: "no-repeat",
          cursor: shotBusy(c) ? "default" : undefined,
        }}
      >
        {height > 40 && <b>{title}</b>}
        {pct ? (
          <span className="flex items-center gap-1.5">
            <span className="block h-1 flex-1 rounded-[2px] bg-s5">
              <span
                className="block h-1 rounded-[2px] bg-a2"
                style={{ width: `${(100 * Number(pct[1])) / Number(pct[2])}%` }}
              />
            </span>
            <small>{line}</small>
          </span>
        ) : (
          <small>{height > 40 ? line : `${title === "Clip" ? "" : title} · ${line}`}</small>
        )}
        {!shotBusy(c) && (
          <>
            <span aria-hidden className="tl-edge left-0" />
            <span aria-hidden className="tl-edge right-0" />
          </>
        )}
      </button>
    );
  };

  return (
    <div
      className="panel shrink-0 overflow-x-auto rounded-[28px_28px_28px_12px] px-3.5 pt-3 pb-3.5"
      onWheel={(e) => {
        if (!e.ctrlKey && !e.metaKey) return;
        e.preventDefault();
        onZoom(Math.min(400, Math.max(5, pps * (e.deltaY < 0 ? 1.25 : 0.8))));
      }}
    >
      <div className="relative flex flex-col gap-1.5" style={{ width: width + HEAD }}>
        <div
          role="slider"
          tabIndex={-1}
          aria-label="Time ruler: click to move the playhead"
          aria-valuemin={0}
          aria-valuemax={doc.frames}
          aria-valuenow={frame}
          aria-valuetext={shortTime(g.s(frame))}
          className="mono relative h-[18px] cursor-pointer text-[10px] text-t2"
          style={{ marginLeft: HEAD }}
          onPointerDown={(e) => {
            seekAt(e);
            e.currentTarget.setPointerCapture(e.pointerId);
          }}
          onPointerMove={(e) => e.buttons === 1 && seekAt(e)}
        >
          {ticks.map((s) => (
            <span key={s} className="absolute top-0" style={{ left: s * pps }}>
              {shortTime(s)}
            </span>
          ))}
        </div>

        <div className="flex h-14 items-stretch" data-lane={0}>
          <span className="mono flex w-[72px] shrink-0 items-center text-[11px] text-t2">V1</span>
          <div className="relative flex-1" role="group" aria-label="V1: the picture">
            {v1.clips.map((c) => clipButton(c, 0, 56))}
            {!v1.clips.length && (
              <span className="flex h-full items-center text-[13px] text-t2">
                No clips yet: add some from the gallery.
              </span>
            )}
          </div>
        </div>

        <div className="flex h-9 items-stretch">
          <span className="mono flex w-[72px] shrink-0 items-center text-[11px] text-t2">A1</span>
          <div className="relative flex-1" aria-label="A1: the clips' own sound" role="group">
            {v1.clips.map((c) => {
              const t = c.take_id ? takes[c.take_id] : undefined;
              if (!t?.video?.has_audio || c.offline) return null;
              const w = g.s(lenOf(c, "video", g)) * pps;
              const gain = (c.gain_db ?? 0) + (v1.gain_db ?? 0);
              return (
                <button
                  key={c.id}
                  type="button"
                  tabIndex={-1}
                  aria-hidden
                  onClick={() => onSelect(c.id, false)}
                  className={`tl-clip absolute top-0 h-9 justify-center border border-line bg-s2 ${c.mute ? "opacity-50" : ""}`}
                  style={{ left: c.at! * pps, width: Math.max(w, 6) }}
                >
                  <small>
                    {c.number ? `Shot ${c.number} sound` : "clip audio"} ·{" "}
                    {c.mute ? "muted" : `${gain > 0 ? "+" : ""}${gain} dB`}
                  </small>
                </button>
              );
            })}
          </div>
        </div>

        {doc.tracks.slice(1).map((t, i) => (
          <div key={t.name} className="flex h-9 items-stretch" data-lane={i + 1}>
            <span className="flex w-[72px] shrink-0 items-center gap-1.5">
              <button
                type="button"
                aria-pressed={track === t.name}
                onClick={() => onTrack(t.name!)}
                className={`mono text-left text-[11px] aria-pressed:text-a1 ${t.mute ? "text-t2 line-through" : "text-t2"}`}
                aria-label={`Track ${t.name}${t.mute ? ", muted" : ""}: settings`}
              >
                {t.name}
              </button>
              {onRemoveTrack && (
                <button
                  type="button"
                  aria-label={`Remove ${t.name}`}
                  title="Remove track"
                  onClick={() => onRemoveTrack(t.name!)}
                  className="grid size-[22px] place-items-center rounded-lg bg-s3 text-t2"
                >
                  <X size={12} aria-hidden />
                </button>
              )}
            </span>
            <div className="relative flex-1" role="group" aria-label={`${t.name}: sound`}>
              {t.clips.map((c) => clipButton(c, i + 1, 36))}
            </div>
          </div>
        ))}
        {onAddTrack && doc.tracks.length < 9 && (
          <button
            type="button"
            className="chip bevel h-8 w-fit rounded-[10px] text-[12px]"
            onClick={onAddTrack}
          >
            + Audio track
          </button>
        )}

        {range && (
          <span
            aria-label={`Range ${shortTime(range[0])} to ${shortTime(range[1])}`}
            className="pointer-events-none absolute top-[18px] bottom-0 block border-x-2 border-a2"
            style={{
              left: HEAD + range[0] * pps,
              width: (range[1] - range[0]) * pps,
              background: "color-mix(in srgb, var(--a2) 12%, transparent)",
            }}
          >
            <span
              aria-hidden
              className="mono absolute -top-[18px] -left-0.5 rounded bg-a2 px-1 text-[10px] text-[var(--on2)]"
            >
              I
            </span>
            <span
              aria-hidden
              className="mono absolute -top-[18px] -right-0.5 rounded bg-a2 px-1 text-[10px] text-[var(--on2)]"
            >
              O
            </span>
          </span>
        )}
        <span
          aria-hidden
          className="pointer-events-none absolute top-3.5 bottom-0 block w-0.5 bg-t1"
          style={{ left: HEAD + g.s(frame) * pps }}
        />
      </div>
    </div>
  );
}
