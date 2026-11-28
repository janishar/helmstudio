// Audio fades, crossfades and ducking (plan 41, contracts/README.md "Audio fades, crossfades and ducking"): the gain
// math the player applies (frameAt's fade), normalize's clamps and drops, the ops, and Magic's board as a sequence.
import { describe, expect, it } from "vitest";
import {
  apply,
  duckEnvelope,
  duckGain,
  frameAt,
  normalize,
  type Doc,
  type Norm,
  type Takes,
} from "@/lib/timeline";
import { apply as magicApply, statusOf, xfadeMode } from "@/lib/magic";
import { sampleBoard } from "@/lib/magic-fixture";
import { blockFades, soundJoin, toSequence, voiceSpans } from "@/lib/magic-play";

const target = { width: 1280, height: 720, fps: 24, sample_rate: 48000 };
const takes: Takes = {
  m: { kind: "audio", duration_s: 20, has_audio: true, sound: "music" },
  short: { kind: "audio", duration_s: 4, has_audio: true, sound: "music" },
  v: { kind: "audio", duration_s: 2, has_audio: true, sound: "voice" },
  img: { kind: "image" },
};
const norm = (d: Doc) => {
  const n = normalize(d, takes);
  if (!("document" in n)) throw new Error(JSON.stringify(n.problems));
  return n.document;
};
const seq = (a2: Doc["tracks"][number]["clips"], extra: Partial<Doc> = {}): Norm =>
  norm({
    target,
    tracks: [
      { kind: "video", clips: [{ id: "g", take_id: null, hold: 10 }] },
      { kind: "audio", clips: a2 },
    ],
    ...extra,
  });
const fade = (d: Norm, f: number, clip: string) =>
  frameAt(d, f, takes).sounds.find((s) => s.clip === clip)?.fade;

describe("clip fades", () => {
  const d = seq([{ id: "a", take_id: "m", in: 0, out: 4, at: 0, fade_in: 1, fade_out: 2 }]);
  it("rise from silence and fall to it, linear in amplitude", () => {
    expect(fade(d, 0, "a")).toBe(0);
    expect(fade(d, 12, "a")).toBe(0.5);
    expect(fade(d, 24, "a")).toBe(1);
    expect(fade(d, 48, "a")).toBe(1);
    expect(fade(d, 72, "a")).toBe(24 / 48);
    expect(fade(d, 95, "a")).toBe(1 / 48);
  });
  it("normalize snaps and clamps them (fade_out ≤ the length − fade_in) and drops 0", () => {
    const n = seq([
      { id: "a", take_id: "m", in: 0, out: 4, at: 0, fade_in: 3, fade_out: 9, gain_db: 0 },
    ]);
    expect(n.tracks[1].clips[0]).toMatchObject({ fade_in: 3, fade_out: 1 });
    const z = seq([{ id: "a", take_id: "m", in: 0, out: 4, at: 0, fade_in: 0 }]);
    expect("fade_in" in z.tracks[1].clips[0]).toBe(false);
  });
  it("an image with a fade is fade_clip; a crossfade on V1 is crossfade_track", () => {
    const r = normalize(
      {
        target,
        tracks: [
          {
            kind: "video",
            clips: [{ id: "i", take_id: "img", hold: 3, fade_in: 1, crossfade: 1 }],
          },
        ],
      },
      takes,
    );
    expect(r).toEqual({
      problems: [
        ["crossfade_track", "V1", "i"],
        ["fade_clip", "V1", "i"],
      ],
    });
  });
  it("set's 0 or null removes a fade", () => {
    const r = apply(d, { op: "set", clip: "a", fade_in: 0, fade_out: null }, takes);
    expect("document" in r && r.document.tracks[1].clips[0]).not.toHaveProperty("fade_in");
    expect("document" in r && r.document.tracks[1].clips[0]).not.toHaveProperty("fade_out");
  });
});

