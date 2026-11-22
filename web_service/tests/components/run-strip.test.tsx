// @vitest-environment jsdom
// The run strip (docs/mock/CreateMediaRefs.dc.html, Main.dc.html): a node with phases (H3) draws a chip per phase,
// the current one lit with its step / of; an image or video strip ends with Saving, "Saving…" while the output
// uploads (its done time is lib/runs.ts segments', tests/studio.test.ts).
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, within } from "@testing-library/react";
import coreNodes from "../../../contracts/fixtures/registry/nodes.valid.json";
import h3Nodes from "../../../contracts/fixtures/registry/minimax-h3.nodes.valid.json";
import h3Workflows from "../../../contracts/fixtures/registry/minimax-h3.workflows.valid.json";
import { Create } from "@/components/create/create";
import type { S } from "@/lib/api";
import { answer } from "./helpers";

vi.mock("@/lib/runs", async (orig) => ({
  ...(await orig<typeof import("@/lib/runs")>()),
  followSession: () => () => undefined,
}));

beforeAll(() => {
  globalThis.requestAnimationFrame ??= (f: FrameRequestCallback) =>
    setTimeout(() => f(0), 0) as unknown as number;
  HTMLElement.prototype.hidePopover ??= () => undefined;
  Element.prototype.scrollIntoView ??= () => undefined;
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const workflow = {
  ...h3Workflows.workflows[0],
  node_schemas: Object.fromEntries([...coreNodes.nodes, ...h3Nodes.nodes].map((n) => [n.kind, n])),
  source: "template",
} as unknown as S<"Workflow">;

function start(progress: S<"RunProgress">) {
  const run = {
    id: "r1",
    session_id: "s1",
    workflow_id: workflow.id,
    inputs: {},
    status: "running",
    progress,
    error: null,
    outputs: [],
    host: "mac",
    attempt: 1,
  } as unknown as S<"Run">;
  answer("GET", {
    "/v1/studio/sessions/{session_id}/runs": () => ({ data: { runs: [run] } }),
    "/v1/studio/sessions/{session_id}/takes": () => ({ data: { takes: [] } }),
    "/v1/studio/library": () => ({ data: { items: [] } }),
    "/v1/studio/loras": () => ({ data: { loras: [] } }),
  });
  answer("POST", {});
  render(
    <Create
      initial={{ sessions: [], workflows: [workflow], sessionId: "s1", runs: [run], takes: [] }}
    />,
  );
  return screen.getByRole("group", { name: "Run progress" });
}

const chips = (strip: HTMLElement) =>
  [...strip.querySelectorAll<HTMLElement>(".step")].map((s) => s.textContent);

describe("Create: the run strip", () => {
  it("H3: a chip per phase, the current one with its bar, then Saving", () => {
    const strip = start({ node: "generate", step: 4, of: 6, phase_id: "sample" });
    expect(chips(strip)).toEqual([
      "Encode",
      "Sample4 / 6",
      "Decode audio",
      "Decode video",
      "Saving",
    ]);
    expect(within(strip).getByRole("progressbar", { name: "Sample progress" })).toBeTruthy();
    expect(strip.querySelector("[aria-current=step]")?.textContent).toBe("Sample4 / 6");
  });

  it("Saving… while the output uploads, with no bar", () => {
    const strip = start({ node: "output", step: 1, of: 1, phase: "saving" });
    expect(strip.querySelector("[aria-current=step]")?.textContent).toBe("Saving…");
    expect(within(strip).queryByRole("progressbar")).toBeNull();
    expect(chips(strip).slice(0, 4)).toEqual(["Encode", "Sample", "Decode audio", "Decode video"]);
  });
});
