"use client";
// Create: one session's runs of one workflow. The form comes from the workflow's node schemas; progress comes live
// from the session's event stream (SSE through the BFF, ?since= polling when the stream can't be held).
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Plus, X } from "lucide-react";
import { ErrorNote, Info } from "@/components/kit";
import { Inspector, TextFields } from "@/components/create/form";
import { api, message, ok, okOr, type S } from "@/lib/api";
import { safeUrl } from "@/lib/format";
import { applyEvent, fraction, isActive, mergeRuns } from "@/lib/runs";
import { buildInputs, fields as fieldsOf, type Raw } from "@/lib/workflow";

type Run = S<"Run">;
type SessionEvent = S<"SessionEvent">;

/** Calls `onEvent` for each new event of the session (by seq), from the start of its log. */
function useSessionEvents(sessionId: string | null, onEvent: (e: SessionEvent) => void) {
  const handler = useRef(onEvent);
  useEffect(() => {
    handler.current = onEvent;
  });
  useEffect(() => {
    if (!sessionId) return;
    // ponytail: replays the whole log on open (cheap for M0 sessions); keep a cursor per session if logs grow long
    let last = 0;
    let stop = false;
    let poll: ReturnType<typeof setTimeout> | undefined;
    const take = (e: SessionEvent) => {
      if (e.seq <= last) return;
      last = e.seq;
      handler.current(e);
    };
    const path = { params: { path: { session_id: sessionId } } };
    const es = new EventSource(
      `/api/v1/studio/sessions/${encodeURIComponent(sessionId)}/events?since=0`,
    );
    const onMessage = (m: MessageEvent<string>) => {
      try {
        take(JSON.parse(m.data) as SessionEvent);
      } catch {
        /* not an event */
      }
    };
    for (const t of ["status", "progress", "output"])
      es.addEventListener(t, onMessage as EventListener);
    const tick = async () => {
      const page = await ok(
        api.GET("/v1/studio/sessions/{session_id}/events", {
          params: { ...path.params, query: { since: last } },
        }),
      ).catch(() => null);
      (page as S<"EventPage"> | null)?.events.forEach(take); // the JSON form (no Accept: text/event-stream)
      if (!stop) poll = setTimeout(() => void tick(), 2000);
    };
    es.onerror = () => {
      if (es.readyState === EventSource.CLOSED && !stop && !poll) void tick();
    };
    return () => {
      stop = true;
      es.close();
      clearTimeout(poll);
    };
  }, [sessionId]);
}

