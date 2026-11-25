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
  const miss = needs.find((n) => lacks(n, mc));
  // a node's add-on reads as a thing ("enhancer not downloaded on gcp"), a value's as the option's name ("Turbo LoRA
  // not downloaded on gcp", docs/mock/H3Options.dc.html frame 12)
  if (miss)
    return `${"field" in miss ? miss.title : miss.title[0].toLowerCase() + miss.title.slice(1)} not downloaded on ${mc.host}`;
  return null;
}

/** One optional add-on a node of the workflow loads (its NodeSchema `addons`, API 1.36; contracts/README.md
 * Optional add-ons): its title, the live hosts it is present on, and (API 1.40) the hosts that list the family's model
 * but not it (`free`: they need none, as NVIDIA for LTX's), the variants whose runs need it and whether the workflow
 * runs its node unbypassed (`always`: ltx/distill). */
export type AddonNeed = {
  node: string;
  family: string;
  component: string;
  title: string;
  hosts: string[];
  free: string[];
  variants?: string[];
  /** API 1.40: the task that needs it (true: a run with references, false: one without); absent = both */
  refs?: boolean;
  always: boolean;
};

/** Whether the add-on is missing on `mc` (it lists it, not present). */
const lacks = (n: AddonNeed, mc: Machine) =>
  !n.hosts.includes(mc.host) && !n.free.includes(mc.host);

/** Whether a run with the form's chosen variants, with references or not, needs the add-on (API 1.40: its
 * `variants`, absent = every one; its `refs`, absent = both: Qwen's PE-T2I is text to image's, PE-I2I an edit's). */
export function needed(n: AddonNeed, picks: { m: Model; v: Variant }[], refs = false): boolean {
  const pick = picks.find((p) => p.m.family === n.family);
  return (
    (!n.variants || !pick || n.variants.includes(pick.v.id)) && (n.refs == null || n.refs === refs)
  );
}

/** The workflow's add-on needs, graph order then the schema's: from the models of each node's family (a family the
 * list does not hold, or no list yet, needs nothing it can tell). Titles as the list has them (API 1.40: its `short`,
 * "Enhancer"), else the component. */
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
      const listed = new Set(as.flatMap((a) => a.hosts.map((h) => h.host)));
      const free = ms.flatMap((m) => m.variants.flatMap((v) => v.hosts.map((h) => h.host)));
      return {
        node,
        family: s.family ?? "",
        component,
        title: as[0]?.short ?? as[0]?.title ?? component,
        hosts: [...new Set(hosts)],
        free: as.length ? [...new Set(free)].filter((h) => !listed.has(h)) : [],
        ...(as[0]?.variants && { variants: as[0].variants }),
        ...(as[0]?.refs != null && { refs: as[0].refs }),
        always: w.nodes[node].bypass !== true,
      };
    });
  });
}

/** An add-on one value of an enum param asks for (API 1.41, ParamSchema `addons`; contracts/README.md Optional
 * add-ons: an add-on per value): the field `node.param`, the value, and `with`, the `addons_with` name (an optional
 * input, or a rows param) whose wiring swaps it in (MiniMax H3's lora turbo: Turbo LoRA; with a reference Turbo Ref
 * LoRA). */
export type ValueNeed = AddonNeed & { field: string; value: string; with?: string };

/** Every add-on the workflow's enum params ask for by value, graph order then the schema's (as addonNeeds). */
export function valueNeeds(
  w: Pick<S<"Workflow">, "nodes" | "node_schemas"> | undefined,
  list: S<"ModelList"> | null,
): ValueNeed[] {
  if (!w || !list) return [];
  const live = new Set(list.hosts.filter((h) => h.live).map((h) => h.host));
  return graphOrder(w).flatMap((node) => {
    const s = w.node_schemas[w.nodes[node].kind];
    const ms = list.models.filter((m) => m.family === s?.family);
    if (!ms.length) return [];
    return s.params.flatMap((p) => {
      const pairs = [
        ...Object.entries(p.addons ?? {}).flatMap(([value, cs]) =>
          cs.map((c) => ({ value, component: c, with: undefined as string | undefined })),
        ),
        ...Object.entries(p.addons_with ?? {}).flatMap(([wi, by]) =>
          Object.entries(by).flatMap(([value, cs]) =>
            cs.map((c) => ({ value, component: c, with: wi })),
          ),
        ),
      ];
      return pairs.map(({ value, component, with: wi }) => {
        const as = ms.flatMap((m) => (m.addons ?? []).filter((a) => a.component === component));
        const listed = new Set(as.flatMap((a) => a.hosts.map((h) => h.host)));
        const free = ms.flatMap((m) => m.variants.flatMap((v) => v.hosts.map((h) => h.host)));
        return {
          node,
          family: s.family ?? "",
          component,
          title: as[0]?.short ?? as[0]?.title ?? component,
          hosts: [
            ...new Set(
              as.flatMap((a) =>
                a.hosts.filter((h) => h.state === "present" && live.has(h.host)).map((h) => h.host),
              ),
            ),
          ],
          free: as.length ? [...new Set(free)].filter((h) => !listed.has(h)) : [],
          ...(as[0]?.variants && { variants: as[0].variants }),
          always: w.nodes[node].bypass !== true,
          field: `${node}.${p.name}`,
          value,
          ...(wi && { with: wi }),
        };
      });
    });
  });
}

