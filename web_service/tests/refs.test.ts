import { describe, expect, it } from "vitest";
import qwenNodes from "../../contracts/fixtures/registry/qwen-image.nodes.valid.json";
import qwenWorkflows from "../../contracts/fixtures/registry/qwen-image.workflows.valid.json";
import minimaxNodes from "../../contracts/fixtures/registry/minimax-h3-image.nodes.valid.json";
import minimaxWorkflows from "../../contracts/fixtures/registry/minimax-h3-image.workflows.valid.json";
import h3Nodes from "../../contracts/fixtures/registry/minimax-h3.nodes.valid.json";
import h3Workflows from "../../contracts/fixtures/registry/minimax-h3.workflows.valid.json";
import nodesFixture from "../../contracts/fixtures/registry/nodes.valid.json";
import workflowsFixture from "../../contracts/fixtures/registry/workflows.valid.json";
import type { S } from "@/lib/api";
import { cellValue, inputSummary, lineFields } from "@/lib/batches";
import {
  FIRST_REF_CANVAS,
  addIds,
  autoCanvas,
  autoSize,
  cellRef,
  editLabel,
  imageInputs,
  inOrder,
  mentionOf,
  move,
  refCount,
  refIds,
  refUnit,
  withIds,
} from "@/lib/refs";
import { studioView } from "@/lib/studio";
import { fpsOf } from "@/lib/video";
import { buildInputs, fields, rawsFrom, readValue, toRaw, type Field } from "@/lib/workflow";

const qwen = {
  ...qwenWorkflows.workflows[0],
  node_schemas: Object.fromEntries(qwenNodes.nodes.map((n) => [n.kind, n])),
} as unknown as S<"Workflow">;
const minimax = {
  ...minimaxWorkflows.workflows[0],
  node_schemas: Object.fromEntries(minimaxNodes.nodes.map((n) => [n.kind, n])),
} as unknown as S<"Workflow">;
const echo = {
  ...workflowsFixture.workflows[0],
  node_schemas: Object.fromEntries(nodesFixture.nodes.map((n) => [n.kind, n])),
} as unknown as S<"Workflow">;

const A = "11111111-1111-4111-8111-111111111111";
const B = "22222222-2222-4222-8222-222222222222";
const C = "33333333-3333-4333-8333-333333333333";
const refs = fields(qwen).find((f) => f.key === "encode.refs") as Field;
const image: Field = {
  ...refs,
  key: "src.image",
  param: { name: "image", type: "image", default: null },
  initial: null,
};

