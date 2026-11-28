"use client";
// Plan 21 25.3-25.5 (docs/mock/Main.dc.html "Other states"): the session menu (the title chip's ⌄) with Rename in
// place and Delete behind its card, the "named by …" hint under the chip, a strip tile's ⋯, and the delete card
// a take, a text answer and a session share.
import { useEffect, useRef, useState } from "react";
import { ChevronDown, FolderOpen, Pencil, Plus, Trash2 } from "lucide-react";
import { openMenu } from "@/components/context-menu";
import { Modal } from "@/components/kit";
import { Marked, NoMatch, Older, SearchField, useSearch, useSlashOpens } from "@/components/search";
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

/** The title chip, the hint under it, and the session menu: the SearchField (past 8 sessions or with older ones,
 * docs/mock/Search.dc.html frame 1), New session, the sessions (Rename, Delete; greyed while one of its runs is going),
 * older matches from the server, Load older, and with a remote namer the line that says so. */
export function SessionMenu({
  sessions,
  next = null,
  current,
  naming,
  hint,
  onOpen,
  onFound,
  onOlder,
  onNew,
  onRenamed,
  onDeleted,
  onError,
}: {
  sessions: Session[] | null;
  /** the list's next page (GET /v1/studio/sessions' next_cursor): Load older shows */
  next?: string | null;
  current: string | null;
  naming?: Naming;
  /** the model that just named the open session, shown once (Create takes it away after a few seconds) */
  hint: string;
  onOpen: (id: string) => void;
  /** an older session the search found, just before it opens (not in `sessions` yet) */
  onFound?: (s: Session) => void;
  /** Load older: the next page and its cursor */
  onOlder?: (more: Session[], next: string | null) => void;
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
  const pop = useRef<HTMLDivElement>(null);
  const hide = () => pop.current?.hidePopover?.();
  const pick = (x: Session) => {
    hide();
    if (x.id === current) return;
    if (!sessions?.some((y) => y.id === x.id)) onFound?.(x);
    onOpen(x.id);
  };
  const search = useSearch({
    items: sessions,
    text: (x) => [x.title],
    id: (x) => x.id,
    more: !!next,
    server: (q, cursor) =>
      ok(
        api.GET("/v1/studio/sessions", {
          params: { query: { q, cursor: cursor ?? undefined, limit: 50 } },
        }),
      ).then((p) => ({ items: p.sessions, next: p.next_cursor })),
    onPick: pick,
    onError,
  });
  const [older, setOlder] = useState(false);
  async function loadOlder() {
    setOlder(true);
    try {
      const p = await ok(
        api.GET("/v1/studio/sessions", {
          params: { query: { cursor: next ?? undefined, limit: 50 } },
        }),
      );
      onOlder?.(p.sessions, p.next_cursor);
    } catch (e) {
      onError(message(e));
    } finally {
      setOlder(false);
    }
  }
  // opening focuses the field; closing clears it
  const { setQ } = search;
  useEffect(() => {
    const el = pop.current;
    const onToggle = (e: Event) => {
      if ((e as ToggleEvent).newState === "open")
        el?.querySelector<HTMLInputElement>("input[type=search]")?.focus();
      else setQ("");
    };
    el?.addEventListener("toggle", onToggle);
    return () => el?.removeEventListener("toggle", onToggle);
  }, [setQ]);
  const readBusy = () =>
    ok(api.GET("/v1/studio/queue"))
      .then((q) =>
        setBusy(new Set(q.runs.filter((r) => r.purpose !== "name").map((r) => r.session_id))),
      )
      .catch(() => undefined);
  // / on Create opens the menu with the field focused
  useSlashOpens(() => {
    void readBusy();
    try {
      pop.current?.showPopover?.();
    } catch {
      /* already open (its field hidden: 8 sessions or fewer) */
    }
  });
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
  const row = (s: Session) => {
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
                onPick: () => pick(s),
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
          id={search.rowId(s)}
          aria-current={here || undefined}
          className={`min-w-0 flex-1 truncate rounded-[10px] px-3 py-2 text-left text-[13px] hover:bg-s3 aria-[current]:bg-s3 aria-[current]:font-semibold ${search.rows[search.active] === s ? "sf-on" : ""}`}
          onClick={() => pick(s)}
        >
          <Marked text={s.title} q={search.q} />
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
  };
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
        ref={pop}
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
        <div className="flex max-h-[60vh] flex-col gap-0.5 [&>*]:shrink-0">
          {search.show && (
            <SearchField {...search.field} label="Search sessions" className="mb-1" />
          )}
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
          <div className="flex min-h-0 shrink! flex-col gap-0.5 overflow-y-auto [&>*]:shrink-0">
            {search.q && !search.rows.length && !search.searching && (
              <NoMatch what="sessions" onClear={() => search.setQ("")} />
            )}
            {search.shown.map((s) => row(s))}
            {search.q && (search.searching || search.older.length > 0) && (
              <Older
                heading="From older sessions"
                what="older sessions"
                searching={search.searching}
                more={search.olderNext}
                onMore={search.moreOlder}
              >
                {search.older.map((s) => row(s))}
              </Older>
            )}
            {!search.q && next && (
              <div className="mt-1 flex items-center justify-between gap-2 border-t border-s5 pt-1.5 pl-3">
                <span className="mono text-[11px] text-t2">{sessions?.length} newest shown</span>
                <button
                  type="button"
                  className="chip h-7 rounded-lg text-[12px]"
                  disabled={older}
                  onClick={() => void loadOlder()}
                >
                  Load older
                </button>
              </div>
            )}
          </div>
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
