"use client";
// The pipeline editor's and execution view's title (owner's Q4): a button with ⌄ opening a menu with Rename and All
// pipelines (and what the page adds); Rename asks for the new title in a dialog.
import Link from "next/link";
import { useId, useState } from "react";
import { ChevronDown } from "lucide-react";
import { Modal } from "@/components/kit";
import { message } from "@/lib/api";

/** A menu item's look, for the items a page adds. */
export const MENU_ITEM = "rounded-[10px] px-3 py-2 text-left text-[13px] hover:bg-s3";

export function TitleMenu({
  title,
  onRename,
  children,
}: {
  title: string;
  /** absent: no Rename (a batch run without a saved pipeline) */
  onRename?: (title: string) => void | Promise<void>;
  /** more menu items, between Rename and All pipelines */
  children?: React.ReactNode;
}) {
  const id = useId().replace(/:/g, "");
  const anchor = `--${id}`;
  const [renaming, setRenaming] = useState<string | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const close = () => document.getElementById(id)?.hidePopover?.();

  async function submit() {
    const t = renaming?.trim();
    if (!t || !onRename) return;
    setBusy(true);
    setError("");
    try {
      if (t !== title) await onRename(t.slice(0, 80));
      setRenaming(null);
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <h1 className="m-0 min-w-0">
        <button
          type="button"
          popoverTarget={id}
          className="flex h-10 max-w-full items-center gap-2 rounded-[10px] px-3 font-display text-[18px] font-bold tracking-[0.01em] hover:bg-s2"
          style={{ anchorName: anchor } as React.CSSProperties}
        >
          <span className="truncate">{title}</span>
          <ChevronDown size={16} strokeWidth={2} aria-hidden />
        </button>
      </h1>
      <div
        id={id}
        popover="auto"
        role="menu"
        aria-label={title}
        className="pop w-60 flex-col p-1.5 open:flex"
        style={
          {
            positionAnchor: anchor,
            positionArea: "bottom span-right",
            marginTop: 6,
          } as React.CSSProperties
        }
      >
        {onRename && (
          <button
            type="button"
            role="menuitem"
            className={MENU_ITEM}
            onClick={() => {
              close();
              setError("");
              setRenaming(title);
            }}
          >
            Rename
          </button>
        )}
        {children}
        <Link role="menuitem" href="/pipelines" className={MENU_ITEM}>
          All pipelines
        </Link>
      </div>
      <Modal open={renaming !== null} onClose={() => setRenaming(null)} title="Rename pipeline">
        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            void submit();
          }}
        >
          <input
            autoFocus
            aria-label="Title"
            className="in"
            maxLength={80}
            value={renaming ?? ""}
            onChange={(e) => setRenaming(e.target.value)}
          />
          {error && <p className="m-0 text-[13px] text-bad">{error}</p>}
          <div className="flex justify-end gap-2">
            <button type="button" className="chip bevel" onClick={() => setRenaming(null)}>
              Cancel
            </button>
            <button
              type="submit"
              className="chip bevel bg-a1 font-semibold text-on1"
              disabled={busy || !renaming?.trim()}
            >
              Rename
            </button>
          </div>
        </form>
      </Modal>
    </>
  );
}
