import { describe, expect, it } from "vitest";
import ltxNodes from "../../contracts/fixtures/registry/ltx.nodes.valid.json";
import ltxWorkflows from "../../contracts/fixtures/registry/ltx.workflows.valid.json";
import qwenNodes from "../../contracts/fixtures/registry/qwen-image.nodes.valid.json";
import qwenWorkflows from "../../contracts/fixtures/registry/qwen-image.workflows.valid.json";
import echoNodes from "../../contracts/fixtures/registry/nodes.valid.json";
import echoWorkflows from "../../contracts/fixtures/registry/workflows.valid.json";
import type { S } from "@/lib/api";
import {
  durationText,
  frameError,
  framesFor,
  createKinds,
  kindOf,
  kindTemplates,
  sideOf,
  soundText,
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
  carryKind,
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
    expect(createKinds([qwen])).toEqual(["image"]);
    expect(createKinds([ltx, qwen])).toEqual(["image", "video"]);
    expect(createKinds([{ media: "audio" }, ltx, qwen])).toEqual(["image", "video", "voice"]);
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
    expect(workflowFor([tts("design", ["design"]), tts("over", ["preset"])], [], "voice")).toBe(
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

describe("prompts and scripts per kind (alpha.2 item 14)", () => {
  it("never carries a picture prompt into an audio workflow, and brings each side's last text back", () => {
    const kept = {};
    const toSound = { from: "picture", to: "sound" };
    const toPicture = { from: "sound", to: "picture" };
    // the audio workflow is drawn with the same prompt path (Qwen3-TTS Voice design's prompt.text)
    const sound = fields(qwen);
    const pic = { "prompt.text": "@elias @biscuit at the door", "seed.seed": "7" };
    const heard = carryKind(pic, sound, fields(qwen), undefined, toSound, kept);
    expect(heard["prompt.text"]).toBeUndefined();
    expect(heard["seed.seed"]).toBe("7");
    const back = carryKind(
      { ...heard, "prompt.text": "Hello there." },
      fields(qwen),
      sound,
      undefined,
      toPicture,
      kept,
    );
    expect(back["prompt.text"]).toBe("@elias @biscuit at the door");
    const again = carryKind(back, sound, fields(qwen), undefined, toSound, kept);
    expect(again["prompt.text"]).toBe("Hello there.");
    // the same side: as carry
    expect(
      carryKind(
        pic,
        fields(ltx),
        fields(qwen),
        undefined,
        { from: "picture", to: "picture" },
        kept,
      ),
    ).toEqual(carry(pic, fields(ltx), fields(qwen)));
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
    // API 1.48: a run output's thumbnail is its take's, before the full file
    expect(thumbOf({ ...img, thumb_url: "/files/k.thumb.webp" }).src).toBe("/files/k.thumb.webp");
    expect(thumbOf({ ...out, thumb_url: "/files/t.webp" }).src).toBe("/files/t.webp");
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

describe("the sound classes as kinds (plan 38, docs/mock/CreateMusicSound.dc.html)", () => {
  const w = (id: string, sound?: string, family = "echo", action?: string) =>
    ({ id, media: "audio", sound, family, source: "template", action, nodes: {} }) as never;
  const ws = [
    w("tts"),
    w("ace", "music", "ace-step"),
    w("repaint", "music", "ace-step", "Repaint"),
    w("minimax", "music", "minimax-music3"),
    w("sfx", "sound", "stable-audio-3"),
    w("foley", "sound", "mmaudio", "Add sound"),
  ];
  it("tabs Voice, Music and Sound by class, in the board's order; no class is voice", () => {
    expect(createKinds([...ws, qwen, ltx])).toEqual(["image", "video", "voice", "music", "sound"]);
    expect(kindOf(ws[0])).toBe("voice");
    expect(kindOf(ws[1])).toBe("music");
    expect(kindOf({ media: "text" })).toBeUndefined();
    expect(sideOf(ws[0])).toBe("sound"); // voice keeps the old draft key
    expect(sideOf(ws[1])).toBe("music");
    expect(sideOf(ws[4])).toBe("effects");
    expect(sideOf(qwen)).toBe("picture");
  });
  it("opens Music on MiniMax (owner, 2026-11-27) and never on a take action", () => {
    expect(workflowFor(ws, [], "music")).toBe("minimax");
    expect(kindTemplates(ws, "music").map((x) => (x as { id: string }).id)).toEqual([
      "minimax",
      "ace",
    ]);
    expect(workflowFor(ws, [{ workflow_id: "ace" }], "music")).toBe("ace");
    expect(workflowFor(ws, [], "sound")).toBe("sfx");
    expect(workflowFor(ws, [], "sound", "foley")).toBe("foley");
  });
  it("reads a sound's line: rate and channels", () => {
    expect(soundText({ sample_rate: 44100, channels: 2 })).toBe("44.1 kHz stereo");
    expect(soundText({ sample_rate: 24000 })).toBe("24 kHz mono");
    expect(soundText(undefined)).toBe("");
  });
});

describe("the run strip of music and sound (docs/mock/CreateMusicSound.dc.html frame 1)", () => {
  const echo = (id: string, sound?: string) =>
    ({
      ...wf(
        echoWorkflows.workflows.find((x) => x.id === id),
        echoNodes.nodes,
      ),
      sound,
    }) as S<"Workflow">;
  const done = { id: "r", inputs: {}, status: "succeeded", outputs: [] } as unknown as S<"Run">;
  it("ends a song's and a sound's with Saving, as a picture's; a voice's has none", () => {
    expect(segments(echo("echo-music", "music"), done).map((x) => x.title)).toEqual([
      "Echo music",
      "Saving",
    ]);
    expect(segments(echo("echo-sound", "sound"), done).at(-1)?.title).toBe("Saving");
    expect(segments(echo("echo-voice-over", "voice"), done).map((x) => x.title)).not.toContain(
      "Saving",
    );
  });
});
