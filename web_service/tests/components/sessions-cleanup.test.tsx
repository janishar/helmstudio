// @vitest-environment jsdom
// Plan 21 on Create (docs/mock/Main.dc.html "Other states"): a take tile's ⋯ Delete and its card, a text answer's
// Delete, ✕ on failed and stopped run tiles (none on a running or a batch's), "N failed · Clear" (no card), the
// session menu's Rename (Enter, Esc, blank) and Delete (greyed while running), the remote-naming line, and the
// `session` event swapping the title with the "named by" hint once.
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import coreNodes from "../../../contracts/fixtures/registry/nodes.valid.json";
import h3Nodes from "../../../contracts/fixtures/registry/minimax-h3.nodes.valid.json";
import h3Workflows from "../../../contracts/fixtures/registry/minimax-h3.workflows.valid.json";
import { Create, type CreateInitial } from "@/components/create/create";
import type { S } from "@/lib/api";
import { answer, take } from "./helpers";

const events = vi.hoisted(() => ({ emit: (() => undefined) as (e: unknown) => void }));
vi.mock("@/lib/runs", async (orig) => ({
  ...(await orig<typeof import("@/lib/runs")>()),
  followSession: (_id: string, on: (e: unknown) => void) => {
    events.emit = on;
    return () => undefined;
  },
}));

beforeAll(() => {
  globalThis.requestAnimationFrame ??= (f: FrameRequestCallback) =>
    setTimeout(() => f(0), 0) as unknown as number;
  HTMLElement.prototype.hidePopover ??= () => undefined;
  Element.prototype.scrollIntoView ??= () => undefined;
  HTMLDialogElement.prototype.showModal ??= function (this: HTMLDialogElement) {
    this.open = true;
  };
  HTMLDialogElement.prototype.close ??= function (this: HTMLDialogElement) {
    this.open = false;
  };
});
beforeEach(() => localStorage.clear());
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const workflow = {
  ...h3Workflows.workflows[0],
  node_schemas: Object.fromEntries([...coreNodes.nodes, ...h3Nodes.nodes].map((n) => [n.kind, n])),
  source: "template",
} as unknown as S<"Workflow">;

let at = 0;
const run = (id: string, status: S<"RunStatus">, over: Partial<S<"Run">> = {}) =>
  ({
    id,
    session_id: "s1",
    workflow_id: workflow.id,
    inputs: {},
    status,
    progress: null,
    error: status === "failed" ? "out of memory on mac" : null,
    outputs: [],
    host: "mac",
    attempt: 1,
    seed: null,
    created_at: `2026-11-22T10:00:${String(at++).padStart(2, "0")}Z`,
    ...over,
  }) as unknown as S<"Run">;

const session = (id: string, title: string, over: Partial<S<"StudioSession">> = {}) =>
  ({
    id,
    title,
    title_source: "user",
    named_by: null,
    created_at: "2026-11-22T10:00:00Z",
    updated_at: "2026-11-22T10:00:00Z",
    ...over,
  }) as S<"StudioSession">;

function start(over: Partial<CreateInitial> = {}, queue: S<"Run">[] = []) {
  answer("GET", {
    "/v1/studio/sessions/{session_id}/runs": () => ({ data: { runs: over.runs ?? [] } }),
    "/v1/studio/sessions/{session_id}/takes": () => ({ data: { takes: over.takes ?? [] } }),
    "/v1/studio/library": () => ({ data: { items: [] } }),
    "/v1/studio/loras": () => ({ data: { loras: [] } }),
    "/v1/studio/queue": () => ({ data: { runs: queue, hosts: [] } }),
  });
  answer("POST", {});
  const del = answer("DELETE", {
    "/v1/studio/runs/{run_id}": () => ({ status: 204 }),
    "/v1/studio/takes/{take_id}": () => ({ status: 204 }),
    "/v1/studio/sessions/{session_id}": () => ({ status: 204 }),
  });
  const patch = answer("PATCH", {
    "/v1/studio/sessions/{session_id}": (init) => ({
      data: session("s1", (init.body as { title: string }).title),
    }),
  });
  render(
    <Create
      initial={{
        sessions: [session("s1", "Fox in the snow"), session("s2", "Neon alley at night")],
        workflows: [workflow],
        sessionId: "s1",
        runs: [],
        takes: [],
        ...over,
      }}
    />,
  );
  return { del, patch };
}

