import { describe, expect, it } from "vitest";
import ltxNodes from "../../contracts/fixtures/registry/ltx.nodes.valid.json";
import ltxWorkflows from "../../contracts/fixtures/registry/ltx.workflows.valid.json";
import qwenNodes from "../../contracts/fixtures/registry/qwen-image.nodes.valid.json";
import qwenWorkflows from "../../contracts/fixtures/registry/qwen-image.workflows.valid.json";
import type { S } from "@/lib/api";
import {
  durationText,
  frameError,
  framesFor,
  mediaKinds,
  mediaOf,
  secondsText,
  snapFrames,
  thumbOf,
  videoLine,
  workflowFor,
} from "@/lib/media";
import { nodeLabel, segments } from "@/lib/runs";
import { deriveStudio } from "@/lib/studio";
import {
  aspectOf,
  buildInputs,
  carry,
  fields,
  megapixelChoices,
  readValue,
  sections,
  sizeFor,
} from "@/lib/workflow";

const wf = (w: unknown, nodes: { kind: string }[], source = "template") =>
  ({
    ...(w as object),
    source,
    node_schemas: Object.fromEntries(nodes.map((n) => [n.kind, n])),
  }) as unknown as S<"Workflow">;
const ltx = wf(ltxWorkflows.workflows[0], ltxNodes.nodes);
const qwen = wf(qwenWorkflows.workflows[0], qwenNodes.nodes);
const frames = ltx.node_schemas["ltx/duration"].params[0];

describe("media of workflows and the Image / Video switch", () => {
  it("reads media, absent as image, and offers only the media present", () => {
    expect(mediaOf(ltx)).toBe("video");
    expect(mediaOf(qwen)).toBe("image");
    expect(mediaKinds([qwen])).toEqual(["image"]);
    expect(mediaKinds([ltx, qwen])).toEqual(["image", "video"]);
    expect(mediaKinds([{ media: "audio" }, ltx, qwen])).toEqual(["image", "video", "audio"]);
  });

  it("opens the open workflow, else the session's newest of that media, else the first template", () => {
    const mine = { ...ltx, id: "my-ltx", source: "mine" } as S<"Workflow">;
    const ws = [qwen, mine, ltx];
    expect(workflowFor(ws, [], "video", "ltx-text-to-video")).toBe("ltx-text-to-video");
    expect(workflowFor(ws, [{ workflow_id: qwen.id }], "video", qwen.id)).toBe(ltx.id);
    expect(
      workflowFor(ws, [{ workflow_id: qwen.id }, { workflow_id: "my-ltx" }], "video", qwen.id),
    ).toBe("my-ltx");
    expect(workflowFor(ws, [], "image", ltx.id)).toBe(qwen.id);
    expect(workflowFor([qwen], [], "video")).toBeUndefined();
    const tts = (id: string, voice: string[]) =>
      ({
        id,
        media: "audio",
        source: "template",
        nodes: { speak: { kind: "q/speak", only: { voice } } },
      }) as never;
    expect(workflowFor([tts("design", ["design"]), tts("over", ["preset"])], [], "audio")).toBe(
      "over",
    );
  });

  it("carries the prompt and seed across, and a canvas only when it suits the new one", () => {
    const raws = {
      "prompt.text": "a fox",
      "seed.seed": "7",
      "canvas.mode": "fixed",
      "canvas.width": "2752",
      "canvas.height": "1536",
    };
    const into = carry(raws, fields(ltx));
    expect(into).toEqual({ "prompt.text": "a fox", "seed.seed": "7", "canvas.mode": "fixed" });
    const fit = carry({ ...raws, "canvas.width": "1024", "canvas.height": "576" }, fields(ltx));
    expect(fit["canvas.width"]).toBe("1024");
    expect(carry({ "duration.frames": "50" }, fields(ltx))).toEqual({});
    // "Use in…" from a Qwen Turbo still: Turbo is no LTX variant, the precision carries
    expect(carry({ "model.variant": "turbo", "model.precision": "int8" }, fields(ltx))).toEqual({
      "model.precision": "int8",
    });
  });

  it("keeps a LoRA stack in its family: it does not cross to another and comes back on the switch back", () => {
    const id = "9b2f4c6e-1a3d-4e5f-8a7b-0c1d2e3f4a51";
    for (const mode of ["adapter", "bake"]) {
      const raws = {
        "prompt.text": "a fox",
        "lora.rows": [{ lora: { lora_id: id }, strength: 1, mode }],
      };
      const video = carry(raws, fields(ltx), fields(qwen));
      expect(video).toEqual({ "prompt.text": "a fox" });
      expect(carry(video, fields(qwen), fields(ltx), raws)).toEqual(raws);
    }
    const ltxRows = { "lora.rows": [{ lora: { lora_id: id }, strength: 1, mode: "bake" }] };
    expect(carry(ltxRows, fields(ltx), fields(ltx))).toEqual(ltxRows);
  });
});

