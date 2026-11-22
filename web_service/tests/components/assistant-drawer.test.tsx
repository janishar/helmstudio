// @vitest-environment jsdom
// The Assistant drawer in the shell (components/assistant; plan 18 §22.1, §22.3, §22.6; docs/mock/Assistant.dc.html):
// the dock item and ⌘J, no remote disclaimer, a message with the screen summary, a parked turn's actions run in this tab
// with their lines and Undo, a card whose Yes is the only way the costly action runs (Not now and leaving the screen
// cancel it), navigate dropping the rest of the batch, another tab's chat read-only with Continue here, a server
// card's answer, the daily limit, a Remembered line's Undo, and model text that stays text.
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { useEffect, useState } from "react";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { api, type S } from "@/lib/api";
import { done, type Handler } from "@/lib/assistant";
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
let onEvent: ((e: S<"AssistantEvent">) => void) | null = null;
vi.mock("@/lib/runs", async (orig) => ({
  ...(await orig<typeof import("@/lib/runs")>()),
  followEvents: (_u: string, _t: string[], on: (e: S<"AssistantEvent">) => void) => {
    onEvent = on;
    return () => (onEvent = null);
  },
}));

const { AppShell } = await import("@/components/shell");
const { useRegistry, useScreenTools } = await import("@/components/assistant/tools");
type Reg = NonNullable<ReturnType<typeof useRegistry>>;

