// The memory chip's data (contracts/README.md "Two envs on one host: the hand-over", Hosts data and the memory
// chip): the host a workflow's env runs on, its shared budget across envs, busy when any env of it is.
import type { S } from "@/lib/api";

type Host = S<"Host">;

export type HostMemory = {
  host: string;
  used_gb: number;
  budget_gb: number;
  busy: boolean;
  envs: Host[];
};

/** The live host of `env` with memory, as one chip: `host_memory` (else the env's own `memory`) and its envs. */
export function hostMemory(
  hosts: Host[] | null | undefined,
  env: string | undefined,
): HostMemory | null {
  const h = hosts?.find(
    (x) => x.env === env && x.state !== "offline" && (x.host_memory ?? x.memory),
  );
  const m = h && (h.host_memory ?? h.memory);
  if (!h || !m) return null;
  const envs = hosts!.filter((x) => x.host === h.host);
  return {
    host: h.host,
    used_gb: m.used_gb,
    budget_gb: m.budget_gb,
    busy: envs.some((x) => x.state === "busy"),
    envs: h.host_memory ? envs : [h],
  };
}
