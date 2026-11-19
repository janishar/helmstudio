import { describe, expect, it } from "vitest";
import list from "../../contracts/fixtures/http/HostList.valid.json";
import type { S } from "@/lib/api";
import queue from "../../contracts/fixtures/http/Queue.valid.json";
import { hostMemory, queuedCount } from "@/lib/hosts";

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

import admin from "../../contracts/fixtures/http/HostList.valid.2.json";
import {
  afterRunTag,
  ago,
  componentText,
  hostCards,
  isPinned,
  machineText,
  pinRef,
  settingsOf,
  togglePin,
} from "@/lib/hosts";

describe("Admin › Hosts", () => {
  const [mac] = hostCards(admin.hosts as S<"Host">[]);
  it("folds the envs of a host into one card", () => {
    expect([mac.host, mac.state, mac.used_gb, mac.budget_gb]).toEqual(["mac", "idle", 58.4, 48]);
    expect(mac.envs.map((e) => e.env)).toEqual(["mlx", "torch"]);
    expect(mac.entries).toHaveLength(8);
    expect([mac.components_gb, mac.cached_gb]).toEqual([58.4, 2.1]);
    expect(mac.settings?.version).toBe(3);
    expect(mac.heartbeat).toBe("2026-11-15T10:11:02Z");
    expect(machineText(mac.machine)).toBe("Apple Silicon · Metal · native");
    expect(afterRunTag(mac.policy!.after_run)).toBe("After run: keep warm");
  });
  it("leaves out a local server's env, which holds no weights", () => {
    const server = { ...admin.hosts[0], env: "server", resident: [] } as S<"Host">;
    const [card] = hostCards([...(admin.hosts as S<"Host">[]), server]);
    expect(card.envs.map((e) => e.env)).toEqual(["mlx", "torch"]);
    expect(hostCards([server])[0].envs.map((e) => e.env)).toEqual(["server"]);
  });
  it("is running while any env is busy, offline when none is live", () => {
    const h = admin.hosts.map((x) => ({ ...x, state: "busy" })) as S<"Host">[];
    expect(hostCards(h)[0].state).toBe("running");
    const off = admin.hosts.map((x) => ({ ...x, state: "offline" })) as S<"Host">[];
    expect(hostCards(off)[0].state).toBe("offline");
  });
  it("shows the pins of the settings and toggles one", () => {
    const [pinned, baked] = mac.entries;
    expect(isPinned(mac, pinned)).toBe(true);
    expect(isPinned(mac, baked)).toBe(false);
    expect(togglePin(mac, pinned)).toEqual([]);
    const vae = mac.entries.find((e) => e.r.component === "vae" && e.env === "torch")!;
    expect(togglePin(mac, vae)).toEqual([
      ...mac.settings!.pins,
      {
        env: "torch",
        component: "vae",
        model: "qwen-image-2.1",
        variant: "base",
        precision: "bf16",
      },
    ]);
  });
  it("cannot name a redacted baked set, names one an admin sees", () => {
    const [, baked] = mac.entries;
    expect(pinRef(baked)).toBeNull();
    const named = {
      ...baked,
      r: { ...baked.r, loras: [{ lora_id: "a".repeat(64), name: null, strength: 0.8, bytes: 1 }] },
    };
    expect(pinRef(named)?.loras).toEqual([{ lora_id: "a".repeat(64), strength: 0.8 }]);
  });
  it("starts from the runner's pins before any settings exist", () => {
    const bare = { ...mac, settings: null };
    expect(isPinned(bare, mac.entries[0])).toBe(true);
    expect(togglePin(bare, mac.entries[0])).toEqual([]);
    expect(settingsOf(bare)).toEqual({
      after_run: "keep_warm",
      idle_unload_min: 10,
      reserve_gb: 12,
    });
    expect(settingsOf({ ...bare, policy: null })).toEqual({
      after_run: "keep_warm",
      idle_unload_min: 10,
      reserve_gb: 0,
    });
  });
  it("words components and times", () => {
    expect(componentText("text_encoder")).toBe("text encoder");
    expect(componentText("vae")).toBe("VAE");
    const now = Date.parse("2026-11-15T10:11:04Z");
    expect(ago("2026-11-15T10:11:02Z", now)).toBe("2 s ago");
    expect(ago("2026-11-15T10:02:00Z", now)).toBe("9 min ago");
    expect(ago("2026-11-13T10:00:00Z", now)).toBe("2 d ago");
  });
});

import two from "../../contracts/fixtures/http/HostList.valid.3.json";
import { deviceLine } from "@/lib/hosts";

describe("two hosts, one with a discrete GPU", () => {
  const all = two.hosts as S<"Host">[];
  const [gcp, mac] = hostCards(all);
  it("lists both hosts by name, the CUDA one with its VRAM budget", () => {
    expect([gcp.host, mac.host]).toEqual(["gcp", "mac"]);
    expect([gcp.used_gb, gcp.budget_gb]).toEqual([8.1, 93.6]);
    expect(machineText(gcp.machine)).toBe("Linux · CUDA · container");
    expect(deviceLine(gcp)).toBe("NVIDIA RTX PRO 6000 Blackwell Server Edition · 95.6 GB");
    expect(deviceLine(mac)).toBe("");
    expect(deviceLine({ machine: { ...gcp.machine!, os: "macos" }, device: undefined })).toBe(
      "Apple Silicon · 176 GB",
    );
  });
  it("leaves a component held in host RAM out of the memory bar", () => {
    expect(gcp.entries).toHaveLength(2);
    expect([gcp.components_gb, gcp.cached_gb]).toEqual([6.8, 1.3]);
  });
  it("puts the memory chip on the run's host when the env is on several", () => {
    expect(hostMemory(all, "torch")!.host).toBe("mac");
    const m = hostMemory(all, "torch", "gcp")!;
    expect([m.host, m.used_gb, m.budget_gb, m.envs.length]).toEqual(["gcp", 8.1, 93.6, 1]);
    expect(hostMemory(all, "torch", "gone")!.host).toBe("mac");
  });
});

describe("queuedCount", () => {
  it("adds the caller's queued runs and the others' counts (the Hosts summary's · N queued)", () => {
    expect(queuedCount(queue as S<"Queue">)).toBe(3);
  });
});
