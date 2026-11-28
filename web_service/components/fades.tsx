"use client";
// Audio fades, crossfades and ducking (plan 41, docs/mock/AudioFades.dc.html; contracts/README.md "Audio fades,
// crossfades and ducking"), the pieces the Timeline and Magic share: a clip's fade curve over its waveform, the fade
// handles at its top corners (under the title row) with their drag readout, the crossfade badge (sky; amber for a dip)
// and its popover, the dip warning, the ducked stretches on a music lane and the Duck music row.
import { useEffect, useRef, useState } from "react";
import { TriangleAlert } from "lucide-react";
import { openMenu, type Item, type MenuItem } from "@/components/context-menu";
import {
  CrossfadeIcon,
  DissolveIcon,
  FadeInIcon,
  FadeOutIcon,
  NoFadeIcon,
} from "@/components/magic/icons";
import {
  DUCK_DB,
  DUCK_PICKS,
  FADE_MAX,
  FADE_PICKS,
  Grid,
  XFADE_S,
  lenOf,
  type Clip,
  type Norm,
  type Op,
} from "@/lib/timeline";
import type { LucideIcon } from "lucide-react";

/** "1.5 s", "2 s" */
export const sText = (s: number) => `${+s.toFixed(2)} s`;
/** "−12 dB" */
export const duckText = (db: number) => `${db < 0 ? "−" : ""}${Math.abs(db)} dB`;

/** The fade curve over a clip (frame 1): the part a fade cuts shaded, a line from silence at the clip's edge. `fin`,
 * `fout` and `w` in px. */
export function FadeCurve({
  w,
  fin,
  fout,
  color = "var(--t1)",
}: {
  w: number;
  fin: number;
  fout: number;
  color?: string;
}) {
  if (!(fin > 0) && !(fout > 0)) return null;
  const W = Math.max(1, w);
  const o = W - fout;
  return (
    <svg
      className="fov"
      aria-hidden
      viewBox={`0 0 ${W} 60`}
      preserveAspectRatio="none"
      data-fades={`${Math.round(fin)},${Math.round(fout)}`}
    >
      {fin > 0 && (
        <>
          <path d={`M0 0H${fin}L0 60Z`} fill="rgba(0,0,0,.42)" />
          <path
            d={`M0 60 C${fin * 0.45} 21 ${fin * 0.7} 3 ${fin} 0`}
            fill="none"
            stroke={color}
            strokeWidth="1.6"
            vectorEffect="non-scaling-stroke"
          />
        </>
      )}
      {fout > 0 && (
        <>
          <path d={`M${o} 0H${W}V60Z`} fill="rgba(0,0,0,.42)" />
          <path
            d={`M${o} 0 C${o + fout * 0.3} 3 ${o + fout * 0.55} 21 ${W} 60`}
            fill="none"
            stroke={color}
            strokeWidth="1.6"
            vectorEffect="non-scaling-stroke"
          />
        </>
      )}
    </svg>
  );
}

export type FadeKey = "fade_in" | "fade_out";

/** The two fade handles at a clip's top corners (frame 1): hollow on hover, filled when the clip is selected, at the
 * fade's end once it has one. Drag inward to lengthen: 0.1 s steps, `snap` (seconds from the clip's start → the time
 * it snaps to) for the playhead and other edges; a yellow readout follows; one `onSet` per drag (one undo step). */
