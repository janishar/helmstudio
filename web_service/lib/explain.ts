// Explain run and Compare (plan 05-ui.md 8.2; owner's Q23): what made a take, read from what the API already returns
// (the take's made_with, the run as requested, the workflow's schemas, the library), and which takes a seed grid shows.
import type { S } from "@/lib/api";
import { isLoraRef, loraFieldOf, strengthOf } from "@/lib/loras";
import {
  display,
  fields as fieldsOf,
  isLoraRows,
  isRef,
  isSpan,
  rawsFrom,
  same,
  toRaw,
  visible,
} from "@/lib/workflow";

type Value = S<"ParamValue">;
type Take = S<"Take">;
type Run = S<"Run">;
type Item = S<"LibraryItem">;

/** Where a setting's value came from. */
export type Origin = "default" | "you" | "preset" | "random" | "model";

export type Setting = { key: string; label: string; value: string; origin: Origin; help?: string };

export type Explained = {
  /** "qwen-image-2.1 · base · int8"; "" without a model node */
  model: string;
  host: string;
  seed: number | null;
  /** the preset whose every value the run has, else "" */
  preset: string;
  /** settings by node, graph order, the prompt and the LoRA stacks left out (their own sections) */
  nodes: { title: string; settings: Setting[] }[];
  /** the prompt as typed, and as the model read it once elements and looks were expanded */
  prompt: { typed: string; sent: string } | null;
  elements: { handle: string; name: string; version: number; text: string; changed: boolean }[];
  looks: { name: string; version: number; text: string; loras: string[]; changed: boolean }[];
  loras: { name: string; strength: number; mode: string; from: string }[];
  command: string;
};

/** A value as a short readout: references count, a span as start-end. */
function shown(v: Value): string {
  if (Array.isArray(v))
    return v.length ? `${v.length} ${v.length === 1 ? "item" : "items"}` : "none";
  if (isSpan(v)) return `${v.start}–${v.end} s`;
  if (isRef(v)) return "1 take";
  return v == null ? "none" : String(v);
}

