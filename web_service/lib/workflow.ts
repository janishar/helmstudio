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
  /** the node outputs a Canvas (its params set the output size) */
  canvas: boolean;
  /** the node outputs a Model handle (its params pick the weights) */
  model: boolean;
};

/** What a form control holds: a switch's boolean, or the text typed ("" in a seed field is random). */
export type Raw = string | boolean;

/** Every param of every node in the workflow, nodes in graph order, params in schema order. */
export function fields(w: S<"Workflow">): Field[] {
  return graphOrder(w).flatMap((node) => {
    const n = w.nodes[node];
    const schema = w.node_schemas[n.kind];
    if (!schema) return [];
    return schema.params.map((param) => ({
      key: `${node}.${param.name}`,
      node,
      nodeTitle: schema.title,
      nodeHelp: schema.help,
      param,
      initial: n.params?.[param.name] ?? param.default,
      canvas: schema.outputs.some((o) => o.type === "Canvas"),
      model: schema.outputs.some((o) => o.type === "Model"),
    }));
  });
}

/** An enum value as shown: a plain lowercase word title-cased ("base" → "Base"); ids like "int8" stay as they are. */
export const pretty = (v: string) => (/^[a-z]+$/.test(v) ? v[0].toUpperCase() + v.slice(1) : v);

/** A tile's label and value as shown. The model node's variant is "Model"; a label ending in its node's title
 * ("Tiled decode" on Decode) becomes that title, its first words leading the value ("Tiled · auto"); other enum
 * values are prettified. */
export function display(f: Field, raw: Raw): { label: string; value: string } {
  const label = f.param.label ?? f.param.name;
  const v = String(raw);
  if (f.model && f.param.name === "variant") return { label: "Model", value: pretty(v) };
  const t = f.nodeTitle.toLowerCase();
  const l = label.toLowerCase();
  if (l.endsWith(" " + t) && f.param.type === "enum")
    return { label: f.nodeTitle, value: `${pretty(label.slice(0, -t.length - 1))} · ${v}` };
  return { label, value: f.param.type === "enum" ? pretty(v) : v };
}

type ExprValue = string | number | boolean | null | undefined;

/** Evaluates a `when` / `requires` expression (contracts/README.md): `node.param` references, 'string' / "string",
 * number, true / false / null literals, ==, !=, &&, ||, ! and parentheses. `get` reads a reference. Throws on a
 * syntax error. */