describe("crossfade into next", () => {
  const two = (first: string) =>
    seq([
      { id: "a", take_id: first, in: 0, out: 3, at: 0, crossfade: 2, fade_out: 1 },
      { id: "b", take_id: "m", in: 0, out: 3, at: 3, fade_in: 1 },
    ]);
  it("a blend: the outgoing plays on past its end while the next fades in over the same span", () => {
    const d = two("m");
    expect(d.tracks[1].clips[0].crossfade).toBe(2);
    // its own fade out and the next one's fade in do not play
    expect(fade(d, 71, "a")).toBe(1);
    expect(fade(d, 72, "a")).toBe(1);
    expect(fade(d, 96, "a")).toBe(0.5);
    expect(fade(d, 96, "b")).toBe(0.5);
    expect(frameAt(d, 96, takes).sounds.find((s) => s.clip === "a")?.t).toBe(4);
    expect(fade(d, 120, "a")).toBeUndefined();
  });
  it("a dip: no sound past the cut, each side fades over half", () => {
    const d = seq([
      { id: "a", take_id: "short", in: 1, out: 4, at: 0, crossfade: 2 },
      { id: "b", take_id: "m", in: 0, out: 3, at: 3 },
    ]);
    expect(fade(d, 48, "a")).toBe(1);
    expect(fade(d, 60, "a")).toBe(0.5);
    expect(fade(d, 72, "a")).toBeUndefined();
    expect(fade(d, 72, "b")).toBe(0);
    expect(fade(d, 84, "b")).toBe(0.5);
    expect(fade(d, 96, "b")).toBe(1);
  });
  it("is dropped when no clip starts on its end, and clamped to both lengths", () => {
    const d = seq([{ id: "a", take_id: "m", in: 0, out: 3, at: 0, crossfade: 2 }]);
    expect(d.tracks[1].clips[0]).not.toHaveProperty("crossfade");
    const c = seq([
      { id: "a", take_id: "m", in: 0, out: 3, at: 0, crossfade: 9 },
      { id: "b", take_id: "m", in: 0, out: 1, at: 3 },
    ]);
    expect(c.tracks[1].clips[0].crossfade).toBe(1);
  });
});

describe("ducking", () => {
  it("the envelope eases over 0.3 s each side of a voice span", () => {
    const spans: [number, number][] = [[2, 4]];
    expect(duckEnvelope(1, spans)).toBe(0);
    expect(duckEnvelope(1.85, spans)).toBeCloseTo(0.5, 9);
    expect(duckEnvelope(3, spans)).toBe(1);
    expect(duckEnvelope(4.15, spans)).toBeCloseTo(0.5, 9);
    expect(duckGain(3, spans, -12)).toBeCloseTo(10 ** -0.6, 12);
    expect(duckGain(0, spans, -12)).toBe(1);
  });
  const d = (on: boolean) =>
    norm({
      target,
      tracks: [
        { kind: "video", clips: [{ id: "g", take_id: null, hold: 10 }] },
        { kind: "audio", clips: [{ id: "mu", take_id: "m", in: 0, out: 8, at: 0 }] },
        { kind: "audio", clips: [{ id: "vo", take_id: "v", in: 0, out: 2, at: 2 }] },
      ],
      ducking: { on, db: -12 },
    });
  it("music dips under a voice clip when on; the voice does not", () => {
    expect(fade(d(true), 72, "mu")).toBeCloseTo(10 ** -0.6, 12);
    expect(fade(d(true), 0, "mu")).toBe(1);
    expect(fade(d(true), 72, "vo")).toBe(1);
    expect(fade(d(false), 72, "mu")).toBe(1);
  });
  it("the op keeps absent fields; a sequence without it is off", () => {
    const r = apply(d(false), { op: "ducking", db: -6 }, takes);
    expect("document" in r && r.document.ducking).toEqual({ on: false, db: -6 });
    const s = seq([]);
    const r2 = apply(s, { op: "ducking", on: true }, takes);
    expect("document" in r2 && r2.document.ducking).toEqual({ on: true, db: -12 });
  });
});

