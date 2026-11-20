"use client";
// A pipeline's Studio view (docs/mock/Pipeline.dc.html; owner's Q2, Q3): its steps as cards, each run as ordinary
// runs in a session of the pipeline's own; the take picked from a step's results goes into the next step's input
// (lib/pipeline-studio). The composer and the step's settings are Create's form parts; the aside's Memory card shows
// the step's host with what Admin › Hosts has (pin, unload, after the queue, unload all), for admins only: others
// see it read-only.
import Link from "next/link";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Check, Plus } from "lucide-react";
import { ErrorNote, Info } from "@/components/kit";
import { FullScreen, Thumb } from "@/components/media";
import { Chips, Inspector, TextFields, idOf } from "@/components/create/form";
import { useRefUploads } from "@/components/create/refs";
import { estimateText, usePlannedPeak } from "@/components/create/memory-chip";
import { useUser } from "@/components/shell";
import { ApiError, api, message, ok, type S } from "@/lib/api";
import { fieldErrors } from "@/lib/mentions";
import { mediaOf } from "@/lib/media";
import { applyEvent, followSession, isActive, mergeRuns, pinnedNote } from "@/lib/runs";
import { presetNames, studioView } from "@/lib/studio";
import {
  AFTER_RUN,
  componentText,
  hostCards,
  hostMemory,
  isPinned,
  pinRef,
  settingsOf,
  togglePin,
  type HostCard,
  type HostEntry,
} from "@/lib/hosts";
import { nodeLabel, stepNumbers, type PDoc } from "@/lib/pipeline";
import {
  currentStep,
  loadStudio,
  saveStudio,
  stepRaws,
  studioSteps,
  takeBinds,
  type StudioState,
} from "@/lib/pipeline-studio";
import {
  buildInputs,
  fields as fieldsOf,
  queueInputs,
  visible,
  type Raw,
  type Update,
} from "@/lib/workflow";

type Run = S<"Run">;
type Take = S<"Take">;
type Workflow = S<"Workflow">;

const ENV_NAMES: Record<string, string> = { mlx: "MLX", torch: "PyTorch", cuda: "CUDA" };
const plural = (n: number, w: string) => `${n} ${w}${n === 1 ? "" : "s"}`;
const seedText = (t: Take) => (t.made_with?.seed != null ? `seed ${t.made_with.seed}` : t.title);

