// Skills in the Assistant drawer (docs/mock/Assistant.dc.html frame 16; contracts/README.md "Skills", API 1.64): the
// Using skill line with the files read after it, the guide's first lines, and the @ picker's matches.
import type { S } from "@/lib/api";

type Line = S<"AssistantLine">;

/** "Using skill: Animator" → "Animator". */
export const skillTitle = (l: Line) => l.text.replace(/^Using skill:\s*/, "");

/** A turn's lines split: each Using skill line with the files read from that skill after it (done "Read <file> ·
 * <title>" lines, shown in its box), and the other lines. */
export function skillLines(lines: Line[]) {
  const used: { line: Line; reads: string[] }[] = [];
  const rest: Line[] = [];
  for (const l of lines) {
    if (l.skill) {
      used.push({ line: l, reads: [] });
      continue;
    }
    const m = l.status === "done" ? /^Read (.+) · (.+)$/.exec(l.text) : null;
    const to = m && used.findLast((u) => skillTitle(u.line) === m[2]);
    if (to) to.reads.push(m[1]);
    else rest.push(l);
  }
  return { used, rest };
}

/** The guide's first n paragraphs and bullets (headings left out, wrapped lines joined, ** and ` dropped). */
export function guideLines(text: string, n = 4) {
  const out: { li: boolean; text: string }[] = [];
  let open = false;
  for (const raw of text.split("\n")) {
    const t = raw.trim().replace(/\*\*|`/g, "");
    if (!t || t.startsWith("#")) {
      open = false;
      continue;
    }
    const li = /^[-*] /.test(t);
    if (open && !li) {
      out[out.length - 1].text += ` ${t}`;
      continue;
    }
    if (out.length === n) break;
    out.push({ li, text: li ? t.slice(2) : t });
    open = true;
  }
  return out;
}

/** The skills the @ picker lists for what was typed after the @ (lower-cased): name or title contains it. */
export const skillsMatching = (skills: S<"SkillSummary">[], q: string) =>
  skills.filter((s) => s.name.includes(q) || s.title.toLowerCase().includes(q));
