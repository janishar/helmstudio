// @vitest-environment jsdom
// The Memory list in the drawer (plan 18 §22.8; docs/mock/Assistant.dc.html frame 6): the four kinds as tabs (Likes,
// Projects, Worked, Chats), each item with its chat, Edit and Delete, and Clear all behind a card; a Remembered line
// with its icon; a web search line with its sources as text.
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { api, type S } from "@/lib/api";
import { remember } from "@/lib/take-cache";
import { answer } from "./helpers";

let path = "/";
const push = vi.fn();
vi.mock("next/navigation", () => ({
  usePathname: () => path,
  useRouter: () => ({ push }),
}));
vi.mock("@/app/(auth)/actions", () => ({ signout: () => undefined }));
vi.mock("@/lib/assistant", async (orig) => ({
  ...(await orig<typeof import("@/lib/assistant")>()),
  tabClientId: () => "tabA00000001",
}));
vi.mock("@/lib/runs", async (orig) => ({
  ...(await orig<typeof import("@/lib/runs")>()),
  followEvents: () => () => undefined,
}));

const { AppShell } = await import("@/components/shell");

beforeAll(() => {
  HTMLElement.prototype.hidePopover ??= () => undefined;
  Element.prototype.scrollIntoView ??= () => undefined;
  HTMLDialogElement.prototype.showModal ??= function (this: HTMLDialogElement) {
    this.open = true;
  };
});
beforeEach(() => {
  sessionStorage.clear(); // the drawer's open state and tab id live per tab

  path = "/";
  history.replaceState(null, "", "/");
  push.mockReset();
  localStorage.clear();
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const ME = "tabA00000001";
const T = "11111111-2222-4333-8444-555555555555";
const id = (n: number, turn = 1) => `${T}:${turn}:${n}`;
const user = { id: "u1", email: "u@x.test", name: "U", roles: ["user"] } as unknown as S<"User">;
const status = (remote = false, thinks = false): S<"AssistantStatus"> => ({
  enabled: true,
  thinks,
  sees: false,
  provider: { title: "DeepSeek", remote },
  web_search: null,
  limits: {
    model_calls_per_day: 200,
    tokens_per_day: 2000000,
    look_at_per_hour: 20,
    messages_per_minute: 6,
  },
});
const thread = (over: Partial<S<"AssistantThread">> = {}): S<"AssistantThread"> => ({
  id: T,
  title: "New chat",
  auto: false,
  state: "idle",
  client_id: null,
  pending: null,
  caps: { model_calls: 0, tool_calls: 0, memory_writes: 0 },
  created_at: "2026-11-22T00:00:00Z",
  updated_at: "2026-11-22T00:00:00Z",
  ...over,
});
const entry = (n: number, over: Partial<S<"AssistantEntry">>): S<"AssistantEntry"> => ({
  id: `00000000-0000-4000-8000-00000000000${n}`,
  turn: 1,
  kind: "assistant",
  at: "2026-11-22T00:00:00Z",
  ...over,
});

/** Serves the assistant routes; returns the bodies POSTed to …/messages. */
function serve({
  remote = false,
  existing,
  entries = [],
}: { remote?: boolean; existing?: S<"AssistantThread">; entries?: S<"AssistantEntry">[] } = {}) {
  const posts: S<"AssistantPost">[] = [];
  answer("GET", {
    "/v1/studio/assistant/threads": () => ({
      data: { status: status(remote), threads: existing ? [existing] : [] },
    }),
    "/v1/studio/assistant/threads/{thread_id}": () => ({ data: { ...existing, entries } }),
  });
  answer("POST", {
    "/v1/studio/assistant/threads": ({ body }) => ({
      status: 201,
      data: thread({ client_id: (body as { client_id: string }).client_id }),
    }),
    "/v1/studio/assistant/threads/{thread_id}/messages": ({ body }) => {
      posts.push(body as S<"AssistantPost">);
      return {
        status: 202,
        data: { thread: thread({ state: "working", client_id: ME }), entry: null },
      };
    },
  });
  return posts;
}

async function openDrawer() {
  const r = render(<AppShell user={user}>{null}</AppShell>);
  fireEvent.click(await screen.findByRole("button", { name: /^Assistant/ }));
  await screen.findByRole("complementary", { name: "Assistant" });
  return r;
}

const now = new Date().toISOString();
const mem = (
  n: number,
  kind: S<"AssistantMemoryKind">,
  text: string,
): S<"AssistantMemoryItem"> => ({
  id: `99999999-0000-4000-8000-00000000000${n}`,
  kind,
  text,
  thread: { id: T, title: "Rain still" },
  created_at: now,
  updated_at: now,
});

describe("the Memory list", () => {
  it("shows the kinds as tabs; Edit and Delete are the user's; Clear all is a card", async () => {
    serve({ existing: thread() });
    let items = [
      mem(1, "preference", "You like 16:9"),
      mem(2, "preference", "No text in images"),
      { ...mem(3, "project", "Bus stop is Seq 2"), thread: null },
    ];
    const counts = () => ({ preference: 2, project: 1, worked: 0, summary: 0 });
    const get = vi.spyOn(api, "GET");
    const base = get.getMockImplementation() as unknown as (p: string, init: unknown) => unknown;
    get.mockImplementation(((p: string, init: unknown) =>
      p === "/v1/studio/assistant/memory"
        ? Promise.resolve({ data: { items, counts: counts() }, response: new Response() })
        : base(p, init)) as never);
    const patches: unknown[] = [];
    vi.spyOn(api, "PATCH").mockImplementation(((_: string, init: { body: { text: string } }) => {
      patches.push(init);
      return Promise.resolve({
        data: { ...items[0], text: init.body.text },
        response: new Response(),
      });
    }) as never);
    const deletes: string[] = [];
    vi.spyOn(api, "DELETE").mockImplementation(((
      p: string,
      init?: { params: { path: { item_id: string } } },
    ) => {
      deletes.push(init ? init.params.path.item_id : p);
      if (!init) items = [];
      return Promise.resolve({ data: undefined, response: new Response(null, { status: 204 }) });
    }) as never);
    await openDrawer();
    fireEvent.click(screen.getByRole("button", { name: "Memory" }));
    const tabs = screen.getByRole("tablist", { name: "Memory" });
    expect(Array.from(tabs.querySelectorAll("[role=tab]")).map((t) => t.textContent)).toEqual([
      "Likes",
      "Projects",
      "Worked",
      "Chats",
    ]);
    const likes = await screen.findByRole("list", { name: "Likes" });
    expect(likes.textContent).toContain("You like 16:9");
    expect(likes.textContent).toContain("chat · Rain still · today");
    // Edit and Delete live in each item's ⋯ menu (alpha.2 item 5, board frame 14a)
    const more = () => screen.getAllByRole("button", { name: "More for this item" });
    fireEvent.click(more()[0]);
    fireEvent.click(screen.getByRole("menuitem", { name: "Edit" }));
    fireEvent.change(screen.getByLabelText("Edit"), { target: { value: "You like 21:9 " } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(patches).toHaveLength(1));
    expect(patches[0]).toMatchObject({
      params: { path: { item_id: items[0].id } },
      body: { text: "You like 21:9" },
    });
    expect(await screen.findByText("You like 21:9")).toBeTruthy();
    fireEvent.click(more()[1]);
    expect(deletes).toHaveLength(0); // the menu first: Delete is two taps
    fireEvent.click(screen.getByRole("menuitem", { name: "Delete" }));
    await waitFor(() => expect(deletes).toEqual([items[1].id]));
    expect(screen.queryByText("No text in images")).toBeNull();
    fireEvent.click(screen.getByRole("tab", { name: "Projects" }));
    expect(screen.getByRole("list", { name: "Projects" }).textContent).toContain("chat · deleted");
    fireEvent.click(screen.getByRole("button", { name: "Clear all" }));
    const card = screen.getByRole("region", { name: "Confirm: Clear all memory?" });
    expect(deletes).toHaveLength(1); // the card first
    fireEvent.click(within(card).getByRole("button", { name: "CLEAR ALL" }));
    await waitFor(() => expect(deletes.at(-1)).toBe("/v1/studio/assistant/memory"));
    expect(await screen.findByText("Nothing here yet.")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Close Memory" }));
    expect(screen.getByRole("textbox", { name: "Message the assistant" })).toBeTruthy();
  });
});

describe("a film's shot list (board frame 18)", () => {
  const TAKE = "77777777-0000-4000-8000-000000000001";
  const shots = (n: number) =>
    Array.from({ length: n }, (_, i) => ({
      n: i + 1,
      what: i === 0 ? "Wide: @mara on the wet street" : `Shot ${i + 1} what`,
      seconds: 3,
      ...(i === 0 ? { take_id: TAKE } : {}),
      ...(i === 2 ? { line: "@mara: Wait for me." } : {}),
    }));
  const list = (n: number, over: Partial<S<"AssistantMemoryItem">> = {}) => ({
    ...mem(5, "project", "Shot list · Neon rain: …"),
    shots: { film: "Neon rain", shots: shots(n) },
    ...over,
  });
  const memoryWith = (items: S<"AssistantMemoryItem">[]) => {
    const get = vi.spyOn(api, "GET");
    const base = get.getMockImplementation() as unknown as (p: string, init: unknown) => unknown;
    get.mockImplementation(((p: string, init: unknown) =>
      p === "/v1/studio/assistant/memory"
        ? Promise.resolve({ data: { items, counts: {} }, response: new Response() })
        : base(p, init)) as never);
  };

  it("a: its own row with counts, one line per shot, the take or a dashed box, Next; ⋯ Edit and Delete", async () => {
    serve({ existing: thread() });
    remember([
      { id: TAKE, title: "Street", kind: "image", thumb_url: "https://x.test/t.jpg" } as S<"Take">,
    ]);
    memoryWith([list(4)]);
    const deletes: string[] = [];
    vi.spyOn(api, "DELETE").mockImplementation(((
      _: string,
      init: { params: { path: { item_id: string } } },
    ) => {
      deletes.push(init.params.path.item_id);
      return Promise.resolve({ data: undefined, response: new Response(null, { status: 204 }) });
    }) as never);
    await openDrawer();
    fireEvent.click(screen.getByRole("button", { name: "Memory" }));
    fireEvent.click(screen.getByRole("tab", { name: "Projects" }));
    expect(await screen.findByText("Neon rain · shot list")).toBeTruthy();
    expect(screen.getByText("4 shots · 1 with a take · 12 s planned · Rain still · today")).toBeTruthy();
    const ol = screen.getByRole("list", { name: "Shots of Neon rain" });
    const rows = ol.querySelectorAll(":scope > li");
    expect(rows).toHaveLength(4);
    expect(rows[0].querySelector(".mn")?.textContent).toBe("@mara");
    expect(rows[2].textContent).toContain('"@mara: Wait for me."');
    expect(rows[0].textContent).toContain("3 s");
    // the first shot without a take is Next; no take is a dashed box
    expect(within(rows[1] as HTMLElement).getByText("Next")).toBeTruthy();
    expect(ol.querySelectorAll(".tag")).toHaveLength(1);
    expect(within(rows[1] as HTMLElement).getByLabelText("No take yet")).toBeTruthy();
    fireEvent.click(within(ol).getByRole("button", { name: "Open the take of shot 1" }));
    expect(screen.getByRole("dialog", { name: "Street" })).toBeTruthy();
    // no chevron for a short list
    expect(screen.queryByRole("button", { name: /^Show all/ })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "More for this item" }));
    expect(screen.getByRole("menuitem", { name: "Edit" })).toBeTruthy();
    fireEvent.click(screen.getByRole("menuitem", { name: "Delete" }));
    await waitFor(() => expect(deletes).toEqual([list(4).id]));
  });

  it("b: a long list shows its first 3 shots and the chevron, up again to fold", async () => {
    serve({ existing: thread() });
    memoryWith([list(8)]);
    await openDrawer();
    fireEvent.click(screen.getByRole("button", { name: "Memory" }));
    fireEvent.click(screen.getByRole("tab", { name: "Projects" }));
    const ol = await screen.findByRole("list", { name: "Shots of Neon rain" });
    expect(ol.querySelectorAll(":scope > li")).toHaveLength(3);
    fireEvent.click(screen.getByRole("button", { name: "Show all 8 shots" }));
    expect(ol.querySelectorAll(":scope > li")).toHaveLength(8);
    fireEvent.click(screen.getByRole("button", { name: "Show less" }));
    expect(ol.querySelectorAll(":scope > li")).toHaveLength(3);
  });

  it("b: the chat's line names the film and its count, with no Undo", async () => {
    serve({
      existing: thread(),
      entries: [
        entry(1, {
          kind: "line",
          line: { id: id(0), status: "done", text: "Shot list · Neon rain: 6 shots", undo: false },
        }),
      ],
    });
    await openDrawer();
    const line = (await screen.findByText("Shot list · Neon rain: 6 shots")).closest("li")!;
    expect(within(line).queryByRole("button", { name: /Undo/ })).toBeNull();
  });
});

describe("lines", () => {
  it("a Remembered line has its icon; a web search line lists its sources as text", async () => {
    serve({
      existing: thread(),
      entries: [
        entry(1, {
          kind: "line",
          line: { id: id(0), status: "done", text: "Remembered: you like 16:9", undo: true },
        }),
        entry(2, {
          kind: "line",
          line: {
            id: id(1),
            status: "done",
            text: 'Searched the web · Brave · "ltx 2.3 prompt tips camera motion"',
            undo: false,
            sources: [
              { title: "LTX camera prompts", url: "https://www.example-forum.org/t/1" },
              { title: "Dolly moves in LTX 2", url: "javascript:alert(1)" },
            ],
          },
        }),
      ],
    });
    const { container } = await openDrawer();
    const remembered = (await screen.findByText("Remembered: you like 16:9")).closest("li")!;
    expect(remembered.querySelector(".lucide-brain")).toBeTruthy();
    expect(await screen.findByText("example-forum.org · LTX camera prompts")).toBeTruthy();
    expect(screen.getByText("Dolly moves in LTX 2")).toBeTruthy();
    expect(container.querySelector("ol.src a")).toBeNull();
  });
});