export function PipelineStudio({
  pipelineId,
  doc,
  workflows,
  titles,
  hosts,
  head,
  onAddStep,
}: {
  pipelineId: string;
  doc: PDoc;
  workflows: Workflow[];
  titles: Record<string, string>;
  /** the runner hosts (useHosts, polled by the editor for its memory chip) */
  hosts: S<"Host">[] | null;
  /** the top bar (title, badge, the Studio | Graph switch, the memory chip) */
  head: React.ReactNode;
  /** Add step: steps are added in the graph */
  onAddStep: () => void;
}) {
  // read after mount: the server render has no browser store
  const [st, setSt] = useState<StudioState>({ session: null, steps: {} });
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- the browser store is only read in the browser
    setSt(loadStudio(pipelineId));
  }, [pipelineId]);
  const update = useCallback(
    (f: (s: StudioState) => StudioState) =>
      setSt((s) => {
        const next = f(s);
        saveStudio(pipelineId, next);
        return next;
      }),
    [pipelineId],
  );
  const [runs, setRuns] = useState<Run[]>([]);
  const runsRef = useRef<Run[]>([]);
  const put = (next: Run[]) => {
    runsRef.current = next;
    setRuns(next);
  };
  const [takes, setTakes] = useState<Take[]>([]);
  const [chosen, setChosen] = useState<string | null>(null);
  const [edits, setEdits] = useState<Record<string, Record<string, Raw>>>({});
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [openKey, setOpenKey] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [full, setFull] = useState<{ list: Take[]; i: number } | null>(null);

  // the session's runs and takes; a session deleted since starts afresh
  const session = st.session;
  const loadTakes = useCallback((sid: string) => {
    ok(
      api.GET("/v1/studio/sessions/{session_id}/takes", {
        // ponytail: the newest 200 takes of the pipeline's session; page with next_cursor if a session outgrows it
        params: { path: { session_id: sid }, query: { limit: 200 } },
      }),
    )
      .then((p) => setTakes(p.takes))
      .catch(() => undefined);
  }, []);
  useEffect(() => {
    if (!session) return;
    let stop = false;
    api
      .GET("/v1/studio/sessions/{session_id}/runs", { params: { path: { session_id: session } } })
      .then(({ data, response }) => {
        if (stop) return;
        if (response.status === 404) return update(() => ({ session: null, steps: {} }));
        if (data) put(mergeRuns(runsRef.current, data.runs));
      })
      .catch(() => undefined);
    loadTakes(session);
    const unfollow = followSession(session, (e) => {
      const r = applyEvent(runsRef.current, e);
      put(r.runs);
      if (r.ended) {
        void ok(api.GET("/v1/studio/runs/{run_id}", { params: { path: { run_id: e.run_id } } }))
          .then((x) => put(runsRef.current.map((y) => (y.id === x.id ? x : y))))
          .catch(() => undefined);
        loadTakes(session);
      }
    });
    return () => {
      stop = true;
      unfollow();
    };
  }, [session, loadTakes, update]);

  const steps = studioSteps(doc);
  const nums = stepNumbers(doc);
  const step = (chosen && steps.includes(chosen) ? chosen : null) ?? currentStep(doc, st);
  const node = step ? doc.nodes[step] : undefined;
  const w = node?.kind === "step" ? workflows.find((x) => x.id === node.workflow) : undefined;
  const all = useMemo(() => (w ? fieldsOf(w) : []), [w]);
  const layout = useMemo(() => (w ? studioView(w, all) : null), [w, all]);
  const picks = Object.fromEntries(Object.entries(st.steps).map(([k, v]) => [k, v.picked]));
  const binds = step ? takeBinds(doc, step) : {};
  const base = step && w ? stepRaws(doc, step, w, all, picks) : {};
  // the bound inputs always hold the pick: the user's edits never cover them
  const raws = { ...base, ...(step ? edits[step] : {}), ...pickOnly(base, binds) };
  const shownAll = visible(layout ? layout.shown : all, raws);
  const fields = shownAll.filter((f) => !(f.key in binds));
  const sendFields = [...shownAll, ...all.filter((f) => f.key in binds && !shownAll.includes(f))];
  const takesOf = (id: string) => {
    const ids = new Set(st.steps[id]?.runs ?? []);
    return takes.filter((t) => t.run_id && ids.has(t.run_id));
  };
  const runsOf = (id: string) => {
    const ids = new Set(st.steps[id]?.runs ?? []);
    return runs.filter((r) => ids.has(r.id));
  };
  const from = Object.values(binds)[0];
  const missing = Object.values(binds).filter((s) => !st.steps[s]?.picked);
  const n = step ? nums[step] : 0;
  const seeds = node?.kind === "step" ? (node.seeds ?? 1) : 1;
  const looks = node?.kind === "step" ? (node.looks ?? []) : [];
  const video = w ? mediaOf(w) === "video" : false;
  const built = buildInputs(sendFields, raws);
  const body: S<"RunRequest"> | null =
    w && !missing.length && !Object.keys(built.errors).length
      ? { workflow_id: w.id, inputs: built.inputs, ...(looks.length && { looks }) }
      : null;
  const peak = usePlannedPeak(body);
  const estimate = estimateText(null, peak);

  const change = (key: string, u: Update) => {
    if (!step) return;
    setEdits((e) => {
      const mine = e[step] ?? {};
      const v = typeof u === "function" ? u(mine[key] ?? raws[key]) : u;
      return { ...e, [step]: { ...mine, [key]: v } };
    });
    setErrors((x) => {
      const next = { ...x };
      delete next[key];
      return next;
    });
  };
  const uploads = useRefUploads(change);

  async function run() {
    if (!w || !step || busy) return;
    const q = queueInputs(sendFields, built.inputs, seeds);
    const errs = { ...built.errors, ...q.errors };
    setErrors(errs);
    if (Object.keys(errs).length) return;
    setBusy(true);
    setError("");
    try {
      const sid =
        st.session ??
        (await ok(api.POST("/v1/studio/sessions", { body: { title: doc.title.slice(0, 200) } })))
          .id;
      if (!st.session) update((s) => ({ ...s, session: sid }));
      for (const inputs of q.runs) {
        const r = await ok(
          api.POST("/v1/studio/sessions/{session_id}/runs", {
            params: { path: { session_id: sid } },
            body: { workflow_id: w.id, inputs, ...(looks.length && { looks }) },
          }),
        );
        put(mergeRuns(runsRef.current, [r]));
        update((s) => ({
          ...s,
          session: sid,
          steps: {
            ...s.steps,
            [step]: { ...s.steps[step], runs: [r.id, ...(s.steps[step]?.runs ?? [])] },
          },
        }));
      }
    } catch (e) {
      const on =
        e instanceof ApiError &&
        e.status === 422 &&
        fieldErrors(
          e.message,
          sendFields.map((f) => f.key),
        );
      if (on) setErrors(on);
      else setError(message(e));
    } finally {
      setBusy(false);
    }
  }

  const pick = (src: string, id: string) =>
    update((s) => ({
      ...s,
      steps: { ...s.steps, [src]: { runs: s.steps[src]?.runs ?? [], picked: id } },
    }));

  const status = (id: string) => {
    const rs = runsOf(id);
    const ts = takesOf(id);
    const active = rs.filter((r) => isActive(r.status)).length;
    const failed = rs.filter((r) => r.status === "failed").length;
    const waits = Object.values(takeBinds(doc, id)).filter((s) => !st.steps[s]?.picked);
    if (active) return { done: false, text: `Running · ${rs.length - active} of ${rs.length}` };
    if (!rs.length)
      return {
        done: false,
        text: waits.length
          ? `Waiting for you to pick ${kindWord(takesOf(waits[0]))}`
          : "Not run yet",
      };
    return {
      done: ts.length > 0,
      text: [`Done · ${plural(ts.length, "take")}`, failed && `${failed} failed`]
        .filter(Boolean)
        .join(" · "),
    };
  };

  const upstream = from ? takesOf(from) : [];
  const own = step ? takesOf(step) : [];
  const pinned = pinnedNote(runsOf(step ?? "").find((r) => r.status === "queued"));
  const textFields = (layout ? fields.filter((f) => layout.text.includes(f)) : fields).filter(
    (f) => f.param.type === "text",
  );
  const groups = layout?.groups
    .map((g) => ({
      ...g,
      title: `Step ${n} · ${g.title}`,
      fields: g.fields.filter((f) => fields.includes(f)),
    }))
    .filter((g) => g.fields.length || g.choices?.length);

  return (
    <>
      <main id="main" className="flex min-w-0 flex-[999_1_560px] flex-col gap-3.5 px-5 pt-4 pb-5">
        {head}
        <div aria-label="Pipeline steps" className="flex flex-wrap items-stretch gap-2.5">
          {steps.map((id) => {
            const s = status(id);
            return (
              <button
                key={id}
                type="button"
                aria-current={id === step ? "step" : undefined}
                onClick={() => {
                  setChosen(id);
                  setOpenKey(null);
                  setErrors({});
                }}
                className={`bevel flex flex-[1_1_220px] flex-col gap-1 rounded-xl border px-3.5 py-3 text-left ${id === step ? "border-a2 bg-s2" : "border-s5 bg-s1"}`}
              >
                <span className="flex items-center gap-2 font-semibold">
                  {s.done && <Check size={16} strokeWidth={2.5} className="text-a1" aria-hidden />}
                  {nums[id]} · {nodeLabel(doc, id, titles)}
                </span>
                <span className="text-[12px] text-t2">{s.text}</span>
              </button>
            );
          })}
          <button
            type="button"
            onClick={onAddStep}
            title="Steps are added in the graph"
            className="flex min-h-11 flex-none items-center gap-1.5 rounded-xl border border-dashed border-dash bg-transparent px-4 text-[13px]"
          >
            <Plus size={16} strokeWidth={2} aria-hidden />
            Add step
          </button>
        </div>
        {error && <ErrorNote>{error}</ErrorNote>}
        {!steps.length ? (
          <p className="text-t2">This pipeline has no step yet: add one in the graph.</p>
        ) : !w ? (
          <ErrorNote>
            The workflow {node?.kind === "step" ? node.workflow : ""} is not available any more.
          </ErrorNote>
        ) : (
          <>
            <section
              aria-labelledby="studio-grid"
              className="panel flex min-h-[380px] grow flex-col gap-3.5 p-5"
              style={{ background: "var(--in)" }}
            >
              {from ? (
                <TakeGrid
                  id="studio-grid"
                  heading={`Pick one ${kindWord(upstream, false)} to ${video && upstream[0]?.kind === "image" ? "animate" : "use"}`}
                  sub={`Step ${nums[from]} results · batch of ${plural(stepSeeds(doc, from), "seed")}`}
                  takes={upstream}
                  picked={st.steps[from]?.picked}
                  onPick={(t) => pick(from, t.id)}
                  empty={`Run step ${nums[from]} first: its takes show here.`}
                />
              ) : (
                <TakeGrid
                  id="studio-grid"
                  heading={`Step ${n} results`}
                  sub={`batch of ${plural(seeds, "seed")}`}
                  takes={own}
                  picked={st.steps[step!]?.picked}
                  onPick={(t) => pick(step!, t.id)}
                  onOpen={(i) => setFull({ list: own, i })}
                  empty={`Run step ${n}: its takes show here.`}
                />
              )}
              {from && own.length > 0 && (
                <TakeGrid
                  id="studio-own"
                  heading={`Step ${n} results`}
                  takes={own}
                  picked={st.steps[step!]?.picked}
                  onPick={(t) => pick(step!, t.id)}
                  onOpen={(i) => setFull({ list: own, i })}
                  small
                />
              )}
            </section>
            <form
              aria-label="Composer"
              noValidate
              onSubmit={(e) => {
                e.preventDefault();
                void run();
              }}
              className="panel flex flex-col gap-3 bg-s2 px-4 py-3.5 [box-shadow:0_16px_40px_color-mix(in_srgb,var(--shd)_45%,transparent)]"
            >
              {Object.keys(binds).length > 0 && (
                <div className="flex flex-wrap items-center gap-2">
                  {Object.entries(binds).map(([k, src]) => {
                    const f = all.find((x) => x.key === k);
                    const t = takes.find((x) => x.id === st.steps[src]?.picked);
                    return (
                      <span key={k} className="mono rounded-lg bg-s5 px-2.5 py-1 text-[12px]">
                        {f?.param.label ?? f?.param.name ?? k} ·{" "}
                        {t ? `take ${seedText(t)}` : `pick one from step ${nums[src]}`}
                      </span>
                    );
                  })}
                </div>
              )}
              <TextFields fields={textFields} raws={raws} errors={errors} onChange={change} />
              <PinLine
                hosts={hosts}
                w={w}
                n={n}
                peak={peak}
                queued={pinned}
                onTry={() => void run()}
                busy={busy}
              />
              {fields.some((f) => f.param.type !== "text" && errors[f.key]) && (
                <ErrorNote>Some settings are not valid: see the step&apos;s settings.</ErrorNote>
              )}
              <div className="flex flex-wrap items-center gap-2">
                <Chips
                  fields={fields}
                  raws={raws}
                  onChange={change}
                  show={(key) => {
                    setOpenKey(key === "canvas" ? null : key);
                    requestAnimationFrame(() =>
                      document
                        .querySelector<HTMLElement>(`#${idOf(key)}-ed :is(input, .opt)`)
                        ?.focus(),
                    );
                  }}
                  compact={video}
                />
                <div className="grow" />
                <button
                  type="submit"
                  className="key flex h-14 flex-col items-center justify-center gap-px bg-a2 px-6 text-[var(--on2)]"
                  disabled={busy || missing.length > 0}
                  title={missing.length ? `Pick a take from step ${nums[missing[0]]} first` : ""}
                >
                  <span className="text-sm">{busy ? "Sending…" : `Run step ${n}`}</span>
                  {estimate && !busy && (
                    <span className="mono text-[11px] font-medium">{estimate}</span>
                  )}
                </button>
              </div>
            </form>
          </>
        )}
      </main>
      <aside
        aria-label="Memory and step settings"
        className="aside m-3.5 flex flex-[1_1_320px] flex-col gap-[22px] px-[18px] pt-4 pb-6 md:ml-0"
      >
        {w && <MemoryCard hosts={hosts} env={w.env} />}
        {w && (
          <Inspector
            groups={groups}
            presets={w.studio?.presets}
            presetOrder={presetNames(w.studio)}
            fields={fields}
            raws={raws}
            errors={errors}
            onChange={change}
            open={openKey}
            setOpen={setOpenKey}
            uploads={uploads}
            compact={video}
          />
        )}
      </aside>
      {full && (
        <FullScreen
          take={full.list[full.i]}
          onClose={() => setFull(null)}
          onStep={(d) =>
            setFull((v) => v && { ...v, i: (v.i + d + v.list.length) % v.list.length })
          }
        />
      )}
    </>
  );
}

