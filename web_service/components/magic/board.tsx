"use client";
// The Magic page (plan 39 phase B, MagicBoard frames 1, 2, 4, 5, 10, 11): the board name and its tools, the Assets
// row, the lanes, the floating player and the side panel. Boards live in studio (phase C, components/magic/use-board.ts);
// `local` keeps them in this browser with fake takes (the tests' fake mode). Every edit is a named op (lib/magic.ts).
import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import {
  Camera,
  Square,
  Clock3,
  Lock,
  X,
  Plus,
  RefreshCw,
  SlidersHorizontal,
  Undo2,
  Redo2,
} from "lucide-react";
import { mod, openMenu } from "@/components/context-menu";
import { ErrorNote } from "@/components/kit";
import { AddDialog, type AddAt } from "@/components/magic/add";
import { AssetDialog } from "@/components/magic/asset-dialog";
import { BoardSwitcher } from "@/components/magic/boards";
import { Marked, NoMatch, SearchField, useSearch } from "@/components/search";
import { BlockDialog, CompareDialog } from "@/components/magic/block-dialog";
import { FrameDialog } from "@/components/magic/frame-dialog";
import { ImportDialog } from "@/components/magic/import";
import { RoughCut } from "@/components/magic/rough-cut";
import {
  MorphDialog,
  ExportButton,
  SendButton,
  SendToast,
  SendsChip,
  useSends,
  type Send,
} from "@/components/magic/send";
import { GalleryPicker } from "@/components/gallery/picker";
import { useVoices } from "@/lib/library";
import { KIND_NAME, LinkGlyph } from "@/components/magic/icons";
import { SoundMark, soundLook } from "@/components/magic/sound-mark";
import { AssetsRow, Lanes, StatusChip, refText } from "@/components/magic/lanes";
import {
  EFFECTS,
  blockMenu,
  effectMenu,
  fromGallery,
  nextLaneName,
  proposalMenu,
  snapshotMenu,
  type Act,
} from "@/components/magic/menus";
import { Player, type PlayerApi } from "@/components/magic/player";
import {
  apply,
  clock,
  duration,
  end,
  freshId,
  inLane,
  overruns,
  secs,
  shown,
  statusOf,
  type Block,
  type Doc,
  type Mode,
  type Op,
  type Takes,
} from "@/lib/magic";
import { EST_HOST, EST_S, estText } from "@/lib/magic-fixture";
import { FORM_KINDS, type Estimate } from "@/lib/magic-standard";
import { LoadingBoard } from "@/components/magic/loading";
import { OUTAGE_TEXT, retryNow } from "@/lib/health";
import { useScreenTools } from "@/components/assistant/tools";
import { HistoryChip, ProposalBar, magicTools, useProposal } from "@/components/magic/assistant";
import {
  useApiBoard,
  useLocalBoard,
  type Board,
  type Snapshot,
  type Store,
} from "@/components/magic/use-board";
import "./magic.css";

const PANEL = "magic-side-panel";
/** Why runs can't go (a board with no way to run). */
export const RUNS_LATER = "Runs come with the standard request";
/** What runs: image, video and voice blocks that are not dialogues (phase D), board frames (phase E), music and sound
 * (phase H). */
const runsHere = (b: Block) =>
  (FORM_KINDS.includes(b.kind) || b.kind === "frame") && !b.request.turns?.length;
/** a Gallery pick a menu asked for */
type Pick = Parameters<NonNullable<Act["gallery"]>>[0];

const noSub = () => () => undefined;
const readPanel = () => {
  try {
    return localStorage.getItem(PANEL) !== "0";
  } catch {
    return true;
  }
};
/** The board's phone layout (the dock's own breakpoint). */
const phoneQuery = "(max-width: 47.99rem)";
const subPhone = (cb: () => void) => {
  const m = matchMedia(phoneQuery);
  m.addEventListener("change", cb);
  return () => m.removeEventListener("change", cb);
};

// ------------------------------------------------------------------------------------------------ the page

/** The Magic page: the boards in studio, or (`local`, or an `initial` store: the tests' fake mode) in this browser. */
export function MagicBoard({ initial, local }: { initial?: Store; local?: boolean }) {
  return local || initial ? <LocalBoard initial={initial} /> : <StudioBoard />;
}

function LocalBoard({ initial }: { initial?: Store }) {
  const board = useLocalBoard(initial);
  return <Editor key={board.id} board={board} sample={!initial} />;
}

function StudioBoard() {
  const board = useApiBoard();
  if (!board.ready)
    return board.error === OUTAGE_TEXT ? (
      // ErrorStates a3: over empty lanes, with Retry (the board loads by itself once the studio answers)
      <LoadingBoard
        failed={
          <>
            <p className="m-0">The board didn’t load: the studio isn’t answering.</p>
            <button
              type="button"
              className="chip h-8 bg-s4 text-t1"
              onClick={() => {
                retryNow();
                board.retry();
              }}
            >
              <RefreshCw size={14} strokeWidth={1.75} aria-hidden />
              Retry
            </button>
          </>
        }
      />
    ) : board.error ? (
      <main id="main" className="mg flex-1 px-5 pt-4">
        <ErrorNote>{board.error}</ErrorNote>
      </main>
    ) : (
      <LoadingBoard />
    );
  return <Editor key={board.id} board={board} sample={false} />;
}

