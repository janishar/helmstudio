"use client";
// Small shared pieces: the logo, the theme select, the info button, error notes and a modal dialog.
import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { useTheme } from "next-themes";
import { X } from "lucide-react";
import { THEMES } from "@/lib/themes";

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
        className="pop max-w-72 text-[13px] font-normal tracking-normal text-t1"
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
  children,
}: {
  open: boolean;
  onClose: () => void;
  title: string;
  /** the dialog's width class */
  width?: string;
  children: ReactNode;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const d = ref.current;
    if (open && !d?.open) d?.showModal();
    if (!open && d?.open) d.close();
  }, [open]);
  return (
    <dialog
      ref={ref}
      onClose={onClose}
      onClick={(e) => {
        // the backdrop's clicks land on the dialog itself, outside its box (its padding is inside)
        const d = e.currentTarget;
        const r = d.getBoundingClientRect();
        const out =
          e.clientX < r.left || e.clientX > r.right || e.clientY < r.top || e.clientY > r.bottom;
        if (e.target === d && out) d.close();
      }}
      aria-label={title}
      className={`pop m-auto ${width} p-5 text-t1 backdrop:bg-black/50`}
    >
      <div className="mb-4 flex h-7 items-center justify-between gap-2">
        <h2 className="m-0 font-display text-base font-bold">{title}</h2>
        <button
          type="button"
          aria-label="Close"
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
