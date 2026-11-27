"use client";
// Magic's lanes (MagicBoard frames 1, 4, 5): the Assets row off the ruler, the ruler and playhead, each lane with its
// blocks (a node with a preview and a status), the joins with their transition chips, tie marks, effect badges.
// Drag a block to move it (snapping to edges, the playhead and the ruler; Alt is Overwrite), its right edge to trim;
// the board draws the op's result while dragging, with dashed outlines where things were.
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { Pin } from "lucide-react";
import { openMenu } from "@/components/context-menu";
import { JOIN_ICON, KIND_NAME, Wave } from "@/components/magic/icons";
import {
  assetMenu,
  blockMenu,
  effectMenu,
  emptyMenu,
  joinMenu,
  laneMenu,
  tieMenu,
  type Act,
} from "@/components/magic/menus";
import {
  apply,
  clock,
  type Role,
  end,
  inLane,
  joins,
  secs,
  shown,
  snap,
  snapGroup,
  statusOf,
  usedIn,
  type Block,
  type Doc,
  type Lane,
  type Op,
  type Ref,
  type SnapKind,
  type Takes,
} from "@/lib/magic";
import { wheelZoom, zoomAbout } from "@/lib/timeline-tools";

const LH_PX = 120;
const LH_PHONE = 64;
/** Lane names that fit a phone's narrow head. */
const PHONE_NAME: Partial<Record<Role, string>> = { storyboard: "Board", voice: "Lines" };
const DRAG_PX = 3;
const SNAP_PX = 8;
/** A lane's height by what it holds (the board's). */
const HEIGHT: Record<Lane["role"], number> = {
  storyboard: 104,
  shots: 118,
  voice: 84,
  music: 70,
  sound: 84,
  free: 84,
};

type Ptr = Pick<
  PointerEvent,
  "clientX" | "clientY" | "altKey" | "shiftKey" | "metaKey" | "ctrlKey"
>;
type Drag = {
  kind: "move" | "trim";
  id: string;
  ids: string[];
  x0: number;
  started: boolean;
  alt: boolean;
};
export type Preview = {
  op: Op;
  snapped: { t: number; kind: SnapKind } | null;
  /** where the tip goes, in the lanes' own px */
  x: number;
  y: number;
};

/** "@mara", "@Shot 2 · last frame". */
export function refText(d: Doc, r: Ref): string {
  if ("asset" in r) return `@${d.assets.find((a) => a.id === r.asset)?.handle ?? "gone"}`;
  const b = d.blocks.find((x) => x.id === r.block);
  const name = b?.name.split(" · ")[0] ?? "gone";
  return `@${name} · ${{ first: "first frame", last: "last frame", take: "take" }[r.part]}`;
}

const STATUS = {
  draft: ["dr", "Draft"],
  done: ["done", "Done"],
  old: ["old", "Out of date"],
  failed: ["fail", "Failed"],
} as const;

/** A block's status chip: Done, 62%, Out of date, Draft, Failed. */
export function StatusChip({ d, b, takes }: { d: Doc; b: Block; takes: Takes }) {
  const st = statusOf(d, b, takes);
  if (st.s === "running") return <span className="mst run">{Math.round(st.progress)}%</span>;
  const [cls, text] = STATUS[st.s];
  return <span className={`mst ${cls}`}>{text}</span>;
}

