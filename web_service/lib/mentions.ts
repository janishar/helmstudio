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

export type Resolved = {
  inputs: Record<string, Value>;
  typed: Record<string, Value>;
  elements: { id: string; handle: string; name: string; version: number }[];
  errors: string[];
  /** each used handle's image mentions (`<image3>`), empty without reference rows */
  tokens: Record<string, string[]>;
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

/** The expansion of every mention (studio_service's resolver): changed paths → resolved values, their values as
 * typed, the elements used, or the errors (422 detail parts). `sizes`: take id → [width, height] of the user's row
 * takes, when known (the canvas pin). */
export function resolveMentions(
  nodes: Record<string, Node>,
  schemas: Schemas,
  library: Resolvable[],
  sizes: Record<string, [number, number]> = {},
): Resolved {
  const lib = new Map(library.map((e) => [e.handle, e]));
  const val = (n: Node, p: Param) => n.params?.[p.name] ?? p.default;
  const prompts = known(nodes, schemas).flatMap(({ id, n, params }) =>
    params
      .filter((p) => p.type === "text" && !(p.name in (n.in ?? {})))
      .map((p) => {
        const text = String(val(n, p) ?? "");
        return { n, p, path: `${id}.${p.name}`, text, parts: scan(text), used: mentioned(text) };
      }),
  );
  const none = { inputs: {}, typed: {}, elements: [], tokens: {} };
  const unknown = prompts.flatMap((x) =>
    x.used.filter((h) => !lib.has(h)).map((h) => `${x.path}: unknown element @${h}`),
  );
  if (unknown.length) return { ...none, errors: unknown };
  const used = [...new Set(prompts.flatMap((x) => x.used))];
  const tokens: Record<string, string[]> = Object.fromEntries(used.map((h) => [h, []]));
  const inputs: Record<string, Value> = {};
  const typed: Record<string, Value> = {};
  const target = used.length ? referenceRows(nodes, schemas) : null;
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
    for (const h of used) {
      const e = lib.get(h) as Resolvable;
      const fresh = e.images.filter((t) => !at.has(t));
      if (full !== null || rows.length + fresh.length > cap) {
        full ??= rows.length;
        if (fresh.length) over.push(`@${h} +${fresh.length}`);
        continue;
      }
      for (const t of fresh) {
        rows.push({ [field]: { take_id: t } });
        at.set(t, rows.length);
      }
      tokens[h] = e.images.map((t) => (tp.mention as string).replaceAll("{n}", String(at.get(t))));
    }
    if (over.length)
      return {
        ...none,
        errors: [`${path}: at most ${cap} images; ${full} used, ${over.join(", ")} do not fit`],
      };
    if (rows.length > before.length) {
      inputs[path] = rows;
      typed[path] = before;
      // element rows never change the output size: each auto canvas is pinned to what auto gives without them
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
  const errors: string[] = [];
  for (const x of prompts) {
    let out = x.parts
      .map((p) =>
        "text" in p
          ? p.text.replaceAll("@@", "@")
          : [(lib.get(p.mention) as Resolvable).name, ...tokens[p.mention]].join(" "),
      )
      .join("");
    const notes = x.used.flatMap((h) => {
      const e = lib.get(h) as Resolvable;
      return e.text ? [`${e.name}: ${e.text}`] : [];
    });
    if (notes.length) out += "\n\n" + notes.join("\n");
    const length = [...out].length;
    if (x.p.max != null && length > x.p.max)
      errors.push(`${x.path}: at most ${x.p.max} characters once elements are added (${length})`);
    else if (out !== x.text) {
      inputs[x.path] = out;
      typed[x.path] = x.text;
    }
  }
  if (errors.length) return { ...none, errors };
  const elements = used.map((h) => {
    const { id, handle, name, version } = lib.get(h) as Resolvable;
    return { id, handle, name, version };
  });
  return { inputs, typed, elements, errors: [], tokens };
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
    const key = keys.find((k) => part.startsWith(k + ": "));
    // a part naming no key continues the one before ("at most 10 images; 9 used, … do not fit")
    const [k, text] = key ? [key, part.slice(key.length + 2)] : [last, part];
    if (!k) return null;
    out[k] = out[k] ? `${out[k]}; ${text}` : text;
    last = k;
  }
  return out;
}
