"use client";
// Media in the Assistant's chat (docs/mock/Assistant.dc.html frame 12; alpha.2 items 7, 8, 10; contracts/README.md,
// Assistant: media and vision): a take shown as itself in an answer (image, video with a small player, audio with a
// waveform; Open and Use in…), the @ picker (elements, then takes and uploads, newest first; windowed) and the
// attachment chip. Plain elements, never HTML; never a raw id.
import { useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { ChevronDown, Maximize2, Play, Search, Square, Volume2, VolumeX, X } from "lucide-react";
import { usePlayer } from "@/components/create/audio";
import { useTakePages } from "@/components/gallery/gallery";
import { useGridWindow } from "@/components/gallery/window";
import { FullScreen } from "@/components/media";
import { safeUrl } from "@/lib/format";
import { useLibrary } from "@/lib/library";
import { offerLabel } from "@/lib/source";
import { useTakes, useUses } from "@/lib/take-cache";
import type { S } from "@/lib/api";
import type { Filter } from "@/lib/takes";

type Take = S<"Take">;

// one plays at a time (frame 12b): a video that starts pauses the one before
let playing: HTMLMediaElement | null = null;
function only(el: HTMLMediaElement) {
  if (playing && playing !== el) playing.pause();
  playing = el;
}

const mmss = (s: number) => `${Math.floor(s / 60)}:${String(Math.floor(s % 60)).padStart(2, "0")}`;

/** A take alone on its answer's line: the take itself, its title, Open and Use in…. */
export function TakeMedia({ id }: { id: string }) {
  const take = useTakes([id])(id);
  const [open, setOpen] = useState(false);
  if (take === undefined) return <div className="med" aria-busy="true" style={{ height: 40 }} />;
  if (take === null)
    return (
      <div className="med">
        <div className="mt">
          <span>deleted take</span>
        </div>
      </div>
    );
  const secs = take.video?.duration_s ?? take.audio?.duration_s;
  const title = take.title + (take.kind === "video" && secs ? ` · ${Math.round(secs)} s` : "");
  return (
    <div className="med">
      {take.kind === "image" ? (
        <span
          className="pv pic"
          role="img"
          aria-label={take.title}
          style={{ backgroundImage: `url("${safeUrl(take.thumb_url)}")` }}
        />
      ) : take.kind === "video" ? (
        <Video take={take} />
      ) : (
        <Sound take={take} />
      )}
      <div className="mt">
        <span title={title}>{title}</span>
        {take.kind !== "audio" && (
          <button type="button" className="mb" onClick={() => setOpen(true)}>
            <Maximize2 size={11} strokeWidth={2} aria-hidden />
            Open
          </button>
        )}
        <UseIn take={take} />
      </div>
      {open && <FullScreen take={take} onClose={() => setOpen(false)} onStep={() => undefined} />}
    </div>
  );
}

/** A video: its poster with Play, then a small player (sound off until turned on) with its time bar. */
function Video({ take }: { take: Take }) {
  const ref = useRef<HTMLVideoElement>(null);
  const [on, setOn] = useState(false);
  const [paused, setPaused] = useState(true);
  const [t, setT] = useState(0);
  const [muted, setMuted] = useState(true);
  const len = take.video?.duration_s ?? 0;
  const play = () => {
    setOn(true);
    const v = ref.current;
    if (v) {
      only(v);
      void v.play().catch(() => undefined);
    }
  };
  return (
    <span
      className="pv pic"
      style={on ? undefined : { backgroundImage: `url("${safeUrl(take.thumb_url)}")` }}
    >
      <video
        ref={ref}
        src={on ? (safeUrl(take.url) ?? undefined) : undefined}
        muted={muted}
        playsInline
        autoPlay={on}
        aria-label={take.title}
        className={on ? "absolute inset-0 h-full w-full object-cover" : "hidden"}
        onPlay={(e) => {
          only(e.currentTarget);
          setPaused(false);
        }}
        onPause={() => setPaused(true)}
        onTimeUpdate={(e) => setT(e.currentTarget.currentTime)}
      />
      <span className="absolute inset-0 grid place-items-center">
        {paused ? (
          <button type="button" className="ply" aria-label="Play" onClick={play}>
            <Play size={14} strokeWidth={2.5} aria-hidden />
          </button>
        ) : (
          <button
            type="button"
            className="absolute inset-0 border-0 bg-transparent shadow-none"
            aria-label="Pause"
            onClick={() => ref.current?.pause()}
          />
        )}
      </span>
      <span className="vbar">
        <span>{mmss(t)}</span>
        <i>
          <b style={{ width: `${len ? Math.min(100, (t / len) * 100) : 0}%` }} />
        </i>
        <span>{mmss(len)}</span>
        <button
          type="button"
          className="grid h-5 w-5 place-items-center border-0 bg-transparent p-0 text-white shadow-none"
          aria-label={muted ? "Sound on" : "Sound off"}
          onClick={() => setMuted((m) => !m)}
        >
          {muted ? (
            <VolumeX size={12} strokeWidth={2} aria-hidden />
          ) : (
            <Volume2 size={12} strokeWidth={2} aria-hidden />
          )}
        </button>
      </span>
    </span>
  );
}

// the waveform's bars: a fixed shape (no audio is decoded for it); the played part is lit
const BARS = [6, 12, 20, 14, 24, 18, 9, 22, 16, 26, 12, 8, 18, 24, 10, 16, 20, 7, 14, 22, 12, 6];

/** Audio: Play (one at a time, the app's own player), a waveform and its length. */
function Sound({ take }: { take: Take }) {
  const { playing: now, toggle } = usePlayer();
  const on = now === take.id;
  const url = safeUrl(take.url);
  const lit = on ? BARS.length : 0;
  return (
    <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "6px 4px 0 6px" }}>
      <button
        type="button"
        className="ply"
        aria-label={on ? "Stop" : "Play"}
        disabled={!url}
        onClick={() => {
          playing?.pause();
          if (url) toggle(take.id, url);
        }}
      >
        {on ? (
          <Square size={11} strokeWidth={2.5} aria-hidden />
        ) : (
          <Play size={14} strokeWidth={2.5} aria-hidden />
        )}
      </button>
      <div className="wf" aria-hidden>
        {BARS.map((h, i) => (
          <span key={i} className={i < lit ? "p" : ""} style={{ height: h }} />
        ))}
      </div>
      <span className="mono" style={{ fontSize: 10 }}>
        {mmss(take.audio?.duration_s ?? 0)}
      </span>
    </div>
  );
}

