// The Models screen's logic (docs/mock/Models.dc.html, ModelDownload.dc.html; contracts/README.md "Models and model
// jobs"): what a variant shows per host, its tags and sizes, and what a download must ask before it starts.
import type { S } from "@/lib/api";
import type { Field } from "@/lib/workflow";

type Model = S<"Model">;
type Variant = S<"ModelVariant">;
type VariantHost = S<"ModelVariantHost">;

/** "57.8 GB" (decimal, as the board), "640 MB" below a gigabyte; "" for null. */
export function bytesText(n: number | null | undefined): string {
  if (n == null) return "";
  return n >= 1e9 ? `${(n / 1e9).toFixed(1)} GB` : `${Math.max(1, Math.round(n / 1e6))} MB`;
}

/** A download job's progress as a whole percent, or null while its size is not known yet. */
export function percent(
  job: { done_bytes: number; total_bytes: number | null } | null,
): number | null {
  if (!job?.total_bytes) return null;
  return Math.min(100, Math.floor((job.done_bytes / job.total_bytes) * 100));
}

/** The word the screen shows for a variant on one host: "present", "downloading · 62%", "downloading · queued",
 * "partial", "missing", "hash mismatch · 2 files". */
export function stateText(h: VariantHost): string {
  switch (h.state) {
    case "downloading": {
      const p = percent(h.job);
      return `downloading · ${p == null ? (h.job?.state === "running" ? "starting" : "queued") : `${p}%`}`;
    }
    case "mismatch": {
      const n = h.mismatch?.length ?? 0;
      return `hash mismatch${n ? ` · ${n} file${n === 1 ? "" : "s"}` : ""}`;
    }
    default:
      return h.state;
  }
}

const QUANTIZED = ["int8", "int4", "fp8"];

/** The precision tags (contracts/README.md, Quantized variants): the stored dtype, then the other precisions made
 * at load ("int8 at load"), or "stored quantized" when it loads only at its stored quantized dtype. Without `stored`
 * the first precision stands for it. */
export function precisionTags(
  p: readonly string[],
  stored?: string | null,
): { text: string; q: boolean; dim?: boolean }[] {
  const s = stored ?? p[0];
  const rest = p.filter((x) => x !== s);
  if (rest.length)
    return [
      { text: s, q: false },
      { text: `${rest.join(" · ")} at load`, q: true },
    ];
  return QUANTIZED.includes(s)
    ? [
        { text: s, q: false },
        { text: "stored quantized", q: false, dim: true },
      ]
    : [{ text: s, q: false }];
}

/** A variant's stored dtype: for a variant of one precision, a component stored at that precision (the fp8 DiT
 * beside a larger int8 text encoder); else that of its largest component that reports one; else undefined. */
export function variantStored(v: Variant): string | undefined {
  const known = v.components
    .filter((c) => c.stored)
    .sort((a, b) => (b.bytes ?? 0) - (a.bytes ?? 0));
  const one = v.precisions.length === 1 ? v.precisions[0] : undefined;
  return (known.find((c) => c.stored === one) ?? known[0])?.stored;
}

/** Where a variant runs: its `devices`, else its model's runtimes' (an mlx or native one without devices: mps). */
export function variantDevices(m: Model, v: Variant): string[] {
  return (
    v.devices ??
    m.runtimes.flatMap(
      (r) => r.devices ?? (MAC_RUNTIMES.includes(r.runtime) ? ["mps"] : ["mps", "cuda"]),
    )
  );
}

/** The variant's note: "Mac only", "NVIDIA only", or null when it runs on both. */
export function deviceNote(m: Model, v: Variant): "Mac only" | "NVIDIA only" | null {
  const d = variantDevices(m, v);
  const mac = d.includes("mps");
  const nv = d.includes("cuda");
  return mac && !nv ? "Mac only" : nv && !mac ? "NVIDIA only" : null;
}

/** "shares 2 with Base": the components this variant loads from another variant's folder (one per owner). */
export function sharesText(m: Model, v: Variant): string[] {
  const by = new Map<string, number>();
  for (const c of v.components)
    if (c.from_variant !== v.id) by.set(c.from_variant, (by.get(c.from_variant) ?? 0) + 1);
  return [...by].map(([id, n]) => `shares ${n} with ${titleOf(m, id)}`);
}

/** A variant's title by id (the id when the model does not list it). */
export const titleOf = (m: Model, id: string) => m.variants.find((v) => v.id === id)?.title ?? id;

/** "image", "video + audio". */
export const mediaText = (m: Model) => m.media.join(" + ");

const RUNTIME_LABELS: Record<string, string> = { mlx: "MLX", native: "Native" };

/** The Apple-silicon-only runtimes (API 1.21: native runs a Metal binary as a child process). */
export const MAC_RUNTIMES = ["mlx", "native"];

/** The runtimes as the board names them ("torch", "MLX", "Native"), each once. */
export const runtimeTags = (m: Model) => [
  ...new Set(m.runtimes.map((r) => RUNTIME_LABELS[r.runtime] ?? r.runtime)),
];

