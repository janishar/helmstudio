import { readdirSync, readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import paletteFixture from "../../contracts/fixtures/graphs/palette.json";
import workflowsFixture from "../../contracts/fixtures/pipelines/workflows.json";
import type { S } from "@/lib/api";
import { paletteOf, type Doc } from "@/lib/graph";
import {
  addPipelineNode,
  bindCandidates,
  blankPipeline,
  chooseWorkflow,
  connectNodes,
  disconnectNodes,
  gateCandidates,
  order,
  outputCandidates,
  pipelineProblems,
  removePipelineNodes,
  settable,
  stepNumbers,
  type PDoc,
} from "@/lib/pipeline";

const DIR = new URL("../../contracts/fixtures/pipelines/", import.meta.url);
const palette = paletteOf(paletteFixture as S<"NodePalette">);
const workflows = workflowsFixture.workflows as unknown as Record<string, Doc>;
const load = (name: string) => JSON.parse(readFileSync(new URL(name, DIR), "utf8"));
const key = (p: { code: string; node?: string; port?: string; param?: string }) =>
  JSON.stringify([p.code, p.node ?? null, p.port ?? null, p.param ?? null]);
const demo = load("valid.text-to-edit.json").pipeline as PDoc;

describe("pipeline rules against contracts/fixtures/pipelines", () => {
  for (const f of readdirSync(DIR).filter((x) => /^(valid|invalid)\./.test(x)))
    it(f, () => {
      const fx = load(f);
      const got = pipelineProblems(fx.pipeline, workflows, palette);
      expect(new Set(got.map(key))).toEqual(new Set(fx.problems.map(key)));
      for (const p of got) expect(p.message).toBeTruthy();
    });
  it("marks run-only problems", () => {
    const got = pipelineProblems(load("invalid.reach.json").pipeline, workflows, palette);
    expect(got.every((p) => p.blocks === "run")).toBe(true);
  });
});

describe("bind candidates by type", () => {
  const qwen = settable(workflows["qwen-image"], palette);
  it("offers text inputs for a text param, picks for refs, nothing upstream of itself", () => {
    expect(bindCandidates(demo, "edit", qwen["prompt.text"])).toEqual(["$prompt", "$edit"]);
    // refs (rows of Image, max 10): the still step's 4 takes and the gate's 1 pick both fit
    expect(bindCandidates(demo, "edit", qwen["encode.refs"])).toEqual([
      "still.takes",
      "review.picked",
    ]);
    // still is before the gate: no takes of its own, nor of the gate after it
    expect(bindCandidates(demo, "still", qwen["encode.refs"])).toEqual([]);
    expect(bindCandidates(demo, "still", qwen["canvas.width"])).toEqual([]);
  });
  it("an image param takes one take only", () => {
    const up = settable(workflows["7a2d3b5f-1c8e-4d9a-8b62-4e3f2a1b0c99"], palette);
    const d: PDoc = {
      ...demo,
      nodes: { ...demo.nodes, up: { kind: "step", workflow: "x" } },
      edges: [...demo.edges.filter((e) => e[0] !== "edit"), ["edit", "up"], ["up", "out"]],
    };
    expect(bindCandidates(d, "up", up["image.image"])).toEqual(["review.picked", "edit.takes"]);
  });
  it("gates and outputs choose among what leads to them", () => {
    expect(gateCandidates(demo, "review")).toEqual(["still"]);
    expect(outputCandidates(demo, "out")).toEqual(["still.takes", "review.picked", "edit.takes"]);
  });
});

describe("pipeline edits", () => {
  it("builds the demo by adding and connecting", () => {
    let d = blankPipeline("Fox at night");
    d.nodes.inputs = {
      kind: "inputs",
      inputs: [
        { name: "prompt", type: "Text" },
        { name: "edit", type: "Text" },
      ],
    };
    let r = addPipelineNode(d, "step", { x: 200, y: 40 }, { workflow: "qwen-image" });
    d = connectNodes(r.doc, "inputs", "next", r.id);
    d = chooseWorkflow(d, r.id, "qwen-image", workflows["qwen-image"], palette);
    expect((d.nodes.step as S<"PipelineStepNode">).bind).toEqual({ "prompt.text": "$prompt" });
    r = addPipelineNode(d, "gate", { x: 400, y: 40 }, { selected: "step" });
    expect(r.doc.nodes.gate).toMatchObject({ takes: "step", keep: 1 });
    d = connectNodes(r.doc, "step", "next", "gate");
    r = addPipelineNode(d, "step", { x: 600, y: 40 });
    d = connectNodes(r.doc, "gate", "next", "step_2");
    d = chooseWorkflow(d, "step_2", "qwen-image", workflows["qwen-image"], palette);
    // refs has two fitting sources (the step's takes and the gate's pick): left for the user
    expect((d.nodes.step_2 as S<"PipelineStepNode">).bind).toEqual({ "prompt.text": "$prompt" });
    r = addPipelineNode(d, "output", { x: 800, y: 40 }, { selected: "step_2" });
    expect(r.doc.nodes.output).toMatchObject({ from: "step_2.takes" });
    // a card is never dropped on another: the spot of step_2 moves the new one right
    expect(addPipelineNode(d, "gate", { x: 610, y: 50 }).doc.nodes.gate_2.position).toEqual({
      x: 830,
      y: 50,
    });
    d = connectNodes(r.doc, "step_2", "next", "output");
    d = connectNodes(d, "gate", "fail", "step");
    expect(d.edges.find((e) => e[0] === "gate")).toEqual([
      "gate",
      { pass: "step_2", fail: "step", max: 2, then: "output" },
    ]);
    expect(order(d)).toEqual(["inputs", "step", "gate", "step_2", "output"]);
    expect(stepNumbers(d)).toEqual({ step: 1, step_2: 2 });
    const codes = pipelineProblems(d, workflows, palette).map((p) => p.code);
    expect(codes).toEqual([]); // refs is optional, so the second step may stay unbound
    // a fail edge needs a pass edge; disconnecting fail keeps the pass
    expect(connectNodes(blankPipeline("x"), "inputs", "fail", "x")).toEqual(blankPipeline("x"));
    expect(disconnectNodes(d, "gate", "fail").edges.find((e) => e[0] === "gate")).toEqual([
      "gate",
      "step_2",
    ]);
  });
  it("removing a node drops its edges and binds, and collapses a branch", () => {
    const d = removePipelineNodes(demo, ["still"]);
    expect(d.edges).toEqual([
      ["review", "edit"],
      ["edit", "out"],
    ]);
    const e = removePipelineNodes(demo, ["review"]);
    expect((e.nodes.edit as S<"PipelineStepNode">).bind).toEqual({ "prompt.text": "$edit" });
    expect(e.edges).toEqual([
      ["inputs", "still"],
      ["edit", "out"],
    ]);
  });
});
