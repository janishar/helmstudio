// Run on's data (lib/run-on.ts; docs/mock/RunOn.dc.html): machine names from devices, the greyed reasons, the
// version clash note. Everything from what hosts publish.
import { describe, expect, it } from "vitest";
import type { S } from "@/lib/api";
import {
  clashNote,
  machineReason,
  machines,
  pinnedMachine,
  versionDim,
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