export function evaluate(expr: string, get: (ref: string) => ExprValue): ExprValue {
  const tokens: string[] = [];
  const re =
    /\s*(?:(-?\d+(?:\.\d+)?|'[^']*'|"[^"]*"|==|!=|&&|\|\||[!()]|[A-Za-z_][\w-]*(?:\.[A-Za-z_][\w-]*)?))/y;
  while (re.lastIndex < expr.length) {
    const at = re.lastIndex;
    const m = re.exec(expr);
    if (!m || m[1] === undefined) {
      if (expr.slice(at).trim() === "") break;
      throw new Error(`unexpected "${expr.slice(at).trim()}"`);
    }
    tokens.push(m[1]);
  }
  let i = 0;
  const peek = () => tokens[i];
  const take = (t: string) => (tokens[i] === t ? (i++, true) : false);
  const atom = (): ExprValue => {
    const t = tokens[i++];
    if (t === undefined) throw new Error("unexpected end");
    if (t === "(") {
      const v = or();
      if (!take(")")) throw new Error('missing ")"');
      return v;
    }
    if (/^['"]/.test(t)) return t.slice(1, -1);
    if (/^-?\d/.test(t)) return Number(t);
    if (t === "true" || t === "false") return t === "true";
    if (t === "null") return null;
    if (/^[A-Za-z_][\w-]*\.[A-Za-z_][\w-]*$/.test(t)) return get(t);
    throw new Error(`unexpected "${t}"`);
  };
  const not = (): ExprValue => (take("!") ? !not() : atom());
  const cmp = (): ExprValue => {
    const a = not();
    if (take("==")) return a === not();
    if (take("!=")) return a !== not();
    return a;
  };
  const and = (): ExprValue => {
    let v = cmp();
    while (take("&&")) {
      const r = cmp();
      v = v && r;
    }
    return v;
  };
  const or = (): ExprValue => {
    let v = and();
    while (take("||")) {
      const r = and();
      v = v || r;
    }
    return v;
  };
  const v = or();
  if (peek() !== undefined) throw new Error(`unexpected "${peek()}"`);
  return v;
}

/** The fields whose `when` holds for the values in the controls now (an expression that does not parse shows its
 * field). Hidden fields are neither shown nor sent. */
export function visible(fs: Field[], raws: Record<string, Raw>): Field[] {
  const get = (ref: string) => {
    const f = fs.find((x) => x.key === ref);
    if (!f) return undefined;
    const r = readValue(f.param, raws[f.key] ?? toRaw(f.initial, f.param));
    return "value" in r ? (r.value as ExprValue) : String(raws[f.key]);
  };
  return fs.filter((f) => {
    if (!f.param.when) return true;
    try {
      return !!evaluate(f.param.when, get);
    } catch {
      return true;
    }
  });
}

/** A value as a form control of param p holds it; a random seed (null or -1) is "". */
export const toRaw = (v: Value, p: Param): Raw =>
  typeof v === "boolean" ? v : v == null || (p.type === "seed" && v === -1) ? "" : String(v);

/** The form control's content as a value, or why it is not one (worded like studio_service's 422). */
export function readValue(p: Param, raw: Raw): { value: Value } | { error: string } {
  if (p.type === "bool") return { value: raw === true };
  const text = String(raw);
  if (p.type === "seed") {
    const t = text.trim();
    if (t === "" || t === "-1") return { value: -1 }; // random: the runner picks one and reports it
    const n = Number(t);
    if (!Number.isSafeInteger(n) || n < 0 || (p.max != null && n > p.max))
      return { error: `must be a whole number from 0 to ${p.max ?? Number.MAX_SAFE_INTEGER}` };
    return { value: n };
  }
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
    const r = readValue(f.param, raws[f.key] ?? toRaw(f.initial, f.param));
    if ("error" in r) errors[f.key] = r.error;
    else if (r.value !== f.initial && !(r.value === -1 && f.initial === null))
      inputs[f.key] = r.value;
  }
  return { inputs, errors };
}

/** A node id's title from its schema ("sample" → "Sample"), else the id. */
export function nodeTitle(w: S<"Workflow"> | undefined, node: string) {
  const kind = w?.nodes[node]?.kind;
  return (kind && w?.node_schemas[kind]?.title) || node;
}

/** Node ids in graph order: every node after the nodes its inputs come from, else in declaration order. */
export function graphOrder(w: S<"Workflow">): string[] {
  const out: string[] = [];
  const seen = new Set<string>();
  const visit = (id: string) => {
    if (seen.has(id) || !w.nodes[id]) return;
    seen.add(id);
    for (const src of Object.values(w.nodes[id].in ?? {})) visit(src.split(".")[0]);
    out.push(id);
  };
  Object.keys(w.nodes).forEach(visit);
  return out;
}

const rank = (f: Field) => (f.model ? 0 : f.param.type === "seed" ? 2 : 1);

/** The inspector's sections: the canvas node's params, label-over-value tiles, switches, and the rest. */
export function sections(fs: Field[]) {
  const rest = fs.filter((f) => f.param.type !== "text");
  const other = rest.filter((f) => !f.canvas);
  return {
    canvas: rest.filter((f) => f.canvas),
    // the model node's first, then graph order, the seed last (its home is the composer's chip), as in the mock
    tiles: other
      .filter((f) => ["enum", "int", "float", "seed"].includes(f.param.type))
      .sort((a, b) => rank(a) - rank(b)),
    switches: other.filter((f) => f.param.type === "bool"),
    more: other.filter((f) => f.param.type === "string"),
  };
}

/** The canvas aspect choices, as width / height. */
export const ASPECTS: [string, number][] = [
  ["1:1", 1],
  ["3:2", 3 / 2],
  ["16:9", 16 / 9],
  ["9:16", 9 / 16],
];
/** The canvas size choices, in megapixels (10^6 pixels). */
export const MEGAPIXELS = [1, 2, 4];

/** Width and height for an aspect and a size in megapixels, each on the grid and within [min, max] (scaled down
 * to fit, keeping the aspect). The short side is snapped first and the long one derived from it, so the grid
 * bends the aspect least. */
export function sizeFor(ratio: number, mp: number, grid = 1, min = 0, max = Infinity) {
  const snap = (v: number) => Math.min(max, Math.max(min, Math.round(v / grid) * grid));
  let h = Math.sqrt((mp * 1e6) / ratio);
  h *= Math.min(1, max / h, max / (h * ratio));
  const r = ratio >= 1 ? ratio : 1 / ratio;
  const short = Math.min(h, h * ratio);
  // the short side's grid step below or above, whichever keeps the aspect closer
  const pairs = [Math.floor(short / grid), Math.ceil(short / grid)].map((k) => {
    const s = Math.min(max, Math.max(min, k * grid));
    return [s, snap(s * r)];
  });
  const [s, l] = pairs.reduce((a, b) =>
    Math.abs(b[1] / b[0] - r) < Math.abs(a[1] / a[0] - r) ? b : a,
  );
  return ratio >= 1 ? { width: l, height: s } : { width: s, height: l };
}

/** The aspect choice a size is (within 2 %), else null. */
export const aspectOf = (w: number, h: number) =>
  ASPECTS.find(([, r]) => Math.abs(w / h / r - 1) < 0.02)?.[0] ?? null;

/** The megapixel choice nearest to a size. */
export const megapixelsOf = (w: number, h: number) =>
  MEGAPIXELS.reduce((a, b) => (Math.abs(b - (w * h) / 1e6) < Math.abs(a - (w * h) / 1e6) ? b : a));

/** One run's inputs per queued seed: n consecutive seeds from the one set, or n random ones (-1). Without a seed
 * param, n copies. `errors` (by field key) when the last seed would pass the seed's max: then nothing is queued. */
export function queueInputs(
  fs: Field[],
  inputs: Record<string, Value>,
  n: number,
): { runs: Record<string, Value>[]; errors: Record<string, string> } {
  const seed = fs.find((f) => f.param.type === "seed");
  if (!seed || n === 1) return { runs: Array.from({ length: n }, () => inputs), errors: {} };
  const v = inputs[seed.key] ?? seed.initial;
  const first = typeof v === "number" && v >= 0 ? v : -1;
  const max = seed.param.max;
  if (first >= 0 && max != null && first + n - 1 > max)
    return {
      runs: [],
      errors: { [seed.key]: `must be at most ${max - n + 1} to queue ${n} consecutive seeds` },
    };
  return {
    runs: Array.from({ length: n }, (_, i) => ({
      ...inputs,
      [seed.key]: first < 0 ? -1 : first + i,
    })),
    errors: {},
  };
}
