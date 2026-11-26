// @vitest-environment jsdom
// The places' right-click menus (alpha.5 item 17, docs/mock/ContextMenus.dc.html frames 1-5 and 7): each item calls
// the action its button or key does. Timeline: a clip, a selection, a gap (Close gap, one edit), a track header, the
// ruler; Gallery: a take, a selection; Create's session row; an @ chip in a prompt.
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import fixture from "../../../contracts/fixtures/http/Timeline.valid.json";
import { api, type S } from "@/lib/api";
import { apply, factsOf, type Op } from "@/lib/timeline";
import { closeMenu } from "@/components/context-menu";
import { menuTarget } from "@/components/timeline/lanes";
import { reloadLibrary } from "@/lib/library";
import { answer, take } from "./helpers";

const push = vi.fn();
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push, replace: vi.fn(), refresh: vi.fn() }),
  usePathname: () => "/timeline",
  useSearchParams: () => new URLSearchParams(),
}));
vi.mock("next/link", () => ({
  default: ({ href, children, ...rest }: { href: string; children: React.ReactNode }) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));
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
    this.dispatchEvent(new Event("close"));
  };
  HTMLElement.prototype.hidePopover ??= () => undefined;
  Element.prototype.scrollIntoView ??= () => undefined;
  document.execCommand ??= () => false;
});
afterEach(() => {
  act(closeMenu);
  cleanup();
  vi.restoreAllMocks();
  push.mockReset();
});

const { TimelineEditor } = await import("@/components/timeline/editor");
const { Gallery } = await import("@/components/gallery/gallery");
const { SessionMenu } = await import("@/components/create/sessions");
const { PromptArea } = await import("@/components/create/prompt");

const reply = (data: unknown, status = 200) =>
  Promise.resolve({
    data: status < 300 ? data : undefined,
    error: status < 300 ? undefined : { detail: "no" },
    response: new Response(null, { status }),
  });
const buttons = (lv: number) => [
  ...document.querySelectorAll<HTMLElement>(`.cm [data-lv="${lv}"]`),
];
const labels = () => buttons(0).map((b) => b.querySelector(".lb")!.textContent);
const item = (name: string) => {
  const b = [...document.querySelectorAll<HTMLElement>(".cm [data-lv]")].find((x) =>
    x.querySelector(".lb")!.textContent!.startsWith(name),
  );
  if (!b) throw new Error(`no menu item ${name}: ${labels().join(", ")}`);
  return b;
};
const header = () => document.querySelector(".cm .hd")?.textContent;
const rightClick = (el: Element, x = 100) =>
  fireEvent.contextMenu(el, { button: 2, clientX: x, clientY: 10 });

it("the lanes say what a right-click is on", () => {
  document.body.innerHTML = `<div data-clip="c"><b>x</b></div><div data-ruler></div><span data-head="A2"></span>
    <div data-track="A2"></div><button data-marker="m1"></button><p></p>`;
  const q = (s: string) => document.querySelector(s)!;
  expect(menuTarget(q("b"), 1)).toEqual({ kind: "clip", id: "c" });
  expect(menuTarget(q("[data-ruler]"), 2)).toEqual({ kind: "ruler", t: 2 });
  expect(menuTarget(q("[data-head]"), 2)).toEqual({ kind: "track", track: "A2" });
  expect(menuTarget(q("[data-track]"), 3)).toEqual({ kind: "gap", track: "A2", t: 3 });
  expect(menuTarget(q("[data-marker]"), 3)).toEqual({ kind: "marker", id: "m1" });
  expect(menuTarget(q("p"), 3)).toBeNull();
  document.body.innerHTML = "";
});

