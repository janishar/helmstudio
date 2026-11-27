// An answer's pick-one block and its refs (contracts/README.md, Assistant: pick one and refs; alpha.2 items 17, 19):
// pipelines checked both, the drawer shows cards and chips and never the raw block or an id.
import type { Entry } from "@/lib/assistant";

export type Option = { title: string; line?: string; best?: boolean };
export type RefKind = "take" | "element" | "voice" | "workflow";
export type RefPart = { t: string } | { kind: RefKind; id: string };

const BLOCK = /```[ \t]*options[ \t]*\n([\s\S]*?)\n?[ \t]*```/i;
const REF = /\[\[(take|element|voice|workflow):([^\]\s|]{1,128})\]\]/;

/** The answer's text without its options block, and the options; a block that does not parse stays in the text. */
export function splitOptions(text: string): { text: string; options: Option[] | null } {
  const m = BLOCK.exec(text);
  if (!m) return { text, options: null };
  try {
    const o: unknown = JSON.parse(m[1]);
    if (
      Array.isArray(o) &&
      o.length >= 2 &&
      o.length <= 4 &&
      o.every((x) => typeof x?.title === "string" && x.title)
    )
      return {
        text: (text.slice(0, m.index) + text.slice(m.index + m[0].length)).trim(),
        options: o as Option[],
      };
  } catch {
    // shown as text
  }
  return { text, options: null };
}

const waits = (e: Entry) => e.kind === "card" && e.card?.state === "waiting";

/** An answer's options as it stands: live while no message came after it; then the title that message picked, if
 * it was one of them (it fades the rest). */
export function pickOf(entries: Entry[], id: string, options: Option[]) {
  const after = entries.slice(entries.findIndex((e) => e.id === id) + 1);
  const next = after.find((e) => e.kind === "user");
  // a card after them waits on its own Apply / Yes: a pick now would send a message, and that cancels the card
  if (!next && after.some(waits)) return { live: false, picked: null, card: true };
  if (!next) return { live: true, picked: null };
  const said = next.text?.trim();
  return { live: false, picked: options.find((o) => o.title === said)?.title ?? null };
}

/** The newest answer's options while nothing was sent after it (the keys 1–4 pick them), else null. */
export function liveOptions(entries: Entry[]): Option[] | null {
  for (let i = entries.length - 1; i >= 0; i--) {
    const e = entries[i];
    if (e.kind === "user" || waits(e)) return null;
    const o = e.kind === "assistant" ? splitOptions(e.text ?? "").options : null;
    if (o) return o;
  }
  return null;
}

/** Text and refs, in order: `[[take:<id>]]` is a ref; an id of `?` is one the model was never given. */
export function refParts(text: string): RefPart[] {
  const s = text.split(new RegExp(REF.source, "g"));
  const out: RefPart[] = [];
  for (let i = 0; i < s.length; i += 3) {
    if (s[i]) out.push({ t: s[i] });
    if (i + 2 < s.length) out.push({ kind: s[i + 1] as RefKind, id: s[i + 2] });
  }
  return out;
}

/** The label each chip showed, by "kind:id" (Chip sets it), so Copy and the screen reader say titles, never ids. */
export const refTitles = new Map<string, string>();

/** Text with its refs as the titles their chips showed ("a take" before one rendered). */
export const refText = (text: string) =>
  text.replace(
    new RegExp(REF.source, "g"),
    (_, k: string, id: string) =>
      refTitles.get(`${k}:${id}`) ?? (id === "?" ? `unknown ${k}` : `a ${k}`),
  );

/** The `@word` the caret is at the end of (item 7: the @ picker opens on it), or null. */
export function atQuery(text: string, caret: number): { start: number; q: string } | null {
  const m = /(^|\s)@([^\s@]{0,40})$/.exec(text.slice(0, caret));
  return m ? { start: caret - m[2].length - 1, q: m[2] } : null;
}

/** The box's text as sent: each picked take's `[title]` becomes its ref `[[take:<id>]]` (the box never shows an id);
 * one the user edited away stays text. */
export function withPicks(text: string, picks: { label: string; id: string }[]): string {
  return picks.reduce((t, p) => t.replace(`[${p.label}]`, `[[take:${p.id}]]`), text);
}
