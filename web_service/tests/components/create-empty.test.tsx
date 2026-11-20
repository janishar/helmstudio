// @vitest-environment jsdom
// The empty Create screen (docs/mock/Direct.dc.html): with no run and no take, the title, the composer under it, the
// open look tile's group of looks inline, and the Elements panel in the aside; after a run, the Stage and Inspector.
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import workflowsFixture from "../../../contracts/fixtures/registry/workflows.valid.json";
import nodesFixture from "../../../contracts/fixtures/registry/nodes.valid.json";
import qwenNodes from "../../../contracts/fixtures/registry/qwen-image.nodes.valid.json";
import qwenWorkflows from "../../../contracts/fixtures/registry/qwen-image.workflows.valid.json";
import { Create } from "@/components/create/create";
import type { S } from "@/lib/api";
import { answer } from "./helpers";

vi.mock("@/lib/runs", async (orig) => ({
  ...(await orig<typeof import("@/lib/runs")>()),
  followSession: () => () => undefined,
}));

beforeAll(() => {
  HTMLDialogElement.prototype.showModal ??= function (this: HTMLDialogElement) {
    this.open = true;
  };
  HTMLDialogElement.prototype.close ??= function (this: HTMLDialogElement) {
    this.open = false;
  };
  globalThis.requestAnimationFrame ??= (f: FrameRequestCallback) =>
    setTimeout(() => f(0), 0) as unknown as number;
  document.execCommand ??= () => false;
  HTMLElement.prototype.hidePopover ??= () => undefined;
  Element.prototype.scrollIntoView ??= () => undefined;
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const echo = {
  ...workflowsFixture.workflows[0],
  node_schemas: Object.fromEntries(nodesFixture.nodes.map((n) => [n.kind, n])),
} as unknown as S<"Workflow">;
const qwen = {
  ...qwenWorkflows.workflows[0],
  node_schemas: Object.fromEntries(qwenNodes.nodes.map((n) => [n.kind, n])),
} as unknown as S<"Workflow">;

const item = (over: Partial<S<"LibraryItem">>) =>
  ({
    images: [],
    thumb_url: null,
    text: "",
    version: 1,
    source: "mine",
    loras: [],
    needs: ["text"],
    created_at: "2026-11-01T00:00:00Z",
    updated_at: "2026-11-01T00:00:00Z",
    ...over,
  }) as S<"LibraryItem">;
const fox = item({ id: "e1", kind: "element", handle: "fox", name: "Fox", group: "character" });
const window = item({
  id: "echo/window",
  kind: "look",
  handle: null,
  name: "Window",
  group: "lighting",
  text: "soft window light",
  source: "starter",
});

function serve(runs: S<"Run">[] = []) {
  answer("GET", {
    "/v1/studio/sessions/{session_id}/runs": () => ({ data: { runs } }),
    "/v1/studio/sessions/{session_id}/takes": () => ({ data: { takes: [] } }),
    "/v1/studio/library": (init) => {
      const kind = (init.params as { query: { kind: string } }).query.kind;
      return { data: { items: kind === "look" ? [window] : [fox] } };
    },
    "/v1/studio/hosts": () => ({ data: { hosts: [] } }),
    "/v1/studio/loras": () => ({ data: { loras: [] } }),
  });
}

const run: S<"Run"> = {
  id: "r1",
  session_id: "s1",
  workflow_id: "echo",
  inputs: {},
  status: "queued",
  progress: null,
  error: null,
  outputs: [],
  host: "fake",
} as unknown as S<"Run">;

const start = (runs: S<"Run">[] = [], w = echo) =>
  render(
    <Create
      initial={{
        sessions: [],
        workflows: [w],
        sessionId: runs.length ? "s1" : null,
        runs,
        takes: [],
      }}
    />,
  );

describe("Create: the empty screen", () => {
  it("shows the title, the composer, the open group's looks and the Elements aside", async () => {
    serve();
    start();
    expect(screen.getByRole("heading", { level: 1 }).textContent).toBe("What will you make?");
    expect(screen.getByRole("form", { name: "Composer" })).toBeTruthy();
    expect(screen.queryByRole("complementary", { name: "Inspector" })).toBeNull();
    const aside = screen.getByRole("complementary", { name: "Elements" });
    // the first look tile's group is open by default
    const looks = await screen.findByRole("region", { name: "Lighting looks" });
    expect(
      screen.getByRole("button", { name: "Lighting: Auto" }).getAttribute("aria-expanded"),
    ).toBe("true");
    const grid = within(looks).getByRole("list", { name: "Looks" });
    expect(within(grid).getByRole("button", { name: /Auto/ }).getAttribute("aria-pressed")).toBe(
      "true",
    );
    expect(within(grid).getByRole("button", { name: /New look/ })).toBeTruthy();
    // a look applies at once: the tile says so
    fireEvent.click(within(grid).getByRole("button", { name: /Window/ }));
    expect(screen.getByRole("button", { name: "Lighting: Window" })).toBeTruthy();
    fireEvent.click(within(looks).getByRole("button", { name: "Reset" }));
    expect(screen.getByRole("button", { name: "Lighting: Auto" })).toBeTruthy();
    // a card puts its @handle in the prompt
    const card = await within(aside).findByTitle("Put @fox in the prompt");
    fireEvent.click(card);
    const prompt = screen.getByLabelText("Prompt") as HTMLTextAreaElement;
    await waitFor(() => expect(prompt.value).toContain("@fox"));
    await waitFor(() => expect(within(aside).getByText(/in this prompt/)).toBeTruthy());
  });

  it("a settings chip brings the Inspector into the aside", async () => {
    serve();
    start([], qwen);
    expect(screen.getByRole("complementary", { name: "Elements" })).toBeTruthy();
    // the size chip opens its control, which lives in the Inspector
    fireEvent.click(screen.getByRole("button", { name: "1:1 · 1 MP" }));
    expect(screen.getByRole("complementary", { name: "Inspector" })).toBeTruthy();
    // the rest of the empty screen stays
    expect(screen.getByRole("heading", { level: 1 })).toBeTruthy();
  });

  it("after a run: the Stage and the Inspector, no title or inline looks", async () => {
    serve();
    start([run]);
    expect(screen.getByRole("complementary", { name: "Inspector" })).toBeTruthy();
    expect(screen.queryByRole("complementary", { name: "Elements" })).toBeNull();
    expect(screen.queryByRole("heading", { level: 1 })).toBeNull();
    // let the looks load: still no inline section, and the tile opens the picker
    await screen.findByRole("button", { name: "Lighting: Auto" });
    expect(screen.queryByRole("region", { name: "Lighting looks" })).toBeNull();
    expect(
      screen.getByRole("button", { name: "Lighting: Auto" }).getAttribute("aria-haspopup"),
    ).toBe("dialog");
  });

  it("the first Generate brings the Stage and the Inspector in the same screen", async () => {
    serve();
    const post = answer("POST", {
      "/v1/studio/sessions": () => ({ data: { id: "s1", title: "a fox" } }),
      "/v1/studio/sessions/{session_id}/runs": () => ({ data: run }),
    });
    start();
    // a different look tile switches the inline group
    fireEvent.click(await screen.findByRole("button", { name: "Lighting: Auto" }));
    expect(screen.getByRole("region", { name: "Lighting looks" })).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Prompt"), { target: { value: "a fox" } });
    fireEvent.submit(screen.getByRole("form", { name: "Composer" }));
    await waitFor(() => expect(screen.queryByRole("heading", { level: 1 })).toBeNull());
    expect(post).toHaveBeenCalledWith("/v1/studio/sessions/{session_id}/runs", expect.anything());
    expect(screen.getByRole("complementary", { name: "Inspector" })).toBeTruthy();
    expect(screen.queryByRole("region", { name: "Lighting looks" })).toBeNull();
    expect(
      screen.getByRole("button", { name: "Lighting: Auto" }).getAttribute("aria-haspopup"),
    ).toBe("dialog");
  });

  it("a new session after the Inspector was opened shows the Elements aside again", async () => {
    serve();
    answer("POST", { "/v1/studio/sessions": () => ({ data: { id: "s2", title: null } }) });
    start([], qwen);
    fireEvent.click(screen.getByRole("button", { name: "1:1 · 1 MP" }));
    expect(screen.getByRole("complementary", { name: "Inspector" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: /New session/, hidden: true }));
    expect(await screen.findByRole("complementary", { name: "Elements" })).toBeTruthy();
  });

  it("the Elements dialog in the Stage layout keeps its (i) and New element", async () => {
    serve([run]);
    start([run]);
    fireEvent.click(screen.getByRole("button", { name: /Elements/ }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("button", { name: "New element" })).toBeTruthy();
    expect(within(dialog).getByRole("button", { name: "More information" })).toBeTruthy();
  });
});