describe("the frame grid and the Duration slider", () => {
  it("checks, snaps and converts frames and seconds", () => {
    expect(frameError(frames, 49)).toBeNull();
    expect(frameError(frames, 50)).toBe("50 is not on the frame grid 9 + 8k up to 321");
    expect(frameError(frames, 329)).not.toBeNull();
    expect(snapFrames(frames, 52)).toBe(49);
    expect(snapFrames(frames, 54)).toBe(57);
    expect(snapFrames(frames, 1)).toBe(9);
    expect(snapFrames(frames, 999)).toBe(321);
    expect(framesFor(frames, 2, 24)).toBe(49);
    expect(framesFor(frames, 5, 25)).toBe(129);
    expect(durationText(49, 24)).toBe("49 frames · 2.0 s");
    expect(secondsText(57 / 25)).toBe("2.3 s");
  });

  it("reads a frames control as a number on the grid", () => {
    expect(readValue(frames, "57")).toEqual({ value: 57 });
    expect(readValue(frames, "50")).toEqual({
      error: "must be on the frame grid 9 + 8k up to 321",
    });
    expect(buildInputs(fields(ltx), { "duration.frames": "57" }).inputs).toEqual({
      "duration.frames": 57,
    });
  });

  it("puts frames and their frame rate in Duration, the canvas in Canvas", () => {
    const s = sections(fields(ltx));
    expect(s.duration.map((f) => f.key)).toEqual(["duration.frames", "duration.fps"]);
    expect(s.canvas.map((f) => f.key)).toEqual(["canvas.width", "canvas.height"]);
    expect(s.tiles.some((f) => f.key === "duration.fps")).toBe(false);
    const st = deriveStudio(ltx, ltx.node_schemas);
    expect(st.inspector?.map((x) => x.title)).toEqual([
      "Prompt beats",
      "Canvas",
      "Duration",
      "Quality",
      "Options",
    ]);
  });

  it("offers sizes a 64 px canvas reaches and names its aspect within a grid step", () => {
    expect(megapixelChoices(128, 1920)).toEqual([0.25, 0.5, 1, 2]);
    expect(megapixelChoices(256, 4096)).toEqual([0.25, 0.5, 1, 2, 4]); // 8 MP only when allowed (Qwen bf16 at 8 MP > 52 GB)
    expect(megapixelChoices(256, 4096, Infinity)).toEqual([0.25, 0.5, 1, 2, 4, 8]);
    expect(megapixelChoices(256, 8192, Infinity)).toEqual([0.25, 0.5, 1, 2, 4, 8, 16]); // minimax-h3-image's bounds
    expect(aspectOf(704, 448, 64)).toBe("3:2");
    expect(aspectOf(704, 448)).toBeNull();
    const s = sizeFor(3 / 2, 0.25, 64, 128, 1920);
    expect([s.width % 64, s.height % 64, aspectOf(s.width, s.height, 64)]).toEqual([0, 0, "3:2"]);
  });
});

describe("video takes and the run strip", () => {
  const video = {
    duration_s: 49 / 24,
    fps: 24,
    frames: 49,
    has_audio: true,
    poster_url: "/files/p.png",
  };
  it("shows a video by its poster and length, an image by its thumbnail", () => {
    const out = {
      index: 0,
      key: "k.mp4",
      kind: "video",
      url: "/files/k.mp4",
      width: 704,
      height: 448,
      video,
    } as S<"RunOutput">;
    expect(thumbOf(out)).toEqual({ src: "/files/p.png", length: "2.0 s" });
    const img = {
      index: 0,
      key: "k.png",
      kind: "image",
      url: "/files/k.png",
      width: 1,
      height: 1,
    } as S<"RunOutput">;
    expect(thumbOf(img)).toEqual({ src: "/files/k.png", length: undefined });
    expect(videoLine(video)).toBe("2.0 s · 24 fps · 49 frames · audio");
  });

  it("numbers twin nodes and lists the stages in graph order with their steps", () => {
    expect(nodeLabel(ltx, "sample1")).toBe("Sample 1");
    expect(nodeLabel(ltx, "sample2")).toBe("Sample 2");
    expect(nodeLabel(ltx, "encode")).toBe("Encode");
    const run = {
      id: "r",
      workflow_id: ltx.id,
      inputs: {},
      status: "running",
      progress: { node: "sample1", step: 6, of: 8 },
      outputs: [],
    } as unknown as S<"Run">;
    expect(segments(ltx, run).map((x) => [x.title, x.state, x.steps])).toEqual([
      ["Encode", "done", undefined],
      ["Sample 1", "active", undefined],
      ["Upscale latent", "pending", undefined],
      ["Sample 2", "pending", 3],
      ["Decode", "pending", undefined],
      ["Saving", "pending", undefined],
    ]);
  });
});
