import { describe, expect, it } from "vitest";
import fixture from "../../contracts/fixtures/http/ModelList.valid.json";
import type { S } from "@/lib/api";
import type { Field } from "@/lib/workflow";
import {
  anyDownloading,
  bytesText,
  canDelete,
  downloadPlan,
  folderOf,
  forHost,
  jobOpen,
  deviceNote,
  downloaded,
  precisionPins,
  variantStored,
  mediaText,
  percent,
  precisionTags,
  runtimeTags,
  sharesText,
  stateText,
  variantBytes,
} from "@/lib/models";

const list = fixture as S<"ModelList">;
const [qwen, ltx] = list.models;
const [base, turbo] = qwen.variants;
type Model = S<"Model">;
type Variant = S<"ModelVariant">;
const on = (v: S<"ModelVariant">, host: string) => v.hosts.find((h) => h.host === host)!;

describe("model state per host", () => {
  it("words each state as the board does", () => {
    expect(stateText(on(base, "mac"))).toBe("present");
    expect(stateText(on(turbo, "mac"))).toBe("downloading · 62%");
    expect(stateText(on(turbo, "gpu1"))).toBe("missing");
    expect(stateText(on(base, "gpu1"))).toBe("hash mismatch · 1 file");
    expect(stateText(on(ltx.variants[0], "mac"))).toBe("partial");
  });
  it("is queued or starting until the runner knows the size", () => {
    const h = on(turbo, "mac");
    const queued = { ...h, job: { ...h.job!, state: "queued" as const, total_bytes: null } };
    expect(stateText(queued)).toBe("downloading · queued");
    expect(stateText({ ...queued, job: { ...queued.job, state: "running" } })).toBe(
      "downloading · starting",
    );
  });
  it("filters to one host and polls fast only while downloading", () => {
    const mac = forHost(list.models, "mac");
    expect(mac[0].variants.every((v) => v.hosts.every((h) => h.host === "mac"))).toBe(true);
    expect(forHost(list.models, "")).toBe(list.models);
    expect(anyDownloading(list.models)).toBe(true);
    expect(anyDownloading(forHost(list.models, "gpu1"))).toBe(false);
  });
  it("offers delete only for files on the host", () => {
    expect(canDelete(base, "mac")).toBe(true);
    expect(canDelete(base, "gpu1")).toBe(true); // mismatch: delete and download again
    expect(canDelete(turbo, "mac")).toBe(false); // downloading
    expect(canDelete(turbo, "gpu1")).toBe(false); // missing
  });
});

describe("optional add-ons (API 1.36)", () => {
  const list = fixture as unknown as S<"ModelList">;
  const m = list.models.find((x) => x.addons.length)!;
  const a = m.addons[0];
  it("are filtered by host, count as downloading, and plan a download as a variant", () => {
    expect(forHost(list.models, "gpu1").find((x) => x.id === m.id)!.addons[0].hosts).toEqual([
      a.hosts.find((h) => h.host === "gpu1"),
    ]);
    const only = [{ ...m, variants: [] }];
    expect(anyDownloading(only)).toBe(true); // the add-on downloads on mac
    expect(anyDownloading(forHost(only, "gpu1"))).toBe(false);
    expect(downloadPlan(list, a, "mac").blocked).toBe("downloading to mac");
    expect(downloadPlan(list, a, "gpu1").blocked).toBeNull();
    expect(canDelete(a, "gpu1")).toBe(false);
  });
});

describe("progress and sizes", () => {
  it("rounds progress down and caps it", () => {
    expect(percent({ done_bytes: 25360000000, total_bytes: 40900000000 })).toBe(62);
    expect(percent({ done_bytes: 5, total_bytes: 4 })).toBe(100);
    expect(percent({ done_bytes: 5, total_bytes: null })).toBeNull();
    expect(percent(null)).toBeNull();
  });
  it("shows GB like the board, MB below", () => {
    expect(bytesText(57800000000)).toBe("57.8 GB");
    expect(bytesText(300000000)).toBe("300 MB");
    expect(bytesText(null)).toBe("");
    expect(variantBytes(turbo)).toBe(40900000000);
    expect(variantBytes(ltx.variants[0])).toBe(1200000000); // no source size: what is on disk
  });
});

