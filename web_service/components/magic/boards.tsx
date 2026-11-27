"use client";
// Magic's board switcher (docs/mock/Search.dc.html frame 2, owner 2026-11-27 "approved"): the board name opens a
// popover with the SearchField (past 8 boards), New board and the boards; each row has its right-click menu (Open,
// Rename, Delete). A right-click on the name keeps the board's own menu (New board, Sample board, Rename, Delete).
import { useEffect, useRef } from "react";
import { ChevronDown, FolderOpen, Pencil, Plus, Trash2 } from "lucide-react";
import { openMenu, type Item } from "@/components/context-menu";
import type { Board, BoardRow } from "@/components/magic/use-board";
import { Marked, NoMatch, SearchField, useSearch, useSlashOpens } from "@/components/search";
import { when } from "@/lib/format";

export function BoardSwitcher({ board }: { board: Board }) {
  const pop = useRef<HTMLDivElement>(null);
  const hide = () => pop.current?.hidePopover?.();
  const pick = (b: BoardRow) => {
    hide();
    if (b.id !== board.id) board.switchTo(b.id);
  };
  const search = useSearch({
    items: board.boards,
    text: (b) => [b.name],
    id: (b) => b.id,
    onPick: pick,
  });
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
  useSlashOpens(() => pop.current?.showPopover?.());

  const rename = (b: BoardRow) => {
    const name = prompt("Board name", b.name)?.trim();
    if (name && name !== b.name) board.rename(name, b.id);
  };
  const only = board.boards.length > 1 ? undefined : "It's the only board";
  // the name's own menu: the board rows moved to the popover
  const boardMenu = (e: React.MouseEvent) =>
    openMenu(e, {
      title: board.name,
      items: [
        { label: "New board", icon: Plus, onPick: board.newBoard },
        { label: "Sample board", onPick: board.sampleBoard },
        { label: "Rename", icon: Pencil, onPick: () => rename(board) },
        "-",
        {
          label: "Delete board",
          icon: Trash2,
          danger: true,
          off: only,
          onPick: () => board.remove(),
        },
      ] as Item[],
    });
  const row = (b: BoardRow) => (
    <button
      key={b.id}
      type="button"
      aria-current={b.id === board.id || undefined}
      className={`flex flex-col items-start gap-0.5 rounded-[10px] px-3 py-1.5 text-left hover:bg-s3 aria-[current]:bg-s3 ${search.rows[search.active] === b ? "sf-on" : ""}`}
      onClick={() => pick(b)}
      onContextMenu={(e) =>
        openMenu(e, {
          title: b.name,
          items: [
            { label: "Open", icon: FolderOpen, onPick: () => pick(b) },
            { label: "Rename", icon: Pencil, onPick: () => (hide(), rename(b)) },
            "-",
            {
              label: "Delete",
              icon: Trash2,
              danger: true,
              off: only,
              onPick: () => (hide(), board.remove(b.id)),
            },
          ] as Item[],
        })
      }
    >
      <span className="w-full truncate text-[13px] font-medium">
        <Marked text={b.name} q={search.q} />
      </span>
      {b.updated_at && (
        <span className="mono text-[11px] text-t2">
          edited {when(b.updated_at)}
          {b.lanes != null && ` · ${b.lanes} ${b.lanes === 1 ? "lane" : "lanes"}`}
        </span>
      )}
    </button>
  );
  return (
    <>
      <button
        type="button"
        aria-haspopup="dialog"
        popoverTarget="magic-boards"
        onContextMenu={boardMenu}
        title={board.name}
        className="flex h-10 min-w-0 items-center gap-2 rounded-[10px] bg-transparent px-3 font-display text-lg font-bold tracking-[0.02em]"
        style={{ anchorName: "--magic-boards" } as React.CSSProperties}
      >
        <span className="truncate">{board.name}</span>
        <ChevronDown size={16} className="shrink-0" aria-hidden />
      </button>
      <div
        ref={pop}
        id="magic-boards"
        popover="auto"
        role="dialog"
        aria-label="Boards"
        className="pop w-[min(380px,calc(100vw-32px))] p-2"
        style={
          {
            positionAnchor: "--magic-boards",
            positionArea: "bottom span-right",
            marginTop: 6,
          } as React.CSSProperties
        }
      >
        <div className="flex max-h-[60vh] flex-col gap-0.5 [&>*]:shrink-0">
          {search.show && <SearchField {...search.field} label="Search boards" className="mb-1" />}
          <button
            type="button"
            className="chip bevel mb-1 justify-center rounded-[10px] bg-s4"
            onClick={() => (hide(), board.newBoard())}
          >
            <Plus size={14} aria-hidden /> New board
          </button>
          <div className="flex min-h-0 flex-col gap-0.5 overflow-y-auto [&>*]:shrink-0">
            {search.q && !search.rows.length && (
              <NoMatch what="boards" onClear={() => search.setQ("")} />
            )}
            {search.shown.map(row)}
          </div>
        </div>
      </div>
    </>
  );
}