export function FadeHandles({
  len,
  pps,
  fadeIn = 0,
  fadeOut = 0,
  onSet,
  snap,
  onDraft,
}: {
  /** the clip's length (s) */
  len: number;
  pps: number;
  fadeIn?: number;
  fadeOut?: number;
  onSet: (k: FadeKey, s: number) => void;
  snap?: (t: number) => number;
  /** the fades while a handle is dragged (null: done), so the curve follows */
  onDraft?: (f: { fade_in: number; fade_out: number } | null) => void;
}) {
  const [drag, setDrag] = useState<{ k: FadeKey; v: number } | null>(null);
  const fi = drag?.k === "fade_in" ? drag.v : fadeIn;
  const fo = drag?.k === "fade_out" ? drag.v : fadeOut;
  const down = (k: FadeKey) => (e: React.PointerEvent<HTMLElement>) => {
    if (e.button !== 0) return;
    e.stopPropagation();
    e.preventDefault();
    e.currentTarget.setPointerCapture?.(e.pointerId);
    const x0 = e.clientX;
    const was = k === "fade_in" ? fadeIn : fadeOut;
    const other = k === "fade_in" ? fadeOut : fadeIn;
    const at = (cx: number) => {
      const dx = (cx - x0) / pps;
      let v = was + (k === "fade_in" ? dx : -dx);
      // the playhead and other edges, as a point in the clip's own time
      if (snap) {
        const p = k === "fade_in" ? v : len - v;
        const s = snap(p);
        if (s !== p) v = k === "fade_in" ? s : len - s;
      }
      v = Math.round(v * 10) / 10;
      return Math.max(0, Math.min(FADE_MAX, len - other, v));
    };
    const el = e.currentTarget;
    let last = was;
    const mv = (m: PointerEvent) => {
      last = at(m.clientX);
      setDrag({ k, v: last });
      onDraft?.(
        k === "fade_in" ? { fade_in: last, fade_out: other } : { fade_in: other, fade_out: last },
      );
    };
    const up = () => {
      el.removeEventListener("pointermove", mv);
      el.removeEventListener("pointerup", up);
      el.removeEventListener("pointercancel", up);
      setDrag(null);
      onDraft?.(null);
      if (Math.abs(last - was) > 1e-9) onSet(k, last);
    };
    el.addEventListener("pointermove", mv);
    el.addEventListener("pointerup", up);
    el.addEventListener("pointercancel", up);
  };
  const w = len * pps;
  const handle = (k: FadeKey, x: number, v: number, name: string) => (
    <span
      key={k}
      role="slider"
      tabIndex={-1}
      aria-label={name}
      aria-valuemin={0}
      aria-valuemax={FADE_MAX}
      aria-valuenow={v}
      aria-valuetext={sText(v)}
      data-fade={k}
      className={`fh-h ${drag?.k === k ? "drag" : ""} ${v ? "set" : ""}`}
      style={{ left: x }}
      onPointerDown={down(k)}
      onClick={(e) => e.stopPropagation()}
      onDoubleClick={(e) => e.stopPropagation()}
    />
  );
  return (
    <>
      {handle("fade_in", Math.min(fi * pps, w), fi, "Fade in")}
      {handle("fade_out", Math.max(0, w - fo * pps), fo, "Fade out")}
      {drag && (
        <span
          className="fh-rd"
          style={{ left: drag.k === "fade_in" ? fi * pps : w - fo * pps }}
          role="status"
        >
          {drag.k === "fade_in" ? "Fade in" : "Fade out"} <b className="mono">{sText(drag.v)}</b>
        </span>
      )}
    </>
  );
}

/** The crossfade badge on a join (frame 2): the bowtie and the length, sky; amber with the warning glyph for a dip.
 * A click opens its popover: the length (1, 2, 4 s or typed, up to 10 s) and Remove. */
export function XfadeBadge({
  left,
  seconds,
  dip,
  title,
  note,
  onSet,
  className = "",
}: {
  left: number;
  seconds: number;
  dip: boolean;
  /** the hover text: "Crossfade 2 s · Night bus fades in under it" */
  title: string;
  /** the popover's line: "Up to 10 s. Soft rain theme runs 2 s longer." */
  note: string;
  /** a new length, or null: Remove */
  onSet: (s: number | null) => void;
  className?: string;
}) {
  const [open, setOpen] = useState<{ x: number; y: number } | null>(null);
  return (
    <>
      <button
        type="button"
        className={`tj ${dip ? "dip" : "xf"} ${className}`}
        aria-label={`${dip ? "Dip" : "Crossfade"} ${sText(seconds)}`}
        title={title}
        style={{ left }}
        onPointerDown={(e) => e.stopPropagation()}
        onClick={(e) => {
          e.stopPropagation();
          const r = e.currentTarget.getBoundingClientRect();
          setOpen(open ? null : { x: r.left, y: r.bottom + 6 });
        }}
        onContextMenu={(e) => {
          e.stopPropagation();
          openMenu(e, {
            title: "Crossfade",
            items: [
              ...[1, 2, 4].map((s) => ({
                label: sText(s),
                checked: Math.abs(s - seconds) < 1e-6,
                onPick: () => onSet(s),
              })),
              "-",
              { label: "Remove", danger: true, onPick: () => onSet(null) },
            ],
          });
        }}
      >
        {dip ? <TriangleAlert size={14} aria-hidden /> : <DissolveIcon />}
        {sText(seconds)}
      </button>
      {open && (
        <LengthPop
          at={open}
          title="Crossfade"
          value={seconds}
          picks={[1, 2, 4]}
          note={note}
          onClose={() => setOpen(null)}
          onSet={onSet}
        />
      )}
    </>
  );
}

