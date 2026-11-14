"use client";
// Small pieces both halves of Catalog Agent use (ported from the demo's CreatePanel.jsx and PoseTile.jsx): chat
// bubbles, typing dots, the staggered reveal, line icons, the section colours and the pose tile.
import { createContext, useContext, useEffect, useState, type CSSProperties, type ReactNode } from "react";
import { activeCategories, poseNoun, type Detail, type Library, type MemoryEntry, type PoseNoun } from "@/lib/catalog";
import type { Ops } from "./agent";

/** SVG paths of the line icons. */
export const ICON = {
  folder: "M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2V7z",
  csv: "M9 17v-6m3 6V7m3 10v-3M5 21h14a2 2 0 002-2V5a2 2 0 00-2-2H5a2 2 0 00-2 2v14a2 2 0 002 2z",
  drive: "M7.5 3h9l5 9-4.5 8h-10L2.5 12z",
  globe:
    "M12 21a9 9 0 100-18 9 9 0 000 18zM3.6 9h16.8M3.6 15h16.8M12 3a15 15 0 010 18M12 3a15 15 0 000 18",
  doc: "M7 3h7l5 5v13H7zM14 3v5h5",
  link: "M10 14a4 4 0 005.66 0l3-3a4 4 0 00-5.66-5.66l-1 1M14 10a4 4 0 00-5.66 0l-3 3a4 4 0 005.66 5.66l1-1",
  heart: "M12 20s-7-4.35-7-10a4 4 0 017-2.65A4 4 0 0119 10c0 5.65-7 10-7 10z",
  grid: "M4 5h6v6H4zM14 5h6v6h-6zM4 15h6v6H4zM14 15h6v6h-6z",
  upload: "M4 16l4-4 4 4 4-6 4 6M4 20h16M12 4v8m0-8l-3 3m3-3l3 3",
  camera: "M4 8h3l2-3h6l2 3h3v11H4zM12 17a4 4 0 100-8 4 4 0 000 8z",
  spark: "M12 3l1.8 5.2L19 10l-5.2 1.8L12 17l-1.8-5.2L5 10l5.2-1.8L12 3z",
  alert:
    "M12 9v4M12 17h.01M10.3 3.9L1.8 18a2 2 0 001.7 3h17a2 2 0 001.7-3L13.7 3.9a2 2 0 00-3.4 0z",
  pen: "M15.2 5.2l3.6 3.6M4 20l4.2-.9L19.4 7.9a1.6 1.6 0 000-2.3l-1-1a1.6 1.6 0 00-2.3 0L4.9 15.8 4 20z",
  frame: "M4 6h16v12H4zM8 10h8v4H8z",
  check: "M5 13l4 4L19 7",
  refresh: "M4 4v5h5M20 20v-5h-5M5.5 15a7 7 0 0012.2 2.3M18.5 9A7 7 0 006.3 6.7",
  play: "M7 5.5v13l11-6.5-11-6.5z",
  download: "M12 4v11m0 0l-4-4m4 4l4-4M4 20h16",
  info: "M12 22a10 10 0 100-20 10 10 0 000 20zM12 16v-4M12 8h.01",
  left: "M15 19l-7-7 7-7",
  right: "M9 5l7 7-7 7",
  close: "M6 6l12 12M18 6L6 18",
  expand: "M4 9V4h5M20 9V4h-5M4 15v5h5M20 15v5h-5",
  home: "M3 12l9-9 9 9M5 10v10a1 1 0 001 1h4v-6h4v6h4a1 1 0 001-1V10",
  lock: "M8 11V7a4 4 0 118 0v4M6 11h12v9H6z",
  skip: "M5 5l7 7-7 7M13 5l7 7-7 7",
  plus: "M12 5v14M5 12h14",
  chat: "M4 5h16v11H9l-5 4z",
  // a raised hand (Tabler's hand-stop, MIT)
  stop:
    "M8 13V5.5a1.5 1.5 0 013 0V12M11 5.5v-2a1.5 1.5 0 113 0V12M14 5.5a1.5 1.5 0 013 0V12M17 7.5a1.5 1.5 0 013 0V16a6 6 0 01-6 6h-2 .208a6 6 0 01-5.012-2.7l-.196-.3c-.312-.479-1.407-2.388-3.286-5.728a1.5 1.5 0 01.536-2.022 1.867 1.867 0 012.28.28L8 13",
  compare: "M4 5h7v14H4zM13 5h7v14h-7z",
};