function Editor({ board, sample }: { board: Board; sample: boolean }) {
  const doc = board.doc;
  const takes = board.takes;
  const fps = doc.target.fps;

  // frame 1 opens with Shot 2 selected, its details in the side panel
  const [sel, setSel] = useState<string[]>(() =>
    sample && doc.blocks.some((b) => b.id === "s2") ? ["s2"] : [],
  );
  const [mode, setMode] = useState<Mode>("insert");
  const [snapOn, setSnapOn] = useState(true);
  const [pps, setPps] = useState(26);
  const [viewW, setViewW] = useState(0);
  const [frame, setFrame] = useState(sample ? Math.round(10.5 * 24) : 0);
  const [adding, setAdding] = useState<AddAt | null>(null);
  const [opened, setOpened] = useState<string | null>(null);
  /** a block whose takes are compared side by side (API 1.89) */
  const [comparing, setComparing] = useState<string | null>(null);
  /** a block Generate made: it runs once the board has it */
  const toRun = useRef<string | null>(null);
  /** the ask card: a run of the board above the limit (plan 39 §45.8) */
  const [asking, setAsking] = useState<string | null>(null);
  /** Stop runs (N) asks first */
  const [stopAsk, setStopAsk] = useState(false);
  const [renaming, setRenaming] = useState<string | null>(null);
  const [usedAsset, setUsedAsset] = useState<string | null>(null);
  // phase E: an asset opened ("new" makes one), Import (into an asset, or a new one), the rough cut (from a frame;
  // null closed), a Gallery pick
  const [assetOpen, setAssetOpen] = useState<string | null>(null);
  const [importing, setImporting] = useState<{ asset?: string } | null>(null);
  const [rough, setRough] = useState<string | null | undefined>(null);
  const [picking, setPicking] = useState<Pick | null>(null);
  // phase G: a morph opened, the board's sends, the toast of the newest one (it follows its send while it bakes)
  const [morph, setMorph] = useState<string | null>(null);
  const sendsList = useSends(board.sends);
  const [sent, setSent] = useState<Send | null>(null);
  const sentNow = sent && sendsList.list.find((s) => s.id === sent.id);
  const toast = sentNow ?? sent;
  const voices = useVoices();
  const [kept, setKept] = useState<string[]>([]);
  const [said, setSaid] = useState("");
  const [refused, setError] = useState("");
  const error = refused || board.error;
  const [clip, setClip] = useState<Block[] | null>(null);
  const player = useRef<PlayerApi | null>(null);
  const [playing, setPlaying] = useState(false);
  const phone = useSyncExternalStore(
    subPhone,
    () => matchMedia(phoneQuery).matches,
    () => false,
  );
  // the side panel: open by default, a closed one remembered in this browser ("0"), as on Create and the Timeline
  const storedPanel = useSyncExternalStore(noSub, readPanel, () => true);
  const [panelSet, setPanelSet] = useState<boolean | null>(null);
  const panel = panelSet ?? storedPanel;
  const setPanel = (on: boolean) => {
    setPanelSet(on);
    try {
      localStorage.setItem(PANEL, on ? "1" : "0");
    } catch {}
  };

  /** One edit (one op, or several in order): applied all or nothing, one step of undo; a refusal says why. */
  // the Assistant's change, drawn on the board until Apply or Discard (plan 39 phase F, frame 8)
  const proposed = useProposal(board);
  const waiting = !!proposed.card;
  const edit = useCallback(
    (ops: Op | Op[]) => {
      if (waiting) {
        setError("Apply or Discard the Assistant's change first.");
        return false;
      }
      const list = Array.isArray(ops) ? ops : [ops];
      let d = doc;
      for (const op of list) {
        const r = apply(d, op);
        if ("error" in r) {
          setError(r.error);
          return false;
        }
        d = r.doc;
      }
      setError("");
      board.edit(list, d);
      setSaid(describe(list[0], doc));
      // what the op removed leaves the selection
      setSel((s) => s.filter((id) => d.blocks.some((b) => b.id === id)));
      return true;
    },
    [doc, board, waiting],
  );

  /** Runs blocks (fake in the fake mode; in studio, their standard requests). */
  const run = useCallback(
    (ids: string[] | "stale") => {
      if (!board.run) return setSaid(RUNS_LATER);
      const n = board.run(ids);
      if (n < 0) setSaid("Running every block that isn't done");
      else if (n) setSaid(`Running ${n} block${n > 1 ? "s" : ""}`);
    },
    [board],
  );
  useEffect(() => {
    const id = toRun.current;
    if (id && doc.blocks.some((b) => b.id === id)) {
      toRun.current = null;
      run([id]);
    }
  }, [doc, run]);
  // the runs a Stop asked to stop, until none of them is going
  const stopping = useRef<string[]>([]);
  /** Stops runs still going: of these blocks, or all the board's */
  const stop = useCallback(
    (ids?: string[]) => {
      const runs = board.going.filter((g) => !ids || ids.includes(g.block)).map((g) => g.run);
      if (!runs.length) return;
      board.stop(runs);
      stopping.current = runs;
      setSaid(`Stopping ${runs.length} run${runs.length > 1 ? "s" : ""}`);
    },
    [board],
  );
  // "Stopping n runs" turns "Stopped" once none of them is going (it stayed for good: nothing set the line again)
  useEffect(() => {
    const runs = stopping.current;
    if (!runs.length || board.going.some((g) => runs.includes(g.run))) return;
    stopping.current = [];
    setSaid((s) =>
      s.startsWith("Stopping") ? `Stopped ${runs.length} run${runs.length > 1 ? "s" : ""}` : s,
    );
  }, [board.going]);
  /** a block's estimate: studio's plan, else (the fake mode) the kind's guess */
  const estOf = useCallback(
    (b: Block): Estimate | undefined =>
      board.plan
        ? board.estimate(b.id)
        : { seconds: EST_S[b.kind], host: EST_HOST, source: "yours" },
    [board],
  );

  const seek = useCallback((f: number) => {
    player.current?.seek(Math.max(0, f));
  }, []);
  const open = useCallback((id: string) => {
    setSel([id]);
    setOpened(id);
  }, []);
  // the Assistant's screen actions and summary (tools/actions.json `magic`)
  useScreenTools({
    // eslint-disable-next-line react-hooks/refs -- the player is read when an action runs, after render
    ...magicTools({
      id: board.id,
      name: board.name,
      revision: board.revision,
      doc,
      sel,
      frame,
      select: setSel,
      open: (id) => {
        if (doc.blocks.some((b) => b.id === id)) open(id);
        else if (doc.transitions.some((t) => t.id === id && t.kind === "morph")) setMorph(id);
        else return false;
        return true;
      },
      play: (f) => player.current?.play(f),
      pause: () => player.current?.pause(),
      seek,
    }),
    // the Assistant's Stop offers to stop its runs on this board too
    // (read again: a run the Assistant started may not have reached the board yet)
    runs: async () => {
      const mine = (await board.fresh()).filter((g) => g.by === "assistant").map((g) => g.run);
      return { going: mine.length, stop: () => board.stop(mine) };
    },
  });

  const act: Act = useMemo(
    () => ({
      doc,
      takes,
      mode,
      playhead: frame,
      sel,
      edit,
      run,
      stop,
      runOff: board.run ? undefined : RUNS_LATER,
      runWhenAdded: (id) => (toRun.current = id),
      // API 1.85: studio's models per kind (Music's Model ›, a sound from a shot's video) and their names
      models: (k) =>
        (board.kinds?.kinds.find((x) => x.kind === k)?.models ?? []).map((m) => ({
          id: m.workflow_id,
          name: m.name,
          video: m.fields.includes("from"),
        })),
      modelName: (k, id) =>
        board.kinds?.kinds
          .find((x) => x.kind === k)
          ?.models.find((m) => m.workflow_id === id || m.video?.workflow_id === id)?.name,
      estimate: board.plan
        ? (id: string) => {
            const s = board.estimate(id)?.seconds;
            return s == null ? undefined : estText(s, true);
          }
        : undefined,
      select: setSel,
      open,
      add: (lane, at, kind) => setAdding({ lane, at, kind }),
      play: (from) => player.current?.play(from),
      seek,
      rename: setRenaming,
      used: setUsedAsset,
      paste: clip
        ? (lane, at) => {
            const lo = Math.min(...clip.map((b) => b.at));
            edit(
              clip.map((b) => ({
                op: "add",
                mode,
                block: { ...b, id: undefined, lane, at: at + b.at - lo },
              })),
            );
          }
        : null,
      copyText: (s) => void navigator.clipboard?.writeText(s).catch(() => undefined),
      openAsset: setAssetOpen,
      importTo: board.gallery ? (asset) => setImporting({ asset }) : undefined,
      rough: setRough,
      openMorph: setMorph,
      compare: setComparing,
      gallery: board.gallery ? setPicking : null,
      views: board.run ? board.makeViews : null,
      board: board.id,
      fromBoard: board.run
        ? (fid, before = []) => {
            // the frame's own changes, then its shot: made under it (the board's place mode) or run again
            let d = doc;
            for (const op of before) {
              const r = apply(d, op);
              if ("error" in r) return setError(r.error);
              d = r.doc;
            }
            const f = d.blocks.find((b) => b.id === fid);
            if (!f) return;
            const model = f.request.shot_model;
            const made = d.blocks.find((b) => b.from_frame === fid);
            if (made) {
              const ops = [
                ...before,
                ...(model && made.request.model !== model
                  ? [
                      {
                        op: "block" as const,
                        id: made.id,
                        set: { request: { ...made.request, model } },
                      },
                    ]
                  : []),
              ];
              if (ops.length && !edit(ops)) return;
              return run([made.id]);
            }
            const shots = d.lanes.find((l) => l.role === "shots");
            if (!shots) return setError("Add a Shots lane first.");
            const add: Op = {
              op: "add",
              mode,
              block: {
                lane: shots.id,
                at: f.at,
                length: f.length,
                kind: "video",
                name: `Shot ${d.blocks.filter((b) => b.kind === "video").length + 1}`,
                from_frame: f.id,
                request: { kind: "video", ...(model ? { model } : {}) },
                takes: [],
                effects: [],
              },
            };
            const r = apply(d, add);
            if ("error" in r) return setError(r.error);
            if (!edit([...before, add])) return;
            const shot = r.doc.blocks.find((b) => b.from_frame === fid);
            if (shot) toRun.current = shot.id;
          }
        : undefined,
    }),
    [doc, takes, mode, frame, sel, edit, run, stop, board, open, seek, clip],
  );

  // zoom: fit (the whole board across) up to 240 px a second, ×1.25 a step
  const total = duration(doc);
  const fitPps = viewW ? Math.max(2, (viewW - 24) / Math.max(5, (total + fps) / fps)) : 26;
  const MAX_PPS = 240;
  const lo = Math.min(fitPps, 26);
  const clampZ = (p: number) => Math.min(MAX_PPS, Math.max(lo, p));
  const zoomIn = (p: number) => clampZ(p * 1.25);
  const zoomOut = (p: number) => clampZ(p * 0.8);
  const fit = () => setPps(clampZ(fitPps));
  // the keys (frame 1; the Timeline's where they mean the same)
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      const t = e.target instanceof Element ? e.target : null;
      if (t?.closest("input, textarea, select, [contenteditable], dialog[open], [popover]")) return;
      const cmd = e.metaKey || e.ctrlKey;
      const k = e.key.toLowerCase();
      if (k === "escape" && openPopover()) return; // Esc closes the boards popover, not the selection
      let done = true;
      if (cmd && k === "z") (e.shiftKey ? board.redo : board.undo)();
      else if (cmd && k === "y") board.redo();
      else if (cmd && k === "d" && sel.length) edit({ op: "duplicate", ids: sel });
      else if (cmd && (k === "=" || k === "+")) setPps((p) => zoomIn(p));
      else if (cmd && k === "-") setPps((p) => zoomOut(p));
      else if (cmd && k === "c" && sel.length && !getSelection()?.toString())
        setClip(doc.blocks.filter((b) => sel.includes(b.id)));
      else if (cmd && k === "v" && clip) {
        const lane = doc.blocks.find((b) => b.id === sel[0])?.lane ?? clip[0].lane;
        act.paste?.(lane, frame);
      } else if (cmd) done = false;
      else if (e.shiftKey && k === "z") fit();
      else if ((k === "delete" || k === "backspace") && sel.length)
        edit({ op: "delete", ids: sel, mode });
      else if (k === "s" && sel.length === 1) edit({ op: "split", id: sel[0], at: frame });
      else if (k === " " && !t?.closest("button, [role=button]")) {
        if (playing) player.current?.pause();
        else player.current?.play();
      } else if (k === "escape") {
        setSel([]);
        setUsedAsset(null);
      } else done = false;
      if (done) e.preventDefault();
    };
    addEventListener("keydown", key);
    return () => removeEventListener("keydown", key);
  });

  const zPct = Math.round((Math.log(pps / lo) / Math.log(MAX_PPS / lo)) * 100) || 0;

  const runnable = doc.blocks.filter(runsHere);
  const todo = runnable.filter((b) => statusOf(doc, b, takes).s !== "done");
  const ests = (todo.length ? todo : runnable).map(estOf);
  const boardEst = ests.some((e) => e?.seconds == null)
    ? null
    : ests.reduce((n, e) => n + (e?.seconds ?? 0), 0);
  const boardHost = ests.find((e) => e?.host)?.host ?? "";
  /** Run board: every block that isn't done; in studio, asking first above the limit */
  const runBoard = async () => {
    if (!board.plan) return run(todo.map((b) => b.id));
    try {
      const p = await board.plan({ stale: true });
      if (p.ask) setAsking(`${p.ask_text} · above your ${p.limit_minutes} min limit`);
      else run("stale");
    } catch {
      run("stale");
    }
  };
  const hiddenLanes = doc.lanes.filter((l) => l.hidden);
  const over = overruns(doc).filter((o) => !kept.includes(o.block));
  const one = sel.length === 1 ? doc.blocks.find((b) => b.id === sel[0]) : undefined;

  const snapshots = board.snapshots;

  const addLane = () =>
    edit({
      op: "lane_add",
      lane: {
        name: doc.lanes.length ? nextLaneName(doc) : "Shots",
        role: doc.lanes.length ? "free" : "shots",
      },
    });

  return (
    <>
      <main
        id="main"
        className="mg relative flex min-w-0 flex-[999_1_560px] flex-col gap-3.5 px-3.5 pt-4 pb-5 md:px-5"
      >
        <h1 className="sr-only">Magic: {board.name}</h1>
        {/* a long name (a board named from its prompt is up to 5 words) cuts short before the bar wraps (walk) */}
        <div className="flex flex-wrap items-center gap-3 md:flex-nowrap md:[&>*:not(:first-child)]:shrink-0">
          <BoardSwitcher board={board} />
          {board.namedBy && (
            <span
              role="status"
              className="fade-late -ml-2 rounded-full bg-s2 px-2 py-0.5 text-[11px] text-t2"
            >
              named by {board.namedBy}
            </span>
          )}
          <span className="pill max-md:hidden" title="Set when the board was made">
            <Lock size={11} className="mr-1 inline align-[-1px] text-t2" aria-hidden />
            {doc.target.width} × {doc.target.height} · {fps} fps · {clock(total, fps, true)}
          </span>
          <span className="grow" />
          <button
            type="button"
            className="chip bevel rounded-[10px] bg-s4 max-md:hidden"
            onClick={() => {
              setSel([]);
              setPanel(true);
            }}
          >
            <Clock3 size={13} aria-hidden />
            Snapshots{snapshots.length ? ` · ${snapshots.length}` : ""}
          </button>
          <HistoryChip
            revisions={board.revisions}
            revision={board.revision}
            onRestore={(rev) => board.restoreSnapshot({ id: "", name: "", at: "", rev })}
          />
          <SendsChip
            list={sendsList.list}
            sends={board.sends}
            onRestore={(rev) => board.restoreSnapshot({ id: "", name: "", at: "", rev })}
            onChanged={sendsList.reload}
          />
          <button
            type="button"
            className="chip bevel rounded-[10px] bg-s4 px-0 w-9 justify-center max-md:hidden"
            disabled={!board.canUndo}
            aria-label="Undo"
            title="Undo (⌘Z)"
            aria-keyshortcuts="Meta+Z Control+Z"
            onClick={board.undo}
          >
            <Undo2 size={16} aria-hidden />
          </button>
          <button
            type="button"
            className="chip bevel rounded-[10px] bg-s4 px-0 w-9 justify-center max-md:hidden"
            disabled={!board.canRedo}
            aria-label="Redo"
            title="Redo (⇧⌘Z)"
            aria-keyshortcuts="Meta+Shift+Z Control+Shift+Z"
            onClick={board.redo}
          >
            <Redo2 size={16} aria-hidden />
          </button>
          <SendButton
            doc={doc}
            sends={board.sends}
            onSent={(s) => {
              setSent(s);
              sendsList.reload();
            }}
          />
          <ExportButton sends={board.sends} onSent={sendsList.reload} />
        </div>

        {!doc.lanes.length ? (
          <>
            {error && <ErrorNote>{error}</ErrorNote>}
            <EmptyBoard onLane={addLane} onAsset={() => setAssetOpen("new")} />
          </>
        ) : (
          <div className="mb">
            <div className="btools" role="toolbar" aria-label="Board tools">
              <button
                type="button"
                className={`chip bevel ${snapOn ? "on" : ""}`}
                aria-pressed={snapOn}
                title="Snap to edges, the playhead and the ruler"
                onClick={() => setSnapOn((x) => !x)}
              >
                <MagnetGlyph />
                Snap
              </button>
              <div className="seg" role="group" aria-label="Place">
                {(["insert", "overwrite"] as const).map((m) => (
                  <button
                    key={m}
                    type="button"
                    aria-pressed={mode === m}
                    title={
                      m === "insert"
                        ? "Insert pushes what's after along"
                        : "Overwrite covers what's there (or hold Alt while dragging)"
                    }
                    onClick={() => setMode(m)}
                  >
                    {m === "insert" ? "Insert" : "Overwrite"}
                  </button>
                ))}
              </div>
              <button type="button" className="chip bevel" onClick={addLane}>
                <Plus size={13} aria-hidden />
                Lane
              </button>
              {hiddenLanes.length > 0 && (
                <button
                  type="button"
                  className="chip bevel"
                  onClick={(e) =>
                    openMenu(e, {
                      title: "Hidden lanes",
                      items: hiddenLanes.map((l) => ({
                        label: `Show ${l.name}`,
                        onPick: () => edit({ op: "lane", id: l.id, set: { hidden: false } }),
                      })),
                    })
                  }
                >
                  {hiddenLanes.length} hidden
                </button>
              )}
              <button
                type="button"
                className="grid h-[30px] w-9 place-items-center rounded-[10px] bg-s4 aria-expanded:bg-s5 aria-expanded:shadow-[inset_0_0_0_1px_var(--a2)]"
                aria-label="Side panel"
                title="Side panel"
                aria-expanded={panel}
                aria-controls="magic-side"
                onClick={() => setPanel(!panel)}
              >
                <SlidersHorizontal size={16} strokeWidth={1.75} aria-hidden />
              </button>
              <span className="bsep" />
              {/* Play the board from the playhead (MagicBoard frame 1; owner, 2026-11-27: "keep it run add play button") */}
              <button
                type="button"
                className="chip bevel max-md:hidden"
                aria-label={playing ? "Pause the board" : "Play the board"}
                aria-keyshortcuts="Space"
                title={playing ? "Pause (Space)" : "Play the board from the playhead (Space)"}
                onClick={() => (playing ? player.current?.pause() : player.current?.play())}
              >
                {playing ? <Pause10 /> : <Play10 />}
                {playing ? "Pause" : "Play"}
              </button>
              <button
                type="button"
                className="chip bevel"
                disabled={!runnable.length || !board.run}
                onClick={() => void runBoard()}
                title={board.run ? "Runs every block that isn't done" : RUNS_LATER}
              >
                <Play10 />
                Run board
              </button>
              {runnable.length > 0 && board.run && (
                <span className="est" title="From your own recent runs of these models">
                  <Clock3 size={13} aria-hidden />
                  <b>{boardEst == null ? "Time unknown" : estText(boardEst)}</b>
                  {boardHost && ` · ${boardHost}`}
                </span>
              )}
              {board.going.length > 0 && (
                <button
                  type="button"
                  className="chip bevel"
                  title="Stops the board's queued and running runs (asks first)"
                  onClick={() => setStopAsk(true)}
                >
                  <Square size={13} aria-hidden />
                  Stop runs ({board.going.length})
                </button>
              )}
              <span className="grow" />
              <div
                role="group"
                aria-label="Zoom"
                className="tl-zf h-[34px]"
                title="Zoom: ⌘ + mouse wheel or pinch zooms at the pointer · Shift + wheel scrolls across · ⇧Z fits the whole board"
              >
                <button
                  type="button"
                  aria-label="Zoom out"
                  title="Zoom out (⌘−)"
                  onClick={() => setPps(zoomOut)}
                >
                  <svg
                    width="15"
                    height="15"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="2.2"
                    strokeLinecap="round"
                    aria-hidden
                  >
                    <path d="M5 12h14" />
                  </svg>
                </button>
                <input
                  type="range"
                  className="zr"
                  aria-label="Zoom"
                  min={0}
                  max={100}
                  value={zPct}
                  style={{ "--z": `${zPct}%` } as React.CSSProperties}
                  onChange={(e) => setPps(clampZ(lo * (MAX_PPS / lo) ** (+e.target.value / 100)))}
                />
                <button
                  type="button"
                  aria-label="Zoom in"
                  title="Zoom in (⌘=)"
                  onClick={() => setPps(zoomIn)}
                >
                  <svg
                    width="15"
                    height="15"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="2.2"
                    strokeLinecap="round"
                    aria-hidden
                  >
                    <path d="M5 12h14M12 5v14" />
                  </svg>
                </button>
                <span className="zv" aria-live="polite">
                  {clock(Math.round((viewW / pps) * fps), fps, true)} in view
                </span>
                <button
                  type="button"
                  className="fit"
                  aria-label="Zoom to fit"
                  title="Fit the whole board (⇧Z)"
                  onClick={fit}
                >
                  <svg
                    width="15"
                    height="15"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="2.2"
                    strokeLinecap="round"
                    aria-hidden
                  >
                    <path d="M4 9V4h5M20 9V4h-5M4 15v5h5M20 15v5h-5" />
                  </svg>
                  Fit
                </button>
              </div>
            </div>
            {over.slice(0, 1).map((o) => {
              const b = doc.blocks.find((x) => x.id === o.block)!;
              const shot = doc.blocks.find((x) => x.id === o.shot)!;
              const fitLen = end(shot) - b.at;
              return (
                <div key={o.block} className="warn" role="alert">
                  <b>Doesn&apos;t fit</b>
                  <span>
                    {b.name} runs {secs(o.by, fps)} past {shot.name.split(" · ")[0]}
                  </span>
                  <span className="grow" />
                  <button
                    type="button"
                    className="chip"
                    disabled={fitLen < 1}
                    onClick={() =>
                      edit([
                        { op: "trim", id: b.id, length: fitLen },
                        {
                          op: "effect",
                          ids: [b.id],
                          effect: {
                            name: `Speed · ${+(b.length / fitLen).toFixed(2)}×`,
                            params: { rate: +(b.length / fitLen).toFixed(2) },
                          },
                        },
                      ])
                    }
                  >
                    Speed up
                  </button>
                  <button
                    type="button"
                    className="chip"
                    disabled={fitLen < 1}
                    onClick={() => edit({ op: "trim", id: b.id, length: fitLen })}
                  >
                    Trim
                  </button>
                  <button
                    type="button"
                    className="chip"
                    onClick={() => edit({ op: "unlink", block: b.id })}
                  >
                    Unlink
                  </button>
                  <button
                    type="button"
                    className="chip"
                    onClick={() => setKept((k) => [...k, b.id])}
                  >
                    Keep
                  </button>
                </div>
              );
            })}
            {asking && (
              <div className="warn" role="alert">
                <b>Run the board?</b>
                <span>{asking}</span>
                <span className="grow" />
                <button
                  type="button"
                  className="chip"
                  onClick={() => {
                    setAsking(null);
                    run("stale");
                  }}
                >
                  Run
                </button>
                <button type="button" className="chip" onClick={() => setAsking(null)}>
                  Cancel
                </button>
              </div>
            )}
            {stopAsk && board.going.length > 0 && (
              <div className="warn" role="alert">
                <b>
                  Stop {board.going.length} run{board.going.length > 1 ? "s" : ""}?
                </b>
                <span>
                  Queued ones drop at once; running ones stop at their next safe point. Takes
                  already made stay.
                </span>
                <span className="grow" />
                <button
                  type="button"
                  className="chip"
                  onClick={() => {
                    setStopAsk(false);
                    stop();
                  }}
                >
                  Stop runs
                </button>
                <button type="button" className="chip" onClick={() => setStopAsk(false)}>
                  Not now
                </button>
              </div>
            )}
            {error && <ErrorNote>{error}</ErrorNote>}
            <AssetsRow
              doc={doc}
              act={act}
              usedAsset={usedAsset}
              voiceName={(id) => voices?.find((v) => v.id === id)?.name}
            />
            <Lanes
              doc={doc}
              takes={takes}
              act={act}
              pps={pps}
              frame={frame}
              sel={sel}
              snapOn={snapOn}
              phone={phone}
              renaming={renaming}
              usedAsset={usedAsset}
              onSelect={(id, addTo) =>
                setSel((s) =>
                  !id
                    ? []
                    : addTo
                      ? s.includes(id)
                        ? s.filter((x) => x !== id)
                        : [...s, id]
                      : [id],
                )
              }
              onCommit={edit}
              onSeek={seek}
              onZoom={(p) => setPps(clampZ(p))}
              onView={setViewW}
              onRename={(lane, name) => {
                setRenaming(null);
                if (
                  name !== null &&
                  name.trim() &&
                  name !== doc.lanes.find((l) => l.id === lane)?.name
                )
                  edit({ op: "lane", id: lane, set: { name: name.trim() } });
              }}
              onLaneClick={(lane, at) => {
                setSel([]);
                setAdding({ lane, at });
              }}
              proposal={proposed.proposal}
              onProposalMenu={(e) =>
                proposed.card &&
                openMenu(
                  e,
                  proposalMenu(
                    { apply: () => proposed.answer(true), discard: () => proposed.answer(false) },
                    proposed.card.title,
                  ),
                )
              }
            />
            {proposed.card && (
              <ProposalBar card={proposed.card} why={proposed.why} onAnswer={proposed.answer} />
            )}
            <button
              type="button"
              className="chip bevel self-start"
              style={{ marginLeft: phone ? 64 : 120 }}
              onClick={addLane}
            >
              <Plus size={13} aria-hidden />
              Lane
            </button>
            <Player
              doc={doc}
              takes={takes}
              frame={frame}
              onFrame={setFrame}
              onPlaying={setPlaying}
              apiRef={player}
            />
            <div className="flex items-center gap-2 md:hidden" style={{ order: -1 }}>
              <button
                type="button"
                className="chip bevel rounded-[10px] bg-s4"
                aria-label={playing ? "Pause" : "Play"}
                onClick={() => (playing ? player.current?.pause() : player.current?.play())}
              >
                <Play10 />
                {playing ? "Pause" : "Play"}
              </button>
              <span className="mono text-[12px]">
                {clock(frame, fps)}
                <span className="text-t2"> / {clock(total, fps)}</span>
              </span>
              <span className="grow" />
              <button
                type="button"
                className="chip bevel rounded-[10px] bg-s4"
                onClick={() =>
                  setAdding({ lane: (one ?? doc.blocks[0])?.lane ?? doc.lanes[0].id, at: frame })
                }
              >
                <Plus size={13} aria-hidden />
                Add
              </button>
            </div>
            <StateBars doc={doc} takes={takes} act={act} />
            {one && phone && (
              <div className="flex items-center gap-2 rounded-xl bg-s2 px-3 py-2.5 text-[13px]">
                <span className={`kb k-${one.kind}`}>{KIND_NAME[one.kind]}</span>
                <b className="min-w-0 truncate">{one.name}</b>
                <StatusChip d={doc} b={one} takes={takes} />
                <span className="grow" />
                <button
                  type="button"
                  className="text-[12px] text-t2"
                  onClick={() => act.open(one.id)}
                >
                  Open
                </button>
              </div>
            )}
          </div>
        )}
        <p role="status" className="m-0 -mt-2 min-h-4 px-1 text-[12px] text-t2">
          {said}
        </p>
        <FrameDialog
          act={act}
          block={doc.blocks.find((b) => b.id === opened && b.kind === "frame") ?? null}
          kinds={board.kinds}
          runs={board.runs}
          estimate={opened ? board.estimate(opened) : undefined}
          shotEstimate={board.estimate}
          gallery={board.gallery}
          onClose={() => setOpened(null)}
        />
        <AssetDialog
          act={act}
          id={assetOpen}
          kinds={board.kinds}
          runs={board.assetRuns}
          gallery={board.gallery}
          onImport={(asset) => setImporting({ asset })}
          onOpen={(id) => {
            setSel([id]);
            setUsedAsset(null);
          }}
          onClose={() => setAssetOpen(null)}
        />
        <ImportDialog
          act={act}
          into={importing?.asset}
          open={!!importing}
          onClose={() => setImporting(null)}
          onMade={setAssetOpen}
        />
        <RoughCut doc={doc} takes={takes} from={rough} onClose={() => setRough(null)} />
        <MorphDialog
          act={act}
          id={morph}
          kinds={board.kinds}
          estimate={morph ? board.estimate(morph) : undefined}
          onClose={() => setMorph(null)}
        />
        <SendToast send={toast} onClose={() => setSent(null)} />
        <GalleryPicker
          open={!!picking}
          onClose={() => setPicking(null)}
          max={picking?.max ?? 1}
          kind={picking?.kind ?? null}
          title={picking?.title}
          onPick={(ts) =>
            picking?.onPick(
              ts.map((t) => ({
                id: t.id,
                kind: t.kind,
                title: t.title,
                duration: t.video?.duration_s ?? t.audio?.duration_s ?? undefined,
              })),
            )
          }
        />
        <CompareDialog
          act={act}
          block={doc.blocks.find((b) => b.id === comparing) ?? null}
          onClose={() => setComparing(null)}
        />
        <BlockDialog
          act={act}
          block={doc.blocks.find((b) => b.id === opened && b.kind !== "frame") ?? null}
          kinds={board.kinds}
          runs={board.runs}
          estimate={opened ? board.estimate(opened) : undefined}
          onClose={() => setOpened(null)}
        />
        <AddDialog
          doc={doc}
          kinds={board.kinds}
          where={adding}
          mode={mode}
          onGenerate={
            board.run
              ? (block, m, tie) => {
                  // a line or cue tied in step 2 is added with its tie, in one edit
                  const id = freshId(doc, "b");
                  const ops: Op[] = [
                    { op: "add", block: { ...block, id }, mode: m },
                    ...(tie ? [{ op: "link" as const, block: id, to: tie }] : []),
                  ];
                  if (!edit(ops)) return;
                  setSel([id]);
                  toRun.current = id;
                }
              : null
          }
          onGallery={
            board.gallery
              ? (w, m) => {
                  const lane = doc.lanes.find((l) => l.id === w.lane);
                  if (lane) fromGallery(act, lane, w.at, m);
                }
              : null
          }
          onClose={() => setAdding(null)}
          onAdd={(block, m) => {
            const before = new Set(doc.blocks.map((b) => b.id));
            const r = apply(doc, { op: "add", block, mode: m });
            if ("doc" in r) {
              edit({ op: "add", block, mode: m });
              const made = r.doc.blocks.filter((b) => !before.has(b.id)).map((b) => b.id);
              setSel(made);
              if (block.kind === "frame" && made.length === 1) setOpened(made[0]);
            } else setError(r.error);
            setAdding(null);
          }}
        />
      </main>

      {panel && (
        <aside
          id="magic-side"
          aria-label="Block details"
          className="mg-side aside m-3.5 flex flex-[1_1_320px] flex-col gap-3.5 px-[18px] pt-4 pb-6 max-md:hidden md:ml-0"
        >
          {one ? (
            <BlockPanel key={one.id} d={doc} b={one} takes={takes} act={act} est={estOf(one)} />
          ) : sel.length > 1 ? (
            <div className="flex flex-col gap-2">
              <h2>{sel.length} blocks</h2>
              <button
                type="button"
                className="act"
                onClick={(e) =>
                  openMenu(
                    e,
                    blockMenu(
                      act,
                      doc.blocks.find((b) => b.id === sel[0])!,
                    ),
                  )
                }
              >
                Actions…
              </button>
              <button
                type="button"
                className="act text-bad"
                onClick={() => edit({ op: "delete", ids: sel, mode })}
              >
                Delete {sel.length}
              </button>
            </div>
          ) : (
            <BoardPanel
              d={doc}
              takes={takes}
              snapshots={snapshots}
              onClose={() => setPanel(false)}
              onSave={() => {
                const name = `Snapshot ${snapshots.length + 1}`;
                board.saveSnapshot(name);
                setSaid(`Saved ${name}`);
              }}
              onRestore={(s) => {
                board.restoreSnapshot(s);
                setSaid(`Restored ${s.name}`);
              }}
              onRename={(s) => {
                const name = prompt("Snapshot name", s.name)?.trim();
                if (name) board.renameSnapshot(s, name);
              }}
              onDelete={board.deleteSnapshot}
              askAbove={board.plan ? board.askAbove : null}
              onAskAbove={board.setAskAbove}
            />
          )}
        </aside>
      )}
    </>
  );
}

