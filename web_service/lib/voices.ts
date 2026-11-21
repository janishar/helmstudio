// Voices (contracts/README.md "Voices (API 1.30)", "Cloned voices (API 1.31)"): the voice fit (check.py
// `voice_sources` / `voice_reason` / `voice_fit`, tested against contracts/fixtures/voices/fit.json), a voice's words in
// the picker and on its tiles, the voice picker's groups, and a clip's limits (3–30 s of a take with sound).
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

/** Every voice param of a known, not bypassed node: node id order (code points), then schema order; its `accepts`
 * narrowed to the node's `only` (API 1.30). */
export function voiceParams(nodes: Record<string, Node>, schemas: Schemas): [string, Param][] {
  return Object.keys(nodes)
    .sort((a, b) => (a < b ? -1 : a > b ? 1 : 0))
    .flatMap((id) => {
      const s = schemas[nodes[id].kind];
      return s && !nodes[id].bypass
        ? s.params
            .filter((p) => p.type === "voice")
            .map((p): [string, Param] => [id, narrowed(p, nodes[id].only)])
        : [];
    });
}

/** A voice param with its `accepts` narrowed to a workflow node's `only` for it (README, Media inputs: only). */
export const narrowed = (p: Param, only?: Record<string, string[]>): Param =>
  only?.[p.name]
    ? {
        ...p,
        accepts: (p.accepts ?? []).filter((a) => only[p.name].includes(a)) as Param["accepts"],
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

/** Seconds as "0:04" (lib/runs' clock; not imported: lib/workflow reads this module). */
const clock = (s: number) => {
  const t = Math.max(0, Math.round(s));
  return `${Math.floor(t / 60)}:${String(t % 60).padStart(2, "0")}`;
};

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
