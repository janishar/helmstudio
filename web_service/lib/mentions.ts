// `@` mentions of elements (contracts/README.md "Mentions (normative)"): the grammar, the caret's token for the `@`
// menu, inserting a mention, and the expansion studio_service does at run create (here for the preview of what the
// mentions add and the cap error before Generate). Tested against contracts/fixtures/mentions; check.py is the
// reference implementation.
import type { S } from "@/lib/api";

type Param = S<"ParamSchema">;
type Value = S<"ParamValue">;

export type Part = { text: string } | { mention: string; raw: string };

const WORD = /[A-Za-z0-9_]/;
const TOKEN = /[A-Za-z0-9_-]/;

/** The token after an `@` at index i, when that `@` starts one: the run of `A-Za-z0-9_-` (end = where it stops)
 * minus trailing `-` and `_`; null when the `@` is not at the start nor after a non-word character. */
function tokenAt(text: string, i: number) {
  if (i > 0 && WORD.test(text[i - 1])) return null;
  let end = i + 1;
  while (end < text.length && TOKEN.test(text[end])) end++;
  return { tok: text.slice(i + 1, end).replace(/[-_]+$/, ""), end };
}

/** The prompt as parts: raw text (`@@` kept) and mentions (handle lower-cased); the raw parts join to the text. */
export function scan(text: string): Part[] {
  const parts: Part[] = [];
  let buf = "";
  let i = 0;
  while (i < text.length) {
    if (text.startsWith("@@", i)) {
      buf += "@@";
      i += 2;
      continue;
    }
    const t = text[i] === "@" ? tokenAt(text, i) : null;
    if (t && /^[A-Za-z]/.test(t.tok)) {
      if (buf) parts.push({ text: buf });
      parts.push({ mention: t.tok.toLowerCase(), raw: "@" + t.tok });
      buf = "";
      i += 1 + t.tok.length;
      continue;
    }
    buf += text[i];
    i++;
  }
  return buf ? [...parts, { text: buf }] : parts;
}

/** Where the mentions are in the text (UTF-16 offsets, as a textarea counts): the chips of the highlight. */
export function mentionRanges(text: string) {
  const out: { start: number; end: number; handle: string }[] = [];
  let at = 0;
  for (const p of scan(text)) {
    const raw = "text" in p ? p.text : p.raw;
    if ("mention" in p) out.push({ start: at, end: at + raw.length, handle: p.mention });
    at += raw.length;
  }
  return out;
}

/** The distinct handles mentioned, first mention first. */
export const mentioned = (text: string) => [
  ...new Set(scan(text).flatMap((p) => ("mention" in p ? [p.mention] : []))),
];

/** The `@` token the caret is in (the `@` menu's state): from the `@` (start) to the token's end, and the query
 * typed so far (lower-cased; "" right after the `@`). Null when the caret is not in one, the `@` is escaped (`@@`)
 * or follows a word character, or the query cannot start a handle (`@3`). */
export function mentionAt(text: string, caret: number) {
  let i = 0;
  while (i < caret) {
    if (text.startsWith("@@", i)) {
      i += 2;
      continue;
    }
    const t = text[i] === "@" ? tokenAt(text, i) : null;
    if (t) {
      if (caret <= t.end) {
        const query = text.slice(i + 1, caret);
        return query === "" || /^[A-Za-z]/.test(query)
          ? { start: i, end: t.end, query: query.toLowerCase() }
          : null;
      }
      i = t.end;
      continue;
    }
    i++;
  }
  return null;
}

/** The text with `@handle` in place of [start, end) (the caret's token, or the selection when inserting from the
 * picker) and the caret after it. A space goes before it when it would otherwise follow a word character or an `@`
 * (and so not be a mention), and after it unless whitespace follows. */
export function insertMention(text: string, start: number, end: number, handle: string) {
  const before = text.slice(0, start);
  const after = text.slice(end);
  const lead = text[start] !== "@" && /[A-Za-z0-9_@]$/.test(before) ? " " : "";
  const ins = `${lead}@${handle}${/^\s/.test(after) ? "" : " "}`;
  return { text: before + ins + after, caret: start + ins.length };
}