export function Lanes({
  doc,
  takes,
  act,
  pps,
  frame,
  sel,
  snapOn,
  phone,
  renaming,
  usedAsset,
  onSelect,
  onCommit,
  onSeek,
  onZoom,
  onView,
  onRename,
  onLaneClick,
}: {
  doc: Doc;
  takes: Takes;
  act: Act;
  /** px per second */
  pps: number;
  frame: number;
  sel: string[];
  snapOn: boolean;
  phone: boolean;
  renaming: string | null;
  usedAsset: string | null;
  onSelect: (id: string | null, add: boolean) => void;
  onCommit: (op: Op) => void;
  onSeek: (f: number) => void;
  onZoom: (pps: number) => void;
  onView: (w: number) => void;
  onRename: (lane: string, name: string | null) => void;
  onLaneClick: (lane: string, at: number) => void;
}) {
  const fps = doc.target.fps;
  const pf = pps / fps; // px per frame
  const head = phone ? LH_PHONE : LH_PX;
  const scroller = useRef<HTMLDivElement>(null);
  const drag = useRef<Drag | null>(null);
  const [pv, setPv] = useState<Preview | null>(null);
  const [view, setView] = useState(0);

  // the board drawn while dragging: the op applied (a refused op keeps the last one that worked)
  const draft = pv ? apply(doc, pv.op) : null;
  const shownDoc = draft && "doc" in draft ? draft.doc : doc;
  const moved = new Set(pv ? ("ids" in pv.op ? pv.op.ids : "id" in pv.op ? [pv.op.id] : []) : []);
  const lastFrame = Math.max(0, ...shownDoc.blocks.map(end), ...doc.blocks.map(end));
  const trackW = Math.max(view - head, (lastFrame + 8 * fps) * pf);

  // the visible width, for Fit and the span in view
  useLayoutEffect(() => {
    const el = scroller.current;
    if (!el) return;
    const ro = new ResizeObserver(() => {
      setView(el.clientWidth);
      onView(el.clientWidth - head);
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, [head, onView]);

  // ⌘ / Ctrl + wheel and a trackpad pinch zoom at the pointer (React's wheel listener is passive)
  const z = useRef({ pps, onZoom, anchor: null as number | null });
  useEffect(() => {
    z.current.pps = pps;
    z.current.onZoom = onZoom;
  });
  const seen = useRef(pps);
  useLayoutEffect(() => {
    const el = scroller.current;
    const was = seen.current;
    seen.current = pps;
    if (!el || was === pps) return;
    const x = z.current.anchor ?? head + frame * (was / fps) - el.scrollLeft;
    z.current.anchor = null;
    el.scrollLeft = zoomAbout(el.scrollLeft, x, was, pps, head);
  }, [pps, head, frame, fps]);
  useEffect(() => {
    const el = scroller.current;
    if (!el) return;
    const wheel = (e: WheelEvent) => {
      if (!e.ctrlKey && !e.metaKey) return;
      e.preventDefault();
      z.current.anchor = e.clientX - el.getBoundingClientRect().left;
      z.current.onZoom(z.current.pps * wheelZoom(e.deltaY, e.deltaMode));
    };
    let scale = 1;
    const gesture = (e: Event) => {
      const g = e as Event & { scale: number; clientX: number };
      e.preventDefault();
      if (e.type !== "gesturestart") {
        z.current.anchor = g.clientX - el.getBoundingClientRect().left;
        z.current.onZoom((z.current.pps * g.scale) / scale);
      }
      scale = e.type === "gesturestart" ? 1 : g.scale;
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

  /** the frame under a client x on a lane's track */
  const frameAt = (clientX: number) => {
    const el = scroller.current!;
    const r = el.getBoundingClientRect();
    return Math.max(0, Math.round((clientX - r.left + el.scrollLeft - head) / pf));
  };

  /** the pointer in the lanes' own px (scrolled), where the drag's tip goes */
  const tipAt = (e: Ptr) => {
    const el = scroller.current!;
    const r = el.getBoundingClientRect();
    return { x: e.clientX - r.left + el.scrollLeft + 12, y: e.clientY - r.top + 14 };
  };
  // the drag follows the window, not the block: the block moves to another lane's element while it is dragged
  const down = (e: React.PointerEvent, b: Block, kind: Drag["kind"]) => {
    if (e.button !== 0) return;
    e.stopPropagation();
    const ids = kind === "move" && sel.includes(b.id) && sel.length > 1 ? sel : [b.id];
    drag.current = { kind, id: b.id, ids, x0: e.clientX, started: false, alt: e.altKey };
    const mv = (m: PointerEvent) => live.current.move(m);
    const stop = (u: PointerEvent) => {
      removeEventListener("pointermove", mv);
      removeEventListener("pointerup", stop);
      removeEventListener("pointercancel", stop);
      live.current.up(u);
    };
    addEventListener("pointermove", mv);
    addEventListener("pointerup", stop);
    addEventListener("pointercancel", stop);
  };
  const move = (e: Ptr) => {
    const g = drag.current;
    if (!g) return;
    if (!g.started && Math.abs(e.clientX - g.x0) < DRAG_PX) return;
    g.started = true;
    const tol = snapOn ? SNAP_PX / pf : 0;
    const dt = Math.round((e.clientX - g.x0) / pf);
    const b = doc.blocks.find((x) => x.id === g.id)!;
    if (g.kind === "trim") {
      const want = end(b) + dt;
      const s = snapOn ? snap(doc, want, tol, frame, new Set([b.id])) : null;
      const length = Math.max(1, (s?.t ?? want) - b.at);
      // never over the next block on its lane
      const next = inLane(doc, b.lane).find((x) => x.at >= end(b));
      const op: Op = {
        op: "trim",
        id: b.id,
        length: next ? Math.min(length, next.at - b.at) : length,
      };
      setPv({ op, snapped: s, ...tipAt(e) });
      return;
    }
    const group = doc.blocks.filter((x) => g.ids.includes(x.id));
    const lo = Math.min(...group.map((x) => x.at));
    const len = Math.max(...group.map(end)) - lo;
    const s = snapOn
      ? snapGroup(doc, lo + dt, len, tol, frame, new Set(g.ids))
      : { at: lo + dt, snapped: null };
    const hit = document
      .elementsFromPoint?.(e.clientX, e.clientY)
      .map((el) => el.closest<HTMLElement>(".lt[data-lane]"))
      .find(Boolean);
    const lane = hit?.dataset.lane;
    const one = new Set(group.map((x) => x.lane)).size === 1;
    const op: Op = {
      op: "move",
      ids: g.ids,
      dt: Math.max(-lo, s.at - lo),
      ...(lane && one && lane !== b.lane ? { lane } : {}),
      mode: e.altKey ? "overwrite" : act.mode,
    };
    setPv({ op, snapped: s.snapped, ...tipAt(e) });
  };
  const up = (e: Ptr) => {
    const g = drag.current;
    drag.current = null;
    if (!g) return;
    if (!g.started) {
      onSelect(g.id, e.shiftKey || e.metaKey || e.ctrlKey);
      return;
    }
    const p = pv;
    setPv(null);
    if (p && (p.op.op !== "move" || p.op.dt || p.op.lane)) onCommit(p.op);
  };

  const live = useRef({ move, up });
  useEffect(() => {
    live.current = { move, up };
  });

  // the ruler: click or drag to put the playhead there
  const rulerDown = (e: React.PointerEvent) => {
    (e.currentTarget as Element).setPointerCapture?.(e.pointerId);
    onSeek(frameAt(e.clientX));
  };

  // ticks: the first step of 1, 2, 5, 10, 30, 60 s with 20 px between; a label every fourth
  const step = [1, 2, 5, 10, 30, 60].find((s) => s * pps >= 20) ?? 120;
  const labels: number[] = [];
  for (let s = 0; s * pps <= trackW; s += step * 4) labels.push(s);

  const tiedSel = new Set(
    doc.links.filter((l) => sel.some((id) => l.to.includes(id))).map((l) => l.block),
  );
  const used = usedAsset ? new Set(usedIn(doc, usedAsset).map((b) => b.id)) : null;
  const visible = shownDoc.lanes.filter((l) => !l.hidden);
  const ghosts =
    pv && draft && "doc" in draft
      ? doc.blocks.filter((b) => {
          const now = shownDoc.blocks.find((x) => x.id === b.id);
          return !now || now.at !== b.at || now.lane !== b.lane;
        })
      : [];

  return (
    <div
      ref={scroller}
      className={`scroller ${used ? "dimb" : ""}`}
      style={{ "--lh": `${head}px` } as React.CSSProperties}
    >
      <div className="relative" style={{ width: head + trackW }}>
        <div
          className="rul"
          role="slider"
          aria-label="Playhead"
          aria-valuemin={0}
          aria-valuemax={lastFrame}
          aria-valuenow={frame}
          aria-valuetext={clock(frame, fps)}
          tabIndex={0}
          style={{ width: trackW, "--px": `${step * pps}px` } as React.CSSProperties}
          onPointerDown={rulerDown}
          onPointerMove={(e) => e.buttons & 1 && onSeek(frameAt(e.clientX))}
          onKeyDown={(e) => {
            const by = { ArrowLeft: -1, ArrowRight: 1 }[e.key];
            if (by) {
              e.preventDefault();
              onSeek(Math.max(0, frame + by * (e.shiftKey ? fps : 1)));
            }
          }}
        >
          {labels.map((s) => (
            <span key={s} style={{ left: s * pps }}>
              {clock(s * fps, fps, true)}
            </span>
          ))}
        </div>
        <div className="relative flex flex-col gap-1.5">
          {visible.map((lane) => {
            const bs = inLane(shownDoc, lane.id);
            const h = bs.length || lane.role !== "free" ? HEIGHT[lane.role] : 46;
            const count = countText(lane, bs, fps);
            return (
              <div key={lane.id} className="ln" role="group" aria-label={`Lane ${lane.name}`}>
                {renaming === lane.id ? (
                  <div className="lh">
                    <input
                      aria-label="Lane name"
                      defaultValue={lane.name}
                      autoFocus
                      onKeyDown={(e) => {
                        if (e.key === "Enter") onRename(lane.id, e.currentTarget.value);
                        if (e.key === "Escape") onRename(lane.id, null);
                      }}
                      onBlur={(e) => onRename(lane.id, e.currentTarget.value)}
                    />
                  </div>
                ) : (
                  <button
                    type="button"
                    className="lh"
                    aria-label={`${lane.name}, lane menu`}
                    onClick={(e) => openMenu(e, laneMenu(act, lane))}
                    onContextMenu={(e) => openMenu(e, laneMenu(act, lane))}
                    onDoubleClick={() => act.rename(lane.id)}
                  >
                    <b>{(phone && PHONE_NAME[lane.role]) || lane.name}</b>
                    <small>{count}</small>
                  </button>
                )}
                <div
                  className={`lt ${bs.length ? "" : "empty"} ${pv?.op.op === "move" && pv.op.lane === lane.id ? "drop" : ""}`}
                  data-lane={lane.id}
                  style={
                    {
                      width: trackW,
                      height: h,
                      "--px4": `${4 * pps}px`,
                    } as React.CSSProperties
                  }
                  onClick={(e) => {
                    if (e.target !== e.currentTarget) return;
                    const t = frameAt(e.clientX);
                    const s = snapOn ? snap(doc, t, SNAP_PX / pf, frame) : null;
                    onLaneClick(lane.id, s?.t ?? t);
                  }}
                  onContextMenu={(e) => {
                    if (e.target !== e.currentTarget) return;
                    openMenu(e, emptyMenu(act, lane, frameAt(e.clientX)));
                  }}
                >
                  {!bs.length && "Empty · click to add"}
                  {ghosts
                    .filter((b) => b.lane === lane.id)
                    .map((b) => (
                      <div
                        key={`g${b.id}`}
                        aria-hidden
                        className="blk ghostb"
                        style={{ left: b.at * pf + 2, width: Math.max(8, b.length * pf - 4) }}
                      />
                    ))}
                  {bs.map((b) => (
                    <BlockView
                      key={b.id}
                      d={shownDoc}
                      b={b}
                      takes={takes}
                      pf={pf}
                      act={act}
                      cls={[
                        sel.includes(b.id) && "sel",
                        moved.has(b.id) && "lift",
                        tiedSel.has(b.id) && !sel.includes(b.id) && "tied",
                        used?.has(b.id) && "use",
                      ]}
                      onDown={down}
                    />
                  ))}
                  {joins(shownDoc, lane.id)
                    // a cut is drawn on a Shots lane only (where the picture cuts); elsewhere only a set transition
                    .filter((j) => j.kind !== "cut" || lane.role === "shots")
                    .map((j) => {
                      const Icon = JOIN_ICON[j.kind];
                      const left = j.start ? j.at * pf + 22 : j.end ? j.at * pf - 22 : j.at * pf;
                      const name = `${j.kind[0].toUpperCase()}${j.kind.slice(1)}${j.kind === "cut" ? "" : ` ${secs(j.length, fps)}`}`;
                      return (
                        <button
                          key={j.key}
                          type="button"
                          className={`tj ${j.kind}`}
                          aria-label={`${name}, ${j.between ? "join" : j.start ? "lane start" : "lane end"}`}
                          title={name}
                          style={{ left }}
                          onClick={(e) => openMenu(e, joinMenu(act, j))}
                          onContextMenu={(e) => openMenu(e, joinMenu(act, j))}
                        >
                          <Icon />
                          {j.kind !== "cut" && secs(j.length, fps)}
                        </button>
                      );
                    })}
                </div>
              </div>
            );
          })}
          <span className="ph" style={{ left: head + frame * pf, top: -26 }} aria-hidden>
            <span>{clock(frame, fps)}</span>
          </span>
          {pv?.snapped && (
            <span className="snapl" style={{ left: head + pv.snapped.t * pf }} aria-hidden>
              <span>
                Snap · {pv.snapped.kind === "edge" ? "edge" : pv.snapped.kind}{" "}
                {clock(pv.snapped.t, fps)}
              </span>
            </span>
          )}
        </div>
        {pv && draft && <DragTip doc={doc} draft={draft} pv={pv} />}
      </div>
    </div>
  );
}

/** "3 frames", "3 shots · 0:24", "2 lines", "1 cue", "2 sounds", "empty". */
function countText(lane: Lane, bs: Block[], fps: number): string {
  if (!bs.length) return "empty";
  const n = bs.length;
  const word = {
    storyboard: "frame",
    shots: "shot",
    voice: "line",
    music: "cue",
    sound: "sound",
    free: "block",
  }[lane.role];
  const s = `${n} ${word}${n === 1 ? "" : "s"}`;
  return lane.role === "shots" ? `${s} · ${clock(Math.max(...bs.map(end)), fps, true)}` : s;
}

/** What the drag does: "Shot 2 → B-roll · 0:10.0 – 0:18.0 · Line 2 comes too", or why it can't. */
function DragTip({ doc, draft, pv }: { doc: Doc; draft: ReturnType<typeof apply>; pv: Preview }) {
  const fps = doc.target.fps;
  let text: string;
  if ("error" in draft) text = draft.error;
  else {
    const op = pv.op;
    const id = "ids" in op ? op.ids[0] : "id" in op ? op.id : "";
    const b = draft.doc.blocks.find((x) => x.id === id);
    const was = doc.blocks.find((x) => x.id === id);
    if (!b || !was) return null;
    const lane = draft.doc.lanes.find((l) => l.id === b.lane)?.name;
    const n = "ids" in op && op.ids.length > 1 ? `${op.ids.length} blocks` : b.name.split(" · ")[0];
    const comes = draft.doc.links
      .filter((l) => l.to.includes(id) && !("ids" in op && op.ids.includes(l.block)))
      .map((l) => draft.doc.blocks.find((x) => x.id === l.block))
      .filter((x) => x && x.at !== doc.blocks.find((y) => y.id === x.id)?.at)
      .map((x) => x!.name.split(" · ")[0]);
    text =
      op.op === "trim"
        ? `Trim end · ${secs(was.length, fps)} → ${secs(b.length, fps)}`
        : `${n}${b.lane !== was.lane ? ` → ${lane}` : ""} · ${clock(b.at, fps)} – ${clock(end(b), fps)}${comes.length ? ` · ${comes.join(", ")} come${comes.length > 1 ? "" : "s"} too` : ""}${op.op === "move" && op.mode === "overwrite" ? " · Overwrite" : ""}`;
  }
  return (
    <span className="tip" role="status" style={{ left: pv.x, top: pv.y }}>
      {text}
    </span>
  );
}

function BlockView({
  d,
  b,
  takes,
  pf,
  act,
  cls,
  onDown,
}: {
  d: Doc;
  b: Block;
  takes: Takes;
  pf: number;
  act: Act;
  cls: (string | false | undefined)[];
  onDown: (e: React.PointerEvent, b: Block, kind: "move" | "trim") => void;
}) {
  const fps = d.target.fps;
  const width = Math.max(10, b.length * pf - 4);
  const st = statusOf(d, b, takes);
  const take = shown(b, takes);
  const link = d.links.find((l) => l.block === b.id);
  const to = link?.to.map((id) => d.blocks.find((x) => x.id === id)?.name.split(" · ")[0]) ?? [];
  const refs = (b.request.refs ?? []).map((r) => ({ r, text: refText(d, r) }));
  const tieMark = link && (
    <button
      type="button"
      className="lkm"
      title={`Tied to ${to.length > 1 ? `${to[0]}–${to.at(-1)!.replace(/^\D+/, "")}` : to[0]}`}
      aria-label={`Tie to ${to.join(", ")}`}
      onPointerDown={(e) => e.stopPropagation()}
      onClick={(e) => {
        e.stopPropagation();
        openMenu(e, tieMenu(act, link));
      }}
      onContextMenu={(e) => {
        e.stopPropagation();
        openMenu(e, tieMenu(act, link));
      }}
    >
      <svg
        width="10"
        height="10"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="2.2"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden
      >
        <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71" />
        <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71" />
      </svg>
    </button>
  );
  const fx = b.effects.map((e, i) => (
    <button
      key={i}
      type="button"
      className={`fx ${e.off ? "off" : ""}`}
      title="Effect"
      onPointerDown={(ev) => ev.stopPropagation()}
      onClick={(ev) => {
        ev.stopPropagation();
        openMenu(ev, effectMenu(act, b, i));
      }}
      onContextMenu={(ev) => {
        ev.stopPropagation();
        openMenu(ev, effectMenu(act, b, i));
      }}
    >
      {e.name}
    </button>
  ));
  const chips = refs.slice(0, 1).map(({ r, text }, i) => (
    <span
      key={i}
      className={`rc ${"asset" in r ? (d.assets.find((a) => a.id === r.asset)?.kind === "character" ? "ch" : "") : "lk"}`}
    >
      {text}
    </span>
  ));
  if (refs.length > 1)
    chips.push(
      <span
        key="more"
        className="rc"
        title={refs
          .slice(1)
          .map((x) => x.text)
          .join(", ")}
      >
        +{refs.length - 1}
      </span>,
    );
  const status = <StatusChip d={d} b={b} takes={takes} />;
  const art = take?.art;
  const audioRow = (orb: React.ReactNode, color: string) => (
    <div className="arow">
      {orb}
      <Wave color={color} width={width - 40} height={b.kind === "music" ? 20 : 16} />
    </div>
  );
  return (
    <div
      role="button"
      tabIndex={0}
      aria-label={`${KIND_NAME[b.kind]} block ${b.name}, ${secs(b.length, fps)}`}
      aria-pressed={cls.includes("sel")}
      className={["blk", st.s === "old" && "old", st.s === "failed" && "fl", ...cls]
        .filter(Boolean)
        .join(" ")}
      style={{ left: b.at * pf + 2, width }}
      onPointerDown={(e) => onDown(e, b, "move")}
      onDoubleClick={() => act.open(b.id)}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          act.open(b.id);
        }
      }}
      onContextMenu={(e) => {
        if (!act.sel.includes(b.id)) act.select([b.id]);
        openMenu(e, blockMenu({ ...act, sel: act.sel.includes(b.id) ? act.sel : [b.id] }, b));
      }}
    >
      <div className="bh">
        <span className={`kb k-${b.kind}`}>{KIND_NAME[b.kind]}</span>
        <b>{b.name}</b>
        <span className="len">{secs(b.length, fps)}</span>
      </div>
      {b.kind === "frame" && (
        <div
          className={`art sk ${b.request.style === "rough" ? "rough" : ""} ${art ? "" : "none"}`}
          style={{ backgroundImage: art }}
        />
      )}
      {(b.kind === "video" || b.kind === "image") && (
        <div className={`art fs ${art ? "" : "none"}`} style={{ backgroundImage: art }} />
      )}
      {b.kind === "voice" && (
        <>
          <span className="scr">
            {b.request.turns?.length
              ? `${b.request.turns.length} turns`
              : b.request.line
                ? `"${b.request.line}"`
                : "No line yet"}
          </span>
          {audioRow(
            <span className="mo k8v" aria-hidden>
              <svg
                width="12"
                height="12"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2.2"
                strokeLinecap="round"
                strokeLinejoin="round"
              >
                <path d="M12 19v3" />
                <path d="M19 10v2a7 7 0 0 1-14 0v-2" />
                <rect x="9" y="2" width="6" height="13" rx="3" />
              </svg>
            </span>,
            "#f2b84b",
          )}
        </>
      )}
      {b.kind === "music" &&
        audioRow(
          <span className="mcv" aria-hidden style={{ background: art ?? "var(--s5)" }}>
            <span className="disc" />
          </span>,
          "#d59bf6",
        )}
      {b.kind === "sound" &&
        audioRow(
          <span className="mo k8s" aria-hidden>
            <svg
              width="12"
              height="12"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2.2"
              strokeLinecap="round"
            >
              <path d="M2 10v3M6 6v11M10 3v18M14 8v7M18 5v13M22 10v3" />
            </svg>
          </span>,
          "#7cc7f2",
        )}
      <div className="bf">
        {b.kind === "voice" || b.kind === "music" ? null : chips}
        {fx}
        {(b.kind === "voice" || b.kind === "music" || b.kind === "sound") && tieMark}
        {(b.kind === "music" || (b.kind === "sound" && b.request.model)) && (
          <span className="text-[10px] text-t2">{b.request.model}</span>
        )}
        <span className="grow" />
        {b.pinned && (
          <span className="pin" title="Pinned take">
            <Pin size={10} aria-hidden />
          </span>
        )}
        {status}
      </div>
      <span className="redge" aria-hidden onPointerDown={(e) => onDown(e, b, "trim")} />
    </div>
  );
}

/** The Assets row (frame 1): this board's assets, off the ruler. */
export function AssetsRow({
  doc,
  act,
  usedAsset,
  voiceName,
}: {
  doc: Doc;
  act: Act;
  usedAsset: string | null;
  /** a saved voice's name ("Warm") */
  voiceName?: (id: string) => string | undefined;
}) {
  const kindCls = { character: "k-char", prop: "k-prop", place: "k-place" } as const;
  // its first view's picture, else a colour of its handle
  const thumbOf = (x: Doc["assets"][number]): React.CSSProperties => {
    const art = x.sheet.map((t) => act.takes[t]?.art).find(Boolean);
    return art
      ? { backgroundImage: art, backgroundSize: "cover", backgroundPosition: "center" }
      : { background: artOf(x.handle) };
  };
  const kindName = { character: "Character", prop: "Prop", place: "Place" } as const;
  return (
    <div className="shelf" role="group" aria-label="Assets, off the time ruler">
      <div className="lh" style={{ position: "static" }}>
        <b>Assets</b>
        <small>{doc.assets.length} · this board&apos;s</small>
      </div>
      <div className="shb overflow-x-auto">
        {doc.assets.map((x) => {
          const used = usedIn(doc, x.id);
          const count = (k: Block["kind"], w: string) => {
            const n = used.filter((b) => b.kind === k).length;
            return n ? `${n} ${w}${n === 1 ? "" : "s"}` : "";
          };
          const u = [
            count("frame", "frame"),
            count("video", "shot"),
            count("voice", "line"),
            count("sound", "sound"),
          ]
            .filter(Boolean)
            .join(" · ");
          return (
            <button
              key={x.id}
              type="button"
              className={`mast ${usedAsset === x.id ? "hov" : ""}`}
              aria-label={`Asset @${x.handle}, ${kindName[x.kind]}`}
              aria-pressed={usedAsset === x.id}
              title="Click: where it's used · double-click or Enter: open"
              onClick={() => act.used(usedAsset === x.id ? null : x.id)}
              onDoubleClick={() => act.openAsset?.(x.id)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && act.openAsset) {
                  e.preventDefault();
                  act.openAsset(x.id);
                }
              }}
              onContextMenu={(e) => openMenu(e, assetMenu(act, x))}
            >
              <span className="ath" aria-hidden style={thumbOf(x)} />
              <span>
                <span className="nm">
                  @{x.handle}
                  <span className={`kb ${kindCls[x.kind]}`}>{kindName[x.kind]}</span>
                </span>
                <small>
                  {x.voice_id
                    ? `voice · ${voiceName?.(x.voice_id) ?? x.voice_id}`
                    : `${x.sheet.length} view${x.sheet.length === 1 ? "" : "s"}`}
                </small>
                <small className="u">{u || "not used yet"}</small>
              </span>
            </button>
          );
        })}
        <button
          type="button"
          className="astb"
          disabled={!act.openAsset}
          onClick={() => act.openAsset?.("new")}
        >
          + Asset
        </button>
        <button
          type="button"
          className="astb"
          disabled={!act.importTo}
          title={act.importTo ? "Import from Gallery, as a copy" : "The Gallery is in studio"}
          onClick={() => act.importTo?.()}
        >
          Import
        </button>
      </div>
    </div>
  );
}

const artOf = (s: string) => {
  let h = 0;
  for (const c of s) h = (h * 31 + c.charCodeAt(0)) % 360;
  return `radial-gradient(60% 60% at 40% 35%, hsl(${h} 70% 65% / .8), transparent 70%), linear-gradient(160deg, hsl(${(h + 40) % 360} 40% 24%), hsl(${h} 45% 10%))`;
};
