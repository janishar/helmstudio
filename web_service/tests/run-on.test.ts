// Run on's data (lib/run-on.ts; docs/mock/RunOn.dc.html): machine names from devices, the greyed reasons, the
// version clash note. Everything from what hosts publish.
import { describe, expect, it } from "vitest";
import type { S } from "@/lib/api";
import {
  addonBlocks,
  addonNeeds,
  clashNote,
  machineReason,
  machines,
  pinnedMachine,
  versionDim,
  versionRow,
  type Machine,
} from "@/lib/run-on";

const host = (name: string, env: string, kind?: string, device = "", state = "idle") =>
  ({
    host: name,
    env,
    state,
    updated_at: "2026-11-22T00:00:00Z",
    ...(kind && { device: { kind, name: device, memory_total_gb: 1 } }),
  }) as S<"Host">;

const model = {
  id: "qwen-image-2.1",
  family: "qwen-image",
  title: "Qwen Image",
  media: ["image"],
  runtimes: [{ runtime: "torch", env: "torch" }],
  variants: [],
} as unknown as S<"Model">;
const variant = (title: string, hosts: string[], devices?: string[]) =>
  ({
    id: title.toLowerCase(),
    title,
    devices,
    hosts: hosts.map((h) => ({ host: h, state: "present" })),
  }) as unknown as S<"ModelVariant">;

describe("machines", () => {
  it("named by device, envs folded, a server env alone is no machine, Mac first", () => {
    const got = machines([
      host("gcp", "torch", "cuda", "RTX PRO 6000"),
      host("mac", "mlx", "mps", "Apple M5 Pro"),
      host("mac", "torch", "mps", "Apple M5 Pro", "offline"),
      host("llm", "server"),
    ]);
    expect(got.map((m) => [m.label, m.sub, m.live, m.envs])).toEqual([
      ["Mac", "mac · Apple M5 Pro", true, ["mlx"]],
      ["NVIDIA", "gcp · RTX PRO 6000", true, ["torch"]],
    ]);
  });

  it("the sub-line names the GPU, not a native env's CPU", () => {
    const gpu = host("gcp", "torch", "cuda", "NVIDIA RTX PRO 6000 Blackwell Server Edition");
    (gpu.device as { driver?: string }).driver = "615.71";
    const [got] = machines([host("gcp", "native", "cuda", "AMD EPYC 9B45"), gpu]);
    expect(got.sub).toBe("gcp · RTX PRO 6000");
  });

  it("two hosts of one device, or neither device: the host name", () => {
    const got = machines([
      host("lab", "torch", "cuda", "RTX 4090"),
      host("gcp", "torch", "cuda", "RTX PRO 6000"),
      host("fake", "fake", "cpu"),
    ]);
    expect(got.map((m) => m.label)).toEqual(["gcp", "lab", "fake"]);
  });

  it("a chosen host no longer connected stays chosen, offline; Auto is undefined", () => {
    expect(pinnedMachine([], "gcp")).toMatchObject({ host: "gcp", live: false });
    expect(pinnedMachine([], "")).toBeUndefined();
  });
});

describe("reasons", () => {
  const [mac, gcp] = machines([
    host("mac", "torch", "mps", "Apple M5 Pro"),
    host("gcp", "torch", "cuda", "RTX PRO 6000"),
  ]);
  const base = variant("Base", ["mac", "gcp"]);
  const turbo = variant("Turbo", ["mac"]);
  const fp8 = variant("Base fp8", ["gcp"], ["cuda"]);

  it("a host row: offline, another env, a device-only version, not downloaded, else pickable", () => {
    const off: Machine = { ...gcp, live: false };
    expect(machineReason(off, "torch", [{ m: model, v: base }])).toBe("offline");
    expect(machineReason(gcp, "mlx", [])).toBe("cannot run this workflow");
    expect(machineReason(mac, "torch", [{ m: model, v: fp8 }])).toBe("Base fp8 is NVIDIA only");
    expect(machineReason(gcp, "torch", [{ m: model, v: turbo }])).toBe("not downloaded on gcp");
    expect(machineReason(gcp, "torch", [{ m: model, v: base }])).toBeNull();
  });

  it("a version under a host: dimmed, and the note when picking it resets Run on", () => {
    expect(versionDim(undefined, model, fp8)).toBeNull();
    expect(versionDim(mac, model, fp8)).toBe("not on Mac");
    expect(versionDim(mac, model, base)).toBeNull();
    expect(clashNote(mac, model, fp8)).toBe("Run on set to Auto: Base fp8 is NVIDIA only");
    expect(clashNote(gcp, model, turbo)).toBe("Run on set to Auto: Turbo is not downloaded on gcp");
    expect(clashNote(mac, model, base)).toBeNull();
  });
});

