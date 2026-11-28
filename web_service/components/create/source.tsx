"use client";
// Media inputs in Create (contracts/README.md "Media inputs", docs/mock/VideoSource.dc.html), from the node schemas
// only: a `media` param is a gallery pick (its poster or a sound tile, its facts, Replace… / Clear, a trim over the
// take when `trim`), a `span` param a range over the workflow's video source (a trim bar on the source's frames,
// the In / Out readouts, keyboard nudges by frame, the stretch the family's frame grid snaps it to). An extension
// (a node's `seconds` and `direction`) shows what it adds. The numbers come from lib/source (check.py's maps).
import { useEffect, useRef, useState } from "react";
import { ImageOff, Pause, Play, Video, X } from "lucide-react";
import { Info } from "@/components/kit";
import { TextArea } from "@/components/expand-text";
import { GalleryPicker } from "@/components/gallery/picker";
import { ItemFields } from "@/components/create/rows";
import { Bars, Sections } from "@/components/create/audio";
import { api, ok, type S } from "@/lib/api";
import { safeUrl } from "@/lib/format";
import type { Opener } from "@/components/create/refs";
import { audioFormat } from "@/lib/media";
import {
  audioMap,
  audioSpanEdge,
  extendMap,
  factsOf,
  familyGrid,
  floorGrid,
  mediaFit,
  mediaItem,
  mediaRowsError,
  mediaValueError,
  optionalMedia,
  partOf,
  pickable,
  rangeText,
  retakeMap,
  soundCount,
  spanEdge,
  trimmed,
  type FrameGrid,
} from "@/lib/source";
import { mentionOf, move } from "@/lib/refs";
import { remember, useTakes } from "@/lib/take-cache";
import { timecode } from "@/lib/timeline";
import type { soundRowsOf } from "@/lib/video";
import {
  buildInputs,
  chipText,
  fields as fieldsOf,
  isRef,
  isSpan,
  itemRuleErrors,
  optionBlocked,
  optionText,
  rawsFrom,
  toRaw,
  visible,
  type Field,
  type Raw,
  type Update,
} from "@/lib/workflow";
import { Range } from "@/components/range";

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
/** API 1.79: a span's source when there is no video one: the one media field that takes audio (a song's Repaint). */
export const spanSource = (fs: Field[]) =>
  videoSource(fs) ?? fs.find((f) => f.param.type === "media" && f.param.accepts?.includes("audio"));
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

/** The picker of a media param: the takes it accepts; wav, mp3 or m4a upload for an audio input. */
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
const picked = (p: S<"ParamSchema">, t: Take): S<"MediaRef"> => {
  const d = t.video?.duration_s ?? t.audio?.duration_s ?? 0;
  return p.trim ? { take_id: t.id, in: 0, out: Math.min(d, p.max_s ?? d) } : { take_id: t.id };
};

/** A span on the new source: the whole take (as studio prefills an offer). */
const wholeSpan = (t: Take): S<"SpanValue"> => ({
  start: 0,
  end: t.video?.duration_s ?? t.audio?.duration_s ?? 0,
});

/** The composer's tile of a media param ("Source video · Fox crossing · seed 11"): opens its picker. A new video
 * source sets the workflow's spans to the whole take. */