/** A small popover at a point: the length typed (Enter sets it), quick picks, a line, Remove. */
function LengthPop({
  at,
  title,
  value,
  picks,
  note,
  onSet,
  onClose,
}: {
  at: { x: number; y: number };
  title: string;
  value: number;
  picks: number[];
  note: string;
  onSet: (s: number | null) => void;
  onClose: () => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [v, setV] = useState(String(+value.toFixed(2)));
  useEffect(() => {
    ref.current?.querySelector("input")?.focus();
    const out = (e: Event) => !ref.current?.contains(e.target as Node) && onClose();
    const key = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    addEventListener("pointerdown", out, true);
    addEventListener("keydown", key);
    return () => {
      removeEventListener("pointerdown", out, true);
      removeEventListener("keydown", key);
    };
  }, [onClose]);
  const set = (x: number | null) => {
    onClose();
    if (x === null) return onSet(null);
    if (Number.isFinite(x) && x > 0) onSet(Math.min(FADE_MAX, x));
  };
  return (
    <div
      ref={ref}
      role="dialog"
      aria-label={title}
      className="cm secs xpop"
      style={{ left: Math.min(at.x, innerWidth - 232), top: at.y }}
      onPointerDown={(e) => e.stopPropagation()}
      onClick={(e) => e.stopPropagation()}
    >
      <span className="hd">{title}</span>
      <label className="secs-in">
        <input
          type="number"
          inputMode="decimal"
          min={0}
          max={FADE_MAX}
          step={0.1}
          value={v}
          aria-label={`${title} (s)`}
          onChange={(e) => setV(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && set(parseFloat(v))}
        />
        <span>s</span>
      </label>
      <div className="secs-picks">
        {picks.map((p) => (
          <button
            key={p}
            type="button"
            className={`chip ${Math.abs(p - value) < 1e-6 ? "on" : ""}`}
            onClick={() => set(p)}
          >
            {p} s
          </button>
        ))}
      </div>
      <p className="secs-note">{note}</p>
      <button type="button" className="xpop-rm" onClick={() => set(null)}>
        Remove
      </button>
    </div>
  );
}

/** The dip warning (frame 2): a line and its actions. */
export function DipWarn({ text, children }: { text: string; children?: React.ReactNode }) {
  return (
    <div className="dipw" role="status">
      <TriangleAlert size={16} aria-hidden className="shrink-0" />
      <span className="grow">{text}</span>
      {children}
    </div>
  );
}

/** The ducked stretches over a music clip or block (frame 3): `spans` (s, in the clip's own time from its start, cut
 * to it already), shaded sky with the amount on a tag. */
export function DuckShades({
  spans,
  pps,
  db,
}: {
  spans: [number, number][];
  pps: number;
  db: number;
}) {
  return (
    <>
      {spans.map(([a, b]) => (
        <span
          key={`${a}-${b}`}
          className="duck"
          aria-label={`Ducked ${duckText(db)}`}
          style={{ left: a * pps, width: Math.max(1, (b - a) * pps) }}
        >
          <span>{duckText(db)}</span>
        </span>
      ))}
    </>
  );
}

/** The voice spans that meet [a, b) (sequence s), in the clip's own time: what DuckShades draws on it. */
export function spansIn(spans: [number, number][], a: number, b: number): [number, number][] {
  return spans
    .filter(([x, y]) => x < b && y > a)
    .map(([x, y]) => [Math.max(x, a) - a, Math.min(y, b) - a] as [number, number]);
}

/** The Duck music row (frame 3): the switch, its line, the amount (−6, −9, −12, −18 dB or typed, −24 to −3). */
export function DuckRow({
  on,
  db = DUCK_DB,
  onSet,
  disabled,
}: {
  on: boolean;
  db?: number;
  onSet: (d: { on?: boolean; db?: number }) => void;
  disabled?: boolean;
}) {
  return (
    <div className="srow">
      <button
        type="button"
        role="switch"
        aria-checked={on}
        aria-label="Duck music"
        className="dk-tog"
        disabled={disabled}
        onClick={() => onSet({ on: !on })}
      />
      <span className="flex min-w-0 grow flex-col">
        <span>Duck music</span>
        <small>Under voice · eases 0.3 s</small>
      </span>
      <button
        type="button"
        className={`dbc ${on ? "" : "text-t2"}`}
        aria-label={`Duck by ${duckText(db)}`}
        disabled={disabled}
        onClick={(e) =>
          openMenu(e, {
            title: "Duck by",
            items: [
              ...DUCK_PICKS.map((v) => ({
                label: duckText(v),
                checked: v === db,
                onPick: () => onSet({ db: v }),
              })),
              "-",
              {
                label: "Typed…",
                seconds: {
                  title: "Duck by",
                  value: db,
                  picks: DUCK_PICKS,
                  min: -24,
                  max: -3,
                  unit: "dB",
                  note: "−24 to −3 dB. Enter sets it.",
                  onSet: (v: number) => onSet({ db: Math.round(v) }),
                },
              },
            ],
          })
        }
      >
        {duckText(db)}
      </button>
    </div>
  );
}

/** 1.106 (AudioFades frame 4): an audio clip's Fade in…, Fade out…, Crossfade into next (greyed with No clip after
 * when none touches it) and Remove fades. */
export function clipFadeItems(
  doc: Norm,
  ti: number,
  c: Clip,
  commit: (op: Op) => void,
  off?: string,
): Item[] {
  const g = new Grid(doc.target);
  const t = doc.tracks[ti];
  const endF = g.f(c.at!) + lenOf(c, "video", g, true);
  const next = t.clips.find((x) => x !== c && g.f(x.at!) === endF && x.take_id);
  const fade = (k: "fade_in" | "fade_out", label: string, icon: LucideIcon): MenuItem => ({
    label,
    icon,
    off,
    keys: c[k] ? sText(c[k]) : undefined,
    seconds: {
      title: label.replace("…", ""),
      value: c[k] ?? 1,
      picks: FADE_PICKS,
      max: FADE_MAX,
      note: "0 to 10 s. Enter sets it.",
      onSet: (v) => commit({ op: "set", clip: c.id, [k]: v || null }),
    },
  });
  const any = !!(c.fade_in || c.fade_out || c.crossfade);
  return [
    "-",
    fade("fade_in", "Fade in…", FadeInIcon as unknown as LucideIcon),
    fade("fade_out", "Fade out…", FadeOutIcon as unknown as LucideIcon),
    {
      label: c.crossfade ? `Crossfade ${sText(c.crossfade)}…` : "Crossfade into next",
      icon: CrossfadeIcon as unknown as LucideIcon,
      off: off ?? (next ? undefined : "No clip after"),
      keys: next ? undefined : "No clip after",
      ...(c.crossfade
        ? {
            seconds: {
              title: "Crossfade",
              value: c.crossfade,
              picks: [1, 2, 4],
              max: FADE_MAX,
              note: "Up to 10 s.",
              onSet: (v: number) => commit({ op: "set", clip: c.id, crossfade: v || null }),
            },
          }
        : { onPick: () => commit({ op: "set", clip: c.id, crossfade: XFADE_S }) }),
    },
    {
      label: "Remove fades",
      icon: NoFadeIcon as unknown as LucideIcon,
      off: off ?? (any ? undefined : "No fades"),
      keys: any ? undefined : "No fades",
      onPick: () =>
        commit({ op: "set", clip: c.id, fade_in: null, fade_out: null, crossfade: null }),
    },
  ];
}
