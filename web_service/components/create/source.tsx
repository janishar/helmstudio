"use client";
// Media inputs in Create (contracts/README.md "Media inputs", docs/mock/VideoSource.dc.html), from the node schemas
// only: a `media` param is a gallery pick (its poster or a sound tile, its facts, Replace… / Clear, a trim over the
// take when `trim`), a `span` param a range over the workflow's video source (a trim bar on the source's frames,
// the In / Out readouts, keyboard nudges by frame, the stretch the family's frame grid snaps it to). An extension
// (a node's `seconds` and `direction`) shows what it adds. The numbers come from lib/source (check.py's maps).
import { useEffect, useRef, useState } from "react";
import { Pause, Play } from "lucide-react";
import { Info } from "@/components/kit";
import { GalleryPicker } from "@/components/gallery/picker";
import { api, ok, type S } from "@/lib/api";
import { safeUrl } from "@/lib/format";
import { audioFormat } from "@/lib/media";
import {
  audioMap,
  extendMap,
  factsOf,
  familyGrid,
  floorGrid,
  mediaFit,
  pickable,
  retakeMap,
  spanEdge,
  trimmed,
  type FrameGrid,
} from "@/lib/source";
import { remember, useTakes } from "@/lib/take-cache";
import { timecode } from "@/lib/timeline";
import { isRef, isSpan, toRaw, type Field, type Raw, type Update } from "@/lib/workflow";

type Take = S<"Take">;
type Change = (key: string, u: Update) => void;

const rawOf = (f: Field, raws: Record<string, Raw>) => raws[f.key] ?? toRaw(f.initial, f.param);
const refOf = (f: Field, raws: Record<string, Raw>) => {
  const r = rawOf(f, raws);
  return isRef(r) ? (r as S<"MediaRef">) : null;
};
/** "0:04.000": audio in and out to the millisecond. */
const msCode = (s: number) => `${Math.floor(s / 60)}:${(s % 60).toFixed(3).padStart(6, "0")}`;

// ---------------------------------------------------------------- the family's frame grid
let nodes: Promise<S<"NodePalette">> | null = null;
/** The family's frame grid (lib/source familyGrid) from the palette, read once per tab; null until known or none. */
export function useFrameGrid(family?: string): FrameGrid | null {
  const [p, setP] = useState<S<"NodePalette"> | null>(null);
  useEffect(() => {
    if (!family) return;
    let stop = false;
    nodes ??= ok(api.GET("/v1/studio/nodes")).catch((e) => {
      nodes = null;
      throw e;
    });
    nodes.then((r) => !stop && setP(r)).catch(() => undefined);
    return () => {
      stop = true;
    };
  }, [family]);
  return p
    ? familyGrid(
        p.groups.flatMap((g) => g.nodes),
        family,
      )
    : null;
}

/** The workflow's video source: its one media field that accepts video (a span is a range on it). */
export const videoSource = (fs: Field[]) =>
  fs.find((f) => f.param.type === "media" && f.param.accepts?.includes("video"));
/** An extension: a node's `seconds` float and its after / before `direction`. */
export function extensionOf(fs: Field[]) {
  const seconds = fs.find(
    (f) =>
      f.param.name === "seconds" &&
      f.param.type === "float" &&
      fs.some((d) => d.node === f.node && d.param.name === "direction"),
  );
  const direction =
    seconds && fs.find((d) => d.node === seconds.node && d.param.name === "direction");
  return seconds && direction ? { seconds, direction } : null;
}

/** A take's facts line in a media picker: "448 × 256 · 49 f · 2.04 s", "mp3 · 20.0 s", "sound · 5.04 s". */
export function takeFacts(t: Take, audioInput = false): string {
  if (t.kind === "audio")
    return `${audioFormat(t.content_type)} · ${(t.audio?.duration_s ?? 0).toFixed(1)} s`;
  if (!t.video) return t.width && t.height ? `${t.width} × ${t.height}` : "";
  const d = `${t.video.duration_s.toFixed(2)} s`;
  return audioInput ? `sound · ${d}` : `${t.width} × ${t.height} · ${t.video.frames} f · ${d}`;
}

