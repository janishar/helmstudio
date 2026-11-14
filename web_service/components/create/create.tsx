"use client";
// Create: one session's runs of one workflow (docs/mock/Main.dc.html). The form comes from the workflow's node
// schemas; progress comes live from the session's event stream (SSE through the BFF, ?since= polling when the stream
// can't be held).
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Check, ChevronDown, Cpu, Maximize, Minus, Plus, Star } from "lucide-react";
import { FullScreen, useFullScreenKey } from "@/components/media";
import { ErrorNote } from "@/components/kit";
import { Chips, Inspector, RefChips, TextFields, canvasOf, idOf } from "@/components/create/form";
import { takeDrop, useRefUploads } from "@/components/create/refs";
import { api, message, ok, okOr, type S } from "@/lib/api";
import { safeUrl } from "@/lib/format";
import {
  applyEvent,
  clock,
  etaText,
  followSession,
  fraction,
  isActive,
  lastDuration,
  mergeRuns,
  seedOf,
  segments,
  track,
  type RunTrack,
} from "@/lib/runs";
import { stripItems } from "@/lib/takes";
import {
  TAKE_MIME,
  capacity,
  carriesImage,
  editLabel,
  imageInputs,
  imageWorkflows,
  refCount,
  refIds,
  targetLabel,
} from "@/lib/refs";
import {
  buildInputs,
  fields as fieldsOf,
  nodeTitle,
  queueInputs,
  rawsFrom,
  visible,
  type Field,
  type Raw,
  type Update,
} from "@/lib/workflow";

type Run = S<"Run">;
type Take = S<"Take">;
type SessionEvent = S<"SessionEvent">;

/** The most runs one Generate queues (consecutive seeds). */
const MAX_QUEUE = 4;

/** Calls `onEvent` for each new event of the session (see followSession). */
function useSessionEvents(sessionId: string | null, onEvent: (e: SessionEvent) => void) {
  const handler = useRef(onEvent);
  useEffect(() => {
    handler.current = onEvent;
  });
  useEffect(
    () => (sessionId ? followSession(sessionId, (e) => handler.current(e)) : undefined),
    [sessionId],
  );
}

/** The runner hosts, refreshed every 10 s while the tab is visible (they rewrite their state that often) and at once
 * when it shows again; null until the first answer. */
function useHosts() {
  const [hosts, setHosts] = useState<S<"Host">[] | null>(null);
  useEffect(() => {
    let stop = false;
    const tick = () => {
      if (document.hidden) return;
      ok(api.GET("/v1/studio/hosts"))
        .then((h) => !stop && setHosts(h.hosts))
        .catch(() => undefined);
    };
    tick();
    const t = setInterval(tick, 10_000);
    document.addEventListener("visibilitychange", tick);
    return () => {
      stop = true;
      clearInterval(t);
      document.removeEventListener("visibilitychange", tick);
    };
  }, []);
  return hosts;
}

/** What the page read on the server: the sessions and workflows, the open session (null: none yet) and its runs and
 * takes, as Create's own first load would leave them. */
export type CreateInitial = {
  sessions: S<"StudioSession">[];
  workflows: S<"Workflow">[];
  sessionId: string | null;
  runs: Run[];
  takes: Take[];
};

/** The Create screen; `initialSession` comes from `?s=`, `reuse` (a take id whose settings fill the form) from
 * `?reuse=`, `use` (a take to use in a workflow: the gallery's "Use in…") from `?use=&w=`, `initial` from the server
 * (without it, Create loads everything itself). */
