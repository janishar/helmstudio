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

// ---- Admin › Hosts (docs/mock/Hosts.dc.html; contracts/README.md "Hosts admin")

type Resident = NonNullable<Host["resident"]>[number];
type PinRef = S<"PinRef">;
type AfterRun = S<"AfterRun">;

/** The after-run choices, in the board's order and words. */
export const AFTER_RUN: readonly (readonly [AfterRun, string])[] = [
  ["unload", "Unload everything"],
  ["keep_warm", "Keep warm"],
  ["keep_pinned", "Keep pinned only"],
];

/** The card's tag: "After run: keep warm". */
export const afterRunTag = (a: AfterRun) =>
  `After run: ${AFTER_RUN.find(([k]) => k === a)![1].toLowerCase()}`;

/** "Apple Silicon · Metal · native", "Linux · CUDA · container". */
export function machineText(m: Host["machine"]): string {
  if (!m) return "";
  const os = m.os === "macos" ? "Apple Silicon" : "Linux";
  const accel = { metal: "Metal", cuda: "CUDA", cpu: "CPU" }[m.accel];
  return `${os} · ${accel} · ${m.packaging}`;
}

/** One resident entry and the env that holds it. */
export type HostEntry = { env: string; r: Resident };

/** One host of the Hosts screen: its envs (the registry's per-env keys) folded together. */
export type HostCard = {
  host: string;
  envs: Host[];
  state: "running" | "idle" | "offline";
  used_gb: number;
  budget_gb: number;
  components_gb: number;
  cached_gb: number;
  machine: Host["machine"];
  admin: S<"HostAdmin"> | undefined;
  /** the settings (the same on every env) and the policy in effect (the first env that reports one) */
  settings: S<"HostSettings"> | null;
  policy: S<"HostAdmin">["policy"];
  run: (NonNullable<S<"HostAdmin">["run"]> & { env: string }) | null;
  entries: HostEntry[];
  /** the newest key's refresh */
  heartbeat: string;
};

const round1 = (n: number) => Math.round(n * 10) / 10;

/** The hosts list folded per host, by name. */
export function hostCards(hosts: Host[]): HostCard[] {
  const by = new Map<string, Host[]>();
  for (const h of hosts) by.set(h.host, [...(by.get(h.host) ?? []), h]);
  return [...by.keys()].sort().map((host) => {
    // a local server's `server` env holds no weights: not shown beside runners (contracts "Hosts admin")
    const all = by.get(host)!.toSorted((a, b) => a.env.localeCompare(b.env));
    const runners = all.filter((e) => e.env !== "server");
    const envs = runners.length ? runners : all;
    const live = envs.filter((e) => e.state !== "offline");
    const entries = envs.flatMap((e) => (e.resident ?? []).map((r) => ({ env: e.env, r })));
    const components = entries.reduce((s, x) => s + x.r.gb, 0);
    const cached = envs.reduce((s, e) => s + (e.cached_gb ?? 0), 0);
    const mem = envs.find((e) => e.host_memory)?.host_memory;
    const budgets = envs.map((e) => e.memory?.budget_gb).filter((b) => b != null);
    const admin = envs.find((e) => e.admin)?.admin;
    const busy = envs.find((e) => e.admin?.run);
    return {
      host,
      envs,
      state: !live.length ? "offline" : live.some((e) => e.state === "busy") ? "running" : "idle",
      used_gb: round1(mem?.used_gb ?? components + cached),
      budget_gb: mem?.budget_gb ?? (budgets.length ? Math.min(...budgets) : 0),
      components_gb: round1(components),
      cached_gb: round1(cached),
      machine: envs.find((e) => e.machine)?.machine,
      admin,
      settings: admin?.settings ?? null,
      policy: envs.find((e) => e.admin?.policy)?.admin?.policy ?? null,
      run: busy ? { ...busy.admin!.run!, env: busy.env } : null,
      entries,
      heartbeat: envs.map((e) => e.updated_at).reduce((a, b) => (a > b ? a : b)),
    };
  });
}

/** The pin (and unload) identity of a resident entry; null when a baked LoRA cannot be named (redacted). */
export function pinRef({ env, r }: HostEntry): PinRef | null {
  const ref: PinRef = {
    env,
    component: r.component,
    model: r.model,
    variant: r.variant,
    precision: r.precision,
  };
  if (!r.loras?.length) return ref;
  if (r.loras.some((l) => l.lora_id == null || l.strength == null)) return null;
  return { ...ref, loras: r.loras.map((l) => ({ lora_id: l.lora_id!, strength: l.strength! })) };
}

const key = (p: PinRef) =>
  JSON.stringify([
    p.env,
    p.component,
    p.model,
    p.variant,
    p.precision,
    (p.loras ?? []).map((l) => [l.lora_id, l.strength]),
  ]);

/** The pin is set: in the host's settings, else (no settings yet) as the runner reports it. */
export function isPinned(card: HostCard, e: HostEntry): boolean {
  const ref = pinRef(e);
  if (card.settings) return !!ref && card.settings.pins.some((p) => key(p) === key(ref));
  return e.r.pinned_why === "pin";
}

/** The host's pins with this entry's pin switched; without settings yet, the runner's pinned entries are the start. */
export function togglePin(card: HostCard, e: HostEntry): PinRef[] {
  const ref = pinRef(e)!;
  const pins =
    card.settings?.pins ??
    card.entries
      .filter((x) => x.r.pinned_why === "pin")
      .map(pinRef)
      .filter((p) => p !== null);
  return pins.some((p) => key(p) === key(ref))
    ? pins.filter((p) => key(p) !== key(ref))
    : [...pins, ref];
}

/** The settings form's starting values: the stored settings, else the policy in effect, else the defaults. */
export function settingsOf(card: HostCard): S<"HostSettingsPut"> {
  const s = card.settings ?? card.policy;
  return {
    after_run: s?.after_run ?? "keep_warm",
    idle_unload_min: s?.idle_unload_min ?? 10,
    reserve_gb: s?.reserve_gb ?? 0,
  };
}

/** "transformer", "text encoder", "VAE". */
export const componentText = (c: string) => (c === "vae" ? "VAE" : c.replaceAll("_", " "));

/** "2 s ago", "4 min ago", "3 h ago", "2 d ago" from `iso` to `now`. */
export function ago(iso: string | null | undefined, now: number): string {
  if (!iso) return "";
  return `${duration(now - Date.parse(iso))} ago`;
}

/** "42 s", "4 min", "3 h", "2 d". */
export function duration(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000));
  if (s < 60) return `${s} s`;
  if (s < 3600) return `${Math.floor(s / 60)} min`;
  if (s < 86400) return `${Math.floor(s / 3600)} h`;
  return `${Math.floor(s / 86400)} d`;
}
