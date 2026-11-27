"use client";
// Where a Magic board lives (plan 39 phase C): in studio (`/v1/studio/magic/boards`, contracts/README.md "Magic"), or,
// for the tests' fake mode, in this browser with fake takes (phase B's). The page edits through one shape, `Board`:
// an edit is applied here first (lib/magic.ts, the same ops as studio's) and then sent with the revision it was made
// on, one call after another, so the board never waits for the network and never runs ahead of studio.
import { useCallback, useEffect, useRef, useState } from "react";
import { api, message, ok, type S } from "@/lib/api";
import {
  commit,
  everywhere,
  history,
  inputsOf,
  redo,
  undo,
  type Doc,
  type History,
  type Op,
  type Takes,
} from "@/lib/magic";
import { artFor, emptyDoc, sampleBoard } from "@/lib/magic-fixture";

export type Snapshot = { id: string; name: string; at: string; rev?: number; doc?: Doc };

/** One open board and what the page does to it. */
export type Board = {
  boards: { id: string; name: string }[];
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
  /** runs blocks: how many started; null while runs can't go (they come with the standard request, phase D) */
  run: ((ids: string[]) => number) | null;
  saveSnapshot: (name: string) => void;
  restoreSnapshot: (s: Snapshot) => void;
  renameSnapshot: (s: Snapshot, name: string) => void;
  deleteSnapshot: (s: Snapshot) => void;
  switchTo: (id: string) => void;
  newBoard: () => void;
  sampleBoard: () => void;
  rename: (name: string) => void;
  remove: () => void;
};

// ------------------------------------------------------------------------------------------------ in studio

type Wire = S<"MagicBoard">;
const CURRENT = "helm.magic.board";
const remember = (id: string) => {
  try {
    localStorage.setItem(CURRENT, id);
  } catch {}
};
const remembered = () => {
  try {
    return localStorage.getItem(CURRENT);
  } catch {
    return null;
  }
};
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
    blocks: d.blocks.map((b) => ({ ...b, takes: [], pinned: undefined })),
    assets: d.assets.map((a) => ({ ...a, sheet: [] })),
  };
}

