"use client";
// New shot (plan 15.3, docs/mock/Timeline.dc.html "New shot"): Create's composer docked in the timeline, for one
// slot: at the playhead (a new empty slot), an empty slot (its gap) or a clip to redo ("from this clip"). It starts
// with the sequence's defaults (the cast typed into the prompt as mentions, the looks sent as RunRequest.looks) and
// offers start/end keyframes only when the workflow (or its family's image-to-video template) has them.
import Link from "next/link";
import { Minus, Plus } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { ErrorNote } from "@/components/kit";
import { PromptArea } from "@/components/create/prompt";
import { useHosts } from "@/components/create/memory-chip";
import { RunOnChip } from "@/components/create/run-on";
import { chosenVariants, machines, pinnedMachine } from "@/lib/run-on";
import { useLibrary } from "@/lib/library";
import { api, message, ok, type S } from "@/lib/api";
import { mediaOf, snapFrames, gridOf } from "@/lib/media";
import { clipOf } from "@/lib/video";
import {
  buildInputs,
  fields as fieldsOf,
  pretty,
  queueInputs,
  rawsFrom,
  toRaw,
  type Raw,
} from "@/lib/workflow";
import {
  Grid,
  kindOf,
  lenOf,
  newId,
  shortTime,
  type Clip,
  type Norm,
  type Takes,
} from "@/lib/timeline";

type Workflow = S<"Workflow">;

/** The most runs one Generate shot queues (consecutive seeds), as Create's stepper. */
const MAX_SEEDS = 4;
type Timeline = S<"Timeline">;

/** Where the shot goes: a new slot at the playhead, an empty slot, or a clip redone with its own settings. */
export type ShotSlot = { at: number } | { clip: string; redo?: boolean };

/** The V1 clips before and after the slot (for its start and end keyframes). */
function neighbours(
  doc: Norm,
  facts: Takes,
  slot: ShotSlot,
): { prev?: Clip; next?: Clip; at: number } {
  const v1 = doc.tracks[0].clips;
  const g = new Grid(doc.target);
  if ("clip" in slot) {
    const i = v1.findIndex((c) => c.id === slot.clip);
    return { prev: v1[i - 1], next: v1[i + 1], at: v1[i]?.at ?? 0 };
  }
  // the V1 cut nearest the playhead (a tie: the later), as studio's insert does
  const cuts = [0];
  for (const c of v1) cuts.push(cuts[cuts.length - 1] + lenOf(c, kindOf(c, facts), g));
  const f = g.f(slot.at);
  let i = 0;
  cuts.forEach((c, k) => {
    if (Math.abs(c - f) <= Math.abs(cuts[i] - f)) i = k;
  });
  return { prev: v1[i - 1], next: v1[i], at: g.s(cuts[i]) };
}

/** An image take of a video take's first or last frame: at once, or by a media job polled until it ends. */
async function frameTake(takeId: string, at: "first" | "last"): Promise<string> {
  const r = await ok(
    api.POST("/v1/studio/takes/{take_id}/frame", {
      params: { path: { take_id: takeId } },
      body: { at },
    }),
  );
  if (r.take) return r.take.id;
  let job = r.job!;
  while (job.status === "queued" || job.status === "running") {
    await new Promise((res) => setTimeout(res, 1000));
    job = await ok(
      api.GET("/v1/studio/media-jobs/{job_id}", { params: { path: { job_id: job.id } } }),
    );
  }
  if (job.status !== "succeeded" || !job.take_id)
    throw new Error(job.failure?.detail ?? `Making the ${at} frame ${job.status}.`);
  return job.take_id;
}

/** The element is an open popover (Escape then closes only it). jsdom knows no :popover-open. */
function popOpen(el: Element | null) {
  try {
    return !!el?.matches(":popover-open");
  } catch {
    return false;
  }
}

