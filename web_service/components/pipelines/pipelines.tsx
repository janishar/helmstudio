"use client";
// Pipelines (docs/mock/Pipelines.dc.html, plan 7.2): the saved pipelines (search, filter chips, a card each with its
// chain of nodes and its last batch), the selected pipeline's panel (its steps, Run, Open graph, Duplicate, Rename,
// Delete), New pipeline and New batch, and the Recent batches table. The mock's agent-node chip and tags, "Models it
// will load" and the Memory box belong to later milestones and are left out.
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useCallback, useEffect, useRef, useState } from "react";
import { ErrorNote, Info, Modal } from "@/components/kit";
import { NewBatch } from "@/components/pipelines/new-batch";
import { api, message, ok, type S } from "@/lib/api";
import { batchState, canPause, canResume, doneRows } from "@/lib/batches";
import { when } from "@/lib/format";
import { useHydrated } from "@/lib/hydrated";
import { blankPipeline, facts, nodeLabel, order, type PDoc } from "@/lib/pipeline";
import { followEvents } from "@/lib/runs";

type Batch = S<"Batch">;
type Pipeline = S<"Pipeline">;

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

/** A node of a pipeline as the mock's pill: inputs and outputs `.pl-io`, gates `.pl-gt`, steps plain. */
export function NodePill({ kind, children }: { kind: string; children: React.ReactNode }) {
  const tone = kind === "inputs" || kind === "output" ? "pl-io" : kind === "gate" ? "pl-gt" : "";
  return <span className={`pl ${tone}`}>{children}</span>;
}

/** The chain on a pipeline's card: Inputs → each step's workflow → Gate → Output, in row order. */
function Chain({ p }: { p: Pipeline }) {
  const ids = order(p);
  const label = (id: string) => {
    const n = p.nodes[id];
    return n.kind === "step"
      ? (p.workflow_titles[n.workflow] ?? nodeLabel(p, id))
      : { inputs: "Inputs", gate: "Gate", output: "Output" }[n.kind];
  };
  return (
    <div className="chain" aria-label={ids.map(label).join(", ")}>
      {ids.map((id, i) => (
        <span key={id} className="contents">
          {i > 0 && (
            <i className="text-[11px] text-t2 not-italic" aria-hidden>
              →
            </i>
          )}
          <NodePill kind={p.nodes[id].kind}>{label(id)}</NodePill>
        </span>
      ))}
    </div>
  );
}

type Filter = "all" | "running";