/** Single quotes when a value would not survive a shell as is. */
const quote = (s: string) => (/^[\w.,:/@=+-]+$/.test(s) ? s : `'${s.replaceAll("'", `'\\''`)}'`);

/** One -p value as helm run reads it back (cli/takes.go runLine): take ids, take:in:out, start:end, a scalar. */
function flag(v: Value): string {
  if (Array.isArray(v))
    return v
      .flatMap((r) => Object.values(r).flatMap((x) => (isRef(x) ? [x.take_id] : [])))
      .join(",");
  if (isSpan(v)) return `${v.start}:${v.end}`;
  if (isRef(v)) return "in" in v && v.in != null ? `${v.take_id}:${v.in}:${v.out}` : v.take_id;
  return quote(String(v));
}

/** The helm run command that makes the run again: what it asked (Run.inputs: only what differs from the workflow),
 * with the seed it was made with, its looks, and the LoRA stack as --lora. */
export function helmRun(
  w: S<"Workflow">,
  inputs: Record<string, Value>,
  seed: number | null,
  looks: string[],
) {
  const all = fieldsOf(w);
  const parts = [`helm run ${w.id}`];
  const seedKey = all.find((f) => f.param.type === "seed")?.key;
  const asked = { ...inputs, ...(seedKey && seed != null && { [seedKey]: seed }) };
  const loras: string[] = [];
  for (const key of Object.keys(asked).sort()) {
    const f = all.find((x) => x.key === key);
    const v = asked[key];
    if (f && isLoraRows(f) && Array.isArray(v)) {
      const lk = loraFieldOf(f.param) ?? "lora";
      const sk = strengthOf(f.param)?.[0] ?? "strength";
      for (const r of v) if (isLoraRef(r[lk])) loras.push(`--lora ${r[lk].lora_id}:${r[sk] ?? 1}`);
    } else parts.push(`-p ${key}=${flag(v)}`);
  }
  return [...parts, ...looks.map((id) => `--look ${id}`), ...loras].join(" ");
}

/** What the drawer shows for the take (its made_with) or, without one, the run (as requested, defaults filled). */
export function explain(
  w: S<"Workflow">,
  take: Take | undefined,
  run: Run | undefined,
  library: Item[] | null,
  lookItems: Item[] | null,
): Explained {
  const m = take?.made_with ?? null;
  const asked = run?.inputs ?? {};
  const all = fieldsOf(w);
  const values: Record<string, Value> = {
    ...Object.fromEntries(all.map((f) => [f.key, f.initial])),
    ...asked,
    ...Object.fromEntries(
      Object.entries(run?.resolved?.params ?? {}).map(([k, v]) => [k, v as Value]),
    ),
    ...m?.inputs,
  };
  const typed = m?.typed ?? {};
  const seed = m?.seed ?? run?.resolved?.seed ?? run?.outputs[0]?.seed ?? null;
  const presets = w.studio?.presets ?? {};
  const preset =
    (w.studio?.preset_order ?? Object.keys(presets)).find((name) =>
      Object.entries(presets[name] ?? {}).every(([k, v]) => same(values[k], v)),
    ) ?? "";
  const fromPreset = new Set(preset ? Object.keys(presets[preset] ?? {}) : []);
  const origin = (key: string, initial: Value, type: string): Origin => {
    if (typed[key] === "auto") return "model";
    if (type === "seed" && (asked[key] == null || asked[key] === -1)) return "random";
    if (fromPreset.has(key)) return "preset";
    return key in asked && !same(asked[key], initial) ? "you" : "default";
  };
  const shownFields = visible(all, rawsFrom(all, values)).filter(
    (f) => f.param.type !== "text" && !isLoraRows(f),
  );
  const nodes: Explained["nodes"] = [];
  for (const f of shownFields) {
    const v = f.param.type === "seed" ? seed : values[f.key];
    const { label, value } =
      v != null && typeof v !== "object"
        ? display(f, toRaw(v, f.param))
        : { label: f.param.label ?? f.param.name, value: shown(v) };
    const s: Setting = {
      key: f.key,
      label,
      value,
      origin: origin(f.key, f.initial, f.param.type),
      ...(f.param.help && { help: f.param.help }),
    };
    const last = nodes.at(-1);
    if (last?.title === f.nodeTitle) last.settings.push(s);
    else nodes.push({ title: f.nodeTitle, settings: [s] });
  }
  const promptKey = all.find((f) => f.param.type === "text")?.key;
  const sent = promptKey ? String(values[promptKey] ?? "") : "";
  const lookList = m?.looks ?? [];
  const lookOf = (id: string) => lookItems?.find((x) => x.id === id);
  const lookLoras = new Map<string, string>();
  for (const l of lookList)
    for (const x of lookOf(l.id)?.loras ?? [])
      if (x.lora?.lora_id) lookLoras.set(x.lora.lora_id, l.name);
  return {
    model: m?.model
      ? [m.model.id, m.model.variant, m.model.precision].filter(Boolean).join(" · ")
      : "",
    host: m?.host ?? run?.host ?? "",
    seed,
    preset,
    nodes,
    prompt: promptKey
      ? { typed: String(typed[promptKey] ?? m?.prompt ?? asked[promptKey] ?? sent), sent }
      : null,
    elements: (m?.elements ?? []).map((e) => {
      const now = library?.find((x) => x.id === e.id);
      return { ...e, text: now?.text ?? "", changed: !!now && now.version !== e.version };
    }),
    looks: lookList.map((l) => {
      const now = lookOf(l.id);
      return {
        name: l.name,
        version: l.version,
        text: now?.text ?? "",
        loras: (now?.loras ?? []).map((x) => x.name),
        changed: !!now && now.version !== l.version,
      };
    }),
    loras: (m?.loras ?? []).map((l) => ({
      name: l.name,
      strength: l.strength,
      mode: l.mode,
      from: lookLoras.get(l.id) ?? "",
    })),
    command: helmRun(w, asked, seed, run?.looks ?? lookList.map((l) => l.id)),
  };
}

/** The seed grid: the takes made with the same workflow and prompt as `take` (what differs is the seed), newest
 * first; just `take` when it has no siblings. */
export function seedGroup(takes: Take[], take: Take): Take[] {
  const m = take.made_with;
  if (!m) return [take];
  return takes.filter(
    (t) =>
      t.kind === take.kind &&
      t.made_with?.workflow_id === m.workflow_id &&
      t.made_with.prompt === m.prompt,
  );
}