describe("image inputs", () => {
  it("finds the references param of qwen-image and echo", () => {
    expect(imageInputs(fields(qwen)).map((f) => f.key)).toEqual(["encode.refs"]);
    expect(imageInputs(fields(echo)).map((f) => f.key)).toEqual(["echo.refs"]);
  });

  it("names item n with the schema's mention", () => {
    expect(mentionOf(refs.param, 2)).toBe("<image2>");
    expect(mentionOf(image.param, 1)).toBe("image 1");
  });

  it("holds take ids as rows items, keeps an item's other fields, and reads them back in order", () => {
    const raw = withIds(refs, undefined, [A, B]);
    expect(raw).toEqual([{ image: { take_id: A } }, { image: { take_id: B } }]);
    expect(refIds(refs, raw)).toEqual([A, B]);
    const kept = withIds(refs, [{ image: { take_id: A }, weight: 0.5 }], [B, A]);
    expect(kept).toEqual([{ image: { take_id: B } }, { image: { take_id: A }, weight: 0.5 }]);
    expect(refIds(image, withIds(image, undefined, [C]))).toEqual([C]);
    expect(refIds(image, undefined)).toEqual([]);
  });

  it("adds each take once up to the max, and moves an item", () => {
    expect(addIds([A], [A, B, C], 2)).toEqual([A, B]);
    expect(move([A, B, C], 0, 2)).toEqual([B, C, A]);
    expect(move([A, B, C], 2, -5)).toEqual([C, A, B]);
    expect(move([A, B], 1, 1)).toEqual([A, B]);
  });

  it("sends references as {take_id} lists, checks the count, and leaves an empty list out", () => {
    const fs = fields(qwen);
    expect(buildInputs(fs, { "encode.refs": [] }).inputs).toEqual({});
    const raws = { "encode.refs": withIds(refs, undefined, [A, B]) };
    expect(buildInputs(fs, raws).inputs).toEqual({
      "encode.refs": [{ image: { take_id: A } }, { image: { take_id: B } }],
    });
    const eleven = Array.from({ length: 11 }, () => ({ image: { take_id: A } }));
    expect(readValue(refs.param, eleven)).toEqual({ error: "at most 10 items" });
    expect(readValue(refs.param, [{ image: "x" }])).toEqual({
      error: "item 1: an image is required",
    });
    expect(readValue(image.param, "")).toEqual({ error: "an image is required" });
    expect(readValue(image.param, C)).toEqual({ value: { take_id: C } });
    expect(refCount(fs, raws)).toBe(2);
  });

  it("fills the form from a reused take's references, and an image param from {take_id}", () => {
    const inputs = { "encode.refs": [{ image: { take_id: A } }] };
    expect(rawsFrom(fields(qwen), inputs)).toEqual(inputs);
    expect(toRaw({ take_id: C }, image.param)).toBe(C);
    expect(toRaw(null, image.param)).toBe("");
  });
});

