// @vitest-environment jsdom
// Send to Timeline (MagicBoard frame 9, plan 39 phase G): the dialog's rows from studio's plan, the sends list's
// right-click menu, a morph's items in its join's menu, its block over the join and its status.
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { closeMenu, type Item, type MenuItem } from "@/components/context-menu";
import { joinMenu, sendMenu, type Act } from "@/components/magic/menus";
import {
  MorphBlocks,
  goesRows,
  joinRows,
  morphStatus,
  morphTake,
  when,
  exportBoard,
  type SendApi,
  type Send,
  type SendPlan,
} from "@/components/magic/send";
import { api } from "@/lib/api";
import { joins, type Doc } from "@/lib/magic";
import { sampleBoard } from "@/lib/magic-fixture";

afterEach(() => {
  act(closeMenu);
  cleanup();
});

const labels = (items: Item[]) =>
  items.filter((x): x is MenuItem | "-" => !!x).map((x) => (x === "-" ? "-" : x.label));
const item = (items: Item[], label: string) =>
  items.find((x): x is MenuItem => !!x && x !== "-" && x.label === label)!;

const T1 = "11111111-1111-4111-8111-111111111111";
const T2 = "22222222-2222-4222-8222-222222222222";

/** the sample board with Shot 1 → Shot 2 a morph that has two takes */
function morphBoard(): Doc {
  const { doc } = sampleBoard();
  const shots = doc.lanes.find((l) => l.role === "shots")!;
  const j = joins(doc, shots.id).find((x) => x.between)!;
  return {
    ...doc,
    transitions: [
      ...doc.transitions.filter((t) => t.between?.join() !== j.between!.join()),
      { id: "tm", between: j.between, kind: "morph", length: 24, takes: [T1, T2] },
    ],
  };
}

function actOn(doc: Doc, over: Partial<Act> = {}) {
  const { takes } = sampleBoard();
  return {
    doc,
    takes,
    mode: "insert",
    playhead: 0,
    sel: [],
    edit: vi.fn(),
    run: vi.fn(),
    select: vi.fn(),
    open: vi.fn(),
    add: vi.fn(),
    play: vi.fn(),
    seek: vi.fn(),
    rename: vi.fn(),
    used: vi.fn(),
    paste: null,
    copyText: vi.fn(),
    openMorph: vi.fn(),
    ...over,
  } as Act & { run: ReturnType<typeof vi.fn>; openMorph: ReturnType<typeof vi.fn> };
}

const plan: SendPlan = {
  name: "The Last Tide · send 3",
  revision: 41,
  goes: [
    { lane: "shots", name: "Shots", kind: "video", blocks: 3, track: "V1", linked: 0 },
    { lane: "lines", name: "Mara's lines", kind: "voice", blocks: 2, track: "A2", linked: 2 },
    { lane: "music", name: "Music", kind: "music", blocks: 1, track: "A3", linked: 0 },
  ],
  joins: [
    { transition: "t1", kind: "fade_in", how: "baked" },
    { transition: "t2", kind: "dissolve", how: "kept" },
    { transition: "t3", kind: "wipe", how: "baked" },
    { transition: "t4", kind: "morph", how: "clip" },
  ],
  effects: [{ block: "s2", name: "Grade · warm", how: "baked" }],
  warnings: ["Shot 3 is out of date: sends take 2 as it is"],
  bakes: 3,
  bake_seconds: 20,
  host: "mac-studio",
};

describe("the send dialog's rows", () => {
  it("what goes: each lane on its track, the shots' picked takes, the lines linked", () => {
    expect(goesRows(plan)).toEqual([
      { kind: "video", text: "3 shots on V1", note: "picked takes" },
      { kind: "voice", text: "2 lines on A2", note: "linked to their shots" },
      { kind: "music", text: "1 cue on A3", note: "" },
    ]);
  });
  it("the joins by what happens to them, the board's cuts kept with the dissolves", () => {
    expect(joinRows(plan, 1)).toEqual([
      { how: "kept", names: "Dissolve, Cut", note: "kept as they are" },
      { how: "baked", names: "Fade in, Wipe", note: "baked into a short clip" },
      { how: "clip", names: "Morph", note: "its own clip" },
    ]);
  });
  it("when a send was made", () => {
    const now = new Date(2026, 10, 27, 12, 0);
    expect(when(new Date(2026, 10, 27, 11, 59, 30).toISOString(), now)).toBe("now");
    expect(when(new Date(2026, 10, 26, 9, 0).toISOString(), now)).toBe("yesterday");
  });
});