/** The picker of a media param: the takes it accepts; wav or mp3 upload for an audio input. */
function MediaPicker({
  f,
  open,
  onClose,
  onPick,
}: {
  f: Field;
  open: boolean;
  onClose: () => void;
  onPick: (t: Take) => void;
}) {
  const audio = !f.param.accepts?.includes("video");
  return (
    <GalleryPicker
      open={open}
      onClose={onClose}
      max={1}
      title={f.param.label ?? f.param.name}
      kind={audio ? null : "video"}
      only={(t) => pickable(f.param, t)}
      sub={(t) => takeFacts(t, audio)}
      audioUpload={audio}
      onPick={(ts) => {
        remember(ts);
        if (ts[0]) onPick(ts[0]);
      }}
    />
  );
}

/** A new pick's value: the whole take; with `trim`, 0 to the most it takes (as studio fills it). */
const picked = (f: Field, t: Take): S<"MediaRef"> => {
  const d = t.video?.duration_s ?? t.audio?.duration_s ?? 0;
  return f.param.trim
    ? { take_id: t.id, in: 0, out: Math.min(d, f.param.max_s ?? d) }
    : { take_id: t.id };
};

/** A span on the new source: the whole take (as studio prefills an offer). */
const wholeSpan = (t: Take): S<"SpanValue"> => ({ start: 0, end: t.video?.duration_s ?? 0 });

/** The composer's tile of a media param ("Source video · Fox crossing · seed 11"): opens its picker. A new video
 * source sets the workflow's spans to the whole take. */
export function MediaTile({
  f,
  fields,
  raws,
  onChange,
}: {
  f: Field;
  fields: Field[];
  raws: Record<string, Raw>;
  onChange: Change;
}) {
  const [open, setOpen] = useState(false);
  const v = refOf(f, raws);
  const t = useTakes(v ? [v.take_id] : [])(v?.take_id ?? "");
  const label = f.param.label ?? f.param.name;
  const name = v ? (t?.title ?? (t === null ? "gone" : "…")) : "None";
  const thumb = t?.kind === "video" ? safeUrl(t.thumb_url) : undefined;
  return (
    <>
      <button
        type="button"
        aria-haspopup="dialog"
        aria-label={`${label}: ${name}`}
        onClick={() => setOpen(true)}
        className="bevel flex min-h-12 max-w-[34ch] flex-[1_1_200px] items-center gap-2 rounded-xl bg-s3 px-3 py-1.5 text-left"
      >
        {thumb ? (
          <img src={thumb} alt="" className="h-7 w-11 shrink-0 rounded-md object-cover" />
        ) : (
          <span
            aria-hidden
            className={`h-7 w-11 shrink-0 rounded-md ${t?.kind === "audio" || (v && !thumb) ? "wave" : "border border-dashed border-dash"}`}
          />
        )}
        <span className="flex min-w-0 flex-col">
          <small className="mono text-[10px] text-t2">{label}</small>
          <b className="truncate text-[13px] font-semibold">{name}</b>
        </span>
      </button>
      <MediaPicker
        f={f}
        open={open}
        onClose={() => setOpen(false)}
        onPick={(nt) => pick(f, nt, fields, onChange)}
      />
    </>
  );
}

/** Puts a picked take in a media field; a video source also resets every span to the whole take. */
function pick(f: Field, t: Take, fields: Field[], onChange: Change) {
  onChange(f.key, picked(f, t));
  if (videoSource(fields) === f)
    for (const s of fields.filter((x) => x.param.type === "span")) onChange(s.key, wholeSpan(t));
}