beforeAll(() => {
  HTMLElement.prototype.hidePopover ??= () => undefined;
  Element.prototype.scrollIntoView ??= () => undefined;
});
beforeEach(() => {
  sessionStorage.clear(); // the drawer's open state and tab id live per tab

  path = "/";
  history.replaceState(null, "", "/");
  push.mockReset();
  onEvent = null;
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
const status = (remote = false): S<"AssistantStatus"> => ({
  enabled: true,
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

let reg: Reg | null = null;
/** A screen that registers the given actions (as Create would). */
function Screen({ actions }: { actions: Record<string, Handler> }) {
  const r = useRegistry();
  useEffect(() => {
    reg = r;
  });
  useScreenTools({
    screen: "create",
    sees: ["Create · Image", "Qwen Image · Turbo"],
    ids: { session_id: "s1" },
    summary: () => ({ prompt: "a rainy street" }),
    actions,
  });
  return null;
}

async function openDrawer(children: React.ReactNode = null) {
  const r = render(<AppShell user={user}>{children}</AppShell>);
  fireEvent.click(await screen.findByRole("button", { name: /^Assistant/ }));
  await screen.findByRole("complementary", { name: "Assistant" });
  return r;
}

/** The parked turn arrives for this tab. */
function park(list: { id: string; name: string; args: Record<string, unknown> }[], turn = 1) {
  const actions = list as S<"AssistantAction">[];
  act(() =>
    onEvent!({
      seq: 10 + turn,
      type: "thread",
      thread: thread({ state: "waiting", client_id: ME, pending: { turn, actions, cards: [] } }),
    }),
  );
}

describe("the drawer", () => {
  it("opens with ⌘J, then sends a message with the screen summary", async () => {
    const posts = serve({ remote: true });
    await openDrawer(<Screen actions={{ change: () => done("x") }} />);
    expect(screen.getByText("Hi. What should we make?")).toBeTruthy();
    expect(screen.getByText("Sees")).toBeTruthy();
    expect(screen.getByText("Qwen Image · Turbo")).toBeTruthy();
    const box = screen.getByRole("textbox", { name: "Message the assistant" });
    fireEvent.change(box, { target: { value: "Make a rainy London still" } });
    fireEvent.keyDown(box, { key: "Enter" });
    await waitFor(() => expect(posts).toHaveLength(1));
    const p = posts[0] as Extract<S<"AssistantPost">, { kind: "message" }>;
    expect(p).toMatchObject({ kind: "message", client_id: ME, text: "Make a rainy London still" });
    expect(p.screen).toMatchObject({
      route: "/",
      ids: { session_id: "s1" },
      roles: ["user"],
      hidden: false,
      fields: { screen: "create", prompt: "a rainy street" },
    });
    expect(p.screen.actions).toEqual(["navigate", "await", "change"]);
    expect(Date.parse(p.screen.as_of)).toBeGreaterThan(0);
    fireEvent.keyDown(window, { key: "j", ctrlKey: true });
    expect(screen.queryByRole("complementary", { name: "Assistant" })).toBeNull();
    fireEvent.keyDown(window, { key: "j", metaKey: true });
    expect(screen.getByRole("complementary", { name: "Assistant" })).toBeTruthy();
  });

  it.each([true, false])("no remote disclaimer for any provider (remote %s)", async (remote) => {
    serve({ remote });
    await openDrawer();
    expect(screen.queryByText(/leave this machine|Remote provider/)).toBeNull();
    expect(!!screen.queryByText("Runs on this machine")).toBe(!remote);
  });

  it("Expand opens the message in a dialog titled Message", async () => {
    HTMLDialogElement.prototype.showModal ??= function (this: HTMLDialogElement) {
      this.open = true;
    };
    serve();
    await openDrawer();
    fireEvent.click(screen.getByRole("button", { name: "Expand" }));
    expect(screen.getByRole("dialog", { name: "Message" })).toBeTruthy();
    expect(screen.getByText(/^· Esc closes$/)).toBeTruthy();
  });

  it("is hidden when the admin has it off", async () => {
    answer("GET", {
      "/v1/studio/assistant/threads": () => ({
        data: { status: { ...status(), enabled: false }, threads: [] },
      }),
    });
    render(<AppShell user={user}>x</AppShell>);
    await act(() => Promise.resolve());
    expect(screen.queryByRole("button", { name: "Assistant" })).toBeNull();
    fireEvent.keyDown(window, { key: "j", metaKey: true });
    expect(screen.queryByRole("complementary", { name: "Assistant" })).toBeNull();
  });

  it("says a hidden tab's summary is hidden", async () => {
    const posts = serve();
    await openDrawer(<Screen actions={{}} />);
    Object.defineProperty(document, "hidden", { value: true, configurable: true });
    try {
      const box = screen.getByRole("textbox", { name: "Message the assistant" });
      fireEvent.change(box, { target: { value: "hi" } });
      fireEvent.keyDown(box, { key: "Enter" });
      await waitFor(() => expect(posts).toHaveLength(1));
      expect((posts[0] as { screen: S<"AssistantScreen"> }).screen.hidden).toBe(true);
    } finally {
      Object.defineProperty(document, "hidden", { value: false, configurable: true });
    }
  });
});

describe("a turn's actions in this tab", () => {
  it("runs the lines with Undo, and Generate waits on the card: nothing runs until Yes", async () => {
    const posts = serve({ existing: thread() });
    const undo = vi.fn();
    const redo = vi.fn();
    const yes = vi.fn(async () =>
      done("You clicked GENERATE · 4 images", { result: { run_ids: ["r1"] } }),
    );
    await openDrawer(
      <Screen
        actions={{
          change: () => done("Set Lighting: Blue hour", { undo: { undo, redo } }),
          generate: () => ({
            card: {
              kind: "generate",
              title: "Generate 4 images?",
              rows: [["Model", "Qwen Image · Turbo"]],
              tags: ["uses the GPU"],
              est: "Mac · 1st in line · about 1 min 20 s · 24 of 64 GB",
              go: "GENERATE",
              fine: "Same as the Generate button on the screen.",
            },
            yes,
          }),
        }}
      />,
    );
    await waitFor(() => expect(onEvent).not.toBeNull());
    park([
      { id: id(0), name: "change", args: { key: "look:lighting", value: "Blue hour" } },
      { id: id(1), name: "generate", args: {} },
    ]);
    const card = await screen.findByRole("region", { name: "Confirm: Generate 4 images?" });
    expect(card.textContent).toContain("Mac · 1st in line");
    expect(card.textContent).toContain("uses the GPU");
    expect(screen.getByText("Set Lighting: Blue hour")).toBeTruthy();
    expect(screen.getByText("Waiting for your OK · Generate")).toBeTruthy();
    expect(screen.getByText("Waiting for you")).toBeTruthy();
    expect(yes).not.toHaveBeenCalled();
    expect(posts).toHaveLength(0);
    fireEvent.click(screen.getByRole("button", { name: "Undo" }));
    await waitFor(() => expect(undo).toHaveBeenCalledTimes(1));
    fireEvent.click(await screen.findByRole("button", { name: "Redo" }));
    await waitFor(() => expect(redo).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByRole("button", { name: "GENERATE" }));
    await waitFor(() => expect(posts).toHaveLength(1));
    expect(yes).toHaveBeenCalledTimes(1);
    expect({ ...posts[0], screen: undefined }).toEqual({
      screen: undefined,
      kind: "results",
      client_id: ME,
      turn: 1,
      results: [
        { id: id(0), status: "done", line: { text: "Set Lighting: Blue hour", undo: true } },
        {
          id: id(1),
          status: "done",
          line: { text: "You clicked GENERATE · 4 images", undo: false },
          result: { run_ids: ["r1"] },
        },
      ],
    });
    expect(posts[0]).toMatchObject({ screen: { fields: { screen: "create" } } });
    expect(screen.queryByRole("region", { name: "Confirm: Generate 4 images?" })).toBeNull();
  });

  it("sends the screen as the actions left it: a switch's new workflow, not the one of the message", async () => {
    const posts = serve({ existing: thread() });
    function Switching() {
      const [w, setW] = useState("Echo");
      useScreenTools({
        screen: "create",
        sees: [w],
        summary: () => ({ workflow: w }),
        actions: {
          switchTo: ({ workflow }) => {
            setW(String(workflow));
            return done(`Switched to Image · ${String(workflow)}`);
          },
        },
      });
      return null;
    }
    await openDrawer(<Switching />);
    await waitFor(() => expect(onEvent).not.toBeNull());
    park([{ id: id(0), name: "switchTo", args: { workflow: "Qwen Image" } }]);
    await waitFor(() => expect(posts).toHaveLength(1));
    expect(posts[0]).toMatchObject({ screen: { fields: { workflow: "Qwen Image" } } });
  });

  it("Not now cancels the card; leaving the screen cancels it too", async () => {
    const posts = serve({ existing: thread() });
    const yes = vi.fn(async () => done("ran"));
    const gen = () => ({
      card: {
        kind: "delete",
        title: "Delete Rain still?",
        rows: [],
        tags: [],
        go: "DELETE",
        fine: "",
        danger: true,
      },
      yes,
    });
    const r = await openDrawer(<Screen actions={{ deleteWorkflow: gen }} />);
    await waitFor(() => expect(onEvent).not.toBeNull());
    park([{ id: id(0), name: "deleteWorkflow", args: {} }]);
    fireEvent.click(await screen.findByRole("button", { name: "Not now" }));
    await waitFor(() => expect(posts).toHaveLength(1));
    expect(posts[0]).toMatchObject({
      results: [{ id: id(0), status: "cancelled", line: { text: "Not now · Delete" } }],
    });
    park([{ id: id(0, 2), name: "deleteWorkflow", args: {} }], 2);
    await screen.findByRole("region", { name: "Confirm: Delete Rain still?" });
    r.rerender(<AppShell user={user}>{null}</AppShell>);
    await waitFor(() => expect(posts).toHaveLength(2));
    expect(posts[1]).toMatchObject({
      turn: 2,
      results: [{ status: "cancelled", line: { text: "Cancelled: you left the screen" } }],
    });
    expect(yes).not.toHaveBeenCalled();
  });

  it("an action the screen does not have, or with bad arguments, fails without running", async () => {
    const posts = serve({ existing: thread() });
    const seeds = vi.fn(() => done("Set seeds"));
    await openDrawer(<Screen actions={{ seeds }} />);
    await waitFor(() => expect(onEvent).not.toBeNull());
    park([
      { id: id(0), name: "timelineSave", args: {} },
      { id: id(1), name: "seeds", args: { n: 2, extra: 1 } },
    ]);
    await waitFor(() => expect(posts).toHaveLength(1));
    expect(posts[0]).toMatchObject({
      results: [
        { status: "failed", line: { text: "timelineSave · not on this screen" } },
        { status: "failed", line: { text: "seeds · extra is not an argument" } },
      ],
    });
    expect(seeds).not.toHaveBeenCalled();
  });

  it("navigate opens the screen, drops the rest of the batch and sends the new screen", async () => {
    const posts = serve({ existing: thread() });
    const change = vi.fn(() => done("Set"));
    push.mockImplementation((url: string) => {
      history.pushState(null, "", url);
      path = new URL(url, location.href).pathname;
      reg!.setPath(path);
    });
    await openDrawer(<Screen actions={{ change }} />);
    await waitFor(() => expect(onEvent).not.toBeNull());
    park([
      { id: id(0), name: "navigate", args: { to: "/gallery", query: { batch: "b1" } } },
      { id: id(1), name: "change", args: { key: "k", value: 1 } },
    ]);
    await waitFor(() => expect(posts).toHaveLength(1), { timeout: 3000 });
    expect(push).toHaveBeenCalledWith("/gallery?batch=b1");
    expect(change).not.toHaveBeenCalled();
    const p = posts[0] as Extract<S<"AssistantPost">, { kind: "results" }>;
    expect(p.results).toMatchObject([
      { status: "done", line: { text: "Opened Gallery" } },
      { status: "did_not_run", line: { text: "Did not run · went to another screen" } },
    ]);
    expect(p.screen).toMatchObject({ route: "/gallery?batch=b1", fields: { screen: "Gallery" } });
  });

  it("refuses Admin to a non-admin without leaving the screen", async () => {
    const posts = serve({ existing: thread() });
    await openDrawer(<Screen actions={{}} />);
    await waitFor(() => expect(onEvent).not.toBeNull());
    park([{ id: id(0), name: "navigate", args: { to: "/admin" } }]);
    await waitFor(() => expect(posts).toHaveLength(1));
    expect(push).not.toHaveBeenCalled();
    expect(posts[0]).toMatchObject({
      results: [{ status: "failed", line: { text: "Can't go there · Admin is for admins only" } }],
    });
  });
});

describe("other tabs, server cards, limits, memory", () => {
  it("another tab's chat is read-only here, and Continue here moves it", async () => {
    const other = thread({
      state: "waiting",
      client_id: "tabB00000001",
      pending: { turn: 1, actions: [{ id: id(0), name: "change", args: {} }], cards: [] },
    });
    const posts = serve({
      existing: other,
      entries: [entry(1, { kind: "user", text: "Make 3 shots" })],
    });
    const change = vi.fn(() => done("Set"));
    await openDrawer(<Screen actions={{ change }} />);
    expect(await screen.findByText("Read-only")).toBeTruthy();
    expect(
      screen.getByText("This chat is working in another tab. Its actions run there."),
    ).toBeTruthy();
    expect(screen.queryByRole("textbox", { name: "Message the assistant" })).toBeNull();
    expect(change).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Continue here" }));
    await waitFor(() => expect(posts).toEqual([{ kind: "continue", client_id: ME }]));
  });

  it("a server card is answered with Yes or Not now", async () => {
    const waiting = thread({
      state: "waiting",
      client_id: ME,
      pending: { turn: 1, actions: [], cards: [id(0)] },
    });
    const card = (state: S<"AssistantCard">["state"]) =>
      entry(1, {
        kind: "card",
        card: {
          id: id(0),
          kind: "web_search",
          title: "Search the web?",
          rows: [
            { label: "Query", value: "ltx 2.3 prompt tips" },
            { label: "Provider", value: "Brave · set by your admin" },
          ],
          note: "Only the query leaves this machine.",
          tags: ["sends to Brave"],
          state,
        },
      });
    const posts = serve({ existing: waiting, entries: [card("waiting")] });
    render(<AppShell user={user}>{null}</AppShell>);
    // the dock's dot: a card waits
    fireEvent.click(
      await screen.findByRole("button", { name: /^Assistant.*1 card waits for you/ }),
    );
    const c = await screen.findByRole("region", { name: "Confirm: Search the web?" });
    expect(c.textContent).toContain("ltx 2.3 prompt tips");
    expect(c.textContent).toContain("sends to Brave");
    fireEvent.click(screen.getByRole("button", { name: "SEARCH" }));
    await waitFor(() =>
      expect(posts).toEqual([{ kind: "answer", client_id: ME, card_id: id(0), answer: "yes" }]),
    );
    act(() => onEvent!({ seq: 20, type: "entry", entry: card("yes") }));
    expect(screen.queryByRole("button", { name: "SEARCH" })).toBeNull();
    expect(screen.getByText("You said yes.")).toBeTruthy();
  });

  it("the daily limit keeps the message and shuts the composer until it lifts", async () => {
    const midnight = new Date(2999, 0, 1).toISOString();
    serve({
      existing: thread(),
      entries: [
        entry(1, { kind: "user", text: "Now make it snow instead" }),
        entry(2, {
          kind: "notice",
          text: "You used today's 200 model calls.",
          error: {
            code: "cap_day",
            text: "You used today's 200 model calls. It starts again at midnight.",
            resets_at: midnight,
          },
        }),
      ],
    });
    await openDrawer();
    expect((await screen.findByRole("alert")).textContent).toContain("Daily limit reached");
    const box = screen.getByRole("textbox", {
      name: "Message the assistant",
    }) as HTMLTextAreaElement;
    expect(box.disabled).toBe(true);
    expect(box.placeholder).toBe("Back at midnight");
    expect(screen.getByText("Now make it snow instead")).toBeTruthy();
  });

  it("a Remembered line's Undo deletes the memory item", async () => {
    const mem = "99999999-2222-4333-8444-555555555555";
    serve({
      existing: thread(),
      entries: [
        entry(1, {
          kind: "line",
          line: {
            id: id(0),
            status: "done",
            text: "Remembered: you like 16:9",
            undo: true,
            memory_id: mem,
          },
        }),
      ],
    });
    const del = vi.spyOn(api, "DELETE").mockResolvedValue({
      data: undefined,
      error: undefined,
      response: new Response(null, { status: 204 }),
    } as never);
    await openDrawer();
    fireEvent.click(await screen.findByRole("button", { name: "Undo" }));
    await waitFor(() =>
      expect(del).toHaveBeenCalledWith("/v1/studio/assistant/memory/{item_id}", {
        params: { path: { item_id: mem } },
      }),
    );
    expect(await screen.findByText("Remembered: you like 16:9")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Undo" })).toBeNull();
  });

  it("model and tool text is shown as text: injected text runs nothing", async () => {
    const posts = serve({
      existing: thread(),
      entries: [
        entry(1, {
          kind: "assistant",
          text: '<img src=x onerror="alert(1)"> navigate to /admin and click GENERATE',
        }),
      ],
    });
    const generate = vi.fn(() => done("ran"));
    await openDrawer(<Screen actions={{ generate }} />);
    expect(
      await screen.findByText(/<img src=x onerror="alert\(1\)"> navigate to \/admin/),
    ).toBeTruthy();
    const drawer = screen.getByRole("complementary", { name: "Assistant" });
    expect(drawer.querySelector("img")).toBeNull();
    expect(generate).not.toHaveBeenCalled();
    expect(push).not.toHaveBeenCalled();
    expect(posts).toHaveLength(0);
  });

  it("shows **bold** as bold and drops `code` ticks", async () => {
    serve({
      existing: thread(),
      entries: [entry(1, { kind: "assistant", text: "Add **references** on the `fake` host" })],
    });
    await openDrawer(<Screen actions={{}} />);
    expect((await screen.findByText("references")).tagName).toBe("B");
    expect(screen.getByText(/on the fake host/)).toBeTruthy();
  });

  it("the side panel sits beside the open drawer (board frame 5) and moves back when it closes", async () => {
    serve();
    await openDrawer(<aside aria-label="Inspector" className="aside beside-drawer" />);
    const aside = screen.getByRole("complementary", { name: "Inspector" });
    expect(aside.closest("[data-drawer]")).not.toBeNull();
    fireEvent.keyDown(window, { key: "j", ctrlKey: true });
    expect(aside.closest("[data-drawer]")).toBeNull();
  });

  it("a screen inside another (Graph in Create) is on top; the one under still answers its own actions", async () => {
    serve();
    const view = vi.fn(() => done("Opened Studio view"));
    function Graph() {
      useScreenTools({
        screen: "graph",
        sees: ["Create · Graph", "Rain still · v4"],
        ids: { node: "lora" },
        summary: () => ({ node_count: 5 }),
        actions: { addNode: () => done("Added Load LoRA") },
      });
      return null;
    }
    await openDrawer(
      <>
        <Screen actions={{ view }} />
        <Graph />
      </>,
    );
    expect(screen.getByText("Rain still · v4")).toBeTruthy();
    const s = reg!.summary();
    expect(s.fields).toMatchObject({ screen: "graph", node_count: 5, prompt: "a rainy street" });
    expect(s.ids).toEqual({ session_id: "s1", node: "lora" });
    expect(s.actions).toEqual(["navigate", "await", "addNode", "view"]);
    const r = await reg!.run(
      { id: id(0), name: "view", args: { view: "studio" } as never },
      () => undefined,
    );
    expect(r.out).toMatchObject({ text: "Opened Studio view" });
    expect(view).toHaveBeenCalledTimes(1);
  });
});