/** A line icon from one SVG path. */
export const railIcon = (d: string, cls = "h-4 w-4") => (
  <svg className={cls} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.8} aria-hidden>
    <path strokeLinecap="round" strokeLinejoin="round" d={d} />
  </svg>
);

/** An "i" button that shows `text` in a small popover: on hover, on keyboard focus and on a tap or click (touch),
 * closed by Escape, blur or leaving it. */
export function InfoTip({ text, label = "What is this?" }: { text: string; label?: string }) {
  const [open, setOpen] = useState(false);
  return (
    <span className="relative inline-flex" onMouseEnter={() => setOpen(true)} onMouseLeave={() => setOpen(false)}>
      <button
        type="button"
        aria-label={label}
        aria-expanded={open}
        onClick={() => setOpen(true)}
        onFocus={() => setOpen(true)}
        onBlur={() => setOpen(false)}
        onKeyDown={(e) => e.key === "Escape" && setOpen(false)}
        className="rounded-full p-0.5 text-gray-500 hover:text-white focus:text-white focus:outline-none"
      >
        {railIcon(ICON.info, "h-3.5 w-3.5")}
      </button>
      {open && (
        <span role="tooltip" className="absolute top-full right-0 z-50 mt-1 w-64 rounded-lg border border-surface-border bg-surface-raised px-3 py-2 text-xs leading-snug text-gray-200 shadow-lg">
          {text}
        </span>
      )}
    </span>
  );
}

/** Each DescribeBox kind's placeholder and the one-line note on how its words are used. */
const DESCRIBE = {
  model: { placeholder: "South Indian woman in her late 20s, wavy shoulder-length hair", hint: "" },
  background: { placeholder: "sunlit marble terrace with soft palm shadows", hint: "" },
  shots: {
    placeholder: "4 shots: full front on the model, close-up of the embroidery, walking outdoors, back view",
    hint: "Sets the exact shots and their number.",
  },
};

/** The brand's own words for a model, background or a product's shots ("Describe your model"), next to every place
 * one is picked: the only choice when nothing is picked, else added to the pick. `name` names the product when a
 * session has several. Capped at the server's 500 characters. */
export function DescribeBox({
  kind,
  value,
  onChange,
  name,
}: {
  kind: keyof typeof DESCRIBE;
  value: string;
  onChange: (v: string) => void;
  name?: string;
}) {
  return (
    <label className="mt-3 block">
      <span className="mb-1 block text-xs text-gray-400">
        Describe your {name ? `${name} ` : ""}
        {kind} <span className="text-gray-500">(optional){DESCRIBE[kind].hint && ` — ${DESCRIBE[kind].hint}`}</span>
      </span>
      <input
        value={value}
        onChange={(e) => onChange(e.target.value)}
        maxLength={500}
        placeholder={DESCRIBE[kind].placeholder}
        className="w-full rounded-lg border border-surface-border bg-surface px-3 py-2 text-sm text-gray-100 placeholder:text-gray-500 focus:border-accent focus:outline-none"
      />
    </label>
  );
}

/** One section's shades: the basics and details in the accent, poses teal, models and backgrounds pink. Full class
 * names, so Tailwind keeps them. */
export type Tone = { card: string; text: string; primary: string; dot: string; sel: string };
const TONES: Record<"accent" | "teal" | "pink", Tone> = {
  accent: {
    card: "border-accent/30 bg-accent/[0.05]",
    text: "text-accent-hover",
    primary: "border-accent/60 bg-accent/10 hover:border-accent hover:bg-accent/15",
    dot: "bg-accent",
    sel: "bg-accent/15",
  },
  teal: {
    card: "border-teal-400/30 bg-teal-400/[0.05]",
    text: "text-teal-300",
    primary: "border-teal-400/60 bg-teal-400/10 hover:border-teal-400 hover:bg-teal-400/15",
    dot: "bg-teal-400",
    sel: "bg-teal-400/15",
  },
  pink: {
    card: "border-pink-400/30 bg-pink-400/[0.05]",
    text: "text-pink-300",
    primary: "border-pink-400/60 bg-pink-400/10 hover:border-pink-400 hover:bg-pink-400/15",
    dot: "bg-pink-400",
    sel: "bg-pink-400/15",
  },
};
/** The colour family of a step. */
const family = (step?: string) =>
  step === "poses" ? "teal" : step === "models" || step === "backgrounds" ? "pink" : "accent";