export function Create({
  initialSession,
  reuse,
  use,
  initial,
}: {
  initialSession?: string;
  reuse?: string;
  use?: { take: string; workflow: string };
  initial?: CreateInitial;
}) {
  const [sessions, setSessions] = useState<S<"StudioSession">[] | null>(initial?.sessions ?? null);
  const [sessionId, setSessionId] = useState<string | null>(initial?.sessionId ?? null);
  const [workflows, setWorkflows] = useState<S<"Workflow">[] | null>(initial?.workflows ?? null);
  // the workflow picked here or by a reuse; "" follows the session's newest run
  const [chosen, setChosen] = useState("");
  const [raws, setRaws] = useState<Record<string, Raw>>({});
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [runs, setRuns] = useState<Run[] | null>(initial?.runs ?? null);
  const [takes, setTakes] = useState<Take[]>(initial?.takes ?? []);
  const [tracks, setTracks] = useState<Record<string, RunTrack>>({});
  const [selected, setSelected] = useState<string | null>(null);
  const [full, setFull] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [count, setCount] = useState(1);
  const [openKey, setOpenKey] = useState<string | null>(null);
  const hosts = useHosts();
  // the run list as the event handler last left it, so an event and a fetch never fold onto a stale copy, and the
  // open session, so a late answer about another one is dropped
  const runsRef = useRef<Run[]>(initial?.runs ?? []);
  const sessionRef = useRef<string | null>(initial?.sessionId ?? null);
  // the session whose runs and takes came from the server: not fetched again until another one is opened
  const seeded = useRef(initial?.sessionId ?? null);
  const put = useCallback((next: Run[]) => {
    runsRef.current = next;
    setRuns(next);
  }, []);
  /** Opens a session (null: none yet); its runs and takes load in the effect below. The workflow follows the
   * session's newest run unless `keep` (a new session keeps the one picked). */
  const open = useCallback((id: string | null, keep = false) => {
    seeded.current = null;
    sessionRef.current = id;
    setSessionId(id);
    setSelected(null);
    setTracks({});
    setTakes([]);
    if (!keep) setChosen("");
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
      })
      .catch((e) => setError(message(e)));
  }, [initialSession, open]);
  // the server's read stands for the first load; a later `?s=` (a new `load`) loads again
  const serverLoad = useRef(initial ? load : null);
  useEffect(() => {
    if (load !== serverLoad.current) load();
  }, [load]);

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
  const loadTakes = useCallback(
    (id: string) =>
      ok(
        api.GET("/v1/studio/sessions/{session_id}/takes", {
          // ponytail: the newest 200; page with next_cursor if a session's strip ever needs more
          params: { path: { session_id: id }, query: { limit: 200 } },
        }),
      )
        .then((p) => id === sessionRef.current && setTakes(p.takes))
        .catch((e) => setError(message(e))),
    [],
  );
  useEffect(() => {
    if (sessionId && sessionId !== seeded.current) {
      void loadRuns(sessionId);
      void loadTakes(sessionId);
    }
  }, [sessionId, loadRuns, loadTakes]);

  // a reused take's settings, once the workflows are known
  const reused = useRef(false);
  useEffect(() => {
    if (!reuse || !workflows || reused.current) return;
    reused.current = true;
    ok(api.POST("/v1/studio/takes/{take_id}/reuse", { params: { path: { take_id: reuse } } }))
      .then((r) => {
        const w = workflows.find((x) => x.id === r.workflow_id);
        if (!w) return setError(`The workflow ${r.workflow_id} is not available any more.`);
        setChosen(w.id);
        setRaws(rawsFrom(fieldsOf(w), r.inputs));
        setErrors({});
        setOpenKey(null);
      })
      .catch((e) => setError(message(e)));
  }, [reuse, workflows]);

  /** "Use in…": the workflow, with the take as its image input (merged over the form). */
  const applyUse = useCallback(
    (takeId: string, workflowId: string) =>
      ok(
        api.POST("/v1/studio/takes/{take_id}/use", {
          params: { path: { take_id: takeId } },
          body: { workflow_id: workflowId },
        }),
      )
        .then((r) => {
          const w = workflows?.find((x) => x.id === r.workflow_id);
          if (!w) return setError(`The workflow ${r.workflow_id} is not available any more.`);
          setChosen(w.id);
          setRaws((old) => ({ ...old, ...rawsFrom(fieldsOf(w), r.inputs) }));
          setErrors({});
          setOpenKey(null);
        })
        .catch((e) => setError(message(e))),
    [workflows],
  );
  const used = useRef(false);
  useEffect(() => {
    if (!use || !workflows || used.current) return;
    used.current = true;
    void applyUse(use.take, use.workflow);
  }, [use, workflows, applyUse]);

  async function star(t: Take) {
    try {
      const n = await ok(
        api.PATCH("/v1/studio/takes/{take_id}", {
          params: { path: { take_id: t.id } },
          body: { starred: !t.starred },
        }),
      );
      setTakes((list) => list.map((x) => (x.id === n.id ? n : x)));
    } catch (err) {
      setError(message(err));
    }
  }

  const refetchRun = useCallback(
    (id: string) =>
      ok(api.GET("/v1/studio/runs/{run_id}", { params: { path: { run_id: id } } }))
        .then((r) => put(runsRef.current.map((x) => (x.id === r.id ? r : x))))
        .catch(() => undefined),
    [put],
  );
  const listTimer = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(() => () => clearTimeout(listTimer.current), []);
  useSessionEvents(runs === null ? null : sessionId, (e) => {
    setTracks((t) => track(t, e));
    const r = applyEvent(runsRef.current, e);
    put(r.runs);
    if (r.ended && sessionId) {
      void refetchRun(e.run_id);
      void loadTakes(sessionId);
    }
    if (!r.known && sessionId) {
      clearTimeout(listTimer.current);
      listTimer.current = setTimeout(() => void loadRuns(sessionId), 300);
    }
  });

  const known = (id?: string) => (workflows?.some((w) => w.id === id) ? id : undefined);
  // until the session's runs are in, its workflow isn't known: no form rather than a flash of the first one
  const workflowId =
    known(chosen) ??
    (runs === null && sessionId ? "" : (known(runs?.[0]?.workflow_id) ?? workflows?.[0]?.id ?? ""));
  const workflow = workflows?.find((w) => w.id === workflowId);
  const all = useMemo(() => (workflow ? fieldsOf(workflow) : []), [workflow]);
  // the params whose `when` holds now: the others are neither shown nor sent
  const fields = visible(all, raws);
  const seedField = fields.find((f) => f.param.type === "seed");
  const change = (key: string, u: Update) => {
    setRaws((r) => ({ ...r, [key]: typeof u === "function" ? u(r[key]) : u }));
    setErrors((errs) => {
      const next = { ...errs };
      delete next[key];
      return next;
    });
  };
  const uploads = useRefUploads(change);
  // a drop or paste on the stage or the composer goes to the first image input
  const refField = imageInputs(fields)[0];
  const intoRefs = (dt: DataTransfer) =>
    !!refField &&
    takeDrop(
      dt,
      refField,
      capacity(refField) - refIds(refField, raws[refField.key]).length,
      change,
      uploads,
    );
  const refs = refCount(fields, raws);
  /** Opens a field's inspector control (or the canvas picker) and moves focus to it. */
  const show = (key: string) => {
    if (key !== "canvas") setOpenKey(key);
    requestAnimationFrame(() => {
      // a size no aspect chip matches leaves none pressed: then the first one
      const q = (s: string) => document.querySelector<HTMLElement>(s);
      const el =
        key === "canvas"
          ? (q("#canvas-pick [aria-pressed=true]") ?? q("#canvas-pick button"))
          : q(`#${idOf(key)}-ed :is(input, .opt)`);
      el?.scrollIntoView({ block: "nearest" });
      el?.focus();
    });
  };

  async function newSession(title?: string) {
    const s = await ok(api.POST("/v1/studio/sessions", { body: title ? { title } : {} }));
    setSessions((list) => [s, ...(list ?? [])]);
    open(s.id, true);
    return s.id;
  }

  // set at once (state lags a render), so a second ⌘Enter while a queue is being sent does nothing
  const sending = useRef(false);
  async function generate(e: React.FormEvent) {
    e.preventDefault();
    if (!workflow || sending.current) return;
    const built = buildInputs(fields, raws);
    const queued = queueInputs(fields, built.inputs, seedField ? count : 1);
    const errs = { ...built.errors, ...queued.errors };
    setErrors(errs);
    if (Object.keys(errs).length) return;
    sending.current = true;
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
      for (const each of queued.runs) {
        const run = await ok(
          api.POST("/v1/studio/sessions/{session_id}/runs", {
            params: { path: { session_id: sid } },
            body: { workflow_id: workflow.id, inputs: each },
          }),
        );
        if (sid === sessionRef.current) put(mergeRuns(runsRef.current, [run]));
        setSelected(run.id);
      }
    } catch (err) {
      setError(message(err));
    } finally {
      sending.current = false;
      setBusy(false);
    }
  }

  async function cancel(id: string) {
    try {
      const r = await okOr(
        api.POST("/v1/studio/runs/{run_id}/cancel", { params: { path: { run_id: id } } }),
        409,
      );
      // only into the open session: Stop may answer after another one was opened
      if (r) {
        if (r.session_id === sessionRef.current) put(mergeRuns(runsRef.current, [r]));
      } else await refetchRun(id); // it had already ended
    } catch (err) {
      setError(message(err));
    }
  }

  // the strip: takes, and the runs without one; a selected run that finished shows its take
  const items = runs ? stripItems(runs, takes) : [];
  const picked =
    items.find((i) => (i.take ?? i.run).id === selected) ??
    items.find((i) => i.take?.run_id === selected) ??
    items[0];
  const shownTake = picked?.take;
  useFullScreenKey(!!shownTake && !full, () => setFull(true));
  const shown = picked?.run ?? runs?.find((r) => r.id === shownTake?.run_id);
  const shownWorkflow =
    workflows?.find((w) => w.id === (shown?.workflow_id ?? shownTake?.made_with?.workflow_id)) ??
    workflow;
  const anyActive = !!runs?.some((r) => isActive(r.status));
  const took = workflow && runs ? lastDuration(runs, workflow.id) : null;
  const host = hosts?.find((h) => h.env === workflow?.env && h.state !== "offline" && h.memory);
  const session = sessions?.find((s) => s.id === sessionId);

  return (
    <>
      <main id="main" className="flex min-w-0 flex-[999_1_560px] flex-col gap-3.5 px-5 pt-4 pb-5">
        <div className="flex flex-wrap items-center gap-3">
          <button
            type="button"
            popoverTarget="sessions"
            aria-label={`Session: ${session?.title ?? "new"}. Change session`}
            className="flex h-10 max-w-[40ch] min-w-0 items-center gap-2 rounded-[10px] px-3 font-display text-lg font-bold"
            style={{ anchorName: "--sessions" } as React.CSSProperties}
          >
            <span className="truncate">{session?.title ?? "New session"}</span>
            <ChevronDown size={16} strokeWidth={2} aria-hidden className="shrink-0" />
          </button>
          <div
            id="sessions"
            popover="auto"
            role="dialog"
            aria-label="Sessions"
            className="pop w-72"
            style={
              {
                positionAnchor: "--sessions",
                positionArea: "bottom span-right",
                marginTop: 6,
              } as React.CSSProperties
            }
          >
            <div className="flex max-h-[60vh] flex-col gap-1 overflow-y-auto">
              <button
                type="button"
                className="chip bevel mb-1 justify-center rounded-[10px] bg-s4"
                onClick={() => {
                  document.getElementById("sessions")?.hidePopover();
                  newSession().catch((e) => setError(message(e)));
                }}
              >
                <Plus size={14} aria-hidden /> New session
              </button>
              {sessions?.map((s) => (
                <button
                  key={s.id}
                  type="button"
                  aria-current={s.id === sessionId || undefined}
                  className="truncate rounded-[10px] px-3 py-2 text-left text-[13px] hover:bg-s3 aria-[current]:bg-s4 aria-[current]:font-semibold"
                  onClick={() => {
                    document.getElementById("sessions")?.hidePopover();
                    if (s.id !== sessionId) open(s.id);
                  }}
                >
                  {s.title}
                </button>
              ))}
            </div>
          </div>
          {workflows && workflows.length > 1 ? (
            <span className="relative inline-flex items-center text-t2">
              <select
                aria-label="Workflow"
                className="appearance-none rounded-full border-0 bg-s2 py-1 pr-7 pl-2.5 text-[12px] font-medium text-t2"
                value={workflowId}
                onChange={(e) => {
                  setChosen(e.target.value);
                  setOpenKey(null);
                }}
              >
                {workflows.map((w) => (
                  <option key={w.id} value={w.id}>
                    {w.id === workflowId ? editLabel(w.title, refs) : w.title}
                  </option>
                ))}
              </select>
              <ChevronDown
                size={12}
                aria-hidden
                className="pointer-events-none absolute right-2.5"
              />
            </span>
          ) : (
            workflow && (
              <span className="rounded-full bg-s2 px-2.5 py-1 text-[12px] font-medium text-t2">
                {editLabel(workflow.title, refs)}
              </span>
            )
          )}
          <div className="grow" />
          {host?.memory && <Memory host={host} memory={host.memory} />}
        </div>

        {error && (
          <ErrorNote
            onRetry={
              !sessions
                ? load
                : runs === null && sessionId
                  ? () => {
                      setError("");
                      void loadRuns(sessionId);
                    }
                  : undefined
            }
          >
            {error}
          </ErrorNote>
        )}
        {workflows?.length === 0 && (
          <ErrorNote onRetry={load}>No workflows yet: no runner has published any.</ErrorNote>
        )}

        <Stage
          run={shown}
          take={shownTake}
          workflow={shownWorkflow}
          onStar={shownTake && (() => void star(shownTake))}
          onFull={() => setFull(true)}
          onDrop={refField && intoRefs}
        />
        {full && shownTake && (
          <FullScreen
            take={shownTake}
            onClose={() => setFull(false)}
            onStep={(d) => {
              const list = items.flatMap((i) => (i.take ? [i.take] : []));
              const next = list[list.findIndex((t) => t.id === shownTake.id) + d];
              if (next) setSelected(next.id);
            }}
          />
        )}

        {shown && shownWorkflow && (
          <RunStrip
            run={shown}
            workflow={shownWorkflow}
            track={tracks[shown.id]}
            onCancel={() => void cancel(shown.id)}
          />
        )}

        {!!items.length && (
          <div
            role="group"
            aria-label="Takes"
            className="flex shrink-0 items-center gap-2.5 overflow-x-auto p-0.5"
          >
            {items.map((it, i) =>
              it.take ? (
                <TakeTile
                  key={it.take.id}
                  take={it.take}
                  current={it === picked}
                  onPick={() => setSelected(it.take.id)}
                />
              ) : (
                <RunTile
                  key={it.run.id}
                  run={it.run}
                  thumb={takes.find((t) => t.id === it.run.outputs[0]?.take_id)?.thumb_url}
                  n={items.length - i}
                  seedKey={seedKeyOf(workflows?.find((w) => w.id === it.run.workflow_id))}
                  current={it === picked}
                  onPick={() => setSelected(it.run.id)}
                />
              ),
            )}
            {shownTake?.kind === "image" && workflows && (
              <UseIn
                workflows={imageWorkflows(workflows)}
                onUse={(w) => void applyUse(shownTake.id, w.id)}
              />
            )}
          </div>
        )}

        <form
          onSubmit={generate}
          onPaste={(e) => {
            if (!e.defaultPrevented && intoRefs(e.clipboardData)) e.preventDefault();
          }}
          noValidate
          aria-label="Composer"
          className="panel flex shrink-0 flex-col gap-3 bg-s2 px-4 py-3.5 [box-shadow:0_16px_40px_color-mix(in_srgb,var(--shd)_45%,transparent)]"
        >
          {refField && (
            <div className="flex flex-wrap items-center gap-2">
              <RefChips
                fields={fields}
                raws={raws}
                errors={errors}
                onChange={change}
                uploads={uploads}
              />
            </div>
          )}
          <TextFields fields={fields} raws={raws} errors={errors} onChange={change} />
          {fields.some((f) => f.param.type !== "text" && errors[f.key]) && (
            <ErrorNote>Some settings are not valid: see the Inspector.</ErrorNote>
          )}
          {workflow &&
            hosts &&
            !hosts.some((h) => h.env === workflow.env && h.state !== "offline") && (
              <p role="status" className="text-[12px] text-t2">
                No runner for {workflow.env} is online; runs wait until one is.
              </p>
            )}
          <div className="flex flex-wrap items-center gap-2">
            <Chips fields={fields} raws={raws} onChange={change} show={show} />
            <div className="grow" />
            {seedField && (
              <div
                role="group"
                aria-label="Seeds to queue"
                className="bevel flex h-9 items-center rounded-[10px] bg-s4 text-[13px] font-medium"
              >
                <button
                  type="button"
                  aria-label="Fewer seeds"
                  disabled={count <= 1}
                  onClick={() => setCount((n) => n - 1)}
                  className="grid size-9 place-items-center rounded-[10px] disabled:text-t2 disabled:opacity-60"
                >
                  <Minus size={14} aria-hidden />
                </button>
                <span className="mono" aria-live="polite">
                  {count} / {MAX_QUEUE}
                </span>
                <button
                  type="button"
                  aria-label="More seeds"
                  disabled={count >= MAX_QUEUE}
                  onClick={() => setCount((n) => n + 1)}
                  className="grid size-9 place-items-center rounded-[10px] disabled:text-t2 disabled:opacity-60"
                >
                  <Plus size={14} aria-hidden />
                </button>
              </div>
            )}
            <button
              type="submit"
              title="Generate (⌘ or Ctrl + Enter)"
              className="key flex h-14 flex-col items-center justify-center gap-px px-6"
              disabled={!workflow || busy}
            >
              <span className="text-sm">
                {busy ? "Sending…" : anyActive ? "Add to queue" : "Generate"}
              </span>
              {took != null && !busy && (
                <span className="mono text-[11px] font-medium">last took {clock(took)}</span>
              )}
            </button>
          </div>
        </form>
      </main>

      <aside
        aria-label="Inspector"
        className="aside m-3.5 flex flex-[1_1_320px] flex-col gap-[22px] px-[18px] pt-4 pb-6 md:ml-0"
      >
        <Inspector
          fields={fields}
          raws={raws}
          errors={errors}
          onChange={change}
          open={openKey}
          setOpen={setOpenKey}
          uploads={uploads}
        />
      </aside>
    </>
  );
}

