"use client";
// The batch run view (plan 8.3, docs/mock/PipelineGraph.dc.html's table): the header with the batch's state, counts
// and Pause / Resume / Stop, and one table line per row, live from the batch's events. A pipeline batch shows the
// run table (a cell per step, gate and agent node, loop attempts stacked) and adds Pick, Retry failed and Run again
// (same decisions or decide again: docs/mock/AgentSteps.dc.html); its graph is the pipeline's editor and each row's
// execution view.
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useCallback, useEffect, useRef, useState } from "react";
import { ChevronLeft } from "lucide-react";
import { ErrorNote, Modal } from "@/components/kit";
import { NoMatch, SearchField, useSearch } from "@/components/search";
import { FullScreen } from "@/components/media";
import { RowsDone, StateChip, controlBatch, followBatch } from "@/components/pipelines/pipelines";
import { RunTable } from "@/components/pipelines/run-table";
import { ChatPanel, useChat } from "@/components/pipelines/chat";
import { rowMarks } from "@/lib/chat";
import { api, message, ok, type S } from "@/lib/api";
import {
  appendRows,
  applyBatchEvent,
  canPause,
  canResume,
  canRetry,
  canStop,
  inputSummary,
  retryFailedRows,
  rowState,
  type BatchView,
} from "@/lib/batches";
import { clock, safeUrl, when } from "@/lib/format";
import { useHydrated } from "@/lib/hydrated";
import { canPick, countFacts } from "@/lib/pipeline-run";
import type { PDoc } from "@/lib/pipeline";
import { useScreenTools } from "@/components/assistant/tools";
import { batchTools } from "@/components/pipelines/assistant";

type Row = S<"BatchRow">;

/** A page of rows. */
export const ROWS = 100;

