"use client";
// Plan 21 25.3-25.5 (docs/mock/Main.dc.html "Other states"): the session menu (the title chip's ⌄) with Rename in
// place and Delete behind its card, the "named by …" hint under the chip, a strip tile's ⋯, and the delete card
// a take, a text answer and a session share.
import { useState } from "react";
import { ChevronDown, FolderOpen, Pencil, Plus, Trash2 } from "lucide-react";
import { openMenu } from "@/components/context-menu";
import { Modal } from "@/components/kit";
import { api, message, ok, type S } from "@/lib/api";

type Session = S<"StudioSession">;
type Naming = S<"SessionList">["naming"];

/** A delete card: Cancel / Delete (the boards' "Delete this take?"). */
export function DeleteCard({
  title,
  text,
  onCancel,
  onDelete,
}: {
  /** null: closed */
  title: string | null;
  text: string;
  onCancel: () => void;
  onDelete: () => void;
}) {
  return (
    <Modal open={!!title} onClose={onCancel} title={title ?? ""} width="w-[min(92vw,400px)]">
      <p className="mb-4 text-[13px] text-t2">{text}</p>
      <div className="flex justify-end gap-2">
        <button type="button" className="chip bevel" onClick={onCancel}>
          Cancel
        </button>
        <button
          type="button"
          className="chip bevel bg-a1 font-semibold text-on1"
          onClick={onDelete}
        >
          Delete
        </button>
      </div>
    </Modal>
  );
}

/** A strip tile's ⋯ (on hover and on the current tile): opens the tile's menu, as its right-click does. */
export function TileMenu({
  label,
  current,
  onMenu,
}: {
  /** "Take seed 43" */
  label: string;
  current: boolean;
  onMenu: (e: React.MouseEvent) => void;
}) {
  return (
    <button
      type="button"
      aria-haspopup="menu"
      aria-label={`${label}: more`}
      onClick={onMenu}
      className={`absolute top-[3px] right-[3px] h-[18px] w-[22px] rounded-md bg-s2/90 p-0 text-[12px] leading-[18px] text-t1 group-hover:opacity-100 focus-visible:opacity-100 ${current ? "" : "opacity-0"}`}
    >
      ⋯
    </button>
  );
}

const HINT = "helm.named-hint";
/** Whether the "named by …" hint is still to show in this browser; marks it shown (it shows once). */
export function firstHint(): boolean {
  try {
    if (localStorage.getItem(HINT)) return false;
    localStorage.setItem(HINT, "1");
    return true;
  } catch {
    return false; // private window: no hint rather than one every time
  }
}

/** The title chip, the hint under it, and the session menu: New session, the sessions (Rename, Delete; greyed while
 * one of its runs is going), and with a remote namer the line that says so. */