/** The composer's tile of a span ("Retake · 0.50 – 1.50 s"): opens its inspector section. */
export function SpanTile({
  f,
  raws,
  onOpen,
}: {
  f: Field;
  raws: Record<string, Raw>;
  onOpen: () => void;
}) {
  const v = rawOf(f, raws);
  return (
    <button
      type="button"
      onClick={onOpen}
      className="bevel flex min-h-12 flex-[1_1_130px] flex-col justify-center rounded-xl bg-s3 px-3 py-1.5 text-left"
    >
      <small className="mono text-[10px] text-t2">{f.param.label ?? f.param.name}</small>
      <b className="text-[13px] font-semibold">
        {isSpan(v) ? `${v.start.toFixed(2)} – ${v.end.toFixed(2)} s` : "Whole take"}
      </b>
    </button>
  );
}

/** A label and its mono value (the board's .row). */
function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex justify-between gap-2.5 text-[13px]">
      <span>{label}</span>
      <span className="mono text-right text-[12px] text-t2">{value}</span>
    </div>
  );
}

/** A label over a mono readout, with a line under it (the board's In / Out tiles). */
function Readout({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <div className="set">
      <span className="block text-[12px] text-t2">{label}</span>
      <span className="mono font-medium">{value}</span>
      {sub && <span className="mono block text-[11px] text-t2">{sub}</span>}
    </div>
  );
}

/** A range over a take: the selection with an in and an out handle (drag them, or focus one and use ← → to move
 * it by `step`, Shift for ten), on a frame grid (`cells`) or a waveform. `snap`: the stretch it snaps to. */
