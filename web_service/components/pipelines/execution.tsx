"use client";
// The execution view of one row (docs/mock/Execution.dc.html, plan 16.8): the pipeline's graph as the row ran it
// (passed nodes marked, edges not taken faint, the loop's count, the node it is at now outlined), the step list, and
// the chosen step's details: a step's runs and takes, a gate's candidates and pick, the state after it (read on
// demand; gone once the batch has finished) and Retry row from here. Live from the batch's events. A judge, write or
// decide step shows its decision: a judge's verdict per check, the thumbnails it saw, the model and what it read from
// memory (Execution.dc.html); a write's text before and after as a diff, a decide's route (AgentSteps.dc.html). A
// judge's checkpoints fold under one line per decision that opens on click (owner's Q8). The graph keeps its saved
// positions; elkjs lays it out left to right only when they overlap or sit off-screen (Q10). The header's title opens
// a menu (Rename, all pipelines: Q4); the batch is a link in the row's line; its Studio | Graph switch opens the
// pipeline's Studio view (Q2); the memory chip is Create's.
import dynamic from "next/dynamic";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Activity, ChevronRight, ImageOff } from "lucide-react";
import { Thumb } from "@/components/media";
import { ErrorNote } from "@/components/kit";
import { FullScreen } from "@/components/media";
import { followBatch } from "@/components/pipelines/pipelines";
import { Memory, useHosts } from "@/components/create/memory-chip";
import type { CanvasProps } from "@/components/pipelines/canvas";
import { TitleMenu } from "@/components/pipelines/title-menu";
import { ViewSwitch } from "@/components/pipelines/studio";
import { api, ApiError, message, ok, type S } from "@/lib/api";
import { nodeLabel, stepNumbers, type PDoc } from "@/lib/pipeline";
import { hostMemory } from "@/lib/hosts";
import {
  agentEnd,
  asRun,
  canRetryFrom,
  criterionLines,
  decisionModel,
  needsLayout,
  stepDetail,
  stepGroups,
  stepLines,
  wordDiff,
  type StepLine,
} from "@/lib/pipeline-run";
import { callLine } from "@/lib/chat";
import { safeUrl } from "@/lib/format";
import { useTakes } from "@/lib/take-cache";

type Exec = S<"RowExecution">;

const PipelineCanvas = dynamic<CanvasProps>(
  () => import("@/components/pipelines/canvas").then((m) => m.PipelineCanvas),
  {
    ssr: false,
    loading: () => (
      <div className="flex h-full items-center justify-center text-t2">Loading the graph…</div>
    ),
  },
);

const TONE = { warn: "text-[#f2c14e]", ok: "text-a1", now: "font-medium text-a2", "": "" };

/** The pipeline without its saved positions: the canvas lays it out with elkjs (when needsLayout says so). */
const unplaced = (doc: PDoc): PDoc => ({
  ...doc,
  nodes: Object.fromEntries(
    Object.entries(doc.nodes).map(([id, n]) => [id, { ...n, position: undefined }]),
  ) as PDoc["nodes"],
});

const STATUS: Record<string, string> = {
  queued: "queued",
  running: "running",
  waiting: "parked",
  succeeded: "done",
  failed: "failed",
  cancelled: "cancelled",
};

