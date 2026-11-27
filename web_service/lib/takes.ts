// Takes: the gallery's filters, labels and the upload flow (contracts/README.md "Takes, the gallery and uploads").
import { api, ApiError, errorText, ok, type S } from "@/lib/api";
import { safeUrl } from "@/lib/format";
import { audioFormat, secondsText } from "@/lib/media";
import { optimise, type Limits, type Optimised, type Step } from "@/lib/optimise";
import { clock } from "@/lib/runs";
import { prettyModel } from "@/lib/workflow";

type Take = S<"Take">;
type Run = S<"Run">;

/** The gallery's chips: a kind, starred, a source, the project (a session) and a batch combine (the API ANDs them);
 * none set is "All". */
export type Filter = {
  /** a take kind, or from API 1.79 a sound class (the audio takes of that class) */
  kind?: S<"TakeKind"> | S<"SoundClass">;
  starred?: boolean;
  source?: S<"TakeSource">;
  session_id?: string;
  batch_id?: string;
  /** API 1.44: the Gallery's sort (absent = newest) */
  order?: "newest" | "oldest" | "starred" | "name";
};

const PROJECT = "helm.project";

/** The session last opened in Create in this browser: the Gallery's "This project only" (a convenience: gone when
 * the store is cleared). */
export function lastProject(): string | null {
  try {
    return localStorage.getItem(PROJECT);
  } catch {
    return null;
  }
}
export function rememberProject(id: string) {
  try {
    localStorage.setItem(PROJECT, id);
  } catch {
    /* private window */
  }
}

/** The list query for a filter, a search text and a page cursor: only what is set (a blank search is none). */
export function takeQuery(f: Filter, q: string, cursor?: string | null) {
  const text = q.trim().slice(0, 200);
  return {
    ...(f.kind && { kind: f.kind }),
    ...(f.starred && { starred: true }),
    ...(f.source && { source: f.source }),
    ...(f.session_id && { session_id: f.session_id }),
    ...(f.batch_id && { batch_id: f.batch_id }),
    ...(f.order && f.order !== "newest" && { order: f.order }),
    ...(text && { q: text }),
    ...(cursor && { cursor }),
  };
}

/** An audio take's sound class (API 1.79; voice when it names none, as studio reads takes made before 1.79); undefined
 * for an image, a video or a run output (which carries no class). */
export const soundOf = (t: Take | S<"RunOutput">): S<"SoundClass"> | undefined =>
  t.kind === "audio" && "source" in t ? (t.sound ?? "voice") : undefined;

/** Whether a take belongs in the list for this filter (search aside): a new upload joins only a list it fits. */
export const fits = (t: Take, f: Filter) =>
  (!f.kind || t.kind === f.kind || soundOf(t) === f.kind) &&
  (!f.starred || t.starred) &&
  (!f.source || t.source === f.source) &&
  (!f.session_id || t.session_id === f.session_id) &&
  (!f.batch_id || t.batch_id === f.batch_id);

/** A page appended to the list; a take already there (it moved while paging) is not repeated. */
export function appendPage(list: Take[], page: Take[]): Take[] {
  const ids = new Set(list.map((t) => t.id));
  return [...list, ...page.filter((t) => !ids.has(t.id))];
}

/** The tile's mono line: what made it and where ("Qwen Image · Create", "Qwen Image · Pipeline", "Upload"); a run's
 * audio take: what made it and its length ("ACE-Step 1.5 · 2:10", docs/mock/CreateMusicSound.dc.html frame 7). */
export const subLine = (t: Take) =>
  t.audio && t.made_with?.workflow_title && t.source !== "upload"
    ? `${t.made_with.workflow_title} · ${clock(t.audio.duration_s)}`
    : t.source === "upload"
      ? t.audio
        ? `Upload · ${audioFormat(t.content_type)} · ${secondsText(t.audio.duration_s)}`
        : "Upload"
      : t.source === "tool"
        ? "Edited" // API 1.70: made by a plugin tool (Media tools)
        : [t.made_with?.workflow_title, t.source === "pipeline" ? "Pipeline" : "Create"]
            .filter(Boolean)
            .join(" · ");

