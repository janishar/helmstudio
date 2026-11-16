import { readdirSync, readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import paletteFixture from "../../contracts/fixtures/graphs/palette.json";
import workflowsFixture from "../../contracts/fixtures/pipelines/workflows.json";
import customFixture from "../../contracts/fixtures/registry/pipelines.custom.valid.json";
import type { S } from "@/lib/api";
import { paletteOf, type Doc } from "@/lib/graph";
import {
  addPipelineNode,
  stepTakes,
  bindCandidates,
  blankPipeline,
  chooseWorkflow,
  connectNodes,
  fanCandidates,
  disconnectNodes,
  gateCandidates,
  order,
  outputCandidates,
  memoryCandidates,
  pipelineBounds,
  pipelineProblems,
  removePipelineNodes,
  setRoutes,
  textCandidates,
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
      const info: Parameters<typeof pipelineProblems>[5] = {};
      const got = pipelineProblems(fx.pipeline, workflows, palette, fx.looks, fx.loras, info, {
        providers: fx.providers,
        remoteOn: fx.remote_deployment ?? false,
        custom: fx.custom ?? customFixture.nodes,
      });
      expect(new Set(got.map(key))).toEqual(new Set(fx.problems.map(key)));
      for (const p of got) expect(p.message).toBeTruthy();
      if (fx.bounds) expect(info.bounds).toEqual(fx.bounds);
      if (fx.models) expect(info.models).toEqual(fx.models);
      if (fx.egress) expect(info.egress).toEqual(fx.egress);
      if (fx.reserved) expect(info.reserved).toEqual(fx.reserved);
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

describe("agent nodes", () => {
  const best = load("valid.judge-best-of-4.json").pipeline as PDoc;
  const decide = load("valid.decide.json").pipeline as PDoc;
  it("offers text sources and memory reads that come before", () => {
    expect(textCandidates(best, "rewrite")).toEqual([
      "$prompt",
      "$motion",
      "mem.notes",
      "judge.reasons",
    ]);
    expect(textCandidates(best, "judge")).toEqual(["$prompt", "$motion", "mem.notes"]);
    expect(memoryCandidates(best, "judge")).toEqual(["mem"]);
    expect(outputCandidates(best, "out")).toContain("judge.picked");
    expect(pipelineBounds(best)).toMatchObject({ runs: 13, llm_calls: 6, tool_calls: 0 });
  });
  it("adds a judge on the selected step and wires its branch like a gate's", () => {
    let d = removePipelineNodes(best, ["judge", "rewrite", "review"]);
    const r = addPipelineNode(d, "judge", { x: 0, y: 400 }, { selected: "still" });
    expect(r.doc.nodes.judge).toMatchObject({
      kind: "judge",
      takes: "still",
      keep: 1,
      mode: "suggest",
    });
    d = connectNodes(connectNodes(r.doc, "still", "next", "judge"), "judge", "next", "clip");
    d = connectNodes(d, "judge", "fail", "still");
    // the clip lost its keyframe with the old judge; the new judge's pick is what fits it
    const clip = d.nodes.clip as S<"PipelineStepNode">;
    const kf = settable(workflows["ltx-image-to-video"], palette)["keyframes.images"];
    expect(bindCandidates(d, "clip", kf)).toEqual(["judge.picked"]);
    d = {
      ...d,
      nodes: {
        ...d.nodes,
        clip: { ...clip, bind: { ...clip.bind, "keyframes.images": "judge.picked" } },
      },
    };
    expect(d.edges.find((e) => e[0] === "judge")).toEqual([
      "judge",
      { pass: "clip", fail: "still", max: 2, then: "out" },
    ]);
    expect(pipelineProblems(d, workflows, palette).map((p) => p.code)).toEqual([]);
  });
  it("a decide's routes edge follows its labels", () => {
    let d = connectNodes(decide, "check", "r:retry", "clip");
    expect(d.edges.find((e) => e[0] === "check")?.[1]).toEqual({
      routes: { retry: "clip", accept: "note" },
      max: 1,
      then: "accept",
    });
    d = setRoutes(d, "check", { accept: "fine", redo: "bad" });
    expect(d.edges.find((e) => e[0] === "check")?.[1]).toEqual({
      routes: { accept: "note", redo: "clip" },
      max: 1,
      then: "accept",
    });
    expect(pipelineProblems(d, workflows, palette).map((p) => p.code)).toEqual([]);
    // a plain edge never leaves a decide: connecting "next" makes its routes edge
    const r = addPipelineNode(decide, "decide", { x: 0, y: 0 });
    const e = connectNodes(r.doc, r.id, "next", "clip").edges.at(-1);
    expect(e).toEqual([
      r.id,
      { routes: { retry: "clip", accept: "clip" }, max: 2, then: "accept" },
    ]);
  });
  it("removing a source drops it from a write and a judge's memory", () => {
    const d = removePipelineNodes(best, ["mem"]);
    expect(d.nodes.judge).not.toHaveProperty("memory");
    const e = removePipelineNodes(best, ["judge"]);
    expect((e.nodes.rewrite as S<"PipelineWriteNode">).from).toEqual(["$prompt"]);
    // a decide losing a route's node loses its edge (decide_routes then says so)
    const f = removePipelineNodes(decide, ["note"]);
    expect(f.edges.find((x) => x[0] === "check")).toBeUndefined();
  });
});

describe("stage B edits", () => {
  const shots = load("valid.director-shots.json").pipeline as PDoc;
  const custom = customFixture.nodes as unknown as S<"CustomNode">[];
  it("a fanned step binds its producer's item; removing the producer drops each and the bind", () => {
    const qwen = settable(workflows["qwen-image"], palette);
    const step = Object.keys(shots.nodes).find((id) => shots.nodes[id].kind === "step")!;
    expect(bindCandidates(shots, step, qwen["prompt.text"])).toContain("director.item");
    expect(fanCandidates(shots, step)).toEqual(["director"]);
    const d = removePipelineNodes(shots, ["director"]);
    const n = d.nodes[step] as S<"PipelineStepNode">;
    expect(n.each).toBeUndefined();
    expect(Object.values(n.bind ?? {}).some((v) => v.startsWith("director."))).toBe(false);
  });
  it("a fanned step makes seeds × its list maximum takes (a judge's Keep counts them)", () => {
    expect(stepTakes(shots)).toMatchObject({ shots: 24, director: 0 });
  });
  it("an agent with routes connects like a decide", () => {
    let d = blankPipeline("Agents");
    let r = addPipelineNode(d, "agent", { x: 200, y: 40 });
    expect(r.doc.nodes.agent).toMatchObject({ kind: "agent", from: ["$prompt"] });
    d = setRoutes(r.doc, "agent", { again: "one more try", done: "finish" });
    r = addPipelineNode(d, "output", { x: 400, y: 40 });
    d = connectNodes(r.doc, "agent", "r:done", "output");
    expect(d.edges).toEqual([
      ["agent", { routes: { again: "output", done: "output" }, max: 2, then: "done" }],
    ]);
    d = connectNodes(d, "inputs", "next", "agent");
    d = connectNodes(d, "agent", "r:again", "agent");
    const codes = pipelineProblems(d, workflows, palette).map((p) => p.code);
    expect(codes).not.toContain("decide_routes");
    expect(codes).not.toContain("uncapped_cycle");
  });
  it("a custom node binds its required text field and gives a text source", () => {
    let d = blankPipeline("Prefix");
    const r = addPipelineNode(d, "custom", { x: 200, y: 40 }, { custom: custom[0] });
    expect(r.doc.nodes.custom).toMatchObject({ node: "text/prefix", bind: { text: "$prompt" } });
    d = connectNodes(r.doc, "inputs", "next", "custom");
    const p = pipelineProblems(d, workflows, palette, undefined, {}, undefined, { custom });
    expect(p.filter((x) => x.node === "custom").map((x) => [x.code, x.port])).toEqual([
      ["custom_input", "prefix"],
    ]);
    d = { ...d, nodes: { ...d.nodes, step: { kind: "step", workflow: "qwen-image" } } };
    d = connectNodes(d, "custom", "next", "step");
    const qwen = settable(workflows["qwen-image"], palette);
    expect(bindCandidates(d, "step", qwen["prompt.text"], custom)).toContain("custom.text");
    expect(textCandidates(d, "step", custom)).toContain("custom.text");
  });
});