/** A handle proposed from a name: "Red Fox (cub)" → "red-fox-cub"; "" when the name has no letter to start one. */
export function handleFrom(name: string) {
  const s = name
    .normalize("NFKD")
    .replace(/[̀-ͯ]/g, "")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^[^a-z]+/, "")
    .slice(0, 40);
  return s.replace(/[-_]+$/, "");
}

/** The handle rule (LibraryItem.handle). */
export const HANDLE = /^[a-z](?:[a-z0-9_-]{0,38}[a-z0-9])?$/;

/** An element as the resolver reads it: images as take ids (deleted takes already left out). */
export type Resolvable = {
  id: string;
  handle: string;
  name: string;
  version: number;
  text: string;
  images: string[];
};

/** Library items as the resolver reads them. */
export const resolvable = (items: S<"LibraryItem">[]): Resolvable[] =>
  items.flatMap((e) =>
    e.handle ? [{ ...e, handle: e.handle, images: e.images.map((i) => i.take_id) }] : [],
  );

type Node = {
  kind: string;
  params?: Record<string, Value>;
  in?: Record<string, string>;
  bypass?: boolean;
};
type Schemas = Record<string, { params: Param[] } | undefined>;

/** A rows param's one Image field, else undefined. */
const imageField = (p: Param) => {
  const ks =
    p.type === "rows" ? Object.keys(p.item ?? {}).filter((k) => p.item?.[k] === "Image") : [];
  return ks.length === 1 ? ks[0] : undefined;
};

/** The nodes in id order (code points) that the expansion looks at: known, not bypassed. */
const known = (nodes: Record<string, Node>, schemas: Schemas) =>
  Object.keys(nodes)
    .sort()
    .flatMap((id) => {
      const s = schemas[nodes[id].kind];
      return s && !nodes[id].bypass ? [{ id, n: nodes[id], params: s.params }] : [];
    });

/** Where element images go: the first unwired rows param with a `{n}` mention and one Image field; null = the
 * family takes text only. */
export function referenceRows(nodes: Record<string, Node>, schemas: Schemas) {
  for (const { id, n, params } of known(nodes, schemas))
    for (const p of params)
      if (
        p.type === "rows" &&
        p.mention?.includes("{n}") &&
        imageField(p) &&
        !(p.name in (n.in ?? {}))
      )
        return { node: id, param: p };
  return null;
}

/** The workflow's nodes with run inputs (`<node>.<param>` → value) merged over their params. */
export function withInputs(nodes: Record<string, Node>, inputs: Record<string, Value>) {
  const out: Record<string, Node> = { ...nodes };
  for (const [key, v] of Object.entries(inputs)) {
    const [id, name] = key.split(".");
    if (out[id]) out[id] = { ...out[id], params: { ...out[id].params, [name]: v } };
  }
  return out;
}

/** A look as the resolver reads it (contracts/README.md "Looks"): images as take ids, LoRAs with their saved
 * strength (`lora_id` null: a starter LoRA no live host has). */
export type ResolvableLook = {
  id: string;
  source: S<"LibrarySource">;
  name: string;
  version: number;
  text: string;
  images: string[];
  loras: { lora_id: string | null; name: string; strength: number }[];
};

/** Library looks as the resolver reads them. */
export const resolvableLooks = (items: S<"LibraryItem">[]): ResolvableLook[] =>
  items.flatMap((x) =>
    x.kind === "look"
      ? [
          {
            id: x.id,
            source: x.source ?? (x.id.includes("/") ? "starter" : "mine"),
            name: x.name,
            version: x.version,
            text: x.text,
            images: x.images.map((i) => i.take_id),
            loras: (x.loras ?? []).map((l) => ({
              lora_id: l.lora?.lora_id ?? null,
              name: l.name,
              strength: l.strength,
            })),
          },
        ]
      : [],
  );

/** Mentions "Where": every unwired `text` param of a known, not bypassed node, node id order then schema order. */
export const promptParams = (nodes: Record<string, Node>, schemas: Schemas) =>
  known(nodes, schemas).flatMap(({ id, n, params }) =>
    params.filter((p) => p.type === "text" && !(p.name in (n.in ?? {}))).map((p) => ({ id, n, p })),
  );