describe("variant tags", () => {
  it("names stored and load-time precisions", () => {
    expect(precisionTags(["bf16", "int8"]).map((t) => t.text)).toEqual(["bf16", "int8 at load"]);
    // official bf16 files quantized to int8 at load (MiniMax H3 Image Base) vs a stored fp8 build
    expect(precisionTags(["int8"], "bf16").map((t) => t.text)).toEqual(["bf16", "int8 at load"]);
    expect(precisionTags(["fp8"], "fp8").map((t) => t.text)).toEqual(["fp8", "stored quantized"]);
    expect(precisionTags(["int8"]).map((t) => t.text)).toEqual(["int8", "stored quantized"]);
    expect(precisionTags(["bf16"], "bf16").map((t) => t.text)).toEqual(["bf16"]);
    expect(precisionTags(["bf16", "int8", "int4"], "bf16")[1].text).toBe("int8 · int4 at load");
  });
  it("takes the variant's stored dtype from its largest component", () => {
    const c = (component: string, stored: string | undefined, bytes: number) =>
      ({
        component,
        from_variant: "base",
        precisions: ["int8"],
        stored,
        bytes,
      }) as Variant["components"][number];
    const v = {
      ...base,
      components: [
        c("text_encoder", "int8", 10),
        c("transformer", "bf16", 99),
        c("vae", undefined, 500),
      ],
    } as Variant;
    expect(variantStored(v)).toBe("bf16");
    expect(variantStored({ ...base, components: [] })).toBeUndefined();
    // one precision: the component stored at it wins over a larger one (MiniMax fp8 DiT beside an int8 text encoder)
    const fp8 = {
      ...base,
      precisions: ["fp8"],
      components: [c("text_encoder", "int8", 27), c("transformer", "fp8", 20)],
    } as Variant;
    expect(variantStored(fp8)).toBe("fp8");
    // one precision none is stored at: the largest (official bf16 files quantized to int8 at load)
    const official = {
      ...base,
      precisions: ["int8"],
      components: [c("text_encoder", "bf16", 66), c("transformer", "bf16", 66)],
    } as Variant;
    expect(variantStored(official)).toBe("bf16");
  });
  it("counts shared components per owner variant", () => {
    expect(sharesText(qwen, turbo)).toEqual(["shares 2 with Base"]);
    expect(sharesText(qwen, base)).toEqual([]);
  });
  it("describes the model", () => {
    expect(mediaText(ltx)).toBe("video + audio");
    expect(runtimeTags(ltx)).toEqual(["MLX"]);
    expect(deviceNote(ltx, ltx.variants[0])).toBe("Mac only");
    expect(deviceNote(qwen, base)).toBeNull();
    const h3 = { ...ltx, runtimes: [{ runtime: "native", env: "native" }] };
    expect(runtimeTags(h3)).toEqual(["Native"]);
    expect(deviceNote(h3, ltx.variants[0])).toBe("Mac only");
    // per variant: MiniMax H3's base on Metal, base-fp8 on CUDA, one model with both runtimes' devices
    const both = {
      ...ltx,
      runtimes: [{ runtime: "native", env: "native", devices: ["mps", "cuda"] }],
    } as Model;
    expect(deviceNote(both, { ...base, devices: ["mps"] })).toBe("Mac only");
    expect(deviceNote(both, { ...base, devices: ["cuda"] })).toBe("NVIDIA only");
    expect(deviceNote(both, base)).toBeNull();
    expect(folderOf(list, turbo, "mac")).toBe("~/GenAI/models/Qwen-Image-2.1-Turbo");
  });
});

describe("download plan", () => {
  it("asks for the licence and notes a missing token for a gated repo", () => {
    const p = downloadPlan(list, turbo, "gpu1");
    expect([p.licence?.id, p.mustAccept, p.blocked]).toEqual(["qwen-research", true, null]);
    expect(p.gatedNote).toBeNull(); // gpu1 has a token
  });
  it("blocks what is there, downloading, or has no source", () => {
    expect(downloadPlan(list, base, "mac").blocked).toBe("already on mac");
    expect(downloadPlan(list, turbo, "mac").blocked).toBe("downloading to mac");
    expect(downloadPlan(list, turbo, "mac").gatedNote).toMatch(/mac has none/);
    expect(downloadPlan(list, ltx.variants[0], "mac").blocked).toBe("no download source");
    expect(downloadPlan(list, ltx.variants[0], "mac").licence).toBeNull();
  });
  it("keeps a queued notice only while its job is open", () => {
    const job = (state: string) => ({ id: "j1", state }) as S<"ModelJob">;
    expect(jobOpen([], "j1")).toBe(true); // not in the list yet
    expect(jobOpen([job("running")], "j1")).toBe(true);
    expect(jobOpen([job("succeeded")], "j1")).toBe(false);
    expect(jobOpen([job("failed")], "j1")).toBe(false);
  });
  it("needs no accept for a licence that does not ask", () => {
    const p = downloadPlan(list, base, "gpu1");
    expect([p.licence?.id, p.mustAccept, p.blocked]).toEqual(["apache-2.0", false, null]);
  });
});

describe("Create's model data", () => {
  const fp8 = { ...base, id: "base-fp8", precisions: ["fp8"] } as Variant;
  const withFp8 = { ...list, models: [{ ...qwen, variants: [base, fp8] }] } as S<"ModelList">;
  const fieldOf = (name: string, enumv: string[], initial: string) =>
    ({
      key: `model.${name}`,
      node: "model",
      nodeTitle: "Model",
      param: { name, type: "enum", enum: enumv, default: initial },
      initial,
      canvas: false,
      model: true,
      family: qwen.family,
    }) as Field;
  const fs = [
    fieldOf("variant", ["base", "base-fp8"], "base"),
    fieldOf("precision", ["bf16", "int8", "fp8"], "int8"),
  ];
  it("pins the precision of a variant that loads at one", () => {
    expect(precisionPins(fs, {}, withFp8)).toEqual({}); // base: bf16 or int8, a choice
    expect(precisionPins(fs, { "model.variant": "base-fp8" }, withFp8)).toEqual({
      "model.precision": "fp8",
    });
    expect(precisionPins(fs, { "model.variant": "base-fp8" }, null)).toEqual({}); // list not known yet
  });
  it("knows a family with no variant present on a live host", () => {
    expect(downloaded(list, qwen.family)).toBe(true); // base present on the live mac
    const dead = { ...list, hosts: list.hosts.map((h) => ({ ...h, live: false })) };
    expect(downloaded(dead, qwen.family)).toBe(false);
    expect(downloaded(null, qwen.family)).toBe(true);
    expect(downloaded(list, null)).toBe(true); // a core workflow
  });
});
