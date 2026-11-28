// Voices (contracts/README.md "Voices (API 1.30)", "Cloned voices (API 1.31)"): the voice fit (check.py
// `voice_sources` / `voice_reason` / `voice_fit`, tested against contracts/fixtures/voices/fit.json), a voice's words in
// the picker and on its tiles, the voice picker's groups, and a clip's limits (3–30 s of a take with sound).
import { clock } from "@/lib/format";
import type { S } from "@/lib/api";
import { mediaValueError, type Facts } from "@/lib/source";

type Item = S<"LibraryItem">;
type Node = { kind: string; bypass?: boolean; only?: Record<string, string[]> };
type Schemas = Record<string, { params: S<"ParamSchema">[] } | undefined>;
type Param = S<"ParamSchema">;

/** A voice's group → the source a voice param's `accepts` names. */
const SOURCE = { preset: "preset", cloned: "clip", designed: "design" } as const;
const REFUSED = {
  preset: "this workflow cannot use a preset voice",
  clip: "this workflow cannot clone a voice",
  design: "this workflow cannot use a designed voice",
} as const;

/** A rows param's one voice field (API 1.32: a dialogue's `lines`), else null. */
export function voiceField(p: Param): string | null {
  if (p.type !== "rows") return null;
  const fs = Object.entries(p.item ?? {}).filter(
    ([, v]) => typeof v === "object" && (v as unknown as Param).type === "voice",
  );
  return fs.length === 1 ? fs[0][0] : null;
}

/** Every voice param of a known, not bypassed node: node id order (code points), then schema order; its `accepts`
 * narrowed to the node's `only` (API 1.30); from 1.32 a rows param's one voice field counts as one, named
 * `<param>.<field>` and narrowed by `only` on the rows param. */
export function voiceParams(nodes: Record<string, Node>, schemas: Schemas): [string, Param][] {
  return Object.keys(nodes)
    .sort((a, b) => (a < b ? -1 : a > b ? 1 : 0))
    .flatMap((id) => {
      const s = schemas[nodes[id].kind];
      if (!s || nodes[id].bypass) return [];
      return s.params.flatMap((p): [string, Param][] => {
        const f = voiceField(p);
        const q =
          p.type === "voice"
            ? p
            : f
              ? ({ ...(p.item![f] as unknown as Param), name: `${p.name}.${f}` } as Param)
              : null;
        return q ? [[id, narrowed(q, nodes[id].only, p.name)]] : [];
      });
    });
}

/** A voice param with its `accepts` narrowed to a workflow node's `only` for it (README, Media inputs: only); a rows
 * voice field reads its rows param's (`key`). */
export const narrowed = (p: Param, only?: Record<string, string[]>, key = p.name): Param =>
  only?.[key]
    ? {
        ...p,
        accepts: (p.accepts ?? []).filter((a) => only[key].includes(a)) as Param["accepts"],
      }
    : p;

/** voice_sources (API 1.31): the sources a voice offers, its own, then `clip` when it keeps one. */
export function voiceSources(item: Item): ("preset" | "clip" | "design")[] {
  const own = SOURCE[item.group as keyof typeof SOURCE];
  if (!own) return [];
  return own !== "clip" && item.voice?.clip ? [own, "clip"] : [own];
}

/** voice_source: the first source the voice offers that the param takes (what a run sends), else undefined. */
export const voiceSource = (param: Param, item: Item) =>
  voiceSources(item).find((s) => (param.accepts as string[] | undefined)?.includes(s));

/** Why one voice param of a node of `kind` cannot take this voice, null when it can (studio's run 422 is
 * `<node>.<param>: <reason>`): no source it offers is in the param's `accepts` (the reason of its own source), or a
 * preset of another family. */
export function voiceReason(kind: string, param: Param, item: Item): string | null {
  const own = voiceSources(item)[0];
  if (!own) return null;
  const src = voiceSource(param, item);
  if (!src) return REFUSED[own];
  const family = kind.includes("/") ? kind.split("/")[0] : null;
  if (src === "preset" && item.voice?.family !== family)
    return `voice ${item.name} is for ${item.voice?.family}`;
  return null;
}

/** Whether some voice param of the workflow takes a voice's clip (ZONOS3: cloned voices and designed ones with a
 * clip fit). */
export const takesClips = (w: { nodes: Record<string, Node>; node_schemas: Schemas }) =>
  voiceParams(w.nodes, w.node_schemas).some(([, p]) => p.accepts?.includes("clip"));