const strip = () => screen.getByRole("group", { name: "Takes" });
const deleted = (spy: ReturnType<typeof answer>, path: string) =>
  (spy.mock.calls as unknown as [string, unknown][])
    .filter(([p]) => p === path)
    .map(([, init]) => init);

describe("Create: deleting from the strip", () => {
  it("a take's ⋯ Delete opens its card, then deletes it", async () => {
    const t = take({ run_id: "r1", made_with: { seed: 43 } as S<"MadeWith"> });
    const { del } = start({ runs: [run("r1", "succeeded")], takes: [t] });
    const tiles = within(strip());
    expect(
      tiles.getByRole("button", { name: "Take seed 43: more" }).getAttribute("popovertarget"),
    ).toBe(`tile-${t.id}`);
    fireEvent.click(within(document.getElementById(`tile-${t.id}`)!).getByText("Delete"));
    const card = await screen.findByRole("dialog", { name: "Delete this take?" });
    expect(card.textContent).toContain("It leaves the gallery and Create. This can't be undone.");
    fireEvent.click(within(card).getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(screen.queryByRole("group", { name: "Takes" })).toBeNull());
    expect(deleted(del, "/v1/studio/takes/{take_id}")).toEqual([
      { params: { path: { take_id: t.id } } },
    ]);
  });

  it("a text answer's Delete has its own card and deletes the run", async () => {
    // a succeeded run shown as a tile (no take loaded for its output)
    const out = { index: 0, key: "k", kind: "image", url: "/files/u/x/1.png", width: 1, height: 1 };
    const { del } = start({
      runs: [run("r2", "succeeded", { outputs: [out] as S<"Run">["outputs"] })],
    });
    expect(within(strip()).getByRole("button", { name: "Answer 1: more" })).toBeTruthy();
    fireEvent.click(within(document.getElementById("tile-r2")!).getByText("Delete"));
    const card = await screen.findByRole("dialog", { name: "Delete this answer?" });
    fireEvent.click(within(card).getByRole("button", { name: "Delete" }));
    await waitFor(() =>
      expect(deleted(del, "/v1/studio/runs/{run_id}")).toEqual([
        { params: { path: { run_id: "r2" } } },
      ]),
    );
  });

  it("a batch's answer has no ⋯ (it goes with its batch)", () => {
    const out = { index: 0, key: "k", kind: "image", url: "/files/u/x/1.png", width: 1, height: 1 };
    start({
      runs: [run("r3", "succeeded", { outputs: [out] as S<"Run">["outputs"], batch_id: "b1" })],
    });
    expect(within(strip()).queryByRole("button", { name: /: more$/ })).toBeNull();
  });

  it("✕ on failed and stopped tiles only; Clear removes both without a card", async () => {
    const runs = [
      run("ok", "running"),
      run("bad", "failed"),
      run("stop", "cancelled"),
      run("batch", "failed", { batch_id: "b1" }),
    ];
    const { del } = start({ runs });
    const s = within(strip());
    expect(s.getAllByRole("button", { name: /^Remove (failed|stopped) run/ })).toHaveLength(2);
    expect(s.getByTitle("Stop it first")).toBeTruthy();
    const clear = s.getByRole("button", { name: "2 failed · Clear" });
    expect(clear.title).toBe("1 failed, 1 stopped");
    fireEvent.click(clear);
    await waitFor(() => expect(s.queryByRole("button", { name: "2 failed · Clear" })).toBeNull());
    expect(
      deleted(del, "/v1/studio/runs/{run_id}").map(
        (i) => (i as { params: { path: { run_id: string } } }).params.path.run_id,
      ),
    ).toEqual(["bad", "stop"]);
    expect(screen.queryByRole("dialog", { name: /Delete/ })).toBeNull();
    // the running and the batch's run stay, the batch's without a ✕
    expect(s.queryAllByRole("button", { name: /^Remove/ })).toHaveLength(0);
  });

  it("a failed tile's ✕ deletes that run alone", async () => {
    const { del } = start({ runs: [run("bad", "failed")] });
    const x = within(strip()).getByRole("button", { name: /^Remove failed run/ });
    expect(within(strip()).getByTitle("out of memory on mac")).toBeTruthy();
    fireEvent.click(x);
    await waitFor(() => expect(screen.queryByRole("group", { name: "Takes" })).toBeNull());
    expect(deleted(del, "/v1/studio/runs/{run_id}")).toHaveLength(1);
  });
});