/** Use in…: the take's offers (the Gallery's Use in…: the workflows it fits, opened on Create with it in place). */
function UseIn({ take }: { take: Take }) {
  const [open, setOpen] = useState(false);
  const uses = useUses(open ? take.id : undefined);
  const router = useRouter();
  const box = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const off = (e: MouseEvent) => !box.current?.contains(e.target as Node) && setOpen(false);
    document.addEventListener("mousedown", off);
    return () => document.removeEventListener("mousedown", off);
  }, [open]);
  return (
    <div ref={box} className="relative" onKeyDown={(e) => e.key === "Escape" && setOpen(false)}>
      <button
        type="button"
        className="mb"
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
      >
        Use in…
        <ChevronDown size={11} strokeWidth={2} aria-hidden />
      </button>
      {open && (
        <div className="pop" role="menu" aria-label="Use in" style={{ width: 190 }}>
          {uses === null ? (
            <span className="pr">…</span>
          ) : uses.length === 0 ? (
            <span className="pr">Nothing takes it here</span>
          ) : (
            uses.map((o) => (
              <button
                key={`${o.workflow_id} ${o.param}`}
                type="button"
                role="menuitem"
                className="pr"
                onClick={() => {
                  setOpen(false);
                  router.push(
                    `/?use=${encodeURIComponent(take.id)}&w=${encodeURIComponent(o.workflow_id)}`,
                  );
                }}
              >
                <span>{offerLabel(o)}</span>
              </button>
            ))
          )}
        </div>
      )}
    </div>
  );
}

// the lists' filters, one object each (a new one every render would read the first page again)
const ALL: Filter = {};
const UPLOADS: Filter = { source: "upload" };

/** An @ pick: an element's handle, or a take (its id; the box shows its title). */
export type Pick = { kind: "element"; handle: string } | { kind: "take"; take: Take };

const age = (iso: string, now = Date.now()) => {
  const m = Math.max(0, Math.round((now - Date.parse(iso)) / 60_000));
  return m < 60
    ? `${m} min`
    : m < 1440
      ? `${Math.round(m / 60)} h`
      : m < 2880
        ? "yesterday"
        : `${Math.round(m / 1440)} d`;
};

/** The box's keys the picker takes while open (focus stays in the box). */
export type AtKey = "up" | "down" | "enter";

/** The @ picker (frame 12c): elements first, then Gallery takes and uploads, newest first; typing searches all
 * three. ↑↓ move, Enter or a click picks; the takes are windowed (item 32). The box passes its keys through `keysRef`. */
