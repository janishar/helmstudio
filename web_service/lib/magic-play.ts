// The board as a sequence for the Timeline's player (plan 39 §45.6; kept out of lib/magic.ts so that one has no runtime
// imports).
import { normalize, type Doc as SeqDoc, type Norm, type Takes as SeqTakes } from "@/lib/timeline";
import { end, inLane, shown, turnTakes, type Block, type Doc, type Takes } from "@/lib/magic";

/** A sound block's clips: its take over the block; a dialogue's turns one after another, a Loop's take again and
 * again, each its own length, the last cut at the block's end (API 1.89, as studio's Send). */
export function soundClips(b: Block, takes: Takes, fps: number) {
  const pieces = b.request.turns ? turnTakes(b, takes) : [shown(b, takes)];
  const loop = !!b.request.loop && !b.request.turns;
  const out: { id: string; take_id: string; in: number; out: number; at: number }[] = [];
  let at = b.at;
  const stop = end(b);
  for (let i = 0; i < pieces.length && at < stop; i++) {
    const t = pieces[i];
    if (!t) continue;
    // a take of unknown length fills the block, as before
    const n = Math.min(stop - at, t.duration ? Math.floor(t.duration * fps + 1e-6) : stop - at);
    if (n < 1) break;
    out.push({ id: out.length ? `${b.id}.${out.length}` : b.id, take_id: t.id, in: 0, out: n / fps, at: at / fps });
    at += n;
    if (loop && out.length < 100) i = -1; // ponytail: 100 clips a block, as studio's Send
  }
  return out;
}

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
      clips: inLane(d, l.id).flatMap((b) => soundClips(b, takes, fps)),
    }));
  const facts = Object.fromEntries(
    d.blocks.flatMap((b) => {
      const kind =
        b.kind === "video" ? "video" : b.kind === "frame" || b.kind === "image" ? "image" : "audio";
      const all = b.request.turns ? turnTakes(b, takes) : [shown(b, takes)];
      return all.flatMap((take) =>
        take
          ? [[take.id, { kind, duration_s: Math.max(s(b.length), take.duration ?? 0), has_audio: false }]]
          : [],
      );
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
