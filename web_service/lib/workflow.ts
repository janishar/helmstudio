// The run form, derived from a workflow's node schemas (contracts/openapi.yaml `Workflow`).
import type { S } from "@/lib/api";
import { frameError } from "@/lib/media";
import { languageName, narrowed } from "@/lib/voices";
import { allowedInts, clipOf, positionErrors, positionOf } from "@/lib/video";

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
  /** the node's kind */
  kind?: string;
  /** the node kind's family (a LoRA must fit it), null for a core kind */
  family?: string | null;
  /** the node's params as the workflow has them (defaults filled): what `values_by` and a frame rate read */
  siblings?: Record<string, Value>;
  /** an `optional` image node's param (API 1.22): may stay empty (null), studio then drops the node */
  optional?: boolean;
};

/** What a form control holds: a switch's boolean, the text typed ("" in a seed field is random), an image param's
 * take id ("" for none), a rows param's items, or a media, span or voice param's value ("" for none). */
export type Raw =
  string | boolean | S<"RowItem">[] | S<"MediaRef"> | S<"SpanValue"> | S<"VoiceRef">;
/** A new value for a control, or how to make it from what it holds (undefined: the field's initial value). Uploads
 * and picks finish late, so they add to the list as it is then. */
export type Update = Raw | ((old: Raw | undefined) => Raw);

/** Every param of every node in the workflow, nodes in graph order, params in schema order; a param fed by a wire
 * is not settable (contracts/README.md, User workflows) and left out. */
export function fields(w: Pick<S<"Workflow">, "nodes" | "node_schemas">): Field[] {
  const kinds = Object.values(w.nodes).map((n) => n.kind);
  return graphOrder(w).flatMap((node) => {
    const n = w.nodes[node];
    const schema = w.node_schemas[n.kind];
    if (!schema) return [];
    // two nodes of one kind: each field names its node ("Steps (sample_2)"), or the form shows two "Steps"
    const twin = kinds.filter((k) => k === n.kind).length > 1;
    const siblings = Object.fromEntries(
      schema.params.map((p) => [p.name, n.params?.[p.name] ?? p.default]),
    );
    return (
      schema.params
        .filter((param) => !(param.name in (n.in ?? {})))
        // `only`: the values this workflow allows of an enum param (API 1.19), the voice sources of a voice param
        // (1.30); the form offers just those
        .map((p) =>
          p.type === "voice"
            ? narrowed(p, n.only)
            : n.only?.[p.name]
              ? { ...p, enum: n.only[p.name] }
              : p,
        )
        .map((param) => ({
          key: `${node}.${param.name}`,
          node,
          nodeTitle: schema.title,
          nodeHelp: schema.help,
          param: twin ? { ...param, label: `${param.label ?? param.name} (${node})` } : param,
          initial: n.params?.[param.name] ?? param.default,
          kind: n.kind,
          canvas: schema.outputs.some((o) => o.type === "Canvas"),
          model: schema.outputs.some((o) => o.type === "Model"),
          family: schema.family,
          siblings,
          ...(n.optional && { optional: true }),
        }))
    );
  });
}

/** An enum value as shown: plain lowercase words title-cased, underscores as spaces ("base" → "Base",
 * "custom_voice" → "Custom voice"); ids like "int8" stay as they are. */
export const pretty = (v: string) =>
  /^[a-z]+(_[a-z]+)*$/.test(v) ? v[0].toUpperCase() + v.slice(1).replaceAll("_", " ") : v;

/** A param's enum value as shown: a `language` param's tag as its language ("en" → "English"; "auto" stays a
 * word; named as the node kind's family names it), else `pretty`. */
export const optionText = (p: Pick<Param, "name">, v: string, kind?: string) =>
  p.name === "language" && v !== "auto" ? languageName(v, kind?.split("/")[0]) : pretty(v);

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
  // a whole float keeps one decimal, so it reads as a float ("CFG 3.0", docs/mock/VideoSource.dc.html)
  if (f.param.type === "float" && /^-?\d+$/.test(v)) return { label, value: `${v}.0` };
  return { label, value: f.param.type === "enum" ? optionText(f.param, v, f.kind) : v };
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
 * field); `scope` holds the fields an expression may name (every field of the workflow: a placed field's `when` may
 * read an unplaced one, Qwen3-TTS style reads model.variant). Hidden fields are neither shown nor sent. */