/** The bound keys of `base` (the picks), so the user's edits of the same keys never win. */
const pickOnly = (base: Record<string, Raw>, binds: Record<string, string>) =>
  Object.fromEntries(Object.keys(binds).flatMap((k) => (k in base ? [[k, base[k]]] : [])));

const stepSeeds = (doc: PDoc, id: string) => {
  const n = doc.nodes[id];
  return n?.kind === "step" ? (n.seeds ?? 1) : 1;
};

/** "an image", "a clip"; bare: "image", "clip". */
function kindWord(ts: Take[], article = true) {
  const k = ts[0]?.kind === "video" ? "clip" : ts[0]?.kind === "audio" ? "sound" : "image";
  return article ? `${k === "image" ? "an" : "a"} ${k}` : k;
}

/** A step's takes as the board's tiles: one is picked (passed on); a double-click opens it full screen. */
function TakeGrid({
  id,
  heading,
  sub,
  takes,
  picked,
  onPick,
  onOpen,
  empty,
  small,
}: {
  id: string;
  heading: string;
  sub?: string;
  takes: Take[];
  picked?: string;
  onPick: (t: Take) => void;
  onOpen?: (i: number) => void;
  empty?: string;
  small?: boolean;
}) {
  return (
    <>
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h2
          id={id}
          className={`m-0 font-display font-bold tracking-[0.01em] ${small ? "text-[16px]" : "text-[24px]"}`}
        >
          {heading}
        </h2>
        {sub && <span className="text-[12px] text-t2">{sub}</span>}
      </div>
      {takes.length ? (
        <div
          role="group"
          aria-label={heading}
          className={`grid gap-3 ${small ? "grid-cols-[repeat(auto-fill,minmax(140px,1fr))]" : "grid-cols-2"}`}
        >
          {takes.map((t, i) => {
            const on = t.id === picked;
            return (
              <button
                key={t.id}
                type="button"
                aria-pressed={on}
                aria-label={`Take, ${seedText(t)}${on ? ", picked" : ""}`}
                onClick={() => onPick(t)}
                onDoubleClick={() => onOpen?.(i)}
                className={`mono relative overflow-hidden rounded-[10px] bg-p0 text-[12px] ${small ? "h-24" : "h-[180px]"} ${on ? "border-2 border-a2 text-t1" : "border border-dashed border-dash text-t2"}`}
              >
                <Thumb
                  take={t}
                  className="absolute inset-0"
                  imgClassName="size-full object-contain"
                />
                <span className="absolute bottom-1.5 left-1.5 max-w-[calc(100%-12px)] truncate rounded bg-s2/90 px-1.5 text-t1">
                  take · {seedText(t)}
                  {on ? " · picked" : ""}
                </span>
              </button>
            );
          })}
        </div>
      ) : (
        empty && <p className="m-0 grow text-[13px] text-t2">{empty}</p>
      )}
    </>
  );
}

