// A pipeline's Studio view (docs/mock/Pipeline.dc.html; owner's Q2): its steps one after another as ordinary runs in
// a session of its own. The take picked from a step's results goes into the next step's input its bind names
// (`<step>.takes`, `<gate>.picked`, `<judge>.picked`); a `$input` bind stays a field the user fills. What was run and
// picked is remembered in this browser (no API change: runs and takes stay ordinary).
import { stored } from "@/lib/hydrated";
import type { S } from "@/lib/api";
import { imageField, order, type PDoc } from "@/lib/pipeline";
import { rawsFrom, type Field, type Raw } from "@/lib/workflow";

type Value = S<"RunInputs">[string];

/** What the Studio view remembers per pipeline: its session, and per step the runs it made and the take picked from
 * its results. */
export type StudioState = {
  session: string | null;
  steps: Record<string, { runs: string[]; picked?: string }>;
};

const EMPTY: StudioState = { session: null, steps: {} };
const keyOf = (pipelineId: string) => `pipeline-studio:${pipelineId}`;

// ponytail: per browser (localStorage); another browser starts a fresh session. A server-side record when that matters.
export function loadStudio(pipelineId: string): StudioState {
  const v = stored.json<StudioState>(keyOf(pipelineId));
  return v && typeof v === "object" && v.steps ? v : EMPTY;
}

export const saveStudio = (pipelineId: string, s: StudioState) =>
  stored.set(keyOf(pipelineId), JSON.stringify(s));

/** The step ids in row order. */
export const studioSteps = (doc: PDoc) => order(doc).filter((id) => doc.nodes[id].kind === "step");

/** The step whose takes a source reads: `<step>.takes` → that step; `<gate|judge>.picked` → the step it picks from;
 * anything else (an input, a text, an item, a frame) → null. */
export function sourceStep(doc: PDoc, src: string): string | null {
  const [id, what] = src.split(".");
  const n = doc.nodes[id];
  if (!n || !what) return null;
  if (n.kind === "step" && what === "takes") return id;
  if ((n.kind === "gate" || n.kind === "judge") && what === "picked")
    return doc.nodes[n.takes]?.kind === "step" ? n.takes : null;
  return null;
}

/** A step's binds that take a picked take: param key → the step it comes from. */
export function takeBinds(doc: PDoc, step: string): Record<string, string> {
  const n = doc.nodes[step];
  if (n?.kind !== "step") return {};
  return Object.fromEntries(
    Object.entries(n.bind ?? {}).flatMap(([k, src]) => {
      const from = sourceStep(doc, src);
      return from && from !== step && !k.includes("[") ? [[k, from]] : [];
    }),
  );
}

/** The value a param takes for one picked take: a rows param its Image field's row, else the reference. */
export function takeValue(f: Field, takeId: string): Value {
  const ref = { take_id: takeId };
  if (f.param.type !== "rows") return ref;
  return [{ [imageField(f.param) ?? "image"]: ref }] as Value;
}

/** A step's form before the user's edits: the preset, then its fixed inputs, then the picked takes of its binds. */
export function stepRaws(
  doc: PDoc,
  step: string,
  w: Pick<S<"Workflow">, "studio">,
  fields: Field[],
  picks: Record<string, string | undefined>,
): Record<string, Raw> {
  const n = doc.nodes[step];
  if (n?.kind !== "step") return {};
  const preset = (n.preset && w.studio?.presets?.[n.preset]) || {};
  const bound: Record<string, Value> = {};
  for (const [k, from] of Object.entries(takeBinds(doc, step))) {
    const f = fields.find((x) => x.key === k);
    const t = picks[from];
    if (f && t) bound[k] = takeValue(f, t);
  }
  return rawsFrom(fields, { ...preset, ...(n.inputs ?? {}), ...bound });
}

/** The step to open: the first whose results nothing was picked from yet while a later step needs a pick, else
 * the first without runs, else the last. */
export function currentStep(doc: PDoc, s: StudioState): string | undefined {
  const steps = studioSteps(doc);
  for (const id of steps) {
    const binds = Object.values(takeBinds(doc, id));
    if (binds.some((from) => !s.steps[from]?.picked)) return id;
    if (!s.steps[id]?.runs.length) return id;
  }
  return steps.at(-1);
}