/** A rows param's Lora field, else undefined. */
const loraField = (p: Param) =>
  p.type === "rows" ? Object.keys(p.item ?? {}).find((k) => p.item?.[k] === "Lora") : undefined;

/** Where look LoRAs go: among the known, not bypassed `<family>/lora-stack` nodes with an unwired rows param that has
 * a Lora field, the first by node id from which no other such node is reachable downstream; null = none. */
export function lookStack(nodes: Record<string, Node>, schemas: Schemas) {
  const cands = new Map<string, Param>();
  for (const { id, n, params } of known(nodes, schemas)) {
    const p = n.kind.endsWith("/lora-stack")
      ? params.find((x) => loraField(x) && !(x.name in (n.in ?? {})))
      : undefined;
    if (p) cands.set(id, p);
  }
  const succ = (id: string) =>
    Object.keys(nodes).filter((m) =>
      Object.values(nodes[m].in ?? {}).some((ref) => ref.split(".")[0] === id),
    );
  const reaches = (id: string) => {
    const seen = new Set<string>();
    const todo = succ(id);
    while (todo.length) {
      const x = todo.pop() as string;
      if (!seen.has(x)) {
        seen.add(x);
        todo.push(...succ(x));
      }
    }
    return seen;
  };
  for (const [id, p] of cands)
    if (![...reaches(id)].some((x) => cands.has(x))) return { node: id, param: p };
  return null;
}

/** What an item brings (LibraryItem.needs): text, images, lora, in that order. */
export const needsOf = (x: { text: string; images: unknown[]; loras?: unknown[] }) =>
  (
    [
      ["text", !!x.text],
      ["images", x.images.length > 0],
      ["lora", !!x.loras?.length],
    ] as const
  ).flatMap(([k, on]) => (on ? [k] : []));

/** Why an item cannot be applied to this graph (README, Looks: fit), null when it can. `loras`: LoRA id → detected
 * family of every LoRA the caller can use now. */
export function lookFit(
  nodes: Record<string, Node>,
  schemas: Schemas,
  item: Pick<ResolvableLook, "text" | "images"> & {
    kind?: string;
    loras?: ResolvableLook["loras"];
  },
  loras: Record<string, string>,
): string | null {
  const needs = needsOf(item);
  const element = item.kind === "element";
  if ((needs.includes("text") || element) && !promptParams(nodes, schemas).length)
    return "this workflow has no prompt";
  if (element) return null; // its images are dropped where there are no reference rows
  if (needs.includes("images") && !referenceRows(nodes, schemas))
    return "this workflow takes no reference images";
  if (needs.includes("lora")) {
    const stack = lookStack(nodes, schemas);
    if (!stack) return "this workflow has no LoRA stack";
    const kind = nodes[stack.node].kind;
    for (const x of item.loras ?? []) {
      if (x.lora_id === null) return `LoRA ${x.name} is on no live host`;
      const fam = loras[x.lora_id];
      if (fam === undefined) return `LoRA ${x.name} is not available`;
      if (fam === "unknown") return `LoRA ${x.name} fits no known model`;
      if (kind.includes("/") && fam !== kind.split("/")[0]) return `LoRA ${x.name} is for ${fam}`;
    }
  }
  return null;
}

export type Resolved = {
  inputs: Record<string, Value>;
  typed: Record<string, Value>;
  elements: { id: string; handle: string; name: string; version: number }[];
  looks: { id: string; source: S<"LibrarySource">; name: string; version: number }[];
  errors: string[];
  /** each used handle's image mentions (`<image3>`), empty without reference rows */
  tokens: Record<string, string[]>;
  /** each applied look's image mentions, and the LoRAs it added to the stack (names) */
  lookTokens: Record<string, string[]>;
  lookLoras: Record<string, string[]>;
};

