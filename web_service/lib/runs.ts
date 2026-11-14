// Folding a session's events (SSE or ?since= pages) into its runs. Events are hints: a run that ends is fetched again.
import { api, ok, type S } from "@/lib/api";
import { graphOrder, nodeTitle } from "@/lib/workflow";

type Run = S<"Run">;
type Status = S<"RunStatus">;

/** queued < running < any end, so a replayed or late event never moves a run backwards. */
const RANK: Record<Status, number> = {
  queued: 0,
  running: 1,
  succeeded: 2,
  failed: 2,
  cancelled: 2,
};

/** A run still waiting or working. */
export const isActive = (s: Status) => s === "queued" || s === "running";

/** The runs after one event. `known` is false for a run not in the list (made elsewhere: fetch the list); `ended`
 * is true when this event ended a run the list had as active, or put it back in the queue for a new attempt (fetch
 * that run for its final state or its place in line). */
export function applyEvent(
  runs: Run[],
  e: S<"SessionEvent">,
): { runs: Run[]; known: boolean; ended: boolean } {
  const i = runs.findIndex((r) => r.id === e.run_id);
  if (i < 0) return { runs, known: false, ended: false };
  const run = runs[i];
  let next = run;
  let ended = false;
  if (e.type === "status") {
    if ((e.data.attempt ?? 1) > run.attempt) {
      // the runner was lost and the run queued again: attempt 1's progress and outputs are gone
      next = {
        ...run,
        status: e.data.status,
        attempt: e.data.attempt ?? 1,
        error: e.data.error,
        host: null,
        progress: null,
        outputs: [],
      };
      ended = true;
    } else if (RANK[e.data.status] > RANK[run.status]) {
      next = {
        ...run,
        status: e.data.status,
        error: e.data.error,
        host: e.data.host ?? run.host,
        resolved: e.data.resolved ?? run.resolved,
      };
      ended = !isActive(e.data.status);
    }
  } else if (e.type === "progress") {
    if (isActive(run.status)) next = { ...run, status: "running", progress: e.data };
  } else if (!run.outputs.some((o) => o.index === e.data.index)) {
    next = { ...run, outputs: [...run.outputs, e.data].sort((a, b) => a.index - b.index) };
  }
  if (next === run) return { runs, known: true, ended: false };
  const out = runs.slice();
  out[i] = next;
  return { runs: out, known: true, ended };
}

/** A fetched list merged with the local one: a run the events already moved further (in the same or a later attempt)
 * keeps its local state, and a
 * run only the local list has (just created) stays in front. */
export function mergeRuns(local: Run[], fetched: Run[]): Run[] {
  const byId = new Map(local.map((r) => [r.id, r]));
  const ids = new Set(fetched.map((r) => r.id));
  return [
    ...local.filter((r) => !ids.has(r.id)),
    ...fetched.map((f) => {
      const l = byId.get(f.id);
      return l && l.attempt >= f.attempt && RANK[l.status] > RANK[f.status] ? l : f;
    }),
  ];
}

/** The seed the run was made with: what the runner resolved, else its first output's. */
export const seedOf = (r: Run) => r.resolved?.seed ?? r.outputs[0]?.seed;

/** Seconds as m:ss ("1:52"). */
export const clock = (s: number) => {
  const t = Math.max(0, Math.round(s));
  return `${Math.floor(t / 60)}:${String(t % 60).padStart(2, "0")}`;
};

/** "1:52 left" from the last progress event's ETA; "" without one. */
export const etaText = (r: Run) =>
  r.progress?.eta_s != null && isActive(r.status) ? `${clock(r.progress.eta_s)} left` : "";

/** How long the newest finished run of the workflow took (s), from the runs newest first; null when none did. */
export function lastDuration(runs: Run[], workflowId: string): number | null {
  const r = runs.find(
    (x) =>
      x.workflow_id === workflowId && x.status === "succeeded" && x.started_at && x.finished_at,
  );
  return r ? (Date.parse(r.finished_at!) - Date.parse(r.started_at!)) / 1000 : null;
}

/** When a run's nodes reported: the run's start and, per node, its first and last progress (ms). */
export type RunTrack = {
  start?: number;
  nodes: Record<string, { first: number; last: number }>;
};

