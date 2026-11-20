// @vitest-environment jsdom
// The pipeline canvas's judge card (docs/mock/PipelineGraph.dc.html; owner's Q5, Q6): its title is edited on the
// card (double-click, Enter saves, Escape cancels), it shows its examples port and "then · ask me" for a branch
// without then.
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import askMe from "../../../contracts/fixtures/pipelines/valid.judge-ask-me.json";
import { PipelineCanvas } from "@/components/pipelines/canvas";
import type { PDoc } from "@/lib/pipeline";

vi.mock("@xyflow/react/dist/base.css", () => ({}));
vi.mock("@/components/graph/graph.css", () => ({}));

beforeAll(() => {
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

// positions given, so no layout runs
const doc = askMe.pipeline as unknown as PDoc;
const placed: PDoc = {
  ...doc,
  nodes: Object.fromEntries(
    Object.entries(doc.nodes).map(([id, n], i) => [id, { ...n, position: { x: i * 200, y: 0 } }]),
  ),
};

describe("the judge card", () => {
  it("is renamed in place and shows Ask me and its examples port", async () => {
    let now = placed;
    const edit = vi.fn((f: (d: PDoc) => PDoc) => {
      now = f(now);
    });
    render(
      <PipelineCanvas doc={placed} titles={{}} selected={null} onSelect={() => {}} edit={edit} />,
    );
    await waitFor(() => screen.getByTitle("Judge (double-click to rename)"));
    expect(screen.getByText("ask me")).toBeTruthy();
    expect(screen.getAllByText("examples").length).toBeGreaterThan(0);
    // cards stay visibility: hidden until xyflow measures them (never in jsdom): find by label, not role
    fireEvent.doubleClick(screen.getByTitle("Judge (double-click to rename)"));
    const input = screen.getByLabelText("Title on the card");
    fireEvent.change(input, { target: { value: "Clean image" } });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(now.nodes.judge.title).toBe("Clean image");
    fireEvent.doubleClick(screen.getByTitle("Judge (double-click to rename)"));
    fireEvent.change(screen.getByLabelText("Title on the card"), {
      target: { value: "nope" },
    });
    fireEvent.keyDown(screen.getByLabelText("Title on the card"), {
      key: "Escape",
    });
    expect(edit).toHaveBeenCalledTimes(1);
  });
});
