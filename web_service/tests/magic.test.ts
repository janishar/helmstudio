// Magic's board model (plan 39 §45.12, phase B): move, snap, insert or overwrite, ties moving with their shot and the
// "doesn't fit" warning, joins and transitions, status from fake takes, undo that never takes a take back.
import { describe, expect, it } from "vitest";
import {
  apply,
  commit,
  end,
  everywhere,
  history,
  inLane,
  inputsOf,
  joins,
  overruns,
  redo,
  snap,
  sectionsOf,
  snapGroup,
  statusOf,
  turnBlock,
  turnStatuses,
  turnTakes,
  undo,
  type Block,
  type Doc,
  type Op,
} from "@/lib/magic";
import { emptyDoc, sampleBoard } from "@/lib/magic-fixture";
import { playable, soundClips, toSequence } from "@/lib/magic-play";

const S = 24; // frames a second
const ok = (d: Doc, op: Op) => {
  const r = apply(d, op);
  if ("error" in r) throw new Error(r.error);
  return r.doc;
};
const at = (d: Doc, id: string) => d.blocks.find((b) => b.id === id)!;
const span = (d: Doc, id: string) => [at(d, id).at / S, end(at(d, id)) / S];

/** Two lanes: Shots (A 0–8, B 8–16, C 16–24) and B-roll (empty); a line tied to B. */
function board(): Doc {
  const d = emptyDoc();
  d.lanes = [
    { id: "shots", name: "Shots", role: "shots" },
    { id: "broll", name: "B-roll", role: "free" },
    { id: "lines", name: "Lines", role: "voice" },
  ];
  const b = (
    id: string,
    lane: string,
    from: number,
    len: number,
    kind: Block["kind"] = "video",
  ): Block => ({
    id,
    lane,
    at: from * S,
    length: len * S,
    kind,
    name: id,
    request: { kind },
    takes: [],
    effects: [],
  });
  d.blocks = [
    b("A", "shots", 0, 8),
    b("B", "shots", 8, 8),
    b("C", "shots", 16, 8),
    b("L", "lines", 9, 6, "voice"),
  ];
  d.links = [{ block: "L", to: ["B"] }];
  return d;
}

describe("move", () => {
  it("Insert: a shot moved to another lane closes its gap; its tied line comes too (frame 4a)", () => {
    const d = ok(board(), { op: "move", ids: ["B"], dt: 2 * S, lane: "broll", mode: "insert" });
    expect(span(d, "B")).toEqual([10, 18]);
    expect(at(d, "B").lane).toBe("broll");
    expect(span(d, "C")).toEqual([8, 16]); // closed the gap
    expect(span(d, "L")).toEqual([11, 17]); // moved with B by +2 s
  });

  it("Overwrite: the gap stays and the block covers what is under it (trimmed, removed or split)", () => {
    const d = ok(board(), { op: "move", ids: ["A"], dt: 12 * S, mode: "overwrite" });
    expect(span(d, "A")).toEqual([12, 20]);
    expect(span(d, "B")).toEqual([8, 12]); // its end trimmed
    expect(span(d, "C")).toEqual([20, 24]); // its start trimmed
    const split = ok(board(), {
      op: "add",
      mode: "overwrite",
      block: {
        lane: "shots",
        at: 10 * S,
        length: 2 * S,
        kind: "video",
        name: "N",
        request: { kind: "video" },
        takes: [],
        effects: [],
      },
    });
    const shots = inLane(split, "shots").map((b) => [b.name, b.at / S, end(b) / S]);
    expect(shots).toEqual([
      ["A", 0, 8],
      ["B", 8, 10],
      ["N", 10, 12],
      ["B", 12, 16],
      ["C", 16, 24],
    ]);
  });

  it("Insert on a lane pushes what is after the point along, and a point inside a block goes to its end", () => {
    const d = ok(board(), {
      op: "add",
      mode: "insert",
      block: {
        lane: "shots",
        at: 16 * S,
        length: 8 * S,
        kind: "video",
        name: "N",
        request: { kind: "video" },
        takes: [],
        effects: [],
      },
    });
    expect(span(d, "C")).toEqual([24, 32]);
    const inside = ok(board(), {
      op: "add",
      mode: "insert",
      block: {
        lane: "shots",
        at: 3 * S,
        length: 4 * S,
        kind: "video",
        name: "N",
        request: { kind: "video" },
        takes: [],
        effects: [],
      },
    });
    const n = inside.blocks.find((b) => b.name === "N")!;
    expect([n.at / S, end(n) / S]).toEqual([8, 12]);
    expect(span(inside, "B")).toEqual([12, 20]);
    // the line tied to B moves with it
    expect(span(inside, "L")).toEqual([13, 19]);
  });

  it("a small nudge in Insert keeps the order; dropped at the next block's end, they swap", () => {
    expect(
      inLane(ok(board(), { op: "move", ids: ["B"], dt: 2 * S, mode: "insert" }), "shots").map(
        (b) => b.id,
      ),
    ).toEqual(["A", "B", "C"]);
    const swapped = ok(board(), { op: "move", ids: ["B"], dt: 16 * S, mode: "insert" });
    expect(inLane(swapped, "shots").map((b) => b.id)).toEqual(["A", "C", "B"]);
    expect(span(swapped, "B")).toEqual([16, 24]);
    // into empty space after everything: where it was dropped (a lone block moves too)
    expect(span(ok(board(), { op: "move", ids: ["C"], dt: 2 * S, mode: "insert" }), "C")).toEqual([
      18, 26,
    ]);
    expect(span(ok(board(), { op: "move", ids: ["L"], dt: 4 * S, mode: "insert" }), "L")).toEqual([
      13, 19,
    ]);
  });

  it("several blocks move together; from two lanes to one is refused", () => {
    const d = ok(board(), { op: "move", ids: ["A", "B"], dt: 4 * S, mode: "overwrite" });
    expect([span(d, "A"), span(d, "B")]).toEqual([
      [4, 12],
      [12, 20],
    ]);
    expect(
      apply(board(), { op: "move", ids: ["A", "L"], dt: 0, lane: "broll", mode: "insert" }),
    ).toEqual({
      error: "Move blocks from one lane at a time.",
    });
  });
});