/** The live region's line for an edit. */
function describe(op: Op, d: Doc): string {
  const name = (id: string) => d.blocks.find((b) => b.id === id)?.name ?? "block";
  switch (op.op) {
    case "add":
      return `Added ${op.block.name}`;
    case "move":
      return `Moved ${op.ids.length > 1 ? `${op.ids.length} blocks` : name(op.ids[0])}`;
    case "trim":
      return `Trimmed ${name(op.id)}`;
    case "delete":
      return `Deleted ${op.ids.length > 1 ? `${op.ids.length} blocks` : name(op.ids[0])}`;
    case "duplicate":
      return "Duplicated";
    case "split":
      return `Split ${name(op.id)}`;
    case "link":
      return `Tied ${name(op.block)} to ${op.to.map(name).join(", ")}`;
    case "unlink":
      return `Unlinked ${name(op.block)}`;
    case "transition":
      return op.kind === "cut" ? "Made a cut" : `Set a ${op.kind}`;
    default:
      return "Changed the board";
  }
}

const MagnetGlyph = () => (
  <svg
    width="13"
    height="13"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    strokeWidth="2.2"
    strokeLinecap="round"
    aria-hidden
  >
    <path d="M6 4v8a6 6 0 0 0 12 0V4" />
    <path d="M6 8h4M14 8h4" />
  </svg>
);
const Pause10 = () => (
  <svg width="13" height="13" viewBox="0 0 24 24" fill="currentColor" aria-hidden>
    <path d="M7 4h3.5v16H7zM13.5 4H17v16h-3.5z" />
  </svg>
);

