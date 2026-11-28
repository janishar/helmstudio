// @vitest-environment jsdom
// The Assistant on the Magic page (plan 39 phase F, MagicBoard frame 8; contracts/README.md "Magic: the Assistant"):
// its change drawn from the board_edit card (new, moved, changed, removed) with Apply and Discard answering the card
// and edits held meanwhile; the bar's and the blocks' menu; History's words and menu; the screen's actions and summary;
// navigate to /magic with a board.
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { sampleBoard } from "@/lib/magic-fixture";
import type { Op } from "@/lib/magic";
import type { S } from "@/lib/api";
import { reset, seedSample } from "./magic-api";

vi.mock("@/components/magic/magic.css", () => ({}));
// the page on the sample board in a mock of studio (./magic-api.ts)
vi.mock("@/lib/api", async (orig) => ({
  ...(await orig<typeof import("@/lib/api")>()),
  api: (await import("./magic-api")).api,
}));
vi.mock("@/lib/runs", async (orig) => ({
  ...(await orig<typeof import("@/lib/runs")>()),
  followSession: (await import("./magic-api")).followSession,
}));
vi.mock("@/lib/library", async (orig) => ({
  ...(await orig<typeof import("@/lib/library")>()),
  useVoices: () => [],
}));

const { MagicBoard } = await import("@/components/magic/board");
const { byOf, magicTools, proposalOf } = await import("@/components/magic/assistant");
const { historyMenu } = await import("@/components/magic/menus");
const { Registry, ToolsContext } = await import("@/components/assistant/tools");
const { closeMenu } = await import("@/components/context-menu");
const { routeUrl } = await import("@/lib/assistant");

/** the page on the sample board, loaded, in the Assistant's registry */
async function openBoard(reg: InstanceType<typeof Registry>) {
  seedSample();
  render(
    <ToolsContext value={reg}>
      <MagicBoard />
    </ToolsContext>,
  );
  await screen.findByRole("toolbar", { name: "Board tools" });
  await act(async () => {}); // the kinds and the plan
}

beforeAll(() => {
  globalThis.ResizeObserver ??= class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((q: string) => ({
    matches: false,
    media: q,
    addEventListener() {},
    removeEventListener() {},
  })) as unknown as typeof matchMedia;
  HTMLDialogElement.prototype.showModal ??= function (this: HTMLDialogElement) {
    this.open = true;
  };
  HTMLDialogElement.prototype.close ??= function (this: HTMLDialogElement) {
    this.open = false;
  };
  HTMLElement.prototype.hidePopover ??= () => undefined;
  document.elementsFromPoint ??= () => [];
});
beforeEach(() => {
  localStorage.clear();
  reset();
});
afterEach(() => {
  act(closeMenu);
  cleanup();
});

const doc = sampleBoard().doc;
const shots = doc.lanes.find((l) => l.role === "shots")!.id;
const last = Math.max(...doc.blocks.filter((b) => b.lane === shots).map((b) => b.at + b.length));
const ADD: Op = {
  op: "add",
  mode: "insert",
  block: {
    id: "zz",
    lane: shots,
    at: last,
    length: 48,
    kind: "video",
    name: "Shot 9",
    request: { kind: "video" },
    takes: [],
    effects: [],
  },
};

describe("the change drawn on the board", () => {
  it("marks what it adds, moves and changes, and keeps what it removes", () => {
    const [a, b] = doc.blocks.filter((x) => x.lane === shots);
    const ops: Op[] = [
      ADD,
      { op: "block", id: a.id, set: { note: "wetter" } },
      { op: "delete", ids: [b.id], mode: "insert" },
    ];
    const p = proposalOf(doc, ops);
    if (!("doc" in p)) throw new Error(p.error);
    expect(p.marks.zz).toBe("new");
    expect(p.marks[a.id]).toBe("changed");
    expect(p.removed.map((x) => x.id)).toEqual([b.id]);
    // insert closed the gap: what came after the deleted shot moved
    expect(Object.values(p.marks)).toContain("moved");
    expect(proposalOf(doc, [{ op: "lane_delete", id: "gone" }])).toEqual({
      error: "It no longer fits this board",
    });
  });

  it("is drawn from the Assistant's card; Apply and Discard answer it; edits wait meanwhile", async () => {
    const reg = new Registry();
    reg.setPath("/magic");
    const answer = vi.fn();
    await openBoard(reg);
    const id = reg.current()!.spec.ids!.board;
    const { revision } = (reg.current()!.spec.summary() as { board: { revision: number } }).board;
    const card: S<"AssistantCard"> = {
      id: "0b6f3a3e-1d2c-4b5a-9e8f-7a6b5c4d3e2f:1:3",
      kind: "board_edit",
      title: "Assistant's change",
      rows: [
        { label: "Change", value: "Added Shot 9" },
        { label: "To run", value: "about 6 min · mac-studio" },
      ],
      tags: ["changes the board"],
      state: "waiting",
      board: { board_id: id, base_revision: revision, ops: [ADD] as S<"MagicOp">[] },
    };
    act(() => reg.setServer([card], "t:1:working", answer));
    const added = screen.getByRole("button", { name: /Video block Shot 9/ });
    expect(within(added).getByText("New")).toBeTruthy();
    expect(added.className).toContain("outline-dashed");
    const bar = screen.getByRole("group", { name: "Assistant's change" });
    expect(bar.textContent).toContain("Added Shot 9");
    expect(bar.textContent).toContain("≈ 6 min to run · mac-studio");
    // its menu, on the bar and on a block it draws
    fireEvent.contextMenu(added);
    expect(screen.getByRole("menuitem", { name: /Apply/ })).toBeTruthy();
    act(closeMenu);
    // an edit waits for the answer
    const first = doc.blocks.find((b) => b.lane === shots)!;
    act(
      () =>
        void reg.current()!.spec.actions.select({ ids: [first.id] }, { progress: () => undefined }),
    );
    fireEvent.keyDown(window, { key: "Delete" });
    expect(screen.getByText("Apply or Discard the Assistant's change first.")).toBeTruthy();
    fireEvent.click(within(bar).getByRole("button", { name: "Apply" }));
    expect(answer).toHaveBeenCalledWith(card.id, true);
    fireEvent.click(within(bar).getByRole("button", { name: "Discard" }));
    expect(answer).toHaveBeenCalledWith(card.id, false);
    // answered: the card is gone, so is the change
    act(() => reg.setServer([], "t:2:idle", answer));
    expect(screen.queryByRole("button", { name: /Video block Shot 9/ })).toBeNull();
    expect(screen.queryByRole("group", { name: "Assistant's change" })).toBeNull();
  });

  it("is not drawn on another board", async () => {
    const reg = new Registry();
    reg.setPath("/magic");
    await openBoard(reg);
    act(() =>
      reg.setServer(
        [
          {
            id: "0b6f3a3e-1d2c-4b5a-9e8f-7a6b5c4d3e2f:1:4",
            kind: "board_edit",
            title: "Assistant's change",
            rows: [],
            tags: ["changes the board"],
            state: "waiting",
            board: { board_id: "another", base_revision: 1, ops: [ADD] as S<"MagicOp">[] },
          },
        ],
        "k",
        () => undefined,
      ),
    );
    expect(screen.queryByRole("group", { name: "Assistant's change" })).toBeNull();
  });
});