/** The size the variant card shows: the expected download, else its files on the host (or the largest seen). */
export function variantBytes(v: Variant, host?: string): number | null {
  if (v.download_bytes) return v.download_bytes;
  const on = v.hosts.filter((h) => !host || h.host === host).map((h) => h.bytes ?? 0);
  return Math.max(0, ...on) || null;
}

/** The variant's folder on a host: "~/GenAI/models/Qwen-Image-2.1-Turbo" ("" without a root or dir). */
export function folderOf(list: S<"ModelList">, v: Variant, host: string | undefined): string {
  const root = list.hosts.find((h) => h.host === host)?.models_root;
  return root && v.dir ? `${root.replace(/\/$/, "")}/${v.dir}` : (v.dir ?? "");
}

/** The models with only the chosen host's presence ("" = all hosts); a variant no longer on any host stays listed. */
export function forHost(models: Model[], host: string): Model[] {
  if (!host) return models;
  return models.map((m) => ({
    ...m,
    variants: m.variants.map((v) => ({ ...v, hosts: v.hosts.filter((h) => h.host === host) })),
  }));
}

/** True while any variant is downloading somewhere (the screen then polls fast). */
export const anyDownloading = (models: Model[]) =>
  models.some((m) => m.variants.some((v) => v.hosts.some((h) => h.state === "downloading")));

export type DownloadPlan = {
  /** the licence to show, or null when the variant names none */
  licence: S<"Licence"> | null;
  /** an explicit accept is required before Download */
  mustAccept: boolean;
  /** the gated note, when the repo needs a token the host does not have */
  gatedNote: string | null;
  /** why it cannot be downloaded to that host at all (the button is disabled with this) */
  blocked: string | null;
};

/** What the Download dialog asks for a variant and target host. The server checks it all again. */
export function downloadPlan(list: S<"ModelList">, v: Variant, host: string): DownloadPlan {
  const h = list.hosts.find((x) => x.host === host);
  const on = v.hosts.find((x) => x.host === host);
  const licence = v.licence ? (list.licences.find((l) => l.id === v.licence!.id) ?? null) : null;
  const blocked = !v.licence
    ? "no download source"
    : !on
      ? `host ${host} has no env for this model`
      : on.state === "present"
        ? `already on ${host}`
        : on.state === "downloading"
          ? `downloading to ${host}`
          : null;
  return {
    licence,
    mustAccept: !!v.licence?.must_accept,
    gatedNote:
      v.gated && h && !h.hub_token
        ? `The repo needs a Hub token: put it in this host's secrets file (RUNNER_HF_TOKEN_FILE). ${host} has none.`
        : null,
    blocked,
  };
}

/** Whether the job a "queued" notice names is still open (or not in the list yet): the notice goes when it ends. */
export function jobOpen(jobs: S<"ModelJob">[], id: string): boolean {
  const j = jobs.find((x) => x.id === id);
  return !j || j.state === "queued" || j.state === "running";
}

/** Delete is offered when the variant has files on that host and no download of it is running there. */
export function canDelete(v: Variant, host: string): boolean {
  const s = v.hosts.find((x) => x.host === host)?.state;
  return s === "present" || s === "partial" || s === "mismatch";
}

/** Create's model node pins (API 1.33 RunsAt): each precision field whose node's chosen variant loads at exactly
 * one precision → that precision (shown fixed and sent). `raws` is what the form holds; a field it lacks keeps the
 * workflow's value. */
export function precisionPins(
  fields: readonly Field[],
  raws: Record<string, unknown>,
  list: S<"ModelList"> | null,
): Record<string, string> {
  const pins: Record<string, string> = {};
  if (!list) return pins;
  for (const f of fields) {
    if (!f.model || f.param.name !== "precision") continue;
    const vf = fields.find((x) => x.node === f.node && x.param.name === "variant");
    const vid = String((vf && raws[vf.key]) ?? vf?.initial ?? f.siblings?.variant ?? "");
    const v = list.models
      .filter((m) => m.family === f.family)
      .flatMap((m) => m.variants)
      .find((x) => x.id === vid);
    const p = v?.precisions.length === 1 ? v.precisions[0] : undefined;
    if (p && f.param.enum?.includes(p)) pins[f.key] = p;
  }
  return pins;
}

/** Whether some model of the family has a variant present on a live host (Create greys a workflow without one:
 * "Not downloaded"). True while the list is not known, for a core workflow, or a family the list does not hold. */
export function downloaded(
  list: S<"ModelList"> | null,
  family: string | null | undefined,
): boolean {
  const ms = list?.models.filter((m) => m.family === family) ?? [];
  if (!ms.length) return true;
  const live = new Set(list!.hosts.filter((h) => h.live).map((h) => h.host));
  return ms.some((m) =>
    m.variants.some((v) => v.hosts.some((h) => h.state === "present" && live.has(h.host))),
  );
}
