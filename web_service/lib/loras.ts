// LoRAs (contracts/README.md "LoRAs"): the caller's library (GET /v1/studio/loras: shared ones of the live hosts and
// their own), the rows of a stack node (a rows param whose item has a `Lora` field, `{lora_id}`), which LoRAs fit a
// family and why not, and the upload of an own one (the takes' two-step signed PUT).
import { api, message, ok, type S } from "@/lib/api";
import { safeUrl } from "@/lib/format";
import { xhrPut } from "@/lib/takes";

type Lora = S<"Lora">;
type Param = S<"ParamSchema">;
type Row = S<"RowItem">;
/** an item field's schema (a ParamSchema without name) */
type Spec = Partial<Param>;

/** The Lora field of a rows param's item ("lora"), if it has one. */
export const loraFieldOf = (p: Param) =>
  p.type === "rows" ? Object.keys(p.item ?? {}).find((k) => p.item?.[k] === "Lora") : undefined;

/** Whether a value is a Lora (`{lora_id}`). */
export const isLoraRef = (v: unknown): v is S<"LoraRef"> =>
  !!v && typeof v === "object" && typeof (v as { lora_id?: unknown }).lora_id === "string";

const specOf = (p: Param, type: string): [string, Spec] | undefined =>
  Object.entries(p.item ?? {})
    .map(([k, v]) => [k, v as unknown as Spec] as [string, Spec])
    .find(([, v]) => typeof v === "object" && v.type === type);

/** The item's strength field (its first float) and mode field (its first enum), when it has them. */
export const strengthOf = (p: Param) => specOf(p, "float");
export const modeOf = (p: Param) => specOf(p, "enum");

/** The LoRA id of each row ("" for a row without one). */
export const rowIds = (p: Param, rows: Row[]) => {
  const k = loraFieldOf(p) ?? "lora";
  return rows.map((r) => (isLoraRef(r[k]) ? r[k].lora_id : ""));
};

/** A new row for a LoRA: its id and every other field at its default. */
export function newRow(p: Param, id: string): Row {
  const row: Row = { [loraFieldOf(p) ?? "lora"]: { lora_id: id } };
  for (const [k, v] of Object.entries(p.item ?? {})) {
    const s = v as unknown as Spec;
    if (typeof s === "object" && s.default !== undefined) row[k] = s.default as Row[string];
  }
  return row;
}

/** Rows with these LoRAs added at the end: each once, no more than the param's max. */
export function addRows(p: Param, rows: Row[], ids: string[]): Row[] {
  const have = new Set(rowIds(p, rows));
  const more = [...new Set(ids)].filter((id) => !have.has(id)).map((id) => newRow(p, id));
  return [...rows, ...more].slice(0, Math.max(p.max ?? Infinity, rows.length));
}

/** A strength as the schema allows it: a number on the step (from 0), within min..max; not a number = the default. */
export function clampStrength(v: number, s: Spec): number {
  const def = typeof s.default === "number" ? s.default : 1;
  if (!Number.isFinite(v)) return def;
  const step = s.step ?? 0;
  let x = step > 0 ? Math.round(v / step) * step : v;
  x = Math.min(s.max ?? Infinity, Math.max(s.min ?? -Infinity, x));
  const places = step > 0 ? (String(step).split(".")[1]?.length ?? 0) : 6;
  return Number(x.toFixed(places));
}

/** Why a LoRA of the library cannot be added to a stack of `family` now (README, LoRAs: the picker's reasons), or
 * null when it fits. */
export function fitReason(l: Lora, family: string | null): string | null {
  if (l.state === "pending") return "checking…";
  if (l.state === "rejected") return `rejected: ${l.reason ?? "it failed a check"}`;
  if (l.family === "unknown") return "model unknown";
  if (family && l.family !== family) return `for ${l.family}`;
  return null;
}

/** What is wrong with each row, as studio words it at run time (null: nothing), with the library known (null: only
 * what the rows themselves show). */
export function rowIssues(
  p: Param,
  rows: Row[],
  library: Lora[] | null,
  family: string | null,
): (string | null)[] {
  const ids = rowIds(p, rows);
  return ids.map((id, i) => {
    if (!id) return "pick a LoRA";
    const first = ids.indexOf(id);
    if (first < i) return `already in row ${first + 1}`;
    if (!library) return null;
    const l = library.find((x) => x.id === id);
    if (!l) return "not found: deleted, or on no live host";
    if (l.state !== "ready") return fitReason(l, family);
    if (l.family === "unknown") return `does not fit ${family} (its model is unknown)`;
    if (family && l.family !== family) return `does not fit ${family} (it is for ${l.family})`;
    return null;
  });
}

/** The graph rules' view of the library (README, Graph rules: `lora_unknown`, `lora_family`): id → detected family of
 * every LoRA the caller can use now. */
export const usable = (library: Lora[] | null): Record<string, string> =>
  Object.fromEntries(
    (library ?? []).filter((l) => l.state === "ready").map((l) => [l.id, l.family]),
  );

/** "144 MB", "1.2 GB". */
export const sizeText = (n: number) =>
  n >= 1e9 ? `${(n / 1e9).toFixed(1)} GB` : `${Math.max(1, Math.round(n / 1e6))} MB`;

/** The picker tile's line: "rank 32 · 144 MB · shared", or the reason it does not fit. */
export function tileLine(l: Lora, family: string | null): string {
  return (
    fitReason(l, family) ??
    [l.rank && `rank ${l.rank}`, sizeText(l.size_bytes), l.source].filter(Boolean).join(" · ")
  );
}

