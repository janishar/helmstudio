// A small studio for the Magic page's tests: the boards in memory behind a mock of lib/api, edits applied with
// lib/magic.ts as studio does, the sample board ("The Last Tide") as studio would keep it, runs that go when asked and
// finish on `finishRuns()` (their takes join their blocks, a session event says so). Each test file mocks with it:
//   vi.mock("@/lib/api", async (orig) => ({ ...(await orig()), api: (await import("./magic-api")).api }));
//   vi.mock("@/lib/runs", async (orig) => ({ ...(await orig()), followSession: (await import("./magic-api")).followSession }));
// Only lib/magic and the fixture are imported here (they import no lib/api), so the mock never waits on itself.
import { apply, EST_S, inputsOf, type Doc, type Kind } from "@/lib/magic";
import { emptyDoc, sampleBoard } from "@/lib/magic-fixture";

type Run = {
  block: string;
  run_id: string;
  status: "queued" | "running" | "succeeded" | "failed" | "cancelled";
  progress?: number;
  takes: string[];
  seed: number;
  current: boolean;
  by: "user" | "assistant";
  created_at: string;
  render_s: number | null;
  turn?: number;
};
type AssetRun = {
  asset: string;
  view: string;
  run_id: string;
  status: Run["status"];
  error: null;
  takes: string[];
  created_at: string;
};
type Take = {
  id: string;
  kind: "image" | "video" | "audio";
  url: string;
  thumb_url?: string;
  sound?: string;
};
type Snap = { id: string; name: string; revision: number; made_at: string; blocks: number };
export type Wire = {
  id: string;
  name: string;
  revision: number;
  /** the undo history: docs[at] is the board */
  docs: Doc[];
  at: number;
  runs: Run[];
  assetRuns: AssetRun[];
  takes: Record<string, Take>;
  snapshots: Snap[];
};

export const studio = {
  boards: [] as Wire[],
  calls: [] as { method: string; path: string; body?: unknown }[],
  onEvent: null as ((e: { type: string; data?: unknown }) => void) | null,
};
let n = 0;
const AT = "2026-11-27T05:00:00Z";

export function reset() {
  studio.boards = [];
  studio.calls = [];
  studio.onEvent = null;
  n = 0;
}

const takeKind = (k: Kind): Take["kind"] =>
  k === "video" ? "video" : k === "image" || k === "frame" ? "image" : "audio";
const takeOf = (id: string, k: Kind): Take => ({
  id,
  kind: takeKind(k),
  url: `https://x/${id}`,
  ...(takeKind(k) !== "audio" && { thumb_url: `https://x/${id}.webp` }),
  ...(k === "music" && { sound: "music" }),
});
const docOf = (b: Wire) => b.docs[b.at];

function add(name: string, doc: Doc): Wire {
  const b: Wire = {
    id: `b${studio.boards.length + 1}`,
    name,
    revision: 1,
    docs: [doc],
    at: 0,
    runs: [],
    assetRuns: [],
    takes: {},
    snapshots: [],
  };
  studio.boards.push(b);
  return b;
}

/** The sample board as studio keeps it (as the Sample board menu item makes it, with its takes): its takes as runs (Shot 3's made before Shot 2 changed: out of date; Shot
 * 2's third still running at 62%); `change` alters its document first. */
export function seedSample(change?: (d: Doc) => void): Wire {
  const s = sampleBoard();
  const doc = s.doc;
  change?.(doc);
  const b = add(s.name, doc);
  for (const t of Object.values(s.takes)) {
    const block = doc.blocks.find((x) => x.id === t.block)!;
    const run: Run = {
      block: t.block,
      run_id: `r-${t.id}`,
      status: t.status === "running" ? "running" : "succeeded",
      takes: [],
      seed: t.seed ?? 7,
      current: true,
      by: "user",
      created_at: AT,
      render_s: null,
    };
    if (t.status === "running") {
      run.progress = t.progress;
      block.takes = block.takes.filter((x) => x !== t.id);
    } else {
      run.takes = [t.id];
      run.current = t.inputs === inputsOf(doc, block, s.takes);
      b.takes[t.id] = takeOf(t.id, block.kind);
    }
    b.runs.push(run);
  }
  // its models are names (lib/magic-fixture's); studio keeps workflow ids, so each kind's first model runs
  for (const x of doc.blocks) delete x.request.model;
  return b;
}

/** Every running run succeeds with a take on its block (a studio write: every document in the history), and the
 * session says so. */