/** GET /library?workflow= for a voice: null when some voice param takes it, else the first param's reason. */
export function voiceFit(
  w: { nodes: Record<string, Node>; node_schemas: Schemas },
  item: Item,
): string | null {
  const params = voiceParams(w.nodes, w.node_schemas);
  if (!params.length) return "this workflow takes no voice";
  const reasons = params.map(([id, p]) => voiceReason(w.nodes[id].kind, p, item));
  return reasons.includes(null) ? null : reasons[0];
}

/** Why the picker greys a voice: the list's own `reason` when it was read for this workflow, else the fit here. */
export const reasonFor = (
  w: { nodes: Record<string, Node>; node_schemas: Schemas },
  item: Item,
): string | null =>
  item.fits === undefined ? voiceFit(w, item) : item.fits ? null : (item.reason ?? null);

/** A family's own name for a tag, where its board differs from Intl's: ZONOS3's zh is Mandarin
 * (docs/mock/VoicePickerZonos3.dc.html; Qwen3-TTS's zh stays Chinese, its board's). */
const FAMILY_NAMES: Record<string, Record<string, string>> = { zonos3: { zh: "Mandarin" } };

/** A BCP 47 tag's language in English ("en" → "English", "en-US" → "English (US)", the board's), as the family
 * names it; the tag itself when it is not known. */
export function languageName(tag?: string | null, family?: string | null): string {
  if (!tag) return "";
  const own = family ? FAMILY_NAMES[family]?.[tag] : undefined;
  if (own) return own;
  try {
    return (
      new Intl.DisplayNames(["en"], {
        type: "language",
        languageDisplay: "standard",
        style: "short",
      }).of(tag) ?? tag
    );
  } catch {
    return tag;
  }
}

/** A tag's language without its region ("en-US" → "en"): the picker's language chips and card lines. */
export const baseLanguage = (tag?: string | null) => (tag ?? "").split("-")[0];

/** A clip's part: "0:04–0:16". */
export const clipText = (c: { in: number; out: number }) => `${clock(c.in)}–${clock(c.out)}`;

/** A voice card's line: "American female · English" for a preset (its style, then its language without the
 * region), the description for a designed voice, the part for a cloned one (the picker adds its take). */
export function voiceLine(x: Item): string {
  const v = x.voice;
  if (!v) return "";
  if (v.source === "designed") return v.design ?? "";
  if (v.source === "cloned") return v.clip ? clipText(v.clip) : "";
  return [v.style, languageName(baseLanguage(v.language), v.family)].filter(Boolean).join(" · ");
}

/** Why a Mine voice fits a workflow that takes clips (the board's green line), "" when it fits otherwise. */
export const clipFitLine = (x: Item) =>
  x.voice?.source === "cloned"
    ? "cloned · a voice print of its clip"
    : x.voice?.clip
      ? `${x.voice.source} · has its clip`
      : "";

/** A voice's fields for a PUT (Rename): as stored, the clip as a media value and its consent again. */
export function voiceBody(x: Item): S<"VoiceDesign"> {
  const v = x.voice;
  return {
    ...(v?.source === "designed" && { design: v.design ?? "" }),
    ...(v?.clip && {
      clip: { take_id: v.clip.take_id, in: v.clip.in, out: v.clip.out },
      consent: true,
    }),
    ...(v?.language && { language: v.language }),
    ...(v?.style && { style: v.style }),
  };
}

/** A voice's clip, as studio checks it (a `media` param, contracts/README.md, Voices: cloned voices). */
export const CLIP: Param = {
  name: "clip",
  type: "media",
  default: null,
  accepts: ["audio"],
  trim: true,
  min_s: 3,
  max_s: 30,
} as Param;

/** Why a part of a take cannot be a voice's clip (the board's words for its length), null when it can. */
export function clipError(v: S<"MediaRef">, t: Facts | null): string | null {
  const err = mediaValueError(CLIP, v, t);
  // a part, or a whole take, of the wrong length (not "take <id> lasts")
  if (!err || !/^(the part|take \S+) lasts/.test(err)) return err;
  const len = err.startsWith("take") ? (t?.duration_s ?? 0) : (v.out ?? 0) - (v.in ?? 0);
  return `clip: the part lasts ${len.toFixed(1)} s; a voice needs 3 to 30 s`;
}