describe("Magic: fades, crossfades and ducking on the board", () => {
  it("a crossfade joins two music or sound blocks only; a new one is 2 s", () => {
    const { doc } = sampleBoard();
    const ok = magicApply(doc, { op: "transition", between: ["x1", "x2"], kind: "crossfade" });
    expect("doc" in ok && ok.doc.transitions.at(-1)).toMatchObject({
      kind: "crossfade",
      length: 48,
    });
    const bad = magicApply(doc, { op: "transition", between: ["s1", "s2"], kind: "crossfade" });
    expect(bad).toEqual({ error: "A crossfade joins two music or sound blocks." });
  });
  it("block fades: only on sound blocks; 0 or null removes", () => {
    const { doc } = sampleBoard();
    const r = magicApply(doc, { op: "block", id: "m1", set: { fade_in: 1.5 } });
    expect("doc" in r && r.doc.blocks.find((b) => b.id === "m1")?.fade_in).toBe(1.5);
    const back = "doc" in r && magicApply(r.doc, { op: "block", id: "m1", set: { fade_in: null } });
    expect(back && "doc" in back && back.doc.blocks.find((b) => b.id === "m1")).not.toHaveProperty(
      "fade_in",
    );
    expect(magicApply(doc, { op: "block", id: "s1", set: { fade_in: 1 } })).toHaveProperty("error");
  });
  it("the ducking op; a board without it is on at −12", () => {
    const { doc } = sampleBoard();
    const r = magicApply(doc, { op: "ducking", on: false });
    expect("doc" in r && r.doc.ducking).toEqual({ on: false, db: -12 });
  });
  it("a short take dips, a long one blends; a lane's start fade is its first block's", () => {
    const { doc, takes: mt } = sampleBoard();
    const r = magicApply(doc, { op: "transition", between: ["x1", "x2"], kind: "crossfade" });
    if (!("doc" in r)) throw new Error(r.error);
    const x1 = r.doc.blocks.find((b) => b.id === "x1")!;
    const t = mt[x1.takes[0]];
    t.duration = 9;
    expect(xfadeMode(r.doc, x1, mt)).toBe("dip");
    expect(soundJoin(r.doc, x1, mt)).toEqual({ frames: 48, mode: "dip" });
    t.duration = 11;
    expect(xfadeMode(r.doc, x1, mt)).toBe("blend");
    // made with a tail and now too short for a longer crossfade: out of date
    t.tail = 2;
    const longer = magicApply(r.doc, {
      op: "transition",
      between: ["x1", "x2"],
      kind: "crossfade",
      length: 96,
    });
    if (!("doc" in longer)) throw new Error(longer.error);
    expect(statusOf(longer.doc, x1, mt).s).toBe("old");
    const lane = {
      ...r.doc,
      transitions: [
        ...r.doc.transitions,
        { id: "e", start: "sound", kind: "fade" as const, length: 24 },
      ],
    };
    expect(blockFades(lane, x1)).toEqual({ fade_in: 1, fade_out: 0, crossfade: 2 });
  });
  it("the board as a sequence carries fades, the crossfade, ducking and the takes' classes", () => {
    const { doc, takes: mt } = sampleBoard();
    doc.blocks.find((b) => b.id === "m1")!.fade_out = 3;
    const { doc: s, facts } = toSequence(doc, mt);
    expect(s.ducking).toEqual({ on: true, db: -12 });
    const clips = s.tracks.slice(1).flatMap((t) => t.clips);
    expect(clips.find((c) => c.id === "m1")).toMatchObject({ fade_out: 3 });
    const m1 = doc.blocks.find((b) => b.id === "m1")!;
    expect(facts[m1.takes[0]]).toMatchObject({ sound: "music" });
    const v1 = doc.blocks.find((b) => b.id === "v1")!;
    expect(voiceSpans(doc, mt)[0]).toEqual([v1.at / 24, (v1.at + v1.length) / 24]);
  });
});