export function finishRuns() {
  for (const b of studio.boards) {
    for (const r of b.runs.filter((x) => x.status === "running" || x.status === "queued")) {
      const kind = docOf(b).blocks.find((x) => x.id === r.block)?.kind ?? "video";
      const id = `${r.block}-t${++n}`;
      r.status = "succeeded";
      r.progress = 100;
      r.takes = [id];
      b.takes[id] = takeOf(id, kind);
      b.docs = b.docs.map((d) => ({
        ...d,
        blocks: d.blocks.map((x) => (x.id === r.block ? { ...x, takes: [...x.takes, id] } : x)),
      }));
      b.revision++;
    }
    for (const r of b.assetRuns.filter((x) => x.status === "running")) {
      const id = `${r.asset}-v${++n}`;
      r.status = "succeeded";
      r.takes = [id];
      b.takes[id] = takeOf(id, "image");
      b.docs = b.docs.map((d) => ({
        ...d,
        assets: d.assets.map((a) =>
          a.id === r.asset
            ? { ...a, sheet: [...a.sheet, id], views: { ...a.views, [id]: r.view } }
            : a,
        ),
      }));
      b.revision++;
    }
  }
  studio.onEvent?.({ type: "run" });
}

export const followSession = (_: string, f: (e: { type: string }) => void) => {
  studio.onEvent = f;
  return () => void (studio.onEvent = null);
};

const answer = (b: Wire) => ({
  id: b.id,
  name: b.name,
  revision: b.revision,
  doc: docOf(b),
  can_undo: b.at > 0,
  can_redo: b.at < b.docs.length - 1,
  created_at: AT,
  updated_at: AT,
  title_source: "user",
  session_id: b.runs.length || b.assetRuns.length ? `sess-${b.id}` : null,
  runs: b.runs,
  asset_runs: b.assetRuns,
  takes: b.takes,
});
const res = (status: number, data?: unknown) => ({
  data: status < 300 ? data : undefined,
  error: status >= 300 ? data : undefined,
  response: new Response(null, { status: status === 204 ? 204 : status }),
});
type Opts = {
  params?: { path?: Record<string, string>; query?: unknown };
  body?: Record<string, unknown>;
};
const find = (o?: Opts) => studio.boards.find((b) => b.id === o?.params?.path?.board_id);
/** an edit, an undo step: what was undone goes */
const write = (b: Wire, doc: Doc) => {
  b.docs = [...b.docs.slice(0, b.at + 1), doc];
  b.at = b.docs.length - 1;
  b.revision++;
};
const stale = (b: Wire, rev: unknown) =>
  rev !== b.revision
    ? res(409, { detail: `board changed since you opened it (now revision ${b.revision})` })
    : null;

/** starts runs of these blocks: a dialogue's one per line */
function start(b: Wire, ids: string[]) {
  for (const id of ids) {
    const x = docOf(b).blocks.find((y) => y.id === id);
    if (!x) continue;
    const turns = x.request.turns?.map((_, i) => i) ?? [undefined];
    for (const turn of turns)
      b.runs.push({
        block: id,
        run_id: `r${++n}`,
        status: "running",
        progress: 40,
        takes: [],
        seed: 7,
        current: true,
        by: "user",
        created_at: AT,
        render_s: null,
        ...(turn != null && { turn }),
      });
  }
}