/** "Qwen Image · Base · int8": the workflow and the weights it used. */
export const templateLine = (m: S<"MadeWith">) =>
  [m.workflow_title, ...prettyModel([m.model?.variant ?? "", m.model?.precision ?? ""])]
    .filter(Boolean)
    .join(" · ");

/** "2752 × 1536 · 43": the size and the seed, either may be missing; "" when neither is known. */
export function sizeSeed(t: Take): string {
  const size = t.width && t.height ? `${t.width} × ${t.height}` : "";
  const seed = t.made_with?.seed;
  return [size, seed == null ? "" : String(seed)].filter(Boolean).join(" · ");
}

/** The value of the first input named `.<name>` ("music.bpm"), if any. */
const inputOf = (m: S<"MadeWith">, name: string) =>
  Object.entries(m.inputs).find(([k]) => k.endsWith(`.${name}`))?.[1];

/** A music take's Song row: "2:10 · 90 BPM · A minor" (BPM and key only when set: Auto is 0 or "auto"). */
export function songLine(t: Take): string {
  const m = t.made_with;
  const bpm = m && inputOf(m, "bpm");
  const key = m && inputOf(m, "key");
  return [
    t.audio && clock(t.audio.duration_s),
    typeof bpm === "number" && bpm > 0 && `${bpm} BPM`,
    typeof key === "string" && key && key !== "auto" && key,
  ]
    .filter(Boolean)
    .join(" · ");
}

/** A song's lyrics as typed, or "" (none, or Instrumental on). */
export function lyricsOf(t: Take): string {
  const m = t.made_with;
  if (!m || inputOf(m, "instrumental") === true) return "";
  const l = inputOf(m, "lyrics");
  return typeof l === "string" ? l.trim() : "";
}

/** The album cover's gradients (docs/mock/CreateMusicSound.dc.html frame 7): three stops, light to dark. */
const COVERS = [
  ["#3b4f7a", "#6a4c93", "#1f2340"],
  ["#e0a44a", "#a5582c", "#3d2414"],
  ["#ff5f8f", "#7a2fb8", "#1a1240"],
  ["#4fb7a0", "#2c6f7a", "#122630"],
  ["#f07a5a", "#b03a5b", "#2a1030"],
  ["#8fb86a", "#4c7a3a", "#16261a"],
  ["#6ab0f0", "#3a5fb0", "#141c3a"],
  ["#d0a0e0", "#8a4fa0", "#2a1436"],
];

/** A music take's cover gradient, picked in the client from a hash of its prompt (else its title): the same song
 * always gets the same cover (plan 38 44.10.9). */
export function coverOf(t: Take): string {
  return coverFor(t.made_with?.prompt || t.title);
}

/** The album cover's gradient for a text (a music take's prompt or title; a Magic music block's style). */
export function coverFor(text: string): string {
  let h = 0;
  for (let i = 0; i < text.length; i++) h = (h * 31 + text.charCodeAt(i)) | 0;
  const [a, b, c] = COVERS[Math.abs(h) % COVERS.length];
  return `linear-gradient(150deg, ${a}, ${b} 55%, ${c})`;
}

/** The edits an audio take's offers carry as their `action` (API 1.79, offers by class), in the menu's order. */
const EDITS = ["Repaint", "Cover", "Vary", "Extend"];

/** An audio take's offers split for its menu and panel (docs/mock/CreateMusicSound.dc.html frame 6): its edits
 * (Repaint, Cover or Vary, Extend: one offer per action) and the rest, under Use in. Other kinds: all under Use in. */
export function splitOffers(t: Take, offers: S<"TakeOffer">[]) {
  if (t.kind !== "audio") return { edits: [], rest: offers };
  const edits = EDITS.flatMap((a) => offers.find((o) => o.action === a) ?? []);
  // every edit offer stays out of Use in (a second family's Repaint too), as on Create's menu
  return { edits, rest: offers.filter((o) => !o.action || !EDITS.includes(o.action)) };
}

/** The sound take a video was made with (MMAudio: the other take of its run; Save with the video: its audio parent),
 * among `takes`; Sound only opens it. */
export const soundTakeOf = (t: Take, takes: Take[]) =>
  t.kind !== "video"
    ? undefined
    : takes.find(
        (x) =>
          x.kind === "audio" &&
          x.id !== t.id &&
          ((!!t.run_id && x.run_id === t.run_id) || t.parents.includes(x.id)),
      );

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

