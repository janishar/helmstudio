import { describe, expect, it } from "vitest";
import list from "../../contracts/fixtures/http/HostList.valid.json";
import type { S } from "@/lib/api";
import { hostMemory } from "@/lib/hosts";

const hosts = list.hosts as S<"Host">[];

describe("hostMemory", () => {
  it("shows the host's shared budget and every env of it", () => {
    const m = hostMemory(hosts, "torch")!;
    expect([m.host, m.used_gb, m.budget_gb, m.busy]).toEqual(["mac", 58.4, 48, false]);
    expect(m.envs.map((e) => e.env)).toEqual(["mlx", "torch"]);
  });
  it("is busy when any env of the host is", () => {
    const h = hosts.map((x) => (x.env === "mlx" ? { ...x, state: "busy" as const } : x));
    expect(hostMemory(h, "torch")!.busy).toBe(true);
  });
  it("falls back to the env's own memory for an older runner", () => {
    const h = hosts.map((x) => ({ ...x, host_memory: undefined }));
    const m = hostMemory(h, "torch")!;
    expect([m.used_gb, m.budget_gb, m.envs.length]).toEqual([17.2, 52, 1]);
  });
  it("is nothing without a live host that reports memory", () => {
    expect(hostMemory(hosts, "fake")).toBeNull();
    expect(
      hostMemory(
        hosts.map((x) => ({ ...x, state: "offline" as const })),
        "torch",
      ),
    ).toBeNull();
  });
});