export function BatchRun({
  id,
  initial,
}: {
  id: string;
  initial?: { batch: S<"Batch">; page: S<"BatchRowPage"> };
}) {
  const [view, setView] = useState<BatchView | null>(
    initial ? { batch: initial.batch, rows: initial.page.rows } : null,
  );
  const [cursor, setCursor] = useState<string | null>(initial?.page.next_cursor ?? null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState("");
  const [stopping, setStopping] = useState(false);
  const [again, setAgain] = useState<"same" | "again" | null>(null);
  const [viewer, setViewer] = useState<{ takes: S<"Take">[]; i: number } | null>(null);
  // the rows' search (past 8 rows or with more pages): the loaded rows at once, then the server's q on every row
  const search = useSearch({
    items: view?.rows,
    text: (r) => [
      ...Object.values(r.inputs).filter((v): v is string => typeof v === "string"),
      r.status,
    ],
    id: (r) => String(r.row),
    more: !!cursor,
    server: (q, c) =>
      ok(
        api.GET("/v1/studio/batches/{batch_id}/rows", {
          params: { path: { batch_id: id }, query: { q, cursor: c ?? undefined, limit: ROWS } },
        }),
      ).then((p) => ({ items: p.rows, next: p.next_cursor })),
    onError: setError,
  });
  const router = useRouter();
  // a pipeline batch: the pipeline now, for its node and workflow titles (cells name nodes the snapshot had)
  const pipelineId = view?.batch.pipeline_id ?? null;
  const [pipeline, setPipeline] = useState<S<"Pipeline"> | null>(null);
  useEffect(() => {
    if (!pipelineId) return;
    let stop = false;
    api
      .GET("/v1/studio/pipelines/{pipeline_id}", { params: { path: { pipeline_id: pipelineId } } })
      .then((r) => !stop && r.data && setPipeline(r.data as S<"Pipeline">))
      .catch(() => undefined);
    return () => {
      stop = true;
    };
  }, [pipelineId]);

  const load = useCallback(async () => {
    setError("");
    try {
      const path = { path: { batch_id: id } };
      const [batch, page] = await Promise.all([
        ok(api.GET("/v1/studio/batches/{batch_id}", { params: path })),
        ok(
          api.GET("/v1/studio/batches/{batch_id}/rows", {
            params: { ...path, query: { limit: ROWS } },
          }),
        ),
      ]);
      setView({ batch, rows: page.rows });
      setCursor(page.next_cursor);
    } catch (e) {
      setError(message(e));
    }
  }, [id]);
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- only when the server's read failed
    if (!initial) void load();
  }, [initial, load]);

  // live: every event of the batch, once it is loaded (a row not loaded yet comes with its page)
  const loaded = view !== null;
  useEffect(() => {
    if (!loaded) return;
    return followBatch(id, ["batch", "row"], (e) => setView((v) => v && applyBatchEvent(v, e)));
  }, [id, loaded]);

  async function more() {
    if (!cursor) return;
    setBusy("more");
    try {
      const p = await ok(
        api.GET("/v1/studio/batches/{batch_id}/rows", {
          params: { path: { batch_id: id }, query: { limit: ROWS, cursor } },
        }),
      );
      setView((v) => v && { ...v, rows: appendRows(v.rows, p.rows) });
      setCursor(p.next_cursor);
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy("");
    }
  }

  async function control(op: "pause" | "resume" | "stop") {
    setBusy(op);
    setError("");
    try {
      const batch = await ok(controlBatch(id, op));
      setView((v) => v && { ...v, batch });
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy("");
      setStopping(false);
    }
  }

  async function retry(r: Row) {
    setBusy(`row${r.row}`);
    setError("");
    try {
      const row = await ok(
        api.POST("/v1/studio/batches/{batch_id}/rows/{row}/retry", {
          params: { path: { batch_id: id, row: String(r.row) } },
        }),
      );
      setView((v) => v && applyBatchEvent(v, { seq: 0, type: "row", ts: "", data: row }));
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy("");
    }
  }

  /** Retry failed: each failed row, one after another, past a row that is refused. */
  async function retryFailed() {
    if (!view) return;
    setBusy("failed");
    setError("");
    try {
      const errors = await retryFailedRows(view.batch, (row) =>
        setView((v) => v && applyBatchEvent(v, { seq: 0, type: "row", ts: "", data: row })),
      );
      if (errors.length) setError(errors.join(" · "));
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy("");
    }
  }

  /** Run again: a new batch of the source's snapshot, rows and seeds; `same` reuses its decisions. */
  async function runAgain(decisions: "same" | "again") {
    setBusy("again");
    setError("");
    try {
      const batch = await ok(
        api.POST("/v1/studio/batches/{batch_id}/again", {
          params: { path: { batch_id: id } },
          body: { decisions },
        }),
      );
      router.push(`/pipelines/batches/${batch.id}`);
    } catch (e) {
      setError(message(e));
      setBusy("");
      setAgain(null);
    }
  }

  // the row's takes, fetched when its thumbnail is opened; the arrow keys step through them
  const opening = useRef(false);
  async function openTakes(r: Row | string[]) {
    if (opening.current) return;
    opening.current = true;
    try {
      const takes = await Promise.all(
        (Array.isArray(r) ? r : r.take_ids).map((t) =>
          ok(api.GET("/v1/studio/takes/{take_id}", { params: { path: { take_id: t } } })),
        ),
      );
      if (takes.length) setViewer({ takes, i: 0 });
    } catch (e) {
      setError(message(e));
    } finally {
      opening.current = false;
    }
  }

  const hydrated = useHydrated();
  // the director's chat, when the batch's pipeline has a director (Batch.director)
  const chat = useChat(view?.batch.director ? id : null);
  // the assistant's batch actions (plan 18 §22.5): the header's own calls; resume, retry, again and stop are cards
  const showRow = (row: Row) =>
    setView((v) => v && applyBatchEvent(v, { seq: 0, type: "row", ts: "", data: row }));
  useScreenTools(
    view
      ? batchTools({
          view,
          pipeline,
          control: async (op) => {
            const batch = await ok(controlBatch(id, op));
            setView((v) => v && { ...v, batch });
            return batch;
          },
          retry: async (row, fromStep) => {
            const r = await ok(
              api.POST("/v1/studio/batches/{batch_id}/rows/{row}/retry", {
                params: { path: { batch_id: id, row: String(row) } },
                ...(fromStep && { body: { from_step: fromStep } }),
              }),
            );
            showRow(r);
            return r;
          },
          again: async (decisions) => {
            const batch = await ok(
              api.POST("/v1/studio/batches/{batch_id}/again", {
                params: { path: { batch_id: id } },
                body: { decisions },
              }),
            );
            router.push(`/pipelines/batches/${batch.id}`);
            return batch;
          },
        })
      : { screen: "batches", sees: ["Batch"], summary: () => ({ loading: true }), actions: {} },
  );
  if (!view)
    return (
      <main id="main" className="flex min-w-0 flex-1 flex-col gap-4 px-5 pt-4 pb-6">
        {error ? (
          <ErrorNote onRetry={load}>{error}</ErrorNote>
        ) : (
          <p className="text-t2">Loading…</p>
        )}
      </main>
    );

  const { batch: b, rows } = view;
  const c = b.counts;
  const summarise = inputSummary(rows);
  const took =
    b.started_at && b.finished_at
      ? clock((Date.parse(b.finished_at) - Date.parse(b.started_at)) / 1000)
      : null;
  const isPipeline = !!b.pipeline_id;
  const ended = ["done", "failed", "stopped"].includes(b.status);
  const failedRows = rows.filter((r) => canRetry(b, r)).length;
  const facts = [
    ...countFacts(c),
    hydrated && b.started_at && `started ${when(b.started_at)}`,
    hydrated && b.finished_at && `finished ${when(b.finished_at)}`,
    took && `took ${took}`,
    b.memory && `memory read: ${b.memory.examples} examples, ${b.memory.notes} notes`,
    b.again_of && (b.same_decisions ? "same decisions as its source" : "run again"),
    b.remote &&
      `remote: ${b.remote.calls} of ${b.remote.max_calls} calls, ${b.remote.tokens.toLocaleString("en-US")} of ${b.remote.max_tokens.toLocaleString("en-US")} tokens${b.remote.network_failures ? `, ${b.remote.network_failures} network failures retried` : ""}`,
  ].filter(Boolean);

  return (
    <>
      <main id="main" className="flex min-w-0 flex-[999_1_560px] flex-col gap-3.5 px-5 pt-4 pb-5">
        <div className="flex flex-wrap items-center gap-3">
          <Link
            href="/pipelines"
            aria-label="Pipelines"
            className="grid size-9 place-items-center rounded-[10px] text-t2 hover:bg-s2"
          >
            <ChevronLeft size={18} aria-hidden />
          </Link>
          <h1 className="m-0 font-display text-[18px] font-bold tracking-[0.01em]">{b.title}</h1>
          <span className="rounded-full bg-s2 px-2.5 py-1 text-[12px] font-medium text-t2">
            Unattended ·{" "}
            {isPipeline ? `${b.pipeline_title} · v${b.pipeline_version}` : b.workflow_title} ·{" "}
            {c.total} row{c.total === 1 ? "" : "s"}
          </span>
          <StateChip batch={b} />
          <div className="grow" />
          {isPipeline && canPick(b) && (
            <Link
              href={`/pipelines/batches/${b.id}/review`}
              className="chip bevel bg-a1 font-semibold text-on1"
            >
              Pick · {c.waiting}
            </Link>
          )}
          {isPipeline && failedRows > 0 && (
            <button
              type="button"
              className="chip bevel"
              disabled={!!busy}
              onClick={() => void retryFailed()}
            >
              Retry failed
            </button>
          )}
          {canPause(b) && (
            <button
              type="button"
              className="chip bevel"
              disabled={!!busy}
              onClick={() => void control("pause")}
            >
              Pause
            </button>
          )}
          {canResume(b) && (
            <button
              type="button"
              className="chip bevel"
              disabled={!!busy}
              onClick={() => void control("resume")}
            >
              Resume
            </button>
          )}
          {canStop(b) && (
            <button
              type="button"
              className="chip bevel"
              disabled={!!busy}
              onClick={() => setStopping(true)}
            >
              {isPipeline ? "Cancel remaining" : "Stop"}
            </button>
          )}
          {isPipeline && ended && (
            <button
              type="button"
              className="chip bevel"
              disabled={!!busy}
              onClick={() => setAgain("same")}
            >
              Run again
            </button>
          )}
        </div>
        <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
          <span className="mono text-[12px] text-t2">
            <RowsDone counts={c} />
          </span>
          <span className="mono text-[12px] text-t2">{facts.join(" · ")}</span>
        </div>
        {b.reason && (b.status === "paused" || b.status === "failed") && (
          <p className="rounded-xl bg-s2 px-3 py-2 text-[13px]">
            {b.status === "paused" ? "Paused: " : "Failed: "}
            {b.reason}
          </p>
        )}
        {error && <ErrorNote>{error}</ErrorNote>}

        {search.show && (
          <div className="flex justify-end">
            <SearchField {...search.field} label="Search rows" className="w-72 max-md:w-full" />
          </div>
        )}
        <div className="panel shrink-0 overflow-x-auto px-2 py-1.5">
          {isPipeline ? (
            <RunTable
              batch={b}
              rows={search.rows}
              doc={(pipeline as PDoc | null) ?? null}
              titles={pipeline?.workflow_titles ?? {}}
              busy={busy}
              onRetry={(r) => void retry(r)}
              onOpen={(ids) => void openTakes(ids)}
              marks={chat.view ? rowMarks(chat.view.messages) : undefined}
            />
          ) : (
            <table className="grid-table">
              <thead>
                <tr>
                  <th>#</th>
                  <th>Inputs</th>
                  <th>Step 1 · {b.workflow_title}</th>
                  <th>Takes</th>
                  <th>
                    <span className="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {search.rows.map((r) => {
                  const inputs = summarise(r);
                  return (
                    <tr key={r.row}>
                      <td className="m">{r.row}</td>
                      <td className="max-w-[420px] truncate" title={inputs}>
                        {inputs || "—"}
                      </td>
                      <td
                        className={
                          r.status === "failed"
                            ? "max-w-[360px] truncate text-[#f2c14e]"
                            : r.status === "running"
                              ? "font-medium text-a2"
                              : "m"
                        }
                        title={r.error ?? undefined}
                      >
                        {rowState(r)}
                        {r.attempt > 1 && ` · attempt ${r.attempt}`}
                      </td>
                      <td>
                        {r.thumb_url ? (
                          <button
                            type="button"
                            className="flex items-center gap-1.5 p-0"
                            aria-label={`Open row ${r.row}'s takes full screen`}
                            onClick={() => void openTakes(r)}
                          >
                            <img
                              loading="lazy"
                              decoding="async"
                              src={safeUrl(r.thumb_url)}
                              alt=""
                              className="h-10 w-16 rounded-md bg-s3 object-cover"
                            />
                            {r.take_ids.length > 1 && (
                              <span className="mono text-[11px] text-t2">
                                +{r.take_ids.length - 1}
                              </span>
                            )}
                          </button>
                        ) : (
                          <span className="mono text-[12px] text-t2">—</span>
                        )}
                      </td>
                      <td>
                        {canRetry(b, r) && (
                          <button
                            type="button"
                            className="chip bevel h-[30px]"
                            disabled={busy === `row${r.row}`}
                            onClick={() => void retry(r)}
                          >
                            Retry
                          </button>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          )}
          {search.q && !search.rows.length && !search.searching && (
            <NoMatch what="rows" onClear={() => search.setQ("")} />
          )}
          {search.q && search.searching && (
            <p role="status" className="m-0 p-2 text-center text-[13px] text-t2">
              Searching more rows…
            </p>
          )}
          {(search.q ? search.olderNext && !search.searching : !!cursor) && (
            <div className="flex justify-center p-2">
              <button
                type="button"
                className="chip bevel h-[30px]"
                disabled={busy === "more"}
                onClick={() => (search.q ? search.moreOlder() : void more())}
              >
                {search.q ? "Load more matches" : "More rows"}
              </button>
            </div>
          )}
        </div>

        <Modal
          open={stopping}
          onClose={() => setStopping(false)}
          title={isPipeline ? "Cancel the remaining rows?" : "Stop this batch?"}
        >
          <p className="mb-4 text-[13px] text-t2">
            Rows not finished are cancelled, rows waiting for a pick too. Finished rows and their
            takes stay.
          </p>
          <div className="flex justify-end gap-2">
            <button type="button" className="chip bevel" onClick={() => setStopping(false)}>
              Keep running
            </button>
            <button
              type="button"
              className="chip bevel bg-a1 font-semibold text-on1"
              disabled={busy === "stop"}
              onClick={() => void control("stop")}
            >
              {isPipeline ? "Cancel remaining" : "Stop"}
            </button>
          </div>
        </Modal>

        <Modal open={!!again} onClose={() => setAgain(null)} title="Run this batch again">
          <form
            className="flex flex-col gap-3"
            onSubmit={(e) => {
              e.preventDefault();
              if (again) void runAgain(again);
            }}
          >
            <p className="m-0 text-[13px] text-t2">
              A new batch with the same pipeline version, rows and seeds.
            </p>
            {(
              [
                [
                  "same",
                  "Same decisions",
                  "Judge, write and decide answers are reused where their inputs match; no model is asked. Your picks are asked again.",
                ],
                ["again", "Decide again", "Every judge, write and decide asks its model again."],
              ] as const
            ).map(([v, t, help]) => (
              <label key={v} className="flex items-start gap-2.5">
                <input
                  type="radio"
                  name="again"
                  className="mt-1 accent-[var(--a2)]"
                  checked={again === v}
                  onChange={() => setAgain(v)}
                />
                <span>
                  <b className="font-semibold">{t}</b>
                  <br />
                  <small className="text-t2">{help}</small>
                </span>
              </label>
            ))}
            <div className="flex justify-end gap-2">
              <button type="button" className="chip bevel" onClick={() => setAgain(null)}>
                Cancel
              </button>
              <button
                type="submit"
                className="chip bevel bg-a1 font-semibold text-on1"
                disabled={busy === "again"}
              >
                Run again
              </button>
            </div>
          </form>
        </Modal>

        {viewer && (
          <FullScreen
            take={viewer.takes[viewer.i]}
            onClose={() => setViewer(null)}
            onStep={(d) =>
              setViewer((v) => v && { ...v, i: (v.i + d + v.takes.length) % v.takes.length })
            }
          />
        )}
      </main>
      {b.director && (
        <aside
          aria-label="Chat with director"
          className="aside m-3.5 flex max-h-[calc(100dvh-28px)] flex-[1_1_340px] flex-col gap-3 px-[18px] pt-4 pb-5 md:ml-0"
        >
          <ChatPanel batch={b} chat={chat} />
        </aside>
      )}
    </>
  );
}
