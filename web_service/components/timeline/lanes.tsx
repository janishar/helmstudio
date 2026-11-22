"use client";
// The tracks (docs/mock/Timeline.dc.html, the lanes panel) and the tools on them (plan 23 §27.2, §27.5; board
// docs/mock/TimelineTools.dc.html): the tool row above, the ruler with its markers, V1's clips, A1 (V1's own sound),
// the extra audio tracks and the playhead. Select: drag a clip to reorder (V1, by midpoint) or move it (audio: another
// audio lane), an edge (7 px) to ripple trim. Trim: an edge ripple trims, the cut between two V1 clips rolls, the body
// slips (⌥: slides). Blade: a dashed cut line follows the pointer; a click cuts (⇧: every track). Range: drag marks in
// and out. Overwrite: drag a clip over what is there. Drags snap (N; ⌘ inverts) to cuts, the playhead, the range and
// markers within 6 px, clamp at their bounds, and draw the draft; the op goes to the editor on release. Hover scrub
// draws a line under the pointer. Zoom (§27.4): Ctrl / ⌘ / ⌥ + wheel and a trackpad pinch zoom about the pointer;
// every other zoom (keys, the row's buttons) keeps the playhead still when it is on screen, else the centre; Fit
// starts at 0. Only what meets the virtual window (the screen and one each side, §27.8) is drawn: clips, the ruler's
// canvas, filmstrips and waveforms. The playhead brought off screen (playing, ↑ ↓, End) pages the view to it.
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { Ellipsis, Link2, X } from "lucide-react";
import type { S } from "@/lib/api";
import { safeUrl } from "@/lib/format";
import { actionShot } from "@/components/timeline/actions";
import { MagnetIcon, ToolIcon } from "@/components/timeline/tools";
import { Filmstrip, RulerCanvas, Waveform } from "@/components/timeline/detail";
import {
  Grid,
  cuts,
  dropIndex,
  kindOf,
  lenOf,
  shortTime,
  timecode,
  type Clip,
  type Norm,
  type Op,
  type Takes,
  shotBusy,
} from "@/lib/timeline";
import {
  clampFrames,
  cutText,
  fits,
  virtualWindow,
  wheelZoom,
  zoomAbout,
  signed,
  snapTo,
  trimZone,
  withIds,
  type SnapPt,
  type Tool,
} from "@/lib/timeline-tools";

type TTake = S<"TimelineTake">;

const HEAD = 72; // the lane name column (px)
const EDGE_PX = 7;
const DRAG_PX = 3;
const RULER = 22; // the board's ruler
const TOP = RULER + 2; // the lines' top: below the ruler
/** A clip narrower than this is a colour block, no text (the board's "Zoomed out to fit"). */
const TEXT_PX = 70;

/** Marker colours (TimelineMarkerColor); none: the accent. */
export const MARKER_COLORS: Record<string, string> = {
  red: "#ff6b6b",
  orange: "#ff9f43",
  yellow: "#f2c14e",
  green: "#8bd17c",
  blue: "#6cb6ff",
  purple: "#c9a7ff",
};

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
  if (c.shot && audio && !c.take_id) {
    // a New line's placeholder (API 1.32, docs/mock/TimelineAudio.dc.html): "Speaking line", "New line failed"
    const st = c.shot.status ?? "queued";
    if (st === "failed")
      return { title: "New line failed", line: c.shot.error ?? "the run failed" };
    return { title: st === "running" ? "Speaking line" : "New line", line: progress ?? st };
  }
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
      // an audio take names its length (docs/mock/TimelineAudio.dc.html: "Dialogue · harbour scene · 0:09")
      title:
        t?.kind === "audio"
          ? `${t.title} · ${clock(g.s(lenOf(c, "audio", g, true)))}`
          : (t?.title ?? "Clip"),
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

/** "0:09": whole seconds of a clip's length. */
const clock = (s: number) => {
  const t = Math.round(s);
  return `${Math.floor(t / 60)}:${String(t % 60).padStart(2, "0")}`;
};

/** An audio take's cues on its clip (API 1.32): each line's tick at its start in the clip, a trimmed-out one not
 * drawn (sequence time = at + cue − in). */
export function cueTicks(
  c: Clip,
  t: TTake | undefined,
): { line: number; x: number; voice: string }[] {
  const cues = t?.audio?.cues ?? [];
  const a = c.in ?? 0;
  const b = c.out ?? 0;
  return cues
    .filter((q) => q.start_s >= a && q.start_s < b)
    .map((q) => ({
      line: q.line,
      x: q.start_s - a,
      voice: q.voice_id,
    }));
}