/** Canvas `auto` with references (the runner's): about 1 MP at width / height, sides rounded half to even to 32. */
export function autoSize(width: number, height: number): [number, number] {
  const even = (x: number) => {
    const f = Math.floor(x);
    return x - f === 0.5 ? (f % 2 ? f + 1 : f) : Math.round(x);
  };
  const ratio = width / height;
  const w = Math.sqrt(1024 * 1024 * ratio);
  return [even(w / 32) * 32, even(w / ratio / 32) * 32];
}

/** The one resolver of mentions and chosen looks (studio_service's, plan 14.2): changed paths → resolved values, their
 * values as typed, the elements and looks used, or the errors (422 detail parts). `sizes`: take id → [width, height] of
 * the user's row takes, when known (the canvas pin); `looks`: the looks the caller can use now; `chosen`:
 * RunRequest.looks; `loras`: LoRA id → detected family of the caller's usable LoRAs (the fit). */
export function resolveMentions(
  nodes: Record<string, Node>,
  schemas: Schemas,
  library: Resolvable[],
  sizes: Record<string, [number, number]> = {},
  looks: ResolvableLook[] = [],
  chosen: string[] = [],
  loras: Record<string, string> = {},
): Resolved {
  const lib = new Map(library.map((e) => [e.handle, e]));
  const found = new Map(looks.map((x) => [x.id, x]));
  const val = (n: Node, p: Param) => n.params?.[p.name] ?? p.default;
  const prompts = promptParams(nodes, schemas).map(({ id, n, p }) => {
    const text = String(val(n, p) ?? "");
    return { n, p, path: `${id}.${p.name}`, text, parts: scan(text), used: mentioned(text) };
  });
  const none = {
    inputs: {},
    typed: {},
    elements: [],
    looks: [],
    tokens: {},
    lookTokens: {},
    lookLoras: {},
  };
  const first = new Map<string, number>(); // look id → its first place in the request, from 1
  chosen.forEach((id, i) => first.has(id) || first.set(id, i + 1));
  const unknown = [
    ...prompts.flatMap((x) =>
      x.used.filter((h) => !lib.has(h)).map((h) => `${x.path}: unknown element @${h}`),
    ),
    ...[...first]
      .filter(([id]) => !found.has(id))
      .map(([id, i]) => `looks[${i}]: unknown look ${id}`),
  ];
  if (unknown.length) return { ...none, errors: unknown };
  const picked = [...first.keys()].map((id) => found.get(id) as ResolvableLook);
  const unfit = picked.flatMap((x) => {
    const r = lookFit(nodes, schemas, x, loras);
    return r ? [`looks[${first.get(x.id)}]: look ${x.name} does not fit this workflow (${r})`] : [];
  });
  if (unfit.length) return { ...none, errors: unfit };
  const used = [...new Set(prompts.flatMap((x) => x.used))];
  // what brings images, in order: the mentioned elements, then the looks
  const adds = [
    ...used.map((h) => ({ label: `@${h}`, key: `e:${h}`, images: lib.get(h)!.images })),
    ...picked.map((x) => ({ label: `look ${x.name}`, key: `l:${x.id}`, images: x.images })),
  ];
  const tok: Record<string, string[]> = Object.fromEntries(adds.map((a) => [a.key, []]));
  const inputs: Record<string, Value> = {};
  const typed: Record<string, Value> = {};
  const target = adds.length ? referenceRows(nodes, schemas) : null;
  if (target) {
    const { node, param: tp } = target;
    const field = imageField(tp) as string;
    const cap = tp.max ?? 100;
    const path = `${node}.${tp.name}`;
    const before = (val(nodes[node], tp) ?? []) as S<"RowItem">[];
    const rows = [...before];
    const at = new Map<string, number>(); // take id → its row number (the user's rows first)
    rows.forEach((r, i) => {
      const ref = r[field] as { take_id?: string } | null;
      if (ref && typeof ref === "object" && ref.take_id && !at.has(ref.take_id))
        at.set(ref.take_id, i + 1);
    });
    const over: string[] = [];
    let full: number | null = null;
    for (const a of adds) {
      const fresh = [...new Set(a.images)].filter((t) => !at.has(t));
      if (full !== null || rows.length + fresh.length > cap) {
        full ??= rows.length;
        if (fresh.length) over.push(`${a.label} +${fresh.length}`);
        continue;
      }
      for (const t of fresh) {
        rows.push({ [field]: { take_id: t } });
        at.set(t, rows.length);
      }
      tok[a.key] = a.images.map((t) => (tp.mention as string).replaceAll("{n}", String(at.get(t))));
    }
    if (over.length)
      return {
        ...none,
        errors: [`${path}: at most ${cap} images; ${full} used, ${over.join(", ")} do not fit`],
      };
    if (rows.length > before.length) {
      inputs[path] = rows;
      typed[path] = before;
      // element and look rows never change the output size: each auto canvas is pinned to what auto gives without them
      const last = before.at(-1)?.[field] as { take_id?: string } | null | undefined;
      const size =
        last && typeof last === "object" && last.take_id ? sizes[last.take_id] : undefined;
      for (const { id, n, params } of known(nodes, schemas)) {
        const ps = Object.fromEntries(params.map((p) => [p.name, p]));
        if (n.kind !== "canvas" || val(n, ps.mode) !== "auto") continue;
        inputs[`${id}.mode`] = "fixed";
        typed[`${id}.mode`] = "auto";
        if (!size) continue;
        autoSize(...size).forEach((v, i) => {
          const p = ps[i ? "height" : "width"];
          v = Math.min(Math.max(v, p.min ?? v), p.max ?? v);
          if (v !== val(n, p)) {
            inputs[`${id}.${p.name}`] = v;
            typed[`${id}.${p.name}`] = val(n, p);
          }
        });
      }
    }
  }
  const lookLoras: Record<string, string[]> = Object.fromEntries(picked.map((x) => [x.id, []]));
  const stack = picked.some((x) => x.loras.length) ? lookStack(nodes, schemas) : null;
  if (stack) {
    const { node, param: sp } = stack;
    const field = loraField(sp) as string;
    const cap = sp.max ?? 100;
    const path = `${node}.${sp.name}`;
    const before = (val(nodes[node], sp) ?? []) as S<"RowItem">[];
    // the user's rows win: a LoRA already in any stack's rows is not added again
    const present = new Set<string>();
    for (const { n, params } of known(nodes, schemas))
      for (const p of params) {
        const f = loraField(p);
        if (!f) continue;
        for (const r of (val(n, p) ?? []) as S<"RowItem">[]) {
          const ref = r[f] as { lora_id?: string } | null;
          if (ref && typeof ref === "object" && ref.lora_id) present.add(ref.lora_id);
        }
      }
    const st = sp.item?.strength as unknown as Partial<Param> | undefined;
    const clamp = (v: number) =>
      st && typeof st === "object" ? Math.min(Math.max(v, st.min ?? v), st.max ?? v) : v;
    const rows = [...before];
    const over: string[] = [];
    let full: number | null = null;
    for (const x of picked) {
      const fresh: ResolvableLook["loras"] = [];
      for (const y of x.loras)
        if (!present.has(y.lora_id as string) && !fresh.some((z) => z.lora_id === y.lora_id))
          fresh.push(y);
      if (full !== null || rows.length + fresh.length > cap) {
        full ??= rows.length;
        if (fresh.length) over.push(`look ${x.name} +${fresh.length}`);
        continue;
      }
      for (const y of fresh) {
        rows.push({
          [field]: { lora_id: y.lora_id as string },
          ...(st ? { strength: clamp(y.strength) } : {}),
        });
        present.add(y.lora_id as string);
        lookLoras[x.id].push(y.name);
      }
    }
    if (over.length)
      return {
        ...none,
        errors: [`${path}: at most ${cap} LoRAs; ${full} used, ${over.join(", ")} do not fit`],
      };
    if (rows.length > before.length) {
      inputs[path] = rows;
      typed[path] = before;
    }
  }
  const lines = [
    ...new Set(
      picked
        .map((x) => [...(x.text ? [x.text] : []), ...tok[`l:${x.id}`]].join(" "))
        .filter(Boolean),
    ),
  ];
  const errors: string[] = [];
  prompts.forEach((x, i) => {
    let out = x.parts
      .map((p) =>
        "text" in p
          ? p.text.replaceAll("@@", "@")
          : [lib.get(p.mention)!.name, ...tok[`e:${p.mention}`]].join(" "),
      )
      .join("");
    const notes = x.used.flatMap((h) => {
      const e = lib.get(h)!;
      return e.text ? [`${e.name}: ${e.text}`] : [];
    });
    if (notes.length) out += "\n\n" + notes.join("\n");
    const extra = i === 0 ? lines : []; // look lines go to the first prompt param only
    out = [out, extra.join("\n")].filter(Boolean).join("\n\n");
    const length = [...out].length;
    if (x.p.max != null && length > x.p.max)
      errors.push(
        `${x.path}: at most ${x.p.max} characters once ${extra.length ? "elements and looks" : "elements"} are added (${length})`,
      );
    else if (out !== x.text) {
      inputs[x.path] = out;
      typed[x.path] = x.text;
    }
  });
  if (errors.length) return { ...none, errors };
  const elements = used.map((h) => {
    const { id, handle, name, version } = lib.get(h)!;
    return { id, handle, name, version };
  });
  return {
    inputs,
    typed,
    elements,
    looks: picked.map(({ id, source, name, version }) => ({ id, source, name, version })),
    errors: [],
    tokens: Object.fromEntries(used.map((h) => [h, tok[`e:${h}`]])),
    lookTokens: Object.fromEntries(picked.map((x) => [x.id, tok[`l:${x.id}`]])),
    lookLoras,
  };
}