/** The boards in studio: null until the first one is open. */
export function useApiBoard(): (Board & { ready: true }) | { ready: false; error: string } {
  const [boards, setBoards] = useState<{ id: string; name: string }[]>([]);
  const [wire, setWire] = useState<Wire | null>(null);
  const [doc, setDoc] = useState<Doc | null>(null);
  const [snapshots, setSnapshots] = useState<Snapshot[]>([]);
  const [error, setError] = useState("");
  const rev = useRef(0);
  const pending = useRef(0);
  const current = useRef("");
  const chain = useRef<Promise<void>>(Promise.resolve());

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
  /** one call after another; a failure says why and reads the board again */
  const queue = useCallback(
    (f: () => Promise<void>) => {
      chain.current = chain.current.then(f).then(
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

  useEffect(() => {
    queue(async () => {
      const { boards: list } = await ok(api.GET("/v1/studio/magic/boards"));
      setBoards(list.map(({ id, name }) => ({ id, name })));
      const want = list.find((b) => b.id === remembered()) ?? list[0];
      if (want) await open(want.id);
      else await create({});
    });
  }, [queue, open, create]);

  if (!wire || !doc) return { ready: false, error };
  const id = wire.id;
  return {
    ready: true,
    boards,
    id,
    name: wire.name,
    doc,
    takes: {},
    canUndo: wire.can_undo,
    canRedo: wire.can_redo,
    snapshots,
    error,
    edit: (ops, next) => {
      setDoc(next);
      pending.current++;
      queue(async () => {
        const r = await ok(
          api.POST("/v1/studio/magic/boards/{board_id}/edits", {
            ...board(id),
            headers: web,
            body: { base_revision: rev.current, ops: ops as unknown as S<"MagicOp">[] },
          }),
        );
        pending.current--;
        show(r.board);
      });
    },
    undo: () =>
      queue(async () =>
        show(
          await ok(
            api.POST("/v1/studio/magic/boards/{board_id}/undo", {
              ...board(id),
              headers: web,
              body: { revision: rev.current },
            }),
          ),
        ),
      ),
    redo: () =>
      queue(async () =>
        show(
          await ok(
            api.POST("/v1/studio/magic/boards/{board_id}/redo", {
              ...board(id),
              headers: web,
              body: { revision: rev.current },
            }),
          ),
        ),
      ),
    run: null,
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
    newBoard: () => queue(() => create({})),
    sampleBoard: () => queue(() => create({ name: sampleBoard().name, doc: sampleDoc() as never })),
    rename: (name) =>
      queue(async () => {
        const w = await ok(
          api.PATCH("/v1/studio/magic/boards/{board_id}", { ...board(id), body: { name } }),
        );
        setBoards((bs) => bs.map((b) => (b.id === id ? { id, name: w.name } : b)));
        show(w);
      }),
    remove: () =>
      queue(async () => {
        await ok(api.DELETE("/v1/studio/magic/boards/{board_id}", board(id)));
        const rest = boards.filter((b) => b.id !== id);
        setBoards(rest);
        if (rest.length) await open(rest[0].id);
        else await create({});
      }),
  };
}

// ------------------------------------------------------------------------------------------------ in this browser

type Saved = { id: string; name: string; doc: Doc; takes: Takes; snapshots: Snapshot[] };
export type Store = { current: string; boards: Saved[] };
const KEY = "helm.magic.boards";
const newId = () => Math.random().toString(36).slice(2, 10);
const withBoard = (s: Store, doc: Doc, takes: Takes): Store => ({
  ...s,
  boards: s.boards.map((b) => (b.id === s.current ? { ...b, doc, takes } : b)),
});
function sample(): Saved {
  const s = sampleBoard();
  return { id: newId(), name: s.name, doc: s.doc, takes: s.takes, snapshots: [] };
}
/** The boards kept in this browser; the first visit gets the sample board. */
export function loadStore(): Store {
  try {
    const raw = localStorage.getItem(KEY);
    const s = raw ? (JSON.parse(raw) as Store) : null;
    if (s?.boards?.length) return s;
  } catch {
    /* unreadable: start again */
  }
  const b = sample();
  return { current: b.id, boards: [b] };
}
function saveStore(s: Store) {
  try {
    localStorage.setItem(KEY, JSON.stringify(s));
  } catch {
    /* full or blocked: the board still works for this visit */
  }
}

/** Fake runs move on every tick; a take is done at 100. */
const TICK_MS = 400;
const STEP = 9;

/** The fake mode (tests, phase B): boards, undo and snapshots in this browser, runs with fake takes. */
export function useLocalBoard(initial?: Store): Board {
  const [store, setStore] = useState<Store>(() => initial ?? loadStore());
  const saved = store.boards.find((b) => b.id === store.current) ?? store.boards[0];
  const [h, setH] = useState<History>(() => history(saved.doc));
  const [takes, setTakes] = useState<Takes>(saved.takes);

  useEffect(() => {
    saveStore(withBoard(store, h.now, takes));
  }, [store, h.now, takes]);
  const switchTo = (id: string, next?: Store) => {
    const s = withBoard(next ?? store, h.now, takes);
    const b = s.boards.find((x) => x.id === id)!;
    const n = { ...s, current: id };
    setStore(n);
    saveStore(n);
    setH(history(b.doc));
    setTakes(b.takes);
  };
  const setSnapshots = (f: (s: Snapshot[]) => Snapshot[]) =>
    setStore((s) => {
      const next = {
        ...s,
        boards: s.boards.map((b) => (b.id === saved.id ? { ...b, snapshots: f(b.snapshots) } : b)),
      };
      saveStore(next);
      return next;
    });

  // fake runs move along, and finish
  const running = Object.values(takes).some((t) => t.status === "running");
  useEffect(() => {
    if (!running) return;
    const t = setInterval(
      () =>
        setTakes((all) =>
          Object.fromEntries(
            Object.entries(all).map(([id, t]) => {
              if (t.status !== "running") return [id, t];
              const p = (t.progress ?? 0) + STEP;
              return [
                id,
                p >= 100 ? { ...t, status: "done", progress: 100 } : { ...t, progress: p },
              ];
            }),
          ),
        ),
      TICK_MS,
    );
    return () => clearInterval(t);
  }, [running]);

  return {
    boards: store.boards.map(({ id, name }) => ({ id, name })),
    id: saved.id,
    name: saved.name,
    doc: h.now,
    takes,
    canUndo: h.past.length > 0,
    canRedo: h.future.length > 0,
    snapshots: saved.snapshots,
    error: "",
    edit: (_, next) => setH((x) => commit(x, next)),
    undo: () => setH(undo),
    redo: () => setH(redo),
    /** a take per block, running, made from the block's inputs now; a studio write: it reaches every document in
     * the history, so Undo never takes a take back */
    run: (ids) => {
      const fresh: Takes = {};
      for (const id of ids) {
        const b = h.now.blocks.find((x) => x.id === id);
        if (!b || b.kind === "music" || b.kind === "sound") continue;
        const tid = `${b.id}-t${newId()}`;
        fresh[tid] = {
          id: tid,
          block: b.id,
          status: "running",
          progress: 0,
          inputs: inputsOf(h.now, b, takes),
          seed: 40 + b.takes.length + 2,
          art: artFor(`${b.name}${b.takes.length}`, b.kind),
        };
      }
      const made = Object.values(fresh);
      if (!made.length) return 0;
      setTakes((t) => ({ ...t, ...fresh }));
      setH((x) =>
        everywhere(x, (d) => ({
          ...d,
          blocks: d.blocks.map((b) => {
            const add = made.filter((t) => t.block === b.id).map((t) => t.id);
            return add.length ? { ...b, takes: [...b.takes, ...add] } : b;
          }),
        })),
      );
      return made.length;
    },
    saveSnapshot: (name) =>
      setSnapshots((s) => [
        { id: newId(), name, doc: h.now, at: new Date().toISOString(), rev: h.past.length },
        ...s,
      ]),
    restoreSnapshot: (s) => setH((x) => commit(x, s.doc!)),
    renameSnapshot: (s, name) =>
      setSnapshots((all) => all.map((x) => (x.id === s.id ? { ...x, name } : x))),
    deleteSnapshot: (s) => setSnapshots((all) => all.filter((x) => x.id !== s.id)),
    switchTo: (id) => switchTo(id),
    newBoard: () => {
      const b: Saved = {
        id: newId(),
        name: "Untitled board",
        doc: emptyDoc(),
        takes: {},
        snapshots: [],
      };
      switchTo(b.id, { ...store, boards: [...store.boards, b] });
    },
    sampleBoard: () => {
      const b = sample();
      switchTo(b.id, { ...store, boards: [...store.boards, b] });
    },
    rename: (name) =>
      setStore((s) => ({
        ...s,
        boards: s.boards.map((b) => (b.id === saved.id ? { ...b, name } : b)),
      })),
    remove: () => {
      const rest = store.boards.filter((b) => b.id !== saved.id);
      if (rest.length) switchTo(rest[0].id, { ...store, boards: rest });
    },
  };
}
