// @vitest-environment jsdom
// The graphs' right-click menus (alpha.5 item 17, docs/mock/ContextMenus.dc.html frame 8): Create's Graph view (a
// node: Bypass, Duplicate, Delete node; the canvas: Add node…, Tidy layout, Undo, Redo; a wire's Add a node on it…
// puts the node between) and a pipeline (a step: Rename, Open memory, Duplicate, Delete step; read-only: only Open
// memory; the canvas: Add node…, Undo, Redo).
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import template from "../../../runner_service/packs/qwen_image/workflows/qwen-image.json";
import paletteFixture from "../../../contracts/fixtures/graphs/palette.json";
import askMe from "../../../contracts/fixtures/pipelines/valid.judge-ask-me.json";
import type { S } from "@/lib/api";
import { paletteOf, type Doc } from "@/lib/graph";
import { addOnWire, duplicateNode } from "@/lib/graph-edit";
import { duplicatePipelineNode, type PDoc } from "@/lib/pipeline";
import { closeMenu } from "@/components/context-menu";
import { GraphView } from "@/components/graph/graph";
import { PipelineCanvas } from "@/components/pipelines/canvas";
import { answer } from "./helpers";

vi.mock("@xyflow/react/dist/base.css", () => ({}));
vi.mock("@/components/graph/graph.css", () => ({}));
vi.mock("@/components/shell", () => ({ useUser: () => ({ id: "u1", roles: [] }) }));

beforeAll(() => {
  globalThis.ResizeObserver ??= class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as never;
});
afterEach(() => {
  act(closeMenu);
  cleanup();
  vi.restoreAllMocks();
});

const nodes = paletteFixture as S<"NodePalette">;
const palette = paletteOf(nodes);
const tdoc = template as unknown as Doc;
const workflow = {
  ...template,
  source: "template",
  node_schemas: Object.fromEntries(Object.values(tdoc.nodes).map((n) => [n.kind, palette[n.kind]])),
} as S<"Workflow">;

const labels = () =>
  [...document.querySelectorAll<HTMLElement>('.cm [data-lv="0"]')].map(
    (b) => b.querySelector(".lb")!.textContent,
  );
const item = (name: string) => {
  const b = [...document.querySelectorAll<HTMLElement>(".cm [data-lv]")].find((x) =>
    x.querySelector(".lb")!.textContent!.startsWith(name),
  );
  if (!b) throw new Error(`no menu item ${name}: ${labels().join(", ")}`);
  return b;
};
const header = () => document.querySelector(".cm .hd")?.textContent;
const rightClick = (el: Element) =>
  fireEvent.contextMenu(el, { button: 2, clientX: 50, clientY: 50 });
const card = (id: string) =>
  waitFor(() => {
    const el = document.querySelector<HTMLElement>(`.react-flow__node[data-id="${id}"]`);
    if (!el) throw new Error(`no ${id} card`);
    return el;
  });

describe("the edits", () => {
  it("a node added on a wire sits between its ends; a duplicate copies a node beside it", () => {
    // the wire lora.model → sample · model, a second LoRA stack put on it
    const r = addOnWire(tdoc, palette, "sample.model", "qwen-image/lora-stack", { x: 1, y: 2 });
    expect(r.doc.nodes[r.id].in).toEqual({ model: "lora.model" });
    expect(r.doc.nodes.sample.in!.model).toBe(`${r.id}.model`);
    expect(r.doc.nodes[r.id].position).toEqual({ x: 1, y: 2 });

    const at = {
      ...tdoc,
      nodes: { ...tdoc.nodes, lora: { ...tdoc.nodes.lora, position: { x: 0, y: 0 } } },
    };
    const d = duplicateNode(at, "lora");
    expect(d.id).not.toBe("lora");
    expect(d.doc.nodes[d.id]).toEqual({ ...at.nodes.lora, position: { x: 40, y: 40 } });
  });
});

