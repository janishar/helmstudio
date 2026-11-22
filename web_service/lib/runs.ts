// Folding a session's events (SSE or ?since= pages) into its runs. Events are hints: a run that ends is fetched again.
import { api, ok, type S } from "@/lib/api";
import { graphOrder, nodeTitle } from "@/lib/workflow";
import { emptyStack } from "@/lib/loras";

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
export function lastDuration(
  runs: Run[],
  workflowId: string,
  bypass: Record<string, boolean> = {},
  host?: string,
): number | null {
  // with `bypass` (a choice's switches, LTX One-stage): only a run that switched the same nodes off; with `host`
  // (Run on): only a run on it
  const r = runs.find(
    (x) =>
      x.workflow_id === workflowId &&
      (!host || x.host === host) &&
      x.status === "succeeded" &&
      x.started_at &&
      x.finished_at &&
      Object.entries(bypass).every(([k, on]) => (x.inputs?.[k] === true) === on),
  );
  return r ? (Date.parse(r.finished_at!) - Date.parse(r.started_at!)) / 1000 : null;
}

/** "unloads pinned transformer, reloads ≈ 6.8 GB after" for a queued run whose admission would take pinned entries
 * (Run.unloads_pinned, contracts "Hosts admin": Generate says so first); "" otherwise. */
export function pinnedNote(r?: Run): string {
  const p = r?.status === "queued" ? (r.unloads_pinned ?? []) : [];
  if (!p.length) return "";
  const names = [
    ...new Set(p.map((x) => (x.component === "vae" ? "VAE" : x.component.replaceAll("_", " ")))),
  ];
  const gb = Math.round(p.reduce((s, x) => s + x.gb, 0) * 10) / 10;
  return `unloads pinned ${names.join(", ")}, reloads ≈ ${gb} GB after`;
}

/** When a run's nodes reported: the run's start and end (its first running and its ending status), per node its
 * first and last progress (ms), and per phase (`<node>:<phase_id>`, API 1.36) the same. */
export type RunTrack = {
  start?: number;
  end?: number;
  nodes: Record<string, { first: number; last: number }>;
  phases?: Record<string, { first: number; last: number }>;
};

/** The tracks by run id after one event. */
export function track(t: Record<string, RunTrack>, e: S<"SessionEvent">): Record<string, RunTrack> {
  const ts = Date.parse(e.ts);
  const r = t[e.run_id] ?? { nodes: {} };
  if (e.type === "status" && e.data.status === "running" && r.start == null)
    return { ...t, [e.run_id]: { ...r, start: ts } };
  if (e.type === "status" && !isActive(e.data.status) && r.end == null)
    return { ...t, [e.run_id]: { ...r, end: ts } };
  if (e.type !== "progress") return t;
  const n = r.nodes[e.data.node];
  const k = `${e.data.node}:${e.data.phase_id}`;
  const ph = e.data.phase_id ? r.phases?.[k] : undefined;
  return {
    ...t,
    [e.run_id]: {
      ...r,
      nodes: { ...r.nodes, [e.data.node]: { first: n?.first ?? ts, last: ts } },
      ...(e.data.phase_id && {
        phases: { ...r.phases, [k]: { first: ph?.first ?? ts, last: ts } },
      }),
    },
  };
}

/** One piece of the run's progress strip. `key`: the node id, `<node>:<phase id>` for a phase of a node with
 * `phases` (API 1.36), or "saving"; `seconds`: how long a done piece took, when its events say; `steps`: the node's
 * `steps` param as the run set it, shown while it waits; `bar`: a step / of bar while active (a phase or Saving
 * shows one only when `of` > 1). */
export type Segment = {
  key: string;
  node: string;
  title: string;
  state: "done" | "active" | "stopped" | "pending" | "bypassed";
  seconds?: number;
  steps?: number;
  bar?: boolean;
};

/** The strip's last piece on an image or video workflow: the upload of the output (docs/mock/CreateMediaRefs.dc.html). */
export const SAVING = "saving";

/** A node's name in the strip and on the stage: its schema's title, numbered in graph order when the graph has
 * several nodes of its kind ("Sample 1", "Sample 2"). */
export function nodeLabel(w: S<"Workflow"> | undefined, node: string): string {
  const kind = w?.nodes[node]?.kind;
  const title = nodeTitle(w, node);
  if (!w || !kind) return title;
  const twins = graphOrder(w).filter((id) => w.nodes[id].kind === kind);
  return twins.length > 1 ? `${title} ${twins.indexOf(node) + 1}` : title;
}

