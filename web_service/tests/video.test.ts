import { describe, expect, it } from "vitest";
import ltxNodes from "../../contracts/fixtures/registry/ltx.nodes.valid.json";
import ltxWorkflows from "../../contracts/fixtures/registry/ltx.workflows.valid.json";
import autoFx from "../../contracts/fixtures/video/auto-frames.json";
import keyFx from "../../contracts/fixtures/video/keyframes.json";
import type { S } from "@/lib/api";
import { modeOf } from "@/lib/loras";
import {
  allowedInts,
  autoBounds,
  autoFrames,
  choosable,
  clipOf,
  firstFrameRow,
  freeFrame,
  placeRows,
  positionErrors,
  setFirstFrame,
} from "@/lib/video";
import { buildInputs, fields, readValue } from "@/lib/workflow";

const schemas = Object.fromEntries(ltxNodes.nodes.map((n) => [n.kind, n]));
const wf = (w: unknown) =>
  ({ source: "template", ...(w as object), node_schemas: schemas }) as unknown as S<"Workflow">;
const [t2v, i2v] = ltxWorkflows.workflows.map(wf);
const param = (kind: string, name: string) =>
  schemas[kind].params.find((p) => p.name === name) as unknown as S<"ParamSchema">;
const frames = param("ltx/duration", "frames");
const images = param("ltx/keyframes", "images");
const ref = (n: number) => ({ take_id: `00000000-0000-4000-8000-00000000000${n}` });

describe("auto length", () => {
  it("resolves seconds to frames as the runner does (contracts/fixtures/video/auto-frames.json)", () => {
    for (const c of autoFx.cases) {
      const p = {
        ...frames,
        ...autoFx.param,
        max: c.max,
        auto: { min_s: c.min_s, max_s: c.max_s },
      };
      expect(autoFrames(p, c.seconds, c.fps), JSON.stringify(c)).toBe(c.frames);
    }
  });

  it("bounds the slider's auto at the param's max: 1–20 s at 24 fps is 25 to 321 frames", () => {
    expect(autoBounds(frames, 24)).toEqual({ lo: 25, hi: 321 });
  });

  it("reads the Auto switch as the value auto, only where the param declares it", () => {
    expect(readValue(frames, "auto")).toEqual({ value: "auto" });
    const plain = { ...frames, auto: undefined };
    expect(readValue(plain as S<"ParamSchema">, "auto")).toEqual({
      error: "auto length is not offered",
    });
    expect(buildInputs(fields(t2v), { "duration.frames": "auto" }).inputs).toEqual({
      "duration.frames": "auto",
    });
    const clip = clipOf(fields(i2v), { "duration.frames": "auto" });
    expect(clip).toEqual({ frames: "auto", fps: 24, last: 320, end: 24 });
  });
});

describe("stage steps", () => {
  const fs = fields(t2v);
  const s1 = fs.find((f) => f.key === "sample1.steps")!;
  const s2 = fs.find((f) => f.key === "sample2.steps")!;
  it("narrows values by the node's role: stage 1 takes 8, stage 2 takes 3", () => {
    expect(allowedInts(s1, {})).toEqual([8]);
    expect(allowedInts(s2, {})).toEqual([3]);
    expect(allowedInts(s2, { "sample2.role": "stage1" })).toEqual([8]);
    expect(
      allowedInts(
        fs.find((f) => f.key === "duration.fps")!,
        {},
      ),
    ).toBeNull();
  });
  it("refuses a value outside values or the role's subset, worded as studio's 422", () => {
    expect(readValue(s1.param, "5")).toEqual({ error: "must be one of 3, 8" });
    expect(buildInputs(fs, { "sample2.steps": "8" }).errors).toEqual({
      "sample2.steps": "must be one of 3",
    });
    expect(buildInputs(fs, {}).errors).toEqual({});
  });
});

