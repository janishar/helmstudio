// @vitest-environment jsdom
// The Magic page on studio's boards (plan 39 phase C), through a mock of the API that applies ops with lib/magic.ts
// as studio does: a first visit makes a board, edits go to …/edits with the revision they were made on (one after
// another), Undo is studio's, a refusal says why and reads the board again, a stale revision (a run's take joined
// meanwhile) sends the same ops again, snapshots are studio's; and (plan 39 phase D) Add's step 2 with the kinds,
// Generate running the block, statuses moving with the board's session events, the block dialog with its run
// history, takes and Pin, and their right-click menus.
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { apply, type Doc } from "@/lib/magic";
import { emptyDoc } from "@/lib/magic-fixture";

vi.mock("@/components/magic/magic.css", () => ({}));

type Run = {
  block: string;
  run_id: string;
  status: string;
  progress?: number;
  takes: string[];
  seed: number;
  current: boolean;
  by: "user";
  created_at: string;
  render_s: number | null;
};
type Wire = {
  id: string;
  name: string;
  revision: number;
  doc: Doc;
  history: Doc[];
  runs?: Run[];
  session?: string;
};
let boards: Wire[] = [];
let calls: { method: string; path: string; body?: unknown }[] = [];
let refuseNext = 0;
let onEvent: ((e: { type: string }) => void) | null = null;
const KINDS = {
  kinds: [
    {
      kind: "video",
      fields: ["prompt", "first", "last", "length", "size", "seed"],
      models: [
        {
          workflow_id: "echo-image-to-video",
          name: "Echo keyframes",
          title: "Echo image to video",
          fields: ["prompt", "first", "last", "length", "size"],
          greyed: { seed: "Echo keyframes can't take a seed" },
          seconds: 0.5,
          advanced: [
            {
              path: "sample.steps",
              param: { name: "steps", type: "int", label: "Steps", default: 4, min: 1, max: 50 },
            },
          ],
        },
        {
          workflow_id: "echo-video",
          name: "Echo video",
          title: "Echo video",
          fields: ["prompt", "length", "size"],
          greyed: {
            first: "Echo video can't take a first frame",
            last: "Echo video can't take a last frame",
            seed: "Echo video can't take a seed",
          },
          seconds: 0.5,
          advanced: [],
        },
      ],
    },
    { kind: "image", fields: ["prompt", "refs", "size", "seed"], models: [] },
    { kind: "voice", fields: ["line", "voice", "seed"], models: [] },
    { kind: "music", fields: [], models: [] },
  ],
};
const TAKE = "11111111-1111-4111-8111-111111111111";
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
  session_id: b.session ?? null,
  runs: b.runs ?? [],
  takes: Object.fromEntries(
    b.doc.blocks
      .flatMap((x) => x.takes)
      .map((id) => [
        id,
        { id, kind: "video", url: `https://x/${id}.mp4`, thumb_url: null, seed: 7 },
      ]),
  ),
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
      if (path === "/v1/studio/magic/kinds") return res(200, KINDS);
      if (path === "/v1/studio/magic/settings") return res(200, { ask_above_gpu_minutes: 5 });
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
          return res(422, { detail: "That lane is gone.", op_index: 0 });
        if (body.base_revision !== b.revision)
          return res(409, {
            detail: `board changed since you opened it (now revision ${b.revision})`,
          });
        let d = b.doc;
        for (const [i, op] of body.ops.entries()) {
          const r = apply(d, op);
          if ("error" in r) return res(422, { detail: r.error, op_index: i });
          d = r.doc;
        }
        write(b, d);
        return res(200, { revision: b.revision, board: answer(b) });
      }
      if (path.endsWith("/run")) {
        const body = o.body as { revision: number; blocks: string[] };
        if (body.revision !== b.revision) return res(409, { detail: "stale" });
        b.session = "sess1";
        b.runs = [
          ...(b.runs ?? []),
          ...body.blocks.map((block, i) => ({
            block,
            run_id: `r${(b.runs?.length ?? 0) + i + 1}`,
            status: "running",
            progress: 40,
            takes: [],
            seed: 7,
            current: true,
            by: "user" as const,
            created_at: "2026-11-27T05:00:00Z",
            render_s: null,
          })),
        ];
        return res(202, {
          board: answer(b),
          started: body.blocks.map((block) => ({ block })),
          skipped: [],
        });
      }
      if (path.endsWith("/plan"))
        return res(200, {
          blocks: b.doc.blocks.map((x) => ({
            block: x.id,
            seconds: 12,
            source: "yours",
            host: "fake",
            peak_gb: null,
            reason: null,
          })),
          lanes: [],
          seconds: 12,
          order: [],
          ask: false,
          ask_text: "",
          limit_minutes: 5,
        });
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
    PUT: async (path: string, o: { body?: unknown }) => {
      calls.push({ method: "PUT", path, body: o.body });
      return res(200, o.body);
    },
    DELETE: async () => res(204),
  },
}));

vi.mock("@/lib/runs", async (orig) => ({
  ...(await orig<typeof import("@/lib/runs")>()),
  followSession: (_: string, f: (e: { type: string }) => void) => {
    onEvent = f;
    return () => void (onEvent = null);
  },
}));
vi.mock("@/lib/library", async (orig) => ({
  ...(await orig<typeof import("@/lib/library")>()),
  useVoices: () => [],
}));