describe("Timeline", () => {
  const TL = fixture as unknown as S<"Timeline">;
  function start() {
    let cur: S<"Timeline"> = structuredClone(TL);
    const edits: Op[][] = [];
    answer("GET", {
      "/v1/studio/timelines": () => ({ data: { timelines: [cur] } }),
      "/v1/studio/timelines/{timeline_id}": () => ({ data: cur }),
      "/v1/studio/timelines/{timeline_id}/exports": () => ({ data: { jobs: [] } }),
      "/v1/studio/workflows": () => ({ data: { workflows: [] } }),
      "/v1/studio/takes": () => ({ data: { takes: [], next_cursor: null } }),
      "/v1/studio/library": () => ({ data: { items: [] } }),
      "/v1/studio/hosts": () => ({ data: { hosts: [] } }),
      "/v1/studio/models": () => ({ data: { hosts: [], licences: [], models: [] } }),
      "/v1/studio/queue": () => ({ data: { runs: [], hosts: [] } }),
      "/v1/studio/takes/{take_id}/uses": () => ({ data: { uses: [] } }),
      "/v1/studio/timelines/{timeline_id}/clips/{clip_id}/actions": () => ({
        data: { actions: [] },
      }),
    });
    vi.spyOn(api, "POST").mockImplementation(((path: string, init: { body?: unknown }) => {
      if (!path.endsWith("/edits")) return reply(undefined, 404);
      const body = init.body as S<"TimelineEdits">;
      edits.push(body.ops as Op[]);
      let d: S<"Timeline"> = cur;
      for (const o of body.ops) {
        const r = apply(d, o as Op, factsOf(cur.takes));
        if (!("document" in r)) return reply(undefined, 422);
        d = { ...d, ...r.document } as S<"Timeline">;
      }
      cur = { ...d, revision: cur.revision + 1 };
      return reply({
        revision: cur.revision,
        dry_run: false,
        timeline: cur,
        diff: {},
        warnings: [],
      });
    }) as never);
    render(<TimelineEditor id={TL.id} />);
    return { edits };
  }
  const clip = (id: string) => document.querySelector(`button[data-clip="${id}"]`)!;

  it("a V1 clip: selects it, the inspector's actions; Replace with › ticks the current take; Remove clip", async () => {
    const { edits } = start();
    await screen.findByRole("heading", { name: `Timeline: ${TL.name}` });
    rightClick(clip("c"));
    // the offers come in once read (none for this clip)
    expect(labels()[0]).toBe("Reading the clip's actions…");
    await waitFor(() => expect(labels()[0]).toBe("Replace with another take"));
    expect(labels()).toEqual([
      "Replace with another take",
      "Redo this shot",
      "Use in",
      "Re-render…",
      "Split at playhead",
      "Mark clip as range",
      "Leave a gap",
      "Remove clip",
    ]);
    expect(item("Split at playhead").title).toBe("The playhead is not on this clip");
    expect(clip("c").getAttribute("aria-pressed")).toBe("true");
    fireEvent.click(item("Replace with another take"));
    expect(buttons(1).map((b) => b.getAttribute("aria-checked"))).toEqual(["false", "true"]);
    fireEvent.click(buttons(1)[0]);
    await waitFor(() =>
      expect(edits.at(-1)).toEqual([
        { op: "replace", clip: "c", take_id: "aaaaaaaa-0000-4000-8000-000000000003" },
      ]),
    );
    rightClick(clip("c"));
    fireEvent.click(item("Remove clip"));
    await waitFor(() => expect(edits.at(-1)).toEqual([{ op: "remove", clip: "c" }]));
  });

  it("several clips: the selection's menu; Remove 2 clips removes both", async () => {
    const { edits } = start();
    await screen.findByRole("heading", { name: `Timeline: ${TL.name}` });
    fireEvent.click(clip("a"));
    fireEvent.click(clip("c"), { shiftKey: true });
    rightClick(clip("c"));
    expect(header()).toBe("2 clips");
    expect(item("Replace with another take").title).toBe("One clip at a time");
    fireEvent.click(item("Remove 2 clips"));
    await waitFor(() => expect(edits.flat().filter((o) => o.op === "remove")).toHaveLength(2));
  });

  it("a gap on an audio track: Close gap moves what is after it, as one edit", async () => {
    const { edits } = start();
    await screen.findByRole("heading", { name: `Timeline: ${TL.name}` });
    // 0.5 s into A2, before its clip at 1.0 s (40 px a second after the 72 px names)
    rightClick(document.querySelector('[data-track="A2"]')!, 72 + 20);
    expect(header()).toMatch(/^A2 at /);
    expect(labels()).toEqual([
      "New shot",
      "New line",
      "Add clips to A2",
      "Add marker",
      "Close gap",
    ]);
    fireEvent.click(item("Close gap"));
    await waitFor(() => expect(edits.at(-1)).toEqual([{ op: "move", clip: "m", at: 0 }]));
  });

  it("a track header: Mute; the ruler: Add marker at the pointer, Clear range greyed", async () => {
    const { edits } = start();
    await screen.findByRole("heading", { name: `Timeline: ${TL.name}` });
    rightClick(document.querySelector('[data-head="A2"]')!);
    expect(labels()).toEqual([
      "Mute",
      "Add clips to A2",
      "New line",
      "Add audio track",
      "Remove track",
    ]);
    fireEvent.click(item("Mute"));
    await waitFor(() => expect(edits.at(-1)).toEqual([{ op: "track", track: "A2", mute: true }]));
    rightClick(document.querySelector("[data-ruler]")!, 72 + 80);
    expect(item("Clear range").title).toBe("No range set");
    expect(item("Snapping").getAttribute("aria-checked")).toBe("true");
    fireEvent.click(item("Add marker"));
    await waitFor(() => expect(edits.at(-1)).toMatchObject([{ op: "marker", at: 2 }]));
  });
});

