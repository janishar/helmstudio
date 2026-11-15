"use client";
// The run table of a pipeline batch (plan 8.3, docs/mock/PipelineGraph.dc.html's table): one line per row, one cell
// per step, gate, judge, write and decide the row reached (loop attempts stacked, each with its thumbnail, seed and
// time; a judge's one line: its verdict and first failed criterion, opening its attempts side by side with each
// take's reason per criterion, docs/mock/AgentNodes.dc.html), the takes it ended with, and per row Pick, Retry and
// the execution view.
import Link from "next/link";
import { useEffect, useState } from "react";
import { Activity } from "lucide-react";
import { Modal } from "@/components/kit";
import { api, ok, type S } from "@/lib/api";
import { canRetry, inputSummary } from "@/lib/batches";
import { safeUrl } from "@/lib/format";
import {
  canPick,
  cellLines,
  judgeLine,
  judgeSummary,
  rowTakes,
  runColumns,
  unreached,
  type CellLine,
} from "@/lib/pipeline-run";
import { nodeLabel, type PDoc } from "@/lib/pipeline";
import { useTakes } from "@/lib/take-cache";

type Row = S<"BatchRow">;

const TONE: Record<CellLine["tone"], string> = {
  "": "mono text-[12px] text-t2",
  ok: "text-a1",
  run: "font-medium text-a2",
  warn: "text-[#f2c14e]",
  wait: "text-[#c1a8f5]",
};

