// @vitest-environment jsdom
// Graph view (components/graph; docs/mock/Graph.dc.html): the cards' short titles ("Model" with its family, "Encode"),
// a canvas's size as one row, and the inspector's "<family> · runs on <runtime>" line without the node's help.
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import template from "../../../runner_service/packs/qwen_image/workflows/qwen-image.json";
import paletteFixture from "../../../contracts/fixtures/graphs/palette.json";
import type { S } from "@/lib/api";
import { paletteOf, type Doc } from "@/lib/graph";
import { GraphView } from "@/components/graph/graph";
import { Panel, envText } from "@/components/graph/panel";
import { answer } from "./helpers";

// the cards' stylesheets are not needed here (vite's PostCSS setup is Next's)
vi.mock("@xyflow/react/dist/base.css", () => ({}));
vi.mock("@/components/graph/graph.css", () => ({}));
vi.mock("@/components/shell", () => ({ useUser: () => ({ id: "u1", roles: [] }) }));

beforeAll(() => {
  // jsdom has no ResizeObserver (xyflow measures the cards with it)
  globalThis.ResizeObserver ??= class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as never;
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const nodes = paletteFixture as S<"NodePalette">;
const palette = paletteOf(nodes);
const workflow = {
  ...template,
  source: "template",
  node_schemas: Object.fromEntries(
    Object.values(template.nodes as unknown as Doc["nodes"]).map((n) => [n.kind, palette[n.kind]]),
  ),
} as S<"Workflow">;

/** A card by node id (xyflow keeps cards hidden until it measures them, which jsdom never does). */
const card = (id: string) =>
  waitFor(() => {
    const el = document.querySelector<HTMLElement>(`[data-id="${id}"]`);
    if (!el) throw new Error(`no ${id} card`);
    return el;
  });

describe("Graph cards", () => {
  it("titles the model card Model and Encode Encode, and draws the canvas's size on one row", async () => {
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
    const model = await card("model");
    expect(within(model).getByText("Model")).toBeTruthy();
    expect(within(model).getByText("Qwen")).toBeTruthy();
    const encode = await card("encode");
    expect(within(encode).getByText("Encode")).toBeTruthy();
    const canvas = await card("canvas");
    expect(within(canvas).getByText("size")).toBeTruthy();
    expect(within(canvas).getByText("Auto · 1:1 · 1 MP")).toBeTruthy();
    expect(within(canvas).queryByText("width")).toBeNull();
    expect(within(canvas).queryByText("mode")).toBeNull();
  });
});

describe("Graph inspector", () => {
  it("says the family and the runtime as the board names it, without the node's help", () => {
    const doc = { ...workflow } as unknown as Doc;
    render(
      <Panel
        doc={doc}
        schemas={workflow.node_schemas}
        palette={palette}
        id="sample"
        problems={[]}
        overrides={{}}
        groupTitle="Qwen Image"
        edit={() => {}}
        onParamSet={() => {}}
      />,
    );
    expect(screen.getByText("Qwen Image · runs on PyTorch MPS")).toBeTruthy();
    expect(screen.queryByText(/Denoises from the seed/)).toBeNull();
  });

  it("names torch by its hosts' device: CUDA, CPU, else MPS", () => {
    const host = (kind?: "cuda" | "mps" | "cpu") =>
      ({ host: "h", env: "torch", ...(kind && { device: { kind } }) }) as S<"Host">;
    expect(envText("torch", [host("cuda")])).toBe("PyTorch CUDA");
    expect(envText("torch", [host("cpu")])).toBe("PyTorch CPU");
    expect(envText("torch", [host()])).toBe("PyTorch MPS");
    expect(envText("torch", null)).toBe("PyTorch MPS");
    expect(envText("mlx", [host("cuda")])).toBe("MLX");
  });
});