describe("review fixes", () => {
  it("Insert on a tied line's own lane pushes it along; its tie does not pull it back over the new block", () => {
    const d = ok(board(), {
      op: "add",
      mode: "insert",
      block: {
        lane: "lines",
        at: 9 * S,
        length: 2 * S,
        kind: "voice",
        name: "N",
        request: { kind: "voice" },
        takes: [],
        effects: [],
      },
    });
    expect(span(d, "L")).toEqual([11, 17]);
  });

  it("split keeps the join after the block on its second part", () => {
    const withT = ok(board(), { op: "transition", between: ["B", "C"], kind: "dissolve" });
    const d = ok(withT, { op: "split", id: "B", at: 12 * S });
    expect(joins(d, "shots").map((j) => j.kind)).toEqual(["cut", "cut", "dissolve"]);
  });
});

describe("snap", () => {
  it("to block edges and the playhead within the tolerance, else a whole second on the ruler", () => {
    const d = board();
    expect(snap(d, 8 * S + 3, 6, 100)).toEqual({ t: 8 * S, kind: "edge" });
    expect(snap(d, 101, 6, 100)).toEqual({ t: 100, kind: "playhead" });
    expect(snap(d, 30 * S + 2, 6, 0)).toEqual({ t: 30 * S, kind: "ruler" });
    expect(snap(d, 30 * S + 10, 6, 0)).toBeNull();
    // the moving block's own edges are not targets: the whole second under it is
    expect(snap(d, 8 * S + 3, 6, 999, new Set(["A", "B"]))).toEqual({ t: 8 * S, kind: "ruler" });
  });

  it("a group snaps by whichever end is nearer", () => {
    const d = board();
    // B (8 s long) dragged so its end lands 2 frames before C's end (24 s)
    expect(snapGroup(d, 16 * S - 2, 8 * S, 6, 999, new Set(["B"])).at).toBe(16 * S);
  });
});

describe("ties", () => {
  it("shortening a shot leaves its tied line running past it: the warning says by how much (frame 5b)", () => {
    const d = ok(board(), { op: "trim", id: "B", length: 4 * S });
    expect(overruns(d)).toEqual([{ block: "L", shot: "B", by: 3 * S }]);
    expect(overruns(ok(d, { op: "trim", id: "L", length: 3 * S }))).toEqual([]);
    expect(overruns(ok(d, { op: "unlink", block: "L" }))).toEqual([]);
  });

  it("music tied to a run of shots stretches with the run, a line just moves", () => {
    const d = board();
    d.blocks.push({
      ...at(d, "L"),
      id: "M",
      lane: "broll",
      at: 0,
      length: 24 * S,
      kind: "music",
      name: "M",
    });
    d.links.push({ block: "M", to: ["A", "B", "C"] });
    const t = ok(d, { op: "trim", id: "C", length: 4 * S });
    expect(span(t, "M")).toEqual([0, 20]);
  });

  it("deleting the shot drops the tie; a tie to itself or nothing is refused", () => {
    expect(ok(board(), { op: "delete", ids: ["B"], mode: "insert" }).links).toEqual([]);
    expect("error" in apply(board(), { op: "link", block: "L", to: [] })).toBe(true);
  });
});

