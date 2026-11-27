// @vitest-environment jsdom
// The Magic page on studio's boards (plan 39 phase C), through a mock of the API that applies ops with lib/magic.ts
// as studio does: a first visit makes a board, edits go to …/edits with the revision they were made on (one after
// another), Undo is studio's, a refusal says why and reads the board again, snapshots are studio's, runs are off.
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { apply, type Doc } from "@/lib/magic";
import { emptyDoc } from "@/lib/magic-fixture";

vi.mock("@/components/magic/magic.css", () => ({}));

type Wire = { id: string; name: string; revision: number; doc: Doc; history: Doc[] };
let boards: Wire[] = [];
let calls: { method: string; path: string; body?: unknown }[] = [];
let refuseNext = 0;
const res = (status: number, data?: unknown) => ({
  data: status < 300 ? data : undefined,
  error: status >= 300 ? data : undefined,
  response: new Response(null, { status: status === 204 ? 204 : status }),
});
const answer = (b: Wire) => ({
  id: b.id,
  name: b.name,
  revision: b.revision,
  doc: b.doc,
  can_undo: b.history.length > 1,
  can_redo: false,
  created_at: "2026-11-27T05:00:00Z",
  updated_at: "2026-11-27T05:00:00Z",
});
const find = (o?: { params?: { path?: { board_id?: string } } }) =>
  boards.find((b) => b.id === o?.params?.path?.board_id)!;
const write = (b: Wire, doc: Doc) => {
  b.history.push(doc);
  b.doc = doc;
  b.revision++;
};

vi.mock("@/lib/api", async (orig) => ({
  ...(await orig<typeof import("@/lib/api")>()),
  api: {
    GET: async (path: string, o?: { params?: { path?: { board_id?: string } } }) => {
      calls.push({ method: "GET", path });
      if (path === "/v1/studio/magic/boards")
        return res(200, { boards: boards.map((b) => ({ id: b.id, name: b.name })) });
      if (path.endsWith("/snapshots")) return res(200, { snapshots: [] });
      return res(200, answer(find(o)));
    },
    POST: async (path: string, o: { body?: Record<string, unknown>; params?: never }) => {
      calls.push({ method: "POST", path, body: o.body });
      if (path === "/v1/studio/magic/boards") {
        const b = {
          id: `b${boards.length + 1}`,
          name: "Untitled board",
          revision: 1,
          doc: emptyDoc(),
        };
        boards.push({ ...b, history: [b.doc] });
        return res(201, answer(boards.at(-1)!));
      }
      const b = find(o);
      if (path.endsWith("/edits")) {
        const body = o.body as { base_revision: number; ops: never[] };
        if (refuseNext && !--refuseNext)
          return res(409, {
            detail: `board changed since you opened it (now revision ${b.revision})`,
          });
        if (body.base_revision !== b.revision) return res(409, { detail: "stale" });
        let d = b.doc;
        for (const [i, op] of body.ops.entries()) {
          const r = apply(d, op);
          if ("error" in r) return res(422, { detail: r.error, op_index: i });
          d = r.doc;
        }
        write(b, d);
        return res(200, { revision: b.revision, board: answer(b) });
      }
      if (path.endsWith("/undo")) {
        write(b, b.history.at(-2)!);
        return res(200, answer(b));
      }
      if (path.endsWith("/snapshots"))
        return res(201, {
          id: "snap1",
          name: (o.body as { name: string }).name,
          revision: b.revision,
          made_at: "2026-11-27T05:00:00Z",
          blocks: b.doc.blocks.length,
        });
      return res(404, { detail: "Not found." });
    },
    PATCH: async () => res(404, {}),
    DELETE: async () => res(204),
  },
}));

const { MagicBoard, RUNS_LATER } = await import("@/components/magic/board");
const { closeMenu } = await import("@/components/context-menu");

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
  boards = [];
  calls = [];
  refuseNext = 0;
});
afterEach(() => {
  act(closeMenu);
  cleanup();
});

const edits = () =>
  calls.filter((c) => c.path.endsWith("/edits")).map((c) => c.body as { base_revision: number });

describe("the board in studio", () => {
  it("a first visit makes a board; edits go to studio with their revisions, one after another", async () => {
    render(<MagicBoard />);
    expect(await screen.findByRole("heading", { name: "A new board" })).toBeTruthy();
    expect(boards).toHaveLength(1);
    // two edits at once: the second waits for the first's revision
    fireEvent.click(screen.getByRole("button", { name: "Lane" }));
    expect(screen.getByRole("group", { name: "Lane Shots" })).toBeTruthy();
    fireEvent.click(screen.getAllByRole("button", { name: "Lane" })[0]);
    await waitFor(() => expect(boards[0].revision).toBe(3));
    expect(edits().map((e) => e.base_revision)).toEqual([1, 2]);
    expect(boards[0].doc.lanes.map((l) => l.name)).toEqual(["Shots", "Lane 2"]);
    expect(screen.getByRole("group", { name: "Lane Lane 2" })).toBeTruthy();
    // Undo is studio's
    const undo = screen.getByRole("button", { name: "Undo" }) as HTMLButtonElement;
    await waitFor(() => expect(undo.disabled).toBe(false));
    fireEvent.click(undo);
    await waitFor(() => expect(screen.queryByRole("group", { name: "Lane Lane 2" })).toBeNull());
    expect(calls.some((c) => c.path.endsWith("/undo"))).toBe(true);
  });

  it("a refusal says why and reads the board again", async () => {
    render(<MagicBoard />);
    await screen.findByRole("heading", { name: "A new board" });
    refuseNext = 1;
    fireEvent.click(screen.getByRole("button", { name: "Lane" }));
    expect(
      await screen.findByText("board changed since you opened it (now revision 1)"),
    ).toBeTruthy();
    // the board as studio has it: no lane
    await waitFor(() => expect(screen.getByRole("heading", { name: "A new board" })).toBeTruthy());
  });

  it("snapshots are studio's; runs are off until the standard request", async () => {
    render(<MagicBoard />);
    await screen.findByRole("heading", { name: "A new board" });
    fireEvent.click(screen.getByRole("button", { name: "Lane" }));
    await waitFor(() => expect(boards[0].revision).toBe(2));
    fireEvent.click(screen.getByRole("button", { name: /^Snapshots/ }));
    fireEvent.click(screen.getByRole("button", { name: "Save snapshot" }));
    const side = screen.getByRole("complementary", { name: "Block details" });
    expect(await within(side).findByText("Snapshot 1")).toBeTruthy();
    expect(calls.find((c) => c.path.endsWith("/snapshots") && c.method === "POST")?.body).toEqual({
      name: "Snapshot 1",
    });
    const runBoard = screen.getByRole("button", { name: "Run board" }) as HTMLButtonElement;
    expect(runBoard.disabled).toBe(true);
    expect(runBoard.title).toBe(RUNS_LATER);
  });
});
