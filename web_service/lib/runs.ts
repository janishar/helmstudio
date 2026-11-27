// Folding a session's events (SSE or ?since= pages) into its runs. Events are hints: a run that ends is fetched again.
import { api, ok, okOr, type S } from "@/lib/api";
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
        // the host that stopped, so "Runner on mac-studio stopped" shows before the run is read again
        lost_host: run.host ?? run.lost_host,
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
  } else if (e.type === "output" && !run.outputs.some((o) => o.index === e.data.index)) {
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

/** A render time as low text, "⏱ 1:14" ("⏱ 1:02:05" from an hour); "" without one (API 1.60, MadeWith.render_s). */
export const renderTime = (s?: number | null) => {
  if (s == null) return "";
  const t = Math.max(0, Math.round(s));
  return `⏱ ${t >= 3600 ? `${Math.floor(t / 3600)}:${clock(t % 3600).padStart(5, "0")}` : clock(t)}`;
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
 * `steps` param as the run set it when the stage fixes it (`values`), shown while it waits; `bar`: a step / of bar while active (a phase or Saving
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

/** The strip's last piece on an image, video, music or sound workflow: the upload of the output (docs/mock/CreateMediaRefs.dc.html). */
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
  // music and sound save their file too (docs/mock/CreateMusicSound.dc.html: Plan, Compose, Decode, Saving); a voice
  // has no Saving piece (CreateAudio.dc.html)
  const saving =
    w.media === "audio" ? w.sound === "music" || w.sound === "sound" : w.media !== "text";
  // an output node (no outputs of its own): its report is Saving's
  const sink = (id: string) => saving && !!schema(id) && !schema(id).outputs.length;
  const order = graphOrder(w).filter((id) => {
    const s = schema(id);
    if (sink(id)) return false;
    if (id in nodes || id === run.progress?.node) return true;
    // a node that only hands a Model on (a LoRA stack, the IC-LoRA of Recipe: Detail) does no work of its own: its
    // load shows under the sampler that uses it (docs/mock/LtxFeatures.dc.html frame 1)
    const modelOnly = [...(s?.inputs ?? []), ...(s?.outputs ?? [])].every(
      (x) => x.type === "Model",
    );
    return !!s?.inputs.length && !!s.outputs.length && !modelOnly && !emptyStack(w, id);
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
    // only a count the stage fixes (its `values`: LTX's Sample 2 · 3 steps); one the form sets is not repeated
    // (Qwen's Sample, docs/mock/Main.dc.html)
    const fixed = schema(node)?.params.some((x) => x.name === "steps" && x.values);
    const asked =
      phase || !fixed ? undefined : (run.inputs[`${node}.steps`] ?? w.nodes[node]?.params?.steps);
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

/** The stage's progress text: the share done ("62%"), or a phase without steps (1 of 1, SGLang's "starting sglang")
 * by its text alone: its 1 / 1 is no share of the work. */
export const progressText = (r: Run) => {
  const p = r.progress;
  return p && p.of <= 1 && p.phase ? p.phase : `${Math.round(fraction(r) * 100)}%`;
};

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
  onPaused?: Paused,
): () => void {
  return followEvents(
    `/api/v1/studio/sessions/${encodeURIComponent(sessionId)}/events`,
    ["status", "progress", "output", "session"],
    onEvent,
    page,
    onPaused,
  );
}

/** Live updates paused (docs/mock/ErrorStates.dc.html d): true once the stream closed and its ?since= polling has
 * failed for 5 s, false when a page or a stream answers; `now` tries the stream at once. */
export type Paused = (paused: boolean, now: () => void) => void;
/** How long polling fails before "Live updates paused" shows. */
const PAUSE_AFTER = 5_000;

/** followSession for any event log of the API: `url` serves SSE (`?since=`) with the named event types, `page`
 * its JSON form. */
export function followEvents<E extends { seq: number }>(
  url: string,
  types: string[],
  onEvent: (e: E) => void,
  page: (since: number) => Promise<{ events: E[] }>,
  onPaused?: Paused,
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
  let failing = false; // polling's pages fail
  let paused = false;
  let pauseTimer: ReturnType<typeof setTimeout> | undefined;
  const now = () => {
    if (stop) return;
    clearTimeout(poll);
    poll = undefined;
    delay = POLL_MIN;
    connect();
    if (polling) void tick();
  };
  const setPaused = (on: boolean) => {
    if (on === paused || stop) return;
    paused = on;
    onPaused?.(on, now);
  };
  const answered = () => {
    failing = false;
    clearTimeout(pauseTimer);
    setPaused(false);
  };
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
    if (p) answered();
    else if (!failing) {
      failing = true;
      pauseTimer = setTimeout(() => failing && setPaused(true), PAUSE_AFTER);
    }
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
      answered();
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
    clearTimeout(pauseTimer);
    if (typeof document !== "undefined")
      document.removeEventListener("visibilitychange", onVisible);
  };
}

/** A run its tile's ✕ and Clear may remove (plan 21 25.3): failed or stopped, not a batch's (it goes with its batch). */
export const removable = (r: Run) =>
  (r.status === "failed" || r.status === "cancelled") && !r.batch_id;

/** "N failed · Clear": how many (failed and stopped together) and its title ("1 failed, 1 stopped"). */
export function failedCount(runs: Run[]): { count: number; title: string } {
  const list = runs.filter(removable);
  const failed = list.filter((r) => r.status === "failed").length;
  const stopped = list.length - failed;
  return {
    count: list.length,
    title: [failed && `${failed} failed`, stopped && `${stopped} stopped`]
      .filter(Boolean)
      .join(", "),
  };
}

/** Deletes finished runs one by one (Clear is at most 100 a session; no bulk endpoint), calling `gone` for each; a
 * run already gone (404) counts as deleted. */
export async function deleteRuns(ids: string[], gone: (id: string) => void) {
  for (const id of ids.slice(0, 100)) {
    await okOr(api.DELETE("/v1/studio/runs/{run_id}", { params: { path: { run_id: id } } }), 404);
    gone(id);
  }
}

// ---- error states (docs/mock/ErrorStates.dc.html c1, c2, e)

/** A run's tries: attempt 2 is the last (studio requeues a lost run once, dispatcher.go lose). */
export const MAX_ATTEMPTS = 2;

/** What to do for a runner of `env`: "Start helmstudio on your Mac" (mlx), else "Start a runner for torch". */
export const startHint = (env: string) =>
  env === "mlx" ? "Start helmstudio on your Mac" : `Start a runner for ${env}`;

/** A failed run in plain words (board c2), from studio's raw error; `label` names a host as Run on does ("Mac").
 * The raw text stays under Show details. */
export function failWords(
  error: string | null | undefined,
  label: (host: string) => string = (h) => h,
) {
  const e = error ?? "";
  let m: RegExpExecArray | null;
  if ((m = /runner on host (\S+) stopped responding; gave up after \d+ attempts/.exec(e)))
    return `The runner on ${m[1]} stopped twice. Retry starts it again.`;
  if ((m = /runner on host (\S+) stopped responding; no other live host for env (\S+)/.exec(e)))
    return `The runner on ${m[1]} stopped and no other runner for ${m[2]} is on. ${startHint(m[2])}, then Retry.`;
  if ((m = /host (\S+) went offline; a run set to \S+ never moves/.exec(e)))
    return `${m[1]} went offline. A run set to ${label(m[1])} never moves to another machine. Pick Auto, or Retry when it’s back.`;
  if ((m = /^no live host for env (\S+)$/.exec(e)))
    return `No runner for ${m[1]} was on when this run started. ${startHint(m[1])}, then Retry.`;
  return GENERIC_FAIL;
}
/** failWords for an error it has no plain words for. */
export const GENERIC_FAIL = "This run stopped with an error.";

/** The raw error under Show details: "run 7f3c2a · attempt 2 of 2" over studio's text. */
export const failDetails = (r: Pick<Run, "id" | "attempt" | "error">) =>
  `run ${r.id.slice(0, 6)} · attempt ${r.attempt} of ${MAX_ATTEMPTS}\n${r.error ?? "no error text"}`;

/** A queued run that waits (board c1, e): the progress line, the tile's two lines, and `retry` for the amber
 * trying-again look; null when it is not waiting on anything to say. */
export function waitText(r: Run): { line: string; tile: [string, string]; retry: boolean } | null {
  if (r.status !== "queued") return null;
  if (r.attempt >= 2)
    return {
      line: `Runner${r.lost_host ? ` on ${r.lost_host}` : ""} stopped · trying again (${r.attempt} of ${MAX_ATTEMPTS})`,
      tile: ["Trying again", `${r.attempt} of ${MAX_ATTEMPTS}`],
      retry: true,
    };
  if (!r.host) return null;
  const ahead = (r.position ?? 1) - 1;
  if (ahead > 0)
    return {
      line: `Waiting · ${ahead} ahead on ${r.host}`,
      tile: ["Waiting", `${ahead} ahead`],
      retry: false,
    };
  // first in line but a model unloads first; else it is about to start: plain "Queued"
  return r.unloads_pinned?.length
    ? { line: `Waiting for ${r.host}`, tile: ["Waiting", ""], retry: false }
    : null;
}