describe("Gallery", () => {
  const a = take({ title: "Fox at dusk", made_with: { seed: 43, prompt: "fox" } as S<"MadeWith"> });
  const b = take({ title: "Snow owl" });
  function start() {
    const patches: unknown[] = [];
    answer("GET", {
      "/v1/studio/takes/{take_id}": () => ({ data: { ...a, children: [] } }),
      "/v1/studio/takes/{take_id}/uses": () => ({
        data: {
          uses: [
            { workflow_id: "ltx-i2v", param: "image", title: "LTX Image to video", inputs: {} },
          ],
        },
      }),
      "/v1/studio/tools": () => ({ data: { tools: [] } }),
      "/v1/studio/library": () => ({ data: { items: [] } }),
      "/v1/studio/looks": () => ({ data: { looks: [] } }),
      "/v1/studio/loras": () => ({ data: { loras: [] } }),
    });
    vi.spyOn(api, "PATCH").mockImplementation(((_: string, init: { body: unknown }) => {
      patches.push(init.body);
      return reply({ ...a, starred: true });
    }) as never);
    render(<Gallery initial={{ takes: [a, b], next_cursor: null }} />);
    return { patches };
  }
  const card = (title: string) =>
    screen.getByText(title, { selector: "b" }).closest('[role="listitem"]')!;

  it("a take: the side panel's actions in its order; Use in › lists its offers; Star patches it", async () => {
    const { patches } = start();
    rightClick(card("Fox at dusk"));
    await waitFor(() => expect(item("Use in").getAttribute("aria-haspopup")).toBe("menu"));
    expect(labels()).toEqual([
      "Use in",
      "Add to timeline",
      "Reuse settings",
      "Star",
      "Rename",
      "Full screen",
      "Save as element",
      "Save as look",
      "Save as pipeline",
      "Export file",
      "Delete",
    ]);
    fireEvent.click(item("Use in"));
    fireEvent.click(item("LTX Image to video"));
    expect(push).toHaveBeenCalledWith(`/?use=${a.id}&w=ltx-i2v`);
    rightClick(card("Fox at dusk"));
    fireEvent.click(item("Star"));
    await waitFor(() => expect(patches).toEqual([{ starred: true }]));
    rightClick(card("Fox at dusk"));
    fireEvent.click(item("Delete"));
    expect(await screen.findByRole("dialog", { name: "Delete this take?" })).toBeTruthy();
  });

  it("Select several: a card outside the picked ones is picked alone; the selection bar's actions", async () => {
    start();
    fireEvent.click(screen.getByRole("button", { name: "Select several" }));
    rightClick(card("Snow owl"));
    expect(header()).toBe("1 take");
    expect(labels()).toEqual([
      "Star",
      "Add to timeline",
      "Save as element",
      "Done",
      "Delete 1 take",
    ]);
    await waitFor(() => expect(screen.getByText("1 selected")).toBeTruthy());
    fireEvent.click(item("Delete 1 take"));
    expect(await screen.findByRole("dialog", { name: "Delete 1 takes?" })).toBeTruthy();
  });
});