export const api = {
  GET: async (path: string, o?: Opts) => {
    studio.calls.push({ method: "GET", path });
    const b = find(o);
    switch (path) {
      case "/v1/studio/magic/boards":
        return res(200, {
          boards: [...studio.boards].reverse().map((x) => ({ id: x.id, name: x.name })),
        });
      case "/v1/studio/magic/kinds":
        return res(200, (await import("@/lib/magic-standard")).FAKE_KINDS);
      case "/v1/studio/magic/settings":
        return res(200, { ask_above_gpu_minutes: 5 });
      case "/v1/studio/magic/boards/{board_id}":
        return b ? res(200, answer(b)) : res(404, { detail: "Not found." });
      case "/v1/studio/magic/boards/{board_id}/snapshots":
        return res(200, { snapshots: b?.snapshots ?? [] });
      case "/v1/studio/magic/boards/{board_id}/sends":
        return res(200, { sends: [] });
      case "/v1/studio/magic/boards/{board_id}/revisions":
        return res(200, { revisions: [] });
      case "/v1/studio/hosts":
        return res(200, { hosts: [] });
    }
    return res(404, { detail: "Not found." });
  },
  POST: async (path: string, o: Opts = {}) => {
    studio.calls.push({ method: "POST", path, body: o.body });
    const body = o.body ?? {};
    if (path === "/v1/studio/magic/boards") {
      const d = (body.doc as Doc | undefined) ?? emptyDoc();
      const nb = add((body.name as string) || "Untitled board", {
        ...d,
        ...(body.target ? { target: body.target as Doc["target"] } : {}),
      });
      return res(201, answer(nb));
    }
    if (path === "/v1/studio/runs/{run_id}/cancel") {
      for (const x of studio.boards)
        for (const r of x.runs)
          if (r.run_id === o.params?.path?.run_id && r.status === "running") r.status = "cancelled";
      return res(202, {});
    }
    const b = find(o);
    if (!b) return res(404, { detail: "Not found." });
    switch (path) {
      case "/v1/studio/magic/boards/{board_id}/edits": {
        const refused = stale(b, body.base_revision);
        if (refused) return refused;
        let d = docOf(b);
        for (const [i, op] of (body.ops as never[]).entries()) {
          const r = apply(d, op);
          if ("error" in r) return res(422, { detail: r.error, op_index: i });
          d = r.doc;
        }
        write(b, d);
        return res(200, { revision: b.revision, board: answer(b) });
      }
      case "/v1/studio/magic/boards/{board_id}/undo":
      case "/v1/studio/magic/boards/{board_id}/redo":
        b.at += path.endsWith("/undo") ? -1 : 1;
        b.revision++;
        return res(200, answer(b));
      case "/v1/studio/magic/boards/{board_id}/run": {
        const refused = stale(b, body.revision);
        if (refused) return refused;
        const ids = body.stale
          ? docOf(b)
              .blocks.filter((x) => !b.runs.some((r) => r.block === x.id && r.current))
              .map((x) => x.id)
          : (body.blocks as string[]);
        start(b, ids);
        b.revision++;
        return res(202, {
          board: answer(b),
          started: ids.map((block) => ({ block })),
          skipped: [],
        });
      }
      case "/v1/studio/magic/boards/{board_id}/assets/{asset_id}/views": {
        const refused = stale(b, body.revision);
        if (refused) return refused;
        for (const view of body.views as string[])
          b.assetRuns.push({
            asset: o.params!.path!.asset_id,
            view,
            run_id: `r${++n}`,
            status: "running",
            error: null,
            takes: [],
            created_at: AT,
          });
        b.revision++;
        return res(202, { board: answer(b) });
      }
      case "/v1/studio/magic/boards/{board_id}/plan": {
        const ids = (body.blocks as string[] | undefined) ?? docOf(b).blocks.map((x) => x.id);
        const blocks = docOf(b)
          .blocks.filter((x) => ids.includes(x.id))
          .map((x) => ({
            block: x.id,
            seconds: EST_S[x.kind],
            source: "yours",
            host: "mac-studio",
            peak_gb: null,
            reason: null,
          }));
        const seconds = blocks.reduce((s, x) => s + x.seconds, 0);
        return res(200, {
          blocks,
          lanes: [],
          seconds,
          order: [],
          ask: false,
          ask_text: "",
          limit_minutes: 5,
        });
      }
      case "/v1/studio/magic/boards/{board_id}/snapshots": {
        const s = {
          id: `snap${++n}`,
          name: body.name as string,
          revision: b.revision,
          made_at: AT,
          blocks: docOf(b).blocks.length,
        };
        b.snapshots.unshift(s);
        return res(201, s);
      }
    }
    return res(404, { detail: "Not found." });
  },
  PATCH: async (path: string, o: Opts = {}) => {
    studio.calls.push({ method: "PATCH", path, body: o.body });
    const b = find(o);
    if (b && path === "/v1/studio/magic/boards/{board_id}") {
      b.name = o.body!.name as string;
      return res(200, answer(b));
    }
    return res(404, { detail: "Not found." });
  },
  PUT: async (path: string, o: Opts = {}) => {
    studio.calls.push({ method: "PUT", path, body: o.body });
    return res(200, o.body);
  },
  DELETE: async (path: string, o: Opts = {}) => {
    studio.calls.push({ method: "DELETE", path });
    if (path === "/v1/studio/magic/boards/{board_id}")
      studio.boards = studio.boards.filter((b) => b.id !== o.params?.path?.board_id);
    return res(204);
  },
};
