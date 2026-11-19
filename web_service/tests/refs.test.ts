import { describe, expect, it } from "vitest";
import qwenNodes from "../../contracts/fixtures/registry/qwen-image.nodes.valid.json";
import qwenWorkflows from "../../contracts/fixtures/registry/qwen-image.workflows.valid.json";
import minimaxNodes from "../../contracts/fixtures/registry/minimax-h3-image.nodes.valid.json";
import minimaxWorkflows from "../../contracts/fixtures/registry/minimax-h3-image.workflows.valid.json";
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
  withIds,
} from "@/lib/refs";
import { studioView } from "@/lib/studio";
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
      ["Quick", ["encode.refs", "enhance.bypass", "canvas.mode", "seed.seed"]],
      ["Canvas", ["canvas.width", "canvas.height"]],
      ["Enhance", ["enhance.think"]],
      ["Quality", ["sample.sampler"]],
    ]);
    expect(v.groups[0].fields[1].param.label).toBe("No enhance");
    expect(v.groups[0].fields[1].initial).toBe(true);
    const refs = imageInputs(fs)[0];
    expect([refs.param.max, mentionOf(refs.param, 2)]).toEqual([16, "<Picture 2>"]);
    const w = fs.find((f) => f.key === "canvas.width")!.param;
    expect([w.min, w.max, w.step]).toEqual([256, 8192, 32]);
    expect(Object.keys(minimax.studio!.presets!)).toEqual(["Plain", "Enhanced", "Enhanced, think"]);
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
