// @vitest-environment jsdom
// The Assistant drawer in the shell (components/assistant; plan 18 §22.1, §22.3, §22.6; docs/mock/Assistant.dc.html):
// the dock item and ⌘J, no remote disclaimer, a message with the screen summary, a parked turn's actions run in this tab
// with their lines and Undo, a card whose Yes is the only way the costly action runs (Not now and leaving the screen
// cancel it), navigate dropping the rest of the batch, another tab's chat read-only with Continue here, a server
// card's answer, the daily limit, a Remembered line's Undo, and model text that stays text.
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { useEffect, useState } from "react";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
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
const status = (remote = false, thinks = false): S<"AssistantStatus"> => ({
  enabled: true,
  set_up: true,
  can_set_up: false,
  first_launch: false,
  tools: [],
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
  thinks = false,
  existing,
  entries = [],
}: {
  remote?: boolean;
  thinks?: boolean;
  existing?: S<"AssistantThread">;
  entries?: S<"AssistantEntry">[];
} = {}) {
  const posts: S<"AssistantPost">[] = [];
  answer("GET", {
    "/v1/studio/assistant/threads": () => ({
      data: { status: status(remote, thinks), threads: existing ? [existing] : [] },
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

  // plan 32 (board frame 21): the dock always has Chat; off or not set up, the drawer shows a message, not a chat
  const off = (over: Partial<S<"AssistantStatus">>) =>
    answer("GET", {
      "/v1/studio/assistant/threads": () => ({
        data: { status: { ...status(), enabled: false, provider: null, ...over }, threads: [] },
      }),
    });
  const admin = { ...user, roles: ["user", "admin"] } as S<"User">;
  const drawer = () => screen.getByRole("complementary", { name: "Assistant" });

  it("off: Assistant stays in the dock and ⌘J opens the message; anyone else is told their admin did it, with no Hub", async () => {
    off({ set_up: true });
    render(<AppShell user={user}>x</AppShell>);
    await screen.findByRole("button", { name: "Assistant" });
    fireEvent.keyDown(window, { key: "j", metaKey: true });
    expect(within(drawer()).getByText("The Assistant is off")).toBeTruthy();
    expect(within(drawer()).getByText("Your admin turned it off.")).toBeTruthy();
    expect(within(drawer()).queryByRole("button", { name: "Turn on" })).toBeNull();
    expect(screen.queryByRole("textbox", { name: "Message the assistant" })).toBeNull();
    expect(within(drawer()).queryByRole("button", { name: "Hub" })).toBeNull();
  });

  it("not set up: anyone else asks their admin, no button; an admin gets Set up, which opens Hub's form", async () => {
    off({ set_up: false });
    const r = render(<AppShell user={user}>x</AppShell>);
    fireEvent.click(await screen.findByRole("button", { name: "Assistant" }));
    expect(within(drawer()).getByText("The Assistant isn't set up yet")).toBeTruthy();
    expect(within(drawer()).getByText("Ask your admin to set it up.")).toBeTruthy();
    expect(within(drawer()).getByText("Plan shots and a shot list")).toBeTruthy();
    expect(within(drawer()).queryByRole("button", { name: "Set up" })).toBeNull();
    r.unmount();
    sessionStorage.clear();
    off({ set_up: false, can_set_up: true });
    render(<AppShell user={admin}>x</AppShell>);
    fireEvent.click(await screen.findByRole("button", { name: "Assistant" }));
    expect(within(drawer()).getByText("Set up the Assistant")).toBeTruthy();
    expect(within(drawer()).getByText("Opens Hub.")).toBeTruthy();
    expect(within(drawer()).getByText("Your Claude subscription")).toBeTruthy();
    fireEvent.click(within(drawer()).getByRole("button", { name: "Set up" }));
    expect(push).toHaveBeenCalledWith("/hub?setup=1");
    expect(screen.queryByRole("complementary", { name: "Assistant" })).toBeNull();
  });

  it("off, an admin: Turn on saves it on as it was, and the chat follows without a reload", async () => {
    let on = false;
    answer("GET", {
      "/v1/studio/assistant/threads": () => ({
        data: {
          status: on ? status() : { ...status(), enabled: false, provider: null, can_set_up: true },
          threads: [],
        },
      }),
      "/v1/studio/admin/assistant": () => ({
        data: {
          enabled: false,
          provider: "deepseek",
          model: null,
          personal: false,
          web_search: false,
          search_provider: null,
          limits: status().limits,
          remote_enabled: true,
          updated_at: "2026-11-25T00:00:00Z",
        },
      }),
    });
    const put = answer("PUT", {
      "/v1/studio/admin/assistant": () => {
        on = true;
        return { data: {} };
      },
    });
    render(<AppShell user={admin}>x</AppShell>);
    fireEvent.click(await screen.findByRole("button", { name: "Assistant" }));
    expect(within(drawer()).getByText("Turn it on for everyone in Hub.")).toBeTruthy();
    fireEvent.click(within(drawer()).getByRole("button", { name: "Turn on" }));
    await waitFor(() => expect(put).toHaveBeenCalled());
    expect(put.mock.calls[0][1]).toEqual({
      body: {
        enabled: true,
        provider: "deepseek",
        model: null,
        web_search: false,
        search_provider: null,
      },
    });
    expect(await screen.findByText("Hi. What should we make?")).toBeTruthy();
  });

  it("first launch: it opens by itself once, says so, and marks it for this user", async () => {
    off({ set_up: false, can_set_up: true, first_launch: true });
    const mark = answer("POST", { "/v1/studio/assistant/first-launch": () => ({ status: 204 }) });
    render(<AppShell user={admin}>x</AppShell>);
    expect(await screen.findByText("Set up the Assistant")).toBeTruthy();
    expect(screen.getByText(/^Close it now and set up the Assistant later/)).toBeTruthy();
    expect(mark).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("button", { name: "Assistant" }).getAttribute("aria-expanded")).toBe(
      "true",
    );
    fireEvent.click(within(drawer()).getByRole("button", { name: /^Close/ }));
    expect(screen.queryByRole("complementary", { name: "Assistant" })).toBeNull();
    fireEvent.keyDown(window, { key: "j", metaKey: true });
    expect(within(drawer()).getByText("Opens Hub.")).toBeTruthy(); // reopened by you
    fireEvent.click(within(drawer()).getByRole("button", { name: "Hub" }));
    expect(push).toHaveBeenCalledWith("/hub");
  });

  it("the rail: anyone has the Assistant with ⌘J; Hub and System only for an admin, as a group", async () => {
    serve();
    const r = render(<AppShell user={user}>x</AppShell>);
    const chat = await screen.findByRole("button", { name: "Assistant" });
    expect(chat.getAttribute("title")).toMatch(/^Assistant \((⌘J|Ctrl J)\)$/);
    expect(screen.queryByRole("link", { name: "Hub" })).toBeNull();
    expect(screen.queryByRole("link", { name: "System" })).toBeNull();
    expect(screen.queryByRole("link", { name: "Plugins" })).toBeNull();
    r.unmount();
    render(<AppShell user={{ ...user, roles: ["user", "admin"] }}>x</AppShell>);
    const group = await screen.findByRole("group", { name: "Admin only" });
    expect(within(group).getByRole("link", { name: "Hub" }).getAttribute("href")).toBe("/hub");
    expect(within(group).getByRole("link", { name: "System" }).getAttribute("href")).toBe(
      "/system",
    );
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
    expect(screen.getByText("Waiting")).toBeTruthy();
    expect(yes).not.toHaveBeenCalled();
    expect(posts).toHaveLength(0);
    // Undo names its line, on hover and to screen readers (alpha.2 item 23, board frame 14c)
    const un = screen.getByRole("button", { name: "Undo: Set Lighting: Blue hour" });
    expect(un.title).toBe("Undo: Set Lighting: Blue hour");
    fireEvent.click(un);
    await waitFor(() => expect(undo).toHaveBeenCalledTimes(1));
    fireEvent.click(await screen.findByRole("button", { name: "Redo: Set Lighting: Blue hour" }));
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

  it("a later action acts on the screen as the earlier ones left it, not on the old render", async () => {
    const posts = serve({ existing: thread() });
    function Switching() {
      const [w, setW] = useState("Echo");
      useScreenTools({
        screen: "create",
        sees: [w],
        summary: () => ({ workflow: w }),
        actions: {
          switchTo: async ({ workflow }) => {
            await Promise.resolve(); // as a real switch: the new render comes after the action returns
            setW(String(workflow));
            return done(`Switched to ${String(workflow)}`);
          },
          change: () => done(`Changed on ${w}`),
        },
      });
      return null;
    }
    await openDrawer(<Switching />);
    await waitFor(() => expect(onEvent).not.toBeNull());
    park([
      { id: id(0), name: "switchTo", args: { workflow: "Qwen Image" } },
      { id: id(1), name: "change", args: { key: "prompt", value: "x" } },
    ]);
    await waitFor(() => expect(posts).toHaveLength(1));
    const r = posts[0] as Extract<S<"AssistantPost">, { kind: "results" }>;
    expect(r.results[1].line.text).toBe("Changed on Qwen Image");
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
      results: [{ status: "failed", line: { text: "Can't go there · System is for admins only" } }],
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

  it("a cap hit is checked against the caps read after it: lowered stays shut, raised opens", async () => {
    serve({ existing: thread() }); // the status read first says 200 a day
    await openDrawer();
    await waitFor(() => expect(onEvent).not.toBeNull());
    let perDay = 1; // the admin lowered it to 1 meanwhile
    answer("GET", {
      "/v1/studio/assistant/threads": () => ({
        data: {
          status: { ...status(), limits: { ...status().limits, model_calls_per_day: perDay } },
          threads: [thread()],
        },
      }),
    });
    const hit = (n: number) =>
      act(() =>
        onEvent!({
          seq: 40 + n,
          type: "entry",
          entry: entry(n, {
            kind: "notice",
            text: "You used today's 1 model calls.",
            error: {
              code: "cap_day",
              text: "You used today's 1 model calls.",
              resets_at: "2999-01-01T00:00:00Z",
              limit: 1,
            },
          }),
        }),
      );
    hit(3);
    const box = screen.getByRole("textbox", {
      name: "Message the assistant",
    }) as HTMLTextAreaElement;
    expect(box.disabled).toBe(true);
    await act(() => new Promise((r) => setTimeout(r, 50))); // the caps read again
    expect(box.disabled).toBe(true);
    perDay = 2000; // raised: the next read opens it
    hit(4);
    await waitFor(() => expect(box.disabled).toBe(false));
  });

  it("a failed line and a provider's error say it in plain words, the raw text under Details", async () => {
    serve({
      existing: thread(),
      entries: [
        entry(1, {
          kind: "line",
          line: {
            id: id(0),
            status: "failed",
            text: "switchTo · name resolution failed",
            undo: false,
          },
        }),
        entry(2, {
          kind: "notice",
          text: "deepseek: the provider is busy or down (HTTP 429)",
          error: { code: "provider", text: "deepseek: the provider is busy or down (HTTP 429)" },
        }),
      ],
    });
    await openDrawer();
    expect(
      await screen.findByText("Lost the connection to the studio for a moment; try again"),
    ).toBeTruthy();
    expect(screen.getByText("deepseek is busy. Try again in a minute.")).toBeTruthy();
    expect(screen.queryByText(/switchTo|HTTP 429/)).toBeNull();
    const [line, note] = screen.getAllByRole("button", { name: "Details" });
    fireEvent.click(line);
    expect(screen.getByText("switchTo · name resolution failed")).toBeTruthy();
    fireEvent.click(note);
    expect(screen.getByText("deepseek: the provider is busy or down (HTTP 429)")).toBeTruthy();
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
    fireEvent.click(await screen.findByRole("button", { name: /^Undo: Remembered/ }));
    await waitFor(() =>
      expect(del).toHaveBeenCalledWith("/v1/studio/assistant/memory/{item_id}", {
        params: { path: { item_id: mem } },
      }),
    );
    expect(await screen.findByText("Remembered: you like 16:9")).toBeTruthy();
    expect(screen.queryByRole("button", { name: /^Undo/ })).toBeNull();
  });

  it("a plugin line's Undo deletes the takes it made (API 1.70), with no Redo", async () => {
    const made = "99999999-2222-4333-8444-666666666666";
    serve({
      existing: thread(),
      entries: [
        entry(1, {
          kind: "line",
          line: { id: id(0), status: "done", text: "Cropped · Fox", undo: true, take_ids: [made] },
        }),
      ],
    });
    const del = vi.spyOn(api, "DELETE").mockResolvedValue({
      data: undefined,
      error: undefined,
      response: new Response(null, { status: 204 }),
    } as never);
    await openDrawer();
    fireEvent.click(await screen.findByRole("button", { name: /^Undo: Cropped/ }));
    await waitFor(() =>
      expect(del).toHaveBeenCalledWith("/v1/studio/takes/{take_id}", {
        params: { path: { take_id: made } },
      }),
    );
    await waitFor(() => expect(screen.queryByRole("button", { name: /^(Undo|Redo)/ })).toBeNull());
    expect(screen.getByText("Undone")).toBeTruthy(); // board MEDIA TOOLS: the line says Undone
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

  it("a not-installed line links to Models", async () => {
    serve({
      existing: thread(),
      entries: [
        entry(1, {
          kind: "line",
          line: {
            id: id(0),
            status: "failed",
            text: "LTX 2.3 is not installed. Get it in Models.",
            undo: false,
          },
        }),
      ],
    });
    await openDrawer();
    const lines = await screen.findByRole("list", { name: "What it did" });
    const link = within(lines).getByRole("link", { name: "Models" });
    expect(link.getAttribute("href")).toBe("/models");
    expect(link.closest("li")?.textContent).toBe("LTX 2.3 is not installed. Get it in Models.");
  });

  it("Escape closes the drawer when focus is in it, but not while a dialog is open", async () => {
    HTMLDialogElement.prototype.showModal ??= function (this: HTMLDialogElement) {
      this.open = true;
    };
    serve();
    await openDrawer();
    const drawer = () => screen.queryByRole("complementary", { name: "Assistant" });
    fireEvent.click(screen.getByRole("button", { name: "Expand" }));
    fireEvent.keyDown(screen.getByRole("dialog", { name: "Message" }), { key: "Escape" });
    expect(drawer()).not.toBeNull();
    screen.getByRole("dialog", { name: "Message" }).removeAttribute("open");
    fireEvent.keyDown(screen.getByRole("textbox", { name: "Message the assistant" }), {
      key: "Escape",
    });
    expect(drawer()).toBeNull();
  });

  it("shows **bold** as bold and drops `code` ticks", async () => {
    serve({
      existing: thread(),
      entries: [entry(1, { kind: "assistant", text: "Add **references** on the `fake` host" })],
    });
    await openDrawer(<Screen actions={{}} />);
    expect((await screen.findByText("references")).tagName).toBe("B");
    expect(screen.getByText("fake").tagName).toBe("CODE");
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

describe("older chats and a broken answer (board frame 7)", () => {
  const tid = (n: number) => `${T.slice(0, -1)}${n}`;
  const today = (h: number, m: number) => {
    const d = new Date();
    d.setHours(h, m, 0, 0);
    return d.toISOString();
  };
  /** Seven chats, newest first: this one, a waiting one, five older; returns the bodies POSTed to …/messages. */
  function serveChats() {
    const titles = [
      "Rain still: blue hour",
      "Harbour clip for the opening",
      "Why did the batch fail?",
      "Bus stop scene, 3 shots",
      "Narrator voice",
      "Sixth chat",
      "Seventh chat",
    ];
    const list = titles.map((title, i) =>
      thread({
        id: tid(i),
        title,
        updated_at: today(14, 2 - i),
        ...(i === 1 ? { state: "waiting" as const, client_id: ME } : {}),
      }),
    );
    const posts: { id: string; body: S<"AssistantPost"> }[] = [];
    answer("GET", {
      "/v1/studio/assistant/threads": () => ({ data: { status: status(), threads: list } }),
      "/v1/studio/assistant/threads/{thread_id}": ({ params }) => {
        const tid = (params as { path: { thread_id: string } }).path.thread_id;
        const t = list.find((x) => x.id === tid)!;
        return { data: { ...t, entries: [entry(1, { kind: "user", text: `${t.title}?` })] } };
      },
    });
    answer("POST", {
      "/v1/studio/assistant/threads/{thread_id}/messages": ({ params, body }) => {
        const tid = (params as { path: { thread_id: string } }).path.thread_id;
        posts.push({ id: tid, body: body as S<"AssistantPost"> });
        return {
          status: 202,
          data: { thread: thread({ id: tid, state: "working" }), entry: null },
        };
      },
    });
    return posts;
  }

  it("Chats replaces New chat: newest first, this one Open, a waiting one Waiting, Show older after 5", async () => {
    serveChats();
    await openDrawer();
    expect(screen.queryByRole("button", { name: "New chat" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Chats" }));
    const menu = await screen.findByRole("menu", { name: "Chats" });
    expect(within(menu).getByText("newest first")).toBeTruthy();
    expect(within(menu).getAllByRole("menuitem")[0].textContent).toBe("New chat");
    const rows = () => within(menu).getAllByRole("menuitemradio");
    await waitFor(() => expect(rows()).toHaveLength(5));
    expect(rows()[0].textContent).toBe("Rain still: blue hourtoday · 14:02Open");
    expect(rows()[0].getAttribute("aria-checked")).toBe("true");
    expect(rows()[1].textContent).toContain("Waiting");
    fireEvent.click(within(menu).getByRole("menuitem", { name: "Show older" }));
    expect(rows()).toHaveLength(7);
    // Escape closes the list, not the drawer
    fireEvent.keyDown(menu, { key: "Escape" });
    expect(screen.queryByRole("menu")).toBeNull();
    expect(screen.getByRole("complementary", { name: "Assistant" })).toBeTruthy();
    // a click outside closes it too
    fireEvent.click(screen.getByRole("button", { name: "Chats" }));
    await screen.findByRole("menu", { name: "Chats" });
    fireEvent.pointerDown(document.body);
    expect(screen.queryByRole("menu")).toBeNull();
  });

  it("the Chats list moves by keys: arrows wrap, Home and End jump, Esc gives focus back to Chats", async () => {
    serveChats();
    await openDrawer();
    fireEvent.click(screen.getByRole("button", { name: "Chats" }));
    const menu = await screen.findByRole("menu", { name: "Chats" });
    await waitFor(() => expect(within(menu).getAllByRole("menuitemradio")).toHaveLength(5));
    const items = () => [...menu.querySelectorAll<HTMLElement>("[role^=menuitem]")];
    expect(document.activeElement).toBe(items()[0]);
    fireEvent.keyDown(menu, { key: "ArrowDown" });
    expect(document.activeElement).toBe(items()[1]);
    fireEvent.keyDown(menu, { key: "ArrowUp" });
    fireEvent.keyDown(menu, { key: "ArrowUp" });
    expect(document.activeElement).toBe(items().at(-1)); // Show older
    fireEvent.keyDown(menu, { key: "Home" });
    expect(document.activeElement).toBe(items()[0]);
    fireEvent.keyDown(menu, { key: "End" });
    expect(document.activeElement?.textContent).toBe("Show older");
    fireEvent.keyDown(menu, { key: "Escape" });
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "Chats" }));
  });

  it("an older chat picked brings its whole thread back, and writing in it goes to that chat", async () => {
    const posts = serveChats();
    await openDrawer(<Screen actions={{}} />);
    await screen.findByText("Rain still: blue hour?");
    fireEvent.click(screen.getByRole("button", { name: "Chats" }));
    const menu = await screen.findByRole("menu", { name: "Chats" });
    fireEvent.click(await within(menu).findByRole("menuitemradio", { name: /^Bus stop scene/ }));
    expect(await screen.findByText("Bus stop scene, 3 shots?")).toBeTruthy();
    expect(screen.queryByRole("menu")).toBeNull();
    const box = screen.getByRole("textbox", { name: "Message the assistant" });
    fireEvent.change(box, { target: { value: "Same idea, but a still" } });
    fireEvent.keyDown(box, { key: "Enter" });
    await waitFor(() => expect(posts).toHaveLength(1));
    expect(posts[0]).toMatchObject({
      id: tid(3),
      body: { kind: "message", text: "Same idea, but a still" },
    });
    expect((posts[0].body as { screen: S<"AssistantScreen"> }).screen.fields).toMatchObject({
      screen: "create",
    });
  });

  it("New chat is the list's first row", async () => {
    serveChats();
    const made = vi.fn();
    await openDrawer();
    answer("POST", {
      "/v1/studio/assistant/threads": () => {
        made();
        return { status: 201, data: thread({ id: tid(9), client_id: ME }) };
      },
    });
    fireEvent.click(screen.getByRole("button", { name: "Chats" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "New chat" }));
    await waitFor(() => expect(made).toHaveBeenCalled());
    expect(await screen.findByText("Hi. What should we make?")).toBeTruthy();
  });

  it("a broken answer twice: the plain line, Retry, and the checker's words behind Details", async () => {
    const posts = serve({
      existing: thread(),
      entries: [
        entry(1, { kind: "user", text: "Launch the rockets" }),
        // a steer the turn read is not the message Retry sends
        entry(3, { kind: "user", text: "and fast", steer: "read" }),
        entry(2, {
          kind: "notice",
          text: "The answer was invalid twice: launch_rockets is not one of your tools now",
          error: {
            code: "answer",
            text: "The answer was invalid twice: launch_rockets is not one of your tools now",
          },
        }),
      ],
    });
    await openDrawer();
    const note = await screen.findByRole("note");
    expect(within(note).getByText("The model gave a broken answer twice.")).toBeTruthy();
    // Retry on the last broken answer sends your last message again (alpha.2 item 24)
    const retry = within(note).getByRole("button", { name: "Retry" });
    fireEvent.click(retry);
    fireEvent.click(retry); // a double click sends it once
    await waitFor(() =>
      expect(posts).toContainEqual(
        expect.objectContaining({ kind: "message", text: "Launch the rockets" }),
      ),
    );
    expect(posts.filter((p) => p.kind === "message")).toHaveLength(1);
    expect(within(note).queryByRole("button", { name: "Retry" })).toBeNull();
    const details = within(note).getByRole("button", { name: "Details" });
    expect(details.getAttribute("aria-expanded")).toBe("false");
    expect(within(note).queryByText(/launch_rockets/)).toBeNull();
    const seen = vi.spyOn(Element.prototype, "scrollIntoView");
    seen.mockClear();
    fireEvent.click(details);
    expect(details.getAttribute("aria-expanded")).toBe("true");
    // opened on the last message, the words come into view rather than below the composer
    await waitFor(() =>
      expect(seen.mock.contexts).toContain(within(note).getByText(/launch_rockets/)),
    );
    seen.mockRestore();
    expect(
      within(note).getByText(
        "The answer was invalid twice: launch_rockets is not one of your tools now",
      ).className,
    ).toContain("mono");
  });
});

describe("thinking (plan 29)", () => {
  const to = (state: S<"AssistantThread">["state"], seq: number, over = {}) =>
    act(() => onEvent!({ seq, type: "thread", thread: thread({ state, client_id: ME, ...over }) }));
  afterEach(() => vi.useRealTimers());

  // the bubble is in the thread (under the last message), never in the header; while the chip shows it takes the
  // title's place (alpha.2, board frame 9)
  const bubble = () => {
    const b = screen.queryByText(/^Thinking…/);
    if (b) {
      expect(b.closest(".thr")).toBeTruthy();
      expect(b.closest(".dh")).toBeNull();
      expect(b.className).toContain("msg");
    }
    return b;
  };
  const title = () =>
    screen.getByRole("heading", { level: 2, name: "Assistant" }).querySelector("span");

  it("counts up while the model call runs, Working while actions run, gone when the step lands", async () => {
    serve({
      thinks: true,
      existing: thread({ client_id: ME }),
      entries: [entry(1, { kind: "user", text: "Make it blue hour" })],
    });
    let finish = () => undefined as void;
    const change = () =>
      new Promise<ReturnType<typeof done>>((r) => (finish = () => r(done("Set"))));
    await openDrawer(<Screen actions={{ change }} />);
    await waitFor(() => expect(onEvent).not.toBeNull());
    vi.useFakeTimers();
    await act(async () => undefined);
    to("working", 20);
    expect(bubble()?.textContent).toBe("Thinking… 0 s");
    // under the user's last message, where the reply lands
    const mine = screen.getByText("Make it blue hour").closest(".msg")!;
    expect(mine.compareDocumentPosition(bubble()!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    // the chip stays Working, in the title's place
    expect(screen.getByText("Working")).toBeTruthy();
    expect(title()).toBeNull();
    act(() => vi.advanceTimersByTime(3000));
    expect(bubble()?.textContent).toBe("Thinking… 3 s");
    // parked on this tab's actions: no bubble, today's chip
    park([{ id: id(0), name: "change", args: {} }]);
    expect(bubble()).toBeNull();
    expect(screen.getByText("Working")).toBeTruthy();
    // the actions done, the turn resumes: a new count from 0
    await act(async () => finish());
    to("working", 30);
    expect(bubble()?.textContent).toBe("Thinking… 0 s");
    act(() => vi.advanceTimersByTime(1000));
    expect(bubble()?.textContent).toBe("Thinking… 1 s");
    // the reply lands and replaces it
    act(() =>
      onEvent!({
        seq: 31,
        type: "entry",
        entry: entry(9, { kind: "assistant", text: "Done: blue hour" }),
      }),
    );
    to("idle", 32);
    expect(bubble()).toBeNull();
    const drawer = screen.getByRole("complementary", { name: "Assistant" });
    expect(within(drawer).getByText("Done: blue hour")).toBeTruthy();
    // screen readers hear the new words alone, from a region outside the thread (alpha.2 item 23)
    const live = document.querySelector("[aria-live=polite]")!;
    expect(live.textContent).toBe("Done: blue hour");
    expect(drawer.contains(live)).toBe(false);
    expect(drawer.querySelector("[aria-live=polite]")).toBeNull();
  });

  it("Retry after steers sent as the next message sends them together", async () => {
    const posts = serve({
      existing: thread(),
      entries: [
        entry(1, { kind: "user", text: "Make it rain" }),
        entry(2, { kind: "user", text: "add a bus", steer: "next" }),
        entry(3, { kind: "user", text: "and night", steer: "next" }),
        entry(4, { kind: "notice", text: "x", error: { code: "answer", text: "x" } }),
      ],
    });
    await openDrawer();
    fireEvent.click(await screen.findByRole("button", { name: "Retry" }));
    await waitFor(() =>
      expect(posts).toContainEqual(
        expect.objectContaining({ kind: "message", text: "add a bus\n\nand night" }),
      ),
    );
  });

  it("closing and reopening the drawer keeps the count", async () => {
    serve({ thinks: true, existing: thread({ client_id: ME }) });
    await openDrawer();
    await waitFor(() => expect(onEvent).not.toBeNull());
    vi.useFakeTimers();
    to("working", 20);
    act(() => vi.advanceTimersByTime(4000));
    fireEvent.click(screen.getByRole("button", { name: /^Close \(/ }));
    act(() => vi.advanceTimersByTime(3000));
    fireEvent.click(screen.getByRole("button", { name: /^Assistant/ }));
    expect(bubble()?.textContent).toBe("Thinking… 7 s");
  });

  it("Working… N s when the model does not think", async () => {
    serve({ existing: thread({ client_id: ME }) });
    await openDrawer();
    await waitFor(() => expect(onEvent).not.toBeNull());
    vi.useFakeTimers();
    to("working", 20);
    expect(screen.getByText("Working")).toBeTruthy();
    expect(screen.queryByText(/^Thinking/)).toBeNull();
    act(() => vi.advanceTimersByTime(2000));
    const b = screen.getByText(/^Working… /);
    expect(b.textContent).toBe("Working… 2 s");
    expect(b.closest(".thr")).toBeTruthy();
    // the chip takes the title's place (alpha.2, board frame 9)
    expect(title()).toBeNull();
  });

  it("another tab's working chat stays Read-only", async () => {
    serve({ thinks: true, existing: thread({ state: "working", client_id: "tabB00000001" }) });
    await openDrawer();
    expect(await screen.findByText("Read-only")).toBeTruthy();
    expect(screen.queryByText(/^Thinking/)).toBeNull();
  });
});

describe("steer while it answers (board frame 8)", () => {
  const working = thread({ state: "working", client_id: ME });
  /** Serves a working chat; a message posted is stored as a steer waiting for the next step. */
  function serveSteer(entries: S<"AssistantEntry">[] = []) {
    const posts: S<"AssistantPost">[] = [];
    serve({ existing: working, entries });
    answer("POST", {
      "/v1/studio/assistant/threads/{thread_id}/messages": ({ body }) => {
        const b = body as S<"AssistantPost">;
        posts.push(b);
        const text = "text" in b ? b.text : "";
        return {
          status: 202,
          data: {
            thread: working,
            steer: b.kind === "message",
            entry:
              b.kind === "message"
                ? entry(posts.length + 4, {
                    kind: "user",
                    text,
                    steer: "waiting",
                    at: "2026-11-22T00:00:05Z",
                  })
                : null,
          },
        };
      },
    });
    return posts;
  }
  const box = () => screen.getByRole("textbox", { name: "Message the assistant" });
  const send = async (text: string) => {
    fireEvent.change(box(), { target: { value: text } });
    fireEvent.keyDown(box(), { key: "Enter" });
    await screen.findByText(text);
  };

  it("a · while it works the box stays open: Steer the answer…, Enter steers, and Stop", async () => {
    serveSteer([entry(1, { kind: "user", text: "Make a rainy London still" })]);
    await openDrawer();
    await waitFor(() => expect(box().getAttribute("placeholder")).toBe("Steer the answer…"));
    expect((box() as HTMLTextAreaElement).disabled).toBe(false);
    expect(screen.getByText("steers").textContent).toBe("Enter steers");
    expect(screen.getByRole("button", { name: "Stop" })).toBeTruthy();
    // idle: as before
    act(() =>
      onEvent!({
        seq: 40,
        type: "thread",
        thread: thread({ client_id: ME, updated_at: "2026-11-22T00:01:00Z" }),
      }),
    );
    expect(box().getAttribute("placeholder")).toBe("Ask, or say what to do");
    expect(screen.queryByRole("button", { name: "Stop" })).toBeNull();
    expect(screen.queryByText(/^steers$/)).toBeNull();
  });

  it("b · sent: shown at once at the bottom, dimmed, at the next step; several wait in order", async () => {
    const posts = serveSteer([entry(1, { kind: "user", text: "Make a rainy London still" })]);
    const change = vi.fn(() => new Promise<ReturnType<typeof done>>(() => undefined)); // still running
    await openDrawer(<Screen actions={{ change }} />);
    await waitFor(() => expect(onEvent).not.toBeNull());
    park([{ id: id(0), name: "change", args: { key: "prompt", value: "rain" } }]);
    await screen.findByText("Change…");
    await send("Use Turbo instead");
    await send("And make it 16:9");
    expect(posts.map((p) => p.kind === "message" && p.text)).toEqual([
      "Use Turbo instead",
      "And make it 16:9",
    ]);
    const waiting = screen.getAllByLabelText("Waiting for the next step");
    expect(waiting.map((w) => w.firstChild?.textContent)).toEqual([
      "Use Turbo instead",
      "And make it 16:9",
    ]);
    expect(waiting[0].className).toContain("steer");
    expect(waiting[0].textContent).toContain("at the next step");
    // under the running line
    const running = screen.getByText("Change…");
    expect(
      running.compareDocumentPosition(waiting[0]) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

  it("c · read: the sub-line goes and it moves to where it was read", async () => {
    serveSteer([
      entry(1, { kind: "user", text: "Make a rainy London still" }),
      entry(2, {
        kind: "user",
        text: "Use Turbo instead",
        steer: "waiting",
        at: "2026-11-22T00:00:01Z",
      }),
      entry(3, {
        kind: "line",
        line: { id: id(0), status: "done", text: "Set model: Qwen Image", undo: true },
        at: "2026-11-22T00:00:02Z",
      }),
    ]);
    await openDrawer();
    await screen.findByLabelText("Waiting for the next step");
    act(() =>
      onEvent!({
        seq: 50,
        type: "entry",
        entry: entry(2, {
          kind: "user",
          text: "Use Turbo instead",
          steer: "read",
          at: "2026-11-22T00:00:03Z",
        }),
      }),
    );
    expect(screen.queryByLabelText("Waiting for the next step")).toBeNull();
    expect(screen.queryByText("at the next step")).toBeNull();
    const line = screen.getByText("Set model: Qwen Image");
    const read = screen.getByText("Use Turbo instead");
    expect(line.compareDocumentPosition(read) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("d · it finished first: sent as the next message", async () => {
    serveSteer([
      entry(1, { kind: "user", text: "Make a rainy London still" }),
      entry(2, { kind: "assistant", text: "Done. Check the prompt on the left." }),
      entry(3, {
        kind: "user",
        text: "Add a red bus behind her",
        steer: "next",
        at: "2026-11-22T00:00:04Z",
      }),
    ]);
    await openDrawer();
    const next = (await screen.findByText("Add a red bus behind her")).closest(".msg")!;
    expect(next.textContent).toContain("sent as the next message");
    expect(next.className).not.toContain("steer");
  });

  it("Stop on the desktop composer stops the running turn", async () => {
    const posts = serveSteer();
    await openDrawer();
    fireEvent.click(await screen.findByRole("button", { name: "Stop" }));
    await waitFor(() => expect(posts).toEqual([{ kind: "stop", client_id: ME }]));
  });

  it("Stopping… (board frame 15): chip, bubble and Stop read it at once, focus to the box, until idle", async () => {
    const posts = serveSteer([entry(1, { kind: "user", text: "Make it blue hour" })]);
    await openDrawer();
    fireEvent.click(await screen.findByRole("button", { name: "Stop" }));
    const d = screen.getByRole("complementary", { name: "Assistant" });
    const stop = within(d).getByRole("button", { name: "Stopping…" }) as HTMLButtonElement;
    expect(stop.disabled).toBe(true);
    expect(d.querySelector(".st")!.textContent).toBe("Stopping…");
    const bubble = within(d).getByRole("status", { name: "" });
    expect(bubble.textContent).toBe("Stopping…");
    expect(d.textContent).not.toMatch(/Working…|Thinking…/);
    expect(document.activeElement).toBe(box());
    await waitFor(() => expect(posts).toEqual([{ kind: "stop", client_id: ME }]));
    // minimized, the bar says it too
    fireEvent.click(screen.getByRole("button", { name: "Minimize" }));
    expect(
      screen.getByRole("button", { name: "Open the Assistant · Stopping" }).textContent,
    ).toContain("Stopping…");
    fireEvent.click(screen.getByRole("button", { name: "Open the Assistant · Stopping" }));
    // idle: no chip, no bubble, no Stopped line; the thread ends at your message
    act(() =>
      onEvent!({
        seq: 70,
        type: "thread",
        thread: thread({ client_id: ME, updated_at: "2026-11-22T00:03:00Z" }),
      }),
    );
    await waitFor(() => expect(screen.queryByText("Stopping…")).toBeNull());
    expect(screen.queryByRole("button", { name: /^Stop/ })).toBeNull();
    expect(screen.queryByText(/Stopped/)).toBeNull();
    expect(box().getAttribute("placeholder")).toBe("Ask, or say what to do");
  });

  it("Stop's reply never takes back a newer state (the turn ended first)", async () => {
    serve({ existing: working });
    answer("POST", {
      "/v1/studio/assistant/threads/{thread_id}/messages": () => {
        // the turn's end arrives before the reply, whose snapshot is the working one
        onEvent!({
          seq: 60,
          type: "thread",
          thread: thread({ client_id: ME, updated_at: "2026-11-22T00:02:00Z" }),
        });
        return { status: 202, data: { thread: working, entry: null } };
      },
    });
    await openDrawer();
    fireEvent.click(await screen.findByRole("button", { name: "Stop" }));
    await waitFor(() => expect(screen.queryByRole("button", { name: "Stop" })).toBeNull());
    await new Promise((r) => setTimeout(r, 20));
    expect(screen.queryByRole("button", { name: "Stop" })).toBeNull();
    expect(box().getAttribute("placeholder")).toBe("Ask, or say what to do");
  });

  it("Stop just before a park: that batch does not run", async () => {
    const posts = serveSteer();
    const change = vi.fn(() => done("Set prompt"));
    await openDrawer(<Screen actions={{ change }} />);
    fireEvent.click(await screen.findByRole("button", { name: "Stop" }));
    await waitFor(() => expect(posts).toHaveLength(1));
    park([{ id: id(0), name: "change", args: { key: "prompt", value: "rain" } }]);
    await waitFor(() => expect(posts).toHaveLength(2));
    expect(change).not.toHaveBeenCalled();
    const r = posts[1] as Extract<S<"AssistantPost">, { kind: "results" }>;
    expect(r.results).toMatchObject([
      { status: "did_not_run", line: { text: "Did not run · you stopped it" } },
    ]);
  });
});

describe("alpha.2: size, answers that read well, polish (board frames 9, 10, 14)", () => {
  const drawer = () => screen.queryByRole("complementary", { name: "Assistant" });
  const bar = (name: string | RegExp) => screen.getByRole("button", { name });

  it("full screen, back with Esc or ⇧⌘J; Minimize to the bar, which opens it as left; Close ends both", async () => {
    serve();
    await openDrawer();
    fireEvent.click(screen.getByRole("button", { name: "Full screen (Ctrl Shift J)" }));
    expect(drawer()!.className).toContain("full");
    expect(screen.getByRole("button", { name: "Back to the drawer (Ctrl Shift J)" })).toBeTruthy();
    fireEvent.keyDown(drawer()!, { key: "Escape" });
    expect(drawer()!.className).not.toContain("full");
    fireEvent.keyDown(window, { key: "J", ctrlKey: true, shiftKey: true });
    expect(drawer()!.className).toContain("full");
    fireEvent.keyDown(window, { key: "J", ctrlKey: true, shiftKey: true });
    expect(drawer()!.className).not.toContain("full");
    fireEvent.click(screen.getByRole("button", { name: "Minimize" }));
    expect(drawer()).toBeNull();
    // the focus moves to the bar, not lost on the page
    expect(document.activeElement).toBe(bar("Open the Assistant"));
    fireEvent.click(bar("Open the Assistant"));
    expect(drawer()).toBeTruthy();
    // opened from the bar, Esc and ⌘J go back to it
    fireEvent.keyDown(drawer()!, { key: "Escape" });
    expect(bar("Open the Assistant")).toBeTruthy();
    fireEvent.keyDown(window, { key: "j", ctrlKey: true });
    expect(drawer()).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: /^Close \(/ }));
    expect(drawer()).toBeNull();
    expect(screen.queryByRole("button", { name: /^Open the Assistant/ })).toBeNull();
  });

  it("a page's side form opening under the drawer minimizes it; one beside the drawer does not", async () => {
    serve();
    await openDrawer();
    const beside = document.createElement("aside");
    beside.className = "aside beside-drawer";
    document.body.append(beside);
    await waitFor(() => expect(drawer()).toBeTruthy());
    const side = document.createElement("aside");
    side.className = "aside";
    document.body.append(side);
    await waitFor(() => expect(drawer()).toBeNull());
    expect(bar("Open the Assistant")).toBeTruthy();
    beside.remove();
    side.remove();
  });

  it("a page's own side panel arriving with a route change leaves it open", async () => {
    serve();
    const r = await openDrawer();
    path = "/gallery";
    r.rerender(
      <AppShell user={user}>
        <aside className="aside" />
      </AppShell>,
    );
    await new Promise((f) => setTimeout(f, 50));
    expect(drawer()).toBeTruthy();
  });

  it("the bar's states: Working with Stop, Waiting for you and what, a new answer", async () => {
    const posts = serve({ existing: thread({ state: "working", client_id: ME }) });
    await openDrawer();
    // while it works, the chip takes the title's place
    expect(screen.getByText("Working")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Minimize" }));
    expect(bar("Open the Assistant · Working").textContent).toContain("Working…");
    fireEvent.click(screen.getByRole("button", { name: "Stop" }));
    await waitFor(() => expect(posts).toContainEqual(expect.objectContaining({ kind: "stop" })));
    const card = (state: S<"AssistantCard">["state"]): S<"AssistantCard"> => ({
      id: id(1),
      kind: "delete",
      title: "Delete the take",
      rows: [],
      tags: [],
      state,
    });
    act(() =>
      onEvent!({
        seq: 40,
        type: "entry",
        entry: entry(2, { kind: "card", card: card("waiting") }),
      }),
    );
    act(() =>
      onEvent!({
        seq: 41,
        type: "thread",
        thread: thread({ state: "waiting", client_id: ME, updated_at: "2026-11-22T00:01:00Z" }),
      }),
    );
    expect(bar("Open the Assistant · Waiting for you").textContent).toContain("Delete the take?");
    act(() =>
      onEvent!({
        seq: 42,
        type: "thread",
        thread: thread({ state: "idle", updated_at: "2026-11-22T00:02:00Z" }),
      }),
    );
    act(() =>
      onEvent!({
        seq: 43,
        type: "entry",
        entry: entry(3, { kind: "assistant", turn: 2, text: "**Done**: 4 takes. Take 2 is best." }),
      }),
    );
    // a card that waits still wins; once answered, the new answer shows
    act(() =>
      onEvent!({ seq: 44, type: "entry", entry: entry(2, { kind: "card", card: card("yes") }) }),
    );
    expect(bar("Open the Assistant · new answer").textContent).toContain("Done: 4 takes.");
    fireEvent.click(bar("Open the Assistant · new answer"));
    fireEvent.click(screen.getByRole("button", { name: "Minimize" }));
    expect(bar("Open the Assistant").textContent).toBe("Assistant");
  });

  it("formats answers as plain elements: bold, italic, lists, code with Copy; links and headings as text", async () => {
    const copied = vi.fn(() => Promise.resolve());
    Object.defineProperty(navigator, "clipboard", {
      value: { writeText: copied },
      configurable: true,
    });
    serve({
      existing: thread(),
      entries: [
        entry(1, {
          kind: "assistant",
          text: [
            "## Name the move first",
            "**Slow** beats *very slow*, see [the guide](https://evil.test/x).",
            "- One subject",
            "- 5 s or less",
            "1. Open Video",
            "2. Set Camera to `Dolly in`",
            "| a | b |",
            "```prompt",
            "Slow dolly in. <img src=x onerror=alert(1)>",
            "```",
          ].join("\n"),
        }),
      ],
    });
    await openDrawer();
    const d = drawer()!;
    expect((await within(d).findByText("Slow")).tagName).toBe("B");
    expect(within(d).getByText("very slow").tagName).toBe("I");
    expect(within(d).getByText("Dolly in").tagName).toBe("CODE");
    expect(within(d).getByText("One subject").closest("ul")!.className).toBe("md");
    expect(
      within(d)
        .getByText(/Open Video/)
        .closest("ol"),
    ).toBeTruthy();
    expect(d.querySelectorAll("h1, h2, h3, table, a[href*='evil']")).toHaveLength(1); // the header's h2
    expect(within(d).getByText("Name the move first").tagName).toBe("P");
    expect(d.textContent).toContain("see the guide.");
    expect(d.textContent).not.toContain("evil.test");
    expect(within(d).getByText("| a | b |")).toBeTruthy();
    expect(d.querySelector("img")).toBeNull();
    expect(d.querySelector("pre")!.textContent).toBe("Slow dolly in. <img src=x onerror=alert(1)>");
    fireEvent.click(within(d).getByRole("button", { name: "Copy the prompt" }));
    await waitFor(() =>
      expect(copied).toHaveBeenCalledWith("Slow dolly in. <img src=x onerror=alert(1)>"),
    );
    fireEvent.click(within(d).getByRole("button", { name: "Copy the answer" }));
    await waitFor(() => expect(copied).toHaveBeenCalledTimes(2));
    const all = (copied.mock.calls.at(-1) as unknown as [string])[0];
    expect(all).toContain("Slow beats *very slow*, see the guide.");
    expect(all).not.toContain("**");
    expect((await within(d).findAllByText("Copied")).length).toBeGreaterThan(0);
  });

  it("a plain checklist is a plan; timecodes jump the playhead on the Timeline, text elsewhere", async () => {
    const text = "Plan:\n- [x] Keyframe for each shot\n- [ ] Place it at 0:26";
    serve({ existing: thread(), entries: [entry(1, { kind: "assistant", text })] });
    const r = await openDrawer(<Screen actions={{}} />);
    const plan = await screen.findByRole("list", { name: "Plan" });
    const items = within(plan).getAllByRole("listitem");
    expect(items.map((x) => x.textContent)).toEqual([
      "Done: Keyframe for each shot",
      "To do: Place it at 0:26",
    ]);
    expect(items[1].className).toBe("todo");
    expect(screen.queryByRole("button", { name: /^Jump the Timeline/ })).toBeNull();
    r.unmount();
    sessionStorage.clear(); // it would come back open
    path = "/timeline";
    const setPlayhead = vi.fn(() => done("Moved"));
    function Timeline() {
      useScreenTools({
        screen: "timeline",
        sees: ["Timeline"],
        summary: () => ({ length_s: 30 }),
        actions: { setPlayhead },
      });
      return null;
    }
    await openDrawer(<Timeline />);
    fireEvent.click(await screen.findByRole("button", { name: "Jump the Timeline to 0:26" }));
    // a time past the sequence's end stays text
    act(() =>
      onEvent!({
        seq: 50,
        type: "entry",
        entry: entry(5, { kind: "assistant", text: "The credits roll at 0:45." }),
      }),
    );
    expect(await within(drawer()!).findByText(/The credits roll at 0:45/)).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Jump the Timeline to 0:45" })).toBeNull();
    expect(setPlayhead).toHaveBeenCalledWith({ at_s: 26 }, expect.anything());
  });

  it("an older long reply stops at 3 lines with one chevron for the whole bubble; the latest shows in full (board frame 17)", async () => {
    const tall = vi
      .spyOn(HTMLElement.prototype, "scrollHeight", "get")
      .mockImplementation(function (this: HTMLElement) {
        return this.className.startsWith("clamp") ? 200 : 0;
      });
    serve({
      existing: thread(),
      entries: [
        entry(1, { kind: "assistant", text: "Opening Create…" }),
        entry(2, { kind: "assistant", text: "Five shots…" }),
        entry(3, { kind: "user", text: "Now shot 2" }),
        entry(4, { kind: "assistant", text: "Shot 2 is set." }),
      ],
    });
    await openDrawer();
    const show = await screen.findByRole("button", { name: "Show all" });
    // one control for the older reply, not one per answer; the latest reply always shows in full
    expect(screen.getAllByRole("button", { name: "Show all" })).toHaveLength(1);
    expect(screen.getByText("Shot 2 is set.").closest(".unclamp")).toBeTruthy();
    expect(screen.getByText("Opening Create…").closest(".clamp")).toBe(
      screen.getByText("Five shots…").closest(".clamp"),
    );
    expect(show.getAttribute("aria-expanded")).toBe("false");
    fireEvent.click(show);
    expect(screen.getByRole("button", { name: "Show less" }).getAttribute("aria-expanded")).toBe(
      "true",
    );
    expect(screen.getByText("Five shots…").closest(".unclamp")).toBeTruthy();
    tall.mockRestore();
  });

  it("an earlier turn's steps fold into one line once a new turn starts; the newest stays open", async () => {
    const ln = (n: number, turn: number, text: string, over = {}) =>
      entry(n, {
        kind: "line",
        turn,
        line: { id: id(n, turn), status: "done", text, undo: false, ...over },
      });
    serve({
      existing: thread(),
      entries: [
        entry(1, { kind: "user", turn: 1, text: "Make shot 1" }),
        ln(2, 1, "Opened Create · Image"),
        ln(3, 1, "Run done · 2 takes"),
        ln(4, 1, "Remembered: Elias's voice is warm", { memory_id: T }),
        // an entry's turn counts inputs (results too): the turn is what one message of yours started
        ln(5, 2, "Set Duration: 5 s"),
        ln(6, 2, "Set Lighting: Window"),
        entry(7, { kind: "user", turn: 3, text: "Now shot 2" }),
        ln(8, 3, "Opened Create · Video"),
        ln(9, 4, "Set Lighting: Rain"),
      ],
    });
    await openDrawer();
    const sum = await screen.findByRole("button", { name: "5 steps · 2 takes · 1 memory" });
    expect(sum.getAttribute("aria-expanded")).toBe("false");
    expect(screen.queryByText("Opened Create · Image")).toBeNull();
    expect(screen.getByText("Opened Create · Video")).toBeTruthy();
    expect(screen.getByText("Set Lighting: Rain")).toBeTruthy();
    expect(screen.getAllByRole("button", { name: /steps?( ·|$)/ })).toHaveLength(1);
    fireEvent.click(sum);
    expect(sum.getAttribute("aria-expanded")).toBe("true");
    expect(screen.queryByText("Opened Create · Image")).toBeNull(); // the last 4, then "+ 1 more step"
    fireEvent.click(screen.getByRole("button", { name: "+ 1 more step" }));
    expect(screen.getByText("Opened Create · Image")).toBeTruthy();
  });

  it("a long line is one line that opens on tap, its full text on hover", async () => {
    const long = "Generated Elias at the window, morning light through lace curtains · 4 takes";
    serve({
      existing: thread(),
      entries: [
        entry(1, { kind: "line", line: { id: id(1), status: "done", text: long, undo: false } }),
      ],
    });
    await openDrawer();
    const one = (await screen.findByText(long)).closest("[role=button]")!;
    expect(one.className).toBe("one");
    expect(one.getAttribute("title")).toBe(long);
    fireEvent.click(one);
    expect(one.getAttribute("aria-expanded")).toBe("true");
    expect(one.className).toBe("wide");
  });

  it("a look card shows the takes it is about", async () => {
    const t1 = "00000000-0000-4000-8000-0000000000a1";
    answer("GET", {
      "/v1/studio/assistant/threads": () => ({
        data: { status: status(), threads: [thread()] },
      }),
      "/v1/studio/assistant/threads/{thread_id}": () => ({
        data: {
          ...thread(),
          entries: [
            entry(1, {
              kind: "card",
              card: {
                id: id(1),
                kind: "look",
                title: "Look at 1 take?",
                rows: [],
                tags: [],
                take_ids: [t1],
                state: "waiting",
              },
            }),
          ],
        },
      }),
      "/v1/studio/takes/{take_id}": () => ({
        data: { id: t1, title: "Fox at dusk", thumb_url: "/files/u/x/fox.png" },
      }),
    });
    await openDrawer();
    const thumbs = await screen.findByLabelText("The takes it is about");
    await waitFor(() =>
      expect(thumbs.querySelector("img")?.getAttribute("src")).toBe("/files/u/x/fox.png"),
    );
    expect(thumbs.querySelector("img")!.getAttribute("alt")).toBe("Fox at dusk");
  });
});

describe("Auto mode (API 1.68, board frame 13)", () => {
  const generate =
    (yes: (auto?: boolean) => Promise<ReturnType<typeof done>>): Handler =>
    () => ({
      card: {
        kind: "generate",
        title: "Generate 4 images?",
        rows: [],
        tags: ["uses the GPU"],
        go: "GENERATE",
        fine: "",
      },
      yes,
    });

  it("a · the switch in the box starts off, its line on hover, and a click turns the chat's Auto on", async () => {
    serve({ existing: thread() });
    const patched: unknown[] = [];
    answer("PATCH", {
      "/v1/studio/assistant/threads/{thread_id}": ({ body }) => {
        patched.push(body);
        return { data: thread({ auto: true }) };
      },
    });
    await openDrawer();
    const sw = await screen.findByRole("switch", { name: "Auto" });
    expect(sw.getAttribute("aria-checked")).toBe("false");
    fireEvent.mouseEnter(sw);
    expect(screen.getByRole("tooltip").textContent).toContain("Deletes, anything off the machine");
    fireEvent.click(sw);
    await waitFor(() => expect(sw.getAttribute("aria-checked")).toBe("true"));
    expect(patched).toEqual([{ auto: true }]);
  });

  it("b · an auto generation runs without its card, says Auto and has Undo; c · one that asks shows why", async () => {
    const posts = serve({ existing: thread({ auto: true }) });
    const yes = vi.fn(async (auto?: boolean) =>
      done(auto ? "Generated 4 takes · Mac" : "You clicked GENERATE", {
        result: { run_ids: ["r1"] },
      }),
    );
    await openDrawer(<Screen actions={{ generate: generate(yes) }} />);
    await waitFor(() => expect(onEvent).not.toBeNull());
    park([{ id: id(0), name: "generate", args: {}, auto: true } as never]);
    await waitFor(() => expect(posts).toHaveLength(1));
    expect(yes).toHaveBeenCalledWith(true);
    expect(screen.queryByRole("region", { name: /^Confirm/ })).toBeNull();
    expect(posts[0]).toMatchObject({
      results: [
        { id: id(0), status: "done", line: { text: "Auto · Generated 4 takes · Mac", undo: true } },
      ],
    });
    act(() =>
      onEvent!({
        seq: 30,
        type: "entry",
        entry: entry(5, {
          kind: "line",
          line: { id: id(0), status: "done", text: "Auto · Generated 4 takes · Mac", undo: true },
        }),
      }),
    );
    expect(await screen.findByRole("button", { name: "Undo: Generated 4 takes" })).toBeTruthy();

    park(
      [
        {
          id: id(0, 2),
          name: "generate",
          args: {},
          asks: "Auto paused · 8 of 8 takes this hour",
        } as never,
      ],
      2,
    );
    await screen.findByRole("region", { name: "Confirm: Generate 4 images?" });
    expect(screen.getByText("Auto paused · 8 of 8 takes this hour")).toBeTruthy();
    expect(yes).toHaveBeenCalledTimes(1);
  });

  it("the cap chip shows under the thread while an Auto turn runs", async () => {
    serve({ existing: thread({ auto: true }) });
    await openDrawer();
    await waitFor(() => expect(onEvent).not.toBeNull());
    act(() =>
      onEvent!({
        seq: 20,
        type: "thread",
        thread: thread({
          auto: true,
          state: "working",
          client_id: ME,
          caps: { model_calls: 1, tool_calls: 1, memory_writes: 0, auto_takes_hour: 4 },
        }),
      }),
    );
    expect(await screen.findByText("4 of 8 takes this hour")).toBeTruthy();
  });
});

describe("alpha.5: move the drawer (plan 36 §40, board frames 29 and 30)", () => {
  const drawer = () => screen.getByRole("complementary", { name: "Assistant" }) as HTMLElement;
  const pill = () => screen.getByRole("button", { name: /^Move the Assistant/ });
  const docked = () => !!document.querySelector("[data-drawer]");
  const at = () => [drawer().style.left, drawer().style.top];
  const size = (w: number, h = 768) =>
    act(() => {
      Object.assign(window, { innerWidth: w, innerHeight: h });
      dispatchEvent(new Event("resize"));
    });
  const drag = (el: Element, from: [number, number], to: [number, number], up = true) => {
    fireEvent.pointerDown(el, { clientX: from[0], clientY: from[1], button: 0 });
    fireEvent.pointerMove(window, { clientX: to[0], clientY: to[1] });
    if (up) fireEvent.pointerUp(window, { clientX: to[0], clientY: to[1] });
  };
  const media = window.matchMedia;
  afterEach(() => {
    size(1024, 768);
    window.matchMedia = media;
  });

  it("dragging the pill floats it there; near the right edge the docked place lights and letting go docks", async () => {
    serve();
    await openDrawer();
    expect(docked()).toBe(true);
    expect(screen.queryByRole("button", { name: "Dock" })).toBeNull();
    // docked at 670, 14 (1024 wide): grabbed 170, 6 into it
    drag(pill(), [840, 20], [400, 200]);
    expect(drawer().className).toContain("float");
    expect(at()).toEqual(["230px", "194px"]);
    expect(docked()).toBe(false); // the page takes its full width
    // the header's empty parts drag too; its buttons still click
    drag(drawer().querySelector(".dh")!, [300, 210], [1020, 210], false);
    expect(document.querySelector(".ast-ghost")).not.toBeNull();
    fireEvent.pointerUp(window);
    expect(document.querySelector(".ast-ghost")).toBeNull();
    expect(drawer().className).not.toContain("float");
    expect(docked()).toBe(true);
    fireEvent.pointerDown(screen.getByRole("button", { name: "Memory" }), { button: 0 });
    fireEvent.pointerMove(window, { clientX: 100, clientY: 100 });
    fireEvent.pointerUp(window);
    expect(drawer().className).not.toContain("float");
  });

  it("Dock and a double-click on the header dock it; the pill's arrows move it 16 px and Enter docks", async () => {
    serve();
    await openDrawer();
    fireEvent.keyDown(pill(), { key: "ArrowLeft" });
    expect(at()).toEqual(["654px", "14px"]);
    fireEvent.keyDown(pill(), { key: "ArrowDown" });
    expect(at()).toEqual(["654px", "30px"]);
    fireEvent.click(screen.getByRole("button", { name: "Dock" }));
    expect(drawer().className).not.toContain("float");
    fireEvent.keyDown(pill(), { key: "ArrowLeft" });
    fireEvent.doubleClick(screen.getByRole("button", { name: "Memory" }));
    expect(drawer().className).toContain("float");
    fireEvent.doubleClick(drawer().querySelector(".dh")!);
    expect(drawer().className).not.toContain("float");
    fireEvent.keyDown(pill(), { key: "ArrowUp" });
    expect(drawer().className).toContain("float");
    fireEvent.keyDown(pill(), { key: "Enter" });
    expect(drawer().className).not.toContain("float");
  });

  it("where it floats is remembered for the user: after ⌘J and a reload it comes back there", async () => {
    serve();
    await openDrawer();
    drag(pill(), [840, 20], [400, 200]);
    fireEvent.keyDown(window, { key: "j", ctrlKey: true });
    fireEvent.keyDown(window, { key: "j", ctrlKey: true });
    expect(at()).toEqual(["230px", "194px"]);
    expect(JSON.parse(localStorage.getItem("helm-assistant-place:u1")!)).toMatchObject({
      x: 230 / 1024,
    });
    cleanup();
    sessionStorage.clear(); // a new tab: only this browser's localStorage carries it
    await openDrawer();
    expect(at()).toEqual(["230px", "194px"]);
    // Full screen and back keep the place
    fireEvent.click(screen.getByRole("button", { name: /^Full screen/ }));
    expect(drawer().className).toContain("full");
    expect(drawer().className).not.toContain("float");
    fireEvent.click(screen.getByRole("button", { name: /^Back to the drawer/ }));
    expect(at()).toEqual(["230px", "194px"]);
  });

  it("a smaller window pulls it back on screen; under 1024 px it docks, and floats again when wider", async () => {
    serve();
    await openDrawer();
    drag(pill(), [840, 20], [400, 200]);
    size(1100, 600);
    expect(at()).toEqual([`${Math.round((230 / 1024) * 1100)}px`, "86px"]); // 600 - 500 - 14
    size(1000);
    expect(drawer().className).not.toContain("float");
    expect(docked()).toBe(true);
    expect(screen.queryByRole("button", { name: /^Move the Assistant/ })).toBeNull();
    size(1024);
    expect(at()).toEqual(["230px", "194px"]);
  });

  it("a phone's sheet has no moving, even with a place remembered", async () => {
    localStorage.setItem("helm-assistant-place:u1", JSON.stringify({ x: 0.2, y: 0.2 }));
    window.matchMedia = ((q: string) => ({
      matches: q.includes("max-width: 767px"),
      addEventListener: () => undefined,
      removeEventListener: () => undefined,
    })) as unknown as typeof matchMedia;
    size(375, 812);
    serve();
    await openDrawer();
    expect(drawer().className).toContain("sheet");
    expect(drawer().className).not.toContain("float");
    expect(screen.queryByRole("button", { name: /^Move the Assistant/ })).toBeNull();
  });
});
