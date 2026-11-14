"use client";
// Pipelines (docs/mock/Pipelines.dc.html, plan 7.2): the header with New batch and the Recent batches table. Saved
// pipelines, their filter chips and the pipeline panel arrive with the pipeline editor (M2) and are left out.
import Link from "next/link";
import { useCallback, useEffect, useRef, useState } from "react";
import { ErrorNote, Info } from "@/components/kit";
import { NewBatch } from "@/components/pipelines/new-batch";
import { api, message, ok, type S } from "@/lib/api";
import { batchState, canPause, canResume, doneRows } from "@/lib/batches";
import { when } from "@/lib/format";
import { followEvents } from "@/lib/runs";

type Batch = S<"Batch">;

/** A page of the batches table. */
export const PAGE = 20;

/** The batch's events, as JSON (the polling form of the stream). */
export const batchEvents = (id: string, since: number) =>
  ok(
    api.GET("/v1/studio/batches/{batch_id}/events", {
      params: { path: { batch_id: id }, query: { since } },
    }),
  ) as Promise<S<"BatchEventPage">>;

/** Follows a batch's event log (SSE, else polling) while mounted; `types` picks the SSE event types. */
export function followBatch(
  id: string,
  types: ("batch" | "row")[],
  onEvent: (e: S<"BatchEvent">) => void,
) {
  return followEvents<S<"BatchEvent">>(
    `/api/v1/studio/batches/${encodeURIComponent(id)}/events`,
    types,
    onEvent,
    (since) => batchEvents(id, since),
  );
}

/** Pause, Resume or Stop: the batch as it is now. */
export function controlBatch(id: string, op: "pause" | "resume" | "stop") {
  const p = { params: { path: { batch_id: id } } };
  if (op === "pause") return api.POST("/v1/studio/batches/{batch_id}/pause", p);
  if (op === "resume") return api.POST("/v1/studio/batches/{batch_id}/resume", p);
  return api.POST("/v1/studio/batches/{batch_id}/stop", p);
}

const ended = (b: Batch) => ["done", "failed", "stopped"].includes(b.status);

/** The state chip of the mock: .st with its tone. */
export function StateChip({ batch }: { batch: Batch }) {
  const s = batchState(batch);
  return (
    <span className={`st ${s.tone && `st-${s.tone}`}`} title={batch.reason ?? undefined}>
      {s.label}
    </span>
  );
}

/** Rows done of total, with the bar: the accent once every row has ended, the second accent until then. */
export function RowsDone({ counts }: { counts: S<"BatchCounts"> }) {
  const done = doneRows(counts);
  return (
    <>
      <span className="prog" aria-hidden>
        <b
          style={{
            width: `${Math.round((done / counts.total) * 100)}%`,
            background: done === counts.total ? "var(--a1)" : "var(--a2)",
          }}
        />
      </span>
      {done} / {counts.total}
    </>
  );
}

