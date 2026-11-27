// The board as a sequence for the Timeline's player (plan 39 §45.6; kept out of lib/magic.ts so that one has no runtime
// imports).
import { normalize, type Doc as SeqDoc, type Norm, type Takes as SeqTakes } from "@/lib/timeline";
import { end, inLane, shown, type Doc, type Takes } from "@/lib/magic";

/** The board as a sequence for the Timeline's player (lib/timeline-preview.ts): the Shots lane on V1 (gaps as empty
 * slots, dissolves as they are), voice, music and sound lanes on audio tracks. Phase G's Send builds on it. */
export function toSequence(d: Doc, takes: Takes, dissolves = true) {
  const fps = d.target.fps;
  const s = (f: number) => f / fps;
  const shots = d.lanes.find((l) => l.role === "shots");
  const v1: Record<string, unknown>[] = [];
  let t = 0;
  for (const b of shots ? inLane(d, shots.id) : []) {
    if (b.at > t) v1.push({ id: `gap${t}`, take_id: null, hold: s(b.at - t) });
    const take = shown(b, takes);
    const tr =
      dissolves && d.transitions.find((x) => x.between?.[1] === b.id && x.kind === "dissolve");
    v1.push({
      id: b.id,
      take_id: take?.id ?? null,
      ...(take ? { in: 0, out: s(b.length) } : { hold: s(b.length) }),
      ...(tr ? { transition_in: { type: "dissolve", frames: tr.length } } : {}),
    });
    t = end(b);
  }
  const audio = d.lanes
    .filter((l) => ["voice", "music", "sound"].includes(l.role))
    .slice(0, 8)
    .map((l) => ({
      kind: "audio" as const,
      clips: inLane(d, l.id).flatMap((b) => {
        const take = shown(b, takes);
        return take ? [{ id: b.id, take_id: take.id, in: 0, out: s(b.length), at: s(b.at) }] : [];
      }),
    }));
  const facts = Object.fromEntries(
    d.blocks.flatMap((b) => {
      const take = shown(b, takes);
      if (!take) return [];
      const kind =
        b.kind === "video" ? "video" : b.kind === "frame" || b.kind === "image" ? "image" : "audio";
      return [[take.id, { kind, duration_s: s(b.length), has_audio: false }]];
    }),
  );
  return {
    doc: { target: d.target, tracks: [{ kind: "video" as const, clips: v1 }, ...audio] },
    facts,
  };
}

/** The board as the player's normalized sequence: with its dissolves when the takes have the handles for them, else
 * without (a dissolve needs media past each cut); null when it can't play at all. */
export function playable(d: Doc, takes: Takes): { doc: Norm; facts: SeqTakes } | null {
  for (const withDissolves of [true, false]) {
    const { doc, facts } = toSequence(d, takes, withDissolves);
    const n = normalize(doc as unknown as SeqDoc, facts as SeqTakes);
    if ("document" in n) return { doc: n.document, facts: facts as SeqTakes };
  }
  return null;
}
