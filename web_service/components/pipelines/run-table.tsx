"use client";
// The run table of a pipeline batch (plan 8.3, docs/mock/PipelineGraph.dc.html's table): one line per row, one cell
// per step and gate the row reached (loop attempts stacked, each with its thumbnail, seed and time), the takes it
// ended with, and per row Pick, Retry and the execution view.
import Link from "next/link";
import { Activity } from "lucide-react";
import type { S } from "@/lib/api";
import { canRetry, inputSummary } from "@/lib/batches";
import { safeUrl } from "@/lib/format";
import {
  canPick,
  cellLines,
  rowTakes,
  runColumns,
  unreached,
  type CellLine,
} from "@/lib/pipeline-run";
import type { PDoc } from "@/lib/pipeline";

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
  return (
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
                const lines = cellLines(r.cells?.find((x) => x.node === c.node));
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
  );
}