type Mode = "in" | "out" | "move" | "roll" | "slip" | "slide" | "overwrite" | "range";
type Drag = {
  id: string;
  track: number;
  mode: Mode;
  x0: number;
  started: boolean;
  /** the dragged clip's (roll: the incoming clip's) start and end */
  span: [number, number];
  /** the document at pointer down: every draft is computed from it, not from the draft drawn */
  doc: Norm;
  /** overwrite: the ids its op makes, fixed for the drag */
  ids?: string[];
  /** range: where it started (s) */
  t0?: number;
};
/** What a drag shows: its op so far, the snap point it holds on, whether it stopped at a bound. */
type Shown = { d: Drag; op: Op | null; to: SnapPt | null; clamped: boolean };

const CURSOR: Record<Tool, string> = {
  select: "default",
  trim: "ew-resize",
  blade: "crosshair",
  range: "text",
  overwrite: "move",
};
const ZONE_CURSOR = {
  roll: "col-resize",
  roll_next: "col-resize",
  in: "w-resize",
  out: "e-resize",
  body: "grab",
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
  onMenu,
  nameOf,
  tool = "select",
  snap = true,
  scrub = false,
  onHover,
  onRange,
  marker,
  onMarker,
  toolbar,
  onView,
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
  /** a click on a clip (`add`: Shift / ⌘; `one`: ⌥, only this clip, not its linked ones) */
  onSelect: (id: string, add: boolean, one?: boolean) => void;
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
  /** an audio clip's ⋯ (API 1.32): opens its menu (`menu-<id>` anchors it) */
  onMenu?: (id: string) => void;
  /** a cue's speaker ("Mara"), from its voice */
  nameOf?: (voiceId: string) => string;
  /** the active tool (plan 23) */
  tool?: Tool;
  /** snapping on (N) */
  snap?: boolean;
  /** hover scrub on (⇧S): a line under the pointer, and `onHover` with its frame */
  scrub?: boolean;
  /** the frame under the pointer (null: it left), for hover scrub and M */
  onHover?: (frame: number | null) => void;
  /** the Range tool's drag: the new range */
  onRange?: (r: [number, number] | null) => void;
  /** the selected marker's id */
  marker?: string | null;
  /** a marker's flag clicked */
  onMarker?: (id: string) => void;
  /** the tool row, above the scrolling lanes */
  toolbar?: React.ReactNode;
  /** the visible track width (px), for the zoom field and Fit */
  onView?: (width: number) => void;
}) {
  const g = new Grid(doc.target);
  const drag = useRef<Drag | null>(null);
  const [shift, setShift] = useState<{ id: string; dx: number; track?: number } | null>(null);
  const [shown, setShown] = useState<Shown | null>(null);
  const [hover, setHover] = useState<{ t: number; all: boolean; to: SnapPt | null } | null>(null);
  const suppress = useRef(false);
  const content = useRef<HTMLDivElement>(null);
  const scroller = useRef<HTMLDivElement>(null);
  const end = g.s(doc.frames);
  const width = Math.max(end * pps + 160, 600);
  const v1 = doc.tracks[0];
  const markers = doc.markers ?? [];
  // the visible track width and the virtual window (track px; not laid out yet: everything)
  const [view, setView] = useState(0);
  const [win, setWin] = useState<[number, number]>([-Infinity, Infinity]);
  const showing = (x0: number, w: number) => x0 + w >= win[0] && x0 <= win[1];

  useEffect(() => {
    const el = scroller.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(() => {
      const w = Math.max(1, el.clientWidth - HEAD);
      setView(w);
      setWin(virtualWindow(el.scrollLeft, w));
      onView?.(w);
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, [onView]);
  const onScroll = () => {
    const el = scroller.current;
    if (!el || !view) return;
    const w = virtualWindow(el.scrollLeft, view);
    if (w[0] !== win[0] || w[1] !== win[1]) setWin(w);
  };

  // zoom about a point: the pointer's x for a wheel or pinch, else the playhead on screen, else the centre
  const anchor = useRef<number | null>(null);
  const seen = useRef(pps);
  const now = g.s(frame);
  useLayoutEffect(() => {
    const el = scroller.current;
    const was = seen.current;
    seen.current = pps;
    if (!el || was === pps) return;
    const phWas = HEAD + now * was - el.scrollLeft;
    const x = anchor.current ?? (phWas >= HEAD && phWas <= el.clientWidth ? phWas : el.clientWidth / 2);
    anchor.current = null;
    // Fit shows the whole sequence from 0
    el.scrollLeft = view && pps * Math.max(end, 5) <= view + 1 ? 0 : zoomAbout(el.scrollLeft, x, was, pps, HEAD);
    if (view) setWin(virtualWindow(el.scrollLeft, view));
  }, [pps, now, view, end]);
  // the playhead moved off screen (playing, ↑ ↓, Home / End): page the view so it sits a tenth in
  useLayoutEffect(() => {
    const el = scroller.current;
    if (!el || !view) return;
    const x = HEAD + now * pps - el.scrollLeft;
    if (x >= HEAD && x <= el.clientWidth - 2) return;
    el.scrollLeft = Math.max(0, now * pps - view / 10);
    setWin(virtualWindow(el.scrollLeft, view));
    // only a move of the playhead, not a zoom, pages
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [now]);
  // Ctrl / ⌘ / ⌥ + wheel and pinch (a wheel with ctrlKey; Safari's gesture events) zoom at the pointer; React's
  // wheel listener is passive, so this one is the element's own
  const zoomRef = useRef({ pps, onZoom });
  useEffect(() => {
    zoomRef.current = { pps, onZoom };
  });
  useEffect(() => {
    const el = scroller.current;
    if (!el) return;
    const at = (clientX: number) => (anchor.current = clientX - el.getBoundingClientRect().left);
    const wheel = (e: WheelEvent) => {
      if (!e.ctrlKey && !e.metaKey && !e.altKey) return;
      e.preventDefault();
      at(e.clientX);
      const z = zoomRef.current;
      z.onZoom(z.pps * wheelZoom(e.deltaY, e.deltaMode));
    };
    let scale = 1;
    const gesture = (e: Event) => {
      const ge = e as Event & { scale: number; clientX: number };
      e.preventDefault();
      if (e.type === "gesturestart") scale = 1;
      else {
        at(ge.clientX);
        const z = zoomRef.current;
        z.onZoom((z.pps * ge.scale) / scale);
      }
      scale = ge.scale;
    };
    el.addEventListener("wheel", wheel, { passive: false });
    el.addEventListener("gesturestart", gesture);
    el.addEventListener("gesturechange", gesture);
    return () => {
      el.removeEventListener("wheel", wheel);
      el.removeEventListener("gesturestart", gesture);
      el.removeEventListener("gesturechange", gesture);
    };
  }, []);

  /** The snap points besides the cuts: the playhead, the range's ends and the markers. */
  const extra: SnapPt[] = [
    { t: g.s(frame), kind: "playhead" },
    ...(range ? range.map((t) => ({ t, kind: "range" as const })) : []),
    ...markers.map((m) => ({ t: m.at, kind: "marker" as const })),
  ];
  const points = (base: Norm, exclude?: string): SnapPt[] => [
    ...cuts(base, facts, exclude).map((t) => ({ t, kind: "cut" as const })),
    ...extra,
  ];
  /** Sequence seconds under a client x. */
  const timeAt = (clientX: number) => {
    const r = content.current?.getBoundingClientRect();
    return Math.max(0, (clientX - (r?.left ?? 0) - HEAD) / pps);
  };
  const snapOn = (e: { metaKey: boolean; ctrlKey: boolean }) => snap !== (e.metaKey || e.ctrlKey);

  /** The op a drag makes so far (null: nothing changes), the snap point and whether it stopped at a bound. */
  function opOf(
    d: Drag,
    e: { clientX: number; clientY: number; metaKey: boolean; ctrlKey: boolean },
  ): Omit<Shown, "d"> {
    const base = d.doc;
    const dt = (e.clientX - d.x0) / pps;
    const on = snapOn(e);
    const pts = points(base, d.id);
    // the edge's own place is no target: a small drag would snap back to it
    const sn = (t: number, from: number) =>
      snapTo(
        t,
        pts.filter((p) => p.t !== from),
        pps,
        g,
        on,
      );
    const none = { op: null, to: null, clamped: false };
    const bt = base.tracks[d.track];
    const c = bt.clips.find((x) => x.id === d.id)!;
    const clamp = (df: number, mk: (k: number) => Op) => {
      const r = clampFrames(df, (k) => fits(base, mk(k), facts));
      return { op: r.df ? mk(r.df) : null, clamped: r.clamped };
    };
    if (d.mode === "move") {
      if (d.track === 0) {
        const index = dropIndex(base, d.id, (d.span[0] + d.span[1]) / 2 + dt, facts);
        return index === bt.clips.findIndex((x) => x.id === d.id)
          ? none
          : { op: { op: "move", clip: d.id, index }, to: null, clamped: false };
      }
      const s = sn(c.at! + dt, c.at!);
      const lane = laneAt(e.clientX, e.clientY);
      const name = lane > 0 ? base.tracks[lane]?.name : undefined;
      return {
        op: { op: "move", clip: d.id, at: Math.max(0, s.t), ...(name && { track: name }) },
        to: s.to,
        clamped: false,
      };
    }
    if (d.mode === "in" || d.mode === "out") {
      const edgeT = d.mode === "in" ? d.span[0] : d.span[1];
      const s = sn(edgeT + dt, edgeT);
      const edge = d.mode;
      return {
        ...clamp(g.f(s.t - edgeT), (k) => ({ op: "trim", clip: d.id, edge, delta: g.s(k) })),
        to: s.to,
      };
    }
    if (d.mode === "roll" || d.mode === "slide") {
      const s = sn(d.span[0] + dt, d.span[0]);
      const op = d.mode;
      return {
        ...clamp(g.f(s.t - d.span[0]), (k) => ({ op, clip: d.id, delta: g.s(k) })),
        to: s.to,
      };
    }
    if (d.mode === "slip")
      return {
        ...clamp(g.f(dt), (k) => ({ op: "slip", clip: d.id, delta: g.s(k) })),
        to: null,
      };
    if (d.mode === "overwrite") {
      const s = sn(d.span[0] + dt, d.span[0]);
      const lane = d.track > 0 ? laneAt(e.clientX, e.clientY) : 0;
      const name = lane > 0 ? base.tracks[lane]?.name : undefined;
      if (g.f(s.t) === g.f(d.span[0]) && (!name || name === bt.name)) return none;
      return {
        op: {
          op: "overwrite",
          clip: d.id,
          at: Math.max(0, s.t),
          ...(name && { track: name }),
          ids: d.ids,
        },
        to: s.to,
        clamped: false,
      };
    }
    return none;
  }
  const laneAt = (x: number, y: number) => {
    const el = document
      .elementsFromPoint?.(x, y)
      .find((e) => e instanceof HTMLElement && e.dataset.lane !== undefined);
    return el ? Number((el as HTMLElement).dataset.lane) : -1;
  };

  function down(e: React.PointerEvent<HTMLButtonElement>, c: Clip, ti: number) {
    // Range drags on the lanes themselves; Blade cuts on the click
    if (e.button !== 0 || shotBusy(c) || tool === "range" || tool === "blade") return;
    e.stopPropagation();
    const r = e.currentTarget.getBoundingClientRect();
    const x = e.clientX - r.left;
    const clips = doc.tracks[ti].clips;
    const i = clips.findIndex((k) => k.id === c.id);
    let id = c.id;
    let mode: Mode;
    if (tool === "select") mode = x < EDGE_PX ? "in" : r.width - x < EDGE_PX ? "out" : "move";
    else if (tool === "overwrite") mode = "overwrite";
    else {
      const z = trimZone(x, r.width, ti === 0 && i > 0, ti === 0 && i < clips.length - 1);
      if (z === "roll_next") id = clips[i + 1].id;
      mode =
        z === "roll" || z === "roll_next"
          ? "roll"
          : z === "body"
            ? e.altKey && ti === 0
              ? "slide"
              : "slip"
            : z;
    }
    const k = clips.find((x) => x.id === id)!;
    const a = k.at!;
    drag.current = {
      id,
      track: ti,
      mode,
      x0: e.clientX,
      started: false,
      span: [a, g.s(g.f(a) + lenOf(k, kindOf(k, facts), g, ti > 0))],
      doc,
      ids:
        mode === "overwrite"
          ? (withIds({ op: "overwrite", clip: id, at: 0 }, doc) as { ids: string[] }).ids
          : undefined,
    };
    e.currentTarget.setPointerCapture(e.pointerId);
  }
  /** The Range tool: a drag anywhere on the lanes or the ruler marks in and out. */
  function rangeDown(e: React.PointerEvent<HTMLDivElement>) {
    if (tool !== "range" || e.button !== 0) return;
    const r0 = content.current?.getBoundingClientRect();
    if (e.clientX - (r0?.left ?? 0) < HEAD) return; // the track names and their buttons
    const t = snapTo(timeAt(e.clientX), points(doc), pps, g, snapOn(e)).t;
    drag.current = {
      id: "",
      track: 0,
      mode: "range",
      x0: e.clientX,
      started: false,
      span: [t, t],
      doc,
      t0: t,
    };
    e.currentTarget.setPointerCapture?.(e.pointerId);
  }
  function move(e: React.PointerEvent) {
    const d = drag.current;
    if (!d) {
      // the pointer over the lanes: the blade line and hover scrub
      if (tool !== "blade" && !scrub) return;
      const s = snapTo(timeAt(e.clientX), points(doc), pps, g, tool === "blade" && snapOn(e));
      const t = Math.min(s.t, end);
      if (hover?.t !== t || hover.all !== e.shiftKey || hover.to !== s.to)
        setHover({ t, all: e.shiftKey, to: tool === "blade" ? s.to : null });
      if (scrub) onHover?.(g.f(t));
      return;
    }
    const dx = e.clientX - d.x0;
    if (!d.started && Math.abs(dx) < DRAG_PX) return;
    d.started = true;
    if (d.mode === "range") {
      const t = snapTo(timeAt(e.clientX), points(d.doc), pps, g, snapOn(e)).t;
      const r: [number, number] = [Math.min(d.t0!, t), Math.min(Math.max(d.t0!, t), end)];
      onRange?.(g.f(r[1]) > g.f(r[0]) ? r : null);
      return;
    }
    const r = opOf(d, e);
    setShown({ d, ...r });
    if (d.mode === "move" && tool === "select") setShift({ id: d.id, dx });
    else onPreview(r.op);
  }
  function up(e: React.PointerEvent) {
    const d = drag.current;
    drag.current = null;
    if (!d?.started) return;
    suppress.current = true;
    setShift(null);
    setShown(null);
    onPreview(null);
    if (d.mode === "range") return;
    const { op } = opOf(d, e);
    if (op) onCommit(op);
  }
  function cancel() {
    drag.current = null;
    setShift(null);
    setShown(null);
    onPreview(null);
  }

  /** Blade: cut the clip under the pointer on its track (⇧: every track) at the cut line's frame. */
  function blade(e: React.MouseEvent, trackName: string) {
    const at = Math.min(snapTo(timeAt(e.clientX), points(doc), pps, g, snapOn(e)).t, end);
    onCommit(e.shiftKey ? { op: "split_all", at } : { op: "split", track: trackName, at, id: "" });
  }

  const seekAt = (e: React.PointerEvent<HTMLElement>) => onSeek(g.f(timeAt(e.clientX)));

  const clipButton = (c: Clip, ti: number, height: number) => {
    const audio = ti > 0;
    const x0 = c.at! * pps;
    const wAll = g.s(lenOf(c, kindOf(c, facts), g, audio)) * pps;
    // virtualised (§27.8): off the window and not selected, not drawn
    if (!showing(x0, Math.max(wAll, 6)) && !sel.includes(c.id)) return null;
    const { title, line } = clipText(c, audio, takes, facts, g, progress[c.id]);
    const t = c.take_id ? takes[c.take_id] : undefined;
    const kind = kindOf(c, facts);
    const w = wAll;
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
    // a V1 take's filmstrip at the clip's foot (§27.4), the poster its stand-in while frames load
    const film = !audio && !!t && !c.shot && !c.offline && (kind === "video" || kind === "image");
    const tiny = w < TEXT_PX;
    // an audio take's clip: a waveform strip, its dialogue's cue ticks and the ⋯ menu (docs/mock/TimelineAudio.dc.html)
    const wave = audio && t?.kind === "audio" && !c.offline;
    const ticks = wave && height > 40 && !tiny ? cueTicks(c, t) : [];
    const menu = audio && !!c.take_id && !c.offline && !!onMenu && !tiny;
    const failedLine = audio && !c.take_id && c.shot?.status === "failed";
    const linked = audio && c.link !== undefined;
    const button = (
      <button
        key={c.id}
        type="button"
        data-clip={c.id}
        aria-pressed={on}
        aria-label={`${title}, ${line}${linked ? ", linked" : ""}${on ? ", selected" : ""}`}
        onPointerDown={(e) => down(e, c, ti)}
        onPointerMove={(e) => {
          // Trim's grab zone shows in the cursor
          if (tool !== "trim" || drag.current) return;
          const r = e.currentTarget.getBoundingClientRect();
          const clips = doc.tracks[ti].clips;
          const i = clips.findIndex((k) => k.id === c.id);
          const z = trimZone(
            e.clientX - r.left,
            r.width,
            ti === 0 && i > 0,
            ti === 0 && i < clips.length - 1,
          );
          e.currentTarget.style.cursor = ZONE_CURSOR[z];
        }}
        onClick={(e) => {
          if (suppress.current) {
            suppress.current = false;
            return;
          }
          if (tool === "blade") return blade(e, ti === 0 ? "V1" : doc.tracks[ti].name!);
          onSelect(c.id, e.shiftKey || e.metaKey || e.ctrlKey, e.altKey);
        }}
        className={`tl-clip absolute top-0 ${film ? "fs" : ""} ${wave ? "wv" : ""} ${failedLine ? "border border-bad bg-s2" : audio && c.shot && !c.take_id ? "border border-[#86cdea] bg-s2" : look} ${on ? "border-2! border-t1!" : ""} ${shift?.id === c.id ? "z-10 opacity-80" : ""}`}
        style={{
          left: c.at! * pps,
          width: Math.max(w, 6),
          height,
          transform: shift?.id === c.id ? `translateX(${shift.dx}px)` : undefined,
          cursor: shotBusy(c) ? "default" : tool === "trim" ? undefined : CURSOR[tool],
        }}
      >
        {film && (
          <Filmstrip
            id={c.id}
            take={c.take_id!}
            url={kind === "video" ? safeUrl(t.url) : undefined}
            posterUrl={safeUrl(t.thumb_url) ?? (kind === "image" ? safeUrl(t.url) : undefined)}
            still={kind === "image"}
            x0={x0}
            w={Math.max(w, 6)}
            src={[c.in ?? 0, g.s(lenOf(c, kind, g))]}
            pps={pps}
            fps={doc.target.fps}
            win={win}
          />
        )}
        {wave && (
          <Waveform
            take={c.take_id!}
            url={safeUrl(t.url)}
            duration={t.audio?.duration_s ?? 0}
            x0={x0}
            w={Math.max(w, 6)}
            h={height}
            src={c.in ?? 0}
            pps={pps}
            win={win}
            fallback={
              <span
                aria-hidden
                className="pointer-events-none absolute inset-x-0 top-[21px] block h-4 opacity-70"
                style={{
                  background:
                    "repeating-linear-gradient(90deg, transparent 0 2px, color-mix(in srgb, #86cdea 70%, transparent) 2px 3px)",
                  maskImage: "linear-gradient(180deg, transparent 20%, #000 20% 80%, transparent 80%)",
                }}
              />
            }
          />
        )}
        {height > 40 && !tiny && (
          <b className={`flex items-center gap-1 ${menu ? "pr-6" : ""}`}>
            {linked && <Link2 size={11} aria-hidden className="shrink-0" />}
            {title}
          </b>
        )}
        {ticks.map((k) => (
          <span key={k.line} aria-hidden>
            <span
              className="absolute top-5 bottom-[3px] block w-px bg-t1"
              style={{ left: k.x * pps }}
            />
            <small
              className="absolute bottom-[3px] rounded bg-s1 px-1"
              style={{ left: k.x * pps + 3 }}
            >
              {k.line} {nameOf?.(k.voice) ?? ""}
            </small>
          </span>
        ))}
        {tiny ? null : pct ? (
          <span className="flex items-center gap-1.5">
            <span className="block h-1 flex-1 rounded-[2px] bg-s5">
              <span
                className="block h-1 rounded-[2px] bg-a2"
                style={{ width: `${(100 * Number(pct[1])) / Number(pct[2])}%` }}
              />
            </span>
            <small>{line}</small>
          </span>
        ) : ticks.length ? (
          <small className="absolute right-2 bottom-[3px]">{line}</small>
        ) : (
          <small className={wave ? "flex justify-between gap-2" : ""}>
            {wave && t?.workflow_title && <span>{t.workflow_title}</span>}
            {height > 40 ? line : `${title === "Clip" ? "" : title} · ${line}`}
          </small>
        )}
        {!shotBusy(c) && tool === "select" && (
          <>
            <span aria-hidden className="tl-edge left-0" />
            <span aria-hidden className="tl-edge right-0" />
          </>
        )}
      </button>
    );
    if (!menu) return button;
    return (
      <span key={c.id}>
        {button}
        <button
          type="button"
          aria-label="Clip menu"
          aria-haspopup="menu"
          popoverTarget="clip-menu"
          onClick={() => onMenu(c.id)}
          className="absolute top-0.5 z-10 grid size-[22px] place-items-center rounded-lg bg-s3 text-t2"
          style={
            {
              left: c.at! * pps + Math.max(w, 6) - 26,
              anchorName: `--menu-${c.id}`,
            } as React.CSSProperties
          }
        >
          <Ellipsis size={14} aria-hidden />
        </button>
      </span>
    );
  };

  // ---- what a drag shows (the board's shared states): the ripple ghost and edge, the tool's tag, the snap line
  const xOf = (t: number) => HEAD + t * pps;
  const overlay = (() => {
    if (!shown?.op) return null;
    const { d, op, clamped } = shown;
    const [a, b] = d.span;
    const out: React.ReactNode[] = [];
    const tag = (x: number, text: string, warn = false) =>
      out.push(
        <span key="tag" className={`tl-tag ${warn ? "warn" : ""}`} style={{ left: x - 4, top: 2 }}>
          {text}
        </span>,
      );
    if (op.op === "trim") {
      const delta = op.delta;
      // V1 ripples: the clip's length changes at its end; an audio clip's edge itself moves
      const onV1 = d.track === 0;
      const grow = op.edge === "out" ? delta : -delta;
      const edgeT = onV1
        ? op.edge === "out"
          ? b + grow
          : a
        : op.edge === "out"
          ? b + delta
          : a + delta;
      const ghost: [number, number] | null = onV1
        ? grow < 0
          ? [b + grow, b]
          : null
        : op.edge === "out" && delta < 0
          ? [b + delta, b]
          : op.edge === "in" && delta > 0
            ? [a, a + delta]
            : null;
      // the ruler, V1 and A1 above
      const top = d.track === 0 ? RULER + 6 : RULER + 110 + 62 * (d.track - 1);
      if (ghost)
        out.push(
          <span
            key="ghost"
            aria-hidden
            className="tl-ghost"
            style={{ left: xOf(ghost[0]), width: (ghost[1] - ghost[0]) * pps, top }}
          />,
        );
      out.push(
        <span
          key="edge"
          aria-hidden
          className={`tl-edgebar ${clamped ? "warn" : ""}`}
          style={{ left: xOf(edgeT) - 3, top }}
        />,
      );
      tag(
        xOf(edgeT),
        onV1
          ? `Ripple ${signed(grow, g)} · later clips move ${grow < 0 ? "left" : "right"}`
          : `Trim ${signed(delta, g)}`,
        onV1,
      );
    } else if (op.op === "slip") tag(xOf(a), `Slip ${signed(op.delta, g)} · place and length kept`);
    else if (op.op === "slide") tag(xOf(a + op.delta), `Slide ${signed(op.delta, g)}`);
    else if (op.op === "roll") tag(xOf(a + op.delta), `Roll ${signed(op.delta, g)} · length kept`);
    else if (op.op === "overwrite") tag(xOf(op.at), `Overwrite at ${timecode(op.at)}`);
    if (clamped && op.op !== "trim")
      out.push(
        <span
          key="edge"
          aria-hidden
          className="tl-edgebar warn"
          style={{ left: xOf(a) - 3, top: RULER + 6 }}
        />,
      );
    return out;
  })();
  const snapLine = shown?.to;
  const kindWord = { cut: "cut", playhead: "playhead", range: "range", marker: "marker" };

  return (
    <div className="panel flex shrink-0 flex-col gap-2 rounded-[28px_28px_28px_12px] px-3.5 pt-3 pb-3.5">
      {toolbar}
      <div
        ref={scroller}
        className="overflow-x-auto"
        onScroll={onScroll}
      >
        <div
          ref={content}
          className="relative flex flex-col gap-1.5"
          style={{ width: width + HEAD, cursor: CURSOR[tool] }}
          onPointerDown={rangeDown}
          onPointerMove={move}
          onPointerUp={up}
          onPointerCancel={cancel}
          onPointerLeave={() => {
            if (drag.current) return;
            setHover(null);
            onHover?.(null);
          }}
        >
          <div
            role="slider"
            tabIndex={-1}
            aria-label="Time ruler: click to move the playhead"
            aria-valuemin={0}
            aria-valuemax={doc.frames}
            aria-valuenow={frame}
            aria-valuetext={shortTime(g.s(frame))}
            className="mono relative h-[22px] cursor-pointer text-[10px] text-t2"
            style={{ marginLeft: HEAD }}
            onPointerDown={(e) => {
              if (tool === "range") return;
              seekAt(e);
              e.currentTarget.setPointerCapture(e.pointerId);
            }}
            onPointerMove={(e) => tool !== "range" && e.buttons === 1 && seekAt(e)}
          >
            <RulerCanvas pps={pps} fps={doc.target.fps} win={win} width={width} />
            {markers.map((m) => (
              <button
                key={m.id}
                type="button"
                aria-pressed={marker === m.id}
                aria-label={`Marker${m.name ? ` ${m.name}` : ""} at ${timecode(m.at)}`}
                title={`${m.name ?? "Marker"} · ${timecode(m.at)}`}
                onPointerDown={(e) => e.stopPropagation()}
                onClick={() => {
                  onSeek(g.f(m.at));
                  onMarker?.(m.id);
                }}
                className="absolute top-0 z-10 h-[14px] w-[10px] -translate-x-1/2 p-0 aria-pressed:outline-2 aria-pressed:outline-t1"
                style={{
                  left: m.at * pps,
                  background: m.color ? MARKER_COLORS[m.color] : "var(--a1)",
                  clipPath: "polygon(0 0, 100% 0, 100% 65%, 50% 100%, 0 65%)",
                }}
              />
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
                if (!showing(c.at! * pps, Math.max(w, 6))) return null;
                const gain = (c.gain_db ?? 0) + (v1.gain_db ?? 0);
                return (
                  <button
                    key={c.id}
                    type="button"
                    tabIndex={-1}
                    aria-hidden
                    onClick={(e) =>
                      tool === "blade" ? blade(e, "V1") : onSelect(c.id, false, e.altKey)
                    }
                    className={`tl-clip wv absolute top-0 h-9 justify-center border border-line bg-s2 ${c.mute ? "opacity-50" : ""}`}
                    style={{ left: c.at! * pps, width: Math.max(w, 6), cursor: CURSOR[tool] }}
                  >
                    <Waveform
                      take={c.take_id!}
                      url={safeUrl(t.url)}
                      duration={t.video?.duration_s ?? 0}
                      x0={c.at! * pps}
                      w={Math.max(w, 6)}
                      h={36}
                      src={c.in ?? 0}
                      pps={pps}
                      win={win}
                    />
                    {w >= TEXT_PX && (
                      <small>
                        {c.number ? `Shot ${c.number} sound` : "clip audio"} ·{" "}
                        {c.mute ? "muted" : `${gain > 0 ? "+" : ""}${gain} dB`}
                      </small>
                    )}
                  </button>
                );
              })}
            </div>
          </div>

          {doc.tracks.slice(1).map((t, i) => (
            <div key={t.name} className="flex h-14 items-stretch" data-lane={i + 1}>
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
                {t.clips.map((c) => clipButton(c, i + 1, 56))}
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
              className="pointer-events-none absolute top-[22px] bottom-0 block border-x-2 border-a2"
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
          <span aria-hidden className="tl-ph" style={{ left: HEAD + g.s(frame) * pps - 1 }} />
          {overlay}
          {snapLine && (
            <>
              <span
                aria-hidden
                className="tl-snapl"
                style={{ left: xOf(snapLine.t) - 1, top: TOP, bottom: 0 }}
              />
              <span className="tl-tag ok" style={{ left: xOf(snapLine.t) - 4, top: 2 }}>
                <MagnetIcon size={12} /> Snap · {kindWord[snapLine.kind]} {timecode(snapLine.t)}
              </span>
            </>
          )}
          {hover && !shown && !shift && tool === "blade" && (
            <>
              <span
                aria-hidden
                className="tl-bl"
                style={{ left: xOf(hover.t) - 1, top: TOP, bottom: 0 }}
              />
              <span aria-hidden className="tl-cur" style={{ left: xOf(hover.t) + 5, top: TOP + 18 }}>
                <ToolIcon tool="blade" size={14} />
              </span>
              <span className="tl-tag" style={{ left: xOf(hover.t) - 4, top: 2 }}>
                Cut {cutText(hover.t, pps, g, hover.all)}
              </span>
            </>
          )}
          {hover && !shown && !shift && tool !== "blade" && scrub && (
            <span
              aria-hidden
              className="pointer-events-none absolute top-0 bottom-0 z-10 block w-px bg-t2"
              style={{ left: xOf(hover.t) }}
            >
              <span className="mono absolute top-0 left-1 rounded bg-s3 px-1 text-[10px] whitespace-nowrap text-t1">
                {timecode(hover.t)}
              </span>
            </span>
          )}
        </div>
      </div>
    </div>
  );
}