export function RunTable({
  batch: b,
  rows,
  doc,
  titles,
  busy,
  onRetry,
  onOpen,
}: {
  batch: S<"Batch">;
  rows: Row[];
  doc: PDoc | null;
  titles: Record<string, string>;
  busy: string;
  onRetry: (r: Row) => void;
  /** opens takes full screen */
  onOpen: (ids: string[]) => void;
}) {
  const cols = runColumns(doc, rows, titles);
  const summarise = inputSummary(rows);
  const pick = canPick(b);
  const [judged, setJudged] = useState<{ row: Row; cell: S<"BatchCell">; title: string } | null>(
    null,
  );
  const name = (id: string) => (doc ? nodeLabel(doc, id, titles) : id);
  return (
    <>
      <table className="grid-table">
        <thead>
          <tr>
            <th>#</th>
            <th>Inputs</th>
            {cols.map((c) => (
              <th key={c.node}>{c.title}</th>
            ))}
            <th>Takes</th>
            <th>
              <span className="sr-only">Actions</span>
            </th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => {
            const inputs = summarise(r);
            const out = rowTakes(r);
            return (
              <tr key={r.row} className="align-top">
                <td className="m">{r.row}</td>
                <td className="max-w-[320px] truncate" title={inputs}>
                  {inputs || "—"}
                </td>
                {cols.map((c) => {
                  const cell = r.cells?.find((x) => x.node === c.node);
                  if (cell?.kind === "judge" && cell.attempts.length) {
                    const j = judgeSummary(cell, name);
                    return (
                      <td key={c.node}>
                        <button
                          type="button"
                          aria-haspopup="dialog"
                          className={`chip bevel h-[30px] max-w-[280px] truncate ${TONE[j.tone]}`}
                          title="Every attempt with its reasons"
                          onClick={() => setJudged({ row: r, cell, title: c.title })}
                        >
                          {j.text}
                        </button>
                      </td>
                    );
                  }
                  const lines = cellLines(cell);
                  if (!lines.length)
                    return (
                      <td key={c.node} className="m">
                        {unreached(r)}
                      </td>
                    );
                  return (
                    <td key={c.node}>
                      <div className="flex flex-col gap-1">
                        {lines.map((l) => (
                          <div key={l.visit} className="flex items-center gap-2">
                            {l.thumb && (
                              <button
                                type="button"
                                className="shrink-0 p-0"
                                aria-label={`Open the takes of row ${r.row}, ${c.title}${lines.length > 1 ? `, attempt ${l.visit}` : ""}`}
                                onClick={() => onOpen(l.takes)}
                              >
                                <img
                                  src={safeUrl(l.thumb)}
                                  alt=""
                                  className="h-7 w-11 rounded-[5px] bg-s3 object-cover"
                                />
                              </button>
                            )}
                            <span className="flex min-w-0 flex-col">
                              <span
                                className={`max-w-[260px] truncate ${TONE[l.tone]}`}
                                title={l.error ?? undefined}
                              >
                                {lines.length > 1 && `${l.visit} · `}
                                {l.text}
                              </span>
                              {(l.seed || l.time) && (
                                <span className="mono text-[11px] text-t2">
                                  {[l.seed, l.time].filter(Boolean).join(" · ")}
                                </span>
                              )}
                            </span>
                          </div>
                        ))}
                      </div>
                    </td>
                  );
                })}
                <td>
                  {out.thumb ? (
                    <button
                      type="button"
                      className="flex items-center gap-1.5 p-0"
                      aria-label={`Open row ${r.row}'s takes full screen`}
                      onClick={() => onOpen(out.ids)}
                    >
                      <img
                        src={safeUrl(out.thumb)}
                        alt=""
                        className="h-10 w-16 rounded-md bg-s3 object-cover"
                      />
                      {out.ids.length > 1 && (
                        <span className="mono text-[11px] text-t2">+{out.ids.length - 1}</span>
                      )}
                    </button>
                  ) : (
                    <span className="mono text-[12px] text-t2">—</span>
                  )}
                </td>
                <td>
                  <span className="flex items-center gap-1.5">
                    {r.status === "waiting" && pick && (
                      <Link
                        href={`/pipelines/batches/${b.id}/review#row-${r.row}`}
                        className="chip bevel h-[30px]"
                      >
                        Pick
                      </Link>
                    )}
                    {canRetry(b, r) && (
                      <button
                        type="button"
                        className="chip bevel h-[30px]"
                        disabled={busy === `row${r.row}`}
                        onClick={() => onRetry(r)}
                      >
                        Retry
                      </button>
                    )}
                    <Link
                      href={`/pipelines/batches/${b.id}/rows/${r.row}`}
                      aria-label={`Row ${r.row}: execution view`}
                      title="Execution view"
                      className="grid h-[30px] w-9 place-items-center rounded-[10px] bg-s4 text-t1"
                    >
                      <Activity size={16} aria-hidden />
                    </Link>
                  </span>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
      {judged && <JudgeAttempts batchId={b.id} {...judged} onClose={() => setJudged(null)} />}
    </>
  );
}

/** A judge cell opened: every attempt side by side, each take with its thumbnail, reason and each criterion's
 * check (from the row's decisions; without them, the take's first failed must). */
function JudgeAttempts({
  batchId,
  row,
  cell,
  title,
  onClose,
}: {
  batchId: string;
  row: Row;
  cell: S<"BatchCell">;
  title: string;
  onClose: () => void;
}) {
  const [decisions, setDecisions] = useState<S<"Decision">[]>([]);
  useEffect(() => {
    let stop = false;
    ok(
      api.GET("/v1/studio/batches/{batch_id}/rows/{row}/decisions", {
        params: {
          path: { batch_id: batchId, row: String(row.row) },
          query: { attempt: row.attempt },
        },
      }),
    )
      .then((l) => !stop && setDecisions(l.decisions))
      .catch(() => undefined);
    return () => {
      stop = true;
    };
  }, [batchId, row.row, row.attempt]);
  const ids = cell.attempts.flatMap((a) => a.judged?.takes.map((t) => t.take_id) ?? []);
  const take = useTakes(ids);
  const n = cell.attempts.length;
  return (
    <Modal
      open
      onClose={onClose}
      title={`Row ${row.row} · ${title} · ${n} attempt${n === 1 ? "" : "s"}`}
      width="w-[min(96vw,1000px)]"
    >
      <div className="flex max-h-[70dvh] gap-2.5 overflow-auto">
        {cell.attempts.map((a) => {
          const line = judgeLine(a);
          const d = decisions.find((x) => x.id === a.decision_id)?.judge;
          return (
            <section key={a.visit} className="set flex min-w-[240px] flex-1 flex-col gap-2">
              <h3 className="m-0 text-[13px] font-semibold">
                Try {a.visit} · <span className={TONE[line.tone]}>{line.text}</span>
              </h3>
              {(a.judged?.takes ?? []).map((t, i) => {
                const tk = take(t.take_id);
                const checks = d?.takes.find((x) => x.take_id === t.take_id)?.checks;
                const kept = (a.take_ids.length ? a.take_ids : a.judged!.suggested).includes(
                  t.take_id,
                );
                return (
                  <div
                    key={t.take_id}
                    className="grid grid-cols-[52px_minmax(0,1fr)] items-start gap-2"
                  >
                    <span
                      className="block h-[34px] w-[52px] overflow-hidden rounded-md bg-s4"
                      style={kept ? { outline: "2px solid var(--a1)" } : undefined}
                    >
                      {tk?.thumb_url && (
                        <img
                          src={safeUrl(tk.thumb_url)}
                          alt=""
                          className="size-full object-cover"
                        />
                      )}
                    </span>
                    <div className="flex min-w-0 flex-col gap-1">
                      <span className="text-[12px] text-[var(--t1b)]">
                        take {i + 1} · {t.reason}
                        {kept && (a.take_ids.length ? " · picked" : " · suggested")}
                      </span>
                      <span className="flex flex-wrap gap-1">
                        {(
                          checks ??
                          (t.first_failed ? [{ criterion: t.first_failed, pass: false }] : [])
                        ).map((c) => (
                          <span
                            key={c.criterion}
                            className={`rounded-full px-[7px] py-px text-[11px] ${c.pass ? "bg-s3 text-t2" : "text-[#f2c14e] shadow-[inset_0_0_0_1px_#f2c14e]"}`}
                            title={`${c.criterion}: ${c.pass ? "passed" : "failed"}`}
                          >
                            {c.criterion}
                          </span>
                        ))}
                      </span>
                    </div>
                  </div>
                );
              })}
            </section>
          );
        })}
      </div>
      <div className="mt-3 flex justify-end">
        <button type="button" className="chip bevel" onClick={onClose}>
          Close
        </button>
      </div>
    </Modal>
  );
}
