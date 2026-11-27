// Magic's standard request in the web (plan 39 phase D): the board's takes made from studio's answer (done takes,
// running and failed runs as placeholders, out of date when studio says so or the request changes here), the
// statuses they give, the pack's estimate and a run history line.
import { describe, expect, it } from "vitest";
import { statusOf, type Doc } from "@/lib/magic";
import { emptyDoc } from "@/lib/magic-fixture";
import { soundLook } from "@/components/magic/sound-mark";
import type { S } from "@/lib/api";
import type { Machine } from "@/lib/run-on";
import {
  advancedOf,
  madeBy,
  modelOf,
  packSeconds,
  runLine,
  takesOf,
  variantChoices,
  variantOf,
  variantParam,
  FAKE_KINDS,
  type BlockRun,
  type Kinds,
  type Model,
} from "@/lib/magic-standard";

const T = "11111111-1111-4111-8111-111111111111";
const run = (o: Partial<BlockRun>): BlockRun => ({
  block: "b1",
  run_id: "r1",
  status: "succeeded",
  takes: [],
  seed: 7,
  current: true,
  by: "user",
  created_at: "2026-11-27T05:00:00Z",
  render_s: 112,
  ...o,
});
function doc(takes: string[]): Doc {
  const d = emptyDoc();
  d.lanes = [{ id: "l1", name: "Shots", role: "shots" }];
  d.blocks = [
    {
      id: "b1",
      lane: "l1",
      at: 0,
      length: 96,
      kind: "video",
      name: "Shot 1",
      request: { kind: "video", prompt: "rain" },
      takes,
      effects: [],
    },
  ];
  return d;
}

describe("the board's takes from studio", () => {
  it("running, failed, done, then out of date", () => {
    let d = doc([]);
    let t = takesOf(d, [run({ status: "running", progress: 62 })], {});
    expect(statusOf(d, d.blocks[0], t)).toEqual({ s: "running", progress: 62 });
    t = takesOf(d, [run({ status: "failed", error: "out of memory" })], {});
    expect(statusOf(d, d.blocks[0], t)).toEqual({ s: "failed", error: "out of memory" });
    d = doc([T]);
    const wire = { [T]: { id: T, kind: "video" as const, url: "u", thumb_url: "th", seed: 7 } };
    t = takesOf(d, [run({ takes: [T] })], wire);
    expect(statusOf(d, d.blocks[0], t)).toEqual({ s: "done" });
    expect(t[T].art).toBe('url("th")');
    // the request changed here: out of date at once; studio saying so: the same
    const edited = {
      ...d,
      blocks: [{ ...d.blocks[0], request: { kind: "video" as const, prompt: "more" } }],
    };
    expect(statusOf(edited, edited.blocks[0], t)).toEqual({ s: "old" });
    t = takesOf(d, [run({ takes: [T], current: false })], wire);
    expect(statusOf(d, d.blocks[0], t)).toEqual({ s: "old" });
  });

  it("the pack's estimate, the default model and a run history line", () => {
    const d = doc([]);
    const m = modelOf(FAKE_KINDS, { kind: "video" });
    expect(m?.name).toBe("LTX 2.5");
    expect(packSeconds(m, d.blocks[0], d)).toBe(56); // 14 s a second × 4 s at 720p
    expect(packSeconds(m, { ...d.blocks[0], request: { kind: "video", count: 2 } }, d)).toBe(112);
    expect(runLine(run({}), 2).title).toBe("Run 2");
    expect(runLine(run({}), 2).sub).toMatch(/^seed 7 · 1:52 · /);
  });
});