export function Pipelines({
  initial,
  workflows,
  pipelines: givenPipelines,
}: {
  initial?: S<"BatchPage">;
  workflows?: S<"Workflow">[];
  pipelines?: Pipeline[];
}) {
  const hydrated = useHydrated(); // local times differ from the server render
  const router = useRouter();
  const [batches, setBatches] = useState<Batch[] | null>(initial?.batches ?? null);
  const [cursor, setCursor] = useState<string | null>(initial?.next_cursor ?? null);
  const [pipelines, setPipelines] = useState<Pipeline[] | null>(givenPipelines ?? null);
  const [selectedId, setSelectedId] = useState<string | null>(givenPipelines?.[0]?.id ?? null);
  const [q, setQ] = useState("");
  const [filter, setFilter] = useState<Filter>("all");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState("");
  const [creating, setCreating] = useState(false);
  const [running, setRunning] = useState<Pipeline | null>(null);
  const [renaming, setRenaming] = useState<{ p: Pipeline; title: string } | null>(null);
  const [deleting, setDeleting] = useState<Pipeline | null>(null);

  const load = useCallback(async () => {
    setError("");
    // each list on its own: one that can't be read leaves the other shown
    const [p, l] = await Promise.allSettled([
      ok(api.GET("/v1/studio/batches", { params: { query: { limit: PAGE } } })),
      ok(api.GET("/v1/studio/pipelines")),
    ]);
    if (p.status === "fulfilled") {
      setBatches(p.value.batches);
      setCursor(p.value.next_cursor);
    }
    if (l.status === "fulfilled") {
      setPipelines(l.value.pipelines as Pipeline[]);
      setSelectedId((s) => s ?? l.value.pipelines[0]?.id ?? null);
    }
    const failed = [p, l].find((r) => r.status === "rejected");
    if (failed) setError(message(failed.reason));
  }, []);
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- only when the server's read failed
    if (!initial || !givenPipelines) void load();
  }, [initial, givenPipelines, load]);

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
  const put = (b: Batch) => {
    setBatches((l) => l?.map((x) => (x.id === b.id ? b : x)) ?? l);
    setPipelines(
      (l) => l?.map((p) => (p.last_batch?.id === b.id ? { ...p, last_batch: b } : p)) ?? l,
    );
  };
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

  /** New pipeline or Duplicate: the new one opens in its editor. */
  async function create(body: S<"PipelineCreate">) {
    setBusy("create");
    setError("");
    try {
      const p = await ok(api.POST("/v1/studio/pipelines", { body }));
      router.push(`/pipelines/${p.id}`);
    } catch (e) {
      setError(message(e));
      setBusy("");
    }
  }

  async function rename() {
    if (!renaming) return;
    setBusy("rename");
    try {
      const p = await ok(
        api.PATCH("/v1/studio/pipelines/{pipeline_id}", {
          params: { path: { pipeline_id: renaming.p.id } },
          body: { title: renaming.title.trim() },
        }),
      );
      setPipelines((l) => l?.map((x) => (x.id === p.id ? (p as Pipeline) : x)) ?? l);
      setRenaming(null);
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy("");
    }
  }

  async function remove() {
    if (!deleting) return;
    setBusy("delete");
    try {
      await ok(
        api.DELETE("/v1/studio/pipelines/{pipeline_id}", {
          params: { path: { pipeline_id: deleting.id } },
        }),
      );
      setPipelines((l) => l?.filter((x) => x.id !== deleting.id) ?? l);
      setSelectedId(null);
    } catch (e) {
      setError(message(e));
    } finally {
      setDeleting(null);
      setBusy("");
    }
  }

  const runningCount = (pipelines ?? []).filter((p) => p.last_batch && !ended(p.last_batch)).length;
  const shown = (pipelines ?? []).filter(
    (p) =>
      (filter === "all" || (p.last_batch && !ended(p.last_batch))) &&
      p.title.toLowerCase().includes(q.trim().toLowerCase()),
  );
  const selected = pipelines?.find((p) => p.id === selectedId) ?? null;

  return (
    <>
      <main id="main" className="flex min-w-0 flex-[999_1_560px] flex-col gap-4 px-5 pt-4 pb-6">
        <div className="flex flex-wrap items-center gap-3">
          <h1 className="m-0 flex items-center font-display text-[22px] font-bold tracking-[0.01em]">
            Pipelines
            <Info>
              A pipeline runs once per row of inputs as one unattended batch: steps run workflows,
              gates stop a row for your pick. A batch always runs the version it was submitted with.
            </Info>
          </h1>
          <div className="grow" />
          <label htmlFor="pl-search" className="sr-only">
            Search pipelines
          </label>
          <input
            id="pl-search"
            className="in w-60"
            type="search"
            placeholder="Search pipelines"
            value={q}
            onChange={(e) => setQ(e.target.value)}
          />
          <button
            type="button"
            className="chip bevel h-10"
            disabled={busy === "create"}
            onClick={() => void create(blankPipeline("Untitled pipeline"))}
          >
            New pipeline
          </button>
          <button
            type="button"
            className="key h-10 rounded-xl px-[18px] text-[14px] tracking-[0.02em]"
            onClick={() => setCreating(true)}
          >
            New batch
          </button>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <div role="tablist" aria-label="Filter pipelines" className="flex flex-wrap gap-2">
            {(
              [
                ["all", `All · ${pipelines?.length ?? 0}`],
                ["running", `Has a running batch · ${runningCount}`],
              ] as const
            ).map(([f, l]) => (
              <button
                key={f}
                type="button"
                role="tab"
                aria-selected={filter === f}
                className="chip bevel aria-selected:bg-t1 aria-selected:font-semibold aria-selected:text-panel"
                onClick={() => setFilter(f)}
              >
                {l}
              </button>
            ))}
          </div>
          <div className="grow" />
          <span className="flex items-center gap-1.5 text-[11px] text-t2">
            <NodePill kind="inputs">input / output</NodePill>
            <NodePill kind="step">step</NodePill>
            <NodePill kind="gate">gate</NodePill>
          </span>
        </div>

        {error && <ErrorNote onRetry={batches && pipelines ? undefined : load}>{error}</ErrorNote>}

        <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
          {shown.map((p) => {
            const b = p.last_batch;
            const sel = p.id === selectedId;
            return (
              <div
                key={p.id}
                role="button"
                tabIndex={0}
                aria-pressed={sel}
                aria-label={`${p.title}, version ${p.version}`}
                className={`panel flex min-w-0 cursor-pointer flex-col gap-2.5 px-4 py-3.5 ${sel ? "bg-s2 outline-2 outline-offset-3 outline-a1" : ""}`}
                onClick={() => setSelectedId(p.id)}
                onDoubleClick={() => router.push(`/pipelines/${p.id}`)}
                onKeyDown={(e) => {
                  if (e.key === " ") {
                    e.preventDefault();
                    setSelectedId(p.id);
                  } else if (e.key === "Enter") router.push(`/pipelines/${p.id}`);
                }}
              >
                <div className="flex items-baseline justify-between gap-2">
                  <h3 className="m-0 truncate font-display text-[15px] font-bold">{p.title}</h3>
                  <span className="mono text-[11px] text-t2">v{p.version}</span>
                </div>
                <Chain p={p} />
                <div className="flex flex-wrap gap-1.5">
                  {facts(p).map((t, i) => (
                    <span key={i} className="tag">
                      {t}
                    </span>
                  ))}
                </div>
                <div className="flex items-center justify-between gap-2.5 text-[13px]">
                  {b ? <StateChip batch={b} /> : <span className="st">Never run</span>}
                  <span className="mono text-right text-[12px] text-t2">
                    {b ? `${doneRows(b.counts)} / ${b.counts.total} rows` : "0 rows"}
                  </span>
                </div>
              </div>
            );
          })}
          {pipelines?.length === 0 && (
            <p className="text-[13px] text-t2">
              No pipelines yet. New pipeline starts one; Save as pipeline on a take in the Gallery
              makes one from the steps that made it.
            </p>
          )}
        </div>

        <div className="flex items-center gap-2">
          <h2 className="heading flex items-center">
            Recent batches
            <Info>
              Parked rows do not hold up the queue; picks can be made in any order from one grid.
              Three failures in a row with the same error pause a batch.
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
                  <td className="font-semibold">{b.pipeline_title ?? b.workflow_title}</td>
                  <td className="m">
                    <RowsDone counts={b.counts} />
                  </td>
                  <td>
                    <StateChip batch={b} />
                  </td>
                  <td className="m">{hydrated && when(b.started_at ?? b.created_at)}</td>
                  <td>
                    <span className="flex gap-1.5">
                      {!!b.counts.waiting && b.pipeline_id && (
                        <Link
                          href={`/pipelines/batches/${b.id}/review`}
                          className="chip bevel h-[30px] bg-a1 font-semibold text-on1"
                        >
                          Pick
                        </Link>
                      )}
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
                    No batches yet. New batch runs a workflow or a pipeline once per row of inputs.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>

        <NewBatch open={creating} onClose={() => setCreating(false)} workflows={workflows} />
        {running && <NewBatch open onClose={() => setRunning(null)} pipeline={running} />}
      </main>

      {selected && (
        <aside
          aria-label="Selected pipeline"
          className="aside m-3.5 flex flex-[1_1_340px] flex-col gap-3.5 px-[18px] pt-4 pb-6 md:ml-0"
        >
          <div className="flex flex-col gap-1">
            <h2 className="m-0 flex items-center gap-2 font-display text-[18px] font-bold">
              {selected.title}
              <Info>
                Edits change only new batches. A running batch keeps the version it was submitted
                with.
              </Info>
            </h2>
            <span className="mono text-[11px] text-t2">
              v{selected.version} · {hydrated && when(selected.updated_at)}
            </span>
          </div>
          <h3 className="heading">Steps</h3>
          <Steps p={selected} />
          <div className="grow" />
          <button
            type="button"
            className="key h-12 text-[14px]"
            onClick={() => setRunning(selected)}
          >
            Run
          </button>
          <div className="grid grid-cols-2 gap-2">
            <Link href={`/pipelines/${selected.id}`} className="act justify-center">
              Open graph
            </Link>
            <button
              type="button"
              className="act justify-center"
              disabled={busy === "create"}
              onClick={() => void create({ based_on: selected.id })}
            >
              Duplicate
            </button>
            <button
              type="button"
              className="act justify-center"
              onClick={() => setRenaming({ p: selected, title: selected.title })}
            >
              Rename
            </button>
            <button
              type="button"
              className="act justify-center text-bad"
              onClick={() => setDeleting(selected)}
            >
              Delete
            </button>
          </div>
        </aside>
      )}

      <Modal open={!!renaming} onClose={() => setRenaming(null)} title="Rename pipeline">
        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            void rename();
          }}
        >
          <input
            autoFocus
            aria-label="Title"
            className="in"
            maxLength={80}
            value={renaming?.title ?? ""}
            onChange={(e) => setRenaming((r) => r && { ...r, title: e.target.value })}
          />
          <div className="flex justify-end gap-2">
            <button type="button" className="chip bevel" onClick={() => setRenaming(null)}>
              Cancel
            </button>
            <button
              type="submit"
              className="chip bevel bg-a1 font-semibold text-on1"
              disabled={busy === "rename" || !renaming?.title.trim()}
            >
              Rename
            </button>
          </div>
        </form>
      </Modal>
      <Modal open={!!deleting} onClose={() => setDeleting(null)} title="Delete this pipeline?">
        <p className="mb-4 text-[13px] text-t2">
          “{deleting?.title}” is deleted. Its batches and their takes stay.
        </p>
        <div className="flex justify-end gap-2">
          <button type="button" className="chip bevel" onClick={() => setDeleting(null)}>
            Keep it
          </button>
          <button
            type="button"
            className="chip bevel bg-a1 font-semibold text-on1"
            disabled={busy === "delete"}
            onClick={() => void remove()}
          >
            Delete
          </button>
        </div>
      </Modal>
    </>
  );
}