describe("joins", () => {
  it("blocks that meet are a cut until a transition is set; Remove makes a cut again", () => {
    const d = board();
    expect(joins(d, "shots").map((j) => [j.between!.join(">"), j.kind])).toEqual([
      ["A>B", "cut"],
      ["B>C", "cut"],
    ]);
    const dis = ok(d, { op: "transition", between: ["A", "B"], kind: "dissolve" });
    expect(joins(dis, "shots")[0]).toMatchObject({ kind: "dissolve", length: 12 });
    const fade = ok(dis, { op: "transition", start: "shots", kind: "fade" });
    expect(joins(fade, "shots")[0]).toMatchObject({ start: "shots", kind: "fade", length: 24 });
    expect(ok(dis, { op: "transition", between: ["A", "B"], kind: "cut" }).transitions).toEqual([]);
    // moving B away from A drops their dissolve
    expect(
      ok(dis, { op: "move", ids: ["B"], dt: 0, lane: "broll", mode: "overwrite" }).transitions,
    ).toEqual([]);
  });
});

describe("status and undo", () => {
  it("the sample board: Shot 2 running, Shot 3 out of date after Shot 2 changed, Line 2 a draft", () => {
    const { doc, takes } = sampleBoard();
    const st = (id: string) => statusOf(doc, at(doc, id), takes).s;
    expect([st("s1"), st("s2"), st("s3"), st("v1"), st("v2"), st("f1")]).toEqual([
      "done",
      "running",
      "old",
      "done",
      "draft",
      "done",
    ]);
    // a request change makes a done block out of date
    const d = ok(doc, {
      op: "block",
      id: "s1",
      set: { request: { ...at(doc, "s1").request, prompt: "new" } },
    });
    expect(statusOf(d, at(d, "s1"), takes).s).toBe("old");
  });

  it("undo and redo step through edits; a take added by a run stays through undo", () => {
    let h = history(board());
    h = commit(h, ok(h.now, { op: "delete", ids: ["C"], mode: "insert" }));
    h = everywhere(h, (d) => ({
      ...d,
      blocks: d.blocks.map((b) => (b.id === "A" ? { ...b, takes: ["t1"] } : b)),
    }));
    h = undo(h);
    expect(h.now.blocks.map((b) => b.id)).toContain("C");
    expect(at(h.now, "A").takes).toEqual(["t1"]);
    h = redo(h);
    expect(h.now.blocks.map((b) => b.id)).not.toContain("C");
    expect(undo(history(board())).past).toEqual([]);
  });

  it("the board as a sequence plays in the Timeline's player: shots on V1, gaps as slots, lines on audio", () => {
    const { doc, takes } = sampleBoard();
    // the fake takes have no handles for the dissolve, so the player gets it as a cut
    const p = playable(doc, takes)!;
    expect(p.doc.tracks[0].clips.map((c) => c.id)).toEqual(["s1", "s2", "s3"]);
    expect(p.doc.tracks.length).toBe(4);
    expect(toSequence(doc, takes).doc.tracks[0].clips[1]).toMatchObject({
      transition_in: { type: "dissolve", frames: 12 },
    });
  });

  it("with no shots yet, the Storyboard frames play as the rough cut", () => {
    const { doc, takes } = sampleBoard();
    const shots = doc.lanes.find((l) => l.role === "shots")!.id;
    const noShots = { ...doc, blocks: doc.blocks.filter((b) => b.lane !== shots) };
    const story = doc.lanes.find((l) => l.role === "storyboard")!.id;
    const frames = noShots.blocks.filter((b) => b.lane === story).map((b) => b.id);
    expect(frames.length).toBeGreaterThan(0);
    const ids = toSequence(noShots, takes).doc.tracks[0].clips.map((c) => c.id);
    expect(ids.filter((id) => !String(id).startsWith("gap"))).toEqual(expect.arrayContaining(frames));
    expect(playable(noShots, takes)).not.toBeNull();
  });
});