/** The line under the prompt for the chosen looks: what each adds ("Looks add Window: text · Noir: <image3> + text +
 * LoRA ink-wash"), or null without looks. */
export function looksNote(r: Resolved, looks: ResolvableLook[]) {
  if (!r.looks.length) return null;
  const by = new Map(looks.map((x) => [x.id, x]));
  const each = r.looks.map((x) => {
    const t = r.lookTokens[x.id] ?? [];
    const l = r.lookLoras[x.id] ?? [];
    const what = [
      ...(by.get(x.id)?.text ? ["text"] : []),
      ...(t.length ? [t.join(" ")] : []),
      ...l.map((n) => `LoRA ${n}`),
    ];
    return `${x.name}: ${what.join(" + ") || "nothing new"}`;
  });
  return `Looks add ${each.join(" · ")}`;
}

/** The line under the prompt: what the mentions add for this workflow ("@fox: <image3> <image4> + text · @owl:
 * text"), or null without mentions. */
export function mentionNote(r: Resolved, library: Resolvable[], textOnly: boolean) {
  if (!r.elements.length) return null;
  const lib = new Map(library.map((e) => [e.handle, e]));
  const each = r.elements.map((e) => {
    const t = r.tokens[e.handle] ?? [];
    const what = [...(t.length ? [t.join(" ")] : []), ...(lib.get(e.handle)?.text ? ["text"] : [])];
    return `@${e.handle}: ${what.join(" + ") || "its name"}`;
  });
  return `${textOnly ? "Text only in this workflow. " : ""}Mentions add ${each.join(" · ")}`;
}

/** A 422 `detail` ("prompt.text: unknown element @fox; encode.refs: …") as errors by field key, when every part
 * names one of `keys`; else null (shown as a whole). */
export function fieldErrors(detail: string, keys: string[]): Record<string, string> | null {
  const out: Record<string, string> = {};
  let last = "";
  for (const part of detail.split("; ")) {
    const key = keys.find((k) => part.startsWith(k + ": ") || part.startsWith(k + "["));
    // a row's item ("keyframes.images[2].seconds: …") is said as "row 2: …"
    const rest = key && part.slice(key.length).replace(/^\[(\d+)\](?:\.\w+)?: /, "row $1: ");
    // a part naming no key continues the one before ("at most 10 images; 9 used, … do not fit")
    const [k, text] = key && rest ? [key, rest.replace(/^: /, "")] : [last, part];
    if (!k) return null;
    out[k] = out[k] ? `${out[k]}; ${text}` : text;
    last = k;
  }
  return out;
}
