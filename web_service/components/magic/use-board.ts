"use client";
// Where a Magic board lives (plan 39 phase C): in studio (`/v1/studio/magic/boards`, contracts/README.md "Magic").
// The page edits through one shape, `Board`:
// an edit is applied here first (lib/magic.ts, the same ops as studio's) and then sent with the revision it was made
// on, one call after another, so the board never waits for the network and never runs ahead of studio.
import { stored } from "@/lib/hydrated";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useSearchParams } from "next/navigation";
import { api, ApiError, message, ok, okOr, type S } from "@/lib/api";
import { followSession } from "@/lib/runs";
import { isOutage, onBack, OUTAGE_TEXT, whenUp } from "@/lib/health";
import { firstHint } from "@/components/create/sessions";
import {
  takesOf,
  type AssetRun,
  type BlockRun,
  type Estimate,
  type Kinds,
  type Plan,
} from "@/lib/magic-standard";
import type { Doc, Op, Takes } from "@/lib/magic";
import { sampleBoard } from "@/lib/magic-fixture";
import { studioSends, type SendApi } from "@/components/magic/send";

/** A run still queued or running. */
export type Going = { run: string; block: string; by: "user" | "assistant" };
const isGoing = (s: string) => s === "queued" || s === "running";
/** a studio board's runs still going, Make views' too */
const goingOf = (w: S<"MagicBoard">): Going[] => [
  ...(w.runs ?? [])
    .filter((r) => isGoing(r.status))
    .map((r) => ({ run: r.run_id, block: r.block, by: r.by })),
  ...(w.asset_runs ?? [])
    .filter((r) => isGoing(r.status))
    .map((r) => ({ run: r.run_id, block: r.asset, by: "user" as const })),
];

export type BoardRow = { id: string; name: string; lanes?: number; updated_at?: string };
export type Snapshot = { id: string; name: string; at: string; rev?: number; doc?: Doc };

/** One open board and what the page does to it. */
export type Board = {
  /** the boards, newest first; studio's carry when each was last edited and its lanes */
  boards: BoardRow[];
  id: string;
  name: string;
  doc: Doc;
  takes: Takes;
  canUndo: boolean;
  canRedo: boolean;
  snapshots: Snapshot[];
  /** what studio refused or failed (the board is read again) */
  error: string;
  /** an edit already applied here (`next`): kept and sent */
  edit: (ops: Op[], next: Doc) => void;
  undo: () => void;
  redo: () => void;
  /** runs blocks (or `stale`: every block not done): how many asked for (-1: stale) */
  run: (ids: string[] | "stale") => number;
  /** the kinds studio serves (null until read) */
  kinds: Kinds | null;
  /** every block's runs, oldest first (the run history) */
  runs: BlockRun[];
  /** the Make views runs, oldest first (phase E) */
  assetRuns: AssetRun[];
  /** the board's runs still queued or running (Stop, Stop runs (N)): each one's block (an asset's id for Make views)
   * and who ran it */
  going: Going[];
  /** stops runs: a queued one drops at once, a running one at its next safe point; the takes made stay */
  stop: (runs: string[]) => void;
  /** the board read again now (runs started elsewhere, the Assistant's, show) and its runs still going */
  fresh: () => Promise<Going[]>;
  /** Make views: one image per view of an asset, onto its sheet, on an image model (absent: the first) */
  makeViews: (
    asset: string,
    views: string[],
    more?: Omit<S<"MagicViewsCreate">, "revision" | "views">,
  ) => void;
  /** a block's GPU-time estimate (studio's plan), undefined while unknown here */
  estimate: (id: string) => Estimate | undefined;
  /** studio's plan of a pick (the ask rule) */
  plan: (pick: { stale: true } | { blocks: string[] }) => Promise<Plan>;
  /** ask before runs above this many GPU minutes (Magic settings) */
  askAbove: number;
  setAskAbove: (minutes: number) => void;
  saveSnapshot: (name: string) => void;
  restoreSnapshot: (s: Snapshot) => void;
  renameSnapshot: (s: Snapshot, name: string) => void;
  deleteSnapshot: (s: Snapshot) => void;
  switchTo: (id: string) => void;
  /** a new board at `target` (absent: 1280 × 720 · 24 fps); its target can't change later */
  newBoard: (target?: S<"MagicTarget">, name?: string) => void;
  sampleBoard: () => void;
  /** renames a board (absent: the open one) */
  rename: (name: string, id?: string) => void;
  /** deletes a board (absent: the open one; then the next opens) */
  remove: (id?: string) => void;
  /** Send to Timeline (phase G): studio's sends of this board */
  sends: SendApi;
  /** studio's revision of the board */
  revision: number;
  /** read studio's board again (the Assistant changed or ran it, API 1.87) */
  reload: () => void;
  /** the kept revisions, newest first (History, MagicBoard frame 8) */
  revisions: () => Promise<S<"MagicRevisionItem">[]>;
  /** the model that just named the board (API 1.91): "named by Qwen3.8" under its name, once per browser; "" none */
  namedBy?: string;
};

