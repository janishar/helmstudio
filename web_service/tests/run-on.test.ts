// Run on's data (lib/run-on.ts; docs/mock/RunOn.dc.html): machine names from devices, the greyed reasons, the
// version clash note. Everything from what hosts publish.
import { describe, expect, it } from "vitest";
import type { S } from "@/lib/api";
import {
  addonBlocks,
  addonNeeds,
  clashNote,
  needed,
  variantBlock,
  machineReason,
  machines,
  pinnedMachine,
  versionDim,
  versionRow,
  valueBlock,
  valueNeeds,
  valueNeedsAt,
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
    const base = { node: "enhance", family: "qwen-image", free: [], always: false };
    expect(addonNeeds(w, list())).toEqual([
      { ...base, component: "text_enhancer", title: "Text enhancer", hosts: ["mac"] },
      { ...base, component: "edit_enhancer", title: "Edit enhancer", hosts: ["mac", "gcp"] },
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

  // API 1.40: Qwen's enhancers per task (`refs`), both named "Enhancer" (`short`, board CreateEnhance)
  it("needs only the add-on of the form's task, by its short name", () => {
    const perTask = {
      ...list(),
      models: [
        {
          ...model,
          addons: [
            {
              ...addon("text_enhancer", "Text enhancer", { mac: "present", gcp: "missing" }),
              short: "Enhancer",
              refs: false,
            },
            {
              ...addon("edit_enhancer", "Edit enhancer", { mac: "missing", gcp: "present" }),
              short: "Enhancer",
              refs: true,
            },
          ],
        },
      ],
    } as unknown as S<"ModelList">;
    const all = addonNeeds(w, perTask);
    expect(all.map((n) => [n.title, n.refs])).toEqual([
      ["Enhancer", false],
      ["Enhancer", true],
    ]);
    const text = all.filter((n) => needed(n, [], false));
    const edit = all.filter((n) => needed(n, [], true));
    expect(text.map((n) => n.component)).toEqual(["text_enhancer"]);
    expect(edit.map((n) => n.component)).toEqual(["edit_enhancer"]);
    expect(addonBlocks(text, gcp)).toEqual({ enhance: "Enhancer not downloaded on gcp" });
    expect(addonBlocks(edit, gcp)).toEqual({});
    expect(machineReason(mac, "torch", [], edit)).toBe("enhancer not downloaded on mac");
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

describe("add-ons scoped to variants (API 1.40)", () => {
  const [mac, gcp] = machines([
    host("mac", "mlx", "mps", "Apple M5 Pro"),
    host("gcp", "torch", "cuda", "RTX PRO 6000"),
  ]);
  const ltx = (lora: string) =>
    ({
      hosts: ["mac", "gcp"].map((h) => ({ host: h, live: true })),
      models: [
        {
          ...model,
          family: "ltx",
          // the NVIDIA host has the model but lists neither add-on: it needs none
          variants: [
            variant("Distilled", ["mac"]),
            variant("Dev", ["mac"]),
            variant("Fp8", ["gcp"]),
          ],
          addons: [
            {
              component: "ic_lora",
              title: "Detailer",
              variants: ["distilled"],
              hosts: [{ host: "mac", state: "missing" }],
            },
            {
              component: "distilled_lora",
              title: "Distilled LoRA",
              variants: ["dev"],
              hosts: [{ host: "mac", state: lora }],
            },
          ],
        },
      ],
    }) as unknown as S<"ModelList">;
  const schema = (kind: string, addons: string[]) => ({
    kind,
    family: "ltx",
    addons,
    params: [],
    inputs: [],
    outputs: [],
  });
  const w = {
    nodes: { detail: { kind: "ltx/ic-lora", bypass: true }, distill: { kind: "ltx/distill" } },
    node_schemas: {
      "ltx/ic-lora": schema("ltx/ic-lora", ["ic_lora"]),
      "ltx/distill": schema("ltx/distill", ["distilled_lora"]),
    },
  } as unknown as S<"Workflow">;
  const pick = (id: string) => [
    { m: ltx("missing").models[0], v: ltx("missing").models[0].variants.find((v) => v.id === id)! },
  ];

  it("a run on another variant needs none; the node always run greys its variants' rows, not its controls", () => {
    const needs = addonNeeds(w, ltx("missing"));
    expect(needs.map((n) => [n.node, n.always, n.free])).toEqual([
      ["detail", false, ["gcp"]],
      ["distill", true, ["gcp"]],
    ]);
    // Distilled: the detailer is needed (Detail greyed), the distilled LoRA not
    const distilled = needs.filter((n) => needed(n, pick("distilled")));
    expect(distilled.map((n) => n.node)).toEqual(["detail"]);
    expect(addonBlocks(distilled)).toEqual({ detail: "Detailer not downloaded" });
    // Dev: the distilled LoRA's node is never forced off; Dev's row is greyed instead (board LtxFeatures 17)
    const dev = needs.filter((n) => needed(n, pick("dev")));
    expect(addonBlocks(dev)).toEqual({});
    expect(variantBlock(needs, "ltx", "dev")).toBe("Distilled LoRA not downloaded");
    expect(variantBlock(needs, "ltx", "dev", mac)).toBe("Distilled LoRA not downloaded on mac");
    expect(variantBlock(needs, "ltx", "distilled")).toBeNull();
    expect(variantBlock(addonNeeds(w, ltx("present")), "ltx", "dev")).toBeNull();
    expect(machineReason(mac, "mlx", [], dev)).toBe("distilled LoRA not downloaded on mac");
  });

  it("a host that does not list an add-on needs none there (NVIDIA: no add-ons)", () => {
    const needs = addonNeeds(w, ltx("missing"));
    expect(addonBlocks(needs, gcp)).toEqual({});
    expect(machineReason(gcp, "torch", [], needs)).toBeNull();
    expect(addonBlocks(needs, mac)).toEqual({ detail: "Detailer not downloaded on mac" });
  });
});

// API 1.41 (contracts/README.md Optional add-ons: an add-on per value; docs/mock/H3Options.dc.html frames 11-13):
// MiniMax H3's lora turbo needs Turbo LoRA, with a reference Turbo Ref LoRA instead
describe("an add-on per value (API 1.41)", () => {
  const [mac, gcp] = machines([
    host("mac", "native", "mps", "Apple M5 Pro"),
    host("gcp", "native", "cuda", "RTX PRO 6000"),
  ]);
  const w = {
    nodes: { generate: { kind: "minimax-h3/generate" } },
    node_schemas: {
      "minimax-h3/generate": {
        kind: "minimax-h3/generate",
        family: "minimax-h3",
        params: [
          {
            name: "lora",
            type: "enum",
            default: "none",
            enum: ["none", "turbo"],
            addons: { turbo: ["turbo"] },
            addons_with: { refs: { turbo: ["turbo_ref"] }, videos: { turbo: ["turbo_ref"] } },
          },
        ],
        inputs: [],
        outputs: [],
      },
    },
  } as unknown as S<"Workflow">;
  // both hosts list both add-ons (the add-on is of both variants): missing unless given
  const on = (x: Record<string, string>) =>
    ["mac", "gcp"].map((host) => ({ host, state: x[host] ?? "missing" }));
  const list = (turbo: Record<string, string>, ref: Record<string, string>) =>
    ({
      hosts: ["mac", "gcp"].map((h) => ({ host: h, live: true })),
      models: [
        {
          ...model,
          id: "minimax-h3",
          family: "minimax-h3",
          variants: [variant("Base", ["mac"]), variant("Base fp8", ["gcp"])],
          addons: [
            { component: "turbo", title: "Turbo LoRA", hosts: on(turbo) },
            { component: "turbo_ref", title: "Turbo Ref LoRA", hosts: on(ref) },
          ],
        },
      ],
    }) as unknown as S<"ModelList">;
  const none = () => false;
  const refs = (n: string) => n === "refs";

  it("lists each value's add-ons and picks by what is wired", () => {
    const needs = valueNeeds(w, list({ mac: "present" }, {}));
    expect(needs.map((n) => [n.field, n.value, n.component, n.with ?? null])).toEqual([
      ["generate.lora", "turbo", "turbo", null],
      ["generate.lora", "turbo", "turbo_ref", "refs"],
      ["generate.lora", "turbo", "turbo_ref", "videos"],
    ]);
    expect(valueNeedsAt(needs, "generate.lora", "turbo", none).map((n) => n.title)).toEqual([
      "Turbo LoRA",
    ]);
    expect(valueNeedsAt(needs, "generate.lora", "turbo", refs).map((n) => n.title)).toEqual([
      "Turbo Ref LoRA",
    ]);
    expect(valueNeedsAt(needs, "generate.lora", "none", refs)).toEqual([]);
  });

  it("greys the option without a reference when no host that can run it has Turbo LoRA (frame 11)", () => {
    const needs = valueNeeds(w, list({ gcp: "present" }, { mac: "present" }));
    // Base runs on the Mac only: gcp's copy does not count
    expect(valueBlock(needs, "generate.lora", "turbo", none, [mac])).toBe(
      "Turbo LoRA not downloaded",
    );
    expect(valueBlock(needs, "generate.lora", "none", none, [mac])).toBeNull();
    expect(valueBlock(needs, "generate.lora", "turbo", none, [mac, gcp])).toBeNull();
  });

  it("per host under Run on, in the option's own case (frame 12)", () => {
    const needs = valueNeeds(w, list({ mac: "present" }, {}));
    expect(valueBlock(needs, "generate.lora", "turbo", none, [mac, gcp], gcp)).toBe(
      "Turbo LoRA not downloaded on gcp",
    );
    expect(valueBlock(needs, "generate.lora", "turbo", none, [mac, gcp], mac)).toBeNull();
    const held = valueNeedsAt(needs, "generate.lora", "turbo", none);
    expect(machineReason(gcp, "native", [], held)).toBe("Turbo LoRA not downloaded on gcp");
    expect(machineReason(mac, "native", [], held)).toBeNull();
  });

  it("with a reference wired it needs Turbo Ref LoRA, and re-checks when it goes (frame 13)", () => {
    const needs = valueNeeds(w, list({ mac: "present" }, {}));
    expect(valueBlock(needs, "generate.lora", "turbo", refs, [mac])).toBe(
      "Turbo Ref LoRA not downloaded",
    );
    expect(valueBlock(needs, "generate.lora", "turbo", none, [mac])).toBeNull();
    const held = valueNeedsAt(needs, "generate.lora", "turbo", refs);
    expect(machineReason(mac, "native", [], held)).toBe("Turbo Ref LoRA not downloaded on mac");
  });
});
