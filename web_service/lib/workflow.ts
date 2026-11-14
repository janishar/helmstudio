// The run form, derived from a workflow's node schemas (contracts/openapi.yaml `Workflow`).
import type { S } from "@/lib/api";

type Param = S<"ParamSchema">;
type Value = S<"ParamValue">;

/** One form field: `key` is `<node id>.<param>` as RunRequest.inputs takes it; `initial` is the workflow's value,
 * else the schema default. */
export type Field = {
  key: string;
  node: string;
  nodeTitle: string;
  nodeHelp?: string;
  param: Param;
  initial: Value;
};

/** What a form control holds: a switch's boolean, or the text typed. */
export type Raw = string | boolean;

/** Every param of every node in the workflow, nodes in graph order, params in schema order. */
export function fields(w: S<"Workflow">): Field[] {
  return Object.entries(w.nodes).flatMap(([node, n]) => {
    const schema = w.node_schemas[n.kind];
    if (!schema) return [];
    return schema.params.map((param) => ({
      key: `${node}.${param.name}`,
      node,
      nodeTitle: schema.title,
      nodeHelp: schema.help,
      param,
      initial: n.params?.[param.name] ?? param.default,
    }));
  });
}

/** A value as a form control holds it. */
export const toRaw = (v: Value): Raw => (typeof v === "boolean" ? v : String(v));

/** The form control's content as a value, or why it is not one (worded like studio_service's 422). */
export function readValue(p: Param, raw: Raw): { value: Value } | { error: string } {
  if (p.type === "bool") return { value: raw === true };
  const text = String(raw);
  if (p.type === "int" || p.type === "float") {
    const t = text.trim();
    const n = Number(t);
    if (t === "" || !Number.isFinite(n)) return { error: "must be a number" };
    if (p.type === "int" && !Number.isInteger(n)) return { error: "must be a whole number" };
    if ((p.min != null && n < p.min) || (p.max != null && n > p.max))
      return { error: bounds(p.min, p.max, "") };
    return { value: n };
  }
  if (p.type === "enum")
    return p.enum?.includes(text) ? { value: text } : { error: "pick one of the options" };
  if ((p.min != null && text.length < p.min) || (p.max != null && text.length > p.max))
    return { error: bounds(p.min, p.max, " characters") };
  return { value: text };
}

/** "must be between 1 and 200", "must be at least 1", "must be at most 4000 characters". */
function bounds(min: number | undefined, max: number | undefined, unit: string): string {
  if (min != null && max != null) return `must be between ${min} and ${max}${unit}`;
  return min != null ? `must be at least ${min}${unit}` : `must be at most ${max}${unit}`;
}

/** The run's inputs (only the values that differ from the workflow's) and the errors by field key. */
export function buildInputs(fs: Field[], raws: Record<string, Raw>) {
  const inputs: Record<string, Value> = {};
  const errors: Record<string, string> = {};
  for (const f of fs) {
    const r = readValue(f.param, raws[f.key] ?? toRaw(f.initial));
    if ("error" in r) errors[f.key] = r.error;
    else if (r.value !== f.initial) inputs[f.key] = r.value;
  }
  return { inputs, errors };
}
