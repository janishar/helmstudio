// Takes: the gallery's filters, labels and the upload flow (contracts/README.md "Takes, the gallery and uploads").
import { api, ApiError, errorText, ok, type S } from "@/lib/api";
import { safeUrl } from "@/lib/format";
import { pretty } from "@/lib/workflow";

type Take = S<"Take">;
type Run = S<"Run">;

/** The gallery's chips: a kind, starred and a source combine (the API ANDs them); none set is "All". */
export type Filter = { kind?: S<"TakeKind">; starred?: boolean; source?: S<"TakeSource"> };

/** The list query for a filter, a search text and a page cursor: only what is set (a blank search is none). */
export function takeQuery(f: Filter, q: string, cursor?: string | null) {
  const text = q.trim().slice(0, 200);
  return {
    ...(f.kind && { kind: f.kind }),
    ...(f.starred && { starred: true }),
    ...(f.source && { source: f.source }),
    ...(text && { q: text }),
    ...(cursor && { cursor }),
  };
}

/** Whether a take belongs in the list for this filter (search aside): a new upload joins only a list it fits. */
export const fits = (t: Take, f: Filter) =>
  (!f.kind || t.kind === f.kind) &&
  (!f.starred || t.starred) &&
  (!f.source || t.source === f.source);

/** A page appended to the list; a take already there (it moved while paging) is not repeated. */
export function appendPage(list: Take[], page: Take[]): Take[] {
  const ids = new Set(list.map((t) => t.id));
  return [...list, ...page.filter((t) => !ids.has(t.id))];
}

/** The tile's mono line: what made it and where ("Qwen Image · Create", "Qwen Image · Pipeline", "Upload"). */
export const subLine = (t: Take) =>
  t.source === "upload"
    ? "Upload"
    : [t.made_with?.workflow_title, t.source === "pipeline" ? "Pipeline" : "Create"]
        .filter(Boolean)
        .join(" · ");

/** "Qwen Image · Base · int8": the workflow and the weights it used. */
export const templateLine = (m: S<"MadeWith">) =>
  [m.workflow_title, m.model?.variant && pretty(m.model.variant), m.model?.precision]
    .filter(Boolean)
    .join(" · ");

/** "2752 × 1536 · 43": the size and the seed, either may be missing; "" when neither is known. */
export function sizeSeed(t: Take): string {
  const size = t.width && t.height ? `${t.width} × ${t.height}` : "";
  const seed = t.made_with?.seed;
  return [size, seed == null ? "" : String(seed)].filter(Boolean).join(" · ");
}

/** What the strip under Create's stage shows, newest first: the session's takes and the runs that have none to show
 * (still working, failed or stopped). A finished run is shown by its takes; until they are loaded, by itself. */
export type StripItem = { take: Take; run?: undefined } | { run: Run; take?: undefined };
export function stripItems(runs: Run[], takes: Take[]): StripItem[] {
  const covered = new Set(takes.map((t) => t.run_id));
  const items: { at: string; item: StripItem }[] = [
    ...takes.map((take) => ({ at: take.created_at, item: { take } })),
    ...runs
      .filter((r) => r.status !== "succeeded" || (!covered.has(r.id) && r.outputs.length > 0))
      .map((run) => ({ at: run.created_at, item: { run } })),
  ];
  return items.sort((a, b) => (a.at < b.at ? 1 : a.at > b.at ? -1 : 0)).map((x) => x.item);
}

/** The image types and the size an upload may have (UploadCreate). */
export const UPLOAD_TYPES = ["image/png", "image/jpeg", "image/webp"] as const;
export const MAX_UPLOAD = 52_428_800;

/** Why a file can't be uploaded (worded like studio_service's 422), or null. */
export function checkFile(f: { type: string; size: number }): string | null {
  if (!(UPLOAD_TYPES as readonly string[]).includes(f.type))
    return "only png, jpeg and webp images can be uploaded";
  if (f.size > MAX_UPLOAD) return "file is larger than 50 MB";
  if (f.size < 1) return "file is empty";
  return null;
}

/** The three calls of an upload; the defaults go through the BFF and the signed link. */
export type UploadIO = {
  create: (body: S<"UploadCreate">) => Promise<S<"Upload">>;
  put: (
    url: string,
    file: Blob,
    contentType: string,
    onProgress: (f: number) => void,
  ) => Promise<void>;
  complete: (takeId: string) => Promise<Take>;
};

/** PUTs the bytes to a signed link with XHR (fetch has no upload progress); `onProgress` gets 0..1. */
function xhrPut(url: string, file: Blob, contentType: string, onProgress: (f: number) => void) {
  return new Promise<void>((resolve, reject) => {
    const x = new XMLHttpRequest();
    x.open("PUT", url);
    x.setRequestHeader("Content-Type", contentType);
    x.upload.onprogress = (e) => e.lengthComputable && onProgress(e.loaded / e.total);
    x.onload = () => {
      if (x.status >= 200 && x.status < 300) return resolve();
      let body: unknown = null;
      try {
        body = JSON.parse(x.responseText);
      } catch {
        /* not JSON */
      }
      reject(new ApiError(x.status, errorText(x.status, body)));
    };
    x.onerror = () => reject(new ApiError(0, "The upload was interrupted. Try again."));
    x.send(file);
  });
}

const defaultIO: UploadIO = {
  create: (body) => ok(api.POST("/v1/studio/uploads", { body })),
  put: xhrPut,
  complete: (id) =>
    ok(api.POST("/v1/studio/uploads/{take_id}/complete", { params: { path: { take_id: id } } })),
};

/** Uploads one image: checks it, starts the upload, PUTs the bytes to the signed link (same-origin path, see
 * safeUrl) and completes it. Throws an Error fit to show. */
export async function uploadFile(
  file: File,
  onProgress: (f: number) => void,
  io: UploadIO = defaultIO,
): Promise<Take> {
  const bad = checkFile(file);
  if (bad) throw new Error(bad);
  const up = await io.create({
    filename: file.name.slice(0, 255) || "upload",
    content_type: file.type as S<"UploadCreate">["content_type"],
    bytes: file.size,
  });
  const url = safeUrl(up.upload_url);
  if (!url) throw new Error("The server gave an unusable upload link.");
  await io.put(url, file, up.content_type, onProgress);
  return io.complete(up.take_id);
}