export function Execution({
  batch: initialBatch,
  initial,
  attempt,
  titles = {},
  envs = {},
}: {
  batch: S<"Batch">;
  initial: Exec;
  /** workflow id → title, for steps without a title of their own */
  titles?: Record<string, string>;
  /** the attempt asked for in the URL (absent: the current one) */
  attempt?: number;
  /** workflow id → its env, for the memory chip */
  envs?: Record<string, string>;
}) {
  const router = useRouter();
  const [batch, setBatch] = useState(initialBatch);
  const [x, setX] = useState(initial);
  const [step, setStep] = useState(
    () =>
      initial.steps.find((s) => s.status === "current")?.step ?? initial.steps.at(-1)?.step ?? 1,
  );
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [state, setState] = useState<{ key: string; text: string; gone?: boolean } | null>(null);
  const [full, setFull] = useState<{ ids: string[]; i: number } | null>(null);
  // the pipeline's title once renamed here (the row ran the version it was submitted with)
  const [title, setTitle] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      const e = await ok(
        api.GET("/v1/studio/batches/{batch_id}/rows/{row}/execution", {
          params: {
            path: { batch_id: x.batch_id, row: String(x.row) },
            query: attempt ? { attempt } : {},
          },
        }),
      );
      setX(e as Exec);
    } catch (err) {
      setError(message(err));
    }
  }, [x.batch_id, x.row, attempt]);

  // live: this row's events reload the execution (steps arrive as it runs)
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(
    () =>
      followBatch(x.batch_id, ["batch", "row"], (e) => {
        if (e.type === "batch") return setBatch(e.data);
        if (e.type !== "row" || e.data.row !== x.row) return;
        clearTimeout(timer.current);
        timer.current = setTimeout(() => void load(), 300);
      }),
    [x.batch_id, x.row, load],
  );

  const lines = stepLines(x, titles);
  const groups = stepGroups(x, titles);
  // the judge lines opened to their checkpoints (by the line's step); the chosen step's line opens by itself
  const [opened, setOpened] = useState<Set<number>>(() => new Set());
  const marks = useMemo(() => asRun(x), [x]);
  const detail = stepDetail(x, step);
  const s = detail?.step;
  const takeIds =
    detail?.kind === "gate"
      ? detail.candidates
      : (detail && "takes" in detail && detail.takes) || [];
  const take = useTakes(takeIds);

  async function showState() {
    if (!s) return;
    const key = `${x.attempt}:${s.step}`;
    setState({ key, text: "Reading…" });
    try {
      const r = await ok(
        api.GET("/v1/studio/batches/{batch_id}/rows/{row}/execution/{step}/state", {
          params: {
            path: { batch_id: x.batch_id, row: String(x.row), step: String(s.step) },
            query: { attempt: x.attempt },
          },
        }),
      );
      setState({ key, text: JSON.stringify(r.state, null, 2) });
    } catch (e) {
      const gone = e instanceof ApiError && e.status === 404;
      setState({
        key,
        gone,
        text: gone
          ? "This row's state is gone: the batch has finished, so its checkpoints were deleted. The steps above remain."
          : e instanceof ApiError && e.status === 503
            ? "The pipeline service did not answer in time. Try again."
            : message(e),
      });
    }
  }
  // as the mock draws it: the selected step's state shows without asking (one read per step chosen)
  const wantState = s?.has_state ? `${x.attempt}:${s.step}` : null;
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- the read's own "Reading…" placeholder
    if (wantState !== null && state?.key !== wantState) void showState();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- once per step chosen
  }, [wantState]);

  async function retryFrom() {
    if (!s) return;
    setBusy(true);
    setError("");
    try {
      await ok(
        api.POST("/v1/studio/batches/{batch_id}/rows/{row}/retry", {
          params: { path: { batch_id: x.batch_id, row: String(x.row) } },
          body: { from_step: s.step },
        }),
      );
      // the new attempt: the current one again
      router.replace(`/pipelines/batches/${x.batch_id}/rows/${x.row}`);
      await load();
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  }

  const doc = x.pipeline;
  const nums = stepNumbers(doc);
  const where = x.current
    ? `${STATUS[x.status] ?? x.status} on ${doc.nodes[x.current]?.kind === "step" ? `step ${nums[x.current]}` : nodeLabel(doc, x.current, titles)}`
    : (STATUS[x.status] ?? x.status);
  // the saved positions, unless they overlap or sit off-screen: then elkjs lays it out left to right
  const laid = useMemo(() => (needsLayout(doc) ? unplaced(doc) : doc), [doc]);
  // the memory chip: the host of the step the row is at, else of the first step
  const hosts = useHosts();
  const stepAt = [x.current, ...Object.keys(nums)].find(
    (id) => id && doc.nodes[id]?.kind === "step",
  );
  const at = stepAt ? doc.nodes[stepAt] : undefined;
  const host = hostMemory(hosts, at?.kind === "step" ? envs[at.workflow] : undefined);
  const rowsTotal = batch.counts.total;
  const picked = detail?.kind === "gate" ? detail.picked : null;

  return (
    <>
      <main id="main" className="flex min-w-0 flex-[999_1_560px] flex-col gap-3.5 px-5 pt-4 pb-5">
        <div className="flex flex-wrap items-center gap-3">
          <TitleMenu
            title={title ?? doc.title}
            onRename={
              batch.pipeline_id
                ? async (t) => {
                    const p = await ok(
                      api.PATCH("/v1/studio/pipelines/{pipeline_id}", {
                        params: { path: { pipeline_id: batch.pipeline_id! } },
                        body: { title: t },
                      }),
                    );
                    setTitle(p.title);
                  }
                : undefined
            }
          />
          {/* the menu is Rename and All pipelines (Q4): the batch's link sits in the row's line */}
          <span className="rounded-full bg-s2 px-2.5 py-1 text-[12px] font-medium text-t2">
            Row {x.row} of {rowsTotal} · {where} ·{" "}
            <Link href={`/pipelines/batches/${x.batch_id}`} className="text-t1 underline">
              {batch.title}
            </Link>
          </span>
          <label className="sr-only" htmlFor="ex-row">
            Row
          </label>
          <select
            id="ex-row"
            className="chip bevel appearance-none bg-s4"
            value={x.row}
            onChange={(e) => router.push(`/pipelines/batches/${x.batch_id}/rows/${e.target.value}`)}
          >
            {Array.from({ length: rowsTotal }, (_, i) => i + 1).map((r) => (
              <option key={r} value={r}>
                Row {r}
              </option>
            ))}
          </select>
          {x.attempts > 1 && (
            <select
              aria-label="Attempt"
              className="chip bevel bg-s4"
              value={x.attempt}
              onChange={(e) =>
                router.push(
                  `/pipelines/batches/${x.batch_id}/rows/${x.row}?attempt=${e.target.value}`,
                )
              }
            >
              {Array.from({ length: x.attempts }, (_, i) => i + 1).map((a) => (
                <option key={a} value={a}>
                  Attempt {a}
                </option>
              ))}
            </select>
          )}
          {batch.pipeline_id && (
            <Link
              href={`/pipelines/${batch.pipeline_id}`}
              aria-label="Back to the pipeline graph"
              title="Back to the pipeline graph"
              className="grid h-9 w-10 place-items-center rounded-[10px] bg-s4 text-t1 outline-2 outline-a2"
            >
              <Activity size={18} aria-hidden />
            </Link>
          )}
          <div className="grow" />
          <ViewSwitch
            view="graph"
            hrefs={
              batch.pipeline_id ? { studio: `/pipelines/${batch.pipeline_id}?view=studio` } : {}
            }
          />
          {host && <Memory memory={host} />}
        </div>
        {x.retried_from && (
          <p className="m-0 text-[12px] text-t2">
            This attempt re-ran attempt {x.retried_from.attempt} from step {x.retried_from.step};
            the steps before it are reused.
          </p>
        )}
        {error && <ErrorNote>{error}</ErrorNote>}
        <div
          className="panel relative h-[400px] shrink-0 overflow-hidden"
          style={{ background: "var(--in)" }}
        >
          <PipelineCanvas
            doc={laid}
            titles={titles}
            marks={marks}
            selected={s?.node ?? null}
            onSelect={(id) => {
              const last = x.steps.filter((t) => t.node === id).at(-1);
              if (last) setStep(last.step);
            }}
          />
        </div>
        <div className="panel shrink-0 overflow-x-auto px-2 py-1.5">
          <table className="grid-table">
            <thead>
              <tr>
                <th>Step</th>
                <th>Node</th>
                <th>What happened</th>
                <th>State change</th>
                <th>Status</th>
              </tr>
            </thead>
            <tbody>
              {groups.flatMap(({ line, inner }) => {
                const open =
                  inner.length > 1 &&
                  (opened.has(line.step) ||
                    inner.some((l) => l.step === step && l.step !== line.step));
                const head = (
                  <StepRow
                    key={line.step}
                    l={line}
                    on={line.step === step && !open}
                    range={inner.length > 1 ? [inner[0].step, inner.at(-1)!.step] : undefined}
                    open={inner.length > 1 ? open : undefined}
                    onPick={() => {
                      setStep(line.step);
                      if (inner.length > 1)
                        setOpened((o) => {
                          const n = new Set(o);
                          if (open) n.delete(line.step);
                          else n.add(line.step);
                          return n;
                        });
                    }}
                  />
                );
                return open
                  ? [
                      head,
                      ...inner.map((l) => (
                        <StepRow
                          key={`${line.step}:${l.step}`}
                          l={l}
                          on={l.step === step}
                          inner
                          onPick={() => setStep(l.step)}
                        />
                      )),
                    ]
                  : [head];
              })}
            </tbody>
          </table>
        </div>
      </main>

      <aside
        aria-label="Step details"
        className="aside m-3.5 flex flex-[1_1_320px] flex-col gap-[18px] px-[18px] pt-4 pb-6 md:ml-0"
      >
        {s ? (
          <>
            <div className="flex flex-col gap-1">
              <h2 className="m-0 font-display text-base font-bold">
                {lines.find((l) => l.step === s.step)?.label} · step {s.step}
              </h2>
              <span className="text-[12px] text-t2">
                Row {x.row} · attempt {x.attempt} · checkpoint {s.step} of {x.steps.length}
              </span>
            </div>
            {s.error && <p className="m-0 text-[13px] text-[#f2c14e]">{s.error}</p>}
            {s.tool_calls && s.tool_calls.length > 0 && !s.decision && (
              <details className="text-[12px] text-t2">
                <summary className="cursor-pointer">
                  {s.tool_calls.length} tool call{s.tool_calls.length === 1 ? "" : "s"}
                </summary>
                <ToolCalls calls={s.tool_calls} />
              </details>
            )}
            {detail && "decision" in detail && detail.decision ? (
              <DecisionDetail d={detail.decision} name={(id) => nodeLabel(doc, id, titles)} />
            ) : (
              <>
                <div className="flex flex-col gap-2">
                  <h3 className="heading">
                    {detail?.kind === "gate"
                      ? "Candidates"
                      : s.run_ids.length
                        ? "Takes"
                        : "What it did"}
                  </h3>
                  {takeIds.length > 0 ? (
                    <div className="grid grid-cols-4 gap-1.5">
                      {takeIds.map((t, i) => {
                        const tk = take(t);
                        const on = picked?.includes(t);
                        return (
                          <button
                            key={t}
                            type="button"
                            aria-label={`Take ${i + 1}${on ? ", picked" : ""}`}
                            className={`grid aspect-square place-items-center overflow-hidden rounded-lg p-0 text-[10px] text-t2 ${on ? "border-2 border-a1" : "border border-dashed border-dash"}`}
                            onClick={() => setFull({ ids: takeIds, i })}
                          >
                            {tk ? (
                              <Thumb take={tk} className="size-full" />
                            ) : (
                              <span className="flex flex-col items-center gap-1">
                                <ImageOff size={14} aria-hidden />
                                take {i + 1}
                              </span>
                            )}
                          </button>
                        );
                      })}
                    </div>
                  ) : (
                    <p className="m-0 text-[13px] text-t2">{s.what}</p>
                  )}
                </div>
              </>
            )}
            {!s.decision && (
              <div className="grid grid-cols-2 gap-2">
                {detail?.kind === "gate" ? (
                  <div className="set flex flex-col gap-0.5">
                    <small className="text-[12px] text-t2">Picked</small>
                    <b className="font-semibold">
                      {picked === null
                        ? "not yet"
                        : picked.length
                          ? `${picked.length} take${picked.length === 1 ? "" : "s"}`
                          : "none: rejected all"}
                    </b>
                  </div>
                ) : (
                  <div className="set flex flex-col gap-0.5">
                    <small className="text-[12px] text-t2">Runs</small>
                    <b className="font-semibold">{s.run_ids.length || "none"}</b>
                  </div>
                )}
                <div className="set flex flex-col gap-0.5">
                  <small className="text-[12px] text-t2">Then</small>
                  <b className="truncate font-semibold">
                    {s.edge
                      ? `${s.edge.via === "next" ? "" : `${s.edge.via} → `}${nodeLabel(doc, s.edge.to, titles)}`
                      : s.status === "current"
                        ? "here now"
                        : "—"}
                  </b>
                </div>
                {Object.entries(s.loops).map(([g, n]) => (
                  <div key={g} className="set flex flex-col gap-0.5">
                    <small className="text-[12px] text-t2">
                      Loop at {nodeLabel(doc, g, titles)}
                    </small>
                    <b className="font-semibold">
                      {n} time{n === 1 ? "" : "s"}
                    </b>
                  </div>
                ))}
              </div>
            )}
            <div className="flex flex-col gap-2">
              <h3 className="heading">State after this step</h3>
              {state?.key === `${x.attempt}:${s.step}` ? (
                <pre
                  className={`m-0 max-h-72 overflow-auto rounded-xl px-3 py-2.5 font-mono text-[11px] leading-4 break-words whitespace-pre-wrap ${state.gone ? "text-t2" : "text-[var(--t1b)]"}`}
                  style={{ background: "var(--in)" }}
                >
                  {state.text}
                </pre>
              ) : s.has_state ? (
                <button
                  type="button"
                  className="chip bevel self-start bg-s4"
                  onClick={() => void showState()}
                >
                  Show state
                </button>
              ) : (
                <p className="m-0 text-[12px] text-t2">
                  {s.status === "current"
                    ? "The row is here now: there is no state after this step yet."
                    : "This step's state can't be read: the batch's checkpoints are gone."}
                </p>
              )}
            </div>
            <div className="flex flex-wrap gap-2">
              {canRetryFrom(x, s.step, batch) && (
                <button
                  type="button"
                  className="chip bevel bg-s4"
                  disabled={busy}
                  onClick={() => void retryFrom()}
                >
                  Retry row from here
                </button>
              )}
              {state?.key === `${x.attempt}:${s.step}` &&
                !state.gone &&
                state.text.startsWith("{") && (
                  <button
                    type="button"
                    className="chip bevel bg-s4"
                    onClick={() => void navigator.clipboard?.writeText(state.text)}
                  >
                    Copy state
                  </button>
                )}
            </div>
          </>
        ) : (
          <p className="m-0 text-[13px] text-t2">This row has not started yet.</p>
        )}
      </aside>
      {full &&
        (() => {
          const t = take(full.ids[full.i]);
          return t ? (
            <FullScreen
              take={t}
              onClose={() => setFull(null)}
              onStep={(d) =>
                setFull((f) => f && { ...f, i: (f.i + d + f.ids.length) % f.ids.length })
              }
            />
          ) : null;
        })()}
    </>
  );
}

