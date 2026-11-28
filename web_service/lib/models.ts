// The Models screen's logic (docs/mock/Models.dc.html, ModelDownload.dc.html; contracts/README.md "Models and model
// jobs"): what a variant shows per host, its tags and sizes, and what a download must ask before it starts.
import type { S } from "@/lib/api";
import type { Field } from "@/lib/workflow";

type Model = S<"Model">;
type Variant = S<"ModelVariant">;
type VariantHost = S<"ModelVariantHost">;
/** What a download, a delete and a folder read of a variant or an optional add-on (API 1.36) */
type Part = Pick<Variant, "licence" | "gated" | "dir" | "hosts">;

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

/** The variant's folder on a host: "~/GenAI/models/Qwen-Image-2.1-Turbo" ("" without a root or dir); in the folder its
 * runs load it from (API 1.78), else the downloads folder. */
export function folderOf(
  list: S<"ModelList">,
  v: Pick<Part, "dir"> & { hosts?: VariantHost[] },
  host: string | undefined,
): string {
  const h = list.hosts.find((x) => x.host === host);
  const used = v.hosts?.find((x) => x.host === host)?.folder;
  const root = h?.folders?.find((f) => f.id === used)?.root ?? h?.models_root;
  return root && v.dir ? `${root.replace(/\/$/, "")}/${v.dir}` : (v.dir ?? "");
}

/** The models with only the chosen host's presence ("" = all hosts); a variant or add-on no longer on any host stays
 * listed. */
export function forHost(models: Model[], host: string): Model[] {
  if (!host) return models;
  const on = <T extends { hosts: VariantHost[] }>(x: T) => ({
    ...x,
    hosts: x.hosts.filter((h) => h.host === host),
  });
  return models.map((m) => ({
    ...m,
    variants: m.variants.map(on),
    addons: (m.addons ?? []).map(on),
  }));
}

/** A model's variants and optional add-ons (API 1.36): every part with a presence per host. */
export const parts = (m: Model): Part[] => [...m.variants, ...(m.addons ?? [])];