/** A row's caption under its name: "Strength · rank 32 ·" (the mode follows as a toggle). */
export const strengthCaption = (l: Lora | undefined) =>
  ["Strength", l?.rank ? `rank ${l.rank}` : ""].filter(Boolean).join(" · ") + " · ";

/** Whether node `id` is a stack with nothing in it (the identity: it does no work, so the progress strip skips it). */
export function emptyStack(w: Pick<S<"Workflow">, "nodes" | "node_schemas">, id: string) {
  const n = w.nodes[id];
  const s = w.node_schemas[n?.kind];
  const p = s?.params.find((x) => loraFieldOf(x));
  if (!p) return false;
  const v = n.params?.[p.name] ?? p.default;
  return n.bypass === true || !Array.isArray(v) || v.length === 0;
}

/** A take's LoRAs as its details list them: "name · 0.80 · adapter", "(deleted)" when an own one is gone now. */
export function madeWithLine(m: S<"MadeWithLora">, library: Lora[] | null): string {
  const gone = library && m.source === "mine" && !library.some((l) => l.id === m.id);
  return `${m.name} · ${m.strength.toFixed(2)} · ${m.mode}${gone ? " (deleted)" : ""}`;
}

/** Reuse's word on the take's LoRAs: which were deleted, or are on no live host now (empty when all are current). */
export function lorasNote(st: S<"LoraStatus">[], made: S<"MadeWithLora">[]): string {
  const name = (id: string) => made.find((m) => m.id === id)?.name ?? "a LoRA";
  const said = st.flatMap((s) =>
    s.status === "deleted"
      ? [`LoRA ${name(s.id)} was deleted`]
      : s.status === "missing"
        ? [`LoRA ${name(s.id)} is on no live host now`]
        : [],
  );
  return said.length ? `Since this take: ${said.join("; ")}. Remove it to run.` : "";
}

// ---------------------------------------------------------------- upload

/** The largest LoRA upload (LoraUploadCreate.bytes). */
export const MAX_LORA = 2_147_483_648;

/** Why a file can't be uploaded as a LoRA (worded like studio_service's 422), or null. */
export function checkLoraFile(f: { name: string; size: number }): string | null {
  if (!/\.safetensors$/i.test(f.name)) return "only .safetensors files can be uploaded";
  if (f.size > MAX_LORA) return "file is larger than 2 GB";
  if (f.size < 1) return "file is empty";
  return null;
}

/** An upload as it goes: sending the bytes (0..1), the server checking them, done, or failed with the reason (`id`:
 * the LoRA was created, so the library lists it, pending or rejected). */
export type UploadState =
  | { phase: "sending"; name: string; progress: number }
  | { phase: "checking"; name: string }
  | { phase: "done"; name: string; lora: Lora }
  | { phase: "failed"; name: string; error: string; id?: string };

/** The three calls of an upload; the defaults go through the BFF and the signed link. */
export type LoraIO = {
  create: (body: S<"LoraUploadCreate">) => Promise<S<"LoraUpload">>;
  put: (url: string, file: Blob, ct: string, onProgress: (f: number) => void) => Promise<void>;
  complete: (id: string) => Promise<Lora>;
};

const defaultIO: LoraIO = {
  create: (body) => ok(api.POST("/v1/studio/loras", { body })),
  put: xhrPut,
  complete: (id) =>
    ok(api.POST("/v1/studio/loras/{lora_id}/complete", { params: { path: { lora_id: id } } })),
};

/** Uploads one LoRA, telling `on` each state: checked here, created (pending), the bytes PUT, then completed by the
 * server, which checks and hashes it (a rejection's reason is its 422 detail). Never throws. */
export async function uploadLora(
  file: File,
  on: (s: UploadState) => void,
  io: LoraIO = defaultIO,
): Promise<Lora | null> {
  const name = file.name;
  let id = "";
  const fail = (error: string) => (on({ phase: "failed", name, error, ...(id && { id }) }), null);
  const bad = checkLoraFile(file);
  if (bad) return fail(bad);
  on({ phase: "sending", name, progress: 0 });
  try {
    const up = await io.create({ filename: name.slice(0, 255), bytes: file.size });
    id = up.lora.id;
    const url = safeUrl(up.upload_url);
    if (!url) return fail("The server gave an unusable upload link.");
    await io.put(url, file, up.content_type, (progress) =>
      on({ phase: "sending", name, progress }),
    );
    on({ phase: "checking", name });
    const lora = await io.complete(id);
    on({ phase: "done", name, lora });
    return lora;
  } catch (e) {
    return fail(message(e));
  }
}

// ---------------------------------------------------------------- the Models screen's LoRAs tab

/** A library tile's line: "checking…", the rejection, else "qwen-image · rank 32 · 144 MB · mac, gpu1" (or "mine"). */
export function libraryLine(l: Lora): string {
  if (l.state !== "ready") return fitReason(l, null)!;
  return [
    l.family,
    l.rank && `rank ${l.rank}`,
    sizeText(l.size_bytes),
    l.source === "mine" ? "mine" : l.hosts.join(", "),
  ]
    .filter(Boolean)
    .join(" · ");
}

/** The quota line: "3 of 50 · 0.4 of 20 GB". */
export const quotaText = (u: S<"LoraUsage">) =>
  `${u.count} of ${u.max_count} · ${(u.bytes / 2 ** 30).toFixed(1)} of ${u.max_bytes / 2 ** 30} GB`;
