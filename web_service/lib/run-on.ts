// Create's Run on (docs/mock/RunOn.dc.html; contracts/README.md "Several hosts", A run set to one host): the
// machines a run may be set to, why one cannot take it, and what a version needs. Everything comes from what the
// hosts publish (GET /v1/studio/hosts: online, device; GET /v1/studio/models: variants, devices, presence).
import type { S } from "@/lib/api";
import { deviceNote, variantDevices } from "@/lib/models";
import { graphOrder, type Field } from "@/lib/workflow";

type Host = S<"Host">;
type Model = S<"Model">;
type Variant = S<"ModelVariant">;

/** One Run on row: a host, named by its device. */
export type Machine = {
  host: string;
  /** "Mac" (mps), "NVIDIA" (cuda); the host name when two hosts share the device or it is neither */
  label: string;
  /** "mac · Apple M5 Pro" */
  sub: string;
  device: string | null;
  live: boolean;
  /** its envs with a live hosts key (as studio's eligibility: an offline runner cannot take the run) */
  envs: string[];
};

const LABELS: Record<string, string> = { mps: "Mac", cuda: "NVIDIA" };
const ACCEL: Record<string, string> = { metal: "mps", cuda: "cuda", cpu: "cpu" };

/** Every connected host (a key in GET /hosts, its envs folded; a local server's `server` env alone is no machine):
 * Mac hosts, then NVIDIA ones, then the rest, each by name. */
export function machines(hosts: Host[] | null | undefined): Machine[] {
  const by = new Map<string, Host[]>();
  for (const h of hosts ?? [])
    if (h.env !== "server") by.set(h.host, [...(by.get(h.host) ?? []), h]);
  const rows = [...by.keys()].sort().map((host) => {
    const envs = by.get(host)!;
    // a GPU's own device (it has a driver) first: a CUDA host's native env names its CPU ("AMD EPYC 9B45")
    const dev = (envs.find((e) => e.device?.driver) ?? envs.find((e) => e.device))?.device;
    const accel = envs.find((e) => e.machine)?.machine?.accel;
    return {
      host,
      label: host,
      // the short GPU name, as the board: "NVIDIA RTX PRO 6000 Blackwell Server Edition" → "RTX PRO 6000"
      // ponytail: a fixed list of architecture words; add one when a new GPU line shows its long name
      sub: [
        host,
        dev?.name?.replace(/^NVIDIA /, "").replace(/ (Blackwell|Hopper|Ada|Ampere)\b.*$/, ""),
      ]
        .filter(Boolean)
        .join(" · "),
      device: dev?.kind ?? (accel ? ACCEL[accel] : null) ?? null,
      live: envs.some((e) => e.state !== "offline"),
      envs: envs.filter((e) => e.state !== "offline").map((e) => e.env),
    };
  });
  for (const r of rows) {
    const named = r.device ? LABELS[r.device] : undefined;
    if (named && rows.filter((x) => x.device === r.device).length === 1) r.label = named;
  }
  // Mac, then NVIDIA, then the rest (the board's order), each by name
  const rank = (r: Machine) => (r.device === "mps" ? 0 : r.device === "cuda" ? 1 : 2);
  return rows.sort((a, b) => rank(a) - rank(b));
}

/** The model variants the form has chosen: each model node's variant field (its value, else the workflow's) with
 * its model in the list. */
export function chosenVariants(
  fields: readonly Field[],
  raws: Record<string, unknown>,
  list: S<"ModelList"> | null,
): { m: Model; v: Variant }[] {
  if (!list) return [];
  return fields
    .filter((f) => f.model && f.param.name === "variant")
    .flatMap((f) => {
      const id = String(raws[f.key] ?? f.initial ?? "");
      for (const m of list.models.filter((x) => x.family === f.family)) {
        const v = m.variants.find((x) => x.id === id);
        if (v) return [{ m, v }];
      }
      return [];
    });
}

/** Why `mc` cannot run variant v: its device is not one v runs on ("Base fp8 is NVIDIA only"), or v is not on its
 * disk ("not downloaded on gcp"); null when it can. */
export function misfit(
  mc: Machine,
  m: Model,
  v: Variant,
): { device: boolean; text: string } | null {
  // only the devices a variant's note speaks of (a CPU runner, the fake one, is decided by what it has on disk)
  if (mc.device && LABELS[mc.device] && !variantDevices(m, v).includes(mc.device))
    return { device: true, text: `${v.title} is ${deviceNote(m, v) ?? `not on ${mc.label}`}` };
  if (v.hosts.find((h) => h.host === mc.host)?.state !== "present")
    return { device: false, text: `not downloaded on ${mc.host}` };
  return null;
}

