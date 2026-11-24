// @vitest-environment jsdom
// Media in the drawer (docs/mock/Assistant.dc.html frame 12; alpha.2 items 7, 8, 10): a take alone on an answer's
// line shows as itself with Open and Use in…; your message shows its attachments and @-picked takes as chips; @ opens
// the picker (elements, then takes and uploads) and a pick is sent as a ref, never shown as an id; a pasted image
// uploads, shows in the box, says once that a text-only model uses Look at, and is sent as the message's takes.
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
const UP = "8a1b2c3d-4e5f-4a6b-8c7d-0e1f2a3b4c5d";
vi.mock("@/lib/takes", async (orig) => ({
  ...(await orig<typeof import("@/lib/takes")>()),
  uploadFile: async () => ({ id: UP, title: "neon-alley.jpg" }),
}));

const { AppShell } = await import("@/components/shell");

beforeAll(() => {
  HTMLElement.prototype.hidePopover ??= () => undefined;
  Element.prototype.scrollIntoView ??= () => undefined;
  URL.createObjectURL ??= () => "blob:x";
  URL.revokeObjectURL ??= () => undefined;
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
const user = { id: "u1", email: "u@x.test", name: "U", roles: ["user"] } as unknown as S<"User">;
const thread = {
  id: T,
  title: "Fox",
  state: "idle",
  client_id: null,
  pending: null,
  caps: { model_calls: 0, tool_calls: 0, memory_writes: 0 },
  created_at: "2026-11-25T00:00:00Z",
  updated_at: "2026-11-25T00:00:00Z",
};
const fox = {
  id: TAKE,
  title: "Fox at dusk · take 2",
  kind: "image",
  source: "create",
  thumb_url: "/files/f.jpg",
  url: "/files/f.jpg",
  created_at: "2026-11-25T00:00:00Z",
};
const said = (n: number, kind: "user" | "assistant", text: string, takes?: string[]) =>
  ({
    id: `00000000-0000-4000-8000-00000000000${n}`,
    turn: 1,
    kind,
    at: thread.created_at,
    text,
    ...(takes && { takes }),
  }) as S<"AssistantEntry">;

function serve(entries: S<"AssistantEntry">[]) {
  const posts: { text?: string; takes?: string[] }[] = [];
  answer("GET", {
    "/v1/studio/assistant/threads": () => ({
      data: {
        status: {
          enabled: true,
          thinks: false,
          sees: false,
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
        ? { data: fox }
        : { data: { ...fox, id, title: "neon-alley.jpg", source: "upload" } };
    },
    "/v1/studio/takes/{take_id}/uses": () => ({
      data: {
        uses: [{ workflow_id: "ltx-i2v", param: "image", title: "First frame", action: "Video" }],
      },
    }),
    "/v1/studio/takes": ({ params }) => {
      const source = (params as { query: { source?: string } }).query.source;
      return { data: { takes: source === "upload" ? [] : [fox], next_cursor: null } };
    },
    "/v1/studio/library": ({ params }) => {
      const kind = (params as { query: { kind: string } }).query.kind;
      return {
        data: {
          items:
            kind === "element"
              ? [{ id: "e1", kind: "element", name: "Fox", handle: "fox", thumb_url: null }]
              : [],
        },
      };
    },
  });
  answer("POST", {
    "/v1/studio/assistant/threads/{thread_id}/messages": ({ body }) => {
      posts.push(body as { text?: string; takes?: string[] });
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

describe("media in answers (frame 12a)", () => {
  it("a take alone on its line shows as itself with Open and Use in…; in a sentence it stays a chip", async () => {
    serve([
      said(2, "assistant", `Take 2, the sharpest:\n[[take:${TAKE}]]\nOr use [[take:${TAKE}]].`),
    ]);
    const d = await openDrawer();
    expect(await within(d).findByRole("img", { name: "Fox at dusk · take 2" })).toBeTruthy();
    expect(within(d).getByRole("button", { name: "take: Fox at dusk · take 2" })).toBeTruthy();
    fireEvent.click(within(d).getByRole("button", { name: "Use in…" }));
    expect(await within(d).findByRole("menuitem", { name: "Video · First frame" })).toBeTruthy();
    expect(within(d).getByRole("menuitem", { name: "Timeline · at the playhead" })).toBeTruthy();
    expect(within(d).getByRole("menuitem", { name: "Save as element" })).toBeTruthy();
    fireEvent.click(within(d).getByRole("button", { name: "Open" }));
    expect(await screen.findByRole("dialog", { name: "Fox at dusk · take 2" })).toBeTruthy();
    expect(d.textContent).not.toMatch(/9a1b|\[\[/);
  });
});

describe("your message (frames 12d, 12e)", () => {
  it("shows its attachments above the words and @-picked takes as chips", async () => {
    serve([said(1, "user", `Animate [[take:${TAKE}]] slowly`, [UP])]);
    const d = await openDrawer();
    expect(await within(d).findByText("neon-alley.jpg")).toBeTruthy();
    expect(
      await within(d).findByRole("button", { name: "take: Fox at dusk · take 2" }),
    ).toBeTruthy();
    expect(d.textContent).not.toMatch(/9a1b|8a1b|\[\[/);
  });
});

describe("the box (frames 12c, 12e, 12f)", () => {
  it("@ lists elements then takes; Enter picks; the pick is sent as a ref", async () => {
    const posts = serve([]);
    const d = await openDrawer();
    const box = within(d).getByRole("textbox", { name: "Message the assistant" });
    fireEvent.change(box, { target: { value: "Animate @fo", selectionStart: 11 } });
    const list = await within(d).findByRole("listbox", { name: "Pick a take or element" });
    expect(await within(list).findByRole("option", { name: /@fox/ })).toBeTruthy();
    expect(await within(list).findByRole("option", { name: /Fox at dusk/ })).toBeTruthy();
    expect(box.getAttribute("aria-controls")).toBe(list.id);
    fireEvent.keyDown(box, { key: "ArrowDown" });
    await waitFor(() => expect(box.getAttribute("aria-activedescendant")).toBe("at-1"));
    fireEvent.keyDown(box, { key: "Enter" });
    await waitFor(() =>
      expect((box as HTMLTextAreaElement).value).toBe("Animate [Fox at dusk · take 2] "),
    );
    fireEvent.change(box, {
      target: { value: "Animate [Fox at dusk · take 2] slowly", selectionStart: 37 },
    });
    fireEvent.keyDown(box, { key: "Enter" });
    await waitFor(() => expect(posts).toHaveLength(1));
    expect(posts[0].text).toBe(`Animate [[take:${TAKE}]] slowly`);
  });

  it("a message within 4000 characters that its picks' refs push past them is not sent", async () => {
    const posts = serve([]);
    const d = await openDrawer();
    const box = within(d).getByRole("textbox", { name: "Message the assistant" });
    fireEvent.change(box, { target: { value: "@fo", selectionStart: 3 } });
    await within(d).findByRole("option", { name: /Fox at dusk/ });
    fireEvent.keyDown(box, { key: "ArrowDown" });
    fireEvent.keyDown(box, { key: "Enter" });
    await waitFor(() => expect((box as HTMLTextAreaElement).value).toMatch(/^\[Fox at dusk/));
    const long = (box as HTMLTextAreaElement).value + "x".repeat(3970); // 3993 in the box, 4017 sent
    fireEvent.change(box, { target: { value: long, selectionStart: long.length } });
    fireEvent.keyDown(box, { key: "Enter" });
    expect(await within(d).findByText(/Too long with its picked takes/)).toBeTruthy();
    expect(posts).toHaveLength(0);
  });

  it("a pasted image uploads, shows in the box, says the model uses Look at, and goes as takes", async () => {
    const posts = serve([]);
    const d = await openDrawer();
    const box = within(d).getByRole("textbox", { name: "Message the assistant" });
    const file = new File(["x"], "image.png", { type: "image/png" });
    fireEvent.paste(box, { clipboardData: { files: [file] } });
    expect(await within(d).findByText("Pasted image")).toBeTruthy();
    expect(within(d).getByText("This model can't see images; it will use Look at.")).toBeTruthy();
    fireEvent.change(box, { target: { value: "Same, but wetter", selectionStart: 16 } });
    fireEvent.keyDown(box, { key: "Enter" });
    await waitFor(() => expect(posts).toHaveLength(1));
    expect(posts[0]).toMatchObject({ text: "Same, but wetter", takes: [UP] });
    expect(within(d).queryByText("Pasted image")).toBeNull();
  });
});