/** The types and the size an upload may have (UploadCreate; audio from 1.19, m4a from 1.38). */
export const UPLOAD_TYPES = ["image/png", "image/jpeg", "image/webp"] as const;
export const AUDIO_TYPES = ["audio/wav", "audio/mpeg", "audio/mp4"] as const;
/** A file input's accept for audio: the types and the extensions browsers may not type. */
export const AUDIO_ACCEPT = [...AUDIO_TYPES, ".wav", ".mp3", ".m4a"].join(",");
export const MAX_UPLOAD = 52_428_800;
export const MAX_AUDIO_S = 600;

/** The upload type of a file: browsers name wav, mp3 and m4a several ways (audio/x-wav, audio/mp3, audio/x-m4a). */
export function typeOf(f: { type: string; name?: string }): string {
  const ext = f.name?.split(".").pop()?.toLowerCase();
  if (/^audio\/(x-|vnd\.)?wave?$/.test(f.type) || (!f.type && ext === "wav")) return "audio/wav";
  if (/^audio\/(mpeg|mp3|x-mpeg)$/.test(f.type) || (!f.type && ext === "mp3")) return "audio/mpeg";
  if (/^audio\/(x-)?(m4a|mp4)$/.test(f.type) || (!f.type && ext === "m4a")) return "audio/mp4";
  return f.type;
}
export const isAudio = (type: string) => (AUDIO_TYPES as readonly string[]).includes(type);

/** Why a file can't be uploaded (worded like studio_service's 422), or null. `audio` false: images only. The type is
 * the dropped file's; the 50 MB cap is checked on the file that uploads, once optimised (`checkSize`). */
export function checkFile(f: { type: string; size: number; name?: string }, audio = false) {
  const t = typeOf(f);
  if (!(UPLOAD_TYPES as readonly string[]).includes(t) && !(audio && isAudio(t)))
    return audio
      ? "only png, jpeg and webp images or wav, mp3 and m4a audio can be uploaded"
      : "only png, jpeg and webp images can be uploaded";
  if (f.size < 1) return "file is empty";
  return null;
}
export const checkSize = (b: Blob) => (b.size > MAX_UPLOAD ? "file is larger than 50 MB" : null);

/** A pending upload's state (docs/mock/UploadOptimise.dc.html): optimising (a fraction for audio, none for an
 * image), then uploading; `line` is the tile's mono line ("6000 × 4000", "12.4 → 1.9 MB · WebP", "as is"). */
export type UploadProgress = { step: "optimising" | "uploading"; f?: number; line?: string };
export const UPLOAD_START: UploadProgress = { step: "optimising" };
/** "Optimising…", "Optimising 62%", "Uploading 48%". */
export const progressText = (p: UploadProgress) =>
  p.step === "uploading"
    ? `Uploading ${Math.round((p.f ?? 0) * 100)}%`
    : p.f === undefined
      ? "Optimising…"
      : `Optimising ${Math.round(p.f * 100)}%`;

let limits: Promise<Limits> | null = null;
/** GET /v1/studio/uploads/limits, once per page session; the upload cap's side when it can't be read (no scaling). */
export function uploadLimits(): Promise<Limits> {
  limits ??= ok(api.GET("/v1/studio/uploads/limits")).catch(() => {
    limits = null;
    return { image_max_side: 16384 };
  });
  return limits;
}

/** An audio file's length as this browser decodes it (studio records it; the runner reads the file itself). */
export function audioDuration(file: Blob): Promise<number> {
  return new Promise((resolve, reject) => {
    const url = URL.createObjectURL(file);
    const a = new Audio();
    const done = (f: () => void) => {
      URL.revokeObjectURL(url);
      f();
    };
    a.preload = "metadata";
    a.onloadedmetadata = () =>
      done(() =>
        Number.isFinite(a.duration) && a.duration > 0
          ? resolve(a.duration)
          : reject(new Error("could not read the audio's length")),
      );
    a.onerror = () => done(() => reject(new Error("could not read the audio's length")));
    a.src = url;
  });
}