/** The memory button: used / budget of the host that runs the workflow; its popover lists what is loaded. */
function Memory({
  host,
  memory,
}: {
  host: S<"Host">;
  memory: { used_gb: number; budget_gb: number };
}) {
  const resident = host.resident ?? [];
  const gb = (n: number) => +n.toFixed(1);
  return (
    <>
      <button
        type="button"
        popoverTarget="memory"
        aria-label={`Memory on ${host.host}: ${gb(memory.used_gb)} of ${gb(memory.budget_gb)} gigabytes used, ${resident.length} loaded`}
        className="bevel mono flex h-10 items-center gap-2 rounded-[10px] border border-s3 bg-s1 px-3 text-[12px]"
        style={{ anchorName: "--memory" } as React.CSSProperties}
      >
        <Cpu size={16} strokeWidth={1.75} aria-hidden />
        {Math.round(memory.used_gb)} / {Math.round(memory.budget_gb)} GB
      </button>
      <div
        id="memory"
        popover="auto"
        role="dialog"
        aria-label="Memory"
        className="pop w-72 text-[13px]"
        style={
          {
            positionAnchor: "--memory",
            positionArea: "bottom span-left",
            marginTop: 6,
          } as React.CSSProperties
        }
      >
        <div className="flex flex-col gap-2">
          <span className="heading">
            {host.host} · {gb(memory.used_gb)} of {gb(memory.budget_gb)} GB
          </span>
          {resident.length ? (
            resident.map((r) => (
              <div
                key={`${r.component}/${r.model}/${r.variant}/${r.precision}`}
                className="flex justify-between gap-3"
              >
                <span className="min-w-0 truncate">
                  {r.component} · {r.variant} · {r.precision}
                </span>
                <span className="mono text-t2">{gb(r.gb)} GB</span>
              </div>
            ))
          ) : (
            <span className="text-t2">Nothing loaded.</span>
          )}
        </div>
      </div>
    </>
  );
}