describe("History", () => {
  it("says who made each revision and restores any but the board now", () => {
    expect(byOf({ by: "user", via: "assistant" })).toEqual({ text: "by Assistant", ai: true });
    expect(byOf({ by: "user", via: "web" }).text).toBe("by you");
    expect(byOf({ by: "studio", via: null }).text).toBe("by studio");
    const rev = (n: number) =>
      ({
        revision: n,
        by: "user",
        via: "web",
        saved_at: "",
        ops_summary: "move",
        blocks: 1,
      }) as S<"MagicRevisionItem">;
    const restore = vi.fn();
    const now = historyMenu({ restore }, rev(41), 41);
    expect(now.items[0]).toMatchObject({ label: "Restore rev 41", off: "It's the board now" });
    const before = historyMenu({ restore }, rev(40), 41);
    (before.items[0] as { onPick: () => void }).onPick();
    expect(restore).toHaveBeenCalledWith(rev(40));
  });
});

describe("the screen's actions", () => {
  const calls: string[] = [];
  const spec = magicTools({
    id: "b1",
    name: "Rain",
    revision: 7,
    doc,
    sel: [],
    frame: 48,
    select: (ids) => calls.push(`select ${ids}`),
    open: (id) => (calls.push(`open ${id}`), doc.blocks.some((b) => b.id === id)),
    play: (f) => calls.push(`play ${f}`),
    pause: () => calls.push("pause"),
    seek: (f) => calls.push(`seek ${f}`),
  });
  const run = (name: string, args: Record<string, unknown>) =>
    spec.actions[name](args, { progress: () => undefined }) as { status: string; text: string };

  it("selects, opens, plays and scrubs with the page's own entry points", () => {
    const b = doc.blocks[0];
    expect(run("select", { ids: [b.id] }).text).toBe(`Selected ${b.name.split(" · ")[0]}`);
    expect(run("select", { ids: ["nope"] }).status).toBe("failed");
    expect(run("openBlock", { id: b.id }).status).toBe("done");
    expect(run("openBlock", { id: "nope" }).status).toBe("failed");
    expect(run("scrub", { at_s: 2 }).text).toBe("Playhead at 2 s");
    expect(run("play", { at_s: 1 }).text).toBe("Playing from 1 s");
    expect(run("play", { pause: true }).text).toBe("Paused");
    expect(calls).toEqual([
      `select ${b.id}`,
      `open ${b.id}`,
      "open nope",
      "seek 48",
      "play 24",
      "pause",
    ]);
  });

  it("sums the board up for the Assistant", () => {
    const sum = spec.summary() as {
      board: { revision: number };
      lanes: unknown[];
      playhead_s: number;
    };
    expect(sum.board.revision).toBe(7);
    expect(sum.lanes).toHaveLength(doc.lanes.length);
    expect(sum.playhead_s).toBe(2);
    expect(spec.screen).toBe("magic");
  });

  it("navigate opens /magic with a board", () => {
    expect(routeUrl({ to: "/magic", query: { b: "b1" } }, [])).toEqual({
      url: "/magic?b=b1",
      path: "/magic",
    });
    expect(routeUrl({ to: "/magic", query: { t: "x" } }, [])).toEqual({
      error: "/magic takes no t",
    });
  });
});