/** The tracks by run id after one event. */
export function track(t: Record<string, RunTrack>, e: S<"SessionEvent">): Record<string, RunTrack> {
  const ts = Date.parse(e.ts);
  const r = t[e.run_id] ?? { nodes: {} };
  if (e.type === "status" && e.data.status === "running" && r.start == null)
    return { ...t, [e.run_id]: { ...r, start: ts } };
  if (e.type !== "progress") return t;
  const n = r.nodes[e.data.node];
  return {
    ...t,
    [e.run_id]: { ...r, nodes: { ...r.nodes, [e.data.node]: { first: n?.first ?? ts, last: ts } } },
  };
}

/** One piece of the run's progress strip. `seconds`: how long a done node took, when its events say. */
export type Segment = {
  node: string;
  title: string;
  state: "done" | "active" | "stopped" | "pending";
  seconds?: number;
};

/** The progress strip: the nodes that do work (take inputs and give outputs, or reported progress), in graph order;
 * the stepping node is active (stopped if the run ended there); those before it, or up to the furthest node that
 * reported (a node may report again after a later one), are done; the rest pending. A done node's time runs from the
 * previous node's last report to its own; after a node that never reported, from its own first report. */
export function segments(w: S<"Workflow">, run: Run, t?: RunTrack): Segment[] {
  const nodes = t?.nodes ?? {};
  const order = graphOrder(w).filter((id) => {
    const s = w.node_schemas[w.nodes[id].kind];
    return id in nodes || id === run.progress?.node || (!!s?.inputs.length && !!s.outputs.length);
  });
  // a node the graph does not have still gets its piece, at the end
  for (const id of Object.keys(nodes)) if (!order.includes(id)) order.push(id);
  if (run.progress && !order.includes(run.progress.node)) order.push(run.progress.node);
  const cur =
    run.status === "succeeded"
      ? order.length
      : run.progress
        ? order.indexOf(run.progress.node)
        : -1;
  const seen = order.reduce((m, id, i) => (id in nodes ? i : m), -1);
  let prevEnd = t?.start;
  return order.map((node, i) => {
    const n = nodes[node];
    const seconds = n ? (n.last - (prevEnd ?? n.first)) / 1000 : undefined;
    // a silent node's time is not known: don't add it to the next node's
    prevEnd = n?.last;
    const title = nodeTitle(w, node);
    if (i === cur) return { node, title, state: isActive(run.status) ? "active" : "stopped" };
    if (i < cur || i <= seen) return { node, title, state: "done", seconds };
    return { node, title, state: "pending" };
  });
}

/** The share of the work done, 0..1, from the last progress event. */
export const fraction = (r: Run) => (r.progress ? Math.min(1, r.progress.step / r.progress.of) : 0);

/** Fetches the session's events after `since` (the JSON form of the events route, no Accept: text/event-stream). */
const eventPage = (sessionId: string, since: number) =>
  ok(
    api.GET("/v1/studio/sessions/{session_id}/events", {
      params: { path: { session_id: sessionId }, query: { since } },
    }),
  ) as Promise<S<"EventPage">>;

/** Calls `onEvent` for each new event of the session (by seq), from the start of its log: SSE, else ?since= polling
 * every 2 s once the stream closes. Returns the unsubscribe; nothing is delivered after it, not even a page in flight. */
export function followSession(
  sessionId: string,
  onEvent: (e: S<"SessionEvent">) => void,
  page: (since: number) => Promise<S<"EventPage">> = (since) => eventPage(sessionId, since),
): () => void {
  // ponytail: replays the whole log on open (cheap for M0 sessions); keep a cursor per session if logs grow long
  let last = 0;
  let stop = false;
  let poll: ReturnType<typeof setTimeout> | undefined;
  const take = (e: S<"SessionEvent">) => {
    if (stop || e.seq <= last) return;
    last = e.seq;
    onEvent(e);
  };
  const es = new EventSource(
    `/api/v1/studio/sessions/${encodeURIComponent(sessionId)}/events?since=0`,
  );
  const onMessage = (m: MessageEvent<string>) => {
    try {
      take(JSON.parse(m.data) as S<"SessionEvent">);
    } catch {
      /* not an event */
    }
  };
  for (const t of ["status", "progress", "output"])
    es.addEventListener(t, onMessage as EventListener);
  const tick = async () => {
    (await page(last).catch(() => null))?.events.forEach(take);
    if (!stop) poll = setTimeout(() => void tick(), 2000);
  };
  es.onerror = () => {
    if (es.readyState === EventSource.CLOSED && !stop && !poll) void tick();
  };
  return () => {
    stop = true;
    es.close();
    clearTimeout(poll);
  };
}
