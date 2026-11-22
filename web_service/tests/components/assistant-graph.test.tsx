// @vitest-environment jsdom
// Graph view's screen actions for the assistant (components/graph/assistant.ts; plan 18 §22.5, board frame 5): the
// edit helpers checked first (an unknown kind, a wire whose types differ, a bad param value are refused and change
// nothing), each answered with the graph's problems; the nodes it added are ringed; one "Undo assistant changes"
// takes them all back; Generate is Create's card and nothing runs until its Yes.
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import template from "../../../runner_service/packs/qwen_image/workflows/qwen-image.json";
import paletteFixture from "../../../contracts/fixtures/graphs/palette.json";
import type { S } from "@/lib/api";
import { paletteOf, type Doc } from "@/lib/graph";
import { Registry, ToolsContext } from "@/components/assistant/tools";
import { asked, done, type Asked, type Outcome } from "@/lib/assistant";
import { GraphView } from "@/components/graph/graph";
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
  cleanup();
  vi.restoreAllMocks();
});

const nodes = paletteFixture as S<"NodePalette">;
const palette = paletteOf(nodes);
const workflow = {
  ...template,
  source: "template",
  version: 4,
  title: "Rain still",
  node_schemas: Object.fromEntries(
    Object.values(template.nodes as unknown as Doc["nodes"]).map((n) => [n.kind, palette[n.kind]]),
  ),
} as S<"Workflow">;

const card = (id: string) => document.querySelector<HTMLElement>(`[data-id="${id}"] .gn`);

function start() {
  answer("GET", { "/v1/studio/loras": () => ({ data: { loras: [] } }) });
  // nothing saves here: the check says no
  answer("POST", {
    "/v1/studio/workflows/validate": () => ({ data: { problems: [], can_save: false } }),
  });
  const generate = vi.fn(async () => ({ ids: ["r1"], error: "" }));
  const reg = new Registry();
  reg.setPath("/");
  render(
    <ToolsContext value={reg}>
      <GraphView
        workflow={workflow}
        palette={{ map: palette, groups: nodes.groups }}
        topBar={(_badge, extra) => <div>{extra}</div>}
        overrides={{}}
        onParamSet={() => {}}
        onWorkflow={() => {}}
        onProblems={() => {}}
        onError={() => {}}
        generate={generate}
        askGenerate={async () => ({
          card: {
            kind: "generate",
            title: "Generate 1 image?",
            rows: [["Model", "Qwen Image"]],
            tags: ["uses the GPU"],
            go: "GENERATE",
            fine: "Same as the Generate button on the screen.",
          },
          yes: async () => done("not this one"),
        })}
        busy={false}
        anyActive={false}
      />
    </ToolsContext>,
  );
  let n = 0;
  const call = async (name: string, args: Record<string, unknown> = {}) =>
    (await act(() =>
      reg
        .run(
          { id: `11111111-2222-4333-8444-555555555555:1:${n++}`, name, args: args as never },
          () => undefined,
        )
        .then((r) => r.out),
    )) as Outcome | Asked;
  return { reg, call, generate };
}
const out = (o: Outcome | Asked) => {
  if (asked(o)) throw new Error("a card");
  return o;
};

describe("Graph's actions", () => {
  it("registers over Create with the graph's Sees and summary", async () => {
    const { reg } = start();
    await waitFor(() => expect(card("sample")).toBeTruthy());
    expect(reg.current()?.spec.screen).toBe("graph");
    expect(reg.current()?.spec.sees.slice(0, 2)).toEqual(["Create · Graph", "Rain still · v4"]);
    const f = reg.summary().fields as unknown as {
      node_count: number;
      workflow: { version: number };
    };
    expect(f.node_count).toBe(Object.keys(template.nodes).length);
    expect(f.workflow.version).toBe(4);
  });

  it("checks each edit first, answers with the problems, rings what it added, and one Undo takes all back", async () => {
    const { call } = start();
    await waitFor(() => expect(card("sample")).toBeTruthy());
    expect(out(await call("addNode", { kind: "no/such" }))).toMatchObject({ status: "failed" });
    expect(out(await call("removeNodes", { ids: ["nope"] }))).toMatchObject({ status: "failed" });
    expect(
      out(await call("connect", { node: "output", key: "media", ref: "prompt.text" })),
    ).toMatchObject({ status: "failed" });
    expect(
      out(await call("setParam", { node: "sample", key: "steps", value: "many" })),
    ).toMatchObject({ status: "failed" });
    expect(screen.queryByRole("button", { name: "Undo assistant changes" })).toBeNull();

    const add = out(await call("addNode", { kind: "seed", near: "sample" }));
    expect(add).toMatchObject({ status: "done", result: { node: "seed_2" } });
    expect(add.text).toMatch(/^Added Seed/);
    expect(add.undo).toBeUndefined(); // one Undo above the graph, none per line
    const wire = out(await call("connect", { node: "sample", key: "seed", ref: "seed_2.seed" }));
    expect(wire.text).toMatch(/^Wired Seed .* into Sample seed$/);
    expect(wire.result).toMatchObject({ valid: expect.any(Boolean), problems: expect.any(Array) });
    const set = out(await call("setParam", { node: "sample", key: "steps", value: 8 }));
    expect(set).toMatchObject({ status: "done" });
    expect(out(await call("setBypass", { node: "lora", on: true })).text).toMatch(/^Bypassed/);

    await waitFor(() => expect(card("seed_2")?.dataset.ai).toBe("true"));
    expect(card("sample")?.dataset.ai).toBeUndefined();
    expect(screen.getByText("ringed = added by the assistant")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Undo assistant changes" }));
    await waitFor(() => expect(card("seed_2")).toBeNull());
    expect(screen.queryByRole("button", { name: "Undo assistant changes" })).toBeNull();
    expect(card("lora")?.dataset.bypass).toBeUndefined();
  });

  it("Generate is Create's card; the graph's Generate runs only on Yes", async () => {
    const { call, generate } = start();
    await waitFor(() => expect(card("sample")).toBeTruthy());
    const o = await call("generate");
    if (!asked(o)) throw new Error("no card");
    expect(o.card).toMatchObject({ kind: "generate", go: "GENERATE", tags: ["uses the GPU"] });
    expect(generate).not.toHaveBeenCalled();
    expect(await act(() => o.yes())).toMatchObject({
      status: "done",
      text: "You clicked GENERATE",
      result: { run_ids: ["r1"] },
    });
    expect(generate).toHaveBeenCalledTimes(1);
  });
});
