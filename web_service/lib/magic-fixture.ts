// Phase B's fake takes (plan 39 §45.11): the sample board of MagicBoard frame 1 ("The Last Tide"), its takes as
// pictures drawn in CSS, and the GPU-time guess per kind. Phase C replaces all of this with studio's boards, takes and
// estimate (§45.10); nothing here is sent anywhere.
import { inputsOf, type Block, type Doc, type Kind, type TakeInfo, type Takes } from "@/lib/magic";

const FPS = 24;
const s = (x: number) => Math.round(x * FPS);

/** Seconds a run of each kind takes, as the board's sample shows (≈ 1:50 a shot, 12 s a line, …). */
export const EST_S: Record<Kind, number> = {
  frame: 6,
  image: 6,
  video: 110,
  voice: 12,
  music: 40,
  sound: 25,
};
export const EST_HOST = "mac-studio";

/** "≈ 1 min 50 s", "≈ 6 min", "≈ 12 s"; `short`: "≈ 1:50". */
export function estText(sec: number, short = false): string {
  const r = Math.round(sec);
  if (r < 60) return `≈ ${r} s`;
  const m = Math.floor(r / 60);
  const x = r % 60;
  if (short && m < 5) return x ? `≈ ${m}:${String(x).padStart(2, "0")}` : `≈ ${m} min`;
  return x && m < 5 ? `≈ ${m} min ${x} s` : `≈ ${Math.round(r / 60)} min`;
}

/** The art a fake take of a block shows: a picture made of gradients, one per block name. */
export function artFor(name: string, kind: Kind): string {
  let h = 0;
  for (const c of name) h = (h * 31 + c.charCodeAt(0)) % 360;
  if (kind === "music") return `linear-gradient(150deg,#3b4f7a,#6a4c93 55%,#1f2340)`;
  return [
    `radial-gradient(40% 60% at 30% 40%, hsl(${(h + 40) % 360} 70% 62% / .85), transparent 70%)`,
    `radial-gradient(30% 40% at 72% 30%, hsl(${(h + 200) % 360} 80% 70% / .7), transparent 70%)`,
    `linear-gradient(160deg, hsl(${h} 40% 22%), hsl(${(h + 60) % 360} 45% 10%))`,
  ].join(",");
}

/** A new board: one Shots lane, nothing on it (MagicBoard frame 11, "A new board"). */
export function emptyDoc(): Doc {
  return {
    target: { width: 1280, height: 720, fps: FPS, sample_rate: 48000 },
    lanes: [],
    blocks: [],
    links: [],
    transitions: [],
    assets: [],
    notes: [],
  };
}

/** MagicBoard frame 1 as a document with fake takes: three frames, three shots (Shot 2 running, Shot 3 out of date
 * after Shot 2 changed), two lines (Line 2 a draft), the music tied to the three shots, two sounds, an empty lane. */
