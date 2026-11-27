"use client";
// The Assistant on the Magic page (plan 39 phase F, MagicBoard frame 8; contracts/README.md "Magic: the Assistant"):
// the screen's actions and summary (tools/actions.json `magic`), the Assistant's change drawn on the board from its
// board_edit card (dashed green new, dashed blue moved, faded removed) with Apply and Discard, which answer the card,
// and History: the board's kept revisions, "by Assistant" for the Assistant's, Restore rev N.
import { useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { Clock3, History as HistoryIcon, RotateCcw } from "lucide-react";
import { openMenu } from "@/components/context-menu";
import { historyMenu, proposalMenu } from "@/components/magic/menus";
import { ErrorNote, Modal } from "@/components/kit";
import { useRegistry, type ScreenSpec, type ServerCards } from "@/components/assistant/tools";
import { when } from "@/components/magic/send";
import type { Proposal } from "@/components/magic/lanes";
import { message, type S } from "@/lib/api";
import { done, failed } from "@/lib/assistant";
import { apply, secs, type Block, type Doc, type Op } from "@/lib/magic";

type Card = S<"AssistantCard">;
type Rev = S<"MagicRevisionItem">;

// ------------------------------------------------------------------ the change drawn on the board

/** The document `ops` make of `doc`, each block marked new, moved or changed, and the ones removed; or why it can't be
 * drawn (an op this board refuses: the board moved on since the Assistant tried it). */
export function proposalOf(doc: Doc, ops: Op[]): Proposal | { error: string } {
  let next = doc;
  for (const op of ops) {
    const r = apply(next, op);
    if (!("doc" in r)) return { error: "It no longer fits this board" };
    next = r.doc;
  }
  const marks: Proposal["marks"] = {};
  for (const b of next.blocks) {
    const was = doc.blocks.find((x) => x.id === b.id);
    if (!was) marks[b.id] = "new";
    else if (was.at !== b.at || was.lane !== b.lane) marks[b.id] = "moved";
    else if (JSON.stringify(was) !== JSON.stringify(b)) marks[b.id] = "changed";
  }
  const removed = doc.blocks.filter((b) => !next.blocks.some((x) => x.id === b.id));
  return { doc: next, marks, removed };
}

const NONE: ServerCards = { cards: [], key: "", answer: () => undefined };
const noSub = () => () => undefined;

/** The Assistant's board_edit card for this board (if one waits), its change drawn, and its answer; the board is read
 * again whenever the chat moves on (an edit applied, a restore, runs started). */
export function useProposal(board: { id: string; doc: Doc; revision: number; reload: () => void }) {
  const reg = useRegistry();
  const server = useSyncExternalStore(
    reg ? (f: () => void) => reg.subscribe(f) : noSub,
    () => reg?.server ?? NONE,
    () => NONE,
  );
  const card =
    server.cards.find((c) => c.kind === "board_edit" && c.board?.board_id === board.id) ?? null;
  const ops = card?.board?.ops as Op[] | undefined;
  const drawn = useMemo(() => (ops?.length ? proposalOf(board.doc, ops) : null), [ops, board.doc]);
  const reload = useRef(board.reload);
  useEffect(() => {
    reload.current = board.reload;
  });
  useEffect(() => {
    if (server.key) reload.current();
  }, [server.key]);
  return {
    card,
    proposal: drawn && "doc" in drawn ? drawn : null,
    /** why it can't be drawn, or that the board changed since it was tried */
    why:
      drawn && "error" in drawn
        ? drawn.error
        : card?.board && card.board.base_revision !== board.revision
          ? "The board changed since; Apply will be refused"
          : "",
    answer: (yes: boolean) => card && server.answer(card.id, yes),
  };
}

/** "Assistant's change · Added Shot 3 · about 6 min to run · mac-studio · Discard · Apply" (frame 8's bar). */
export function ProposalBar({
  card,
  why,
  onAnswer,
}: {
  card: Card;
  why: string;
  onAnswer: (yes: boolean) => void;
}) {
  const row = (label: string) => card.rows.find((r) => r.label === label)?.value;
  const restore = !!card.board?.to;
  const [run, host] = (row("To run") ?? "").split(" · ");
  const menu = (e: React.MouseEvent) =>
    openMenu(
      e,
      proposalMenu({ apply: () => onAnswer(true), discard: () => onAnswer(false) }, card.title),
    );
  return (
    <div
      role="group"
      aria-label={card.title}
      onContextMenu={menu}
      className="sticky bottom-3 z-20 mx-auto flex max-w-full flex-wrap items-center gap-2.5 rounded-[18px_18px_18px_8px] bg-s2 py-2 pr-2 pl-4 text-[13px] shadow-[0_0_0_1.5px_var(--a1),0_24px_40px_-12px_#000]"
    >
      <span className="min-w-0">
        <b>{restore ? card.title : "Assistant's change"}</b>
        {" · "}
        {restore ? row("Revision") : row("Change")}
      </span>
      {run && (
        <span
          className="est flex items-center gap-1 text-t2"
          title="The estimate to run what it adds"
        >
          <Clock3 size={13} aria-hidden />
          <b className="text-t1">{run.replace(/^about /, "≈ ")} to run</b>
          {host && ` · ${host}`}
        </span>
      )}
      {why && <span className="text-[12px] text-[#f2c14e]">{why}</span>}
      <span className="grow" />
      <button type="button" className="chip bevel rounded-[10px]" onClick={() => onAnswer(false)}>
        Discard
      </button>
      <button
        type="button"
        className="h-8 rounded-[10px] bg-a1 px-3.5 text-[13px] font-semibold text-on1"
        onClick={() => onAnswer(true)}
      >
        Apply
      </button>
    </div>
  );
}

// ------------------------------------------------------------------ History

const capital = (s: string) => (s ? s[0].toUpperCase() + s.slice(1) : s);

/** Who made a revision, in History's words. */
export function byOf(r: Pick<Rev, "by" | "via">): { text: string; ai: boolean } {
  if (r.via === "assistant") return { text: "by Assistant", ai: true };
  return { text: r.by === "studio" ? "by studio" : "by you", ai: false };
}

/** "History": the board's kept revisions, newest first, each by you, by Assistant or by studio, and Restore. */
export function HistoryChip({
  revisions,
  revision,
  onRestore,
}: {
  revisions: (() => Promise<Rev[]>) | null;
  revision: number;
  onRestore: (revision: number) => void;
}) {
  const [open, setOpen] = useState(false);
  const [list, setList] = useState<Rev[] | null>(null);
  const [pick, setPick] = useState<number | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    if (!open || !revisions) return;
    let gone = false;
    revisions().then(
      (l) => !gone && setList(l),
      (e) => !gone && setError(message(e)),
    );
    return () => {
      gone = true;
    };
  }, [open, revisions, revision]);
  const restore = (r: Rev) => {
    setOpen(false);
    onRestore(r.revision);
  };
  const picked = pick ?? list?.find((r) => r.revision !== revision)?.revision ?? null;
  return (
    <>
      <button
        type="button"
        className="chip bevel rounded-[10px] bg-s4 max-md:hidden"
        disabled={!revisions}
        title={revisions ? undefined : "History needs a board in studio"}
        onClick={() => {
          setPick(null);
          setOpen(true);
        }}
      >
        <HistoryIcon size={13} aria-hidden />
        History
      </button>
      <Modal
        open={open}
        onClose={() => setOpen(false)}
        title="History"
        aside={<span className="text-[12px] text-t2">this board</span>}
        width="w-[min(94vw,460px)]"
      >
        <div className="flex flex-col gap-1.5" role="listbox" aria-label="Revisions">
          {error && <ErrorNote>{error}</ErrorNote>}
          {!list && !error && <span className="text-[13px] text-t2">Reading…</span>}
          {list?.map((r) => {
            const by = byOf(r);
            return (
              <button
                key={r.revision}
                type="button"
                role="option"
                aria-selected={picked === r.revision}
                onClick={() => setPick(r.revision)}
                onDoubleClick={() => r.revision !== revision && restore(r)}
                onContextMenu={(e) => openMenu(e, historyMenu({ restore }, r, revision))}
                className={`flex min-h-9 items-center gap-2 rounded-[10px] px-2.5 text-left text-[13px] ${picked === r.revision ? "bg-s4" : "bg-transparent"} ${r.revision === revision ? "shadow-[inset_0_0_0_1.5px_var(--a1)]" : ""}`}
              >
                <span
                  className={`shrink-0 rounded-full px-1.5 text-[10px] leading-4 font-semibold ${by.ai ? "bg-[color-mix(in_srgb,#2f8f6a_50%,var(--s5))] text-[#d6f5e4]" : "bg-s5 text-t1b"}`}
                >
                  {by.text}
                </span>
                <span className="min-w-0 truncate">{capital(r.ops_summary)}</span>
                <span className="mono ml-auto shrink-0 text-[11px] text-t2">
                  rev {r.revision} · {r.revision === revision ? "now" : when(r.saved_at)}
                </span>
              </button>
            );
          })}
          {picked !== null && (
            <button
              type="button"
              className="chip bevel mt-1 self-start rounded-[10px] bg-s4"
              disabled={picked === revision}
              title={picked === revision ? "It's the board now" : undefined}
              onClick={() => restore({ revision: picked } as Rev)}
            >
              <RotateCcw size={13} aria-hidden />
              Restore rev {picked}
            </button>
          )}
          <p className="m-0 text-[12px] text-t2">Restore makes a new revision; nothing is lost.</p>
        </div>
      </Modal>
    </>
  );
}