const Play10 = () => (
  <svg width="13" height="13" viewBox="0 0 24 24" fill="currentColor" aria-hidden>
    <path d="M7 4v16l13-8z" />
  </svg>
);

/** A new board (frame 11): lanes in time; a brief (with the Assistant) or a lane. */
function EmptyBoard({ onLane, onAsset }: { onLane: () => void; onAsset: () => void }) {
  return (
    <div className="mb items-center justify-center gap-3 py-16 text-center">
      <h2 className="m-0 font-display text-xl font-bold">A new board</h2>
      <p className="m-0 text-[13px] text-t2">Lanes in time. Start from a brief, or add a lane.</p>
      <div className="flex flex-wrap justify-center gap-2">
        <button
          type="button"
          className="chip bevel rounded-[10px] bg-s4"
          disabled
          title="Comes with the Assistant on boards"
        >
          Build from a brief
        </button>
        <button type="button" className="chip bevel rounded-[10px] bg-s4" onClick={onLane}>
          <Plus size={13} aria-hidden />
          Lane
        </button>
        <button type="button" className="chip bevel rounded-[10px] bg-s4" onClick={onAsset}>
          <Plus size={13} aria-hidden />
          Asset
        </button>
      </div>
    </div>
  );
}

// ------------------------------------------------------------------------------------------------ the side panel