/** The composer tile's value ("Ryan · preset"), the inspector's ("Ryan · preset · English"), or "Pick a voice". */
export function voiceTileText(x: Item | undefined, long = false): string {
  if (!x) return "Pick a voice";
  const lang = long ? languageName(x.voice?.language, x.voice?.family ?? x.family) : "";
  return [x.name, x.group, lang].filter(Boolean).join(" · ");
}

/** The picker's preset groups: starter voices by family, in first-seen order (the list is by name). */
export function presetGroups(items: Item[]): { family: string; voices: Item[] }[] {
  const out: { family: string; voices: Item[] }[] = [];
  for (const x of items) {
    if (x.kind !== "voice" || x.group !== "preset") continue;
    const fam = x.voice?.family ?? x.family ?? "";
    const g = out.find((o) => o.family === fam);
    if (g) g.voices.push(x);
    else out.push({ family: fam, voices: [x] });
  }
  return out;
}

/** The picker's search: every word in the name or the voice's line. */
export function matches(x: Item, q: string): boolean {
  const hay = `${x.name} ${voiceLine(x)}`.toLowerCase();
  return q
    .toLowerCase()
    .split(/\s+/)
    .filter(Boolean)
    .every((w) => hay.includes(w));
}

// ---------------------------------------------------------------- dialogue scripts (API 1.32)

/** The node a script expands into has a rows param `lines` of exactly voice, text, style and pause_s. */
export function scriptTarget(params: Param[]): Param | null {
  const p = params.find((q) => q.name === "lines" && q.type === "rows");
  const it = (p?.item ?? {}) as unknown as Record<string, Param>;
  const typ = (k: string) => (typeof it[k] === "object" ? it[k].type : null);
  const ok =
    !!p &&
    voiceField(p) === "voice" &&
    Object.keys(it).sort().join() === "pause_s,style,text,voice" &&
    typ("text") === "string" &&
    typ("style") === "string" &&
    typ("pause_s") === "float";
  return ok ? p! : null;
}

const SCRIPT_LINE = /^@([A-Za-z][A-Za-z0-9_-]*)\s*(?:\(([^()]*)\))?\s*:(.*)$/;
const HANDLE = /^[a-z](?:[a-z0-9_-]{0,38}[a-z0-9])?$/;
export const SCRIPT_FORM = "write @name: text or @name (style): text";

export type ScriptLine =
  | { line: number; blank: true }
  | { line: number; error: string }
  | { line: number; handle: string; style: string; text: string };

/** check.py script_lines: each line (split on \n, a \r before it dropped), trimmed, numbered from 1: blank, the form's
 * error, or the handle (lower-cased), style and text. */
export function scriptLines(script: string): ScriptLine[] {
  return script
    .replace(/\r\n/g, "\n")
    .split("\n")
    .map((raw, i): ScriptLine => {
      const t = raw.trim();
      const line = i + 1;
      if (!t) return { line, blank: true };
      const m = SCRIPT_LINE.exec(t);
      if (!m || !HANDLE.test(m[1].toLowerCase())) return { line, error: SCRIPT_FORM };
      return { line, handle: m[1].toLowerCase(), style: (m[2] ?? "").trim(), text: m[3].trim() };
    });
}

type El = {
  id: string;
  handle: string | null;
  name: string;
  version: number;
  voice_id?: string | null;
};
export type Expanded =
  | { errors: string[] }
  | {
      inputs: Record<string, unknown>;
      typed: Record<string, unknown>;
      elements: { id: string; handle: string; name: string; version: number }[];
      voices: { id: string; name: string; version: number; source: string }[];
      parents: string[];
    };

const len = (s: string) => [...s].length;

/** check.py expand_script: studio's expansion of every script at create time (the Create screen runs it for the line
 * marks before studio answers). `nodes` hold the merged values; `library` the caller's elements, `voices` id → the
 * voices the caller can use now. */
