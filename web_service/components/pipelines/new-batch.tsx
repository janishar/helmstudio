"use client";
// New batch (plan 7.2): a workflow (or a saved pipeline, whose inputs are the columns), its base settings (Create's
// inspector), the rows as pasted lines, a CSV table or a grid, seeds per row, and Submit. Rows are built and checked
// here; the server's 422 is shown against its row. A pipeline's Submit shows what leaves the machine and the memory
// local servers keep reserved (docs/mock/SubmitEgress.dc.html), as POST /pipelines/validate answers for it.
import { useRouter } from "next/navigation";
import { useEffect, useMemo, useState } from "react";
import { X } from "lucide-react";
import { ErrorNote, Info, Modal } from "@/components/kit";
import { Inspector, TextFields } from "@/components/create/form";
import { MentionText, PromptArea } from "@/components/create/prompt";
import { useRefUploads } from "@/components/create/refs";
import { GalleryPicker } from "@/components/gallery/picker";
import { LooksPicker, QuickTiles } from "@/components/create/looks";
import { useLooks } from "@/lib/library";
import { useLoras } from "@/lib/lora-library";
import { usable } from "@/lib/loras";
import { chosenIds, liveChosen, reasonOf, tilesOf, type Chosen } from "@/lib/looks";
import { withInputs } from "@/lib/mentions";
import { imageInputs } from "@/lib/refs";
import { remember } from "@/lib/take-cache";
import { MODEL_KINDS, nodeLabel, pipelineBounds, pipelineFields } from "@/lib/pipeline";
import { egressLines, remoteLine, reservedLines } from "@/lib/egress";
import { boundsLine } from "@/lib/pipeline-run";
import { TextArea } from "@/components/expand-text";
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
import {
  buildInputs,
  fields as fieldsOf,
  visible,
  type Field,
  type Raw,
  type Update,
} from "@/lib/workflow";

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
  pipeline,
}: {
  open: boolean;
  onClose: () => void;
  workflows?: S<"Workflow">[];
  /** a batch of this saved pipeline instead of a workflow */
  pipeline?: S<"Pipeline">;
}) {
  const router = useRouter();
  const [workflows, setWorkflows] = useState(given ?? null);
  const [loadError, setLoadError] = useState("");
  const [workflowId, setWorkflowId] = useState(given?.[0]?.id ?? "");
  const [title, setTitle] = useState("");
  const [raws, setRaws] = useState<Record<string, Raw>>({});
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [openKey, setOpenKey] = useState<string | null>(null);
  // a pipeline needing several inputs per row (a still's prompt and its clip's) opens on the table: one column each
  const [mode, setMode] = useState<Mode>(() =>
    pipeline && rowFields(pipelineFields(pipeline)).length > 1 ? "table" : "lines",
  );
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
  // the looks every row is made with (BatchCreate.looks); a pipeline batch takes its looks from its steps
  const [looksPicked, setLooksChosen] = useState<Chosen>({});
  const [lookGroup, setLookGroup] = useState<S<"LibraryGroup"> | null>(null);
  const looks = useLooks();
  const looksChosen = liveChosen(looksPicked, looks);
  const loras = useLoras();
  // what leaves the machine, as studio sees it now (a listed pipeline carries none)
  const [check, setCheck] = useState<S<"PipelineValidation"> | null>(null);
  const pipeDoc = pipeline && {
    title: pipeline.title,
    nodes: pipeline.nodes,
    edges: pipeline.edges,
    ...(pipeline.remote && { remote: pipeline.remote }),
  };
  const pipeKey = pipeDoc ? JSON.stringify(pipeDoc) : "";
  useEffect(() => {
    if (!open || !pipeKey) return;
    let stop = false;
    ok(api.POST("/v1/studio/pipelines/validate", { body: JSON.parse(pipeKey) }))
      .then((v) => !stop && setCheck(v))
      .catch(() => undefined);
    return () => {
      stop = true;
    };
  }, [open, pipeKey]);

  useEffect(() => {
    if (!open || workflows || pipeline) return;
    ok(api.GET("/v1/studio/workflows"))
      .then((w) => {
        setWorkflows(w.workflows);
        setWorkflowId((id) => id || (w.workflows[0]?.id ?? ""));
      })
      .catch((e) => setLoadError(message(e)));
  }, [open, workflows, pipeline]);

  const workflow = pipeline ? undefined : workflows?.find((w) => w.id === workflowId);
  const all = useMemo(
    () => (pipeline ? pipelineFields(pipeline) : workflow ? fieldsOf(workflow) : []),
    [workflow, pipeline],
  );
  const sourceTitle = pipeline?.title ?? workflow?.title;
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
  const change = (key: string, u: Update) => {
    setRaws((r) => ({ ...r, [key]: typeof u === "function" ? u(r[key]) : u }));
    setErrors((e) => {
      const n = { ...e };
      delete n[key];
      return n;
    });
  };
  const uploads = useRefUploads(change);
  const [picking, setPicking] = useState(false);
  const lineIsImage = !!lineField && imageInputs([lineField]).length > 0;
  const loraFamilies = usable(loras);
  const lookTiles = workflow
    ? tilesOf(workflow, looks ?? [], loraFamilies, looksChosen).filter((t) => t !== "elements")
    : [];
  const lookIds = chosenIds(lookTiles, looksChosen);

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
    if (!(workflow || pipeline) || !built.rows || busy) return;
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
            ...(pipeline ? { pipeline_id: pipeline.id } : { workflow_id: workflow!.id }),
            ...(title.trim() && { title: title.trim() }),
            inputs: b.inputs,
            rows: built.rows,
            ...(lookIds.length && { looks: lookIds }),
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
        {!pipeline && workflows?.length === 0 && (
          <ErrorNote>No workflows yet: no runner has published any.</ErrorNote>
        )}
        <div className="grid gap-3 sm:grid-cols-2">
          {pipeline ? (
            <div className="flex flex-col gap-1.5">
              <span className="label">Pipeline</span>
              <span className="in flex items-center truncate">
                {pipeline.title} · v{pipeline.version}
              </span>
            </div>
          ) : (
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
          )}
          <label className="flex flex-col gap-1.5">
            <span className="label">Title</span>
            <input
              className="in"
              maxLength={200}
              value={title}
              placeholder={sourceTitle ? `${sourceTitle} × ${built.count * seedsPer}` : ""}
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
                {lineIsImage && (
                  <div className="flex flex-wrap items-center gap-2">
                    <button
                      type="button"
                      className="chip bevel border border-dashed border-dash bg-transparent"
                      onClick={() => setPicking(true)}
                    >
                      Pick from gallery
                    </button>
                    <span className="text-[12px] text-t2">one take id per line, one row each</span>
                    <GalleryPicker
                      open={picking}
                      onClose={() => setPicking(false)}
                      max={100}
                      onPick={(takes) => {
                        remember(takes);
                        setLines((l) => [...lineValues(l), ...takes.map((t) => t.id)].join("\n"));
                      }}
                    />
                  </div>
                )}
                <label className="flex flex-col gap-1.5">
                  <span className="label flex justify-between">
                    <span>Lines</span>
                    <span className="mono" aria-live="polite">
                      {built.count} row{built.count === 1 ? "" : "s"}
                    </span>
                  </span>
                  {lineField?.param.type === "text" ? (
                    <PromptArea
                      expand="Lines"
                      className="in h-40 resize-y py-2 leading-5"
                      placeholder={"a fox at dusk\n@fox in @snowfield"}
                      value={lines}
                      onChange={setLines}
                    />
                  ) : (
                    <TextArea
                      expand="Lines"
                      className="in h-40 resize-y py-2 leading-5"
                      placeholder={
                        lineIsImage ? "a take id per line" : "a fox at dusk\na heron in the reeds"
                      }
                      value={lines}
                      onChange={(e) => setLines(e.target.value)}
                    />
                  )}
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
                <TextArea
                  expand="Table"
                  aria-label="CSV table"
                  className="in mono h-28 resize-y py-2 text-[12px] leading-5"
                  placeholder={
                    // a pipeline's header: its inputs, the columns each row fills
                    pipeline
                      ? rowFields(all)
                          .map((f) => f.param.name)
                          .join(",")
                      : "text,steps\na fox at dusk,20"
                  }
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
                                {byKey(columns[i])?.param.type === "text" ? (
                                  <MentionText text={line[i] ?? ""} />
                                ) : (
                                  line[i]
                                )}
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
                    {byKey(a.key)?.param.type === "text" ? (
                      <PromptArea
                        expand={`Axis ${i + 1}`}
                        aria-label={`Axis ${i + 1} values, one per line`}
                        className="in h-20 resize-y py-2 leading-5"
                        placeholder="One value per line"
                        value={a.text}
                        onChange={(v) =>
                          setAxes(axes.map((x, j) => (j === i ? { ...x, text: v } : x)))
                        }
                      />
                    ) : (
                      <TextArea
                        expand={`Axis ${i + 1}`}
                        aria-label={`Axis ${i + 1} values, one per line`}
                        className="in h-20 resize-y py-2 leading-5"
                        placeholder="One value per line"
                        value={a.text}
                        onChange={(e) =>
                          setAxes(
                            axes.map((x, j) => (j === i ? { ...x, text: e.target.value } : x)),
                          )
                        }
                      />
                    )}
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
            {workflow && lookTiles.length > 0 && (
              <div role="group" aria-label="Looks" className="flex flex-wrap items-center gap-2">
                <QuickTiles
                  tiles={lookTiles}
                  chosen={looksChosen}
                  mentioned={0}
                  onElements={() => undefined}
                  onLook={setLookGroup}
                />
              </div>
            )}
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
              uploads={uploads}
            />
          </section>
        </div>

        {pipeline && (
          <Egress
            pipeline={pipeline}
            egress={check?.egress ?? pipeline.egress}
            reserved={check?.reserved ?? pipeline.reserved}
            rows={built.count * (seedField ? seedCount : 1)}
          />
        )}
        {error && <ErrorNote>{error}</ErrorNote>}
        <div className="flex flex-wrap items-center justify-end gap-3">
          <span className="mono mr-auto text-[12px] text-t2" aria-live="polite">
            {tooBig && built.count ? (
              <span className="text-bad">{tooBig}</span>
            ) : (
              <>
                {summary(built.count, seedField ? seedCount : null, !!pipeline)}
                {pipeline &&
                  built.count > 0 &&
                  ` · ${boundsLine(
                    pipeline.bounds ?? pipelineBounds(pipeline),
                    built.count * (seedField ? seedCount : 1),
                  )}`}
              </>
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
      {workflow && (
        <LooksPicker
          group={lookGroup}
          onClose={() => setLookGroup(null)}
          chosen={looksChosen}
          onChosen={setLooksChosen}
          reason={(x) =>
            reasonOf(
              {
                ...workflow,
                nodes: withInputs(workflow.nodes, buildInputs(base, raws).inputs),
              },
              x,
              loraFamilies,
            )
          }
        />
      )}
    </Modal>
  );
}

/** Submit's "What leaves the machine" (docs/mock/SubmitEgress.dc.html): per node where its calls go and exactly what
 * it sends, the remote worst case beside the caps, and the local servers' reserved memory. Shown for a pipeline with
 * a model or custom node; "Nothing leaves the machine" when none of them calls out. */
function Egress({
  pipeline,
  egress,
  reserved,
  rows,
}: {
  pipeline: S<"Pipeline">;
  egress?: S<"PipelineEgress">[];
  reserved?: S<"PipelineReserved">[];
  rows: number;
}) {
  const kinds = Object.values(pipeline.nodes).map((n) => n.kind);
  if (!kinds.some((k) => MODEL_KINDS.includes(k) || k === "custom")) return null;
  const name = (id: string) => nodeLabel(pipeline, id, pipeline.workflow_titles);
  const heading = (id: string) => (name(id) === id ? id : `${name(id)} · ${id}`); // the board's "Decide · check"
  const source = (src: string) =>
    src.startsWith("$")
      ? `Input · ${src.slice(1)}`
      : `${name(src.split(".")[0])} · ${src.split(".")[1]}`;
  const lines = egress ? egressLines(egress, Math.max(1, rows), source) : [];
  const remote = egress ? remoteLine(egress, Math.max(1, rows), pipeline.remote) : "";
  return (
    <section aria-labelledby="nb-egress" className="flex flex-col gap-2">
      <h3 id="nb-egress" className="heading flex items-center">
        What leaves the machine
        <Info>
          Exactly what each node sends to a model outside this app. Thumbnails are never sent. The
          caps, not the estimate, stop the batch.
        </Info>
      </h3>
      {!egress && <p className="m-0 text-[13px] text-t2">Checking…</p>}
      {egress && !lines.length && (
        <p className="m-0 text-[13px] text-t2">Nothing leaves the machine.</p>
      )}
      {lines.map((l) => (
        <div key={l.node} className="set flex flex-col gap-1.5 text-[13px]">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <b className="font-semibold">{heading(l.node)}</b>
            <span className={`st ${l.remote ? "st-run" : ""}`}>{l.where}</span>
          </div>
          <small className="text-[12px] text-t2">sends {l.sends}</small>
          {l.load && <small className="text-[12px] text-t2">{l.load}</small>}
        </div>
      ))}
      {remote && (
        <div className="set flex items-center justify-between gap-2 py-2 text-[13px]">
          {remote}
          <span className="text-[11px] text-t2">caps</span>
        </div>
      )}
      {reservedLines(reserved ?? []).map((r) => (
        <div key={r} className="set flex items-center justify-between gap-2 py-2 text-[13px]">
          {r}
          <span className="text-[11px] text-t2">reserved memory</span>
        </div>
      ))}
    </section>
  );
}