/** What to do about a failed or out-of-date block (frame 11): a bar under the toolbar, up to three (at the bottom the
 * floating player covered its Run). */
function StateBars({ doc, takes, act }: { doc: Doc; takes: Takes; act: Act }) {
  const rows = doc.blocks
    .map((b) => ({ b, st: statusOf(doc, b, takes) }))
    .filter((x) => x.st.s === "failed" || x.st.s === "old")
    .sort((x, y) => Number(y.st.s === "failed") - Number(x.st.s === "failed"))
    .slice(0, 3);
  return rows.map(({ b, st }) => {
    const take = shown(b, takes);
    const name = b.name.split(" · ")[0];
    return (
      <div
        key={b.id}
        className={`warn ${st.s === "failed" ? "bad" : ""}`}
        role="alert"
        style={{ order: -1 }}
      >
        <b>{st.s === "failed" ? `${name} failed` : `${name} out of date`}</b>
        {st.s === "failed" && <span>{st.error}</span>}
        <span className="grow" />
        <button type="button" className="chip" onClick={() => act.run([b.id])}>
          {st.s === "failed" ? "Retry" : "Run"}
        </button>
        {st.s === "failed" ? (
          <button type="button" className="chip" onClick={() => act.open(b.id)}>
            Open
          </button>
        ) : (
          take &&
          b.pinned !== take.id && (
            <button
              type="button"
              className="chip"
              onClick={() => act.edit({ op: "block", id: b.id, set: { pinned: take.id } })}
            >
              Pin take
            </button>
          )
        )}
      </div>
    );
  });
}