/** A line of the step list: a step, a judge's decision (`open`: its checkpoints shown; `range`: their steps), or
 * one of those checkpoints (`inner`). */
function StepRow({
  l,
  on,
  open,
  range,
  inner,
  onPick,
}: {
  l: StepLine;
  on: boolean;
  open?: boolean;
  range?: [number, number];
  inner?: boolean;
  onPick: () => void;
}) {
  return (
    <tr
      className="cursor-pointer"
      style={on ? { background: "var(--s3)" } : undefined}
      onClick={onPick}
    >
      <td className="m">
        <button
          type="button"
          className="flex items-center gap-1 p-0 font-mono"
          aria-pressed={on}
          aria-expanded={open}
          aria-label={
            range
              ? `${l.label}, steps ${range[0]} to ${range[1]}${open ? "" : ": show its checkpoints"}`
              : `Step ${l.step}: ${l.label}`
          }
          onClick={(e) => {
            e.stopPropagation();
            onPick();
          }}
        >
          {range ? `${range[0]}–${range[1]}` : l.step}
          {open !== undefined && (
            <ChevronRight
              size={12}
              aria-hidden
              className={`transition-transform ${open ? "rotate-90" : ""}`}
            />
          )}
        </button>
      </td>
      <td className={`${inner ? "pl-6 text-t2" : ""} ${on ? "font-semibold" : ""}`}>{l.label}</td>
      <td className={`max-w-[360px] truncate ${TONE[l.tone]}`} title={l.error ?? l.what}>
        {l.what}
      </td>
      <td className="m max-w-[280px] whitespace-normal [overflow-wrap:anywhere]">{l.changed}</td>
      <td className={l.status === "now" ? "text-a2" : "m"}>{l.status}</td>
    </tr>
  );
}