describe("Create", () => {
  it("a session row: Open, Rename, Delete greyed while a run is going", async () => {
    answer("GET", {
      "/v1/studio/queue": () => ({
        data: { runs: [{ session_id: "s2", purpose: "run" }], hosts: [] },
      }),
    });
    const onOpen = vi.fn();
    const s = (id: string, title: string) => ({ id, title }) as S<"StudioSession">;
    render(
      <SessionMenu
        sessions={[s("s1", "Snow owl"), s("s2", "Fox at dusk")]}
        current="s1"
        hint=""
        onOpen={onOpen}
        onNew={vi.fn()}
        onRenamed={vi.fn()}
        onDeleted={vi.fn()}
        onError={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: /Change session/ }));
    const row = await screen.findByText("Fox at dusk", { selector: "button" });
    await waitFor(() => expect(row.parentElement!.textContent).toContain("running"));
    rightClick(row);
    expect(labels()).toEqual(["Open", "Rename", "Delete"]);
    expect(item("Delete").title).toBe("A run is going: stop it first to delete");
    fireEvent.click(item("Open"));
    expect(onOpen).toHaveBeenCalledWith("s2");
  });

  it("an @ chip: Edit for a known one, New element for an unknown; Write as text, Remove from prompt", async () => {
    answer("GET", {
      "/v1/studio/library": () => ({
        data: {
          items: [
            {
              id: "e1",
              kind: "element",
              handle: "fox",
              name: "Fox",
              group: "character",
              images: [],
            },
            {
              id: "e2",
              kind: "element",
              handle: "snowfield",
              name: "Snowfield",
              group: "location",
              images: [],
            },
          ],
        },
      }),
    });
    // the library is this tab's module state: read it again with these elements
    await act(() => reloadLibrary());
    const change = vi.fn();
    const Host = () => {
      const [v, setV] = React.useState("a @fox at dusk, @owl overhead");
      return (
        <PromptArea
          aria-label="Prompt"
          value={v}
          onChange={(x) => {
            change(x);
            setV(x);
          }}
        />
      );
    };
    const React = await import("react");
    render(<Host />);
    const box = screen.getByLabelText<HTMLTextAreaElement>("Prompt");
    // a chip is the highlight layer's span under the textarea: the pointer over it
    const chipAt = async (handle: string) => {
      const span = await waitFor(() => {
        const el = [...document.querySelectorAll<HTMLElement>("[data-start]")].find(
          (x) => x.textContent === `@${handle}`,
        );
        if (!el) throw new Error("no chip");
        return el;
      });
      span.getClientRects = () =>
        [{ left: 0, right: 50, top: 0, bottom: 20 }] as unknown as DOMRectList;
      for (const o of document.querySelectorAll<HTMLElement>("[data-start]"))
        if (o !== span) o.getClientRects = () => [] as unknown as DOMRectList;
      fireEvent.contextMenu(box, { button: 2, clientX: 10, clientY: 10 });
    };
    await waitFor(() => expect(document.querySelector(".mention-unknown")).toBeTruthy());
    await chipAt("fox");
    expect(header()).toBe("@fox · Fox");
    expect(labels()).toEqual(["Edit @fox", "Replace with", "Write as text", "Remove from prompt"]);
    fireEvent.click(item("Replace with"));
    fireEvent.click(item("@snowfield"));
    expect(change).toHaveBeenLastCalledWith("a @snowfield at dusk, @owl overhead");
    await chipAt("owl");
    expect(header()).toBe("@owl · no element");
    expect(labels()[0]).toBe("New element @owl");
    fireEvent.click(item("Write as text"));
    expect(change).toHaveBeenLastCalledWith("a @snowfield at dusk, @@owl overhead");
    await chipAt("snowfield");
    fireEvent.click(item("Remove from prompt"));
    expect(change).toHaveBeenLastCalledWith("a  at dusk, @@owl overhead");
  });
});