export function Pipelines({
  initial,
  workflows,
}: {
  initial?: S<"BatchPage">;
  workflows?: S<"Workflow">[];
}) {
  const [batches, setBatches] = useState<Batch[] | null>(initial?.batches ?? null);
  const [cursor, setCursor] = useState<string | null>(initial?.next_cursor ?? null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState("");
  const [creating, setCreating] = useState(false);

  const load = useCallback(async () => {
    setError("");
    try {
      const p = await ok(api.GET("/v1/studio/batches", { params: { query: { limit: PAGE } } }));
      setBatches(p.batches);
      setCursor(p.next_cursor);
    } catch (e) {
      setError(message(e));
    }
  }, []);
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- only when the server's read failed
    if (!initial) void load();
  }, [initial, load]);

  async function more() {
    setBusy("more");
    try {
      let next = cursor;
      while (next) {
        const p = await ok(
          api.GET("/v1/studio/batches", { params: { query: { limit: 200, cursor: next } } }),
        );
        const ids = new Set(batches?.map((b) => b.id));
        setBatches((l) => [...(l ?? []), ...p.batches.filter((b) => !ids.has(b.id))]);
        next = p.next_cursor;
      }
      setCursor(null);
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy("");
    }
  }

  // live: the unfinished batches' own event logs (at most 5 per user), their `batch` events only
  const live = (batches ?? [])
    .filter((b) => !ended(b))
    .map((b) => b.id)
    .join(",");
  const put = (b: Batch) => setBatches((l) => l?.map((x) => (x.id === b.id ? b : x)) ?? l);
  const putRef = useRef(put);
  useEffect(() => {
    putRef.current = put;
  });
  useEffect(() => {
    if (!live) return;
    const stops = live
      .split(",")
      .map((id) => followBatch(id, ["batch"], (e) => e.type === "batch" && putRef.current(e.data)));
    return () => stops.forEach((s) => s());
  }, [live]);

  async function control(b: Batch, op: "pause" | "resume") {
    setBusy(b.id);
    setError("");
    try {
      put(await ok(controlBatch(b.id, op)));
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy("");
    }
  }

  return (
    <main id="main" className="flex min-w-0 flex-1 flex-col gap-4 px-5 pt-4 pb-6">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="m-0 flex items-center font-display text-[22px] font-bold tracking-[0.01em]">
          Pipelines
          <Info>
            A batch runs one workflow once per row of inputs, unattended. It always runs the version
            it was submitted with.
          </Info>
        </h1>
        <div className="grow" />
        <button
          type="button"
          className="key h-10 rounded-xl px-[18px] text-[14px] tracking-[0.02em]"
          onClick={() => setCreating(true)}
        >
          New batch
        </button>
      </div>

      {error && <ErrorNote onRetry={batches ? undefined : load}>{error}</ErrorNote>}

      <div className="flex items-center gap-2">
        <h2 className="heading flex items-center">
          Recent batches
          <Info>
            Row 1 runs first, alone; then every row. Three failures in a row with the same error
            pause a batch.
          </Info>
        </h2>
        <div className="grow" />
        {cursor && (
          <button
            type="button"
            className="chip bevel h-[30px]"
            disabled={busy === "more"}
            onClick={() => void more()}
          >
            All batches
          </button>
        )}
      </div>
      <div className="panel overflow-x-auto px-2 py-1.5">
        <table className="grid-table">
          <thead>
            <tr>
              <th>Batch</th>
              <th>Pipeline</th>
              <th>Rows done</th>
              <th>State</th>
              <th>Started</th>
              <th>
                <span className="sr-only">Actions</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {batches?.map((b) => (
              <tr key={b.id}>
                <td className="m max-w-[280px] truncate" title={b.title}>
                  {b.title}
                </td>
                <td className="font-semibold">{b.workflow_title}</td>
                <td className="m">
                  <RowsDone counts={b.counts} />
                </td>
                <td>
                  <StateChip batch={b} />
                </td>
                <td className="m">{when(b.started_at ?? b.created_at)}</td>
                <td>
                  <span className="flex gap-1.5">
                    <Link href={`/pipelines/batches/${b.id}`} className="chip bevel h-[30px]">
                      Open
                    </Link>
                    {canPause(b) && (
                      <button
                        type="button"
                        className="chip bevel h-[30px]"
                        disabled={busy === b.id}
                        onClick={() => void control(b, "pause")}
                      >
                        Pause
                      </button>
                    )}
                    {canResume(b) && (
                      <button
                        type="button"
                        className="chip bevel h-[30px]"
                        disabled={busy === b.id}
                        onClick={() => void control(b, "resume")}
                      >
                        Resume
                      </button>
                    )}
                  </span>
                </td>
              </tr>
            ))}
            {batches?.length === 0 && (
              <tr>
                <td colSpan={6} className="text-t2">
                  No batches yet. New batch runs a workflow once per row of inputs.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      <NewBatch open={creating} onClose={() => setCreating(false)} workflows={workflows} />
    </main>
  );
}