export function expandScript(
  nodes: Record<string, Node & { params?: Record<string, unknown> }>,
  schemas: Schemas,
  library: El[],
  voices: Record<string, Item>,
): Expanded {
  const byHandle = new Map(library.map((e) => [e.handle, e]));
  const errs: string[] = [];
  const res = {
    inputs: {} as Record<string, unknown>,
    typed: {} as Record<string, unknown>,
    elements: [] as { id: string; handle: string; name: string; version: number }[],
    voices: [] as { id: string; name: string; version: number; source: string }[],
    parents: [] as string[],
  };
  for (const nid of Object.keys(nodes).sort((a, b) => (a < b ? -1 : a > b ? 1 : 0))) {
    const n = nodes[nid];
    const s = schemas[n.kind];
    const sp = s && !n.bypass ? s.params.find((p) => p.script) : undefined;
    if (!s || !sp) continue;
    const lp = scriptTarget(s.params)!;
    const vals: Record<string, unknown> = {
      ...Object.fromEntries(s.params.map((p) => [p.name, p.default])),
      ...(n.params ?? {}),
    };
    const script = String(vals[sp.name] ?? "");
    const given = (vals.lines ?? []) as unknown[];
    if (!script.trim()) {
      if (!given.length) errs.push(`${nid}.${sp.name}: the script is empty`);
      continue;
    }
    if (given.length) {
      errs.push(`${nid}.lines: give the script or the lines, not both`);
      continue;
    }
    const field = voiceParams({ [nid]: n }, schemas).find(([, q]) => q.name === "lines.voice")![1];
    const it = lp.item as unknown as Record<string, Param>;
    const pause = Number(vals.pause ?? it.pause_s.default);
    const rows: [{ voice: { voice_id: string }; text: string; style: string }, number][] = [];
    const mine: [El, Item][] = [];
    for (const ln of scriptLines(script)) {
      if ("blank" in ln) {
        if (rows.length) rows[rows.length - 1][1] += 1;
        continue;
      }
      const at = `${nid}.${sp.name} line ${ln.line}: `;
      if ("error" in ln) {
        errs.push(at + ln.error);
        continue;
      }
      const el = byHandle.get(ln.handle);
      const item = el?.voice_id ? voices[el.voice_id] : undefined;
      const why = item ? voiceReason(n.kind, field, item) : null;
      const e = !ln.text
        ? "nothing to say"
        : len(ln.text) > (it.text.max ?? Infinity)
          ? `at most ${it.text.max} characters (${len(ln.text)})`
          : len(ln.style) > (it.style.max ?? Infinity)
            ? `the style is at most ${it.style.max} characters (${len(ln.style)})`
            : !el
              ? `unknown element @${ln.handle}`
              : !item
                ? `@${ln.handle} has no voice`
                : why
                  ? `voice ${item.name} does not fit this workflow (${why})`
                  : null;
      if (e) {
        errs.push(at + e);
        continue;
      }
      rows.push([{ voice: { voice_id: item!.id }, text: ln.text, style: ln.style }, 0]);
      mine.push([el!, item!]);
    }
    if (errs.length) continue;
    if (!rows.length) {
      errs.push(`${nid}.${sp.name}: the script is empty`);
      continue;
    }
    if (rows.length > (lp.max ?? 40)) {
      errs.push(`${nid}.lines: at most ${lp.max} lines`);
      continue;
    }
    const top = Number(it.pause_s.max ?? Infinity);
    const lines = rows.map(([r, b], i) => ({
      ...r,
      pause_s: i === rows.length - 1 ? 0 : Math.min(top, Math.round(pause * (1 + b) * 1000) / 1000),
    }));
    res.inputs[`${nid}.${sp.name}`] = "";
    res.inputs[`${nid}.lines`] = lines;
    res.typed[`${nid}.${sp.name}`] = script;
    res.typed[`${nid}.lines`] = given;
    for (const [el, item] of mine) {
      if (!res.elements.some((x) => x.id === el.id))
        res.elements.push({ id: el.id, handle: el.handle!, name: el.name, version: el.version });
      if (!res.voices.some((x) => x.id === item.id))
        res.voices.push({
          id: item.id,
          name: item.name,
          version: item.version,
          source: item.group,
        });
      const clip = voiceSource(field, item) === "clip" ? item.voice?.clip?.take_id : undefined;
      if (clip && !res.parents.includes(clip)) res.parents.push(clip);
    }
  }
  return errs.length ? { errors: errs } : res;
}

/** Studio's 422 for scripts (`dialogue.script line 3: …`, joined by "; "): line → its message, and the rest. */
export function scriptErrors(detail: string): { lines: Record<number, string>; other: string[] } {
  const lines: Record<number, string> = {};
  const other: string[] = [];
  for (const part of detail.split("; ")) {
    const m = /^(?:row \d+: )?[\w-]+\.\w+ line (\d+): (.*)$/.exec(part);
    if (m) lines[Number(m[1])] ??= m[2];
    else other.push(part);
  }
  return { lines, other };
}