export function ShotComposer({
  tl,
  doc,
  facts,
  slot,
  workflows,
  onDone,
  onClose,
}: {
  tl: Timeline;
  doc: Norm;
  facts: Takes;
  slot: ShotSlot;
  workflows: Workflow[];
  /** the shot made; `note`: what did not run (more seeds with no session to run them in) */
  onDone: (t: Timeline, note?: string) => void;
  onClose: () => void;
}) {
  const video = useMemo(() => workflows.filter((w) => mediaOf(w) === "video"), [workflows]);
  const g = new Grid(doc.target);
  const { prev, next, at } = neighbours(doc, facts, slot);
  const here = "clip" in slot ? doc.tracks[0].clips.find((c) => c.id === slot.clip) : undefined;
  const fixedLen = here ? g.s(lenOf(here, kindOf(here, facts), g)) : null;
  const usable = (c?: Clip) =>
    c?.take_id && !c.offline && !c.shot && tl.takes[c.take_id]?.kind === "video" ? c : undefined;
  const from = usable(prev);
  const to = usable(next);
  const name = (c: Clip) => tl.takes[c.take_id!]?.title ?? "the clip";

  const [workflowId, setWorkflowId] = useState(
    () => (from || to ? video.find((w) => w.keyframes) : undefined)?.id ?? video[0]?.id ?? "",
  );
  const [raws, setRaws] = useState<Record<string, Raw>>({});
  const [start, setStart] = useState(false);
  const [end, setEnd] = useState(false);
  const [frames, setFrames] = useState<number | null>(null);
  const [count, setCount] = useState(1);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const library = useLibrary();
  // Run on, as Create's (docs/mock/Timeline.dc.html, RunOn.dc.html): "" = Auto
  const hosts = useHosts();
  const [runOnHost, setRunOnHost] = useState("");
  const [modelList, setModelList] = useState<S<"ModelList"> | null>(null);
  useEffect(() => {
    ok(api.GET("/v1/studio/models")).then(setModelList, () => undefined);
  }, []);
  const machineList = useMemo(() => machines(hosts), [hosts]);
  const runOn = pinnedMachine(machineList, runOnHost);
  const runOnOffline = !!runOn && !runOn.live;

  // "from this clip": the clip's own workflow and settings
  const redoTake = "clip" in slot && slot.redo ? here?.take_id : null;
  useEffect(() => {
    if (!redoTake) return;
    let stop = false;
    ok(api.POST("/v1/studio/takes/{take_id}/reuse", { params: { path: { take_id: redoTake } } }))
      .then((r) => {
        const w = workflows.find((x) => x.id === r.workflow_id);
        if (stop) return;
        if (!w) return setError(`The workflow ${r.workflow_id} is not available any more.`);
        setWorkflowId(w.id);
        setRaws(rawsFrom(fieldsOf(w), r.inputs));
      })
      .catch((e) => !stop && setError(message(e)));
    return () => {
      stop = true;
    };
  }, [redoTake, workflows]);

  const workflow = video.find((w) => w.id === workflowId);
  const fields = useMemo(() => (workflow ? fieldsOf(workflow) : []), [workflow]);
  const promptField = fields.find((f) => f.param.type === "text");
  const framesField = fields.find((f) => f.param.type === "frames");
  const seedField = fields.find((f) => f.param.type === "seed");
  // the workflow chip names the model: "LTX-2.5 · Distilled" (its model node's title and variant), else the title
  const variant = fields.find((f) => f.model && f.param.name === "variant");
  const modelText = variant
    ? `${variant.nodeTitle} · ${pretty(String(raws[variant.key] ?? variant.initial))}`
    : (workflow?.title ?? "Workflow");
  // the start / end frame options: this workflow's keyframes, else its family's template that has them
  const kfw = workflow?.keyframes
    ? workflow
    : video.find((w) => w.family === workflow?.family && w.source === "template" && w.keyframes);
  const castText = useMemo(() => {
    const handles = (library ?? [])
      .filter((x) => tl.defaults.cast.includes(x.id))
      .map((x) => `@${x.handle}`);
    return handles.length ? handles.join(" ") + " " : "";
  }, [library, tl.defaults.cast]);
  const prompt = promptField
    ? String(
        raws[promptField.key] ??
          (redoTake ? toRaw(promptField.initial, promptField.param) : castText),
      )
    : "";

  const fps = clipOf(fields, raws)?.fps ?? 24;
  const grid = framesField ? gridOf(framesField.param) : null;
  const want = fixedLen ?? (frames !== null ? frames / fps : 3);
  const nFrames = framesField ? snapFrames(framesField.param, want * fps) : null;
  const choices = grid
    ? Array.from(
        { length: Math.floor((grid.max - grid.min) / grid.step) + 1 },
        (_, i) => grid.min + i * grid.step,
      ).filter((n) => n / fps <= 20)
    : [];

  /** Switches to `w`, keeping the prompt typed so far. */
  function pick(w: Workflow) {
    if (w.id === workflowId) return;
    const p = fieldsOf(w).find((f) => f.param.type === "text");
    setWorkflowId(w.id);
    setRaws(p && promptField ? { [p.key]: prompt } : {});
  }
  const toggle = (which: "start" | "end") => {
    if (kfw && kfw.id !== workflowId) pick(kfw);
    (which === "start" ? setStart : setEnd)((v) => !v);
  };

  async function generate() {
    if (!workflow || runOnOffline) return;
    setBusy(true);
    setError("");
    try {
      const r: Record<string, Raw> = { ...raws };
      if (promptField) r[promptField.key] = prompt;
      if (framesField && nFrames) r[framesField.key] = String(nFrames);
      const kf = workflow.keyframes;
      if (kf && (start || end)) {
        const rows: S<"RowItem">[] = [];
        if (start && from)
          rows.push({
            [kf.image]: { take_id: await frameTake(from.take_id!, "last") },
            [kf.seconds]: 0,
          });
        if (end && to && kf.last && nFrames)
          rows.push({
            [kf.image]: { take_id: await frameTake(to.take_id!, "first") },
            // the clip's last frame (Video: Keyframes: s = F / fps, F the last frame's index)
            [kf.seconds]: (nFrames - 1) / fps,
          });
        r[`${kf.param}`] = rows;
      }
      const built = buildInputs(fields, r);
      const queued = queueInputs(fields, built.inputs, seedField ? count : 1);
      const bad = Object.entries({ ...built.errors, ...queued.errors })[0];
      if (bad) throw new Error(`${bad[0]}: ${bad[1]}`);
      const looks = {
        ...(tl.defaults.looks.length && { looks: tl.defaults.looks }),
        ...(runOn && { host: runOn.host }),
      };
      const shotSlot: S<"TimelineSlot"> =
        "clip" in slot
          ? { clip: slot.clip }
          : { at: slot.at, length: (nFrames ?? 3 * fps) / fps, id: newId(doc) };
      const res = await ok(
        api.POST("/v1/studio/timelines/{timeline_id}/shots", {
          params: { path: { timeline_id: tl.id } },
          body: {
            slot: shotSlot,
            run: { workflow_id: workflow.id, inputs: queued.runs[0], ...looks },
          },
        }),
      );
      // the other seeds run in the sequence's session: their takes land in Add clips and the gallery
      const sid = res.timeline.session_id;
      for (const inputs of sid ? queued.runs.slice(1) : [])
        await ok(
          api.POST("/v1/studio/sessions/{session_id}/runs", {
            params: { path: { session_id: sid! } },
            body: { workflow_id: workflow.id, inputs, ...looks },
          }),
        );
      onDone(
        res.timeline,
        !sid && queued.runs.length > 1
          ? "Only the first seed ran: this sequence has no session."
          : undefined,
      );
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  }

  const where =
    "clip" in slot
      ? slot.redo
        ? `redoes this clip · ${fixedLen?.toFixed(1)} s`
        : `fills the ${fixedLen?.toFixed(1)} s gap`
      : "a new slot at the playhead";
  const uses = [tl.defaults.cast.length && "cast", tl.defaults.looks.length && "look"].filter(
    Boolean,
  );
  return (
    <section
      aria-label="New shot"
      // the board draws no Close: Escape (or New shot again) leaves the composer
      onKeyDown={(e) => {
        if (e.key === "Escape" && !popOpen(e.currentTarget.querySelector("[popover]"))) onClose();
      }}
      className="flex flex-col gap-2.5 rounded-[28px_28px_28px_12px] border border-s5 bg-s2 px-3.5 py-3 shadow-[0_16px_40px_color-mix(in_srgb,var(--shd)_50%,transparent)]"
    >
      <div className="flex flex-wrap items-center gap-2">
        <h2 className="m-0 font-display text-sm font-bold tracking-[0.04em]">New shot</h2>
        <span className="mono text-[12px] text-t2">
          at {shortTime(at)} · {where}
          {uses.length ? ` · uses this sequence's ${uses.join(" and ")}` : ""}
        </span>
        <div className="grow" />
        {kfw && from && (
          <button
            type="button"
            aria-pressed={start}
            onClick={() => toggle("start")}
            className="chip bevel rounded-[10px] aria-pressed:outline-2 aria-pressed:outline-a1"
          >
            Start from last frame of {name(from)}
          </button>
        )}
        {kfw?.keyframes?.last && to && (
          <button
            type="button"
            aria-pressed={end}
            onClick={() => toggle("end")}
            className="chip bevel rounded-[10px] aria-pressed:outline-2 aria-pressed:outline-a1"
          >
            End on first frame of {name(to)}
          </button>
        )}
      </div>
      {promptField && (
        <>
          <label htmlFor="shot" className="sr-only">
            Shot prompt
          </label>
          <PromptArea
            id="shot"
            rows={1}
            value={prompt}
            onChange={(v) => setRaws((r) => ({ ...r, [promptField.key]: v }))}
            placeholder="What happens in this shot"
            className="w-full resize-none border-0 bg-transparent text-[15px] leading-[22px] text-t1 outline-0"
            onKeyDown={(e) => {
              if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) void generate();
            }}
          />
        </>
      )}
      <div className="flex flex-wrap items-center gap-2">
        <button
          type="button"
          popoverTarget="shot-workflow"
          className="chip bevel rounded-[10px] text-t1"
          title={workflow?.title}
          style={{ anchorName: "--shot-workflow" } as React.CSSProperties}
        >
          {modelText}
        </button>
        <div
          id="shot-workflow"
          popover="auto"
          role="dialog"
          aria-label="Workflow"
          className="pop w-64"
          style={
            {
              positionAnchor: "--shot-workflow",
              positionArea: "top span-right",
              marginBottom: 6,
            } as React.CSSProperties
          }
        >
          <div className="flex flex-col gap-1.5">
            {video.map((w) => (
              <button
                key={w.id}
                type="button"
                className="opt bevel px-3 text-left text-[13px]"
                aria-pressed={w.id === workflowId}
                onClick={(e) => {
                  pick(w);
                  e.currentTarget.closest<HTMLElement>("[popover]")?.hidePopover?.();
                }}
              >
                {w.title}
              </button>
            ))}
          </div>
        </div>
        <RunOnChip
          id="shot-run-on"
          list={machineList}
          chosen={runOn}
          env={workflow?.env}
          picks={chosenVariants(fields, raws, modelList)}
          onPick={setRunOnHost}
        />
        {framesField && nFrames && (
          <>
            <label htmlFor="shot-length" className="sr-only">
              Length
            </label>
            <select
              id="shot-length"
              className="chip bevel appearance-none rounded-[10px] text-t1"
              disabled={fixedLen !== null}
              value={nFrames}
              onChange={(e) => setFrames(Number(e.target.value))}
            >
              {(choices.includes(nFrames) ? choices : [nFrames, ...choices]).map((n) => (
                <option key={n} value={n}>
                  {(n / fps).toFixed(1)} s · {n} frames
                </option>
              ))}
            </select>
          </>
        )}
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
              {count} / {MAX_SEEDS}
            </span>
            <button
              type="button"
              aria-label="More seeds"
              disabled={count >= MAX_SEEDS}
              onClick={() => setCount((n) => n + 1)}
              className="grid size-9 place-items-center rounded-[10px] disabled:text-t2 disabled:opacity-60"
            >
              <Plus size={14} aria-hidden />
            </button>
          </div>
        )}
        {tl.session_id && (
          <Link
            href={`/?s=${encodeURIComponent(tl.session_id)}`}
            className="chip bevel rounded-[10px]"
          >
            Open in Create
          </Link>
        )}
        <div className="grow" />
        <button
          type="button"
          className="key h-12 rounded-xl px-[22px] text-sm"
          disabled={busy || !workflow || runOnOffline}
          onClick={() => void generate()}
        >
          {busy ? (
            "Starting…"
          ) : runOnOffline ? (
            <span className="flex flex-col items-center gap-px">
              Generate shot
              <span className="mono text-[11px] font-medium">{runOn.label} is offline</span>
            </span>
          ) : (
            "Generate shot"
          )}
        </button>
      </div>
      {!video.length && <p className="m-0 text-[13px] text-t2">No video workflow is installed.</p>}
      {error && <ErrorNote>{error}</ErrorNote>}
    </section>
  );
}
