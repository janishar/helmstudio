// Folding a session's events (SSE or ?since= pages) into its runs. Events are hints: a run that ends is fetched again.
import type { S } from "@/lib/api";

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
 * is true when this event ended a run the list had as active (fetch that run for its final state). */
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
    if (RANK[e.data.status] > RANK[run.status]) {
      next = { ...run, status: e.data.status, error: e.data.error, host: e.data.host ?? run.host };
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

/** A fetched list merged with the local one: a run the events already moved further keeps its local state, and a
 * run only the local list has (just created) stays in front. */
export function mergeRuns(local: Run[], fetched: Run[]): Run[] {
  const byId = new Map(local.map((r) => [r.id, r]));
  const ids = new Set(fetched.map((r) => r.id));
  return [
    ...local.filter((r) => !ids.has(r.id)),
    ...fetched.map((f) => {
      const l = byId.get(f.id);
      return l && RANK[l.status] > RANK[f.status] ? l : f;
    }),
  ];
}

/** The share of the work done, 0..1, from the last progress event. */
export const fraction = (r: Run) => (r.progress ? Math.min(1, r.progress.step / r.progress.of) : 0);