function RangeBar({
  label,
  duration,
  value,
  step,
  cells,
  snap,
  onEdge,
}: {
  label: string;
  duration: number;
  value: [number, number];
  step: number;
  cells?: number;
  snap?: [number, number] | null;
  onEdge: (edge: 0 | 1, t: number) => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const drag = useRef<0 | 1 | null>(null);
  const pct = (t: number) => `${(100 * Math.max(0, Math.min(duration, t))) / (duration || 1)}%`;
  const at = (x: number) => {
    const r = ref.current!.getBoundingClientRect();
    return ((x - r.left) / r.width) * duration;
  };
  const handle = (edge: 0 | 1) => (
    <span
      role="slider"
      tabIndex={0}
      aria-label={edge ? "Out" : "In"}
      aria-valuemin={0}
      aria-valuemax={Math.round(duration * 1000) / 1000}
      aria-valuenow={Math.round(value[edge] * 1000) / 1000}
      aria-valuetext={`${value[edge].toFixed(2)} s`}
      onPointerDown={(e) => {
        e.stopPropagation();
        drag.current = edge;
        ref.current?.setPointerCapture(e.pointerId);
      }}
      onKeyDown={(e) => {
        if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
        e.preventDefault();
        const d = (e.key === "ArrowLeft" ? -step : step) * (e.shiftKey ? 10 : 1);
        onEdge(edge, value[edge] + d);
      }}
      className="absolute top-2 bottom-2 z-10 w-2 -translate-x-1/2 cursor-ew-resize rounded-[3px] bg-a2 focus-visible:outline-2 focus-visible:outline-t1"
      style={{ left: pct(value[edge]) }}
    />
  );
  return (
    <div
      ref={ref}
      role="group"
      aria-label={label}
      className={`relative h-11 touch-none rounded-[10px] ${cells ? "border border-s5" : "wave"}`}
      style={
        cells
          ? {
              background: `repeating-linear-gradient(90deg, var(--s3) 0 calc(100% / ${cells} - 1px), var(--s1) calc(100% / ${cells} - 1px) calc(100% / ${cells}))`,
            }
          : undefined
      }
      onPointerDown={(e) => {
        const t = at(e.clientX);
        drag.current = Math.abs(t - value[0]) <= Math.abs(t - value[1]) ? 0 : 1;
        e.currentTarget.setPointerCapture(e.pointerId);
        onEdge(drag.current, t);
      }}
      onPointerMove={(e) => drag.current !== null && onEdge(drag.current, at(e.clientX))}
      onPointerUp={() => (drag.current = null)}
      onPointerCancel={() => (drag.current = null)}
    >
      <span
        aria-hidden
        className="absolute -top-[3px] -bottom-[3px] box-border rounded-lg border-2 border-a2"
        style={{
          left: pct(value[0]),
          width: `calc(${pct(value[1])} - ${pct(value[0])})`,
          background: "color-mix(in srgb, var(--a2) 14%, transparent)",
        }}
      />
      {handle(0)}
      {handle(1)}
      {snap && (
        <span
          aria-hidden
          className="absolute -bottom-3 h-1.5 rounded-[3px] bg-a1"
          style={{ left: pct(snap[0]), width: `calc(${pct(snap[1])} - ${pct(snap[0])})` }}
        />
      )}
    </div>
  );
}

/** What a span regenerates on the family's grid: the pixel frames and seconds, or why nothing (the run's failure). */
type Stretch = { frames: [number, number]; seconds: [number, number] } | { error: string };
function spanStretch(
  v: S<"SpanValue">,
  t: Take | null | undefined,
  grid: FrameGrid | null,
): Stretch | null {
  if (!t?.video || !grid) return null;
  const r = retakeMap(v.start, v.end, t.video.fps, t.video.frames, grid);
  if ("error" in r) return { error: r.error };
  const fps = t.video.fps;
  return {
    frames: [Math.round(r.map.new_in_s * fps), Math.round(r.map.new_out_s * fps) - 1],
    seconds: [r.map.new_in_s, r.map.new_out_s],
  };
}

/** The stage of a workflow with a span (the board's stage): the source playing the range on a loop, and the trim
 * bar on its frame grid with the stretch it snaps to. */
export function SourceStage({
  span,
  fields,
  raws,
  onChange,
  grid,
}: {
  span: Field;
  fields: Field[];
  raws: Record<string, Raw>;
  onChange: Change;
  grid: FrameGrid | null;
}) {
  const src = videoSource(fields);
  const ref = src ? refOf(src, raws) : null;
  const t = useTakes(ref ? [ref.take_id] : [])(ref?.take_id ?? "");
  const video = useRef<HTMLVideoElement>(null);
  const [playing, setPlaying] = useState(false);
  const raw = rawOf(span, raws);
  const v = t?.video;
  if (!t || !v) return null;
  const range: S<"SpanValue"> = isSpan(raw) ? raw : wholeSpan(t);
  const fr = (s: number) => Math.round(s * v.fps);
  const st = spanStretch(range, t, grid);
  const label = span.param.label ?? span.param.name;
  return (
    <div className="panel relative flex min-h-[300px] grow flex-col items-center justify-center gap-3 bg-inset px-5 pt-[60px] pb-5">
      <span className="mono absolute top-4 left-4 rounded-lg bg-s2/90 px-2.5 py-1.5 text-[12px]">
        {src?.param.label ?? "Source"} · {t.width} × {t.height} · {+v.fps.toFixed(3)} fps
      </span>
      <button
        type="button"
        aria-label={playing ? "Pause" : `Play the ${label.toLowerCase()} range`}
        title="Play the range"
        onClick={() => {
          const el = video.current;
          if (!el) return;
          if (!el.paused) return el.pause();
          if (el.currentTime < range.start || el.currentTime >= range.end)
            el.currentTime = range.start;
          void el.play();
        }}
        className="bevel absolute top-4 right-4 grid size-9 place-items-center rounded-lg border border-s5 bg-s2/90"
      >
        {playing ? (
          <Pause size={16} aria-hidden className="fill-current" />
        ) : (
          <Play size={16} aria-hidden className="fill-current" />
        )}
      </button>
      <video
        ref={video}
        src={safeUrl(t.url)}
        poster={safeUrl(v.poster_url)}
        aria-label={`${src?.param.label ?? "Source"}: ${t.title}`}
        playsInline
        preload="metadata"
        onPlay={() => setPlaying(true)}
        onPause={() => setPlaying(false)}
        onTimeUpdate={(e) => {
          if (e.currentTarget.currentTime >= range.end) e.currentTarget.currentTime = range.start;
        }}
        className="w-full max-w-[560px] rounded-[10px] bg-black"
        style={{ aspectRatio: `${t.width} / ${t.height}` }}
      />
      <div className="flex w-full max-w-[560px] flex-col gap-1.5">
        <div className="flex justify-between text-[12px] text-t2">
          <span className="flex items-center">
            {label}
            {span.param.help && <Info>{span.param.help}</Info>}
          </span>
          <span className="mono">
            {range.start.toFixed(2)} – {range.end.toFixed(2)} s · frames {fr(range.start)} –{" "}
            {fr(range.end)}
          </span>
        </div>
        <RangeBar
          label={`${label} range on the source's ${v.frames} frames`}
          duration={v.duration_s}
          value={[range.start, range.end]}
          step={1 / v.fps}
          cells={v.frames}
          snap={st && "seconds" in st ? st.seconds : null}
          onEdge={(e, x) =>
            onChange(span.key, spanEdge(range, e ? "end" : "start", x, v.fps, v.duration_s))
          }
        />
        <div className="mono mt-2 flex justify-between text-[11px]">
          <span>{timecode(0)}</span>
          {st && "frames" in st ? (
            <span className="text-a1">
              regenerates frames {st.frames[0]} – {st.frames[1]} · {st.seconds[0].toFixed(2)} –{" "}
              {st.seconds[1].toFixed(2)} s
            </span>
          ) : (
            st && <span className="text-bad">{st.error}</span>
          )}
          <span>{timecode(v.duration_s)}</span>
        </div>
      </div>
    </div>
  );
}

/** A span's inspector section: In / Out with their frames, what the grid regenerates, the same node's switches
 * (Regenerate audio) and the field's 422. */
export function SpanControl({
  f,
  fields,
  raws,
  error,
  grid,
  switches,
}: {
  f: Field;
  fields: Field[];
  raws: Record<string, Raw>;
  error?: string;
  grid: FrameGrid | null;
  /** the same node's bool params, drawn by the caller */
  switches?: React.ReactNode;
}) {
  const src = videoSource(fields);
  const ref = src ? refOf(src, raws) : null;
  const t = useTakes(ref ? [ref.take_id] : [])(ref?.take_id ?? "");
  const raw = rawOf(f, raws);
  const v = isSpan(raw) ? raw : t ? wholeSpan(t) : null;
  const fps = t?.video?.fps ?? 0;
  const st = v && spanStretch(v, t, grid);
  return (
    <div className="col-span-2 flex flex-col gap-2.5">
      {v ? (
        <div className="grid grid-cols-2 gap-2">
          <Readout
            label="In"
            value={timecode(v.start)}
            sub={`frame ${Math.round(v.start * fps)}`}
          />
          <Readout label="Out" value={timecode(v.end)} sub={`frame ${Math.round(v.end * fps)}`} />
        </div>
      ) : (
        <span className="text-[13px] text-t2">Pick the {src?.param.label ?? "source"} first.</span>
      )}
      {st && grid && (
        <span className={`mono text-[12px] ${"error" in st ? "text-bad" : ""}`}>
          {"error" in st
            ? st.error
            : `On the ${grid.step}-frame grid: frames ${st.frames[0]} – ${st.frames[1]} are regenerated (${st.seconds[0].toFixed(2)} – ${st.seconds[1].toFixed(2)} s); the rest is kept as context`}
        </span>
      )}
      {switches}
      {error && (
        <span
          id={`f-${f.key.replace(/[^a-z0-9_]/gi, "-")}-err`}
          className="mono text-[12px] text-bad"
        >
          {error}
        </span>
      )}
    </div>
  );
}

/** A media param's inspector section: the take's card (poster or sound tile, title, facts), Replace… / Clear, a
 * trim over it when the param has `trim` (with what the clip makes of the part), and why it does not fit. */
export function MediaControl({
  f,
  fields,
  raws,
  error,
  grid,
  onChange,
}: {
  f: Field;
  fields: Field[];
  raws: Record<string, Raw>;
  error?: string;
  grid: FrameGrid | null;
  onChange: Change;
}) {
  const [open, setOpen] = useState(false);
  const v = refOf(f, raws);
  const t = useTakes(v ? [v.take_id] : [])(v?.take_id ?? "");
  const audioIn = !f.param.accepts?.includes("video");
  const facts = t ? factsOf(t) : null;
  const unfit = facts && mediaFit(f.param, facts);
  const d = facts?.duration_s ?? 0;
  const thumb = t?.kind === "video" ? safeUrl(t.thumb_url) : undefined;
  // the clip a part makes: the same node's frame rate on the family's grid
  const fps = Number(raws[`${f.node}.fps`] ?? f.siblings?.fps);
  const part =
    v && f.param.trim ? ([v.in ?? 0, v.out ?? Math.min(d, f.param.max_s ?? d)] as const) : null;
  const clip = part && grid && fps > 0 ? audioMap(part[0], part[1], fps, grid) : null;
  return (
    <div className="col-span-2 flex flex-col gap-2.5">
      {t ? (
        <div className="flex flex-col gap-2 rounded-xl bg-s2 px-3 py-2.5">
          <div className="flex items-center gap-2.5">
            {thumb ? (
              <img
                src={thumb}
                alt=""
                className="w-[88px] shrink-0 rounded-lg object-cover"
                style={{ aspectRatio: `${t.width} / ${t.height}` }}
              />
            ) : (
              <span aria-hidden className="wave h-11 w-[88px] shrink-0" />
            )}
            <span className="flex min-w-0 flex-col">
              <b className="truncate text-[13px]">{t.title}</b>
              <span className="mono text-[11px] text-t2">
                {t.kind === "audio" ? takeFacts(t) : (t.made_with?.workflow_title ?? t.source)}
              </span>
            </span>
          </div>
          {t.video && !audioIn && (
            <>
              <Row
                label="Size"
                value={`${t.width} × ${t.height}${f.param.grid ? ` · ${f.param.grid} px grid` : ""}`}
              />
              <Row label="Frames" value={`${t.video.frames} · ${+t.video.fps.toFixed(3)} fps`} />
              <Row
                label="Length · sound"
                value={`${t.video.duration_s.toFixed(2)} s · ${t.video.has_audio ? "yes" : "no"}`}
              />
            </>
          )}
          {t.video && audioIn && <Row label="Length" value={takeFacts(t, true)} />}
        </div>
      ) : (
        <span className="text-[13px] text-t2">
          {t === null ? "The take was deleted." : v ? "Loading…" : "None picked."}
        </span>
      )}
      {part && t && (
        <>
          <RangeBar
            label={`Part of the ${audioIn ? "audio" : "take"}, in ${part[0].toFixed(2)} s, out ${part[1].toFixed(2)} s`}
            duration={d}
            value={[part[0], part[1]]}
            step={t.video && !audioIn ? 1 / t.video.fps : 0.01}
            onEdge={(e, x) =>
              onChange(f.key, trimmed(v!, factsOf(t), f.param, e ? "out" : "in", x))
            }
          />
          <div className="mt-1 grid grid-cols-2 gap-2">
            <Readout label="In" value={msCode(part[0])} />
            <Readout label="Out" value={msCode(part[1])} />
          </div>
          {clip && (
            <Row
              label="Clip"
              value={
                "error" in clip
                  ? clip.error
                  : `${(part[1] - part[0]).toFixed(1)} s part → ${clip.frames} frames · ${clip.map.new_out_s.toFixed(2)} s · ${fps} fps`
              }
            />
          )}
        </>
      )}
      <div className="grid grid-cols-2 gap-2">
        <button
          type="button"
          aria-haspopup="dialog"
          className="h-10 rounded-[10px] border border-dashed border-dash bg-transparent text-[13px]"
          onClick={() => setOpen(true)}
        >
          {v ? "Replace…" : "Pick…"}
        </button>
        <button
          type="button"
          disabled={!v}
          className="h-10 rounded-[10px] border border-dashed border-dash bg-transparent text-[13px] disabled:opacity-50"
          onClick={() => onChange(f.key, "")}
        >
          Clear
        </button>
      </div>
      {(error || unfit) && (
        <span
          id={`f-${f.key.replace(/[^a-z0-9_]/gi, "-")}-err`}
          className="mono text-[12px] text-bad"
        >
          {error ?? unfit}
        </span>
      )}
      <MediaPicker
        f={f}
        open={open}
        onClose={() => setOpen(false)}
        onPick={(nt) => pick(f, nt, fields, onChange)}
      />
    </div>
  );
}

/** An extension's seconds (the board's "Add": the slider, the steps and frames it adds, the result and the room
 * left under the source's frame cap) and its direction (After / Before). */
export function ExtendControl({
  seconds,
  direction,
  fields,
  raws,
  error,
  grid,
  onChange,
}: {
  seconds: Field;
  direction: Field;
  fields: Field[];
  raws: Record<string, Raw>;
  error?: string;
  grid: FrameGrid | null;
  onChange: Change;
}) {
  const src = videoSource(fields);
  const ref = src ? refOf(src, raws) : null;
  const t = useTakes(ref ? [ref.take_id] : [])(ref?.take_id ?? "");
  const s = Number(rawOf(seconds, raws));
  const dir = String(rawOf(direction, raws));
  const p = seconds.param;
  const v = t?.video;
  const cap = src?.param.max_frames ?? grid?.max;
  const r = v && grid ? extendMap(s, dir, v.fps, v.frames, grid, cap) : null;
  const n = v && grid ? floorGrid(v.frames, grid) : 0;
  const steps = grid && v ? Math.max(1, Math.ceil((s * v.fps) / grid.step - 1e-9)) : 0;
  const id = `f-${seconds.key.replace(/[^a-z0-9_]/gi, "-")}`;
  return (
    <div className="col-span-2 flex flex-col gap-2.5">
      <label htmlFor={id} className="flex items-center justify-between gap-2">
        <span className="flex items-center">
          {p.label ?? p.name}
          {p.help && <Info>{p.help}</Info>}
        </span>
        <span className="mono text-[12px]">
          {Number.isFinite(s) ? s.toFixed(1) : "—"} s
          {grid && v ? ` · ${steps} steps · + ${steps * grid.step} frames` : ""}
        </span>
      </label>
      <input
        id={id}
        type="range"
        min={p.min}
        max={p.max}
        step={p.step ?? 0.1}
        value={Number.isFinite(s) ? s : (p.min ?? 0)}
        onChange={(e) => onChange(seconds.key, e.target.value)}
        className="w-full accent-a2"
      />
      <div className="flex items-center justify-between gap-2">
        <span>{direction.param.label ?? direction.param.name}</span>
        <div
          className="seg"
          role="group"
          aria-label={direction.param.label ?? direction.param.name}
        >
          {direction.param.enum?.map((o) => (
            <button
              key={o}
              type="button"
              aria-pressed={dir === o}
              onClick={() => onChange(direction.key, o)}
            >
              {o[0].toUpperCase() + o.slice(1)}
            </button>
          ))}
        </div>
      </div>
      {r && v && grid && (
        <>
          <Row
            label="Result"
            value={
              "error" in r
                ? r.error
                : `${n} + ${r.frames - n} = ${r.frames} frames · ${(r.frames / v.fps).toFixed(2)} s`
            }
          />
          {cap && (
            <Row
              label="Room left"
              value={`up to ${cap} frames: + ${cap - n} (${((cap - n) / v.fps).toFixed(1)} s)`}
            />
          )}
        </>
      )}
      {error && <span className="mono text-[12px] text-bad">{error}</span>}
    </div>
  );
}
