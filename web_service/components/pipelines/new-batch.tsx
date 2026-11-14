"use client";
// New batch (plan 7.2): a workflow, its base settings (Create's inspector), the rows as pasted lines, a CSV table or
// a grid, seeds per row, and Submit. Rows are built and checked here; the server's 422 is shown against its row.
import { useRouter } from "next/navigation";
import { useEffect, useMemo, useState } from "react";
import { X } from "lucide-react";
import { ErrorNote, Modal } from "@/components/kit";
import { Inspector, TextFields } from "@/components/create/form";
import { ApiError, api, message, ok, type S } from "@/lib/api";
import {
  MAX_AXES,
  MAX_SEEDS,
  buildTable,
  gridSize,
  lineFields,
  lineValues,
  matchColumn,
  parseCsv,
  rowError,
  rowFields,
  sizeError,
  summary,
  typedLines,
  type RowError,
} from "@/lib/batches";
import { buildInputs, fields as fieldsOf, visible, type Field, type Raw } from "@/lib/workflow";

type Mode = "lines" | "table" | "grid";
const MODES: [Mode, string][] = [
  ["lines", "Lines"],
  ["table", "CSV table"],
  ["grid", "Grid"],
];
/** Table rows shown under the CSV. */
const PREVIEW = 5;
/** The largest CSV file read. */
const MAX_CSV_BYTES = 1 << 20;

/** "Sample · Steps"; just "Prompt" when the param is named like its node. */
const label = (f: Field) => {
  const p = f.param.label ?? f.param.name;
  return p.toLowerCase() === f.nodeTitle.toLowerCase() ? p : `${f.nodeTitle} · ${p}`;
};