/** True while any variant or add-on is downloading somewhere (the screen then polls fast). */
export const anyDownloading = (models: Model[]) =>
  models.some((m) => parts(m).some((v) => v.hosts.some((h) => h.state === "downloading")));

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
export function downloadPlan(list: S<"ModelList">, v: Part, host: string): DownloadPlan {
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
export function canDelete(v: Pick<Part, "hosts">, host: string): boolean {
  const s = v.hosts.find((x) => x.host === host)?.state;
  return s === "present" || s === "partial" || s === "mismatch";
}

/** Create's model node choices. Variants first: a variant field offers only the variants present on a live host
 * (`enums`; all of them while none is, as the workflow then reads "Not downloaded"), and a form holding another sends
 * the field's default when present, else the first (`pins`). Then precisions (API 1.33 RunsAt): each precision field
 * whose node's chosen variant loads at only
 * some of the field's values → those values (`enums`: the tile offers only them, so Base never offers Base fp8's fp8)
 * and, when the form's value is not one of them, the one to send instead (`pins`: the field's default when allowed,
 * else the first, as stored; one value left is always pinned). `raws` is what the form holds; a field it lacks keeps
 * the workflow's value. */
export function precisionPins(
  fields: readonly Field[],
  raws: Record<string, unknown>,
  list: S<"ModelList"> | null,
): { enums: Record<string, string[]>; pins: Record<string, string> } {
  const enums: Record<string, string[]> = {};
  const pins: Record<string, string> = {};
  if (!list) return { enums, pins };
  const live = new Set(list.hosts.filter((h) => h.live).map((h) => h.host));
  const variantsOf = (family: string | null | undefined) =>
    list.models.filter((m) => m.family === family).flatMap((m) => m.variants);
  for (const f of fields) {
    if (!f.model || f.param.name !== "variant") continue;
    const here = new Set(
      variantsOf(f.family)
        .filter((v) => v.hosts.some((h) => h.state === "present" && live.has(h.host)))
        .map((v) => v.id),
    );
    const ok = (f.param.enum ?? []).filter((o) => here.has(o));
    if (!ok.length || ok.length === f.param.enum?.length) continue;
    enums[f.key] = ok;
    const cur = String(raws[f.key] ?? f.initial ?? "");
    if (!ok.includes(cur))
      pins[f.key] = ok.includes(String(f.param.default)) ? String(f.param.default) : ok[0];
  }
  for (const f of fields) {
    if (!f.model || f.param.name !== "precision") continue;
    const vf = fields.find((x) => x.node === f.node && x.param.name === "variant");
    const vid = String(
      (vf && (pins[vf.key] ?? raws[vf.key])) ?? vf?.initial ?? f.siblings?.variant ?? "",
    );
    const v = variantsOf(f.family).find((x) => x.id === vid);
    const ok = (f.param.enum ?? []).filter((p) =>
      (v?.precisions as string[] | undefined)?.includes(p),
    );
    if (!ok.length || ok.length === f.param.enum?.length) continue;
    enums[f.key] = ok;
    const cur = String(raws[f.key] ?? f.initial ?? "");
    if (ok.length === 1 || !ok.includes(cur))
      pins[f.key] = ok.includes(String(f.param.default)) ? String(f.param.default) : ok[0];
  }
  return { enums, pins };
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

// ------------------------------------------------------------------ model folders (API 1.78, plan 40, ModelFolders board)

type Folder = S<"ModelFolder">;
type Copy = S<"ModelCopy">;

/** A host's model folders in priority order ([] from a runner older than 1.78). */
export const foldersOf = (list: S<"ModelList">, host: string): Folder[] =>
  list.hosts.find((h) => h.host === host)?.folders ?? [];

/** The folder of a host by id. */
export const folderById = (list: S<"ModelList">, host: string, id: string | null | undefined) =>
  foldersOf(list, host).find((f) => f.id === id);

/** A host chip's disk tag and note (frames 1, 3): the folder it loads from (only on a host with more than one) and
 * `also on T9`, `T9 not connected` or, when it fell back past an unplugged one, `T9 not connected · using Mac`. */
export function copyNote(
  list: S<"ModelList">,
  h: VariantHost,
): { tag: string | null; note: string | null } {
  const fs = foldersOf(list, h.host);
  if (fs.length < 2) return { tag: null, note: null };
  const used = fs.find((f) => f.id === h.folder);
  const rank = (id: string) => fs.findIndex((f) => f.id === id);
  const others = (h.copies ?? []).filter((c) => c.folder !== h.folder);
  const name = (c: Copy) => fs.find((f) => f.id === c.folder)?.name ?? c.folder;
  const away = others.find((c) => c.state === "unplugged");
  const spare = others.find((c) => c.state !== "unplugged");
  if (h.state === "unplugged")
    return { tag: null, note: away ? `${name(away)} not connected` : null };
  const note = away
    ? `${name(away)} not connected${used && rank(away.folder) < rank(used.id) ? ` · using ${used.name}` : ""}`
    : spare
      ? `also on ${name(spare)}`
      : null;
  return { tag: used?.name ?? null, note };
}

/** The Hosts row's words: `present · Mac` on a host with more than one folder, `T9 not connected` when unplugged. */
export function hostText(list: S<"ModelList">, h: VariantHost): string {
  if (h.state === "unplugged") return copyNote(list, h).note ?? "not connected";
  const tag = copyNote(list, h).tag;
  return tag && h.state === "present" ? `${stateText(h)} · ${tag}` : stateText(h);
}

/** Download's Folder pick (frame 4): the downloads folder first, then the rest in priority order; the one picked at
 * first is the downloads folder when it can take files, else the next connected writable one. */
export function downloadFolders(
  list: S<"ModelList">,
  host: string,
): { folders: Folder[]; pick: string | null } {
  const fs = foldersOf(list, host);
  const folders = [...fs.filter((f) => f.downloads), ...fs.filter((f) => !f.downloads)];
  const pick =
    folders.find((f) => f.downloads && f.connected && f.writable) ??
    fs.find((f) => f.connected && f.writable);
  return { folders, pick: pick?.id ?? null };
}

/** Delete's copies (frame 6): each folder with its files of it, the used one first. */
export function deleteCopies(
  list: S<"ModelList">,
  h: VariantHost | undefined,
): { copy: Copy; folder: Folder | undefined; used: boolean }[] {
  if (!h) return [];
  const rows = (h.copies ?? []).map((copy) => ({
    copy,
    folder: folderById(list, h.host, copy.folder),
    used: copy.folder === h.folder,
  }));
  return [...rows.filter((r) => r.used), ...rows.filter((r) => !r.used)];
}

/** A folder's name for a path not yet on the list (the runner names it on Save): `/Volumes/<X>/…` → X, else the
 * host's name. */
export const pathName = (path: string, host: string) =>
  /^\/Volumes\/([^/]+)/.exec(path)?.[1] ?? host;

/** Edit folders' own checks before Save (the server words them alike and checks the rest). */
export function pathProblem(path: string, paths: string[]): string | null {
  if (!/^(\/|~\/)/.test(path) || path.split("/").includes(".."))
    return "paths must be absolute or start with ~/";
  if (path.length > 400) return "paths are at most 400 characters";
  const norm = (p: string) => p.replace(/\/+$/, "");
  if (paths.some((p) => norm(p) === norm(path))) return `${path} is listed twice`;
  return null;
}

/** A disk's space as the Folders board writes it: "43 GB", "1.8 TB", "2 TB" ("" for null). */
export function diskText(n: number | null | undefined): string {
  if (n == null) return "";
  if (n >= 1e12) return `${+(n / 1e12).toFixed(1)} TB`;
  return n >= 1e9 ? `${Math.round(n / 1e9)} GB` : bytesText(n);
}