/** Why the Run on row of `mc` is greyed for the form's workflow (env), chosen variants and the add-ons its nodes
 * that run need: "offline", a version's device, "not downloaded on <host>", "enhancer not downloaded on gcp"; null =
 * pickable. */
export function machineReason(
  mc: Machine,
  env: string | undefined,
  picks: { m: Model; v: Variant }[],
  needs: AddonNeed[] = [],
): string | null {
  if (!mc.live) return "offline";
  if (env && !mc.envs.includes(env)) return "cannot run this workflow";
  for (const { m, v } of picks) {
    const why = misfit(mc, m, v);
    if (why) return why.text;
  }
  const miss = needs.find((n) => !n.hosts.includes(mc.host));
  if (miss)
    return `${miss.title[0].toLowerCase()}${miss.title.slice(1)} not downloaded on ${mc.host}`;
  return null;
}

/** One optional add-on a node of the workflow loads (its NodeSchema `addons`, API 1.36; contracts/README.md
 * Optional add-ons): its title and the live hosts it is present on. */
export type AddonNeed = { node: string; component: string; title: string; hosts: string[] };

/** The workflow's add-on needs, graph order then the schema's: from the models of each node's family (a family the
 * list does not hold, or no list yet, needs nothing it can tell). Titles as the list has them, else the component. */
export function addonNeeds(
  w: Pick<S<"Workflow">, "nodes" | "node_schemas"> | undefined,
  list: S<"ModelList"> | null,
): AddonNeed[] {
  if (!w || !list) return [];
  const live = new Set(list.hosts.filter((h) => h.live).map((h) => h.host));
  return graphOrder(w).flatMap((node) => {
    const s = w.node_schemas[w.nodes[node].kind];
    const ms = list.models.filter((m) => m.family === s?.family);
    if (!ms.length) return [];
    return (s.addons ?? []).map((component) => {
      const as = ms.flatMap((m) => (m.addons ?? []).filter((a) => a.component === component));
      const hosts = as.flatMap((a) =>
        a.hosts.filter((h) => h.state === "present" && live.has(h.host)).map((h) => h.host),
      );
      return { node, component, title: as[0]?.title ?? component, hosts: [...new Set(hosts)] };
    });
  });
}

/** The nodes whose add-on is missing where the run may go, each with why its controls are greyed: under Auto on
 * every live host ("Enhancer not downloaded"), under Run on `mc` there ("Enhancer not downloaded on gcp"). */
export function addonBlocks(needs: AddonNeed[], mc?: Machine): Record<string, string> {
  const out: Record<string, string> = {};
  for (const n of needs)
    if (!(n.node in out) && (mc ? !n.hosts.includes(mc.host) : !n.hosts.length))
      out[n.node] = `${n.title} not downloaded${mc ? ` on ${mc.host}` : ""}`;
  return out;
}

/** A version row's dim text under Run on `mc` ("not on Mac"), null when it runs there or under Auto. */
export function versionDim(mc: Machine | undefined, m: Model, v: Variant): string | null {
  return mc && misfit(mc, m, v) ? `not on ${mc.label}` : null;
}

/** The note when picking v sets Run on back to Auto: "Run on set to Auto: Base fp8 is NVIDIA only". */
export function clashNote(mc: Machine, m: Model, v: Variant): string | null {
  const why = misfit(mc, m, v);
  if (!why) return null;
  return `Run on set to Auto: ${why.device ? why.text : `${v.title} is ${why.text}`}`;
}

/** The machine Run on is set to ("" = Auto: undefined); a host no longer connected stays chosen, offline. */
export function pinnedMachine(list: Machine[], host: string): Machine | undefined {
  if (!host) return undefined;
  return (
    list.find((x) => x.host === host) ?? {
      host,
      label: host,
      sub: host,
      device: null,
      live: false,
      envs: [],
    }
  );
}

/** A version menu row for value `o` of a variant field: its title, its note ("NVIDIA only") and, under Run on
 * `mc`, the dim text; null when the models list does not know it. */
export function versionRow(
  list: S<"ModelList"> | null,
  mc: Machine | undefined,
  f: Field,
  o: string,
): { title: string; note: string | null; dim: string | null } | null {
  for (const m of list?.models.filter((x) => x.family === f.family) ?? []) {
    const v = m.variants.find((x) => x.id === o);
    if (v) return { title: v.title, note: deviceNote(m, v), dim: versionDim(mc, m, v) };
  }
  return null;
}