const { MagicBoard } = await import("@/components/magic/board");
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
  onEvent = null;
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
    expect(await screen.findByText("That lane is gone.")).toBeTruthy();
    // the board as studio has it: no lane
    await waitFor(() => expect(screen.getByRole("heading", { name: "A new board" })).toBeTruthy());
  });

  it("a revision studio wrote meanwhile (a run's take): the same ops go again on it", async () => {
    render(<MagicBoard />);
    await screen.findByRole("heading", { name: "A new board" });
    const b = boards[0];
    write(b, b.doc); // a studio write the page has not seen
    fireEvent.click(screen.getByRole("button", { name: "Lane" }));
    await waitFor(() => expect(b.revision).toBe(3));
    expect(edits().map((e) => e.base_revision)).toEqual([1, 2]);
    expect(b.doc.lanes.map((l) => l.name)).toEqual(["Shots"]);
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("snapshots are studio's; Run board waits for a block that runs", async () => {
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
  });
});

/** A board with a Shots lane, opened in studio. */
async function withShots() {
  render(<MagicBoard />);
  await screen.findByRole("heading", { name: "A new board" });
  fireEvent.click(screen.getByRole("button", { name: "Lane" }));
  await waitFor(() => expect(boards[0].revision).toBe(2));
  return boards[0];
}

describe("runs in studio (plan 39 phase D)", () => {
  it("Add's step 2: the standard request with greyed fields; Generate adds the block, then runs it", async () => {
    const b = await withShots();
    fireEvent.click(screen.getByText("Empty · click to add"), { clientX: 0 });
    let dlg = screen.getByRole("dialog", { name: /Add at 0:00 · Shots/ });
    fireEvent.click(within(dlg).getByRole("button", { name: "Next" }));
    dlg = screen.getByRole("dialog", { name: "Video · Shot 1" });
    // the first model: every field but the seed
    const model = within(dlg).getByRole("group", { name: "Model" });
    expect(
      within(model).getByRole("button", { name: "Echo keyframes" }).getAttribute("aria-pressed"),
    ).toBe("true");
    expect(within(dlg).getByText("Echo keyframes can't take a seed")).toBeTruthy();
    expect(within(dlg).getByRole("spinbutton", { name: "Seed" }).matches(":disabled")).toBe(true);
    // the other model greys the frames, each with why
    fireEvent.click(within(model).getByRole("button", { name: "Echo video" }));
    expect(within(dlg).getByText("Echo video can't take a last frame")).toBeTruthy();
    expect(within(dlg).getByRole("combobox", { name: "First frame" }).matches(":disabled")).toBe(
      true,
    );
    fireEvent.change(within(dlg).getByRole("textbox", { name: "Prompt" }), {
      target: { value: "a fox in the rain" },
    });
    expect(within(dlg).getByText("≈ 3 s")).toBeTruthy(); // the pack's 0.5 s a second, 5 s at 720p
    fireEvent.click(within(dlg).getByRole("button", { name: "Generate" }));
    await waitFor(() => expect(calls.some((c) => c.path.endsWith("/run"))).toBe(true));
    const add = calls.find((c) => c.path.endsWith("/edits") && c !== calls[0])!;
    expect(b.doc.blocks[0].request).toEqual({
      kind: "video",
      refs: [],
      model: "echo-video",
      prompt: "a fox in the rain",
    });
    expect(add).toBeTruthy();
    expect(calls.find((c) => c.path.endsWith("/run"))!.body).toEqual({
      revision: 3,
      blocks: ["b1"],
    });
    // running, from studio's answer
    const shot = await screen.findByRole("button", { name: /Video block Shot 1/ });
    await waitFor(() => expect(shot.textContent).toContain("40%"));
  });

  it("statuses follow the board's session: running, done with its take, out of date once its request changes", async () => {
    const b = await withShots();
    fireEvent.click(screen.getByText("Empty · click to add"), { clientX: 0 });
    fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Next" }));
    fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Generate" }));
    await waitFor(() => expect(b.runs?.length).toBe(1));
    await waitFor(() => expect(onEvent).not.toBeNull());
    // the run made its take: studio's write, then an output event on the session
    b.runs![0] = { ...b.runs![0], status: "succeeded", takes: [TAKE] };
    b.doc = { ...b.doc, blocks: b.doc.blocks.map((x) => ({ ...x, takes: [TAKE] })) };
    b.revision++;
    act(() => onEvent!({ type: "output" }));
    const shot = screen.getByRole("button", { name: /Video block Shot 1/ });
    await waitFor(() => expect(within(shot).getByText("Done")).toBeTruthy());
    // the dialog: its run history, its take, Pin; the take's right-click menu
    fireEvent.doubleClick(shot);
    const dlg = await screen.findByRole("dialog", { name: "Video · Shot 1" });
    expect(within(dlg).getByRole("group", { name: "Run 1" }).textContent).toContain("seed 7");
    const take = within(dlg).getByRole("listitem", { name: /Take 1 · seed 7/ });
    fireEvent.contextMenu(take);
    const menu = screen.getByRole("menu", { name: "Take 1" });
    expect(
      within(menu)
        .getAllByRole("menuitem")
        .map((m) => m.textContent),
    ).toEqual(["Show", "Pin take 1", "Copy take id"]);
    act(closeMenu);
    fireEvent.click(within(dlg).getByRole("button", { name: "Pin take 1" }));
    await waitFor(() => expect(b.doc.blocks[0].pinned).toBe(TAKE));
    // a changed request: out of date at once
    fireEvent.change(within(dlg).getByRole("textbox", { name: "Prompt" }), {
      target: { value: "heavier rain" },
    });
    fireEvent.click(within(dlg).getByRole("button", { name: "Save and close" }));
    await waitFor(() => expect(within(shot).getByText("Out of date")).toBeTruthy());
  });
});