/** An agent's tool calls, each in words with its arguments and result as JSON text (never HTML). */
function ToolCalls({ calls }: { calls: S<"ToolCall">[] }) {
  return calls.map((c) => (
    <details key={c.id} className="mt-1 pl-3">
      <summary className="mono cursor-pointer text-[11px]">{callLine(c)}</summary>
      <pre
        className="m-0 mt-1 max-h-48 overflow-auto rounded-lg px-2 py-1.5 font-mono text-[11px] leading-4 break-words whitespace-pre-wrap text-[var(--t1b)]"
        style={{ background: "var(--in)" }}
      >
        {JSON.stringify({ args: c.args, result: c.result, error: c.error }, null, 2)}
      </pre>
    </details>
  ));
}

const tile = (label: string, value: React.ReactNode) => (
  <div className="set flex min-w-0 flex-col gap-0.5">
    <small className="text-[12px] text-t2">{label}</small>
    <b className="truncate font-semibold">{value}</b>
  </div>
);

/** A judge's, write's or decide's step details from its decision. */
function DecisionDetail({ d, name }: { d: S<"Decision">; name: (id: string) => string }) {
  const model = decisionModel(d);
  const answer = d.replayed
    ? tile("Reused", d.replayed_from ? `from ${d.replayed_from.split(":")[0].slice(0, 8)}` : "yes")
    : d.replayable === false
      ? tile("Reused", "never: a remote answer")
      : tile("Answer", `${Math.round(d.seconds * 10) / 10} s · ${d.tokens_out} tokens`);
  return (
    <>
      {d.error && <p className="m-0 text-[13px] text-[#f2c14e]">{d.error}</p>}
      {d.agent && (
        <>
          <div className="set flex items-center justify-between gap-2 py-2 text-[13px]">
            {agentEnd(d.agent)}
            <span className="text-[11px] whitespace-nowrap text-t2">
              {d.agent.turns.length} turns · {d.agent.tool_calls} tools ·{" "}
              {d.agent.tokens.toLocaleString("en-US")} tokens
            </span>
          </div>
          <div className="flex flex-col gap-2">
            <h3 className="heading">Messages and tool calls</h3>
            {d.agent.turns.map((t) => (
              <details key={t.turn} className="text-[12px] text-t2">
                <summary className="cursor-pointer">
                  Turn {t.turn} ·{" "}
                  {t.calls.length
                    ? `${t.calls.length} tool call${t.calls.length === 1 ? "" : "s"}`
                    : "the end"}
                </summary>
                {t.say && (
                  <p className="my-1.5 text-[13px] break-words whitespace-pre-wrap text-[var(--t1b)]">
                    {t.say}
                  </p>
                )}
                <ToolCalls calls={t.calls} />
              </details>
            ))}
            {d.agent.items.length > 0 && (
              <details className="text-[12px] text-t2">
                <summary className="cursor-pointer">
                  Its list · {d.agent.items.length} items
                </summary>
                <ol className="m-0 mt-1 flex flex-col gap-1 pl-5 text-[13px] text-[var(--t1b)]">
                  {d.agent.items.map((x, i) => (
                    <li key={i} className="break-words whitespace-pre-wrap">
                      {x}
                    </li>
                  ))}
                </ol>
              </details>
            )}
          </div>
          <div className="grid grid-cols-2 gap-2">
            {tile("Model", model)}
            {d.network_failures
              ? tile("Network failures", `${d.network_failures} retried`)
              : answer}
          </div>
        </>
      )}
      {d.judge && (
        <>
          <div className="flex flex-col gap-1.5">
            <h3 className="heading">Verdict</h3>
            {criterionLines(d.judge).map((c) => (
              <div
                key={c.criterion}
                className="set flex items-center justify-between gap-2 py-2 text-[13px]"
              >
                {c.criterion}
                <span
                  className="text-[11px] whitespace-nowrap"
                  style={{ color: c.bad ? "#f2c14e" : "var(--t2)" }}
                >
                  {c.text}
                </span>
              </div>
            ))}
          </div>
          <div className="flex flex-col gap-2">
            <h3 className="heading">What it saw</h3>
            <div className="grid grid-cols-4 gap-1.5">
              {d.seen.map((t) => (
                <img
                  key={`${t.take_id}:${t.frame}`}
                  src={safeUrl(t.thumb_url)}
                  alt={`take ${d.judge!.takes.findIndex((x) => x.take_id === t.take_id) + 1}${t.frame != null ? `, frame ${t.frame}` : ""}`}
                  title={`${t.width} × ${t.height}${t.frame != null ? ` · frame ${t.frame}` : ""}`}
                  className={`aspect-square rounded-lg bg-s3 object-cover ${d.judge!.picked.includes(t.take_id) ? "outline-2 outline-a1" : ""}`}
                />
              ))}
              {!d.seen.length &&
                d.judge.takes.map((t, i) => (
                  <div
                    key={t.take_id}
                    className="flex aspect-square flex-col items-center justify-center gap-1 rounded-lg border border-dashed border-dash text-[10px] text-t2"
                  >
                    <ImageOff size={14} aria-hidden />
                    take {i + 1}
                  </div>
                ))}
            </div>
          </div>
          <div className="grid grid-cols-2 gap-2">
            {tile("Model", model)}
            {tile(
              "Memory read",
              `${d.memory_read.length} example${d.memory_read.length === 1 ? "" : "s"}`,
            )}
          </div>
        </>
      )}
      {d.write && (
        <>
          <div className="flex flex-col gap-2">
            <h3 className="heading">Input · {d.write.to.slice(1)}, before and after</h3>
            <p
              className="m-0 rounded-xl px-3 py-2.5 text-[13px] leading-5"
              style={{ background: "var(--in)" }}
            >
              {wordDiff(d.write.before, d.write.after).map((p, i) =>
                p.op === "same" ? (
                  <span key={i}>{p.text}</span>
                ) : p.op === "del" ? (
                  <del
                    key={i}
                    className="bg-[color-mix(in_srgb,#f2c14e_22%,transparent)] text-[#f2c14e]"
                  >
                    {p.text}
                  </del>
                ) : (
                  <ins
                    key={i}
                    className="bg-[color-mix(in_srgb,var(--a1)_22%,transparent)] text-a1 no-underline"
                  >
                    {p.text}
                  </ins>
                ),
              )}
            </p>
            {d.write.note && (
              <div className="set flex items-center justify-between gap-2 py-2 text-[13px]">
                {d.write.note}
                <span className="text-[11px] text-t2">its note</span>
              </div>
            )}
          </div>
          <div className="grid grid-cols-2 gap-2">
            {tile("Model", model)}
            {answer}
          </div>
        </>
      )}
      {d.decide && (
        <>
          <div className="flex flex-col gap-2">
            <h3 className="heading">Route</h3>
            <div className="set flex items-center justify-between gap-2 py-2 text-[13px]">
              {d.decide.route} → {name(d.decide.to)}
              <span className="text-[11px] text-t2">
                {d.decide.forced ? "forced by the cap" : "answered"}
              </span>
            </div>
            {d.decide.reason && (
              <p className="m-0 text-[13px] text-[var(--t1b)]">{d.decide.reason}</p>
            )}
          </div>
          <div className="grid grid-cols-2 gap-2">
            {tile("Model", model)}
            {answer}
          </div>
        </>
      )}
    </>
  );
}