export function sampleBoard(): { name: string; doc: Doc; takes: Takes } {
  const doc: Doc = {
    ...emptyDoc(),
    lanes: [
      { id: "story", name: "Storyboard", role: "storyboard" },
      { id: "shots", name: "Shots", role: "shots" },
      { id: "lines", name: "Mara's lines", role: "voice" },
      { id: "music", name: "Music", role: "music" },
      { id: "sound", name: "Sound", role: "sound" },
      { id: "l7", name: "Lane 7", role: "free" },
    ],
    assets: [
      {
        id: "mara",
        kind: "character",
        handle: "mara",
        name: "Mara",
        sheet: ["mara-1", "mara-2", "mara-3", "mara-4", "mara-5", "mara-6"],
        voice_id: "warm",
      },
      {
        id: "umbrella",
        kind: "prop",
        handle: "red umbrella",
        name: "Red umbrella",
        sheet: ["umb-1", "umb-2"],
      },
      {
        id: "stop",
        kind: "place",
        handle: "bus stop",
        name: "Bus stop",
        sheet: ["stop-1", "stop-2", "stop-3"],
      },
    ],
    notes: [],
  };
  const block = (
    b: Partial<Block> & Pick<Block, "id" | "lane" | "at" | "length" | "kind" | "name">,
  ) => {
    const x: Block = { request: { kind: b.kind, refs: [] }, takes: [], effects: [], ...b };
    doc.blocks.push(x);
    return x;
  };
  const takes: Takes = {};
  let seed = 41;
  /** a take of `b`, made from the board as it is now */
  const take = (b: Block, over: Partial<TakeInfo> = {}) => {
    const id = `${b.id}-t${b.takes.length + 1}`;
    takes[id] = {
      id,
      block: b.id,
      status: "done",
      inputs: inputsOf(doc, b, takes),
      seed: ++seed,
      art: artFor(b.name, b.kind),
      ...over,
    };
    b.takes.push(id);
    return id;
  };
  const mara = { asset: "mara" };
  const stop = { asset: "stop" };
  const frames = ["waits, rain", "the umbrella", "the puddle"].map((what, i) =>
    block({
      id: `f${i + 1}`,
      lane: "story",
      at: s(8 * i),
      length: s(8),
      kind: "frame",
      name: `Frame ${i + 1}`,
      request: {
        kind: "frame",
        model: "Qwen Image 2.1 Turbo",
        style: "sketch",
        prompt: what,
        refs: i === 1 ? [mara, { asset: "umbrella" }] : [mara],
      },
    }),
  );
  frames.forEach((f) => take(f));
  const s1 = block({
    id: "s1",
    lane: "shots",
    at: 0,
    length: s(8),
    kind: "video",
    name: "Shot 1 · bus stop",
    from_frame: "f1",
    request: {
      kind: "video",
      model: "LTX 2.5",
      prompt: "@mara waits under the shelter, rain on the glass",
      refs: [mara, stop],
    },
  });
  s1.pinned = take(s1);
  const s2 = block({
    id: "s2",
    lane: "shots",
    at: s(8),
    length: s(8),
    kind: "video",
    name: "Shot 2 · umbrella",
    effects: [{ name: "Grade · warm", params: { temperature: 12, contrast: 4 } }],
    request: {
      kind: "video",
      model: "LTX 2.5",
      prompt: "@mara opens a @red umbrella as the bus pulls away, rain on the glass",
      refs: [mara, { block: "s1", part: "last" }, stop, { asset: "umbrella" }],
    },
  });
  take(s2);
  const s3 = block({
    id: "s3",
    lane: "shots",
    at: s(16),
    length: s(8),
    kind: "video",
    name: "Shot 3 · puddle",
    request: {
      kind: "video",
      model: "LTX 2.5",
      prompt: "@mara steps into a puddle, the umbrella tilts, the bus lights fade behind her",
      refs: [{ block: "s2", part: "last" }],
    },
  });
  take(s3);
  // Shot 2 changed after Shot 3 was made from its last frame: a new take, and a third one running
  take(s2);
  take(s2, { status: "running", progress: 62 });
  const l1 = block({
    id: "v1",
    lane: "lines",
    at: s(0.6),
    length: s(6.5),
    kind: "voice",
    name: "Line 1",
    request: {
      kind: "voice",
      model: "Qwen3-TTS",
      line: "Every night, the same bus.",
      voice: "mara",
    },
  });
  take(l1);
  block({
    id: "v2",
    lane: "lines",
    at: s(9),
    length: s(7),
    kind: "voice",
    name: "Line 2",
    request: { kind: "voice", model: "Qwen3-TTS", line: "Tonight I let it go.", voice: "mara" },
  });
  const m1 = block({
    id: "m1",
    lane: "music",
    at: 0,
    length: s(24),
    kind: "music",
    name: "Soft rain theme",
    request: {
      kind: "music",
      model: "ACE-Step 1.5 XL",
      prompt: "quiet piano, soft rain, slow, hopeful at the end",
    },
  });
  take(m1);
  const rain = block({
    id: "x1",
    lane: "sound",
    at: 0,
    length: s(9),
    kind: "sound",
    name: "Rain on a shelter",
    request: {
      kind: "sound",
      model: "Stable Audio 3 Small-SFX",
      prompt: "rain on a bus shelter",
      refs: [stop],
    },
  });
  take(rain);
  block({
    id: "x2",
    lane: "sound",
    at: s(9),
    length: s(4),
    kind: "sound",
    name: "Bus pulls away",
    request: { kind: "sound", model: "MMAudio", from: "s2" },
  });
  doc.links = [
    { block: "v1", to: ["s1"] },
    { block: "v2", to: ["s2"] },
    { block: "m1", to: ["s1", "s2", "s3"] },
  ];
  doc.transitions = [
    { id: "t1", start: "shots", kind: "fade", length: s(1) },
    { id: "t2", between: ["s1", "s2"], kind: "dissolve", length: s(0.5) },
  ];
  return { name: "The Last Tide", doc, takes };
}
