// The board as a sequence for the Timeline's player (plan 39 §45.6; kept out of lib/magic.ts so that one has no runtime
// imports).
import { normalize, type Doc as SeqDoc, type Norm, type Takes as SeqTakes } from "@/lib/timeline";
import {
  BOARD_DUCKING,
  end,
  inLane,
  shown,
  turnTakes,
  type Block,
  type Doc,
  type Takes,
} from "@/lib/magic";

/** API 1.106: a block's sound fades as played and sent: its own, a lane's start or end transition on its first or last
 * block (the longer), and the crossfade into the next block of its lane (any kind of join between two sound blocks). */
export function blockFades(d: Doc, b: Block) {
  const fps = d.target.fps;
  const bs = inLane(d, b.lane);
  const i = bs.indexOf(b);
  const sound = (x?: Block) =>
    !!x && (x.kind === "voice" || x.kind === "music" || x.kind === "sound");
  const lane = (k: "start" | "end") => d.transitions.find((t) => t[k] === b.lane)?.length ?? 0;
  let fade_in = b.fade_in ?? 0;
  let fade_out = b.fade_out ?? 0;
  if (i === 0) fade_in = Math.max(fade_in, lane("start") / fps);
  if (i === bs.length - 1) fade_out = Math.max(fade_out, lane("end") / fps);
  const next = bs[i + 1];
  const j =
    next && sound(b) && sound(next) && end(b) === next.at
      ? d.transitions.find((t) => t.between?.[0] === b.id && t.between?.[1] === next.id)
      : undefined;
  return { fade_in, fade_out, crossfade: j ? j.length / fps : 0 };
}

/** API 1.106: the crossfade a sound block plays into the next block of its lane (frames), and whether it blends (its
 * shown take runs at least the block plus the crossfade, one frame's slack) or dips; null without one. */
export function soundJoin(
  d: Doc,
  b: Block,
  takes: Takes,
): { frames: number; mode: "blend" | "dip" } | null {
  const fps = d.target.fps;
  const n = Math.round(blockFades(d, b).crossfade * fps);
  if (!n) return null;
  const t = shown(b, takes);
  const blend = t?.duration !== undefined && t.duration + 1 / fps >= (b.length + n) / fps;
  return { frames: n, mode: blend ? "blend" : "dip" };
}

/** API 1.106: Duck music's voice spans on the board (s): each voice block's time, a blend's tail in. */
export function voiceSpans(d: Doc, takes: Takes): [number, number][] {
  const fps = d.target.fps;
  return d.blocks
    .filter((b) => b.kind === "voice")
    .map((b) => {
      const x = soundJoin(d, b, takes);
      return [b.at / fps, (end(b) + (x?.mode === "blend" ? x.frames : 0)) / fps];
    });
}

/** A sound block's clips: its take over the block; a dialogue's turns one after another, a Loop's take again and
 * again, each its own length, the last cut at the block's end (API 1.89, as studio's Send). */
export function soundClips(b: Block, takes: Takes, fps: number, d?: Doc) {
  const pieces = b.request.turns ? turnTakes(b, takes, d) : [shown(b, takes)];
  // a sound made from a shot's video follows the shot: no Loop (API 1.90)
  const loop = !!b.request.loop && !b.request.turns && !b.request.from;
  const out: {
    id: string;
    take_id: string;
    in: number;
    out: number;
    at: number;
    gain_db?: number;
    fade_in?: number;
    fade_out?: number;
    crossfade?: number;
  }[] = [];
  // the block's level is each clip's gain, as studio's Send (API 1.101)
  const gain = b.gain_db ? { gain_db: b.gain_db } : {};
  let at = b.at;
  const stop = end(b);
  for (let i = 0; i < pieces.length && at < stop; i++) {
    const t = pieces[i];
    if (!t) continue;
    // a take of unknown length fills the block, as before
    const n = Math.min(stop - at, t.duration ? Math.floor(t.duration * fps + 1e-6) : stop - at);
    if (n < 1) break;
    out.push({
      id: out.length ? `${b.id}.${out.length}` : b.id,
      take_id: t.id,
      in: 0,
      out: n / fps,
      at: at / fps,
      ...gain,
    });
    at += n;
    if (loop && out.length < 100) i = -1; // ponytail: 100 clips a block, as studio's Send
  }
  // API 1.106: the fades on its first and last clip, the crossfade on its last
  if (d && out.length) {
    const f = blockFades(d, b);
    if (f.fade_in) out[0].fade_in = f.fade_in;
    if (f.fade_out) out.at(-1)!.fade_out = f.fade_out;
    if (f.crossfade) out.at(-1)!.crossfade = f.crossfade;
  }
  return out;
}

/** The blocks the picture plays, in time: the Shots lane's, and a Storyboard frame wherever no shot covers it (the
 * rough cut of a board with few or no shots yet). */
export function pictures(d: Doc): Block[] {
  const lane = (r: string) => {
    const l = d.lanes.find((x) => x.role === r);
    return l ? inLane(d, l.id) : [];
  };
  const shots = lane("shots");
  const frames = lane("storyboard").filter(
    (f) => !shots.some((s) => s.at < end(f) && f.at < end(s)),
  );
  return [...shots, ...frames].sort((a, b) => a.at - b.at);
}

/** The board as a sequence for the Timeline's player (lib/timeline-preview.ts): the Shots lane on V1 (gaps as empty
 * slots, dissolves as they are), voice, music and sound lanes on audio tracks. Phase G's Send builds on it. */
export function toSequence(d: Doc, takes: Takes, dissolves = true) {
  const fps = d.target.fps;
  const s = (f: number) => f / fps;
  const v1: Record<string, unknown>[] = [];
  let t = 0;
  for (const b of pictures(d)) {
    if (b.at > t) v1.push({ id: `gap${t}`, take_id: null, hold: s(b.at - t) });
    const take = shown(b, takes);
    const tr =
      dissolves && d.transitions.find((x) => x.between?.[1] === b.id && x.kind === "dissolve");
    v1.push({
      id: b.id,
      take_id: take?.id ?? null,
      // a still (a frame, an image) holds; a video plays in to out
      ...(take && b.kind === "video" ? { in: 0, out: s(b.length) } : { hold: s(b.length) }),
      ...(tr ? { transition_in: { type: "dissolve", frames: tr.length } } : {}),
    });
    t = end(b);
  }
  const audio = d.lanes
    .filter((l) => ["voice", "music", "sound"].includes(l.role))
    .slice(0, 8)
    .map((l) => ({
      kind: "audio" as const,
      clips: inLane(d, l.id).flatMap((b) => soundClips(b, takes, fps, d)),
    }));
  const facts = Object.fromEntries(
    d.blocks.flatMap((b) => {
      const kind =
        b.kind === "video" ? "video" : b.kind === "frame" || b.kind === "image" ? "image" : "audio";
      const all = b.request.turns ? turnTakes(b, takes, d) : [shown(b, takes)];
      return all.flatMap((take) =>
        take
          ? [
              [
                take.id,
                {
                  kind,
                  duration_s: Math.max(s(b.length), take.duration ?? 0),
                  has_audio: false,
                  // API 1.106: what ducking reads (a dialogue is voice)
                  ...(kind === "audio" ? { sound: b.kind } : {}),
                },
              ],
            ]
          : [],
      );
    }),
  );
  return {
    doc: {
      target: d.target,
      tracks: [{ kind: "video" as const, clips: v1 }, ...audio],
      ducking: d.ducking ?? BOARD_DUCKING,
    },
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