/** A step's shades. */
export const toneOf = (step?: string) => TONES[family(step)];
/** The same shades for a step's own panel: everything accented inside it takes the step's colour. */
export function toneVarsOf(step?: string): CSSProperties | undefined {
  const f = family(step);
  if (f === "teal") return { "--catalog-accent": "13 148 136", "--catalog-accent-hover": "45 212 191" } as CSSProperties;
  if (f === "pink") return { "--catalog-accent": "219 39 119", "--catalog-accent-hover": "244 114 182" } as CSSProperties;
  return undefined;
}

/** A chat bubble: the agent's on the left, the brand's on the right. */
export function Bubble({ from, children }: { from: "agent" | "user"; children: ReactNode }) {
  const mine = from === "user";
  return (
    <div className={`flex ${mine ? "justify-end" : "justify-start"}`}>
      <div
        className={`max-w-[85%] rounded-2xl px-3.5 py-2.5 text-sm leading-relaxed [overflow-wrap:anywhere] ${mine ? "bg-accent/20 text-white" : "bg-surface-raised text-gray-200"}`}
      >
        {children}
      </div>
    </div>
  );
}

/** The brand's side of the chat: a plain, quiet bubble on the right. */
export function UserLine({ children }: { children: ReactNode }) {
  return (
    <div className="flex justify-end">
      <div className="max-w-[80%] rounded-2xl bg-white/[0.06] px-3.5 py-2 text-sm text-gray-200 [overflow-wrap:anywhere]">{children}</div>
    </div>
  );
}

/** Three bouncing dots while the agent "types". */
export function TypingDots() {
  return (
    <div className="flex justify-start" aria-label="Typing">
      <div className="flex items-center gap-1 rounded-2xl bg-surface-raised px-3.5 py-3">
        {[0, 160, 320].map((d) => (
          <span
            key={d}
            className="h-1.5 w-1.5 animate-bounce rounded-full bg-gray-400"
            style={{ animationDelay: `${d}ms` }}
          />
        ))}
      </div>
    </div>
  );
}

/**
 * Reveals chat messages one at a time, like a person typing: lines `instant` says are the brand's own show at once,
 * each agent line waits behind typing dots. Messages already there when the chat opened show straight away.
 */
export function useStaggered<T extends { id: string; text?: string }>(list: T[], instant: (m: T) => boolean) {
  const [shown, setShown] = useState(() => new Set(list.map((m) => m.id)));
  const hiddenAt = list.findIndex((m) => !shown.has(m.id));
  const next = hiddenAt === -1 ? null : list[hiddenAt];
  const quick = !!next && instant(next);
  const nextId = next?.id;
  const wait = quick ? 0 : 650 + Math.min(String(next?.text ?? "").length * 6, 650);
  useEffect(() => {
    if (!nextId) return undefined;
    const t = setTimeout(() => setShown((s) => new Set(s).add(nextId)), wait);
    return () => clearTimeout(t);
  }, [nextId, wait]);
  return { visible: next ? list.slice(0, hiddenAt) : list, typing: !!next && !quick };
}

/** A status line in the chat: a spinner while the work runs, then a tick (or a red mark when it failed); a `detail`
 * (e.g. each guideline document and why it wasn't read) opens under View details. */
export function UpdateLine({ text, done, error, detail }: { text?: string; done?: boolean; error?: boolean; detail?: string | null }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="flex items-start gap-2 pl-1 text-[13px] text-gray-300">
      <span
        className={`mt-0.5 flex h-5 w-5 shrink-0 items-center justify-center rounded-full ${error ? "bg-rose-500/15 text-rose-400" : done ? "bg-emerald-500/15 text-emerald-400" : "bg-white/[0.06] text-gray-400"}`}
      >
        {error ? (
          railIcon(ICON.alert, "h-3 w-3")
        ) : done ? (
          railIcon(ICON.check, "h-3 w-3")
        ) : (
          <span className="h-3 w-3 animate-spin rounded-full border-[1.5px] border-gray-500 border-t-transparent" />
        )}
      </span>
      <span className={`min-w-0 flex-1 leading-relaxed ${error ? "text-rose-300" : ""}`}>
        {text}
        {detail && (
          <button type="button" onClick={() => setOpen((o) => !o)} aria-expanded={open} className="ml-2 text-xs text-accent-hover hover:underline">
            {open ? "Hide details" : "View details"}
          </button>
        )}
        {detail && open && (
          <span className="mt-1.5 block border-t border-white/10 pt-1.5 text-xs whitespace-pre-wrap text-gray-400 [overflow-wrap:anywhere]">{detail}</span>
        )}
      </span>
    </div>
  );
}