describe("optional add-ons (API 1.36)", () => {
  const [mac, gcp] = machines([
    host("mac", "torch", "mps", "Apple M5 Pro"),
    host("gcp", "torch", "cuda", "RTX PRO 6000"),
  ]);
  const w = {
    nodes: { prompt: { kind: "prompt" }, enhance: { kind: "qwen-image/enhance", bypass: true } },
    node_schemas: {
      prompt: { kind: "prompt", family: null, params: [], inputs: [], outputs: [] },
      "qwen-image/enhance": {
        kind: "qwen-image/enhance",
        family: "qwen-image",
        addons: ["text_enhancer", "edit_enhancer"],
        params: [],
        inputs: [],
        outputs: [],
      },
    },
  } as unknown as S<"Workflow">;
  const addon = (component: string, title: string, on: Record<string, string>) => ({
    component,
    title,
    hosts: Object.entries(on).map(([h, state]) => ({ host: h, state })),
  });
  const list = (offline = "") =>
    ({
      hosts: ["mac", "gcp"].map((h) => ({ host: h, live: h !== offline })),
      models: [
        {
          ...model,
          addons: [
            addon("text_enhancer", "Text enhancer", { mac: "present", gcp: "missing" }),
            addon("edit_enhancer", "Edit enhancer", { mac: "present", gcp: "present" }),
          ],
        },
      ],
    }) as unknown as S<"ModelList">;

  it("a node's add-ons with the live hosts they are on; nothing to tell without a list or the family", () => {
    expect(addonNeeds(w, list())).toEqual([
      { node: "enhance", component: "text_enhancer", title: "Text enhancer", hosts: ["mac"] },
      {
        node: "enhance",
        component: "edit_enhancer",
        title: "Edit enhancer",
        hosts: ["mac", "gcp"],
      },
    ]);
    expect(addonNeeds(w, list("mac"))[0].hosts).toEqual([]); // an offline host does not count
    expect(addonNeeds(w, null)).toEqual([]);
    expect(addonNeeds(w, { ...list(), models: [] })).toEqual([]);
    // the family's models do not list it: missing everywhere, named by its component
    const none = { ...list(), models: [{ ...model, addons: [] }] } as S<"ModelList">;
    expect(addonNeeds(w, none)[0]).toMatchObject({ title: "text_enhancer", hosts: [] });
  });

  it("greys the node under Auto when no live host has it, under a host when that one lacks it", () => {
    expect(addonBlocks(addonNeeds(w, list()))).toEqual({});
    expect(addonBlocks(addonNeeds(w, list("mac")))).toEqual({
      enhance: "Text enhancer not downloaded",
    });
    expect(addonBlocks(addonNeeds(w, list()), gcp)).toEqual({
      enhance: "Text enhancer not downloaded on gcp",
    });
    expect(addonBlocks(addonNeeds(w, list()), mac)).toEqual({});
  });

  it("a Run on row without an add-on a running node needs is greyed, in lower case", () => {
    const needs = addonNeeds(w, list());
    expect(machineReason(gcp, "torch", [], needs)).toBe("text enhancer not downloaded on gcp");
    expect(machineReason(mac, "torch", [], needs)).toBeNull();
    expect(machineReason(gcp, "torch", [], [])).toBeNull(); // the node is bypassed: not asked
  });
});

describe("versionRow", () => {
  it("carries the variant's line (API 1.39 help) for the model chip's menu", () => {
    const list = {
      models: [
        {
          ...model,
          variants: [{ ...variant("Distilled", ["mac"]), help: "8 steps, about 10× faster" }],
        },
      ],
    } as unknown as S<"ModelList">;
    const f = { family: "qwen-image" } as Parameters<typeof versionRow>[2];
    expect(versionRow(list, undefined, f, "distilled")?.sub).toBe("8 steps, about 10× faster");
  });
});