/** The three calls of an upload; the defaults go through the BFF and the signed link. */
export type UploadIO = {
  create: (body: S<"UploadCreate">) => Promise<S<"Upload">>;
  put: (
    url: string,
    file: Blob,
    contentType: string,
    onProgress: (f: number) => void,
    signal?: AbortSignal,
  ) => Promise<void>;
  complete: (takeId: string) => Promise<Take>;
  /** an audio file's length in seconds */
  duration: (file: Blob) => Promise<number>;
  /** the file that uploads (lib/optimise.ts) */
  optimise: (
    file: Blob,
    type: string,
    onStep: (s: Step) => void,
    signal?: AbortSignal,
  ) => Promise<Optimised>;
};

/** PUTs the bytes to a signed link with XHR (fetch has no upload progress); `onProgress` gets 0..1; the signal
 * aborts it. */
export function xhrPut(
  url: string,
  file: Blob,
  contentType: string,
  onProgress: (f: number) => void,
  signal?: AbortSignal,
) {
  return new Promise<void>((resolve, reject) => {
    const x = new XMLHttpRequest();
    signal?.addEventListener("abort", () => x.abort(), { once: true });
    x.onabort = () => reject(signal?.reason ?? new DOMException("stopped", "AbortError"));
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
    if (signal?.aborted) return reject(signal.reason);
    x.send(file);
  });
}

const defaultIO: UploadIO = {
  create: (body) => ok(api.POST("/v1/studio/uploads", { body })),
  put: xhrPut,
  complete: (id) =>
    ok(api.POST("/v1/studio/uploads/{take_id}/complete", { params: { path: { take_id: id } } })),
  duration: audioDuration,
  optimise: async (file, type, onStep, signal) =>
    optimise(file, type, await uploadLimits(), onStep, signal),
};

export type UploadOptions = {
  /** audio too (a wav, mp3 or m4a) */
  audio?: boolean;
  /** a voice clip for cloning (the voice picker's Clone from a clip): never converted, the clone reads the WAV */
  asIs?: boolean;
  /** × on the pending tile: stops the optimising or the PUT (an AbortError) */
  signal?: AbortSignal;
  /** API 1.79: an audio file's class (the Gallery filter it came from); absent = voice; never sent for an image */
  sound?: S<"SoundClass">;
};

/** Uploads one image (or, with `audio`, a wav, mp3 or m4a with its length read here), the one way the browser
 * uploads media: checks its type, optimises it (contracts/README.md, Optimised uploads; `asIs`: not), checks the
 * size of what uploads, starts the upload, PUTs the bytes to the signed link (same-origin path, see safeUrl) and
 * completes it. Throws an Error fit to show, or the signal's AbortError. */
export async function uploadFile(
  file: File,
  onProgress: (p: UploadProgress) => void,
  io: UploadIO = defaultIO,
  { audio = false, asIs = false, signal, sound }: UploadOptions = {},
): Promise<Take> {
  const bad = checkFile(file, audio);
  if (bad) throw new Error(bad);
  const type = typeOf(file);
  let duration_s: number | undefined;
  if (isAudio(type)) {
    duration_s = await io.duration(file).catch(() => {
      throw new Error(`not a valid ${audioFormat(type)} file`);
    });
    if (duration_s > MAX_AUDIO_S) throw new Error(`audio is longer than ${MAX_AUDIO_S} s`);
  }
  signal?.throwIfAborted();
  const out =
    asIs && isAudio(type)
      ? { blob: file, type, line: type === "audio/wav" ? "voice clip · kept as WAV" : "as is" }
      : await io.optimise(file, type, (s) => onProgress({ step: "optimising", ...s }), signal);
  const big = checkSize(out.blob);
  if (big) throw new Error(big);
  signal?.throwIfAborted();
  const up = await io.create({
    filename: file.name.slice(0, 255) || "upload",
    content_type: out.type as S<"UploadCreate">["content_type"],
    bytes: out.blob.size,
    ...(duration_s !== undefined && { duration_s }),
    ...(sound && isAudio(type) && { sound }),
  });
  const url = safeUrl(up.upload_url);
  if (!url) throw new Error("The server gave an unusable upload link.");
  onProgress({ step: "uploading", f: 0, line: out.line });
  await io.put(
    url,
    out.blob,
    up.content_type,
    (f) => onProgress({ step: "uploading", f, line: out.line }),
    signal,
  );
  return io.complete(up.take_id);
}