/** The progress strip: the nodes that do work (take inputs and give outputs, and are not an empty LoRA stack, or
 * reported progress), in graph order, a node with `phases` as one piece per phase; on an image or video workflow a
 * last "Saving" piece for its output nodes (they report `saving` while an upload runs, contracts/README.md Phases);
 * the stepping piece is active (stopped if the run ended there); those before it, or up to the furthest piece that
 * reported (a node may report again after a later one), are done; the rest pending. A done piece's time runs from the
 * previous piece's last report to its own (Saving's to the run's end); after a piece that never reported, from its
 * own first report. */
export function segments(w: S<"Workflow">, run: Run, t?: RunTrack): Segment[] {
  const nodes = t?.nodes ?? {};
  const schema = (id: string) => w.node_schemas[w.nodes[id]?.kind];
  const saving = w.media !== "audio" && w.media !== "text";
  // an output node (no outputs of its own): its report is Saving's
  const sink = (id: string) => saving && !!schema(id) && !schema(id).outputs.length;
  const order = graphOrder(w).filter((id) => {
    const s = schema(id);
    if (sink(id)) return false;
    if (id in nodes || id === run.progress?.node) return true;
    return !!s?.inputs.length && !!s.outputs.length && !emptyStack(w, id);
  });
  // a node the graph does not have still gets its piece, at the end
  for (const id of Object.keys(nodes)) if (!order.includes(id) && !sink(id)) order.push(id);
  const p = run.progress;
  if (p && !order.includes(p.node) && !sink(p.node)) order.push(p.node);
  const pieces = order.flatMap((node) => {
    const ph = schema(node)?.phases ?? [];
    return ph.length
      ? ph.map((x) => ({ key: `${node}:${x.id}`, node, title: x.label, phase: true }))
      : [{ key: node, node, title: nodeLabel(w, node), phase: false }];
  });
  const reports: Record<string, { first: number; last: number }> = { ...nodes, ...t?.phases };
  const sinks = graphOrder(w).filter(sink);
  if (saving) {
    pieces.push({ key: SAVING, node: sinks.at(-1) ?? SAVING, title: "Saving", phase: true });
    const rs = sinks.flatMap((id) => (nodes[id] ? [nodes[id]] : []));
    if (rs.length)
      reports[SAVING] = {
        first: Math.min(...rs.map((r) => r.first)),
        last: Math.max(...rs.map((r) => r.last)),
      };
  }
  const phased = (node: string) => !!schema(node)?.phases?.length;
  const curKey = !p
    ? undefined
    : sink(p.node)
      ? SAVING
      : phased(p.node)
        ? `${p.node}:${p.phase_id ?? schema(p.node).phases![0].id}`
        : p.node;
  // a phase the schema does not list still gets its piece, after its node's
  if (curKey && !pieces.some((x) => x.key === curKey))
    pieces.splice(pieces.findLastIndex((x) => x.node === p!.node) + 1 || pieces.length, 0, {
      key: curKey,
      node: p!.node,
      title: p!.phase ?? nodeLabel(w, p!.node),
      phase: true,
    });
  const cur =
    run.status === "succeeded"
      ? pieces.length
      : curKey
        ? pieces.findIndex((x) => x.key === curKey)
        : -1;
  const seen = pieces.reduce((m, x, i) => (x.key in reports ? i : m), -1);
  let prevEnd = t?.start;
  return pieces.map(({ key, node, title, phase }, i) => {
    const n = reports[key];
    const base = { key, node, title };
    // a bypassed node did no work: struck through, as the mock's Enhance (docs/mock/Main.dc.html); it leaves the
    // next node's start where it was
    if ((run.inputs[`${node}.bypass`] ?? w.nodes[node]?.bypass) === true && !n)
      return { ...base, state: "bypassed" };
    // Saving ends with the run (an upload under 2 s never reports)
    const last = key === SAVING && run.status === "succeeded" ? (t?.end ?? n?.last) : n?.last;
    // a piece that started before the previous one ended (the graph order is not the run order: LTX's Encode
    // before Source audio) is timed alone, never negative
    const from = n && prevEnd != null && prevEnd > n.first ? n.first : (prevEnd ?? n?.first);
    const seconds = last != null && from != null ? (last - from) / 1000 : undefined;
    // a silent node's time is not known: don't add it to the next node's
    prevEnd = last;
    const asked = phase ? undefined : (run.inputs[`${node}.steps`] ?? w.nodes[node]?.params?.steps);
    const steps = typeof asked === "number" ? asked : undefined;
    if (i === cur)
      return {
        ...base,
        state: isActive(run.status) ? "active" : "stopped",
        bar: !phase || (p?.of ?? 0) > 1,
      };
    if (i < cur || i <= seen) return { ...base, state: "done", seconds };
    return { ...base, state: "pending", steps };
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

/** Polling's pace: every 2 s while pages come, doubling to 30 s while they fail. */
const POLL_MIN = 2_000;
const POLL_MAX = 30_000;
const hidden = () => typeof document !== "undefined" && document.hidden;

/** Calls `onEvent` for each new event of the session (by seq), from the start of its log: SSE, else ?since= polling
 * once the stream closes (a refused reconnect, e.g. a 502 while studio restarts). Polling backs off while pages fail,
 * pauses in a hidden tab (one page as soon as it shows again) and, after a page comes, tries a stream from ?since= the
 * last event again (backing off likewise), stopping once one opens. Returns the unsubscribe; nothing is delivered
 * after it, not even a page in flight. */
export function followSession(
  sessionId: string,
  onEvent: (e: S<"SessionEvent">) => void,
  page: (since: number) => Promise<S<"EventPage">> = (since) => eventPage(sessionId, since),
): () => void {
  return followEvents(
    `/api/v1/studio/sessions/${encodeURIComponent(sessionId)}/events`,
    ["status", "progress", "output"],
    onEvent,
    page,
  );
}

/** followSession for any event log of the API: `url` serves SSE (`?since=`) with the named event types, `page`
 * its JSON form. */
export function followEvents<E extends { seq: number }>(
  url: string,
  types: string[],
  onEvent: (e: E) => void,
  page: (since: number) => Promise<{ events: E[] }>,
): () => void {
  // ponytail: replays the whole log on open (cheap for M0 sessions); keep a cursor per session if logs grow long
  let last = 0;
  let stop = false;
  let es: EventSource | undefined;
  let polling = false; // the stream closed: pages until a new one opens
  let busy = false; // a page is in flight
  let poll: ReturnType<typeof setTimeout> | undefined;
  let delay = POLL_MIN;
  let streamDelay = POLL_MIN;
  let streamAt = 0; // when a new stream may be tried
  const take = (e: E) => {
    if (stop || e.seq <= last) return;
    last = e.seq;
    onEvent(e);
  };
  const onMessage = (m: MessageEvent<string>) => {
    try {
      take(JSON.parse(m.data) as E);
    } catch {
      /* not an event */
    }
  };
  const tick = async () => {
    poll = undefined;
    if (stop || !polling || busy || hidden()) return; // a hidden tab waits for visibilitychange
    busy = true;
    const p = await page(last).catch(() => null);
    busy = false;
    if (stop || !polling) return;
    p?.events.forEach(take);
    delay = p ? POLL_MIN : Math.min(delay * 2, POLL_MAX);
    if (p && Date.now() >= streamAt) {
      streamAt = Date.now() + streamDelay;
      streamDelay = Math.min(streamDelay * 2, POLL_MAX);
      connect();
    }
    if (polling) poll = setTimeout(() => void tick(), delay);
  };
  const onVisible = () => {
    if (!hidden() && polling && !poll) void tick();
  };
  function connect() {
    es?.close();
    const s = new EventSource(`${url}?since=${last}`);
    for (const t of types) s.addEventListener(t, onMessage as EventListener);
    s.onopen = () => {
      if (stop || s !== es) return;
      polling = false;
      streamDelay = POLL_MIN;
      clearTimeout(poll);
      poll = undefined;
    };
    s.onerror = () => {
      if (s.readyState !== EventSource.CLOSED || stop || s !== es || polling) return;
      polling = true;
      delay = POLL_MIN;
      void tick();
    };
    es = s;
  }
  connect();
  if (typeof document !== "undefined") document.addEventListener("visibilitychange", onVisible);
  return () => {
    stop = true;
    es?.close();
    clearTimeout(poll);
    if (typeof document !== "undefined")
      document.removeEventListener("visibilitychange", onVisible);
  };
}
