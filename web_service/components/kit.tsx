"use client";
// Small shared pieces: the logo, the theme select, the info button, error notes, a modal dialog and an upload's state.
import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { useTheme } from "next-themes";
import { ChevronLeft, X } from "lucide-react";
import { THEMES } from "@/lib/themes";
import { progressText, type UploadProgress } from "@/lib/takes";

/** The helmstudio mark on its accent gradient. */
export function Logo({ size = 44 }: { size?: number }) {
  return (
    <span
      aria-hidden
      className="grid shrink-0 place-items-center text-on1"
      style={{
        width: size,
        height: size,
        borderRadius: "14px 14px 14px 6px",
        background: "linear-gradient(155deg, var(--a1), var(--a2))",
        boxShadow:
          "inset 0 1px 0 rgba(255,255,255,.5), inset 0 -4px 6px color-mix(in srgb, var(--shd) 35%, transparent), 0 10px 18px -6px color-mix(in srgb, var(--shd) 80%, transparent)",
      }}
    >
      <svg
        width={size / 2}
        height={size / 2}
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
      >
        <circle cx="12" cy="12" r="7" />
        <circle cx="12" cy="12" r="2" />
        <path d="M12 2v5M12 17v5M2 12h5M17 12h5" />
      </svg>
    </span>
  );
}

/** The theme dropdown; next-themes keeps the choice in localStorage. Empty until mounted (the server can't know). */
export function ThemeSelect({ id, className }: { id: string; className?: string }) {
  const { theme, setTheme } = useTheme();
  const [mounted, setMounted] = useState(false);
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- the stored theme is only known in the browser
    setMounted(true);
  }, []);
  return (
    <select
      id={id}
      className={className ?? "in"}
      value={mounted ? theme : "forest"}
      onChange={(e) => setTheme(e.target.value)}
    >
      {THEMES.map((t) => (
        <option key={t.id} value={t.id}>
          {t.label}
        </option>
      ))}
    </select>
  );
}

/** A small "i" button whose popover holds an explanation, so the screen itself stays short. */
export function Info({
  children,
  label = "More information",
}: {
  children: ReactNode;
  label?: string;
}) {
  const id = "info" + useId().replace(/[^a-zA-Z0-9]/g, "");
  return (
    <>
      <button
        type="button"
        className="info ml-1.5"
        aria-label={label}
        popoverTarget={id}
        style={{ anchorName: `--${id}` } as React.CSSProperties}
      >
        i
      </button>
      {/* a span, not a div: callers put Info inside <p> and headings, where a div breaks the parse (hydration #418) */}
      <span
        id={id}
        popover="auto"
        className="pop w-max max-w-72 text-[13px] font-normal tracking-normal text-t1"
        style={
          {
            positionAnchor: `--${id}`,
            positionArea: "bottom span-right",
            marginTop: 6,
          } as React.CSSProperties
        }
      >
        {children}
      </span>
    </>
  );
}

/** An error message with an optional retry. */
export function ErrorNote({ children, onRetry }: { children: ReactNode; onRetry?: () => void }) {
  return (
    <p
      role="alert"
      className="flex flex-wrap items-center gap-3 rounded-xl bg-s2 px-3 py-2 text-[13px] text-bad"
    >
      {children}
      {onRetry && (
        <button type="button" className="chip h-7 text-t1" onClick={onRetry}>
          Retry
        </button>
      )}
    </p>
  );
}

/** A modal <dialog>, open while `open`; Escape, the × button or a click on the backdrop call `onClose`. */
export function Modal({
  open,
  onClose,
  title,
  width = "w-[min(92vw,380px)]",
  back,
  children,
}: {
  open: boolean;
  onClose: () => void;
  title: string;
  /** the dialog's width class */
  width?: string;
  /** a "Back" button before the title (a dialog's second view, docs/mock/VoicePicker.dc.html) */
  back?: () => void;
  children: ReactNode;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const d = ref.current;
    if (open && d && !d.open) {
      d.showModal();
      // showModal focuses the first button, the ×, and Safari rings it: keep the focus on the dialog instead
      if (document.activeElement === d.querySelector("[data-close]")) d.focus();
    }
    if (!open && d?.open) d.close();
  }, [open]);
  return (
    <dialog
      ref={ref}
      // only this dialog's own close: React bubbles a nested dialog's (a picker inside an editor) up its tree
      onClose={(e) => e.target === e.currentTarget && onClose()}
      onClick={(e) => {
        // the backdrop's clicks land on the dialog itself, outside its box (its padding is inside)
        const d = e.currentTarget;
        const r = d.getBoundingClientRect();
        const out =
          e.clientX < r.left || e.clientX > r.right || e.clientY < r.top || e.clientY > r.bottom;
        if (e.target === d && out) d.close();
      }}
      aria-label={title}
      tabIndex={-1}
      className={`pop m-auto ${width} p-5 text-t1 outline-none backdrop:bg-black/50`}
    >
      <div className="mb-4 flex h-7 items-center justify-between gap-2">
        {back && (
          <button
            type="button"
            aria-label="Back"
            onClick={back}
            className="grid size-8 shrink-0 place-items-center rounded-lg bg-s3 text-t2"
          >
            <ChevronLeft size={16} aria-hidden />
          </button>
        )}
        <h2 className="m-0 mr-auto font-display text-base font-bold">{title}</h2>
        <button
          type="button"
          aria-label="Close"
          data-close
          onClick={() => ref.current?.close()}
          className="grid size-7 shrink-0 place-items-center rounded-lg hover:bg-s3"
        >
          <X size={16} aria-hidden />
        </button>
      </div>
      {open && children}
    </dialog>
  );
}

/** A pending upload's state (docs/mock/UploadOptimise.dc.html): "Optimising…" (an image), "Optimising 62%" (audio) or
 * "Uploading 48%", the bar while a fraction is known, and the mono line ("12.4 → 1.9 MB · WebP", "as is"). */
export function UploadStatus({ p, name }: { p: UploadProgress; name: string }) {
  const pct = p.f === undefined ? undefined : Math.round(p.f * 100);
  return (
    <>
      <span aria-live="polite" className="text-t1">
        {progressText(p)}
      </span>
      {pct !== undefined && (
        <span
          role="progressbar"
          aria-label={`${p.step === "uploading" ? "Uploading" : "Optimising"} ${name}`}
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={pct}
          className="block h-1 w-full overflow-hidden rounded-[2px] bg-inset"
        >
          <span className="block h-full bg-a2" style={{ width: `${pct}%` }} />
        </span>
      )}
      {p.line && <span className="mono text-[10px] text-t2">{p.line}</span>}
    </>
  );
}