export function NewBatch({
  open,
  onClose,
  workflows: given,
}: {
  open: boolean;
  onClose: () => void;
  workflows?: S<"Workflow">[];
}) {
  const router = useRouter();
  const [workflows, setWorkflows] = useState(given ?? null);
  const [loadError, setLoadError] = useState("");
  const [workflowId, setWorkflowId] = useState(given?.[0]?.id ?? "");
  const [title, setTitle] = useState("");
  const [raws, setRaws] = useState<Record<string, Raw>>({});
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [openKey, setOpenKey] = useState<string | null>(null);
  const [mode, setMode] = useState<Mode>("lines");
  const [lineKey, setLineKey] = useState("");
  const [lines, setLines] = useState("");
  const [csv, setCsv] = useState("");
  const [mapping, setMapping] = useState<string[] | null>(null);
  const [axes, setAxes] = useState<{ key: string; text: string }[]>([{ key: "", text: "" }]);
  const [seedCount, setSeedCount] = useState(1);
  const [seedStart, setSeedStart] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [serverRow, setServerRow] = useState<RowError | null>(null);

  useEffect(() => {
    if (!open || workflows) return;
    ok(api.GET("/v1/studio/workflows"))
      .then((w) => {
        setWorkflows(w.workflows);
        setWorkflowId((id) => id || (w.workflows[0]?.id ?? ""));
      })
      .catch((e) => setLoadError(message(e)));
  }, [open, workflows]);

  const workflow = workflows?.find((w) => w.id === workflowId);
  const all = useMemo(() => (workflow ? fieldsOf(workflow) : []), [workflow]);
  const seeds = all.filter((f) => f.param.type === "seed");
  const seedField = seeds.length === 1 ? seeds[0] : undefined;
  const byKey = (k: string) => all.find((f) => f.key === k);

  // the rows, by mode
  const lineField = byKey(lineKey) ?? lineFields(all)[0];
  const cells = useMemo(() => (mode === "table" ? parseCsv(csv) : []), [mode, csv]);
  const columns = useMemo(
    () => mapping ?? (cells[0] ?? []).map((h) => matchColumn(h, rowFields(all))),
    [mapping, cells, all],
  );
  const table = useMemo(
    () => (mode === "table" && cells.length >= 2 ? buildTable(cells, columns, all) : null),
    [mode, cells, columns, all],
  );
  const built = (() => {
    if (mode === "lines") {
      if (!lineField) return { count: 0, varied: [], errors: [], rows: null };
      const t = typedLines(lineField, lines);
      return {
        count: lineValues(lines).length,
        varied: [lineField.key],
        errors: t.errors,
        rows: { lines: { param: lineField.key, values: lineValues(lines) } },
      };
    }
    if (mode === "table") {
      if (!table) return { count: 0, varied: [], errors: [], rows: null };
      const t = table;
      return {
        count: t.table.rows.length,
        varied: t.table.columns,
        errors: t.errors,
        rows: { table: t.table },
      };
    }
    const typed = axes.map((a) => {
      const f = byKey(a.key);
      return f ? { f, ...typedLines(f, a.text) } : null;
    });
    const set = typed.filter((t) => t !== null);
    return {
      count: set.length === axes.length ? gridSize(set) : 0,
      varied: set.map((t) => t.f.key),
      errors: set.flatMap((t) => t.errors),
      rows:
        set.length === axes.length
          ? { grid: { axes: set.map((t) => ({ param: t.f.key, values: t.values })) } }
          : null,
    };
  })();
  const seedsPer = seedField ? seedCount : 1;
  const tooBig = sizeError(built.count, seedsPer);

  // the base settings: every visible param the rows and seeds don't set
  const base = visible(all, raws).filter(
    (f) => !built.varied.includes(f.key) && f.key !== seedField?.key,
  );
  const change = (key: string, raw: Raw) => {
    setRaws((r) => ({ ...r, [key]: raw }));
    setErrors((e) => {
      const n = { ...e };
      delete n[key];
      return n;
    });
  };

  function pickWorkflow(id: string) {
    setWorkflowId(id);
    setRaws({});
    setErrors({});
    setOpenKey(null);
    setLineKey("");
    setMapping(null);
    setAxes([{ key: "", text: "" }]);
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!workflow || !built.rows || busy) return;
    const b = buildInputs(base, raws);
    setErrors(b.errors);
    setError("");
    setServerRow(null);
    if (Object.keys(b.errors).length || built.errors.length || tooBig) return;
    const start = seedStart.trim();
    if (seedField && start && !/^\d+$/.test(start)) {
      setError("The first seed must be a whole number, or empty for random.");
      return;
    }
    setBusy(true);
    try {
      const batch = await ok(
        api.POST("/v1/studio/batches", {
          body: {
            workflow_id: workflow.id,
            ...(title.trim() && { title: title.trim() }),
            inputs: b.inputs,
            rows: built.rows,
            ...(seedField && {
              seeds: { count: seedCount, ...(start && { start: Number(start) }) },
            }),
          },
        }),
      );
      router.push(`/pipelines/batches/${batch.id}`);
    } catch (err) {
      const r = err instanceof ApiError && err.status === 422 ? rowError(err.message) : null;
      if (r) setServerRow(r);
      else setError(message(err));
      setBusy(false);
    }
  }

  const rowWord = mode === "lines" ? "Line" : mode === "table" ? "Row" : "Combination";
  const rowErrors = [...built.errors, ...(serverRow ? [serverRow] : [])];

  return (
    <Modal open={open} onClose={onClose} title="New batch" width="w-[min(94vw,920px)]">
      <form onSubmit={submit} className="flex max-h-[78dvh] flex-col gap-4 overflow-y-auto">
        {loadError && <ErrorNote>{loadError}</ErrorNote>}
        {workflows?.length === 0 && (
          <ErrorNote>No workflows yet: no runner has published any.</ErrorNote>
        )}
        <div className="grid gap-3 sm:grid-cols-2">
          <label className="flex flex-col gap-1.5">
            <span className="label">Workflow</span>
            <select
              className="in"
              value={workflowId}
              onChange={(e) => pickWorkflow(e.target.value)}
            >
              {workflows?.map((w) => (
                <option key={w.id} value={w.id}>
                  {w.title}
                </option>
              ))}
            </select>
          </label>
          <label className="flex flex-col gap-1.5">
            <span className="label">Title</span>
            <input
              className="in"
              maxLength={200}
              value={title}
              placeholder={workflow ? `${workflow.title} × ${built.count * seedsPer}` : ""}
              onChange={(e) => setTitle(e.target.value)}
            />
          </label>
        </div>

        <div className="grid gap-5 md:grid-cols-[minmax(0,1.3fr)_minmax(0,1fr)]">
          <section aria-labelledby="nb-rows" className="flex min-w-0 flex-col gap-3">
            <h3 id="nb-rows" className="heading">
              Rows
            </h3>
            <div role="group" aria-label="Rows from" className="grid grid-cols-3 gap-1.5">
              {MODES.map(([m, l]) => (
                <button
                  key={m}
                  type="button"
                  className="opt bevel"
                  aria-pressed={mode === m}
                  onClick={() => {
                    setMode(m);
                    setServerRow(null);
                  }}
                >
                  {l}
                </button>
              ))}
            </div>

            {mode === "lines" && (
              <>
                <label className="flex flex-col gap-1.5">
                  <span className="label">One value per line for</span>
                  <select
                    className="in"
                    value={lineField?.key ?? ""}
                    onChange={(e) => setLineKey(e.target.value)}
                  >
                    {lineFields(all).map((f) => (
                      <option key={f.key} value={f.key}>
                        {label(f)}
                      </option>
                    ))}
                  </select>
                </label>
                <label className="flex flex-col gap-1.5">
                  <span className="label flex justify-between">
                    <span>Lines</span>
                    <span className="mono" aria-live="polite">
                      {built.count} row{built.count === 1 ? "" : "s"}
                    </span>
                  </span>
                  <textarea
                    className="in h-40 resize-y py-2 leading-5"
                    placeholder={"a fox at dusk\na heron in the reeds"}
                    value={lines}
                    onChange={(e) => setLines(e.target.value)}
                  />
                </label>
              </>
            )}

            {mode === "table" && (
              <>
                <div className="flex flex-wrap items-center gap-2">
                  <label className="chip bevel cursor-pointer">
                    Choose a CSV file
                    <input
                      type="file"
                      accept=".csv,.tsv,text/csv,text/plain"
                      className="sr-only"
                      onChange={async (e) => {
                        const f = e.target.files?.[0];
                        e.target.value = "";
                        if (!f) return;
                        // 500 rows fit well under 1 MB; a bigger file would only freeze the tab
                        if (f.size > MAX_CSV_BYTES) {
                          setError("That file is over 1 MB; a batch has at most 500 rows.");
                          return;
                        }
                        setError("");
                        setCsv(await f.text());
                        setMapping(null);
                      }}
                    />
                  </label>
                  <span className="text-[12px] text-t2">
                    or paste a table; the first line names the settings
                  </span>
                </div>
                <textarea
                  aria-label="CSV table"
                  className="in mono h-28 resize-y py-2 text-[12px] leading-5"
                  placeholder={"text,steps\na fox at dusk,20"}
                  value={csv}
                  onChange={(e) => {
                    setCsv(e.target.value);
                    setMapping(null);
                  }}
                />
                {cells.length > 0 && (
                  <div className="overflow-x-auto rounded-xl bg-s1 px-1.5 py-1">
                    <table className="grid-table min-w-0">
                      <thead>
                        <tr>
                          <th>#</th>
                          {cells[0].map((h, i) => (
                            <th key={i}>
                              <select
                                aria-label={`Column ${h}`}
                                className="rounded-md border-0 bg-s4 px-1.5 py-1 text-[11px] text-t1"
                                value={columns[i] ?? ""}
                                onChange={(e) =>
                                  setMapping(columns.map((c, j) => (j === i ? e.target.value : c)))
                                }
                              >
                                <option value="">{h} · not used</option>
                                {rowFields(all).map((f) => (
                                  <option key={f.key} value={f.key}>
                                    {h} → {label(f)}
                                  </option>
                                ))}
                              </select>
                            </th>
                          ))}
                        </tr>
                      </thead>
                      <tbody>
                        {cells.slice(1, PREVIEW + 1).map((line, r) => (
                          <tr
                            key={r}
                            className={
                              rowErrors.some((e) => e.row === r + 1) ? "text-bad" : undefined
                            }
                          >
                            <td className="m">{r + 1}</td>
                            {cells[0].map((_, i) => (
                              <td key={i} className="max-w-48 truncate" title={line[i]}>
                                {line[i]}
                              </td>
                            ))}
                          </tr>
                        ))}
                      </tbody>
                    </table>
                    <p className="mono px-2 py-1 text-[11px] text-t2">
                      {built.count} row{built.count === 1 ? "" : "s"}
                      {built.count > PREVIEW && ` · the first ${PREVIEW} shown`}
                    </p>
                  </div>
                )}
              </>
            )}

            {mode === "grid" && (
              <>
                {axes.map((a, i) => (
                  <div key={i} className="flex flex-col gap-1.5 rounded-xl bg-s1 p-2.5">
                    <div className="flex items-center gap-2">
                      <select
                        aria-label={`Axis ${i + 1} setting`}
                        className="in"
                        value={a.key}
                        onChange={(e) =>
                          setAxes(axes.map((x, j) => (j === i ? { ...x, key: e.target.value } : x)))
                        }
                      >
                        <option value="">Choose a setting</option>
                        {rowFields(all)
                          .filter((f) => f.key === a.key || !axes.some((x) => x.key === f.key))
                          .map((f) => (
                            <option key={f.key} value={f.key}>
                              {label(f)}
                            </option>
                          ))}
                      </select>
                      {axes.length > 1 && (
                        <button
                          type="button"
                          aria-label={`Remove axis ${i + 1}`}
                          className="grid size-9 shrink-0 place-items-center rounded-lg bg-s3"
                          onClick={() => setAxes(axes.filter((_, j) => j !== i))}
                        >
                          <X size={14} aria-hidden />
                        </button>
                      )}
                    </div>
                    <textarea
                      aria-label={`Axis ${i + 1} values, one per line`}
                      className="in h-20 resize-y py-2 leading-5"
                      placeholder="One value per line"
                      value={a.text}
                      onChange={(e) =>
                        setAxes(axes.map((x, j) => (j === i ? { ...x, text: e.target.value } : x)))
                      }
                    />
                  </div>
                ))}
                <div className="flex items-center gap-2">
                  {axes.length < MAX_AXES && (
                    <button
                      type="button"
                      className="chip bevel h-[30px]"
                      onClick={() => setAxes([...axes, { key: "", text: "" }])}
                    >
                      Add axis
                    </button>
                  )}
                  <span className="mono text-[12px] text-t2" aria-live="polite">
                    {axes.map((a) => lineValues(a.text).length).join(" × ")} = {built.count} row
                    {built.count === 1 ? "" : "s"}
                  </span>
                </div>
              </>
            )}

            {rowErrors.length > 0 && (
              <ul role="alert" className="flex flex-col gap-1 text-[13px] text-bad">
                {rowErrors.slice(0, 5).map((e, i) => (
                  <li key={i}>{e.row ? `${rowWord} ${e.row}: ${e.text}` : e.text}</li>
                ))}
                {rowErrors.length > 5 && <li>and {rowErrors.length - 5} more</li>}
              </ul>
            )}

            {seedField && (
              <div className="grid grid-cols-2 gap-3">
                <label className="flex flex-col gap-1.5">
                  <span className="label">Seeds per row</span>
                  <select
                    className="in"
                    value={seedCount}
                    onChange={(e) => setSeedCount(Number(e.target.value))}
                  >
                    {Array.from({ length: MAX_SEEDS }, (_, i) => i + 1).map((n) => (
                      <option key={n} value={n}>
                        {n}
                      </option>
                    ))}
                  </select>
                </label>
                <label className="flex flex-col gap-1.5">
                  <span className="label">First seed</span>
                  <input
                    className="in mono"
                    inputMode="numeric"
                    placeholder="Random"
                    value={seedStart}
                    onChange={(e) => setSeedStart(e.target.value)}
                  />
                </label>
              </div>
            )}
          </section>

          <section aria-labelledby="nb-base" className="flex min-w-0 flex-col gap-4">
            <h3 id="nb-base" className="heading">
              Every row
            </h3>
            {base.some((f) => f.param.type === "text") && (
              <div className="rounded-xl bg-s4 px-3 py-2">
                <TextFields fields={base} raws={raws} errors={errors} onChange={change} />
              </div>
            )}
            <Inspector
              fields={base}
              raws={raws}
              errors={errors}
              onChange={change}
              open={openKey}
              setOpen={setOpenKey}
            />
          </section>
        </div>

        {error && <ErrorNote>{error}</ErrorNote>}
        <div className="flex flex-wrap items-center justify-end gap-3">
          <span className="mono mr-auto text-[12px] text-t2" aria-live="polite">
            {tooBig && built.count ? (
              <span className="text-bad">{tooBig}</span>
            ) : (
              summary(built.count, seedField ? seedCount : null)
            )}
          </span>
          <button type="button" className="chip bevel" onClick={onClose}>
            Cancel
          </button>
          <button
            type="submit"
            className="key h-10 px-[18px] text-[14px]"
            disabled={busy || !built.rows || !!tooBig || built.errors.length > 0}
          >
            Submit
          </button>
        </div>
      </form>
    </Modal>
  );
}
