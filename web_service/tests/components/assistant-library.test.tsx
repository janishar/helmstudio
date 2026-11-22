// @vitest-environment jsdom
// The library dialogs the assistant opens (components/assistant/library.tsx; plan 18 §22.5 "dialog", decision 5;
// board frame 6 "Clone voice"): the screen's own dialog, prefilled; the line waits for the user's Save (and the
// rights tick); on close the action resolves with the new id and @handle, or "cancelled". Create refuses a starter or
// a voice to edit, Hear it stays the dialog's button, and an element's pictures are images.
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import workflows from "../../../contracts/fixtures/registry/workflows.valid.json";
import coreNodes from "../../../contracts/fixtures/registry/nodes.valid.json";
import zonosWfs from "../../../contracts/fixtures/registry/zonos3.workflows.valid.json";
import zonosNodes from "../../../contracts/fixtures/registry/zonos3.nodes.valid.json";
import qwenWfs from "../../../contracts/fixtures/registry/qwen3-tts.workflows.valid.json";
import qwenNodes from "../../../contracts/fixtures/registry/qwen3-tts.nodes.valid.json";
import { Registry, ToolsContext } from "@/components/assistant/tools";
import { useLibraryDialog, type LibraryAsk } from "@/components/assistant/library";
import { createTools, type CreateCtx } from "@/components/create/assistant";
import type { VoiceCtx } from "@/components/create/voices";
import type { Outcome } from "@/lib/assistant";
import { type S } from "@/lib/api";
import { answer, audio, take, video } from "./helpers";

vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn(), refresh: vi.fn() }),
  usePathname: () => "/gallery",
  useSearchParams: () => new URLSearchParams(),
}));
vi.mock("next/link", () => ({
  default: ({ href, children, ...rest }: { href: string; children: React.ReactNode }) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));

beforeAll(() => {
  HTMLDialogElement.prototype.showModal ??= function (this: HTMLDialogElement) {
    this.open = true;
  };
  HTMLDialogElement.prototype.close ??= function (this: HTMLDialogElement) {
    this.open = false;
    this.dispatchEvent(new Event("close"));
  };
  HTMLElement.prototype.hidePopover ??= () => undefined;
  Element.prototype.scrollIntoView ??= () => undefined;
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const { Gallery } = await import("@/components/gallery/gallery");

type Item = S<"LibraryItem">;
const item = (over: Partial<Item>): Item =>
  ({
    id: "5a0c1e2f-3b4d-4e5f-8a6b-7c8d9e0f1a01",
    kind: "element",
    group: "character",
    name: "Mara",
    handle: "mara",
    text: "",
    images: [],
    thumb_url: null,
    version: 1,
    created_at: "2026-11-21T10:00:00Z",
    updated_at: "2026-11-21T10:00:00Z",
    source: "mine",
    family: null,
    loras: [],
    needs: [],
    ...over,
  }) as Item;

function serve() {
  const made: Record<string, unknown>[] = [];
  answer("GET", {
    "/v1/studio/library": () => ({ data: { items: [] } }),
    "/v1/studio/takes": () => ({ data: { takes: [], next_cursor: null } }),
    "/v1/studio/takes/{take_id}/uses": () => ({ data: { uses: [] } }),
    "/v1/studio/loras": () => ({ data: { loras: [] } }),
    "/v1/studio/hosts": () => ({ data: { hosts: [] } }),
  });
  answer("POST", {
    "/v1/studio/library": ({ body }) => {
      const b = body as Record<string, unknown>;
      made.push(b);
      return {
        status: 201,
        data: item({
          kind: b.kind as Item["kind"],
          name: String(b.name),
          handle: (b.handle as string) ?? null,
        }),
      };
    },
  });
  return { made };
}

function gallery(takes: S<"Take">[]) {
  const reg = new Registry();
  reg.setPath("/gallery");
  render(
    <ToolsContext value={reg}>
      <Gallery initial={{ takes, next_cursor: null } as S<"TakePage">} />
    </ToolsContext>,
  );
  const lines: string[] = [];
  let n = 0;
  const run = (name: string, args: Record<string, unknown>) =>
    reg
      .run(
        { id: `11111111-2222-4333-8444-555555555555:1:${n++}`, name, args: args as never },
        (t) => void lines.push(t),
      )
      .then((r) => r.out as Outcome);
  return { run, lines };
}

describe("library dialogs from the Gallery", () => {
  it("Save as element opens New element with the handle; Save resolves with the id and @handle", async () => {
    const { made } = serve();
    const t = take({ title: "Mara, front" });
    const { run, lines } = gallery([t]);
    const o = run("saveAsElement", { take_id: t.id, handle: "@mara" });
    const dialog = await screen.findByRole("dialog", { name: "New element" });
    expect(lines).toEqual(["Waiting for you · Save in New element"]);
    expect(within(dialog).getAllByDisplayValue("mara")).toHaveLength(2); // the name and the handle
    expect(made).toEqual([]); // Save is the user's click
    fireEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(await o).toMatchObject({
      status: "done",
      text: "Saved @mara",
      result: { item_id: "5a0c1e2f-3b4d-4e5f-8a6b-7c8d9e0f1a01", handle: "@mara" },
    });
    expect(made[0]).toMatchObject({ kind: "element", handle: "mara", images: [{ take_id: t.id }] });
  });

  it("Save as look opens prefilled; closing it without Save is cancelled; a video is no element", async () => {
    const { made } = serve();
    const t = take({ title: "Rain street" });
    const v = video(3);
    const { run } = gallery([t, v]);
    const o = run("saveAsLook", { take_id: t.id, name: "Blue hour" });
    const dialog = await screen.findByRole("dialog", { name: "Save as look" });
    expect((within(dialog).getByLabelText("Name") as HTMLInputElement).value).toBe("Blue hour");
    fireEvent.click(within(dialog).getByRole("button", { name: "Close" }));
    expect(await o).toMatchObject({ status: "cancelled", text: "Save as look · cancelled" });
    expect(made).toEqual([]);
    expect(await run("saveAsElement", { take_id: v.id })).toMatchObject({
      status: "failed",
      text: "Save as element · an element's pictures are images",
    });
  });
});

const byKind = (ns: { kind: string }[]) => Object.fromEntries(ns.map((n) => [n.kind, n]));
const schemas = byKind([...coreNodes.nodes, ...zonosNodes.nodes, ...qwenNodes.nodes]);
const asWf = (w: object) =>
  ({ ...w, node_schemas: schemas, source: "template" }) as unknown as S<"Workflow">;
const echo = asWf(workflows.workflows.find((w) => w.id === "echo-voice-over")!);
const zonos = asWf(zonosWfs.workflows[0]);
const qwen = asWf(qwenWfs.workflows.find((w) => w.id === "qwen3-tts-voice-over")!);
// on a workflow that does not take clips: no Hear it, so Save voice needs only the rights tick
const ctx: VoiceCtx = {
  workflow: qwen,
  familyTitle: (f) => (f === "zonos3" ? "ZONOS3" : f),
  clone: { workflow: zonos, label: zonos.title, session: async () => "s1" },
  design: { workflow: echo, label: echo.title, session: async () => "s1" },
};

/** The hook alone, as a screen hosts it. */
function Host({ onReady }: { onReady: (ask: (a: LibraryAsk) => Promise<Outcome>) => void }) {
  const lib = useLibraryDialog();
  onReady((a) => lib.ask(a, () => undefined));
  return <>{lib.node}</>;
}
function host() {
  let ask: (a: LibraryAsk) => Promise<Outcome> = async () => ({ status: "failed", text: "" });
  render(<Host onReady={(f) => (ask = f)} />);
  return (a: LibraryAsk) => ask(a);
}

describe("voice dialogs", () => {
  it("Clone a voice opens on the take's part with the name; the rights tick and Save stay the user's", async () => {
    const { made } = serve();
    answer("GET", {
      "/v1/studio/library": () => ({ data: { items: [] } }),
      "/v1/studio/takes": () => ({ data: { takes: [], next_cursor: null } }),
    });
    const t = audio(20, { title: "Interview" });
    const ask = host();
    let o!: Promise<Outcome>;
    act(() => {
      o = ask({ kind: "clone", take: t, ctx, from_s: 3, to_s: 11, name: "Narrator" });
    });
    const dialog = await screen.findByRole("dialog", { name: "Clone from a clip" });
    expect((within(dialog).getByDisplayValue("Narrator") as HTMLInputElement).value).toBe(
      "Narrator",
    );
    const tick = within(dialog).getByRole("checkbox", { name: /I have the right/ });
    expect((tick as HTMLInputElement).checked).toBe(false);
    expect(
      within(dialog).getByRole("button", { name: "Save voice" }).hasAttribute("disabled"),
    ).toBe(true);
    fireEvent.click(tick);
    fireEvent.click(within(dialog).getByRole("button", { name: "Save voice" }));
    await screen.findByRole("dialog", { name: "Voice" }); // saved: back to the list, picked in Mine
    expect(made[0]).toMatchObject({
      kind: "voice",
      name: "Narrator",
      from_take: { take_id: t.id, in: 3, out: 11 },
    });
    fireEvent.click(
      within(screen.getByRole("dialog", { name: "Voice" })).getByRole("button", { name: "Close" }),
    );
    expect(await o).toMatchObject({ status: "done", text: "Saved Narrator" });
  });

  it("a starter or a voice is not edited here", async () => {
    serve();
    const ask = host();
    expect(
      await ask({ kind: "edit", item: item({ source: "starter", name: "Ada" }) }),
    ).toMatchObject({
      status: "failed",
      text: "Edit · Ada is a starter element: it can't be edited",
    });
    expect(await ask({ kind: "edit", item: item({ kind: "voice" }) })).toMatchObject({
      status: "failed",
    });
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("Design a voice opens with the description and name; closing it is cancelled", async () => {
    serve();
    const ask = host();
    let o!: Promise<Outcome>;
    act(() => {
      o = ask({ kind: "design", ctx, text: "Low and warm, slow", name: "Narrator" });
    });
    const dialog = await screen.findByRole("dialog", { name: "Design a voice" });
    expect(within(dialog).getByDisplayValue("Low and warm, slow")).toBeTruthy();
    expect(within(dialog).getByDisplayValue("Narrator")).toBeTruthy();
    expect(
      within(dialog).getByRole("button", { name: "Save as voice" }).hasAttribute("disabled"),
    ).toBe(true); // a try first: Hear it is the user's click
    fireEvent.click(within(dialog).getByRole("button", { name: "Close" }));
    expect(await o).toMatchObject({ status: "cancelled", text: "Design a voice · cancelled" });
  });
});

describe("Create's library actions", () => {
  const dialog = vi.fn(async (): Promise<Outcome> => ({ status: "done", text: "Saved" }));
  const c = {
    takes: [],
    library: [item({}), item({ id: "s", source: "starter", name: "Ada" })],
    looks: [],
    dialog,
    voiceCtx: undefined,
    plan: { gb: null, host: null },
    machines: [],
    fields: [],
    all: [],
    raws: {},
    picks: [],
    needs: [],
    handles: [],
    tiles: [],
  } as unknown as CreateCtx;
  const act1 = (name: string, args: Record<string, unknown>) =>
    createTools(c).actions[name](args, { progress: () => undefined }) as Promise<Outcome>;

  it("opens Edit on the item; refuses Hear it and a voice dialog with no audio workflow", async () => {
    expect(await act1("editItem", { item_id: "s" })).toMatchObject({ status: "done" });
    expect(dialog).toHaveBeenCalledWith(
      expect.objectContaining({ kind: "edit", item: expect.objectContaining({ id: "s" }) }),
      expect.any(Function),
    );
    expect(await act1("hear", {})).toMatchObject({ status: "failed" });
    expect(await act1("designVoice", { description: "warm" })).toMatchObject({
      status: "failed",
      text: "Design · no workflow here designs a voice: open an audio workflow",
    });
    expect(await act1("editItem", { item_id: "nope" })).toMatchObject({ status: "failed" });
  });
});
