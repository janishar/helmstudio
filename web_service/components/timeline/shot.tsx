"use client";
// New shot (plan 15.3, docs/mock/Timeline.dc.html "New shot"): Create's composer docked in the timeline, for one
// slot: at the playhead (a new empty slot), an empty slot (its gap) or a clip to redo ("from this clip"). It starts
// with the sequence's defaults (the cast typed into the prompt as mentions, the looks sent as RunRequest.looks) and
// offers start/end keyframes only when the workflow (or its family's image-to-video template) has them.
import Link from "next/link";
import { Minus, Plus, TriangleAlert, X } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { ErrorNote } from "@/components/kit";
import { PromptArea } from "@/components/create/prompt";
import { frameTake } from "@/components/create/frame-in";
import { useHosts } from "@/components/create/memory-chip";
import { RunOnChip } from "@/components/create/run-on";
import { chosenVariants, machines, pinnedMachine, runnerWhy } from "@/lib/run-on";
import { DOWN_KEY, useHealth } from "@/lib/health";
import { useLibrary } from "@/lib/library";
import { api, message, ok, type S } from "@/lib/api";
import { mediaOf, snapFrames, gridOf } from "@/lib/media";
import { clipOf } from "@/lib/video";
import {
  buildInputs,
  fields as fieldsOf,
  prettyModel,
  queueInputs,
  rawsFrom,
  toRaw,
  type Raw,
} from "@/lib/workflow";
import type { FormSlot, Started, Want } from "@/components/timeline/assistant";
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
  ask,
  onForm,
}: {
  tl: Timeline;
  doc: Norm;
  facts: Takes;
  slot: ShotSlot;
  workflows: Workflow[];
  /** what the assistant prefilled (its prompt, its length) */
  ask?: Want;
  /** the form for the assistant's card: its Generate is this one's */
  onForm?: (f: FormSlot) => void;
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
        const p = fieldsOf(w).find((f) => f.param.type === "text");
        setRaws({
          ...rawsFrom(fieldsOf(w), r.inputs),
          ...(p && ask?.prompt && { [p.key]: ask.prompt }),
        });
      })
      .catch((e) => !stop && setError(message(e)));
    return () => {
      stop = true;
    };
    // the assistant's prompt is read once, as the composer opens
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [redoTake, workflows]);

  const workflow = video.find((w) => w.id === workflowId);
  // docs/mock/ErrorStates.dc.html b3: no runner for its env (or its Run on host offline) greys Generate, with the
  // line under it; the studio down greys it too (the banner says why)
  const down = useHealth().down;
  const why = down ? null : runnerWhy(hosts, workflow?.env, runOn);
  const fields = useMemo(() => (workflow ? fieldsOf(workflow) : []), [workflow]);
  const promptField = fields.find((f) => f.param.type === "text");
  const framesField = fields.find((f) => f.param.type === "frames");
  const seedField = fields.find((f) => f.param.type === "seed");
  // the workflow chip names the model's choices: "Distilled · int8" (its tooltip the workflow), else the title
  const model = fields.filter((f) => f.model && f.param.type === "enum");
  const modelText = model.length
    ? prettyModel(model.map((f) => String(raws[f.key] ?? f.initial))).join(" · ")
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
          ask?.prompt ??
          (redoTake ? toRaw(promptField.initial, promptField.param) : castText),
      )
    : "";

  const fps = clipOf(fields, raws)?.fps ?? 24;
  const grid = framesField ? gridOf(framesField.param) : null;
  const want = fixedLen ?? (frames !== null ? frames / fps : (ask?.seconds ?? 3));
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

  async function generate(): Promise<Started> {
    if (!workflow) return { error: "no video workflow is installed" };
    if (runOnOffline) return { error: `${runOn.label} is offline` };
    if (why) return { error: why.key };
    if (down) return { error: DOWN_KEY };
    setBusy(true);
    setError("");
    try {
      const r: Record<string, Raw> = { ...raws };
      if (promptField) r[promptField.key] = prompt;
      if (framesField && nFrames) r[framesField.key] = String(nFrames);
      const kf = workflow.keyframes;
      if (kf && "first" in kf) {
        // API 1.37: frames as image params (MiniMax H3 Image to video's First and Last frame)
        if (start && from) r[kf.first] = await frameTake(from.take_id!, "last");
        if (end && to && kf.last) r[kf.last] = await frameTake(to.take_id!, "first");
      } else if (kf && (start || end)) {
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
      const made = "clip" in slot ? slot.clip : newId(doc);
      const shotSlot: S<"TimelineSlot"> =
        "clip" in slot
          ? { clip: slot.clip }
          : { at: slot.at, length: (nFrames ?? 3 * fps) / fps, id: made };
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
      return { timeline: res.timeline, clip: made };
    } catch (e) {
      setError(message(e));
      return { error: message(e) };
    } finally {
      setBusy(false);
    }
  }

  // the form as the assistant's card reads it (plan 18 §22.6 Generate); its Generate is this one
  useEffect(() => {
    if (!onForm) return;
    if (!workflow) {
      onForm("none");
      return () => onForm(null);
    }
    const r: Record<string, Raw> = { ...raws };
    if (promptField) r[promptField.key] = prompt;
    if (framesField && nFrames) r[framesField.key] = String(nFrames);
    const built = workflow && buildInputs(fields, r);
    onForm({
      model: modelText,
      size: `${doc.target.width} × ${doc.target.height} · ${fps} fps`,
      detail: `at ${shortTime(at)} · ${((nFrames ?? want * fps) / fps).toFixed(1)} s · ${count} seed${count === 1 ? "" : "s"}`,
      prompt,
      run:
        workflow && built
          ? { workflow_id: workflow.id, inputs: built.inputs, ...(runOn && { host: runOn.host }) }
          : null,
      generate,
    });
    return () => onForm(null);
  });

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
      // × (owner, 2026-11-26: "new shot is missing close button"), Escape or New shot again leaves the composer
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
        <button
          type="button"
          aria-label="Close New shot"
          title="Close (Esc)"
          onClick={onClose}
          className="grid size-7 shrink-0 place-items-center rounded-lg hover:bg-s3"
        >
          <X size={16} aria-hidden />
        </button>
      </div>
      {promptField && (
        <>
          <label htmlFor="shot" className="sr-only">
            Shot prompt
          </label>
          <PromptArea
            id="shot"
            expand="Shot prompt"
            rows={1}
            value={prompt}
            onChange={(v) => setRaws((r) => ({ ...r, [promptField.key]: v }))}
            placeholder="What happens in this shot"
            className={`w-full resize-none border-0 bg-transparent text-[15px] leading-[22px] text-t1 ${ask?.prompt && prompt === ask.prompt ? "rounded-md outline-2 outline-offset-2 outline-a2" : "outline-0"}`}
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
          why={runOnOffline ? undefined : why?.line}
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
        {/* bottom right even when the row wraps */}
        <button
          type="button"
          className="key ml-auto h-12 rounded-xl px-[22px] text-sm"
          disabled={busy || !workflow || runOnOffline || !!why || down}
          onClick={() => void generate()}
        >
          {busy ? (
            "Starting…"
          ) : why || down ? (
            <span className="flex flex-col items-center gap-px">
              Generate shot
              <span className="mono text-[11px] font-medium">{why?.key ?? DOWN_KEY}</span>
            </span>
          ) : (
            "Generate shot"
          )}
        </button>
      </div>
      {why && (
        <p className="why" role="status">
          <TriangleAlert size={14} aria-hidden />
          {why.line}
        </p>
      )}
      {!video.length && <p className="m-0 text-[13px] text-t2">No video workflow is installed.</p>}
      {error && <ErrorNote>{error}</ErrorNote>}
    </section>
  );
}
