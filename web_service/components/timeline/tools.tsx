"use client";
// The Timeline's tool row (docs/mock/TimelineTools.dc.html, option B; plan 23 §27.10): the segmented tool picker
// (icon, short name, key; a radio group with one tab stop, arrows move inside), the snap and hover-scrub toggles, the
// zoom field with −, + and Fit on the right, and the ? button; the shortcut sheet (the board's dialog); Slip's source
// window (the shared state "Slip with the source window").
import { useEffect, useRef } from "react";
import { Modal } from "@/components/kit";
import { TOOLS, type Tool } from "@/lib/timeline-tools";
import { timecode } from "@/lib/timeline";

const Svg = ({ size = 16, children }: { size?: number; children: React.ReactNode }) => (
  <svg
    aria-hidden
    width={size}
    height={size}
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    strokeWidth="1.75"
    strokeLinecap="round"
    strokeLinejoin="round"
  >
    {children}
  </svg>
);

/** The board's tool icons. */
export const ICONS: Record<Tool, React.ReactNode> = {
  select: <path d="M6 3l12 8-5.5 1.5L10 18z" />,
  trim: <path d="M9 4H6v16h3M15 4h3v16h-3" />,
  blade: (
    <>
      <path d="M8 3h8l-4 4z" />
      <path d="M12 9v12" strokeDasharray="2 2" />
    </>
  ),
  range: (
    <>
      <path d="M4 6v12M20 6v12M4 12h16" />
      <path d="m8 9-3 3 3 3M16 9l3 3-3 3" />
    </>
  ),
  overwrite: (
    <>
      <rect x="7" y="7" width="10" height="10" rx="2" />
      <path d="M2 12h3M19 12h3M12 2v3M12 19v3" />
    </>
  ),
};
export const ToolIcon = ({ tool, size }: { tool: Tool; size?: number }) => (
  <Svg size={size}>{ICONS[tool]}</Svg>
);
export const MagnetIcon = ({ size }: { size?: number }) => (
  <Svg size={size}>
    <path d="M6 4v8a6 6 0 0 0 12 0V4" />
    <path d="M6 8h4M14 8h4" />
  </Svg>
);

export function ToolRow({
  tool,
  onTool,
  snap,
  onSnap,
  scrub,
  onScrub,
  zoom,
  onZoomOut,
  onZoomIn,
  onFit,
  onSheet,
}: {
  tool: Tool;
  onTool: (t: Tool) => void;
  snap: boolean;
  onSnap: () => void;
  scrub: boolean;
  onScrub: () => void;
  /** the zoom field's text ("22 s", "Fit · 3:45") */
  zoom: string;
  onZoomOut: () => void;
  onZoomIn: () => void;
  onFit: () => void;
  onSheet: () => void;
}) {
  const group = useRef<HTMLDivElement>(null);
  const arrow = (e: React.KeyboardEvent) => {
    const step = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }[e.key];
    if (!step) return;
    e.preventDefault();
    e.stopPropagation(); // the Timeline's own arrows step the playhead
    const i = TOOLS.findIndex((t) => t.id === tool);
    const next = TOOLS[(i + step + TOOLS.length) % TOOLS.length].id;
    onTool(next);
    group.current?.querySelector<HTMLElement>(`[data-tool="${next}"]`)?.focus();
  };
  return (
    <div className="tl-bar">
      <div ref={group} role="radiogroup" aria-label="Tool" className="tl-seg" onKeyDown={arrow}>
        {TOOLS.map((t) => (
          <button
            key={t.id}
            type="button"
            role="radio"
            data-tool={t.id}
            aria-checked={tool === t.id}
            aria-label={t.name}
            aria-keyshortcuts={t.key}
            title={`${t.name} (${t.key})`}
            tabIndex={tool === t.id ? 0 : -1}
            className="tl-tb"
            onClick={() => onTool(t.id)}
          >
            <ToolIcon tool={t.id} />
            <span>{t.name}</span>
            <kbd>{t.key}</kbd>
          </button>
        ))}
      </div>
      <span className="tl-sep" />
      <button
        type="button"
        className="tl-tg"
        aria-pressed={snap}
        aria-label="Snapping"
        aria-keyshortcuts="N"
        title="Snapping (N)"
        onClick={onSnap}
      >
        <MagnetIcon />
      </button>
      <button
        type="button"
        className="tl-tg"
        aria-pressed={scrub}
        aria-label="Hover scrub"
        aria-keyshortcuts="Shift+S"
        title="Hover scrub (⇧S)"
        onClick={onScrub}
      >
        <Svg>
          <path d="M12 3v18" />
          <path d="M3 12c3-4 6-6 9-6s6 2 9 6c-3 4-6 6-9 6s-6-2-9-6z" opacity=".55" />
        </Svg>
      </button>
      <span className="grow" />
      <div role="group" aria-label="Zoom" className="tl-zf">
        <button
          type="button"
          aria-label="Zoom out"
          aria-keyshortcuts="Meta+-"
          title="Zoom out (⌘−)"
          onClick={onZoomOut}
        >
          <Svg>
            <path d="M5 12h14" />
          </Svg>
        </button>
        <span className="tl-zv" aria-live="polite">
          {zoom}
        </span>
        <button
          type="button"
          aria-label="Zoom in"
          aria-keyshortcuts="Meta+="
          title="Zoom in (⌘=)"
          onClick={onZoomIn}
        >
          <Svg>
            <path d="M5 12h14M12 5v14" />
          </Svg>
        </button>
        <button
          type="button"
          className="fit"
          aria-label="Zoom to fit"
          aria-keyshortcuts="Shift+Z"
          title="Fit (⇧Z)"
          onClick={onFit}
        >
          <Svg size={14}>
            <path d="M4 9V4h5M20 9V4h-5M4 15v5h5M20 15v5h-5" />
          </Svg>
          <span>Fit</span>
        </button>
      </div>
      <button
        type="button"
        className="tl-tg"
        aria-haspopup="dialog"
        aria-label="Keyboard shortcuts"
        aria-keyshortcuts="Shift+/"
        title="Shortcuts (?)"
        onClick={onSheet}
      >
        ?
      </button>
    </div>
  );
}

