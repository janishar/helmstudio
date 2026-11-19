import { describe, expect, it } from "vitest";
import fixture from "../../contracts/fixtures/http/ModelList.valid.json";
import type { S } from "@/lib/api";
import {
  anyDownloading,
  bytesText,
  canDelete,
  downloadPlan,
  folderOf,
  forHost,
  jobOpen,
  macOnly,
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
    expect(precisionTags(["int8"]).map((t) => t.text)).toEqual([
      "int8",
      "no load-time quantization",
    ]);
  });
  it("counts shared components per owner variant", () => {
    expect(sharesText(qwen, turbo)).toEqual(["shares 2 with Base"]);
    expect(sharesText(qwen, base)).toEqual([]);
  });
  it("describes the model", () => {
    expect(mediaText(ltx)).toBe("video + audio");
    expect(runtimeTags(ltx)).toEqual(["MLX"]);
    expect(macOnly(ltx)).toBe(true);
    expect(macOnly(qwen)).toBe(false);
    const h3 = { ...ltx, runtimes: [{ runtime: "native", env: "native" }] };
    expect(runtimeTags(h3)).toEqual(["Native"]);
    expect(macOnly(h3)).toBe(true);
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