/** One block's details (frame 1's side panel), with what to do when it failed or is out of date (frame 11). */
function BlockPanel({
  d,
  b,
  takes,
  act,
  est,
}: {
  d: Doc;
  b: Block;
  takes: Takes;
  act: Act;
  est?: Estimate;
}) {
  const fps = d.target.fps;
  const take = shown(b, takes);
  const lane = d.lanes.find((l) => l.id === b.lane);
  const nth = (id?: string) => (id ? b.takes.indexOf(id) + 1 : 0);
  const runningTake = Object.values(takes).findLast(
    (t) => t.block === b.id && t.status === "running",
  );
  const tied = d.links.filter((l) => l.block === b.id || l.to.includes(b.id));
  const into =
    d.transitions.find((t) => t.between?.[1] === b.id) ??
    d.transitions.find((t) => t.start === b.lane && inLane(d, b.lane)[0]?.id === b.id);
  const out = d.transitions.find((t) => t.between?.[0] === b.id);
  const jn = (t?: { kind: string; length: number }) =>
    t ? `${t.kind[0].toUpperCase()}${t.kind.slice(1)} ${secs(t.length, fps)}` : "Cut";
  const look = soundLook(b, take);
  const short = (id: string) => d.blocks.find((x) => x.id === id)?.name ?? "";
  return (
    <>
      <div className="flex flex-col gap-1">
        <div className="flex items-center gap-2">
          <span className={`kb k-${b.kind}`}>{KIND_NAME[b.kind]}</span>
          <h2>{b.name}</h2>
        </div>
        <span className="text-[12px] text-t2">
          {lane?.name} ·{" "}
          <span className="mono">
            {clock(b.at, fps)} – {clock(end(b), fps)}
          </span>
        </span>
      </div>
      <div
        className="pic isolate"
        style={{ backgroundColor: "var(--s2)", backgroundImage: look ? undefined : take?.art }}
      >
        {look && (
          <span className="absolute inset-0 -z-10 grid place-items-center">
            <SoundMark look={look} art={take?.art} large />
          </span>
        )}
        {runningTake ? (
          <span className="mst run rounded-full bg-black/60 px-2 py-0.5">
            {nth(runningTake.id) ? `Take ${nth(runningTake.id)}` : "Running"} ·{" "}
            {Math.round(runningTake.progress ?? 0)}%
          </span>
        ) : take ? (
          <span className="rounded-full bg-black/60 px-2 py-0.5 text-[11px] text-white">
            Take {nth(take.id)}
            {b.pinned === take.id ? " · pinned" : ""}
          </span>
        ) : (
          <span className="text-[12px] text-t2">No take yet</span>
        )}
      </div>
      {(b.request.refs?.length ?? 0) > 0 && (
        <div className="flex flex-col gap-1.5">
          <h3>Inputs</h3>
          <div className="wrap">
            {b.request.refs!.map((r, i) => (
              <span
                key={i}
                className={`rc big ${"block" in r ? "lk" : r.asset === "mara" ? "ch" : ""}`}
              >
                {refText(d, r)}
              </span>
            ))}
          </div>
        </div>
      )}
      {(b.request.prompt || b.request.line) && (
        <p className="m-0 text-[13px] leading-[18px] text-t1b">
          {b.request.line ? `"${b.request.line}"` : b.request.prompt}
        </p>
      )}
      <div className="kv">
        <div>
          <span>Model</span>
          <b>{act.modelName?.(b.kind, b.request.model) ?? b.request.model ?? "Not picked"}</b>
        </div>
        <div>
          <span>Length</span>
          <b className="mono">{secs(b.length, fps)}</b>
        </div>
        {lane?.role === "shots" && (
          <>
            <div>
              <span>In</span>
              <b>{jn(into)}</b>
            </div>
            <div>
              <span>Out</span>
              <b>{jn(out)}</b>
            </div>
          </>
        )}
        <div>
          <span>Status</span>
          <b>
            <StatusChip d={d} b={b} takes={takes} />
          </b>
        </div>
      </div>
      {tied.length > 0 && (
        <div className="flex flex-col gap-1.5">
          <h3>Tied</h3>
          {tied.map((l) => {
            const other = l.block === b.id ? l.to[0] : l.block;
            const x = d.blocks.find((y) => y.id === other)!;
            return (
              <div key={l.block} className="nr">
                <span className="lkm" aria-hidden>
                  <LinkGlyph />
                </span>
                <span className={`kb k-${x.kind}`}>{KIND_NAME[x.kind]}</span>
                {l.block === b.id
                  ? `${x.name}${l.to.length > 1 ? ` +${l.to.length - 1}` : ""}`
                  : x.name}
                <span className="mono">
                  {l.block === b.id
                    ? "moves with it"
                    : l.to.length > 1
                      ? `${short(l.to[0]).split(" · ")[0]}–${short(l.to.at(-1)!).split(" ")[1]}`
                      : "moves with it"}
                </span>
              </div>
            );
          })}
        </div>
      )}
      <div className="flex flex-col gap-1.5">
        <h3>Effects</h3>
        <div className="wrap">
          {b.effects.map((e, i) => (
            <button
              key={i}
              type="button"
              className={`fx ${e.off ? "off" : ""}`}
              style={{ fontSize: 11, lineHeight: "22px", padding: "0 9px" }}
              onClick={(ev) => openMenu(ev, effectMenu(act, b, i))}
              onContextMenu={(ev) => openMenu(ev, effectMenu(act, b, i))}
            >
              {e.name}
            </button>
          ))}
          <button
            type="button"
            className="chip bevel h-[30px] rounded-[10px] bg-s4 px-2.5 text-[12px]"
            onClick={(e) =>
              openMenu(e, {
                title: "Add effect",
                items: EFFECTS.map((x) => ({
                  label: x.name,
                  onPick: () => act.edit({ op: "effect", ids: [b.id], effect: x }),
                })),
              })
            }
          >
            <Plus size={13} aria-hidden />
            Effect
          </button>
        </div>
      </div>
      <button type="button" className="act" onClick={() => act.open(b.id)}>
        Open
      </button>
      <div className="flex items-center gap-2">
        <button
          type="button"
          className="chip bevel flex-1 justify-center rounded-[10px] bg-s4"
          disabled={!!act.runOff}
          title={act.runOff}
          onClick={() => act.run([b.id])}
        >
          Run
        </button>
        <button
          type="button"
          className="chip bevel flex-1 justify-center rounded-[10px] bg-s4"
          disabled={!!act.runOff}
          title={act.runOff}
          onClick={() =>
            act.run(
              inLane(d, b.lane)
                .filter((x) => x.at <= b.at)
                .map((x) => x.id),
            )
          }
        >
          Run up to here
        </button>
      </div>
      {!act.runOff && est && (
        <span className="est" title="From your own recent runs of this model">
          <Clock3 size={13} aria-hidden />
          <b>{est.seconds == null ? "Time unknown" : estText(est.seconds)}</b>
          {est.host && ` · ${est.host}`}
        </span>
      )}
      <p className="m-0 text-[11px] text-t2">
        {mod("D")} duplicates · S splits at the playhead · ⌫ deletes
      </p>
    </>
  );
}