/** The shortcut sheet's groups, as the board lists them (plus M, markers: owner point 6). */
export const SHEET: [string, [string, string][]][] = [
  [
    "Tools",
    [
      ["A", "Select"],
      ["T", "Trim · roll · slip (⌥ slide)"],
      ["B", "Blade"],
      ["R", "Range"],
      ["P", "Overwrite"],
      ["Esc", "Back to Select"],
    ],
  ],
  [
    "Edit",
    [
      ["⌘B", "Blade at playhead"],
      ["⇧⌘B", "Blade all tracks"],
      ["S", "Split at playhead"],
      ["Delete", "Ripple delete"],
      ["⇧Delete", "Lift"],
      ["[ ]", "Trim a frame"],
      ["⌥[ ⌥]", "Trim to playhead"],
      ["⌥, ⌥.", "Roll a frame"],
      [", .", "Slip a frame"],
      ["⇧, ⇧.", "Slide a frame"],
      ["M", "Marker"],
      ["⌘Z ⇧⌘Z", "Undo · Redo"],
    ],
  ],
  [
    "Play and move",
    [
      ["Space", "Play · pause"],
      ["J K L", "Back · stop · forward"],
      ["← →", "Frame (⇧ 1 s)"],
      ["↑ ↓", "Previous · next cut"],
      ["Home End", "Start · end"],
      ["I O X", "In · out · clip"],
      ["⌥X", "Clear range"],
    ],
  ],
  [
    "Zoom and view",
    [
      ["⌘= ⌘−", "Zoom in · out"],
      ["⇧Z", "Fit"],
      ["⌃ wheel", "Zoom at pointer (pinch too)"],
      ["N", "Snapping"],
      ["⇧S", "Hover scrub"],
      ["?", "This sheet"],
    ],
  ],
];

/** The shortcut sheet (? or the ? button): a modal dialog, focus on Close, Esc closes, Tab stays inside. */
export function ShortcutSheet({ open, onClose }: { open: boolean; onClose: () => void }) {
  return (
    <Modal open={open} onClose={onClose} title="Keyboard shortcuts" width="w-[min(94vw,940px)]">
      <Sheet />
    </Modal>
  );
}

function Sheet() {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    ref.current?.closest("dialog")?.querySelector<HTMLElement>("[data-close]")?.focus();
  }, []);
  return (
    <div ref={ref} className="tl-ks">
      {SHEET.map(([h, rows]) => (
        <div key={h}>
          <h3 className="heading">{h}</h3>
          <dl>
            {rows.map(([k, d]) => (
              <div key={k}>
                <dt>
                  <kbd>{k}</kbd>
                </dt>
                <dd>{d}</dd>
              </div>
            ))}
          </dl>
        </div>
      ))}
    </div>
  );
}

/** Slip's source window: the new in and out frames side by side and the part of the take used (board "Slip"). */
export function SlipSource({
  url,
  clip,
  take,
  delta,
}: {
  url?: string;
  clip: { in: number; out: number };
  /** the take's length (s) */
  take: number;
  /** the slip so far ("+12 f") */
  delta: string;
}) {
  const frame = (t: number, label: string) => (
    <span className="tl-pv">
      {url ? (
        <video
          aria-hidden
          muted
          preload="auto"
          src={`${url}#t=${t.toFixed(3)}`}
          className="size-full object-cover"
        />
      ) : (
        label
      )}
    </span>
  );
  return (
    <div className="tl-src" role="group" aria-label="Source of the clip">
      <div className="flex flex-col items-center gap-1">
        {frame(clip.in, "In frame")}
        <span className="mono text-[12px] text-t2">In {timecode(clip.in)}</span>
      </div>
      <div className="flex flex-1 flex-col items-center gap-1">
        <span className="mono text-[10px] text-t2">take {take.toFixed(1)} s</span>
        <div className="tl-take">
          <span
            className="tl-used"
            style={{
              left: `${(100 * clip.in) / take}%`,
              width: `${(100 * (clip.out - clip.in)) / take}%`,
            }}
          />
        </div>
        <span className="mono text-[10px] text-t2">{delta}</span>
      </div>
      <div className="flex flex-col items-center gap-1">
        {frame(clip.out, "Out frame")}
        <span className="mono text-[12px] text-t2">Out {timecode(clip.out)}</span>
      </div>
    </div>
  );
}