/** A small square icon button with a tooltip. */
export function IconButton({
  onClick,
  disabled,
  title,
  d,
  hint,
}: {
  onClick: () => void;
  disabled?: boolean;
  title: string;
  d: string;
  hint?: string; // a name shown above it on hover at once (as on image tiles), instead of the native title
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      title={hint ? undefined : title}
      aria-label={title}
      className="group/act relative flex h-8 w-8 items-center justify-center rounded-lg border border-surface-border text-gray-300 transition hover:border-accent hover:bg-accent/10 hover:text-white disabled:opacity-30"
    >
      {railIcon(d)}
      {hint && (
        <span className="pointer-events-none absolute right-0 bottom-full mb-1.5 rounded bg-black/85 px-1.5 py-0.5 text-[10px] font-semibold whitespace-nowrap text-white opacity-0 transition group-hover/act:opacity-100 group-focus-visible/act:opacity-100">
          {hint}
        </span>
      )}
    </button>
  );
}

/** One pose to pick: its example picture (library `example_url`, contained so a tall full-body crop shows whole, zoomed by `frame`) with the name under it, ticked
 * when chosen (teal, the poses section's colour). */
export function PoseTile({
  lib,
  pose,
  on,
  onToggle,
}: {
  lib: Library | null;
  pose: string;
  on: boolean;
  onToggle: () => void;
}) {
  const p = lib?.poses.find((x) => x.id === pose);
  return (
    <button
      type="button"
      onClick={onToggle}
      aria-pressed={on}
      className={`group relative overflow-hidden rounded-lg border text-left transition ${on ? "border-teal-400 ring-1 ring-teal-400" : "border-surface-border hover:border-gray-500"}`}
    >
      <div className="aspect-square overflow-hidden bg-white/[0.04]">
        {p?.example_url ? (
          <img
            src={p.example_url}
            alt=""
            loading="lazy"
            className={`h-full w-full object-contain transition ${on ? "" : "opacity-70 group-hover:opacity-100"}`}
            style={p.frame ? { transform: `scale(${p.frame.zoom})`, transformOrigin: p.frame.origin } : undefined}
          />
        ) : (
          <div className="flex h-full flex-col items-center justify-center gap-1 text-gray-600">
            {railIcon("M4 5h16v14H4zM4 15l4-4 4 4 3-3 5 5", "h-5 w-5")}
            <span className="text-[9px]">No example yet</span>
          </div>
        )}
      </div>
      <div className={`truncate px-1.5 py-1 text-[11px] ${on ? "bg-teal-400/15 text-white" : "text-gray-400"}`}>
        {p?.name ?? pose}
      </div>
      <span
        className={`absolute top-1 right-1 flex h-5 w-5 items-center justify-center rounded-full border text-[10px] ${on ? "border-teal-400 bg-teal-400 text-gray-900" : "border-white/50 bg-black/40 text-transparent group-hover:text-white/70"}`}
      >
        ✓
      </span>
    </button>
  );
}

/** What every part of the screen reads (library, session, memory) and the server calls it can make. */
export type Catalog = {
  lib: Library | null;
  detail: Detail;
  memory: MemoryEntry[];
  ops: Ops;
};
/** The screen's shared state, provided by CatalogAgent. */
export const CatalogContext = createContext<Catalog | null>(null);
/** The screen's shared state; only used under CatalogAgent. */
export function useCatalog(): Catalog {
  const v = useContext(CatalogContext);
  if (!v) throw new Error("useCatalog outside CatalogAgent");
  return v;
}
/** What the session's products call a pose: their shared word, else the generic one. */
export function usePoseNoun(): PoseNoun {
  const { lib, detail } = useCatalog();
  return poseNoun(lib, activeCategories(lib, detail.skus));
}