/** The host of `env` as the chip counts it and as one Hosts card (lib/hosts); undefined before it reports. */
function hostCard(hosts: S<"Host">[] | null, env: string) {
  const mem = hostMemory(hosts, env);
  return { mem, card: mem ? hostCards(hosts ?? []).find((c) => c.host === mem.host) : undefined };
}

/** "Step 2 runs on MLX. The pinned … will be unloaded first.": the queued run's own word (`unloads_pinned`), else
 * the planned peak against what the host's pins leave; admins may run it anyway (a pin goes only as the last
 * resort and stays set). */
function PinLine({
  hosts,
  w,
  n,
  peak,
  queued,
  onTry,
  busy,
}: {
  hosts: S<"Host">[] | null;
  w: Workflow;
  n: number;
  peak: number | null;
  queued: string;
  onTry: () => void;
  busy: boolean;
}) {
  const admin = !!useUser()?.roles.includes("admin");
  const { mem, card } = hostCard(hosts, w.env);
  const pins = card ? card.entries.filter((e) => isPinned(card, e)) : [];
  const pinGb = pins.reduce((s, e) => s + e.r.gb, 0);
  const others = pins.filter((e) => e.env !== w.env);
  const victims = others.length ? others : pins;
  const env = ENV_NAMES[w.env] ?? w.env;
  const text = queued
    ? `Step ${n} runs on ${env}: the queued run ${queued}.`
    : mem && peak != null && victims.length && peak + pinGb > mem.budget_gb
      ? `Step ${n} runs on ${env}. The pinned ${victims.map((e) => `${e.r.model} ${componentText(e.r.component)}`).join(", ")} (${Math.round(victims.reduce((s, e) => s + e.r.gb, 0))} GB) will be unloaded first.`
      : "";
  if (!text) return null;
  return (
    <div
      role="status"
      className="flex flex-wrap items-center gap-2.5 rounded-[10px] border border-line bg-s1 px-3 py-2.5 text-[13px]"
    >
      <span className="flex-[1_1_280px]">{text}</span>
      {admin && !queued && (
        <button
          type="button"
          className="chip bevel h-9 rounded-[10px] bg-s4"
          disabled={busy}
          title="Runs the step now: a pin is unloaded only when the run does not fit otherwise, and it stays set"
          onClick={onTry}
        >
          Keep it and try anyway
        </button>
      )}
    </div>
  );
}