export function MediaTile({
  f,
  fields,
  raws,
  onChange,
  openerRef,
  clearable,
}: {
  f: Field;
  fields: Field[];
  raws: Record<string, Raw>;
  onChange: Change;
  /** set to what opens the picker (the composer's +) */
  openerRef?: Opener;
  /** music and sound (docs/mock/CreateMusicSound.dc.html 2f, 5): the take's length after its name and a × */
  clearable?: boolean;
}) {
  const [open, setOpen] = useState(false);
  useEffect(() => {
    if (openerRef) openerRef.current = () => setOpen(true);
  }, [openerRef]);
  const v = refOf(f, raws);
  const t = useTakes(v ? [v.take_id] : [])(v?.take_id ?? "");
  const label = f.param.label ?? f.param.name;
  const len = t?.video?.duration_s ?? t?.audio?.duration_s;
  const name = v
    ? `${t?.title ?? (t === null ? "gone" : "…")}${clearable && len != null ? ` · ${clockOf(len)}` : ""}`
    : clearable
      ? f.param.accepts?.includes("video")
        ? "Pick a take"
        : "Drop one"
      : "None";
  const thumb = t?.kind === "video" ? safeUrl(t.thumb_url) : undefined;
  const tile = (
    <button
      type="button"
      aria-haspopup="dialog"
      aria-label={`${label}: ${name}`}
      onClick={() => setOpen(true)}
      className="bevel flex min-h-12 max-w-[34ch] flex-[1_1_220px] items-center gap-2 rounded-xl bg-s3 px-3 py-1.5 text-left"
    >
      {thumb ? (
        <img
          loading="lazy"
          decoding="async"
          src={thumb}
          alt=""
          className="h-7 w-11 shrink-0 rounded-md object-cover"
        />
      ) : (
        <span
          aria-hidden
          className={`grid h-7 w-11 shrink-0 place-items-center rounded-md ${t?.kind === "audio" || (v && !thumb) ? "wave" : "border border-dashed border-dash text-t2"}`}
        >
          {!(t?.kind === "audio" || (v && !thumb)) && <Video size={14} />}
        </span>
      )}
      <span className="flex min-w-0 flex-col">
        <small className="mono text-[10px] text-t2">{label}</small>
        <b className="truncate text-[13px] font-semibold">{name}</b>
      </span>
    </button>
  );
  return (
    <>
      {clearable ? (
        // the tile and its × together, one cell of the composer's grid
        <div className="relative flex min-w-0">
          {tile}
          {v && (
            <button
              type="button"
              aria-label={`Clear ${label}`}
              title={f.param.max_s ? `The first ${f.param.max_s} s are used` : undefined}
              onClick={() => onChange(f.key, "")}
              className="absolute top-1/2 right-2 grid size-6 -translate-y-1/2 place-items-center rounded-md bg-s4 text-t2"
            >
              <X size={13} aria-hidden />
            </button>
          )}
        </div>
      ) : (
        tile
      )}
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
  onChange(f.key, picked(f.param, t));
  if (spanSource(fields) === f)
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

/** "2:10" from seconds. */
const clockOf = (x: number) => {
  const n = Math.round(x);
  return `${Math.floor(n / 60)}:${String(n % 60).padStart(2, "0")}`;
};

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
export function RangeBar({
  label,
  duration,
  value,
  step,
  cells,
  snap,
  onEdge,
  children,
}: {
  label: string;
  duration: number;
  value: [number, number];
  step: number;
  cells?: number;
  snap?: [number, number] | null;
  onEdge: (edge: 0 | 1, t: number) => void;
  /** a waveform to pick on (the Repaint stage's, RangeBar in the Design System): the bar takes its height */
  children?: React.ReactNode;
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
      className={`relative touch-none rounded-[10px] ${children ? "" : "h-11"} ${cells ? "border border-s5" : children ? "" : "wave"}`}
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
      {children}
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
  const src = spanSource(fields);
  const ref = src ? refOf(src, raws) : null;
  const t = useTakes(ref ? [ref.take_id] : [])(ref?.take_id ?? "");
  const raw = rawOf(f, raws);
  const v = isSpan(raw) ? raw : t ? wholeSpan(t) : null;
  const fps = t?.video?.fps ?? 0;
  const st = v && spanStretch(v, t, grid);
  // a range on a song (API 1.79) has no frames: its length instead
  const sub = (s: number) => (fps ? `frame ${Math.round(s * fps)}` : undefined);
  return (
    <div className="col-span-2 flex flex-col gap-2.5">
      {v ? (
        <div className="grid grid-cols-2 gap-2">
          <Readout label="In" value={timecode(v.start)} sub={sub(v.start)} />
          <Readout label="Out" value={timecode(v.end)} sub={sub(v.end)} />
          {!fps && (
            <span className="mono col-span-2 text-[12px] text-t2">{rangeText(v.start, v.end)}</span>
          )}
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
                loading="lazy"
                decoding="async"
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
  const blocked = Object.fromEntries(
    (direction.param.enum ?? []).map((o) => [o, optionBlocked(direction, o, fields, raws)]),
  );
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
      <Range
        id={id}

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
              aria-disabled={blocked[o] ? true : undefined}
              title={blocked[o] ?? undefined}
              className={blocked[o] ? "opacity-55" : undefined}
              onClick={() => !blocked[o] && onChange(direction.key, o)}
            >
              {o[0].toUpperCase() + o.slice(1)}
            </button>
          ))}
        </div>
      </div>
      {/* API 1.39: a value the model does not allow stays visible, faded, with its reason (docs/mock/LtxFeatures 7) */}
      {Object.values(blocked)
        .filter(Boolean)
        .map((why) => (
          <span
            key={why}
            className={`text-right text-[12px] ${blocked[dir] ? "text-bad" : "text-t2"}`}
          >
            {why}
          </span>
        ))}
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

/** An item's optional media field (API 1.37, docs/mock/H3Options.dc.html frames 5-6: a video reference's
 * Soundtrack): Pick when empty (with why Replace needs one), else the take with its trim and part, cut to the
 * video part's length. */
function ItemMedia({
  spec,
  value,
  part,
  rule,
  onSet,
}: {
  spec: S<"ParamSchema">;
  value: S<"MediaRef"> | null;
  part: number | null;
  /** API 1.47: the field's broken rule, studio's text (Replace without it, or it without Replace) */
  rule?: string;
  onSet: (v: S<"MediaRef"> | null) => void;
}) {
  const [picking, setPicking] = useState(false);
  const get = useTakes(value ? [value.take_id] : []);
  const t = value ? get(value.take_id) : undefined;
  const label = spec.label ?? spec.name;
  const d = t ? (factsOf(t).duration_s ?? 0) : 0;
  const ab = value && t ? partOf(value, d, spec) : null;
  const issue = value && t !== undefined ? mediaValueError(spec, value, t && factsOf(t)) : null;
  return (
    <div className="flex flex-col gap-2 border-t border-s3 pt-2">
      <span className="text-[12px] text-t2">{label}</span>
      {!value ? (
        <>
          <button
            type="button"
            aria-haspopup="dialog"
            onClick={() => setPicking(true)}
            className="h-9 rounded-[10px] border border-dashed border-dash bg-transparent text-[13px]"
          >
            Pick {label.toLowerCase()}
          </button>
          {rule && (
            <span role="alert" className="mono text-[12px] text-bad">
              {rule}
            </span>
          )}
        </>
      ) : (
        <>
          <div className="flex items-center gap-2">
            <span aria-hidden className="wave h-7 w-11 shrink-0 rounded-md" />
            <span className="flex min-w-0 grow flex-col">
              <b className="truncate text-[13px]">
                {t ? t.title : t === null ? "The take was deleted." : "Loading…"}
              </b>
              {t && <span className="mono text-[11px] text-t2">{takeFacts(t, true)}</span>}
            </span>
            <button
              type="button"
              aria-label={`Clear ${label.toLowerCase()}`}
              onClick={() => onSet(null)}
              className="grid size-[22px] shrink-0 place-items-center rounded-lg bg-s3 text-t2 hover:text-t1"
            >
              <X size={12} aria-hidden />
            </button>
          </div>
          {ab && t && spec.trim && (
            <>
              <RangeBar
                label={`Part of ${label}, in ${ab[0].toFixed(2)} s, out ${ab[1].toFixed(2)} s`}
                duration={d}
                value={[ab[0], ab[1]]}
                step={0.01}
                onEdge={(e, x) => onSet(trimmed(value!, factsOf(t), spec, e ? "out" : "in", x))}
              />
              <Row
                label="Part"
                value={`${(ab[1] - ab[0]).toFixed(1)} s${part != null ? ` · cut to the video's ${part.toFixed(1)} s` : ""}`}
              />
            </>
          )}
          {(issue || rule) && (
            <span role="alert" className="mono text-[12px] text-bad">
              {issue ?? rule}
            </span>
          )}
        </>
      )}
      <GalleryPicker
        open={picking}
        onClose={() => setPicking(false)}
        max={1}
        title={`Pick ${label.toLowerCase()}`}
        kind={null}
        only={(x) => pickable(spec, x)}
        sub={(x) => takeFacts(x, true)}
        audioUpload
        onPick={(ts) => {
          remember(ts);
          if (ts[0]) onSet(picked(spec, ts[0]));
        }}
      />
    </div>
  );
}

/** API 1.47 (docs/mock/H3Options.dc.html frame 5): the sound a node's references carry together and its limits. */
function SoundLine({
  sound,
  facts,
}: {
  sound: NonNullable<ReturnType<typeof soundRowsOf>>;
  facts: (id: string) => ReturnType<typeof factsOf> | null | undefined;
}) {
  const { s, n } = soundCount(sound.rows, facts, sound.clipS);
  const over = s > sound.limits.max_s + 1e-9 || n > sound.limits.max_inputs;
  return (
    <span className={`mono text-[12px] ${over ? "text-bad" : "text-t2"}`}>
      {s.toFixed(1)} s of {sound.limits.max_s.toFixed(1)} s sound · {n} of {sound.limits.max_inputs}{" "}
      sound inputs
    </span>
  );
}

/** A rows param of media (API 1.22, docs/mock/CreateMediaRefs.dc.html: MiniMax H3's video and audio references):
 * `compact` is the composer's chips (the mention, Remove, Add video / Add audio); else the inspector's cards, each
 * the take (poster or sound tile, facts), its trim with In / Out and the part's length, the item's other fields
 * (Sound: Keep / Silent) and why it does not fit, then the seconds in all and Add. Reorder by Alt + arrows. */
export function MediaRows({
  f,
  raw,
  error,
  onChange,
  compact = false,
  sound,
}: {
  f: Field;
  raw: Raw | undefined;
  error?: string;
  onChange: Change;
  compact?: boolean;
  /** API 1.47: the node's rows of media and clip length, for "3.0 s of 15.0 s sound · 1 of 3 sound inputs" */
  sound?: ReturnType<typeof soundRowsOf>;
}) {
  const [picking, setPicking] = useState(false);
  const list = useRef<HTMLOListElement>(null);
  const p = f.param;
  const [k, spec] = mediaItem(p)!;
  const rows: S<"RowItem">[] = Array.isArray(raw)
    ? raw
    : Array.isArray(f.initial)
      ? (f.initial as S<"RowItem">[])
      : [];
  const refs = rows.map((r) => (isRef(r[k]) ? (r[k] as S<"MediaRef">) : null));
  // this field's takes and (API 1.47) every take its node's sound count reads
  const soundIds = (sound?.rows ?? []).flatMap(({ items }) =>
    items.flatMap((it) => Object.values(it).flatMap((v) => (isRef(v) ? [v.take_id] : []))),
  );
  const get = useTakes([...refs.flatMap((v) => (v ? [v.take_id] : [])), ...soundIds]);
  const itemErrs = itemRuleErrors(f.node, p, rows);
  const facts = (id: string) => {
    const t = get(id);
    return t === undefined ? undefined : t && factsOf(t);
  };
  const audio = !spec.accepts?.includes("video");
  const noun = audio ? "audio" : "video";
  const max = p.max ?? 100;
  const room = max - rows.length;
  const set = (u: (rows: S<"RowItem">[]) => S<"RowItem">[]) =>
    onChange(f.key, (old) => u(Array.isArray(old) ? old : rows));
  const label = (i: number) => mentionOf(p, i + 1);
  // studio's text without the path: "row 2: …" for an item, the total as it is
  const own = mediaRowsError(f.key, p, rows, facts)
    ?.slice(f.key.length)
    .replace(/^\[(\d+)\](?:\.\w+)?: /, "row $1: ")
    .replace(/^: /, "");
  const total = refs.reduce((n, v) => {
    const t = v && get(v.take_id);
    if (!v || !t) return n;
    const [a, b] = partOf(v, factsOf(t).duration_s ?? 0, spec);
    return n + b - a;
  }, 0);
  const picker = (
    <GalleryPicker
      open={picking}
      onClose={() => setPicking(false)}
      max={Math.max(room, 0)}
      title={`Add ${noun}`}
      kind={audio ? null : "video"}
      only={(t) => pickable(spec, t)}
      sub={(t) => takeFacts(t, audio)}
      audioUpload={audio}
      onPick={(ts) => {
        remember(ts);
        set((old) => [...old, ...ts.map((t) => ({ [k]: picked(spec, t) }))].slice(0, max));
      }}
    />
  );
  const addButton = room > 0 && (
    <button
      type="button"
      aria-haspopup="dialog"
      onClick={() => setPicking(true)}
      className={
        compact
          ? "h-8 rounded-lg border border-dashed border-dash bg-transparent px-2.5 text-[12px] text-t2"
          : "h-10 rounded-[10px] border border-dashed border-dash bg-transparent text-[13px]"
      }
    >
      Add {noun}
    </button>
  );
  const remove = (i: number) => set((old) => old.filter((_, j) => j !== i));
  const thumbOf = (t: Take | null | undefined, cls: string) => {
    const src = t?.kind === "video" && !audio ? safeUrl(t.thumb_url) : undefined;
    return src ? (
      <img
        loading="lazy"
        decoding="async"
        src={src}
        alt=""
        draggable={false}
        className={`${cls} object-cover`}
      />
    ) : audio ? (
      <span aria-hidden className={`${cls} wave`} />
    ) : (
      <span
        aria-hidden
        className={`${cls} grid place-items-center border border-dashed border-dash text-t2`}
      >
        {t?.kind === "image" ? <ImageOff size={12} /> : <Video size={12} />}
      </span>
    );
  };

  if (compact)
    return (
      <>
        <div role="list" aria-label={p.label ?? p.name} className="contents">
          {refs.map((v, i) => {
            const t = v ? get(v.take_id) : undefined;
            return (
              <div
                key={i}
                role="listitem"
                aria-label={`${label(i)}: ${t ? t.title : t === null ? "gone" : "loading"}`}
                className="flex h-8 items-center gap-1.5 rounded-lg bg-s5 pr-1 pl-1 text-[12px]"
              >
                {thumbOf(t, "h-6 w-9 shrink-0 rounded")}
                <span className="mono">{label(i)}</span>
                <button
                  type="button"
                  aria-label={`Remove ${label(i)}`}
                  onClick={() => remove(i)}
                  className="grid size-6 place-items-center rounded text-t2 hover:text-t1"
                >
                  <X size={12} aria-hidden />
                </button>
              </div>
            );
          })}
        </div>
        {addButton}
        {picker}
      </>
    );

  return (
    <div className="flex flex-col gap-2.5">
      <span className="mono flex justify-between gap-2 text-[12px] text-t2">
        <span className="min-w-0" />
        <span aria-label={`${rows.length} of ${max}`} className="shrink-0">
          {rows.length} / {max}
        </span>
      </span>
      {rows.length > 0 && (
        <ol
          ref={list}
          aria-label={`${p.label ?? p.name}, in order`}
          className="m-0 flex list-none flex-col gap-2 p-0"
        >
          {rows.map((r, i) => {
            const v = refs[i];
            const t = v ? get(v.take_id) : undefined;
            const d = t ? (factsOf(t).duration_s ?? 0) : 0;
            const part = v && t ? partOf(v, d, spec) : null;
            const issue = v && t !== undefined ? mediaValueError(spec, v, t && factsOf(t)) : null;
            return (
              <li
                key={i}
                tabIndex={-1}
                aria-keyshortcuts="Alt+ArrowUp Alt+ArrowDown"
                onKeyDown={(e) => {
                  const dir =
                    e.altKey && e.key === "ArrowUp"
                      ? -1
                      : e.altKey && e.key === "ArrowDown"
                        ? 1
                        : 0;
                  if (!dir || e.target !== e.currentTarget) return;
                  e.preventDefault();
                  set((old) => move(old, i, i + dir));
                  requestAnimationFrame(() =>
                    list.current
                      ?.querySelectorAll<HTMLElement>(":scope > li")
                      [Math.max(0, Math.min(rows.length - 1, i + dir))]?.focus(),
                  );
                }}
                className={`flex flex-col gap-2 rounded-[10px] border bg-s1 px-3 py-2.5 ${issue ? "border-bad" : "border-s3"}`}
              >
                <div className="flex items-center gap-2">
                  {thumbOf(t, "h-7 w-11 shrink-0 rounded-md")}
                  <span className="flex min-w-0 grow flex-col">
                    <b className="truncate text-[13px]">
                      {t ? t.title : t === null ? "The take was deleted." : "Loading…"}
                    </b>
                    {t && <span className="mono text-[11px] text-t2">{takeFacts(t, audio)}</span>}
                  </span>
                  <span className="mono shrink-0 text-[11px] text-t1">{label(i)}</span>
                  <button
                    type="button"
                    aria-label={`Remove ${label(i)}`}
                    onClick={() => remove(i)}
                    className="grid size-[22px] shrink-0 place-items-center rounded-lg bg-s3 text-t2 hover:text-t1"
                  >
                    <X size={12} aria-hidden />
                  </button>
                </div>
                {part && t && v && spec.trim && (
                  <>
                    <RangeBar
                      label={`Part of ${label(i)}, in ${part[0].toFixed(2)} s, out ${part[1].toFixed(2)} s`}
                      duration={d}
                      value={[part[0], part[1]]}
                      step={t.video && !audio ? 1 / t.video.fps : 0.01}
                      cells={t.video && !audio ? t.video.frames : undefined}
                      onEdge={(e, x) =>
                        set((old) =>
                          old.map((it, j) =>
                            j === i
                              ? { ...it, [k]: trimmed(v, factsOf(t), spec, e ? "out" : "in", x) }
                              : it,
                          ),
                        )
                      }
                    />
                    <Row label="In · Out" value={`${msCode(part[0])} – ${msCode(part[1])}`} />
                    <Row
                      label="Part"
                      value={`${(part[1] - part[0]).toFixed(1)} s${spec.min_s || spec.max_s ? ` · ${spec.min_s ?? 0} to ${spec.max_s ?? "∞"} s` : ""}`}
                    />
                  </>
                )}
                <ItemFields
                  p={p}
                  row={r}
                  id={`f-${f.key.replace(/\W/g, "-")}-${i}`}
                  clip={null}
                  skip={[k, ...optionalMedia(p).map(([o]) => o)]}
                  onRow={(patch) =>
                    set((old) => old.map((it, j) => (j === i ? { ...it, ...patch } : it)))
                  }
                />
                {optionalMedia(p).map(
                  ([o, ospec]) =>
                    // API 1.37: shown while set or while the row's Sound is Replace (MiniMax H3's soundtrack)
                    (isRef(r[o]) || Object.values(r).includes("replace")) && (
                      <ItemMedia
                        key={o}
                        spec={ospec}
                        value={isRef(r[o]) ? (r[o] as S<"MediaRef">) : null}
                        part={part ? part[1] - part[0] : null}
                        rule={itemErrs.find((x) => x.startsWith(`${f.key}[${i + 1}].${o}: `))}
                        onSet={(x) =>
                          set((old) => old.map((it, j) => (j === i ? { ...it, [o]: x } : it)))
                        }
                      />
                    ),
                )}
                {issue && (
                  <span role="alert" className="mono text-[12px] text-bad">
                    {issue}
                  </span>
                )}
              </li>
            );
          })}
        </ol>
      )}
      {sound ? (
        <SoundLine sound={sound} facts={facts} />
      ) : (
        p.max_total_s != null &&
        rows.length > 0 && (
          <span
            className={`mono text-[12px] ${total > p.max_total_s + 1e-9 ? "text-bad" : "text-t2"}`}
          >
            {total.toFixed(1)} s of {p.max_total_s.toFixed(1)} s in all
          </span>
        )
      )}
      {addButton}
      {audio && (
        <span className="text-[12px] text-t2">
          Audio takes and videos with sound come from the gallery; Add audio also uploads wav, mp3
          or m4a.
        </span>
      )}
      {(error || (own && !own.startsWith("row "))) && (
        <span
          id={`f-${f.key.replace(/[^a-z0-9_]/gi, "-")}-err`}
          className="mono text-[12px] text-bad"
        >
          {error ?? own}
        </span>
      )}
      {picker}
    </div>
  );
}

// ---------------------------------------------------------------- music and sound (plan 38, API 1.79)

/** Repaint on the stage (docs/mock/CreateMusicSound.dc.html 3a, RangeBar in the Design System): the source song's
 * sections over its waveform with the range to make again (drag the edges, or click a section to take it), then
 * "0:48 – 1:05 · 17 s", Cancel and Repaint with its estimate. Only the range is made again. */
export function SoundStage({
  span,
  fields,
  raws,
  onChange,
  onCancel,
  onRun,
  estimate,
  busy,
}: {
  span: Field;
  fields: Field[];
  raws: Record<string, Raw>;
  onChange: Change;
  onCancel: () => void;
  onRun: () => void;
  estimate?: string | null;
  busy?: boolean;
}) {
  const src = spanSource(fields);
  const ref = src ? refOf(src, raws) : null;
  const t = useTakes(ref ? [ref.take_id] : [])(ref?.take_id ?? "");
  const d = t?.audio?.duration_s;
  if (!t || !d) return null;
  const raw = rawOf(span, raws);
  const range: S<"SpanValue"> = isSpan(raw) ? raw : wholeSpan(t);
  const action = span.param.label ?? span.param.name;
  const seed = t.made_with?.seed;
  return (
    <div className="panel relative flex min-h-[400px] grow flex-col items-center justify-center gap-3 bg-inset px-6 pt-[68px] pb-6">
      <span className="mono absolute top-4 left-4 rounded-lg bg-s2/90 px-2.5 py-1.5 text-[12px]">
        {[action, seed != null ? `seed ${seed}` : t.title, clockOf(d)].join(" · ")}
      </span>
      <div className="flex w-full max-w-[860px] flex-col gap-2">
        <Sections
          sections={t.audio?.sections ?? []}
          length={d}
          verb={`${action}`}
          onPick={(c) => onChange(span.key, { start: c.start_s, end: c.end_s })}
        />
        <RangeBar
          label={`${action} range on the song's ${clockOf(d)}`}
          duration={d}
          value={[range.start, range.end]}
          step={0.1}
          onEdge={(e, x) => onChange(span.key, audioSpanEdge(range, e ? "end" : "start", x, d))}
        >
          <Bars
            url={safeUrl(t.url)}
            n={100}
            className="h-[168px]"
            lit={[range.start / d, range.end / d]}
          />
        </RangeBar>
        <div className="mt-2 flex flex-wrap items-center gap-2.5">
          <span className="mono text-[13px] text-t1">{rangeText(range.start, range.end)}</span>
          <span className="grow" />
          <button type="button" className="chip bevel rounded-[10px]" onClick={onCancel}>
            Cancel
          </button>
          <button
            type="button"
            disabled={busy}
            onClick={onRun}
            className="key flex h-9 items-center gap-1.5 bg-a2 px-4 text-[13px] text-[var(--on2)]"
          >
            {action}
            {estimate && <span className="mono text-[11px] font-medium">{estimate}</span>}
          </button>
        </div>
      </div>
    </div>
  );
}

/** Cover, Extend or Vary as a popup over its strip button (docs/mock/CreateMusicSound.dc.html 3b, 3c, 4b): the
 * offer's workflow filled as the offer says, with what one may change there: its prompt (from the take's: Cover and
 * Vary start from its style), and the float and enum settings of the node the take goes into (Keep the song, Add
 * and Where, Change). The run goes beside the take in the strip; the form below is not touched. */
export function OfferPop({
  id,
  offer,
  workflow,
  take,
  estimate,
  onRun,
}: {
  id: string;
  offer: S<"TakeOffer">;
  workflow: S<"Workflow">;
  take: Take;
  estimate?: string | null;
  onRun: (w: S<"Workflow">, inputs: S<"RunInputs">) => Promise<unknown>;
}) {
  const all = fieldsOf(workflow);
  const node = offer.param.split(".")[0];
  const text = all.find((f) => f.param.type === "text" && f.param.name === "text");
  const [raws, setRaws] = useState<Record<string, Raw>>(() => ({
    ...rawsFrom(all, offer.inputs),
    ...(text && offer.action !== "Extend" && take.made_with?.prompt
      ? { [text.key]: take.made_with.prompt }
      : {}),
  }));
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const set = (k: string, v: Raw) => setRaws((r) => ({ ...r, [k]: v }));
  const knobs = all.filter(
    (f) =>
      f.node === node &&
      (f.param.type === "float" || (f.param.type === "enum" && (f.param.enum?.length ?? 0) > 1)),
  );
  const seed = take.made_with?.seed;
  const action = offer.action ?? offer.title;
  async function go() {
    const built = buildInputs(visible(all, raws), raws);
    if (Object.keys(built.errors).length)
      return setError(
        Object.entries(built.errors)
          .map(([k, v]) => `${k}: ${v}`)
          .join("; "),
      );
    setBusy(true);
    setError("");
    try {
      await onRun(workflow, built.inputs);
      document.getElementById(id)?.hidePopover();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <div
      id={id}
      popover="auto"
      role="dialog"
      aria-label={`${action} · ${seed != null ? `seed ${seed}` : take.title}`}
      className="pop w-80"
      style={
        {
          positionAnchor: `--${id}`,
          positionArea: "top span-right",
          marginBottom: 6,
          maxWidth: "calc(100vw - 16px)",
        } as React.CSSProperties
      }
    >
      <div className="flex flex-col gap-3">
        <span className="heading">
          {action} · {seed != null ? `seed ${seed}` : take.title}
        </span>
        {text && action !== "Extend" && (
          // a fixed 3 lines that scroll, with Expand, as the prompt (owner, 2026-11-27)
          <TextArea
            expand="Style"
            aria-label="Style"
            rows={3}
            className="in h-auto resize-none py-2 leading-[19px]"
            value={String(raws[text.key] ?? "")}
            onChange={(e) => set(text.key, e.target.value)}
          />
        )}
        {knobs.map((f) => {
          const raw = raws[f.key] ?? toRaw(f.initial, f.param);
          const label = f.param.label ?? f.param.name;
          return f.param.type === "enum" ? (
            <div key={f.key} role="group" aria-label={label} className="flex flex-col gap-1.5">
              <span className="text-[12px] text-t2">{label}</span>
              <div className="flex flex-wrap gap-1.5">
                {f.param.enum!.map((o) => (
                  <button
                    key={o}
                    type="button"
                    className="opt bevel min-w-16 px-3 text-[13px]"
                    aria-pressed={raw === o}
                    onClick={() => set(f.key, o)}
                  >
                    {optionText(f.param, o, f.kind)}
                  </button>
                ))}
              </div>
            </div>
          ) : (
            <label key={f.key} className="flex flex-col gap-1.5 text-[13px]">
              <span className="flex justify-between gap-2">
                {label}
                <span className="mono text-[12px] text-t2">{chipText(f, raw)}</span>
              </span>
              <Range
                min={f.param.min}
                max={f.param.max}
                step={f.param.step ?? 0.05}
                value={Number(raw) || f.param.min || 0}
                onChange={(e) => set(f.key, e.target.value)}
                className="w-full accent-a2"
              />
            </label>
          );
        })}
        {error && <span className="text-[12px] text-bad">{error}</span>}
        <button
          type="button"
          disabled={busy}
          onClick={() => void go()}
          className="key flex h-10 items-center justify-center gap-1.5 self-end bg-a2 px-4 text-[13px] text-[var(--on2)]"
        >
          {busy ? "Sending…" : action}
          {estimate && !busy && <span className="mono text-[11px] font-medium">{estimate}</span>}
        </button>
      </div>
    </div>
  );
}

/** Save with the video (API 1.79 `POST …/takes/{id}/mux`): the new video take of `videoId`'s picture with the sound
 * take `audioId`, by a media job polled until it ends (as frameTake). */
export async function muxTake(audioId: string, videoId: string): Promise<string> {
  let { job } = await ok(
    api.POST("/v1/studio/takes/{take_id}/mux", {
      params: { path: { take_id: audioId } },
      body: { video_take_id: videoId },
    }),
  );
  while (job.status === "queued" || job.status === "running") {
    await new Promise((res) => setTimeout(res, 1000));
    job = await ok(
      api.GET("/v1/studio/media-jobs/{job_id}", { params: { path: { job_id: job.id } } }),
    );
  }
  if (job.status !== "succeeded" || !job.take_id)
    throw new Error(job.failure?.detail ?? `Saving with the video ${job.status}.`);
  return job.take_id;
}