describe("phase H: music and sound", () => {
  it("a music take's cover, a sound take's class, the look by the take's class", () => {
    const d = doc([]);
    d.blocks.push({
      id: "m1",
      lane: "l1",
      at: 0,
      length: 96,
      kind: "music",
      name: "Cue 1",
      request: { kind: "music", prompt: "soft rain" },
      takes: [T],
      effects: [],
    });
    const url = "https://x/0.wav";
    const out = takesOf(d, [run({ block: "m1", takes: [T] })], {
      [T]: {
        id: T,
        kind: "audio",
        url,
        thumb_url: url,
        poster_url: null,
        seed: 7,
        run_id: "r1",
        sound: "music",
      },
    });
    expect(out[T].art).toMatch(/^linear-gradient\(150deg/); // never the wav as a picture
    expect(out[T].sound).toBe("music");
    expect(soundLook(d.blocks[1], out[T])).toBe("music");
    // a sound placed on a music block draws as a sound
    expect(soundLook(d.blocks[1], { ...out[T], sound: "sound" })).toBe("sound");
    expect(soundLook(d.blocks[0], undefined)).toBeNull();
  });

  it("a sound from a shot's video runs the first model that takes one, with its video workflow's Advanced", () => {
    const sound = FAKE_KINDS.kinds.find((k) => k.kind === "sound")!;
    expect(modelOf(FAKE_KINDS, { kind: "sound" })?.workflow_id).toBe("sa3-sound");
    const m = modelOf(FAKE_KINDS, { kind: "sound", from: "s2" });
    expect(m?.workflow_id).toBe("mmaudio-sound");
    expect(modelOf(FAKE_KINDS, { kind: "sound", model: "mmaudio-video" })).toBe(m);
    expect(advancedOf(m, { kind: "sound", from: "s2" })).toEqual(sound.models[1].video!.advanced);
    expect(modelOf(FAKE_KINDS, { kind: "music" })?.name).toBe("MiniMax Music 3");
  });
});

// API 1.94 (owner, 2026-11-27: "yes add them"; MagicBoard 3 and 7): the variant on runs and takes, the Variant
// choice and the estimate following it.
describe("variants", () => {
  const variant = {
    path: "model.variant",
    param: { name: "variant", type: "enum", default: "base", enum: ["base", "turbo", "turbo-fp8"] },
  } as Model["advanced"][number];
  const qwen: Model = {
    ...FAKE_KINDS.kinds[1].models[0],
    family: "qwen-image",
    env: "torch",
    advanced: [
      variant,
      { ...variant, path: "sample.steps", param: { name: "steps", type: "int", default: 40 } },
    ],
  };
  const kinds: Kinds = { kinds: [{ kind: "frame", fields: [], models: [qwen] }] };
  const v = (id: string, title: string, hosts: string[], devices?: ("cuda" | "mps")[]) => ({
    id,
    title,
    devices,
    precisions: ["int8"],
    dir: id,
    licence: null,
    gated: false,
    download_bytes: null,
    components: [],
    hosts: hosts.map((host) => ({ host, state: "present" as const, bytes: 1, job: null })),
  });
  const list = {
    hosts: [],
    licences: [],
    models: [
      {
        id: "qwen-image-2.1",
        family: "qwen-image",
        title: "Qwen Image 2.1",
        media: ["image"],
        runtimes: [{ runtime: "torch", env: "torch", devices: ["mps", "cuda"] }],
        variants: [
          v("base", "Base", ["mac"]),
          v("turbo", "Turbo", ["mac"]),
          v("turbo-fp8", "Turbo fp8", [], ["cuda"]),
        ],
        addons: [],
      },
    ],
  } as unknown as S<"ModelList">;
  const mc = (host: string, device: string): Machine => ({
    host,
    label: host,
    sub: host,
    device,
    live: true,
    envs: ["torch"],
  });

  it("the param is lifted out of Advanced; the request's variant is its value, else the workflow's own", () => {
    const req = { kind: "frame" as const, advanced: { "model.variant": "turbo" } };
    expect(advancedOf(qwen, req).map((p) => p.path)).toEqual(["sample.steps"]);
    expect(variantParam(qwen, req)?.enum).toHaveLength(3);
    expect(variantOf(qwen, req)).toBe("turbo");
    expect(variantOf(qwen, { kind: "frame", advanced: { "model.variant": "nope" } })).toBe("base");
    expect(variantOf(FAKE_KINDS.kinds[0].models[0], { kind: "video" })).toBeUndefined();
  });

  it("the choice: titles, fp8 only where a host has the GPU, greyed with why", () => {
    const mac = mc("mac", "mps");
    // a Mac alone: no fp8
    expect(variantChoices(qwen, { kind: "frame" }, list, [mac])).toEqual([
      { id: "base", title: "Base", why: undefined },
      { id: "turbo", title: "Turbo", why: undefined },
    ]);
    // an NVIDIA host too: Turbo fp8 shows, greyed (on no live host); under Run on gcp Base is not downloaded there
    const both = [mac, mc("gcp", "cuda")];
    expect(variantChoices(qwen, { kind: "frame" }, list, both).at(-1)).toEqual({
      id: "turbo-fp8",
      title: "Turbo fp8",
      why: "Turbo fp8 not downloaded",
    });
    expect(variantChoices(qwen, { kind: "frame" }, list, both, both[1])[0].why).toBe(
      "Base not downloaded on gcp",
    );
    // without the models list: the ids, nothing greyed
    expect(variantChoices(qwen, { kind: "frame" }, null, []).map((c) => c.title)).toEqual([
      "base",
      "turbo",
      "turbo-fp8",
    ]);
  });

  it("runs, takes and the model line say the variant; the pack's rate is only the workflow's own variant's", () => {
    const d = doc(["t1"]);
    const r = run({ takes: [T], variant: "Turbo", workflow_id: "qwen-image" });
    expect(runLine(r, 1).sub).toMatch(/^Turbo · seed 7 · /);
    expect(takesOf(d, [r], {})[T].variant).toBe("Turbo");
    expect(madeBy(kinds, [r], T)).toBe("Qwen Image 2.1 · Turbo");
    expect(madeBy(kinds, [r], "other")).toBe("");
    const b = { kind: "frame" as const, length: 96, request: { kind: "frame" as const } };
    expect(packSeconds(qwen, b, d)).not.toBeNull();
    expect(
      packSeconds(
        qwen,
        { ...b, request: { kind: "frame", advanced: { "model.variant": "turbo" } } },
        d,
      ),
    ).toBeNull();
  });
});
