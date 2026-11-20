// @vitest-environment jsdom
// The Judge panel as docs/mock/PipelineGraph.dc.html draws it (owner's Q5-Q7): no title field, takes-of, if-passes,
// learns-from, remove-node or × on the checks (wires on the canvas, the card's title and Backspace do those);
// emptying a check removes it; the tiles Keep / If none pass / On retry / Then map to the pipeline's data (Then: Ask
// me is a branch without then); Test on takes shows each take's verdict and reason.
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import pipelineFixture from "../../../contracts/fixtures/http/Pipeline.valid.1.json";
import askMe from "../../../contracts/fixtures/pipelines/valid.judge-ask-me.json";
import workflowsFixture from "../../../contracts/fixtures/pipelines/workflows.json";
import paletteFixture from "../../../contracts/fixtures/graphs/palette.json";
import { PipelineEditor } from "@/components/pipelines/editor";
import type { CanvasProps } from "@/components/pipelines/canvas";
import type { S } from "@/lib/api";
import type { PDoc } from "@/lib/pipeline";
import { answer, take } from "./helpers";

const canvas: CanvasProps[] = [];
vi.mock("next/dynamic", () => ({
  default: () => (p: CanvasProps) => {
    canvas.push(p);
    return null;
  },
}));
vi.mock("next/navigation", () => ({ useRouter: () => ({ push: vi.fn(), replace: vi.fn() }) }));
vi.mock("next/link", () => ({
  default: ({ href, children }: { href: string; children: React.ReactNode }) => (
    <a href={href}>{children}</a>
  ),
}));
const picked = [take(), take({ kind: "video" })];
vi.mock("@/components/gallery/picker", () => ({
  GalleryPicker: ({ open, onPick }: { open: boolean; onPick: (t: unknown[]) => void }) =>
    open ? (
      <button type="button" onClick={() => onPick(picked)}>
        pick two
      </button>
    ) : null,
}));
vi.stubGlobal(
  "EventSource",
  class {
    addEventListener() {}
    close() {}
  },
);

beforeAll(() => {
  HTMLDialogElement.prototype.showModal ??= function (this: HTMLDialogElement) {
    this.open = true;
  };
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  canvas.length = 0;
});

const doc = askMe.pipeline as unknown as PDoc;
const pipeline = {
  ...(pipelineFixture as unknown as S<"Pipeline">),
  ...doc,
  last_batch: null,
  problems: [],
} as S<"Pipeline">;
const workflows = Object.entries(workflowsFixture.workflows).map(([id, w]) => ({
  id,
  node_schemas: {},
  ...w,
})) as unknown as S<"Workflow">[];
const latest = () => canvas.at(-1)!.doc;
const judge = () => latest().nodes.judge as S<"PipelineJudgeNode">;

function open() {
  answer("GET", {});
  const posts = answer("POST", {
    "/v1/studio/pipelines/validate": () => ({
      data: { problems: [], can_save: true, can_run: true },
    }),
    "/v1/studio/pipelines/{pipeline_id}/judge-tests": () => ({
      status: 202,
      data: { ...test, status: "queued", takes: [], verdict: null },
    }),
  });
  render(
    <PipelineEditor
      pipeline={pipeline}
      workflows={workflows}
      nodes={paletteFixture as S<"NodePalette">}
    />,
  );
  act(() => canvas.at(-1)!.onSelect("judge"));
  return { panel: screen.getByRole("complementary", { name: "Inspector" }), posts };
}

const test = {
  id: "5b9d1f3a-7c2e-4a6b-8d0f-1e3c5a7b9d20",
  pipeline_id: pipeline.id,
  node: "judge",
  take_ids: picked.map((t) => t.id),
  status: "succeeded",
  run_id: null,
  verdict: "pass",
  first_failed: null,
  takes: [
    {
      take_id: picked[0].id,
      passes: false,
      rank: 2,
      picked: false,
      reason: "the fox has five legs",
      checks: [{ criterion: "No extra or missing limbs", level: "must", pass: false }],
    },
    { take_id: picked[1].id, passes: true, rank: 1, picked: true, reason: "clean", checks: [] },
  ],
  error: null,
  created_at: "2026-11-20T10:00:00Z",
};

describe("the Judge panel (PipelineGraph board)", () => {
  it("has only the board's controls and the four tiles from the pipeline's data", () => {
    const { panel } = open();
    const p = within(panel);
    for (const gone of [
      "Title on the card",
      "Judges the takes of",
      "If it passes",
      "Learns from",
      "Remove this node",
    ])
      expect(p.queryByText(gone)).toBeNull();
    expect(p.queryByRole("button", { name: /Remove check/ })).toBeNull();
    const tile = (name: string) => p.getByText(name).closest("label")!.querySelector("select")!;
    expect(tile("Keep").selectedOptions[0].textContent).toBe("Best 1 of 4");
    expect(tile("If none pass").selectedOptions[0].textContent).toBe("Retry, max 2");
    expect(tile("On retry").value).toBe("rewrite");
    expect(tile("Then").selectedOptions[0].textContent).toBe("Ask me");
    expect(p.getByRole("button", { name: "Open memory" })).toBeTruthy();
    // Then → a node, and back to Ask me: the branch's then set and dropped
    fireEvent.change(tile("Then"), { target: { value: "clip" } });
    expect(latest().edges.find((e) => e[0] === "judge")?.[1]).toMatchObject({ then: "clip" });
    fireEvent.change(tile("Then"), { target: { value: "" } });
    expect(latest().edges.find((e) => e[0] === "judge")?.[1]).toEqual({
      pass: "clip",
      fail: "rewrite",
      max: 2,
    });
    // If none pass → Retry, max 3; then no retry (Ask me in Suggest)
    fireEvent.change(tile("If none pass"), { target: { value: "3" } });
    expect(latest().edges.find((e) => e[0] === "judge")?.[1]).toMatchObject({ max: 3 });
    fireEvent.change(tile("If none pass"), { target: { value: "" } });
    expect(latest().edges.find((e) => e[0] === "judge")?.[1]).toBe("clip");
  });

  it("removes a check emptied, and keeps one being typed", () => {
    const { panel } = open();
    const first = within(panel).getByRole("textbox", { name: "Check 1" });
    fireEvent.change(first, { target: { value: "" } });
    expect(judge().criteria.must).toHaveLength(3);
    fireEvent.blur(first);
    expect(judge().criteria.must).toEqual(["No text or watermarks", "Matches the brief"]);
  });

  it("tests the saved judge on picked takes and shows each verdict and reason", async () => {
    const { panel, posts } = open();
    answer("GET", { "/v1/studio/judge-tests/{test_id}": () => ({ data: test }) });
    fireEvent.click(within(panel).getByRole("button", { name: "Test on takes" }));
    fireEvent.click(screen.getByRole("button", { name: "pick two" }));
    await waitFor(() =>
      expect(posts).toHaveBeenCalledWith("/v1/studio/pipelines/{pipeline_id}/judge-tests", {
        params: { path: { pipeline_id: pipeline.id } },
        body: { node: "judge", take_ids: picked.map((t) => t.id) },
      }),
    );
    expect(await within(panel).findByText("queued…")).toBeTruthy();
    const result = await within(panel).findByText("pass · keeps take 2", {}, { timeout: 4000 });
    const list = within(result.closest("section")!);
    expect(list.getByText("the fox has five legs")).toBeTruthy();
    expect(list.getByText("fails: No extra or missing limbs")).toBeTruthy();
    expect(list.getByText("kept")).toBeTruthy();
  });
});