describe("keyframe positions", () => {
  it("matches studio's rule on contracts/fixtures/video/keyframes.json", () => {
    for (const c of keyFx.cases) {
      const w = wf(c.workflow);
      // a bypassed Keyframes node is spliced out: the form's rows are not the clip's
      if (w.nodes.keyframes.bypass) continue;
      const fs = fields(w);
      const f = fs.find((x) => x.key === "keyframes.images")!;
      const rows = f.initial as S<"RowItem">[];
      const got = positionErrors(f.param, rows, clipOf(fs, {})).flatMap((e, i) =>
        e ? [`keyframes.images[${i + 1}].seconds: ${e}`] : [],
      );
      expect(got, c.description).toEqual(c.errors);
    }
  });

  it("puts a new keyframe on the first frame, then the last, then the widest gap", () => {
    expect(freeFrame([], 48)).toBe(0);
    expect(freeFrame([0], 48)).toBe(48);
    expect(freeFrame([0, 48, 12], 48)).toBe(30);
    const clip = { frames: 49, fps: 24, last: 48 };
    const old = [{ image: ref(1) }];
    const rows = placeRows(images, [...old, { image: ref(2) }, { image: ref(3) }], old, clip);
    expect(rows.map((r) => r.seconds)).toEqual([undefined, 2, 1]);
    expect(placeRows(images, old, [], null)).toBe(old);
  });

  it("with auto length puts a second keyframe on the shortest clip's last frame, never the longest's", () => {
    const clip = clipOf(fields(i2v), { "duration.frames": "auto" })!;
    const old = [{ image: ref(1) }];
    const rows = placeRows(images, [...old, { image: ref(2) }], old, clip);
    expect(rows[1].seconds).toBe(1); // frame 24: the shortest auto clip (25 frames) has it
  });

  it("refuses a keyframe past the clip before sending", () => {
    const rows: S<"RowItem">[] = [{ image: ref(1) }, { image: ref(2), seconds: 2.1 }];
    expect(buildInputs(fields(i2v), { "keyframes.images": rows }).errors).toEqual({
      "keyframes.images": "row 2: frame 50 is past the clip's last frame 48",
    });
  });

  it("sends keyframe rows that fit the clip", () => {
    const rows: S<"RowItem">[] = [{ image: ref(1) }, { image: ref(2), seconds: 2 }];
    expect(buildInputs(fields(i2v), { "keyframes.images": rows })).toEqual({
      inputs: { "keyframes.images": rows },
      errors: {},
    });
  });

  it("needs one image at run, and checks the merged item", () => {
    const fs = fields(i2v);
    expect(buildInputs(fs, {}).errors).toEqual({ "keyframes.images": "at least 1 items" });
    expect(
      buildInputs(fs, { "keyframes.images": [{ image: ref(1), strength: 2 }] }).errors,
    ).toEqual({ "keyframes.images": "item 1, Strength: must be between 0 and 1" });
  });
});

describe("prompt beats", () => {
  it("requires a beat's text", () => {
    expect(buildInputs(fields(t2v), { "encode.beats": [{ seconds: 1 }] }).errors).toEqual({
      "encode.beats": "item 1, Beat: must be between 1 and 500 characters",
    });
  });
});

describe("the First frame tile", () => {
  const clip = { frames: 49, fps: 24, last: 48 };
  it("replaces the image on frame 0, else adds a row there", () => {
    const rows = [{ image: ref(1), seconds: 2 }];
    expect(setFirstFrame(images, rows, ref(2).take_id, clip)).toEqual([
      { image: ref(2), seconds: 0 },
      ...rows,
    ]);
    const two = [{ image: ref(1), seconds: 0.01 }, ...rows];
    expect(setFirstFrame(images, two, ref(3).take_id, clip)[0].image).toEqual(ref(3));
    expect(firstFrameRow(images, two, clip)).toBe(two[0]);
    expect(firstFrameRow(images, rows, clip)).toBeUndefined();
  });
});

describe("the LoRA stack's mode", () => {
  it("is drawn for ltx (adapter and bake) and not for a one-value enum", () => {
    expect(modeOf(param("ltx/lora-stack", "rows"))?.[1]).toMatchObject({ default: "adapter", enum: ["adapter", "bake"] });
    expect(choosable(modeOf(param("ltx/lora-stack", "rows"))?.[1])).toBe(true);
    expect(choosable({ enum: ["bake"] })).toBe(false);
  });
});