// ------------------------------------------------------------------ the screen's actions (tools/actions.json `magic`)

/** What the Magic screen registers for the Assistant: its board in a few fields and select, openBlock, play, scrub,
 * each the page's own entry point. */
export function magicTools(c: {
  id: string;
  name: string;
  revision: number;
  doc: Doc;
  sel: string[];
  frame: number;
  select: (ids: string[]) => void;
  open: (id: string) => boolean;
  play: (from?: number) => void;
  pause: () => void;
  seek: (frame: number) => void;
}): ScreenSpec {
  const fps = c.doc.target.fps;
  const name = (id: string) => c.doc.blocks.find((b) => b.id === id)?.name.split(" · ")[0];
  const at = (s: unknown) => Math.max(0, Math.round(Number(s) * fps));
  const total = Math.max(0, ...c.doc.blocks.map((b: Block) => b.at + b.length));
  return {
    screen: "magic",
    sees: ["Magic", c.name],
    ids: { board: c.id },
    summary: () => ({
      board: { id: c.id, name: c.name, revision: c.revision, fps, seconds: secs(total, fps) },
      lanes: c.doc.lanes.map((l) => ({
        id: l.id,
        name: l.name,
        role: l.role,
        blocks: c.doc.blocks.filter((b) => b.lane === l.id).length,
        ...(l.hidden ? { hidden: true } : {}),
      })),
      selection: c.sel.map((id) => ({ id, name: name(id) ?? id })),
      playhead_s: Math.round((c.frame / fps) * 100) / 100,
    }),
    actions: {
      select: (a) => {
        const ids = (a.ids as string[]).filter((id) => name(id));
        if (ids.length < (a.ids as string[]).length) return failed("Select · no such block");
        c.select(ids);
        return done(ids.length ? `Selected ${ids.map(name).join(", ")}` : "Cleared the selection");
      },
      openBlock: (a) =>
        c.open(String(a.id))
          ? done(`Opened ${name(String(a.id)) ?? "the morph"}`)
          : failed("Open · no such block"),
      play: (a) => {
        if (a.pause) {
          c.pause();
          return done("Paused");
        }
        c.play(a.at_s === undefined ? undefined : at(a.at_s));
        return done(a.at_s === undefined ? "Playing" : `Playing from ${secs(at(a.at_s), fps)}`);
      },
      scrub: (a) => {
        c.seek(at(a.at_s));
        return done(`Playhead at ${secs(at(a.at_s), fps)}`);
      },
    },
    examples: [
      { text: "Finish the story in three more shots", sub: "drawn on the board first" },
      { text: "Run what's out of date", sub: "asks above your limit" },
    ],
  };
}