export function visible(fs: Field[], raws: Record<string, Raw>, scope: Field[] = fs): Field[] {
  const get = (ref: string) => {
    const f = scope.find((x) => x.key === ref);
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

/** A value as a form control of param p holds it; a random seed (null or -1) is "", an image its take id. */
export function toRaw(v: Value, p: Param): Raw {
  if (p.type === "rows") return Array.isArray(v) ? v : [];
  if (p.type === "image") return v && typeof v === "object" && "take_id" in v ? v.take_id : "";
  if (p.type === "media") return isRef(v) ? v : "";
  if (p.type === "span") return isSpan(v) ? v : "";
  if (p.type === "voice") return isVoice(v) ? v : "";
  if (typeof v === "boolean") return v;
  return v == null || typeof v === "object" || (p.type === "seed" && v === -1) ? "" : String(v);
}

/** The form's controls filled from a run's inputs (a reused take's settings): only the workflow's params, each as
 * its control holds it. */
export function rawsFrom(fs: Field[], inputs: Record<string, Value>): Record<string, Raw> {
  return Object.fromEntries(
    fs.filter((f) => f.key in inputs).map((f) => [f.key, toRaw(inputs[f.key], f.param)]),
  );
}

/** The form control's content as a value, or why it is not one (worded like studio_service's 422). */
export function readValue(p: Param, raw: Raw): { value: Value } | { error: string } {
  if (p.type === "bool") return { value: raw === true };
  if (p.type === "rows") {
    const items = Array.isArray(raw) ? raw : [];
    if (p.max != null && items.length > p.max) return { error: `at most ${p.max} items` };
    if (p.min != null && items.length < p.min) return { error: `at least ${p.min} items` };
    const need = Object.keys(p.item ?? {}).filter((k) => p.item?.[k] === "Image");
    const bad = items.findIndex((it) => need.some((k) => !isRef(it[k])));
    if (bad >= 0) return { error: `item ${bad + 1}: an image is required` };
    // the other fields, a field left out at its default (studio checks the merged item)
    for (const [i, it] of items.entries())
      for (const [k, v] of Object.entries(p.item ?? {})) {
        if (typeof v !== "object") continue;
        const spec = { name: k, ...(v as object) } as Param;
        const r = readValue(spec, toRaw((it[k] ?? spec.default) as Value, spec));
        if ("error" in r) return { error: `item ${i + 1}, ${spec.label ?? k}: ${r.error}` };
      }
    const lora = Object.keys(p.item ?? {}).find((k) => p.item?.[k] === "Lora");
    const noLora = lora ? items.findIndex((it) => !isLoraRef(it[lora])) : -1;
    return noLora < 0 ? { value: items } : { error: `item ${noLora + 1}: a LoRA is required` };
  }
  if (p.type === "image")
    return typeof raw === "string" && raw
      ? { value: { take_id: raw } }
      : { error: "an image is required" };
  if (p.type === "media") return isRef(raw) ? { value: raw } : { error: "a take is required" };
  if (p.type === "voice") return isVoice(raw) ? { value: raw } : { error: "a voice is required" };
  if (p.type === "span")
    return !isSpan(raw)
      ? { error: "a range is required" }
      : raw.start < raw.end
        ? { value: raw }
        : { error: "start must be before end" };
  const text = String(raw);
  if (p.type === "seed") {
    const t = text.trim();
    if (t === "" || t === "-1") return { value: -1 }; // random: the runner picks one and reports it
    const n = Number(t);
    if (!Number.isSafeInteger(n) || n < 0 || (p.max != null && n > p.max))
      return { error: `must be a whole number from 0 to ${p.max ?? Number.MAX_SAFE_INTEGER}` };
    return { value: n };
  }
  if (p.type === "frames") {
    const t = text.trim();
    if (t === "auto") return p.auto ? { value: "auto" } : { error: "auto length is not offered" };
    const n = Number(t);
    if (t === "" || !Number.isInteger(n)) return { error: "must be a whole number" };
    const err = frameError(p, n);
    return err ? { error: err.replace(`${n} is not`, "must be") } : { value: n };
  }
  if (p.type === "int" || p.type === "float") {
    const t = text.trim();
    const n = Number(t);
    if (t === "" || !Number.isFinite(n)) return { error: "must be a number" };
    if (p.type === "int" && !Number.isInteger(n)) return { error: "must be a whole number" };
    if (p.type === "int" && p.values && !p.values.includes(n))
      return { error: `must be one of ${p.values.join(", ")}` };
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

/** Whether a value is a Lora (`{lora_id}`). */
const isLoraRef = (v: unknown) =>
  !!v && typeof v === "object" && typeof (v as { lora_id?: unknown }).lora_id === "string";

/** Whether a value is an Image (`{take_id}`). */
export const isRef = (v: unknown): v is S<"ImageRef"> =>
  !!v && typeof v === "object" && typeof (v as { take_id?: unknown }).take_id === "string";

/** Whether a value is a voice (`{voice_id}`, API 1.30). */
export const isVoice = (v: unknown): v is S<"VoiceRef"> =>
  !!v && typeof v === "object" && typeof (v as { voice_id?: unknown }).voice_id === "string";

/** Whether a value is a span (`{start, end}`). */
export const isSpan = (v: unknown): v is S<"SpanValue"> =>
  !!v &&
  typeof v === "object" &&
  typeof (v as { start?: unknown }).start === "number" &&
  typeof (v as { end?: unknown }).end === "number";

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
    const raw = raws[f.key] ?? toRaw(f.initial, f.param);
    // an optional image left empty is null (sent only when the workflow holds one)
    const r = f.optional && raw === "" ? { value: null } : readValue(f.param, raw);
    const allowed = allowedInts(f, raws);
    if ("error" in r) errors[f.key] = r.error;
    else if (allowed && !allowed.includes(r.value as number))
      errors[f.key] = `must be one of ${allowed.join(", ")}`;
    else {
      // keyframe times against the clip the form makes (studio's run rule, said per row)
      const all = positionOf(f.param)
        ? positionErrors(f.param, r.value as S<"RowItem">[], clipOf(fs, raws))
        : [];
      const i = all.findIndex(Boolean);
      if (i >= 0) errors[f.key] = `row ${i + 1}: ${all[i]}`;
      else if (!same(r.value, f.initial) && !(r.value === -1 && f.initial === null))
        inputs[f.key] = r.value;
    }
  }
  return { inputs, errors };
}

/** Equal values; a list or an image compares by content. */
export const same = (a: Value, b: Value) =>
  a === b || (typeof a === "object" && JSON.stringify(a) === JSON.stringify(b));

/** A node id's title from its schema ("sample" → "Sample"), else the id. */
export function nodeTitle(w: S<"Workflow"> | undefined, node: string) {
  const kind = w?.nodes[node]?.kind;
  return (kind && w?.node_schemas[kind]?.title) || node;
}

/** Node ids in graph order: every node after the nodes its inputs come from, else in declaration order. */
export function graphOrder(w: Pick<S<"Workflow">, "nodes">): string[] {
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

/** Whether a field is a LoRA stack's rows (its item has a `Lora` field). */
export const isLoraRows = (f: Field) =>
  f.param.type === "rows" && Object.values(f.param.item ?? {}).includes("Lora");

/** The inspector's sections: image inputs (References), other rows (each its own section: prompt beats), LoRA
 * stacks, the canvas node's params, frame counts with their frame rates (Duration), label-over-value tiles,
 * switches, and the rest. */
export function sections(fs: Field[]) {
  const rest = fs.filter((f) => f.param.type !== "text");
  const frames = rest.filter((f) => f.param.type === "frames");
  const rates = frames.flatMap((f) => fs.filter((x) => x.key === fpsKey(f)));
  const duration = rest.filter((f) => frames.includes(f) || rates.includes(f));
  const other = rest.filter((f) => !f.canvas && !duration.includes(f));
  return {
    duration,
    refs: other.filter((f) => f.param.type === "image" || !!imageField(f.param)),
    rows: other.filter((f) => f.param.type === "rows" && !isLoraRows(f) && !imageField(f.param)),
    loras: other.filter(isLoraRows),
    canvas: rest.filter((f) => f.canvas),
    // a video or audio take and the range on it (API 1.19): each its own section, in graph order
    media: other.filter((f) => f.param.type === "media" || f.param.type === "span"),
    // the model node's first, then graph order, the seed last (its home is the composer's chip), as in the mock
    tiles: other
      .filter((f) => ["enum", "int", "float", "seed"].includes(f.param.type))
      .sort((a, b) => rank(a) - rank(b)),
    switches: other.filter((f) => f.param.type === "bool"),
    more: other.filter((f) => f.param.type === "string"),
  };
}

/** A rows param's Image item field, if it has one. */
const imageField = (p: Param) => Object.keys(p.item ?? {}).find((k) => p.item?.[k] === "Image");

/** The key of a frames field's frame-rate field (`<node>.<fps>`), if it names one. */
export const fpsKey = (f: Field) => (f.param.fps ? `${f.node}.${f.param.fps}` : undefined);

/** The canvas aspect choices, as width / height. */
export const ASPECTS: [string, number][] = [
  ["1:1", 1],
  ["3:2", 3 / 2],
  ["16:9", 16 / 9],
  ["9:16", 9 / 16],
];
/** The canvas size choices, in megapixels (10^6 pixels), before a canvas's bounds narrow them (megapixelChoices). */
export const MEGAPIXELS = [0.25, 0.5, 1, 2, 4, 8, 16];

/** The size choices a canvas with sides in [min, max] can reach at every aspect (at least one), at most `top` MP:
 * above 4 MP only for a family measured there (form.tsx: a FIRST_REF_CANVAS; Qwen's sample at 8 MP is over the
 * 52 GB budget in bf16). */
export function megapixelChoices(min = 0, max = Infinity, top = 4) {
  const r = Math.max(...ASPECTS.map(([, x]) => Math.max(x, 1 / x)));
  const ok = MEGAPIXELS.filter(
    (m) => m <= top && m * 1e6 >= min * min * r && m * 1e6 * r <= max * max,
  );
  return ok.length
    ? ok
    : [
        MEGAPIXELS.reduce((a, b) =>
          Math.abs(b * 1e6 - max * max) < Math.abs(a * 1e6 - max * max) ? b : a,
        ),
      ];
}

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

/** The aspect choice a size is (within 2 %, or the width within half a grid step of it), else null. */
export const aspectOf = (w: number, h: number, grid = 1) =>
  ASPECTS.find(([, r]) => Math.abs(w / h / r - 1) < 0.02 || Math.abs(w - r * h) <= grid / 2)?.[0] ??
  null;

/** The size choice nearest to a size. */
export const megapixelsOf = (w: number, h: number, choices = MEGAPIXELS) =>
  choices.reduce((a, b) => (Math.abs(b - (w * h) / 1e6) < Math.abs(a - (w * h) / 1e6) ? b : a));

/** The session rule (plan 01-concepts.md, Session) on a switch of workflow from the fields `from` to `fs`: what the
 * controls hold carries over where the paths match and the value suits the new field (a canvas's width and height
 * together; a LoRA stack only within its family, its LoRAs fit no other); a value that does not suit goes, so the
 * field starts from `back` (what the new workflow held when it was last left), else from the workflow's own. Paths
 * the new workflow lacks are kept for a switch back. */
export function carry(
  raws: Record<string, Raw>,
  fs: Field[],
  from: Field[] = [],
  back: Record<string, Raw> = {},
): Record<string, Raw> {
  const was = new Map(from.map((f) => [f.key, f]));
  const otherFamily = (f: Field) =>
    isLoraRows(f) && was.has(f.key) && was.get(f.key)?.family !== f.family;
  const bad = (f: Field) =>
    f.key in raws && (otherFamily(f) || "error" in readValue(f.param, raws[f.key]));
  const canvasBad = fs.some((f) => f.canvas && bad(f));
  const drop = new Set(fs.filter((f) => bad(f) || (f.canvas && canvasBad)).map((f) => f.key));
  const out = Object.fromEntries(Object.entries(raws).filter(([k]) => !drop.has(k)));
  for (const f of fs) if (!(f.key in out) && f.key in back) out[f.key] = back[f.key];
  return out;
}

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