describe("a send's menu (frame 10)", () => {
  const send = (over: Partial<Send> = {}): Send => ({
    id: "s",
    name: "The Last Tide · send 3",
    revision: 41,
    status: "done",
    timeline_id: "c3a1f2e4-5b6d-4e7f-8a9b-0c1d2e3f4a5b",
    bakes: 0,
    baked: 0,
    failure: null,
    made_at: "2026-11-27T10:48:00Z",
    ...over,
  });
  it("Open in Timeline, Restore board to its revision, Remove from list", () => {
    const on = { open: vi.fn(), restore: vi.fn(), remove: vi.fn() };
    const m = sendMenu(on, send());
    expect(m.title).toBe("The Last Tide · send 3");
    expect(labels(m.items)).toEqual([
      "Open in Timeline",
      "Restore board to rev 41",
      "-",
      "Remove from list",
    ]);
    item(m.items, "Restore board to rev 41").onPick?.();
    expect(on.restore).toHaveBeenCalled();
  });
  it("Open is greyed while it bakes, once it failed, and once its sequence is deleted", () => {
    const on = { open: vi.fn(), restore: vi.fn(), remove: vi.fn() };
    expect(item(sendMenu(on, send({ status: "baking" })).items, "Open in Timeline").off).toBe(
      "Still baking",
    );
    expect(
      item(
        sendMenu(on, send({ status: "failed", failure: "a bake failed: boom" })).items,
        "Open in Timeline",
      ).off,
    ).toBe("a bake failed: boom");
    expect(item(sendMenu(on, send({ timeline_gone: true })).items, "Open in Timeline").off).toBe(
      "Its sequence was deleted",
    );
  });
});

describe("a morph", () => {
  it("its join's menu opens it and generates its take", () => {
    const d = morphBoard();
    const a = actOn(d);
    const shots = d.lanes.find((l) => l.role === "shots")!;
    const j = joins(d, shots.id).find((x) => x.kind === "morph")!;
    const m = joinMenu(a, j);
    expect(labels(m.items)).toContain("Open");
    expect(labels(m.items)).toContain("Generate again");
    item(m.items, "Open").onPick?.();
    expect(a.openMorph).toHaveBeenCalledWith("tm");
    item(m.items, "Generate again").onPick?.();
    expect(a.run).toHaveBeenCalledWith(["tm"]);
  });
  it("its status and shown take: the pinned one, else its latest", () => {
    const t = morphBoard().transitions.find((x) => x.id === "tm")!;
    expect(morphTake(t)).toBe(T2);
    expect(morphTake({ ...t, pinned: T1 })).toBe(T1);
    expect(morphStatus(t, {}).s).toBe("done");
    expect(
      morphStatus(
        { ...t, takes: [] },
        { "run:x": { id: "run:x", block: "tm", status: "running", progress: 40, inputs: "" } },
      ),
    ).toEqual({ s: "running", progress: 40 });
  });
  it("is drawn as its own block over its join; a click opens it", () => {
    const d = morphBoard();
    const a = actOn(d);
    const shots = d.lanes.find((l) => l.role === "shots")!;
    render(<MorphBlocks act={a} lane={shots} pf={10} />);
    fireEvent.click(screen.getByRole("button", { name: /^Morph 1 s, Done/ }));
    expect(a.openMorph).toHaveBeenCalledWith("tm");
  });
});

describe("Export (one step)", () => {
  it("sends with studio's name, waits out the bakes, exports the plan's revision and follows the job", async () => {
    const base = { id: "s1", name: "Board · send 1", revision: 3, bakes: 1, failure: null };
    const sent: (string | undefined)[] = [];
    const sends = {
      key: "b1",
      send: async (name?: string) => (
        sent.push(name),
        { ...base, status: "baking", baked: 0, timeline_id: null }
      ),
      list: async () => [{ ...base, status: "done", baked: 1, timeline_id: T1 }],
    } as unknown as SendApi;
    const reply = (data: unknown) =>
      Promise.resolve({ data, error: undefined, response: new Response(null, { status: 200 }) });
    const posts: [string, unknown][] = [];
    vi.spyOn(api, "POST").mockImplementation(((path: string, init: { body: unknown }) => {
      posts.push([path, init.body]);
      return path.endsWith("/plan")
        ? reply({ revision: 7, blocked: [] })
        : reply({ id: "j1", status: "queued" });
    }) as never);
    vi.spyOn(api, "GET").mockImplementation((() =>
      reply({ id: "j1", status: "succeeded", take_id: T2 })) as never);
    const lines: string[] = [];
    const { send, job } = await exportBoard(sends, (l) => lines.push(l));
    expect(sent).toEqual([undefined]);
    expect(send.timeline_id).toBe(T1);
    expect(posts).toEqual([
      ["/v1/studio/timelines/{timeline_id}/export/plan", {}],
      ["/v1/studio/timelines/{timeline_id}/export", { revision: 7 }],
    ]);
    expect(job.status).toBe("succeeded");
    expect(lines).toEqual(["sending", "baking 0/1", "planning", "queued"]);
    vi.restoreAllMocks();
  }, 10_000);
});
