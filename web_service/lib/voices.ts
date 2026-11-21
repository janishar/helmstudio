// Voices (contracts/README.md "Voices (API 1.30)"): the voice fit (check.py `voice_reason` / `voice_fit`, tested
// against contracts/fixtures/voices/fit.json), a voice's words in the picker and on its tiles, and the voice
// picker's groups.
import type { S } from "@/lib/api";

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

/** Why one voice param of a node of `kind` cannot take this voice, null when it can (studio's run 422 is
 * `<node>.<param>: <reason>`). */
export function voiceReason(kind: string, param: Param, item: Item): string | null {
  const src = SOURCE[item.group as keyof typeof SOURCE];
  if (!src) return null;
  if (!(param.accepts as string[] | undefined)?.includes(src)) return REFUSED[src];
  const family = kind.includes("/") ? kind.split("/")[0] : null;
  if (src === "preset" && item.voice?.family !== family)
    return `voice ${item.name} is for ${item.voice?.family}`;
  return null;
}

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

/** A BCP 47 tag's language in English ("en" → "English"); the tag itself when it is not known. */
export function languageName(tag?: string | null): string {
  if (!tag) return "";
  try {
    return new Intl.DisplayNames(["en"], { type: "language" }).of(tag) ?? tag;
  } catch {
    return tag;
  }
}

/** A voice card's line: "Dynamic male voice · English" for a preset (its style, then its language), the description
 * for a designed voice. */
export function voiceLine(x: Item): string {
  const v = x.voice;
  if (!v) return "";
  if (v.source === "designed") return v.design ?? "";
  return [v.style, languageName(v.language)].filter(Boolean).join(" · ");
}

/** The composer tile's value ("Ryan · preset"), the inspector's ("Ryan · preset · English"), or "Pick a voice". */
export function voiceTileText(x: Item | undefined, long = false): string {
  if (!x) return "Pick a voice";
  const lang = long ? languageName(x.voice?.language) : "";
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