describe("Graph view (frame 8)", () => {
  it("a node: Duplicate, Delete node; the canvas: Add node…, Tidy layout, Undo greyed until an edit", async () => {
    answer("GET", { "/v1/studio/loras": () => ({ data: { loras: [] } }) });
    answer("POST", {
      "/v1/studio/workflows/validate": () => ({ data: { problems: [], can_save: true } }),
    });
    render(
      <GraphView
        workflow={workflow}
        palette={{ map: palette, groups: nodes.groups }}
        topBar={() => null}
        overrides={{}}
        onParamSet={() => {}}
        onWorkflow={() => {}}
        onProblems={() => {}}
        onError={() => {}}
        generate={async () => {}}
        busy={false}
        anyActive={false}
      />,
    );
    const pane = await waitFor(() => {
      const el = document.querySelector(".react-flow__pane");
      if (!el) throw new Error("no canvas");
      return el;
    });
    rightClick(pane);
    expect(labels()).toEqual(["Add node…", "Tidy layout", "Undo", "Redo"]);
    expect(item("Undo").title).toBe("Nothing to undo");
    act(closeMenu);

    // a LoRA stack passes its model through: it has Bypass
    const id = "lora";
    rightClick(await card(id));
    expect(header()).toBe("LoRA Stack");
    expect(labels()).toEqual(["Bypass", "Duplicate", "Delete node"]);
    fireEvent.click(item("Bypass"));
    rightClick(await card(id));
    expect(labels()[0]).toBe("Bypassed");
    expect(item("Bypassed").getAttribute("aria-checked")).toBe("true");
    const before = document.querySelectorAll(".react-flow__node").length;
    fireEvent.click(item("Duplicate"));
    await waitFor(() =>
      expect(document.querySelectorAll(".react-flow__node").length).toBe(before + 1),
    );

    rightClick(pane);
    expect(item("Undo").title).toBeFalsy();
    fireEvent.click(item("Undo"));
    await waitFor(() => expect(document.querySelectorAll(".react-flow__node").length).toBe(before));

    rightClick(await card(id));
    fireEvent.click(item("Delete node"));
    await waitFor(() =>
      expect(document.querySelector(`.react-flow__node[data-id="${id}"]`)).toBeNull(),
    );

    rightClick(pane);
    fireEvent.click(item("Add node…"));
    expect(screen.getByRole("dialog", { name: "Add node" })).toBeTruthy();
  });
});

// positions given, so no layout runs
const pdoc = askMe.pipeline as unknown as PDoc;
const placed: PDoc = {
  ...pdoc,
  nodes: Object.fromEntries(
    Object.entries(pdoc.nodes).map(([id, n], i) => [id, { ...n, position: { x: i * 200, y: 0 } }]),
  ),
};

describe("a pipeline (frame 8)", () => {
  it("a step: Rename (in place), Duplicate, Delete step; the canvas: Add node…, Undo, Redo", async () => {
    let now = placed;
    const edit = vi.fn((f: (d: PDoc) => PDoc) => {
      now = f(now);
    });
    const undo = vi.fn();
    const { rerender } = render(
      <PipelineCanvas
        doc={placed}
        titles={{}}
        selected={null}
        onSelect={() => {}}
        edit={edit}
        history={{ undo }}
      />,
    );
    rightClick(await card("judge"));
    expect(header()).toBe("Judge");
    expect(labels()).toEqual(["Rename", "Duplicate", "Delete step"]);
    fireEvent.click(item("Rename"));
    const input = screen.getByLabelText("Title on the card");
    fireEvent.change(input, { target: { value: "Keep the best" } });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(now.nodes.judge.title).toBe("Keep the best");

    rightClick(await card("judge"));
    fireEvent.click(item("Duplicate"));
    expect(now.nodes.judge_2?.kind).toBe("judge");

    rightClick(await card("judge"));
    fireEvent.click(item("Delete step"));
    expect(now.nodes.judge).toBeUndefined();

    const pane = document.querySelector(".react-flow__pane")!;
    rightClick(pane);
    expect(labels()).toEqual(["Add node…", "Undo", "Redo"]);
    expect(item("Redo").title).toBe("Nothing to redo");
    fireEvent.click(item("Undo"));
    expect(undo).toHaveBeenCalledOnce();
    rightClick(pane);
    fireEvent.click(item("Add node…"));
    expect(screen.getByRole("listbox", { name: "Add node" })).toBeTruthy();

    // read-only (an execution view): the judge (its memory wired) has only Open memory
    const open = vi.fn();
    rerender(
      <PipelineCanvas
        doc={placed}
        titles={{}}
        selected={null}
        onSelect={() => {}}
        onOpenMemory={open}
      />,
    );
    rightClick(await card("judge"));
    expect(labels()).toEqual(["Open memory"]);
    fireEvent.click(item("Open memory"));
    expect(open).toHaveBeenCalledOnce();
    rightClick(await card("still"));
    expect(document.querySelector(".cm")).toBeNull();
  });

  it("a duplicate copies the node's settings, not its edge out", () => {
    const d = duplicatePipelineNode(placed, "judge");
    expect(d.id).toBe("judge_2");
    expect({ ...d.doc.nodes.judge_2, position: null }).toEqual({
      ...placed.nodes.judge,
      position: null,
    });
    expect(d.doc.edges.some((e) => e[0] === "judge_2")).toBe(false);
  });
});