/** Whether a popover is open (jsdom knows no :popover-open). */
function openPopover() {
  try {
    return !!document.querySelector("[popover]:popover-open");
  } catch {
    return false;
  }
}

/** Nothing selected: the board in numbers and its snapshots (frame 11). */
function BoardPanel({
  d,
  takes,
  snapshots,
  onClose,
  onSave,
  onRestore,
  onRename,
  onDelete,
  askAbove,
  onAskAbove,
}: {
  d: Doc;
  takes: Takes;
  snapshots: Snapshot[];
  /** Magic settings: ask before runs above this many GPU minutes (null: the fake mode) */
  askAbove: number | null;
  onAskAbove: (minutes: number) => void;
  onClose: () => void;
  onSave: () => void;
  onRestore: (s: Snapshot) => void;
  onRename: (s: Snapshot) => void;
  onDelete: (s: Snapshot) => void;
}) {
  const fps = d.target.fps;
  const by = (s: string) => d.blocks.filter((b) => statusOf(d, b, takes).s === s).length;
  // past 8 snapshots, the browser searches their names (all loaded)
  const search = useSearch({
    items: snapshots,
    text: (s) => [s.name],
    id: (s) => s.id,
    onPick: onRestore,
  });
  return (
    <>
      <h2>Board</h2>
      <div className="kv">
        <div>
          <span>Lanes</span>
          <b>{d.lanes.length}</b>
        </div>
        <div>
          <span>Blocks</span>
          <b>{d.blocks.length}</b>
        </div>
        <div>
          <span>Length</span>
          <b className="mono">{clock(duration(d), fps, true)}</b>
        </div>
        <div>
          <span>Out of date</span>
          <b>{by("old")}</b>
        </div>
      </div>
      <p className="m-0 text-[12px] text-t2">Select a block to see its details.</p>
      {askAbove != null && (
        <label className="flex items-center gap-2 text-[12px] text-t2">
          Ask before runs over
          <input
            type="number"
            aria-label="Ask above GPU minutes"
            className="in h-7 w-16 px-2"
            min={0}
            max={600}
            step={0.5}
            defaultValue={askAbove}
            onBlur={(e) => {
              const v = Math.min(600, Math.max(0, +e.target.value));
              if (!Number.isNaN(v) && v !== askAbove) onAskAbove(v);
            }}
          />
          GPU min
        </label>
      )}
      <div className="flex items-center gap-2">
        <h3 className="mr-auto">
          Snapshots <small className="mono text-t2">{snapshots.length}</small>
        </h3>
        <button
          type="button"
          className="chip h-7 w-7 justify-center rounded-[9px] bg-s4 p-0"
          aria-label="Close"
          onClick={onClose}
        >
          <X size={13} aria-hidden />
        </button>
      </div>
      <button
        type="button"
        className="chip bevel h-[30px] self-start rounded-[10px] bg-s4 px-2.5 text-[12px]"
        onClick={onSave}
      >
        <Camera size={13} aria-hidden />
        Save snapshot
      </button>
      {search.show && <SearchField {...search.field} label="Search snapshots" />}
      {search.q && !search.shown.length && (
        <NoMatch what="snapshots" onClear={() => search.setQ("")} />
      )}
      {search.shown.map((s) => (
        <div
          key={s.id}
          id={search.rowId(s)}
          className={`nr ${search.rows[search.active] === s ? "sf-on" : ""}`}
          onContextMenu={(e) =>
            openMenu(
              e,
              snapshotMenu(s.name, {
                restore: () => onRestore(s),
                rename: () => onRename(s),
                remove: () => onDelete(s),
              }),
            )
          }
        >
          <b className="min-w-0 grow truncate">
            <Marked text={s.name} q={search.q} />
          </b>
          <span className="mono">
            {s.rev !== undefined && `rev ${s.rev} · `}
            {new Date(s.at).toDateString() === new Date().toDateString()
              ? new Date(s.at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })
              : new Date(s.at).toLocaleDateString([], { month: "short", day: "numeric" })}
          </span>
          <button
            type="button"
            className="chip h-7 rounded-[9px] bg-s4 px-2.5 text-[12px]"
            onClick={() => onRestore(s)}
          >
            Restore
          </button>
        </div>
      ))}
      {snapshots.length > 0 && (
        <p className="m-0 text-[11px] text-t2">Restore makes a new revision; nothing is lost.</p>
      )}
    </>
  );
}