describe("Create: the session menu", () => {
  // the popover (closed in jsdom, so out of the accessibility tree)
  const menu = () => document.getElementById("sessions")!;

  it("Rename: Enter saves, Esc and blank keep the old title", async () => {
    const { patch } = start();
    const m = within(menu());
    fireEvent.click(m.getByRole("button", { name: "Rename Fox in the snow", hidden: true }));
    const box = m.getByLabelText("Session title");
    fireEvent.keyDown(box, { key: "Escape" });
    expect(m.queryByLabelText("Session title")).toBeNull();

    fireEvent.click(m.getByRole("button", { name: "Rename Fox in the snow", hidden: true }));
    fireEvent.change(m.getByLabelText("Session title"), { target: { value: "   " } });
    fireEvent.submit(m.getByLabelText("Session title"));
    expect(patch).not.toHaveBeenCalled();

    fireEvent.click(m.getByRole("button", { name: "Rename Fox in the snow", hidden: true }));
    fireEvent.change(m.getByLabelText("Session title"), { target: { value: " Red fox " } });
    fireEvent.submit(m.getByLabelText("Session title"));
    await waitFor(() => expect(screen.getAllByText("Red fox").length).toBeGreaterThan(0));
    expect(patch.mock.calls[0][1]).toEqual({
      params: { path: { session_id: "s1" } },
      body: { title: "Red fox" },
    });
  });

  it("Delete is greyed while a run of the session is going", async () => {
    start({}, [run("q1", "running", { session_id: "s2" })]);
    fireEvent.click(screen.getByRole("button", { name: /^Session: Fox in the snow/ }));
    const d = await within(menu()).findByRole("button", {
      name: "Delete Neon alley at night",
      hidden: true,
    });
    await waitFor(() => expect(d.getAttribute("aria-disabled")).toBe("true"));
    expect(d.title).toBe("Stop its runs first");
    expect(within(menu()).getByText("running")).toBeTruthy();
    fireEvent.click(d);
    expect(screen.queryByRole("dialog", { name: "Delete this session?" })).toBeNull();
  });

  it("Delete opens its card and Create opens the other session", async () => {
    const { del } = start();
    fireEvent.click(
      within(menu()).getByRole("button", { name: "Delete Fox in the snow", hidden: true }),
    );
    const card = await screen.findByRole("dialog", { name: "Delete this session?" });
    expect(card.textContent).toContain(
      "Fox in the snow: its runs go. Its takes stay in the gallery. This can't be undone.",
    );
    fireEvent.click(within(card).getByRole("button", { name: "Delete" }));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /^Session: Neon alley at night/ })).toBeTruthy(),
    );
    expect(deleted(del, "/v1/studio/sessions/{session_id}")).toHaveLength(1);
  });

  it("says a remote model names new sessions", () => {
    start({ naming: { mode: "remote", named_by: "DeepSeek", remote: true } });
    expect(
      within(menu()).getByText("New sessions are named by DeepSeek from their first prompt."),
    ).toBeTruthy();
  });

  it("no line for a local namer", () => {
    start({ naming: { mode: "local", named_by: "Qwen3.8", remote: false } });
    expect(within(menu()).queryByText(/New sessions are named/)).toBeNull();
  });

  it("the `session` event swaps the title; the hint shows once", async () => {
    start({ runs: [run("r1", "succeeded")] });
    const named = {
      seq: 9,
      type: "session",
      run_id: "r1",
      ts: "2026-11-22T10:01:00Z",
      data: { title: "Red fox at dusk", title_source: "model", named_by: "Qwen3.8" },
    };
    act(() => events.emit(named));
    expect(screen.getByRole("button", { name: /^Session: Red fox at dusk/ })).toBeTruthy();
    expect(screen.getByRole("status").textContent).toBe("named by Qwen3.8");
    cleanup();
    start({ runs: [run("r1", "succeeded")] });
    act(() => events.emit(named));
    expect(screen.queryByText("named by Qwen3.8")).toBeNull();
  });
});