/** The Create screen; `initialSession` comes from `?s=`. */
export function Create({ initialSession }: { initialSession?: string }) {
  const [sessions, setSessions] = useState<S<"StudioSession">[] | null>(null);
  const [sessionId, setSessionId] = useState<string | null>(null);
  const [workflows, setWorkflows] = useState<S<"Workflow">[] | null>(null);
  const [workflowId, setWorkflowId] = useState<string>("");
  const [raws, setRaws] = useState<Record<string, Raw>>({});
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [runs, setRuns] = useState<Run[] | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  // the run list as the event handler last left it, so an event and a fetch never fold onto a stale copy, and the
  // open session, so a late answer about another one is dropped
  const runsRef = useRef<Run[]>([]);
  const sessionRef = useRef<string | null>(null);
  const put = useCallback((next: Run[]) => {
    runsRef.current = next;
    setRuns(next);
  }, []);
  /** Opens a session (null: none yet); its runs load in the effect below. */
  const open = useCallback((id: string | null) => {
    sessionRef.current = id;
    setSessionId(id);
    setSelected(null);
    runsRef.current = [];
    setRuns(id ? null : []);
  }, []);

  const load = useCallback(() => {
    Promise.all([ok(api.GET("/v1/studio/sessions")), ok(api.GET("/v1/studio/workflows"))])
      .then(([s, w]) => {
        setError("");
        setSessions(s.sessions);
        open(s.sessions.find((x) => x.id === initialSession)?.id ?? s.sessions[0]?.id ?? null);
        setWorkflows(w.workflows);
        setWorkflowId((id) => id || w.workflows[0]?.id || "");
      })
      .catch((e) => setError(message(e)));
  }, [initialSession, open]);
  useEffect(load, [load]);

  // the open session is in the URL, without a server round trip
  useEffect(() => {
    if (sessionId) history.replaceState(null, "", `/?s=${encodeURIComponent(sessionId)}`);
  }, [sessionId]);

  const loadRuns = useCallback(
    (id: string) =>
      ok(api.GET("/v1/studio/sessions/{session_id}/runs", { params: { path: { session_id: id } } }))
        .then((r) => id === sessionRef.current && put(mergeRuns(runsRef.current, r.runs)))
        .catch((e) => setError(message(e))),
    [put],
  );
  useEffect(() => {
    if (sessionId) void loadRuns(sessionId);
  }, [sessionId, loadRuns]);

  const refetchRun = useCallback(
    (id: string) =>
      ok(api.GET("/v1/studio/runs/{run_id}", { params: { path: { run_id: id } } }))
        .then((r) => put(runsRef.current.map((x) => (x.id === r.id ? r : x))))
        .catch(() => undefined),
    [put],
  );
  const listTimer = useRef<ReturnType<typeof setTimeout>>(undefined);
  useSessionEvents(runs === null ? null : sessionId, (e) => {
    const r = applyEvent(runsRef.current, e);
    put(r.runs);
    if (r.ended) void refetchRun(e.run_id);
    if (!r.known && sessionId) {
      clearTimeout(listTimer.current);
      listTimer.current = setTimeout(() => void loadRuns(sessionId), 300);
    }
  });

  const workflow = workflows?.find((w) => w.id === workflowId);
  const fields = useMemo(() => (workflow ? fieldsOf(workflow) : []), [workflow]);
  const change = (key: string, raw: Raw) => {
    setRaws((r) => ({ ...r, [key]: raw }));
    setErrors((errs) => {
      const next = { ...errs };
      delete next[key];
      return next;
    });
  };

  async function newSession(title?: string) {
    const s = await ok(api.POST("/v1/studio/sessions", { body: title ? { title } : {} }));
    setSessions((list) => [s, ...(list ?? [])]);
    open(s.id);
    return s.id;
  }

  async function generate(e: React.FormEvent) {
    e.preventDefault();
    if (!workflow) return;
    const { inputs, errors: errs } = buildInputs(fields, raws);
    setErrors(errs);
    if (Object.keys(errs).length) return;
    setBusy(true);
    setError("");
    try {
      const prompt = fields.find((f) => f.param.type === "text");
      const title = prompt
        ? String(raws[prompt.key] ?? "")
            .trim()
            .slice(0, 60)
        : "";
      const sid = sessionId ?? (await newSession(title || undefined));
      const run = await ok(
        api.POST("/v1/studio/sessions/{session_id}/runs", {
          params: { path: { session_id: sid } },
          body: { workflow_id: workflow.id, inputs },
        }),
      );
      if (sid === sessionRef.current) put(mergeRuns(runsRef.current, [run]));
      setSelected(run.id);
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  async function cancel(id: string) {
    try {
      const r = await okOr(
        api.POST("/v1/studio/runs/{run_id}/cancel", { params: { path: { run_id: id } } }),
        409,
      );
      if (r) put(mergeRuns(runsRef.current, [r]));
      else await refetchRun(id); // it had already ended
    } catch (err) {
      setError(message(err));
    }
  }

  const shown = runs?.find((r) => r.id === selected) ?? runs?.[0];
  const anyActive = !!runs?.some((r) => isActive(r.status));

  return (
    <>
      <main id="main" className="flex min-w-0 flex-[999_1_560px] flex-col gap-3.5 px-5 pt-4 pb-5">
        <div className="flex flex-wrap items-center gap-3">
          <label htmlFor="session" className="sr-only">
            Session
          </label>
          <select
            id="session"
            className="h-10 max-w-[40ch] truncate rounded-[10px] border-0 bg-transparent px-2 font-display text-lg font-bold text-t1"
            value={sessionId ?? ""}
            disabled={!sessions?.length}
            onChange={(e) => open(e.target.value)}
          >
            {!sessions?.length && <option value="">New session</option>}
            {sessions?.map((s) => (
              <option key={s.id} value={s.id} className="bg-s2 font-sans text-sm font-normal">
                {s.title}
              </option>
            ))}
          </select>
          <button
            type="button"
            className="chip bevel size-9 justify-center p-0"
            aria-label="New session"
            title="New session"
            onClick={() => newSession().catch((e) => setError(message(e)))}
          >
            <Plus size={16} aria-hidden />
          </button>
          {workflows && workflows.length > 1 ? (
            <select
              aria-label="Workflow"
              className="h-8 rounded-full border-0 bg-s2 px-3 text-[12px] text-t2"
              value={workflowId}
              onChange={(e) => setWorkflowId(e.target.value)}
            >
              {workflows.map((w) => (
                <option key={w.id} value={w.id}>
                  {w.title} · {w.env}
                </option>
              ))}
            </select>
          ) : (
            workflow && (
              <span className="rounded-full bg-s2 px-2.5 py-1 text-[12px] font-medium text-t2">
                {workflow.title} · {workflow.env}
              </span>
            )
          )}
        </div>

        {error && <ErrorNote onRetry={sessions ? undefined : load}>{error}</ErrorNote>}
        {workflows?.length === 0 && (
          <ErrorNote onRetry={load}>No workflows yet: no runner has published any.</ErrorNote>
        )}

        <Stage run={shown} workflow={workflow} />

        {shown && (
          <RunStrip run={shown} workflow={workflow} onCancel={() => void cancel(shown.id)} />
        )}

        {!!runs?.length && (
          <div
            role="group"
            aria-label="Takes"
            className="flex items-center gap-2.5 overflow-x-auto py-1"
          >
            {runs.map((r, i) => (
              <Take
                key={r.id}
                run={r}
                n={runs.length - i}
                current={r.id === shown?.id}
                onPick={() => setSelected(r.id)}
              />
            ))}
          </div>
        )}

        <form
          onSubmit={generate}
          aria-label="Composer"
          className="panel flex flex-col gap-3 bg-s2 px-4 py-3.5"
        >
          <TextFields fields={fields} raws={raws} errors={errors} onChange={change} />
          <div className="flex flex-wrap items-center gap-2">
            <div className="grow" />
            <button
              type="submit"
              className="key h-14 px-6 text-sm uppercase"
              disabled={!workflow || busy}
            >
              {busy ? "Sending…" : anyActive ? "Add to queue" : "Generate"}
            </button>
          </div>
        </form>
      </main>

      <aside
        aria-label="Inspector"
        className="aside m-3.5 ml-0 hidden flex-[1_1_300px] flex-col gap-5 self-start px-[18px] pt-4 pb-6 lg:flex"
      >
        <Inspector fields={fields} raws={raws} errors={errors} onChange={change} />
      </aside>
    </>
  );
}

/** A node id's title from the workflow ("echo" → "Echo"), else the id. */
function nodeTitle(workflow: S<"Workflow"> | undefined, node: string) {
  const kind = workflow?.nodes[node]?.kind;
  return (kind && workflow?.node_schemas[kind]?.title) || node;
}

/** The big picture: the take, the run's state, or the empty-state headline. */
function Stage({ run, workflow }: { run?: Run; workflow?: S<"Workflow"> }) {
  const out = run?.outputs[0];
  const prompt = Object.values(run?.inputs ?? {}).find((v) => typeof v === "string" && v);
  return (
    <div
      className="relative flex min-h-[340px] grow items-center justify-center bg-inset p-6"
      style={{ borderRadius: "28px 28px 28px 12px" }}
    >
      {!run ? (
        <h1 className="font-display text-3xl font-bold text-t1">What will you make?</h1>
      ) : out?.kind === "image" ? (
        <img
          src={safeUrl(out.url)}
          alt={prompt ? String(prompt) : "The take"}
          width={out.width ?? undefined}
          height={out.height ?? undefined}
          className="max-h-[60vh] w-auto max-w-full rounded-[10px] object-contain"
        />
      ) : (
        <div className="flex aspect-video w-full max-w-[760px] flex-col items-center justify-center gap-1.5 rounded-[10px] border border-dashed border-dash p-4 text-center text-t2">
          {run.status === "failed" ? (
            <>
              <span className="text-[15px] font-medium text-bad">Failed</span>
              <span className="mono text-[12px] break-words text-t1">{run.error}</span>
            </>
          ) : (
            <span className="text-[15px] font-medium text-t1">
              {run.status === "queued"
                ? "Queued"
                : run.status === "running"
                  ? `${Math.round(fraction(run) * 100)}%`
                  : run.status === "cancelled"
                    ? "Cancelled"
                    : "No output"}
            </span>
          )}
        </div>
      )}
      {run?.status === "running" && run.progress && (
        <span className="mono absolute top-4 left-4 rounded-lg bg-s2 px-2.5 py-1.5 text-[12px]">
          {nodeTitle(workflow, run.progress.node)} · step {run.progress.step} of {run.progress.of}
        </span>
      )}
    </div>
  );
}

/** The run's progress line: node, bar and step while it works, its end state after; Cancel while active. */
function RunStrip({
  run,
  workflow,
  onCancel,
}: {
  run: Run;
  workflow?: S<"Workflow">;
  onCancel: () => void;
}) {
  const active = isActive(run.status);
  return (
    <div aria-label="Run progress" className="flex flex-wrap items-center gap-2 text-[13px]">
      {run.progress ? (
        <span className="flex items-center gap-2.5 rounded-lg bg-s2 px-2.5 py-1.5 font-medium">
          {nodeTitle(workflow, run.progress.node)}
          <span
            role="progressbar"
            aria-label="Progress"
            aria-valuemin={0}
            aria-valuemax={run.progress.of}
            aria-valuenow={run.progress.step}
            className="block h-1 w-[120px] rounded-sm bg-s5"
          >
            <span
              className="block h-1 rounded-sm bg-a1 transition-[width]"
              style={{ width: `${fraction(run) * 100}%` }}
            />
          </span>
          <span className="mono font-normal">
            {run.progress.step} / {run.progress.of}
          </span>
        </span>
      ) : null}
      <span
        aria-live="polite"
        className={`rounded-lg bg-s1 px-2.5 py-1.5 ${run.status === "failed" ? "text-bad" : "text-t2"}`}
      >
        {run.status}
        {run.host ? ` · ${run.host}` : ""}
      </span>
      <div className="grow" />
      {active && (
        <button type="button" className="chip bevel h-9 bg-transparent" onClick={onCancel}>
          <X size={14} aria-hidden /> Cancel
        </button>
      )}
      <Info>
        Runs are queued on the one live runner host that has the workflow&apos;s env. Cancel stops
        it at the next step.
      </Info>
    </div>
  );
}

/** One take in the strip: its picture, else its state. */
function Take({
  run,
  n,
  current,
  onPick,
}: {
  run: Run;
  n: number;
  current: boolean;
  onPick: () => void;
}) {
  const out = run.outputs[0];
  return (
    <button
      type="button"
      aria-label={`Take ${n}, ${run.status}`}
      aria-pressed={current}
      onClick={onPick}
      className={`mono grid h-[63px] w-28 shrink-0 place-items-center overflow-hidden rounded-lg bg-s1 text-[11px] ${
        current ? "border-2 border-a1" : "border border-s5"
      } ${run.status === "failed" ? "text-bad" : "text-t2"}`}
    >
      {out?.kind === "image" ? (
        <img src={safeUrl(out.url)} alt="" loading="lazy" className="size-full object-cover" />
      ) : run.status === "running" ? (
        `${Math.round(fraction(run) * 100)}%`
      ) : (
        run.status
      )}
    </button>
  );
}