/** The aside's Memory card: the step's host, its components, what stays after the queue, Unload all now. Admins
 * change them (Admin › Hosts' calls); others see them read-only. */
export function MemoryCard({ hosts, env }: { hosts: S<"Host">[] | null; env: string }) {
  const admin = !!useUser()?.roles.includes("admin");
  const { mem, card } = hostCard(hosts, env);
  // what a PUT answered, until the next poll shows it
  const [settings, setSettings] = useState<S<"HostSettings"> | null>(null);
  const [note, setNote] = useState("");
  const [error, setError] = useState("");
  if (!mem || !card) return null;
  const c: HostCard = settings && settings.host === card.host ? { ...card, settings } : card;
  const after = admin ? settingsOf(c) : null;
  const gb = (x: number) => +x.toFixed(1);
  const act = async (f: () => Promise<unknown>, done = "") => {
    try {
      await f();
      setError("");
      setNote(done);
    } catch (e) {
      setError(message(e));
    }
  };
  const host = { params: { path: { host: c.host } } };
  const pin = (e: HostEntry) =>
    act(async () =>
      setSettings(
        await ok(
          api.PUT("/v1/studio/admin/hosts/{host}/pins", {
            ...host,
            body: { pins: togglePin(c, e) },
          }),
        ),
      ),
    );
  const unload = (body: S<"HostUnload">) =>
    act(
      () => ok(api.POST("/v1/studio/admin/hosts/{host}/unload", { ...host, body })),
      "Asked: a running run finishes first.",
    );
  const afterRun = (a: S<"AfterRun">) =>
    act(async () =>
      setSettings(
        await ok(
          api.PUT("/v1/studio/admin/hosts/{host}/settings", {
            ...host,
            body: { ...settingsOf(c), after_run: a },
          }),
        ),
      ),
    );
  const label = (k: S<"AfterRun">, t: string) =>
    k === "keep_warm" && after ? `Keep warm for ${after.idle_unload_min} minutes` : t;

  return (
    <section
      aria-labelledby="mem-h"
      className="flex flex-col gap-3 rounded-[18px_18px_18px_8px] bg-s2 p-3.5 [box-shadow:0_12px_32px_color-mix(in_srgb,var(--shd)_40%,transparent)]"
    >
      <div className="flex items-center justify-between gap-2">
        <h2 id="mem-h" className="m-0 flex items-center text-[15px] font-semibold">
          Memory
          <Info>
            {c.host}: what its envs hold together.{" "}
            {admin
              ? "The same controls as Admin › Hosts."
              : "Only admins can pin, unload or change what stays after the queue."}
          </Info>
        </h2>
        <span className="mono text-[12px] text-t2">
          {gb(mem.used_gb)} of {gb(mem.budget_gb)} GB
        </span>
      </div>
      <span className="block h-1.5 rounded-[3px] bg-s5">
        <span
          className="block h-1.5 rounded-[3px] bg-a2"
          style={{ width: `${Math.min(100, (mem.used_gb / (mem.budget_gb || 1)) * 100)}%` }}
        />
      </span>
      {c.entries.map((e, i) => {
        const on = isPinned(c, e);
        const name = `${e.r.model} ${componentText(e.r.component)}`;
        const ref = pinRef(e);
        return (
          <div key={i} className="flex flex-col gap-1.5 rounded-[10px] bg-s1 px-3 py-2.5">
            <div className="flex justify-between gap-2">
              <span className="min-w-0 truncate">{name}</span>
              <span className="mono text-[12px] text-t2">{gb(e.r.gb)} GB</span>
            </div>
            <span className="text-[12px] text-t2">
              {e.r.precision} · {e.env}
              {e.r.pinned_why === "in_use" ? " · in use" : ""}
              {!admin && on ? " · pinned" : ""}
            </span>
            {admin && (
              <div className="flex gap-2">
                <button
                  type="button"
                  aria-pressed={on}
                  aria-label={`${on ? "Unpin" : "Pin"} ${name}`}
                  disabled={!ref}
                  onClick={() => void pin(e)}
                  className={`h-9 flex-1 rounded-lg text-[13px] ${on ? "border border-a2 bg-transparent" : "bevel border-0 bg-s4 font-medium"}`}
                >
                  {on ? "Pinned" : "Pin"}
                </button>
                <button
                  type="button"
                  aria-label={`Unload ${name}`}
                  disabled={!ref}
                  onClick={() => ref && void unload({ entries: [ref] })}
                  className="bevel h-9 flex-1 rounded-[10px] border-0 bg-s4 text-[13px] font-medium"
                >
                  Unload
                </button>
              </div>
            )}
          </div>
        );
      })}
      {!c.entries.length && <p className="m-0 text-[13px] text-t2">Nothing loaded.</p>}
      {/* the host's settings are admin-only (Host.admin), so others get a line, not an empty choice */}
      {after ? (
        <fieldset className="m-0 flex flex-col gap-2 border-0 p-0">
          <legend className="pb-2 text-[12px] font-semibold tracking-[0.04em] text-t2">
            When the queue finishes
          </legend>
          {AFTER_RUN.map(([k, t]) => (
            <label key={k} className="flex min-h-8 items-center gap-2.5">
              <input
                type="radio"
                name="after-run"
                className="accent-a2"
                checked={after.after_run === k}
                onChange={() => void afterRun(k)}
              />
              {label(k, t)}
            </label>
          ))}
        </fieldset>
      ) : (
        <p className="m-0 text-[12px] text-t2">What stays after the queue is set by an admin.</p>
      )}
      <button
        type="button"
        disabled={!admin}
        title={admin ? undefined : "Only admins can unload"}
        onClick={() => void unload({ all: true })}
        className="h-10 rounded-[10px] border border-line bg-transparent text-[13px] disabled:opacity-60"
      >
        Unload all now
      </button>
      {note && (
        <p role="status" className="m-0 text-[12px] text-t2">
          {note}
        </p>
      )}
      {error && <ErrorNote>{error}</ErrorNote>}
    </section>
  );
}