export function SessionMenu({
  sessions,
  current,
  naming,
  hint,
  onOpen,
  onNew,
  onRenamed,
  onDeleted,
  onError,
}: {
  sessions: Session[] | null;
  current: string | null;
  naming?: Naming;
  /** the model that just named the open session, shown once (Create takes it away after a few seconds) */
  hint: string;
  onOpen: (id: string) => void;
  onNew: () => void;
  onRenamed: (s: Session) => void;
  onDeleted: (id: string) => void;
  onError: (e: string) => void;
}) {
  // the sessions with a queued or running run (GET /v1/studio/queue, read as the menu opens; a naming run does not
  // count); an active batch is caught by the delete's 409
  const [busy, setBusy] = useState<Set<string>>(new Set());
  const [renaming, setRenaming] = useState<string | null>(null);
  const [draft, setDraft] = useState("");
  const [deleting, setDeleting] = useState<Session | null>(null);
  const session = sessions?.find((s) => s.id === current);
  const hide = () => document.getElementById("sessions")?.hidePopover?.();
  const readBusy = () =>
    ok(api.GET("/v1/studio/queue"))
      .then((q) =>
        setBusy(new Set(q.runs.filter((r) => r.purpose !== "name").map((r) => r.session_id))),
      )
      .catch(() => undefined);
  async function rename(s: Session) {
    const title = draft.trim().slice(0, 200);
    setRenaming(null);
    if (!title || title === s.title) return; // blank keeps the old title
    try {
      onRenamed(
        await ok(
          api.PATCH("/v1/studio/sessions/{session_id}", {
            params: { path: { session_id: s.id } },
            body: { title },
          }),
        ),
      );
    } catch (e) {
      onError(message(e));
    }
  }
  async function remove(s: Session) {
    setDeleting(null);
    try {
      await ok(
        api.DELETE("/v1/studio/sessions/{session_id}", { params: { path: { session_id: s.id } } }),
      );
      onDeleted(s.id);
    } catch (e) {
      onError(message(e)); // a run started since the menu opened: its 409 detail
    }
  }
  const small = "shrink-0 rounded-lg px-2 py-1 text-[12px] text-t2 hover:bg-s3 hover:text-t1";
  return (
    <>
      <div className="flex min-w-0 flex-col items-start gap-0.5">
        <button
          type="button"
          popoverTarget="sessions"
          aria-label={`Session: ${session?.title ?? "new"}. Change session`}
          className="flex h-10 max-w-[40ch] min-w-0 items-center gap-2 rounded-[10px] px-3 font-display text-lg font-bold"
          style={{ anchorName: "--sessions" } as React.CSSProperties}
          onClick={() => void readBusy()}
        >
          <span className="truncate">{session?.title ?? "New session"}</span>
          <ChevronDown size={16} strokeWidth={2} aria-hidden className="shrink-0" />
        </button>
        {hint && (
          <span
            role="status"
            className={`fade-late ml-3 rounded-full bg-s2 px-2 py-0.5 text-[11px] ${
              naming?.remote && naming.named_by === hint ? "text-[#f2c14e]" : "text-t2"
            }`}
          >
            named by {hint}
          </span>
        )}
      </div>
      <div
        id="sessions"
        popover="auto"
        role="dialog"
        aria-label="Sessions"
        className="pop w-[min(380px,calc(100vw-32px))]"
        style={
          {
            positionAnchor: "--sessions",
            positionArea: "bottom span-right",
            marginTop: 6,
          } as React.CSSProperties
        }
      >
        <div className="flex max-h-[60vh] flex-col gap-0.5 overflow-y-auto [&>*]:shrink-0">
          <button
            type="button"
            className="chip bevel mb-1 justify-center rounded-[10px] bg-s4"
            onClick={() => {
              hide();
              onNew();
            }}
          >
            <Plus size={14} aria-hidden /> New session
          </button>
          {sessions?.map((s) => {
            const here = s.id === current;
            const going = busy.has(s.id);
            if (renaming === s.id)
              return (
                <form
                  key={s.id}
                  className="flex items-center gap-1.5 rounded-[10px] bg-s4 p-1"
                  onSubmit={(e) => {
                    e.preventDefault();
                    void rename(s);
                  }}
                >
                  <label htmlFor="session-title" className="sr-only">
                    Session title
                  </label>
                  <input
                    id="session-title"
                    className="h-8 min-w-0 flex-1 rounded-lg border border-a1 bg-[var(--in)] px-2.5 text-[13px] font-semibold text-t1"
                    autoFocus
                    autoComplete="off"
                    maxLength={200}
                    value={draft}
                    onFocus={(e) => e.currentTarget.select()}
                    onChange={(e) => setDraft(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key !== "Escape") return;
                      e.preventDefault(); // Esc cancels the rename, not the menu
                      e.stopPropagation();
                      setRenaming(null);
                    }}
                  />
                  <button
                    type="submit"
                    className="h-8 shrink-0 rounded-lg bg-a1 px-3 text-[12px] font-semibold text-on1"
                  >
                    Save
                  </button>
                </form>
              );
            return (
              <div
                key={s.id}
                className="group flex items-center gap-1"
                // its row's buttons as a menu (ContextMenus frame 5)
                onContextMenu={(e) =>
                  openMenu(e, {
                    title: s.title,
                    items: [
                      {
                        label: "Open",
                        icon: FolderOpen,
                        onPick: () => (hide(), !here && onOpen(s.id)),
                      },
                      {
                        label: "Rename",
                        icon: Pencil,
                        onPick: () => (setDraft(s.title), setRenaming(s.id)),
                      },
                      "-",
                      {
                        label: "Delete",
                        icon: Trash2,
                        danger: true,
                        off: going ? "A run is going: stop it first to delete" : undefined,
                        onPick: () => (hide(), setDeleting(s)),
                      },
                    ],
                  })
                }
              >
                <button
                  type="button"
                  aria-current={here || undefined}
                  className="min-w-0 flex-1 truncate rounded-[10px] px-3 py-2 text-left text-[13px] hover:bg-s3 aria-[current]:bg-s3 aria-[current]:font-semibold"
                  onClick={() => {
                    hide();
                    if (!here) onOpen(s.id);
                  }}
                >
                  {s.title}
                </button>
                {going && !here && (
                  <span
                    className="px-2 py-1 text-[11px] text-t2 group-focus-within:hidden group-hover:hidden"
                    title="A run is going: stop it first to delete"
                  >
                    running
                  </span>
                )}
                <span
                  className={`${here ? "flex" : "hidden group-focus-within:flex group-hover:flex"} shrink-0`}
                >
                  <button
                    type="button"
                    aria-label={`Rename ${s.title}`}
                    className={small}
                    onClick={() => {
                      setDraft(s.title);
                      setRenaming(s.id);
                    }}
                  >
                    Rename
                  </button>
                  <button
                    type="button"
                    aria-label={`Delete ${s.title}`}
                    aria-disabled={going || undefined}
                    title={going ? "Stop its runs first" : undefined}
                    className={`${small} aria-disabled:cursor-not-allowed aria-disabled:opacity-45`}
                    onClick={() => {
                      if (going) return;
                      hide();
                      setDeleting(s);
                    }}
                  >
                    Delete
                  </button>
                </span>
              </div>
            );
          })}
          {naming?.remote && naming.named_by && (
            <span className="mt-1 border-t border-s5 px-3 pt-2 pb-1 text-[12px] text-[#f2c14e]">
              New sessions are named by {naming.named_by} from their first prompt.
            </span>
          )}
        </div>
      </div>
      <DeleteCard
        title={deleting && "Delete this session?"}
        text={`${deleting?.title}: its runs go. Its takes stay in the gallery. This can't be undone.`}
        onCancel={() => setDeleting(null)}
        onDelete={() => deleting && void remove(deleting)}
      />
    </>
  );
}