// ------------------------------------------------------------------------------------------------ in studio

type Wire = S<"MagicBoard">;
const CURRENT = "helm.magic.board";
const remember = (id: string) => stored.set(CURRENT, id);
const remembered = () => stored.get(CURRENT);
const board = (id: string) => ({ params: { path: { board_id: id } } });
const web = { "X-Helm-Client": "web" };
const toSnapshot = (s: S<"MagicSnapshot">): Snapshot => ({
  id: s.id,
  name: s.name,
  at: s.made_at,
  rev: s.revision,
});
/** The sample board as studio can keep it: its blocks are drafts (its takes are pictures in this browser). */
function sampleDoc(): Doc {
  const d = sampleBoard().doc;
  return {
    ...d,
    // its models are names (lib/magic-fixture's); studio runs each kind's own first model
    blocks: d.blocks.map((b) => ({
      ...b,
      takes: [],
      pinned: undefined,
      request: { ...b.request, model: undefined },
    })),
    assets: d.assets.map((a) => ({ ...a, sheet: [] })),
  };
}

/** The boards in studio: null until the first one is open. */
export function useApiBoard():
  (Board & { ready: true }) | { ready: false; error: string; retry: () => void } {
  const [boards, setBoards] = useState<BoardRow[]>([]);
  const [wire, setWire] = useState<Wire | null>(null);
  const [doc, setDoc] = useState<Doc | null>(null);
  const [snapshots, setSnapshots] = useState<Snapshot[]>([]);
  const [error, setError] = useState("");
  const [kinds, setKinds] = useState<Kinds | null>(null);
  const [estimates, setEstimates] = useState<Record<string, Estimate>>({});
  const [askAbove, setAskAboveState] = useState(5);
  const rev = useRef(0);
  const pending = useRef(0);
  const current = useRef("");
  const chain = useRef<Promise<void>>(Promise.resolve());
  /** a call waits for the studio to answer (it is made again then) */
  const holding = useRef(false);

  /** studio's board: the revision always, the document once no edit of ours is still on its way */
  const show = useCallback((w: Wire) => {
    rev.current = w.revision;
    setWire(w);
    if (!pending.current) setDoc(w.doc as Doc);
  }, []);
  const open = useCallback(
    async (id: string) => {
      const [w, s] = await Promise.all([
        ok(api.GET("/v1/studio/magic/boards/{board_id}", board(id))) as Promise<Wire>,
        ok(api.GET("/v1/studio/magic/boards/{board_id}/snapshots", board(id))),
      ]);
      current.current = id;
      remember(id);
      pending.current = 0;
      show(w);
      setSnapshots(s.snapshots.map(toSnapshot));
    },
    [show],
  );
  const create = useCallback(
    async (body: S<"MagicBoardCreate">) => {
      const w = await ok(api.POST("/v1/studio/magic/boards", { headers: web, body }));
      setBoards((bs) => [{ id: w.id, name: w.name }, ...bs]);
      await open(w.id);
    },
    [open],
  );
  /** one call after another; a failure says why and reads the board again. The studio not answering (docs/mock/
   * ErrorStates.dc.html a3) holds the call instead: the banner says it (a board not loaded yet says it didn't load),
   * and the call is made again once the studio answers, so an edit is not lost (at most 5 times) */
  const queue = useCallback(
    (f: () => Promise<void>) => {
      const held = async (n = 0): Promise<void> => {
        try {
          await f();
        } catch (e) {
          if (!isOutage(e) || n >= 5) throw e;
          if (!current.current) setError(OUTAGE_TEXT);
          holding.current = true;
          await whenUp().finally(() => (holding.current = false));
          return held(n + 1);
        }
      };
      chain.current = chain.current
        .then(() => held())
        .then(
          () => setError(""),
          async (e) => {
            setError(message(e));
            pending.current = 0;
            if (current.current) await open(current.current).catch(() => undefined);
          },
        );
    },
    [open],
  );

  // the kinds and the settings, once
  useEffect(() => {
    ok(api.GET("/v1/studio/magic/kinds")).then(setKinds, () => undefined);
    ok(api.GET("/v1/studio/magic/settings")).then(
      (s) => setAskAboveState(s.ask_above_gpu_minutes),
      () => undefined,
    );
  }, []);

  /** the board again (a run moved on): only while no edit of ours is on its way, which brings it anyway */
  const refresh = useCallback(async () => {
    const id = current.current;
    if (!id || pending.current) return;
    const w = (await ok(api.GET("/v1/studio/magic/boards/{board_id}", board(id)))) as Wire;
    if (current.current === id && !pending.current) {
      show(w);
      setBoards((bs) => bs.map((b) => (b.id === id ? { id, name: w.name } : b)));
    }
  }, [show]);
  // the studio is back (ErrorStates a2): the board as it is now
  useEffect(() => {
    const off = onBack(() => void refresh().catch(() => undefined));
    return () => off();
  }, [refresh]);
  // API 1.91: an untitled board is named from its first prompt, then perhaps by the namer: its session says so
  const [namedBy, setNamedBy] = useState("");
  useEffect(() => {
    if (!namedBy) return;
    const t = setTimeout(() => setNamedBy(""), 6000);
    return () => clearTimeout(t);
  }, [namedBy]);
  const naming = useRef(false);
  const untitled = wire?.title_source === "default" || wire?.title_source === "prompt";
  useEffect(() => {
    naming.current = untitled;
  }, [untitled]);
  // the board's runs move through its session's events (status, progress, outputs): read it again on them
  const session = wire?.session_id ?? null;
  useEffect(() => {
    if (!session) return;
    let t: ReturnType<typeof setTimeout> | undefined;
    const stop = followSession(session, (e) => {
      if (e.type === "session") {
        // the namer's answer for a name nobody typed (a replay of an old one finds the board named already)
        if (!naming.current || e.data.title_source !== "model") return;
        naming.current = false;
        void refresh().catch(() => undefined);
        if (e.data.named_by && firstHint()) setNamedBy(e.data.named_by);
        return;
      }
      clearTimeout(t);
      t = setTimeout(() => void refresh().catch(() => undefined), 250);
    });
    return () => {
      clearTimeout(t);
      stop();
    };
  }, [session, refresh]);
  // every block's estimate at this revision (studio's plan)
  const planKey = wire ? `${wire.id}:${wire.revision}:${wire.runs?.length ?? 0}` : "";
  useEffect(() => {
    if (!wire || !wire.doc.blocks.length) return;
    let gone = false;
    // the blocks and (API 1.82) the morphs
    const ids = [
      ...wire.doc.blocks.map((b) => b.id),
      ...wire.doc.transitions.filter((t) => t.kind === "morph" && t.between).map((t) => t.id),
    ].slice(0, 200);
    ok(
      api.POST("/v1/studio/magic/boards/{board_id}/plan", {
        ...board(wire.id),
        body: { blocks: ids },
      }),
    ).then(
      (p) => {
        if (gone) return;
        const m: Record<string, Estimate> = {};
        for (const b of p.blocks)
          if (!b.reason) m[b.block] = { seconds: b.seconds, host: b.host, source: b.source };
        setEstimates(m);
      },
      () => undefined,
    );
    return () => {
      gone = true;
    };
    // the plan follows the revision and the runs, not every render
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [planKey]);

  // a board the address names later (navigate to /magic?b= while the page is open)
  const named = useSearchParams()?.get("b") ?? null;
  useEffect(() => {
    if (named && current.current && named !== current.current)
      queue(async () => {
        if (named !== current.current) await open(named);
      });
  }, [named, queue, open]);

  /** the boards, then the one to show (a first visit makes one); again from Retry (ErrorStates a3) */
  const start = useCallback(
    () =>
      queue(async () => {
        const { boards: list } = await ok(api.GET("/v1/studio/magic/boards"));
        setBoards(list.map(({ id, name, lanes, updated_at }) => ({ id, name, lanes, updated_at })));
        // the board the address names (the Assistant's navigate, API 1.87), else the one open last
        const asked = new URLSearchParams(location.search).get("b");
        const want =
          list.find((b) => b.id === asked) ?? list.find((b) => b.id === remembered()) ?? list[0];
        if (want) await open(want.id);
        else await create({});
      }),
    [queue, open, create],
  );
  useEffect(start, [start]);

  const takes = useMemo(
    () => (wire ? takesOf(wire.doc as Doc, wire.runs ?? [], wire.takes ?? {}) : {}),
    [wire],
  );
  // Retry (ErrorStates a3): a load already waiting for the studio goes on by itself once it answers, no second one
  if (!wire || !doc) return { ready: false, error, retry: () => !holding.current && start() };
  const id = wire.id;
  /** a call made on a revision studio has written past since (a run's take joined its block): made again, once, on
   * the revision as it is now */
  const fresh = <T>(send: () => Promise<T>): Promise<T> =>
    send().catch(async (e) => {
      if (
        !(e instanceof ApiError) ||
        e.status !== 409 ||
        !/changed since you opened it/.test(e.message)
      )
        throw e;
      rev.current = (
        (await ok(api.GET("/v1/studio/magic/boards/{board_id}", board(id)))) as Wire
      ).revision;
      return send();
    });
  return {
    ready: true,
    boards,
    id,
    name: wire.name,
    namedBy,
    doc,
    takes,
    canUndo: wire.can_undo,
    canRedo: wire.can_redo,
    snapshots,
    error,
    edit: (ops, next) => {
      setDoc(next);
      pending.current++;
      queue(async () => {
        // made on a board being left: dropped (the one opening is shown)
        if (current.current !== id)
          return void (pending.current = Math.max(0, pending.current - 1));
        const send = () =>
          ok(
            api.POST("/v1/studio/magic/boards/{board_id}/edits", {
              ...board(id),
              headers: web,
              body: { base_revision: rev.current, ops: ops as unknown as S<"MagicOp">[] },
            }),
          );
        // a studio write since (a run's take joined its block): the same ops on the board as it is now, once
        const r = await fresh(send);
        // a failure before it zeroed the count
        pending.current = Math.max(0, pending.current - 1);
        if (current.current === id) show(r.board);
      });
    },
    // nothing to go to is nothing to do (a key press)
    undo: () =>
      (wire.can_undo || pending.current > 0) &&
      queue(async () =>
        show(
          await fresh(() =>
            ok(
              api.POST("/v1/studio/magic/boards/{board_id}/undo", {
                ...board(id),
                headers: web,
                body: { revision: rev.current },
              }),
            ),
          ),
        ),
      ),
    redo: () =>
      wire.can_redo &&
      queue(async () =>
        show(
          await fresh(() =>
            ok(
              api.POST("/v1/studio/magic/boards/{board_id}/redo", {
                ...board(id),
                headers: web,
                body: { revision: rev.current },
              }),
            ),
          ),
        ),
      ),
    run: (ids) => {
      queue(async () => {
        const r = await fresh(() =>
          ok(
            api.POST("/v1/studio/magic/boards/{board_id}/run", {
              ...board(id),
              headers: web,
              body:
                ids === "stale"
                  ? { revision: rev.current, stale: true }
                  : { revision: rev.current, blocks: ids },
            }),
          ),
        );
        if (current.current === id) show(r.board);
      });
      return ids === "stale" ? -1 : ids.length;
    },
    kinds,
    runs: wire.runs ?? [],
    assetRuns: wire.asset_runs ?? [],
    going: goingOf(wire),
    fresh: async () => {
      const w = (await ok(api.GET("/v1/studio/magic/boards/{board_id}", board(id)))) as Wire;
      if (current.current === id && !pending.current) show(w);
      return goingOf(w);
    },
    // each run's own cancel (409: it ended meanwhile); the board is read again after
    stop: (runs) =>
      queue(async () => {
        await Promise.all(
          runs.map((run_id) =>
            okOr(
              api.POST("/v1/studio/runs/{run_id}/cancel", { params: { path: { run_id } } }),
              409,
            ),
          ),
        );
        await refresh();
      }),
    makeViews: (asset, views, more) =>
      queue(async () => {
        const r = await fresh(() =>
          ok(
            api.POST("/v1/studio/magic/boards/{board_id}/assets/{asset_id}/views", {
              params: { path: { board_id: id, asset_id: asset } },
              headers: web,
              body: { revision: rev.current, views, ...more },
            }),
          ),
        );
        if (current.current === id) show(r.board);
      }),
    // the revision studio last answered (an edit still on its way is a 409, said in the dialog)
    sends: studioSends(id, wire.revision),
    revision: wire.revision,
    reload: () => void refresh().catch(() => undefined),
    revisions: () =>
      ok(
        api.GET("/v1/studio/magic/boards/{board_id}/revisions", {
          params: { path: { board_id: id }, query: { limit: 30 } },
        }),
      ).then((r) => r.revisions),
    estimate: (b) => estimates[b],
    plan: (pick) =>
      ok(api.POST("/v1/studio/magic/boards/{board_id}/plan", { ...board(id), body: pick })),
    askAbove,
    setAskAbove: (m) => {
      setAskAboveState(m);
      void ok(api.PUT("/v1/studio/magic/settings", { body: { ask_above_gpu_minutes: m } })).catch(
        (e) => setError(message(e)),
      );
    },
    saveSnapshot: (name) =>
      queue(async () => {
        const s = await ok(
          api.POST("/v1/studio/magic/boards/{board_id}/snapshots", {
            ...board(id),
            body: { name },
          }),
        );
        setSnapshots((all) => [toSnapshot(s), ...all]);
      }),
    restoreSnapshot: (s) =>
      queue(async () =>
        show(
          await ok(
            api.POST("/v1/studio/magic/boards/{board_id}/restore", {
              ...board(id),
              headers: web,
              body: { revision: rev.current, to: s.rev! },
            }),
          ),
        ),
      ),
    renameSnapshot: (s, name) =>
      queue(async () => {
        const n = await ok(
          api.PATCH("/v1/studio/magic/boards/{board_id}/snapshots/{snapshot_id}", {
            params: { path: { board_id: id, snapshot_id: s.id } },
            body: { name },
          }),
        );
        setSnapshots((all) => all.map((x) => (x.id === s.id ? toSnapshot(n) : x)));
      }),
    deleteSnapshot: (s) =>
      queue(async () => {
        await ok(
          api.DELETE("/v1/studio/magic/boards/{board_id}/snapshots/{snapshot_id}", {
            params: { path: { board_id: id, snapshot_id: s.id } },
          }),
        );
        setSnapshots((all) => all.filter((x) => x.id !== s.id));
      }),
    switchTo: (to) => queue(() => open(to)),
    newBoard: (target, name) =>
      queue(() => create({ ...(target && { target }), ...(name && { name }) })),
    sampleBoard: () => queue(() => create({ name: sampleBoard().name, doc: sampleDoc() as never })),
    rename: (name, which = id) =>
      queue(async () => {
        const w = await ok(
          api.PATCH("/v1/studio/magic/boards/{board_id}", { ...board(which), body: { name } }),
        );
        setBoards((bs) => bs.map((b) => (b.id === which ? { ...b, name: w.name } : b)));
        if (which === current.current) show(w);
      }),
    remove: (which = id) =>
      queue(async () => {
        await ok(api.DELETE("/v1/studio/magic/boards/{board_id}", board(which)));
        const rest = boards.filter((b) => b.id !== which);
        setBoards(rest);
        if (which !== current.current) return;
        if (rest.length) await open(rest[0].id);
        else await create({});
      }),
  };
}