/** The top bar's Studio | Graph switch (the boards' `View` group): buttons, or links where a view is another page
 * (the execution view's Studio opens the pipeline's). */
export function ViewSwitch({
  view,
  onView,
  hrefs,
}: {
  view: "studio" | "graph";
  onView?: (v: "studio" | "graph") => void;
  /** a view that is another page; absent and without `onView` the button is disabled */
  hrefs?: Partial<Record<"studio" | "graph", string>>;
}) {
  const cls =
    "grid h-[34px] place-items-center rounded-lg px-4 font-medium text-t2 aria-pressed:bg-s5 aria-pressed:text-t1";
  return (
    <div
      role="group"
      aria-label="View"
      className="flex rounded-[10px] border border-s3 bg-s1 p-[3px]"
    >
      {(["studio", "graph"] as const).map((v) => {
        const name = v === "studio" ? "Studio" : "Graph";
        const href = hrefs?.[v];
        return href && v !== view ? (
          <Link key={v} href={href} className={cls}>
            {name}
          </Link>
        ) : (
          <button
            key={v}
            type="button"
            aria-pressed={view === v}
            disabled={v !== view && !onView}
            title={onView ? `${name} (⌘ or Ctrl + \\)` : undefined}
            onClick={() => onView?.(v)}
            className={cls}
          >
            {name}
          </button>
        );
      })}
    </div>
  );
}