/** The selected pipeline's nodes in row order, as the mock's step list. */
function Steps({ p }: { p: Pipeline }) {
  const doc: PDoc = p;
  const line = (id: string): [string, string, string] => {
    const n = doc.nodes[id];
    switch (n.kind) {
      case "inputs":
        return ["Inputs", n.inputs.map((i) => i.label ?? i.name).join(", "), ""];
      case "step": {
        const seeds = n.seeds ?? 1;
        return [
          "Step",
          nodeLabel(doc, id, p.workflow_titles),
          [n.preset, `${seeds} seed${seeds === 1 ? "" : "s"}`].filter(Boolean).join(" · "),
        ];
      }
      case "gate": {
        const loop = doc.edges.find((e) => e[0] === id)?.[1];
        return [
          "Gate",
          n.title ?? `Pick from ${nodeLabel(doc, n.takes, p.workflow_titles)}`,
          [`keep ${n.keep ?? 1}`, typeof loop === "object" && `retry max ${loop.max}`]
            .filter(Boolean)
            .join(" · "),
        ];
      }
      case "output":
        return ["Output", `collection ${n.collection}`, ""];
    }
  };
  return (
    <ol className="m-0 flex list-none flex-col gap-2 p-0">
      {order(doc).map((id) => {
        const [pill, text, meta] = line(id);
        const kind = doc.nodes[id].kind;
        return (
          <li key={id} className="flex items-center gap-2.5 rounded-xl bg-s2 px-3 py-2">
            <NodePill kind={kind}>{pill}</NodePill>
            <span
              className={`min-w-0 flex-1 truncate ${kind === "inputs" || kind === "output" ? "text-[12px] text-t2" : "text-[13px]"}`}
            >
              {text}
            </span>
            {meta && <span className="mono text-[11px] text-t2">{meta}</span>}
          </li>
        );
      })}
    </ol>
  );
}