// API 1.89 (plan 39 gaps): a dialogue's turns, each a line block with its own status; its takes play one after
// another, a Loop's take again and again; a song's sections from its take, else its lyrics' tags.
describe("dialogue, Loop and sections", () => {
  const dialogue = (): Block => ({
    id: "dl",
    lane: "lines",
    at: 24,
    length: 4 * S,
    kind: "voice",
    name: "Dialogue 1",
    request: {
      kind: "voice",
      turns: [
        { speaker: "a1", text: "Hi." },
        { speaker: "a2", text: "Yo." },
      ],
    },
    takes: [],
    effects: [],
  });
  it("a turn is its line block; the dialogue's status is its turns'", () => {
    const d = board();
    const b = dialogue();
    d.blocks.push(b);
    expect(turnBlock(b, 1).request).toEqual({ kind: "voice", line: "Yo.", voice: "a2" });
    expect(statusOf(d, b, {}).s).toBe("draft");
    const t0 = { id: "t0", block: "dl", status: "done" as const, turn: 0, inputs: "" };
    t0.inputs = inputsOf(d, turnBlock(b, 0), {});
    b.takes = ["t0"];
    expect(turnStatuses(d, b, { t0 }).map((s) => s.s)).toEqual(["done", "draft"]);
    expect(statusOf(d, b, { t0 }).s).toBe("old"); // a line with no take yet
    const t1 = { ...t0, id: "t1", turn: 1, inputs: inputsOf(d, turnBlock(b, 1), {}) };
    b.takes = ["t0", "t1"];
    expect(statusOf(d, b, { t0, t1 }).s).toBe("done");
    b.request.turns![1].text = "Yo, later.";
    expect(turnStatuses(d, b, { t0, t1 }).map((s) => s.s)).toEqual(["done", "old"]);
    // API 1.90: a moved line keeps its take (made from its inputs now), wherever it went
    b.request.turns![1].text = "Yo.";
    b.request.turns!.reverse();
    expect(turnTakes(b, { t0, t1 }, d).map((t) => t?.id)).toEqual(["t1", "t0"]);
    expect(statusOf(d, b, { t0, t1 }).s).toBe("done");
    b.request.turns!.reverse();
    // Run on and Loop are not inputs
    expect(inputsOf(d, { ...b, request: { ...b.request, host: "gcp", loop: true } }, {})).toBe(
      inputsOf(d, b, {}),
    );
  });
  it("turns play one after another, a Loop's take repeats, each cut at the block's end", () => {
    const b = dialogue();
    const takes = {
      t0: { id: "t0", block: "dl", status: "done" as const, turn: 0, inputs: "", duration: 1.5 },
      t1: { id: "t1", block: "dl", status: "done" as const, turn: 1, inputs: "", duration: 5 },
    };
    b.takes = ["t0", "t1"];
    expect(soundClips(b, takes, S).map((c) => [c.take_id, c.at, c.out])).toEqual([
      ["t0", 1, 1.5],
      ["t1", 2.5, 2.5],
    ]);
    const rain: Block = { ...b, request: { kind: "sound", loop: true }, kind: "sound", takes: ["t0"] };
    expect(soundClips(rain, takes, S).map((c) => c.out)).toEqual([1.5, 1.5, 1]);
    // API 1.90: no Loop on a sound made from a shot's video
    expect(soundClips({ ...rain, request: { ...rain.request, from: "s1" } }, takes, S)).toHaveLength(1);
    // API 1.101: the block's level is each clip's gain, so Play sounds as Send
    expect(soundClips({ ...rain, gain_db: -6 }, takes, S).map((c) => c.gain_db)).toEqual([-6, -6, -6]);
  });
  it("a song's sections: its take's, else its lyrics' tags by their lines", () => {
    const b = { ...dialogue(), kind: "music" as const, request: { kind: "music" as const, lyrics: "[verse]\na\nb\nc\n[chorus]\nd" } };
    expect(sectionsOf(b, undefined, 8).map((c) => [c.label, c.start_s, c.end_s])).toEqual([
      ["verse", 0, 6],
      ["chorus", 6, 8],
    ]);
    const take = { id: "m", block: "dl", status: "done" as const, inputs: "", sections: [{ label: "intro", start_s: 0, end_s: 3 }] };
    expect(sectionsOf(b, take, 8)).toEqual(take.sections);
  });
});