describe("canvas auto in an edit", () => {
  it("is about 1 MP at the last reference's aspect, sides on the 32 px grid (calculate_dimensions)", () => {
    expect(autoSize(1024, 1024)).toEqual({ width: 1024, height: 1024 });
    expect(autoSize(1920, 1080)).toEqual({ width: 1376, height: 768 });
    expect(autoSize(768, 1344)).toEqual({ width: 768, height: 1344 });
    const s = autoSize(3000, 2000);
    expect([s.width % 32, s.height % 32]).toEqual([0, 0]);
  });

  it("follows the first reference, clamped to 1:4…4:1, at the canvas's area on a FIRST_REF_CANVAS", () => {
    expect(autoCanvas({ width: 1536, height: 2048 }, 2048, 2048, true)).toEqual({
      width: 1792,
      height: 2368,
    });
    expect(autoCanvas({ width: 2048, height: 1536 }, 2048, 2048, true)).toEqual({
      width: 2368,
      height: 1792,
    });
    expect(autoCanvas({ width: 8000, height: 1000 }, 1024, 1024, true)).toEqual({
      width: 2048,
      height: 512,
    });
    expect(autoCanvas({ width: 1920, height: 1080 }, 2048, 2048, false)).toEqual(
      autoSize(1920, 1080),
    );
    const canvas = fields(minimax).find((f) => f.canvas && f.param.name === "mode")!;
    expect(FIRST_REF_CANVAS.has(canvas.kind!)).toBe(true);
    expect(fields(qwen).some((f) => FIRST_REF_CANVAS.has(f.kind ?? ""))).toBe(false);
  });

  it("lays out the minimax-h3-image template from its node schemas and studio block alone", () => {
    const fs = fields(minimax);
    const v = studioView(minimax, fs)!;
    expect(v.text.map((f) => f.key)).toEqual(["prompt.text"]);
    expect(v.groups.map((g) => [g.title, g.fields.map((f) => f.key)])).toEqual([
      ["Quick", ["encode.refs", "enhance.bypass", "canvas.mode", "model.variant", "seed.seed"]],
      ["Canvas", ["canvas.width", "canvas.height"]],
      ["Enhance", ["enhance.think"]],
      ["Quality", ["model.precision", "sample.sampler"]],
    ]);
    expect(v.groups[0].fields[1].param.label).toBe("No enhance");
    expect(v.groups[0].fields[1].initial).toBe(true);
    const refs = imageInputs(fs)[0];
    expect([refs.param.max, mentionOf(refs.param, 2)]).toEqual([16, "<Picture 2>"]);
    const w = fs.find((f) => f.key === "canvas.width")!.param;
    expect([w.min, w.max, w.step]).toEqual([256, 8192, 32]);
    expect(Object.keys(minimax.studio!.presets!)).toEqual(["Plain", "Enhanced", "Enhanced, think"]);
  });

  it("lays out the qwen-image template as its studio block places it, every param reachable", () => {
    const fs = fields(qwen);
    const v = studioView(qwen, fs)!;
    expect(v.text.map((f) => f.key)).toEqual(["prompt.text"]);
    expect(v.groups.map((g) => [g.title, g.fields.map((f) => f.key)])).toEqual([
      ["Quick", ["encode.refs", "model.variant", "enhance.bypass", "seed.seed"]],
      ["Canvas", ["canvas.mode", "canvas.width", "canvas.height"]],
      ["Quality", ["model.precision", "sample.steps", "decode.tiled"]],
      ["LoRA", ["lora.rows"]],
      ["Enhance", ["enhance.think"]],
      ["Options", ["encode.transparent"]],
    ]);
    // + the No enhance switch (a node's bypass, not a param)
    expect(v.shown.map((f) => f.key).sort()).toEqual(
      [...fs.map((f) => f.key), "enhance.bypass"].sort(),
    );
  });

  it("lays out the three minimax-h3 video templates (API 1.21; 1.37 presets, Advanced) from their node schemas and studio blocks", () => {
    const schemas = Object.fromEntries(h3Nodes.nodes.map((n) => [n.kind, n]));
    const [t2v, i2v, refs] = h3Workflows.workflows.map(
      (w) => ({ ...w, node_schemas: schemas }) as unknown as S<"Workflow">,
    );
    const layout = (w: S<"Workflow">) => {
      const v = studioView(w, fields(w))!;
      return [v.text.map((f) => f.key), v.groups.map((g) => [g.title, g.fields.map((f) => f.key)])];
    };
    const rest = [
      ["Canvas", ["canvas.width", "canvas.height"]],
      ["Duration", ["duration.frames"]],
      [
        "Quality",
        [
          "model.precision",
          "generate.steps",
          "generate.quality",
          "generate.flow_shift",
          "generate.audio_flow_shift",
        ],
      ],
      [
        "Advanced",
        [
          "generate.layers",
          "generate.reuse",
          "generate.core_reuse",
          "generate.render",
          "generate.token_reduction",
        ],
      ],
    ];
    expect(layout(t2v)).toEqual([
      ["prompt.text"],
      [["Quick", ["model.variant", "seed.seed"]], ...rest],
    ]);
    expect(layout(i2v)).toEqual([
      ["prompt.text"],
      [["Quick", ["first.image", "last.image", "model.variant", "seed.seed"]], ...rest],
    ]);
    expect(layout(refs)).toEqual([
      ["prompt.text"],
      [
        [
          "Quick",
          ["generate.refs", "generate.videos", "generate.audios", "model.variant", "seed.seed"],
        ],
        ...rest,
      ],
    ]);
    // the top bar counts H3 refs as references, a first frame as a frame
    expect([refUnit(imageInputs(fields(refs))[0]), refUnit(imageInputs(fields(i2v))[0])]).toEqual([
      "reference",
      "frame",
    ]);
    // the fps field is not shown: the Duration label's seconds use the node's fixed 24, not 0
    const shown = studioView(t2v, fields(t2v))!.groups.flatMap((g) => g.fields);
    expect(
      fpsOf(
        shown.find((f) => f.key === "duration.frames")!,
        shown,
        {},
      ),
    ).toBe(24);
    const [first, rows] = [imageInputs(fields(i2v)), imageInputs(fields(refs))];
    expect(first.map((f) => f.key)).toEqual(["first.image", "last.image", "generate.refs"]);
    expect(i2v.studio!.labels!["first.image"]).toBe("First frame");
    // the last frame (API 1.22) is optional: left empty it is not sent (null, as the template holds it)
    const fi = fields(i2v);
    expect(fi.find((f) => f.key === "last.image")!.optional).toBe(true);
    expect(fi.find((f) => f.key === "first.image")!.optional).toBeUndefined();
    expect(buildInputs(fi, { "first.image": A, "prompt.text": "go" })).toEqual({
      inputs: { "first.image": { take_id: A }, "prompt.text": "go" },
      errors: {},
    });
    expect(buildInputs(fi, { "first.image": A, "last.image": A }).inputs["last.image"]).toEqual({
      take_id: A,
    });
    expect([rows[0].param.max, mentionOf(rows[0].param, 3)]).toEqual([9, "<Picture 3>"]);
    const inputs = buildInputs(fields(refs), {
      ...rawsFrom(fields(refs), {}),
      "prompt.text": "<Picture 1> runs",
      "generate.refs": [{ image: { take_id: A } }],
    });
    expect(inputs).toEqual({
      inputs: { "prompt.text": "<Picture 1> runs", "generate.refs": [{ image: { take_id: A } }] },
      errors: {},
    });
  });

  it("labels the workflow as an edit with its reference count", () => {
    expect(editLabel("Qwen Image", 2)).toBe("Qwen Image · Edit · 2 references");
    expect(editLabel("Qwen Image", 1)).toBe("Qwen Image · Edit · 1 reference");
    expect(editLabel("Qwen Image", 0)).toBe("Qwen Image");
  });
});