export function AtPicker({
  q,
  keysRef,
  onPick,
}: {
  q: string;
  keysRef: React.RefObject<((k: AtKey) => void) | null>;
  onPick: (p: Pick) => void;
}) {
  const lib = useLibrary();
  const needle = q.toLowerCase();
  const elements = (lib ?? [])
    .filter(
      (x) =>
        x.handle &&
        (x.handle.toLowerCase().includes(needle) || x.name.toLowerCase().includes(needle)),
    )
    .slice(0, 4);
  const { takes: made, loading, sentinel } = useTakePages(ALL, q, undefined, 40);
  const ups = useTakePages(UPLOADS, q, undefined, 5).takes;
  const takes = (made ?? []).filter((t) => t.source !== "upload");
  const uploads = (ups ?? []).slice(0, 5);
  const all: Pick[] = [
    ...elements.map((x) => ({ kind: "element" as const, handle: x.handle! })),
    ...takes.map((t) => ({ kind: "take" as const, take: t })),
    ...uploads.map((t) => ({ kind: "take" as const, take: t })),
  ];
  const [at, setAt] = useState(0);
  const list = useRef<HTMLDivElement>(null);
  const win = useGridWindow(list, takes.length);
  const cur = Math.min(at, Math.max(0, all.length - 1));
  useEffect(() => {
    keysRef.current = (k) => {
      if (k === "enter") return all[cur] && onPick(all[cur]);
      setAt(Math.max(0, Math.min(all.length - 1, cur + (k === "down" ? 1 : -1))));
    };
  });
  useEffect(() => {
    document.getElementById(`at-${cur}`)?.scrollIntoView({ block: "nearest" });
  }, [cur]);
  const row = (p: Pick, i: number) => {
    const t = p.kind === "take" ? p.take : null;
    const el = p.kind === "element" ? elements.find((x) => x.handle === p.handle) : null;
    const thumb = safeUrl(t ? (t.kind === "audio" ? null : t.thumb_url) : el?.thumb_url);
    return (
      <button
        key={t?.id ?? `@${p.kind === "element" ? p.handle : ""}`}
        id={`at-${i}`}
        type="button"
        role="option"
        aria-selected={i === cur}
        className={`pr${i === cur ? " on" : ""}`}
        // the box keeps focus
        onMouseDown={(e) => e.preventDefault()}
        onClick={() => onPick(p)}
      >
        <i
          className="pic"
          style={{
            ...(thumb ? { backgroundImage: `url("${thumb}")` } : {}),
            ...(el ? { width: 26 } : {}),
          }}
        />
        <span>{t ? t.title : `@${p.kind === "element" ? p.handle : ""}`}</span>
        <small>{t ? age(t.created_at) : "element"}</small>
      </button>
    );
  };
  const n = elements.length;
  return (
    <div className="pop at" role="listbox" aria-label="Pick a take or element">
      <div className="atq">
        <Search size={13} strokeWidth={2} aria-hidden />
        <span>{q || "Type to search"}</span>
      </div>
      {elements.length > 0 && <h4>Elements</h4>}
      {elements.map((_, i) => row(all[i], i))}
      {takes.length > 0 && <h4>Takes · newest first</h4>}
      <div
        ref={list}
        style={{ display: "grid", gap: 2, paddingTop: win.padTop, paddingBottom: win.padBottom }}
      >
        {takes
          .slice(win.start, win.end)
          .map((_, j) => row(all[n + win.start + j], n + win.start + j))}
      </div>
      <div ref={sentinel} />
      {uploads.length > 0 && <h4>Uploads</h4>}
      {uploads.map((_, j) => row(all[n + takes.length + j], n + takes.length + j))}
      {all.length === 0 && !loading && <span className="pr">Nothing matches</span>}
    </div>
  );
}

/** An attached image: its picture and name; in the box with × to remove it (frame 12e). */
export function AttChip({
  name,
  url,
  busy,
  onRemove,
}: {
  name: string;
  url?: string | null;
  busy?: boolean;
  onRemove?: () => void;
}) {
  return (
    <span className="att-chip" aria-busy={busy || undefined}>
      <i className="pic" style={url ? { backgroundImage: `url("${url}")` } : undefined} />
      <span className="truncate">{busy ? `${name} · uploading…` : name}</span>
      {onRemove && (
        <button type="button" className="x" aria-label="Remove the image" onClick={onRemove}>
          <X size={11} strokeWidth={2} aria-hidden />
        </button>
      )}
    </span>
  );
}

/** An attachment on your sent message: the take's picture and title. */
export function SentAtt({ id }: { id: string }) {
  const take = useTakes([id])(id);
  return (
    <AttChip
      name={take === null ? "deleted image" : (take?.title ?? "…")}
      url={take ? safeUrl(take.thumb_url) : null}
    />
  );
}
