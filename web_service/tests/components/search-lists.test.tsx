// @vitest-environment jsdom
// The lists that got the SearchField (docs/mock/Search.dc.html): Create's sessions (older ones from the server, Load
// older), Magic's board popover (rows with their own menu), Sequences, All batches (state chips asked of the server),
// the pickers (Enter picks the ringed tile) and API keys (Show revoked).
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import batchFixture from "../../../contracts/fixtures/http/Batch.valid.3.json";
import { api, type S } from "@/lib/api";
import { closeMenu } from "@/components/context-menu";
import type { Board } from "@/components/magic/use-board";
import { answer } from "./helpers";

vi.mock("next/navigation", () => ({ useRouter: () => ({ push: vi.fn(), replace: vi.fn() }) }));
vi.mock("next/link", () => ({
  default: ({ href, children, ...rest }: { href: string; children: React.ReactNode }) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));
vi.mock("@/components/shell", () => ({ useUser: () => ({ name: "Ada" }) }));
vi.stubGlobal(
  "EventSource",
  class {
    addEventListener() {}
    close() {}
  },
);

beforeAll(() => {
  HTMLDialogElement.prototype.showModal ??= function (this: HTMLDialogElement) {
    this.open = true;
  };
  HTMLDialogElement.prototype.close ??= function (this: HTMLDialogElement) {
    this.open = false;
    this.dispatchEvent(new Event("close"));
  };
  HTMLElement.prototype.hidePopover ??= () => undefined;
});
afterEach(() => {
  act(closeMenu);
  cleanup();
  vi.restoreAllMocks();
  vi.useRealTimers();
});

const { SessionMenu } = await import("@/components/create/sessions");
const { BoardSwitcher } = await import("@/components/magic/boards");
const { Sequences } = await import("@/components/timeline/dialogs");
const { Pipelines } = await import("@/components/pipelines/pipelines");
const { VisualPicker } = await import("@/components/create/visual-picker");
const { ApiKeysAdmin } = await import("@/components/admin/apikeys");

const box = (name: string) => screen.getByRole("searchbox", { name, hidden: true });
const labels = () =>
  [...document.querySelectorAll<HTMLElement>('.cm [data-lv="0"]')].map(
    (b) => b.querySelector(".lb")!.textContent,
  );
const titles = (n: number, word = "Take") => Array.from({ length: n }, (_, i) => `${word} ${i}`);

describe("Create's sessions", () => {
  const session = (id: string, title: string) => ({ id, title }) as S<"StudioSession">;
  it("finds older sessions on the server and opens one; Load older pages the list", async () => {
    vi.useFakeTimers();
    const get = answer("GET", {
      "/v1/studio/queue": () => ({ data: { runs: [], hosts: [] } }),
      "/v1/studio/sessions": ({ params }) => {
        const q = (params as { query: { q?: string; cursor?: string } }).query;
        if (q.q)
          return { data: { sessions: [session("old", "Mara, first look")], next_cursor: null } };
        return { data: { sessions: [session("p2", "Page two")], next_cursor: null } };
      },
    });
    const onOpen = vi.fn();
    const onFound = vi.fn();
    const onOlder = vi.fn();
    const list = [
      session("s0", "Mara, rooftop"),
      ...titles(8).map((t, i) => session(`s${i + 1}`, t)),
    ];
    render(
      <SessionMenu
        sessions={list}
        next="c1"
        current="s1"
        hint=""
        onOpen={onOpen}
        onFound={onFound}
        onOlder={onOlder}
        onNew={vi.fn()}
        onRenamed={vi.fn()}
        onDeleted={vi.fn()}
        onError={vi.fn()}
      />,
    );
    expect(screen.getByText("9 newest shown")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Load older", hidden: true }));
    await act(async () => {});
    expect(onOlder).toHaveBeenCalledWith([session("p2", "Page two")], null);

    fireEvent.change(box("Search sessions"), { target: { value: "mara" } });
    expect(screen.queryByText("9 newest shown")).toBeNull();
    await act(async () => void vi.advanceTimersByTime(300));
    expect(get).toHaveBeenLastCalledWith("/v1/studio/sessions", {
      params: { query: { q: "mara", cursor: undefined, limit: 50 } },
    });
    expect(screen.getByText("From older sessions")).toBeTruthy();
    // ↓ rings the older one; Enter opens it, adding it to the list first
    fireEvent.keyDown(box("Search sessions"), { key: "ArrowDown" });
    fireEvent.keyDown(box("Search sessions"), { key: "Enter" });
    expect(onFound).toHaveBeenCalledWith(session("old", "Mara, first look"));
    expect(onOpen).toHaveBeenCalledWith("old");
  });
});

describe("Magic's boards", () => {
  const board = (n: number) =>
    ({
      id: "b0",
      name: "Board 0",
      boards: titles(n, "Board").map((name, i) => ({
        id: `b${i}`,
        name,
        lanes: 2,
        updated_at: "2026-11-27T10:00:00Z",
      })),
      switchTo: vi.fn(),
      rename: vi.fn(),
      remove: vi.fn(),
      newBoard: vi.fn(),
      sampleBoard: vi.fn(),
    }) as unknown as Board;

  it("a popover of boards; rows keep their own menu, the name's menu has no board rows", () => {
    const b = board(10);
    render(<BoardSwitcher board={b} />);
    const pop = document.getElementById("magic-boards")!;
    expect(within(pop).getAllByText(/edited .* · 2 lanes/)).toHaveLength(10);
    fireEvent.change(box("Search boards"), { target: { value: "board 9" } });
    expect(within(pop).queryByText("Board 3")).toBeNull();
    fireEvent.keyDown(box("Search boards"), { key: "Enter" });
    expect(b.switchTo).toHaveBeenCalledWith("b9");
    fireEvent.contextMenu(pop.querySelector(".sf-on")!, { button: 2, clientX: 5, clientY: 5 });
    expect(labels()).toEqual(["Open", "Rename", "Delete"]);
    act(closeMenu);
    fireEvent.contextMenu(screen.getByRole("button", { name: "Board 0" }), {
      button: 2,
      clientX: 5,
      clientY: 5,
    });
    expect(labels()).toEqual(["New board", "Sample board", "Rename", "Delete board"]);
  });

  it("no field at 8 boards", () => {
    render(<BoardSwitcher board={board(8)} />);
    expect(screen.queryByRole("searchbox", { hidden: true })).toBeNull();
  });
});

describe("Sequences", () => {
  it("searches the names in the browser, Esc clears first", async () => {
    answer("GET", {
      "/v1/studio/timelines": () => ({
        data: {
          timelines: titles(9, "Cut").map((name, i) => ({
            id: `t${i}`,
            name,
            clips: 2,
            duration_s: 8,
          })),
          next_cursor: null,
        },
      }),
    });
    const onOpen = vi.fn();
    render(
      <Sequences
        open
        onClose={vi.fn()}
        current={null}
        onOpen={onOpen}
        onNew={vi.fn()}
        onRenamed={vi.fn()}
        onDeleted={vi.fn()}
      />,
    );
    await screen.findByText("Cut 8");
    fireEvent.change(box("Search sequences"), { target: { value: "cut 4" } });
    expect(screen.queryByText("Cut 3")).toBeNull();
    fireEvent.keyDown(box("Search sequences"), { key: "Enter" });
    expect(onOpen).toHaveBeenCalledWith("t4");
    fireEvent.change(box("Search sequences"), { target: { value: "zzz" } });
    expect(screen.getByText("No sequences match.")).toBeTruthy();
    fireEvent.keyDown(box("Search sequences"), { key: "Escape" });
    expect((box("Search sequences") as HTMLInputElement).value).toBe("");
  });
});

describe("All batches", () => {
  const base = batchFixture as unknown as S<"Batch">;
  it("the state chips and the words ask the server (q, state) and Load more pages the matches", async () => {
    vi.useFakeTimers();
    const batches = Array.from({ length: 6 }, (_, i) => ({
      ...base,
      id: `00000000-0000-4000-8000-00000000000${i}`,
      title: `Run ${i}`,
      status: i === 0 ? ("failed" as const) : ("done" as const),
    }));
    const get = vi.spyOn(api, "GET").mockImplementation((() =>
      Promise.resolve({
        data: { batches: [{ ...batches[0], id: "old", title: "Old failure" }], next_cursor: "n2" },
        response: new Response(null, { status: 200 }),
      })) as never);
    render(<Pipelines initial={{ batches, next_cursor: null }} pipelines={[]} workflows={[]} />);
    fireEvent.click(screen.getByRole("button", { name: "All batches" }));
    fireEvent.click(screen.getByRole("button", { name: "Failed" }));
    // at once: the loaded failed batch
    expect(screen.getByText("Run 0")).toBeTruthy();
    expect(screen.queryByText("Run 1")).toBeNull();
    await act(async () => void vi.advanceTimersByTime(300));
    expect(get).toHaveBeenLastCalledWith("/v1/studio/batches", {
      params: {
        query: { q: undefined, state: ["failed", "stopped"], cursor: undefined, limit: 60 },
      },
    });
    expect(screen.getByText("Old failure")).toBeTruthy();
    fireEvent.change(box("Search batches"), { target: { value: "old" } });
    await act(async () => void vi.advanceTimersByTime(300));
    expect(get).toHaveBeenLastCalledWith("/v1/studio/batches", {
      params: { query: { q: "old", state: ["failed", "stopped"], cursor: undefined, limit: 60 } },
    });
    fireEvent.click(screen.getByRole("button", { name: "Load more" }));
    expect(get).toHaveBeenLastCalledWith("/v1/studio/batches", {
      params: { query: { q: "old", state: ["failed", "stopped"], cursor: "n2", limit: 60 } },
    });
  });
});

describe("the pickers", () => {
  it("Enter picks the ringed item, ↓ moves the ring; Enter never submits the form", () => {
    const picked = vi.fn();
    const submit = vi.fn((e: React.FormEvent) => e.preventDefault());
    function Host() {
      return (
        <form onSubmit={submit}>
          <VisualPicker
            info=""
            search={{
              id: "x",
              label: "Search looks",
              value: "a",
              onChange: () => {},
              className: "",
            }}
            listLabel="Looks"
            grid=""
            footer={null}
          >
            {["Alpha", "Gamma", "Beta"].map((n) => (
              <li key={n}>
                <button type="button" data-pick disabled={n === "Gamma"} onClick={() => picked(n)}>
                  {n}
                </button>
              </li>
            ))}
          </VisualPicker>
        </form>
      );
    }
    render(<Host />);
    const f = box("Search looks");
    expect(document.querySelector(".sf-on")?.textContent).toBe("Alpha");
    fireEvent.keyDown(f, { key: "ArrowDown" });
    expect(document.querySelector(".sf-on")?.textContent).toBe("Beta"); // the greyed one is skipped
    fireEvent.keyDown(f, { key: "Enter" });
    expect(picked).toHaveBeenCalledWith("Beta");
    expect(submit).not.toHaveBeenCalled();
  });
});

describe("API keys", () => {
  it("revoked keys hide behind Show revoked, the foot counts them", async () => {
    const key = (id: string, client: string, revoked_at: string | null) => ({
      id,
      client,
      prefix: `hs_${id}`,
      created_at: "2026-11-01T00:00:00Z",
      last_used_at: null,
      revoked_at,
    });
    answer("GET", {
      "/auth/admin/apikeys": () => ({
        data: {
          api_keys: [key("a1", "web", null), key("b2", "old", "2026-11-02T00:00:00Z")],
        },
      }),
    });
    render(<ApiKeysAdmin />);
    await screen.findByText("web");
    expect(screen.queryByText("old")).toBeNull();
    expect(screen.getByText(/1 active.*1 revoked hidden/)).toBeTruthy();
    fireEvent.click(screen.getByRole("switch", { name: "Show revoked" }));
    await waitFor(() => expect(screen.getByText("old")).toBeTruthy());
  });
});