describe("image values in batches", () => {
  it("offers the references param for lines and types a take id cell as its value", () => {
    expect(lineFields(fields(qwen)).map((f) => f.key)).toContain("encode.refs");
    expect(cellValue(refs, ` ${A} `)).toEqual({ value: [{ image: { take_id: A } }] });
    expect(cellValue(image, A)).toEqual({ value: { take_id: A } });
    expect(cellRef(refs, "not-an-id")).toEqual({ error: "must be a take id" });
  });

  it("summarises image inputs by count, not as [object Object]", () => {
    const row = (inputs: S<"RunInputs">, n: number) =>
      ({ row: n, inputs }) as unknown as S<"BatchRow">;
    const rows = [
      row({ "encode.refs": [{ image: { take_id: A } }] }, 1),
      row({ "encode.refs": [{ image: { take_id: A } }, { image: { take_id: B } }] }, 2),
    ];
    expect(inputSummary(rows)(rows[1])).toBe("refs ×2");
  });
});

describe("inOrder", () => {
  it("adds dropped files in drop order when the second upload finishes first", async () => {
    let finishScene: (id: string) => void = () => {};
    const scene = new Promise<string>((r) => (finishScene = r));
    const hat = Promise.resolve("hat");
    const failing = Promise.reject(new Error("too big"));
    const seen: string[] = [];
    const all = inOrder(
      [scene, hat, failing],
      (t) => seen.push(t),
      (e, i) => seen.push(`${i}: ${(e as Error).message}`),
    );
    await hat;
    await new Promise((r) => setTimeout(r, 0));
    expect(seen).toEqual([]); // the hat waits for the scene
    finishScene("scene");
    await all;
    expect(seen).toEqual(["scene", "hat", "2: too big"]);
  });
});

describe("the run strip on a bypassed node", () => {
  it("strikes the enhancer through when it is bypassed, and runs it when it is not", async () => {
    const { segments } = await import("@/lib/runs");
    const run = (inputs: Record<string, unknown>) =>
      ({ id: "r", status: "succeeded", inputs }) as unknown as S<"Run">;
    const state = (r: S<"Run">) => segments(minimax, r).find((x) => x.node === "enhance")?.state;
    expect(state(run({}))).toBe("bypassed");
    expect(state(run({ "enhance.bypass": false }))).toBe("done");
  });
});
