// @vitest-environment jsdom
// Pick one cards and id chips in the drawer (docs/mock/Assistant.dc.html frames 10b and 11; alpha.2 items 17, 19):
// cards send the title, keys 1–N pick, the pick stays marked and the rest fade; chips show picture and title, never
// an id; a deleted one and an unknown one say so; a take chip opens its preview.
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import type { S } from "@/lib/api";
import { answer } from "./helpers";

vi.mock("next/navigation", () => ({
  usePathname: () => "/",
  useRouter: () => ({ push: vi.fn() }),
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
  sessionStorage.clear();
  localStorage.clear();
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const T = "11111111-2222-4333-8444-555555555555";
const TAKE = "9a1b2c3d-4e5f-4a6b-8c7d-0e1f2a3b4c5d";
const GONE = "9a1b2c3d-4e5f-4a6b-8c7d-0e1f2a3b4c5e";
const ELEMENT = "7a1b2c3d-4e5f-4a6b-8c7d-0e1f2a3b4c5d";
const user = { id: "u1", email: "u@x.test", name: "U", roles: ["user"] } as unknown as S<"User">;
const thread = {
  id: T,
  title: "Widower",
  state: "idle",
  client_id: null,
  pending: null,
  caps: { model_calls: 0, tool_calls: 0, memory_writes: 0 },
  created_at: "2026-11-25T00:00:00Z",
  updated_at: "2026-11-25T00:00:00Z",
};
const OPTS = [
  { title: "Fix a part", line: "Repaint the hands in the keyframe" },
  { title: "Shot 2", line: "Elias at the window, Biscuit at his feet", best: true },
  { title: "Shot 3 keyframe", line: "The empty chair, morning light" },
];
const said = (n: number, kind: "user" | "assistant", text: string) =>
  ({
    id: `00000000-0000-4000-8000-00000000000${n}`,
    turn: 1,
    kind,
    at: thread.created_at,
    text,
  }) as S<"AssistantEntry">;

function serve(entries: S<"AssistantEntry">[]) {
  const posts: { text?: string }[] = [];
  answer("GET", {
    "/v1/studio/assistant/threads": () => ({
      data: {
        status: {
          enabled: true,
          thinks: false,
          provider: { title: "DeepSeek", remote: true },
          web_search: null,
          limits: {
            model_calls_per_day: 200,
            tokens_per_day: 2000000,
            look_at_per_hour: 20,
            messages_per_minute: 6,
          },
        },
        threads: [thread],
      },
    }),
    "/v1/studio/assistant/threads/{thread_id}": () => ({ data: { ...thread, entries } }),
    "/v1/studio/takes/{take_id}": ({ params }) => {
      const id = (params as { path: { take_id: string } }).path.take_id;
      return id === TAKE
        ? {
            data: {
              id,
              title: "Shot 1 keyframe",
              kind: "image",
              thumb_url: "/files/t.jpg",
              url: "/files/t.jpg",
            },
          }
        : { status: 404, error: { detail: "Not found." } };
    },
    "/v1/studio/library": ({ params }) => {
      const kind = (params as { query: { kind: string } }).query.kind;
      return {
        data: {
          items:
            kind === "element"
              ? [
                  {
                    id: ELEMENT,
                    kind: "element",
                    name: "Biscuit",
                    handle: "biscuit",
                    thumb_url: "/files/b.jpg",
                  },
                ]
              : kind === "voice"
                ? [
                    {
                      id: "qwen3-tts/elias",
                      kind: "voice",
                      name: "Elias · warm, slow",
                      handle: null,
                      thumb_url: null,
                    },
                  ]
                : [],
        },
      };
    },
    "/v1/studio/workflows": () => ({
      data: { workflows: [{ id: "ltx-i2v", title: "LTX-2.5 · Image to video" }] },
    }),
  });
  answer("POST", {
    "/v1/studio/assistant/threads/{thread_id}/messages": ({ body }) => {
      posts.push(body as { text?: string });
      return {
        status: 202,
        data: { thread: { ...thread, state: "working", client_id: "tabA00000001" }, entry: null },
      };
    },
  });
  return posts;
}

async function openDrawer() {
  render(<AppShell user={user}>{null}</AppShell>);
  fireEvent.click(await screen.findByRole("button", { name: /^Assistant/ }));
  return screen.findByRole("complementary", { name: "Assistant" });
}

const offered = "Shot 1 is placed. Next, pick one:\n```options\n" + JSON.stringify(OPTS) + "\n```";

describe("pick one (frame 11)", () => {
  it("shows the options as cards, Best next ringed, and a click sends the title", async () => {
    const posts = serve([said(1, "user", "Animate shot 1"), said(2, "assistant", offered)]);
    const d = await openDrawer();
    const group = await within(d).findByRole("group", { name: "Pick one" });
    const cards = within(group).getAllByRole("button");
    expect(cards.map((c) => c.textContent)).toEqual([
      "1Fix a partRepaint the hands in the keyframe",
      "2Shot 2Elias at the window, Biscuit at his feetBest next",
      "3Shot 3 keyframeThe empty chair, morning light",
    ]);
    expect(cards[1].className).toContain("rec");
    expect(within(d).queryByText(/```|"title"/)).toBeNull();
    expect(d.querySelector(".cmpb small")?.textContent).toBe("1–3 picks");
    fireEvent.click(cards[1]);
    await waitFor(() => expect(posts.at(-1)?.text).toBe("Shot 2"));
  });

  it("a number key in the empty box picks", async () => {
    const posts = serve([said(2, "assistant", offered)]);
    const d = await openDrawer();
    await within(d).findByRole("group", { name: "Pick one" });
    fireEvent.keyDown(within(d).getByRole("textbox", { name: "Message the assistant" }), {
      key: "3",
    });
    await waitFor(() => expect(posts.at(-1)?.text).toBe("Shot 3 keyframe"));
  });

  it("picked: the pick stays marked, the others fade, none works", async () => {
    serve([said(2, "assistant", offered), said(3, "user", "Shot 2")]);
    const d = await openDrawer();
    const cards = within(await within(d).findByRole("group", { name: "Pick one" })).getAllByRole(
      "button",
    );
    expect(cards.map((c) => (c as HTMLButtonElement).disabled)).toEqual([true, true, true]);
    expect(cards.map((c) => c.className.includes("on"))).toEqual([false, true, false]);
    expect(cards.map((c) => c.className.includes("fade"))).toEqual([true, false, true]);
    expect(within(d).queryByText("Best next")).toBeNull();
  });

  it("a block that did not parse shows as text", async () => {
    serve([said(2, "assistant", "Pick one:\n1. Fix a part\n2. Shot 2")]);
    const d = await openDrawer();
    expect(await within(d).findByText(/1\. Fix a part/)).toBeTruthy();
    expect(within(d).queryByRole("group", { name: "Pick one" })).toBeNull();
  });
});

describe("chips, not ids (frame 10b)", () => {
  it("shows picture and title for each kind, deleted and unknown ones say so, never an id", async () => {
    serve([
      said(
        2,
        "assistant",
        `Ready: animate [[take:${TAKE}]] with [[voice:qwen3-tts/elias]] on [[workflow:ltx-i2v]]. ` +
          `[[element:${ELEMENT}]] stays. The other is [[take:${GONE}]], not [[take:?]].`,
      ),
    ]);
    const d = await openDrawer();
    const take = await within(d).findByRole("button", { name: "take: Shot 1 keyframe" });
    expect(take.querySelector("i.pic")).toBeTruthy();
    expect(await within(d).findByLabelText("voice: Elias · warm, slow")).toBeTruthy();
    expect(await within(d).findByLabelText("workflow: LTX-2.5 · Image to video")).toBeTruthy();
    expect(await within(d).findByRole("button", { name: "element: @biscuit" })).toBeTruthy();
    expect((await within(d).findByText("deleted take")).closest(".idc")?.className).toContain(
      "gone",
    );
    expect(within(d).getByText("unknown take")).toBeTruthy();
    expect(d.textContent).not.toMatch(/9a1b|7a1b|\[\[|qwen3-tts\/elias|ltx-i2v/);
    fireEvent.click(take);
    expect(await screen.findByRole("dialog", { name: "Shot 1 keyframe" })).toBeTruthy();
  });
});