/** The key of a workflow's seed param, if it has one. */
const seedKeyOf = (w?: S<"Workflow">) => w && fieldsOf(w).find((f) => f.param.type === "seed")?.key;

/** The run's size: its first output's, else what it asked of the canvas, else the workflow's. */
function sizeOf(run: Run, workflow?: S<"Workflow">) {
  const out = run.outputs[0];
  if (out?.width && out.height) return { w: out.width, h: out.height };
  const c = workflow && canvasOf(fieldsOf(workflow), {});
  if (!c) return null;
  const v = (f: Field, d: number) => Number(run.inputs[f.key] ?? d);
  return { w: v(c.width, c.w), h: v(c.height, c.h) };
}

/** The stage: the take as large as fits (with its star), the run's state, or the empty-state headline. */
function Stage({
  run,
  take,
  workflow,
  onStar,
  onFull,
  onDrop,
}: {
  run?: Run;
  take?: Take;
  workflow?: S<"Workflow">;
  onStar?: () => void;
  onFull: () => void;
  /** takes a drop of images or dragged takes as references; absent: the workflow takes none */
  onDrop?: (dt: DataTransfer) => boolean;
}) {
  const [over, setOver] = useState(false);
  const out = take ?? run?.outputs[0];
  // the prompt the run was made with: its input, else the workflow's (inputs hold only what differs)
  const text = workflow && fieldsOf(workflow).find((f) => f.param.type === "text");
  const prompt = take?.made_with?.prompt ?? (text && (run?.inputs[text.key] ?? text.initial));
  const size =
    take?.width && take.height ? { w: take.width, h: take.height } : run && sizeOf(run, workflow);
  const p = run?.progress;
  const node = run?.status === "running" && p ? nodeTitle(workflow, p.node) : "";
  const label = !run
    ? take
      ? "Take"
      : ""
    : node && p
      ? [`${node} · step ${p.step} of ${p.of}`, p.phase].filter(Boolean).join(" · ")
      : {
          queued: "Queued",
          running: "Starting",
          succeeded: "Take",
          failed: "Failed",
          cancelled: "Stopped",
        }[run.status];
  // what a screen reader hears: the state and the node, not every step
  const said = node ? `Running ${node}` : label;
  return (
    <div
      className={`panel relative min-h-[400px] grow bg-inset ${over ? "outline-2 -outline-offset-4 outline-a1 outline-dashed" : ""}`}
      onDragOver={(e) => {
        if (!onDrop || !carriesImage(e.dataTransfer)) return;
        e.preventDefault();
        setOver(true);
      }}
      onDragLeave={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setOver(false);
      }}
      onDrop={(e) => {
        setOver(false);
        if (onDrop?.(e.dataTransfer)) e.preventDefault();
      }}
    >
      {over && (
        <span className="absolute inset-x-0 bottom-4 z-10 text-center text-[13px] font-medium">
          Drop to add as a reference
        </span>
      )}
      <span className="sr-only" aria-live="polite">
        {said}
      </span>
      {(run || take) && (
        <div className="absolute inset-x-4 top-4 flex flex-wrap items-center justify-between gap-2">
          <span
            className={`mono rounded-lg bg-s2/90 px-2.5 py-1.5 text-[12px] ${run?.status === "failed" ? "text-bad" : ""}`}
          >
            {label}
          </span>
          <span className="flex items-center gap-2">
            {take && (
              <button
                type="button"
                aria-label="Full screen"
                title="Full screen (F)"
                onClick={onFull}
                className="bevel grid size-9 place-items-center rounded-lg border border-s5 bg-s2/90"
              >
                <Maximize size={16} strokeWidth={1.75} aria-hidden />
              </button>
            )}
            {size && (
              <span className="mono rounded-lg bg-s2/90 px-2.5 py-1.5 text-[12px] text-t2">
                {size.w} × {size.h}
              </span>
            )}
            {take && onStar && (
              <button
                type="button"
                aria-pressed={take.starred}
                onClick={onStar}
                className="bevel flex h-9 items-center gap-1.5 rounded-lg border border-s5 bg-s2/90 px-3 text-[13px]"
              >
                <Star
                  size={14}
                  aria-hidden
                  className={take.starred ? "fill-a1 text-a1" : undefined}
                />
                {take.starred ? "Starred" : "Star"}
              </button>
            )}
          </span>
        </div>
      )}
      <div
        className="absolute inset-x-6 top-[68px] bottom-6 flex items-center justify-center"
        style={{ containerType: "size" }}
      >
        {!run && !take ? (
          <h1 className="text-center font-display text-[40px] leading-[44px] font-bold tracking-[0.01em]">
            What will you <span className="text-a1">make?</span>
          </h1>
        ) : out?.kind === "image" ? (
          <img
            src={safeUrl(out.url)}
            alt={take?.title || (prompt ? String(prompt) : "The take")}
            width={out.width ?? undefined}
            height={out.height ?? undefined}
            fetchPriority="high"
            className="rounded-[10px] object-cover"
            // as large as fits the stage, keeping its aspect, so the corners round the picture itself
            style={
              size
                ? {
                    width: `min(100cqw, 100cqh * ${size.w / size.h})`,
                    aspectRatio: `${size.w} / ${size.h}`,
                  }
                : { maxWidth: "100%", maxHeight: "100%" }
            }
          />
        ) : !run ? (
          <span className="text-t2">{take?.kind}</span>
        ) : (
          <div
            className="flex max-h-full w-full max-w-[760px] flex-col items-center justify-center gap-1.5 rounded-[10px] border border-dashed border-dash p-4 text-center text-t2"
            style={{ aspectRatio: size ? `${size.w} / ${size.h}` : "16 / 9" }}
          >
            {run.status === "failed" ? (
              <>
                <span className="text-[15px] font-medium text-bad">Failed</span>
                <span className="mono text-[12px] break-words text-t1">{run.error}</span>
              </>
            ) : (
              <span className="text-[15px] font-medium text-t1">
                {isActive(run.status)
                  ? `${Math.round(fraction(run) * 100)}%`
                  : run.status === "cancelled"
                    ? "Stopped"
                    : "No output"}
              </span>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

/** The run's progress: one piece per working node (done with its time, active with its bar, pending), the time
 * left and Stop. */
function RunStrip({
  run,
  workflow,
  track,
  onCancel,
}: {
  run: Run;
  workflow: S<"Workflow">;
  track?: RunTrack;
  onCancel: () => void;
}) {
  const p = run.progress;
  return (
    <div
      role="group"
      aria-label="Run progress"
      className="flex shrink-0 flex-wrap items-center gap-2 text-[13px]"
    >
      {segments(workflow, run, track).map((s) =>
        s.state === "done" ? (
          <span key={s.node} className="step">
            <Check size={14} strokeWidth={2.5} className="text-a1" aria-label="done" />
            {s.title}
            {s.seconds != null && <span className="mono text-t2">{s.seconds.toFixed(1)} s</span>}
          </span>
        ) : s.state === "active" && p ? (
          <span
            key={s.node}
            className="step gap-2.5 border border-s5 bg-s2 font-medium"
            aria-current="step"
          >
            {s.title}
            <span
              role="progressbar"
              aria-label={`${s.title} progress`}
              aria-valuemin={0}
              aria-valuemax={p.of}
              aria-valuenow={p.step}
              className="block h-1 w-[120px] rounded-[2px] bg-s5"
            >
              <span
                className="block h-1 rounded-[2px] bg-a1 transition-[width]"
                style={{ width: `${fraction(run) * 100}%` }}
              />
            </span>
            <span className="mono font-normal">
              {p.step} / {p.of}
            </span>
          </span>
        ) : (
          <span
            key={s.node}
            className={`step text-t2 ${s.state === "stopped" ? "line-through" : ""}`}
          >
            {s.title}
          </span>
        ),
      )}
      <div className="grow" />
      {etaText(run) && <span className="mono text-t2">{etaText(run)}</span>}
      {isActive(run.status) && (
        <button
          type="button"
          className="bevel h-9 rounded-lg border border-s5 px-3 text-[13px]"
          onClick={onCancel}
        >
          Stop
        </button>
      )}
    </div>
  );
}

/** The strip's label for a seed: a random seed is 16 digits, so the middle goes (the title keeps the whole). */
const seedLabel = (seed: number) => {
  const digits = String(seed);
  return `seed ${digits.length > 8 ? `${digits.slice(0, 4)}…${digits.slice(-3)}` : digits}`;
};

/** One take in the strip: its picture labelled by seed (or title), a star when starred. */
function TakeTile({ take, current, onPick }: { take: Take; current: boolean; onPick: () => void }) {
  const seed = take.made_with?.seed;
  const label = seed != null ? seedLabel(seed) : take.title;
  return (
    <button
      type="button"
      aria-label={[
        seed != null ? `Take, seed ${seed}` : `Take, ${take.title}`,
        take.starred && "starred",
      ]
        .filter(Boolean)
        .join(", ")}
      aria-pressed={current}
      title={label}
      onClick={onPick}
      draggable={take.kind === "image"}
      onDragStart={(e) => e.dataTransfer.setData(TAKE_MIME, take.id)}
      className={`bevel mono relative grid h-[63px] w-28 shrink-0 place-items-center overflow-hidden rounded-lg bg-s1 text-[11px] text-t2 ${
        current ? "border-2 border-a1" : "border border-s5"
      }`}
    >
      {take.kind === "image" ? (
        <img
          src={safeUrl(take.thumb_url)}
          alt=""
          loading="lazy"
          decoding="async"
          className="size-full object-cover"
        />
      ) : (
        <span>{take.kind}</span>
      )}
      <span className="absolute bottom-1 left-1 max-w-[calc(100%-8px)] truncate rounded bg-s2/90 px-1 text-t1">
        {label}
      </span>
      {take.starred && (
        <Star size={12} aria-hidden className="absolute top-1 right-1 fill-a1 text-a1" />
      )}
    </button>
  );
}

/** A run in the strip that has no take to show: its picture labelled by seed, else its state. */
function RunTile({
  run,
  thumb,
  n,
  seedKey,
  current,
  onPick,
}: {
  run: Run;
  /** its output's take thumbnail, once that take is loaded; else the output itself */
  thumb?: string;
  n: number;
  seedKey?: string;
  current: boolean;
  onPick: () => void;
}) {
  const out = run.outputs[0];
  const asked = seedKey ? run.inputs[seedKey] : undefined;
  const seed = seedOf(run) ?? (typeof asked === "number" && asked >= 0 ? asked : undefined);
  const label = seed == null ? `take ${n}` : seedLabel(seed);
  const state =
    run.status === "running"
      ? `${Math.round(fraction(run) * 100)}%`
      : run.status === "succeeded"
        ? ""
        : run.status === "cancelled"
          ? "stopped"
          : run.status;
  return (
    <button
      type="button"
      aria-label={[seed != null ? `Take, seed ${seed}` : `Take ${n}`, state]
        .filter(Boolean)
        .join(", ")}
      aria-pressed={current}
      title={seed != null ? `seed ${seed}` : undefined}
      onClick={onPick}
      className={`bevel mono relative grid h-[63px] w-28 shrink-0 place-items-center overflow-hidden rounded-lg bg-s1 text-[11px] ${
        current ? "border-2 border-a1" : "border border-s5"
      } ${run.status === "failed" ? "text-bad" : "text-t2"}`}
    >
      {out?.kind === "image" ? (
        <>
          <img
            src={safeUrl(thumb ?? out.url)}
            alt=""
            loading="lazy"
            decoding="async"
            className="size-full object-cover"
          />
          <span className="absolute bottom-1 left-1 rounded bg-s2/90 px-1 text-t1">{label}</span>
        </>
      ) : (
        <span className="flex flex-col items-center">
          <span>{label}</span>
          {state && <span>{state}</span>}
        </span>
      )}
    </button>
  );
}

/** The strip's "Use in…": a menu of the workflows that take an image; choosing one puts the shown take in it. */
function UseIn({
  workflows,
  onUse,
}: {
  workflows: S<"Workflow">[];
  onUse: (w: S<"Workflow">) => void;
}) {
  if (!workflows.length) return null;
  return (
    <>
      <button
        type="button"
        popoverTarget="use-in"
        className="flex h-11 shrink-0 items-center gap-1.5 rounded-[10px] border border-dashed border-dash bg-transparent px-3.5 text-[13px]"
        style={{ anchorName: "--use-in" } as React.CSSProperties}
      >
        <Plus size={16} aria-hidden />
        Use in…
      </button>
      <div
        id="use-in"
        popover="auto"
        role="menu"
        aria-label="Use in"
        className="pop w-64"
        style={
          {
            positionAnchor: "--use-in",
            positionArea: "top span-left",
            marginBottom: 6,
          } as React.CSSProperties
        }
      >
        <div className="flex flex-col gap-1">
          {workflows.map((w) => (
            <button
              key={w.id}
              type="button"
              role="menuitem"
              className="rounded-[10px] px-3 py-2 text-left text-[13px] hover:bg-s3"
              onClick={() => {
                document.getElementById("use-in")?.hidePopover();
                onUse(w);
              }}
            >
              {targetLabel(w)}
            </button>
          ))}
        </div>
      </div>
    </>
  );
}