/** The add-ons `field` asks for at value `o` now: those of its `addons_with` names that are wired (`wired(name)`),
 * else its plain `addons`. */
export function valueNeedsAt(
  needs: ValueNeed[],
  field: string,
  o: string,
  wired: (name: string) => boolean,
): ValueNeed[] {
  const mine = needs.filter((n) => n.field === field);
  const swapped = mine.some((n) => n.with != null && wired(n.with));
  return mine.filter(
    (n) => n.value === o && (swapped ? n.with != null && wired(n.with) : n.with == null),
  );
}

/** Why value `o` of `field` cannot run now (API 1.41), else null: an add-on it asks for is scoped to other variants
 * than the chosen one (`picks`; MiniMax H3's Turbo LoRA is Base's, SGLang on Base fp8 takes none: "Turbo LoRA does
 * not run on Base fp8"); under Run on `mc` one is missing there ("Turbo LoRA not downloaded on gcp"); under Auto on
 * every machine that could take the run (`can`; none known: every live host), "Turbo LoRA not downloaded". */
export function valueBlock(
  needs: ValueNeed[],
  field: string,
  o: string,
  wired: (name: string) => boolean,
  can: Machine[],
  mc?: Machine,
  picks: { m: Model; v: Variant }[] = [],
): string | null {
  const at = valueNeedsAt(needs, field, o, wired);
  for (const x of at) {
    const pick = picks.find((p) => p.m.family === x.family);
    if (x.variants && pick && !x.variants.includes(pick.v.id))
      return `${x.title} does not run on ${pick.v.title}`;
  }
  const n = at.find((x) =>
    mc ? lacks(x, mc) : can.length ? can.every((m) => lacks(x, m)) : !x.hosts.length,
  );
  return n ? `${n.title} not downloaded${mc ? ` on ${mc.host}` : ""}` : null;
}

/** The nodes whose add-on is missing where the run may go, each with why its controls are greyed: under Auto on
 * every live host ("Enhancer not downloaded"), under Run on `mc` there ("Enhancer not downloaded on gcp"). */
export function addonBlocks(needs: AddonNeed[], mc?: Machine): Record<string, string> {
  const out: Record<string, string> = {};
  for (const n of needs)
    if (!(n.node in out) && !(n.always && n.variants) && (mc ? lacks(n, mc) : !n.hosts.length))
      out[n.node] = `${n.title} not downloaded${mc ? ` on ${mc.host}` : ""}`;
  return out;
}

/** Why the version row `o` of `family` is greyed (API 1.40): a node the workflow always runs needs an add-on scoped
 * to it that is missing where the run may go ("Distilled LoRA not downloaded", board LtxFeatures frame 17); null when
 * it is pickable. */
export function variantBlock(
  needs: AddonNeed[],
  family: string,
  o: string,
  mc?: Machine,
): string | null {
  const n = needs.find(
    (x) =>
      x.family === family &&
      x.always &&
      x.variants?.includes(o) &&
      (mc ? lacks(x, mc) : !x.hosts.length),
  );
  return n ? `${n.title} not downloaded${mc ? ` on ${mc.host}` : ""}` : null;
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

/** A version menu row for value `o` of a variant field: its title, its line (API 1.39 `help`, "8 steps, about 10×
 * faster"), its note ("NVIDIA only") and, under Run on `mc`, the dim text; null when the models list does not know it. */
export function versionRow(
  list: S<"ModelList"> | null,
  mc: Machine | undefined,
  f: Field,
  o: string,
  needs: AddonNeed[] = [],
): {
  title: string;
  sub?: string;
  note: string | null;
  dim: string | null;
  why: string | null;
} | null {
  for (const m of list?.models.filter((x) => x.family === f.family) ?? []) {
    const v = m.variants.find((x) => x.id === o);
    if (v)
      return {
        title: v.title,
        sub: v.help,
        note: deviceNote(m, v),
        dim: versionDim(mc, m, v),
        why: variantBlock(needs, f.family ?? "", o, mc),
      };
  }
  return null;
}
