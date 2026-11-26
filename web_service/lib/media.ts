// Media: what a workflow's takes are (the Image / Video / Audio switch), the frame grid of a `frames` param (the Duration
// slider), and how a take or a run output is shown small (contracts/README.md "Video").
import type { S } from "@/lib/api";

type Workflow = S<"Workflow">;
type Param = S<"ParamSchema">;
export type MediaKind = NonNullable<Workflow["media"]>; // text: an ask workflow (never listed)

/** A workflow's media; absent (an API before 1.11) is image. */
export const mediaOf = (w: Pick<Workflow, "media">): MediaKind => w.media ?? "image";

/** Create's kind tabs (plan 38, docs/mock/CreateMusicSound.dc.html): Image, Video, then the three sound classes of
 * an audio workflow (Voice, Music, Sound: `Workflow.sound`, absent = voice). */
export const KINDS = ["image", "video", "voice", "music", "sound"] as const;
export type Kind = (typeof KINDS)[number];
export const KIND_LABEL: Record<Kind, string> = {
  image: "Image",
  video: "Video",
  voice: "Voice",
  music: "Music",
  sound: "Sound",
};
/** A workflow's tab: its media, or its sound class for audio (none = voice); an ask workflow is none. */
export function kindOf(w?: Pick<Workflow, "media" | "sound">): Kind | undefined {
  const m = w ? mediaOf(w) : "image";
  if (m === "text") return undefined;
  return m === "audio" ? (w?.sound ?? "voice") : m;
}
/** The sides whose texts never cross (alpha.2 item 14): picture (image, video) and each sound class; voice keeps the
 * old "sound" key, so a script drafted before plan 38 still comes back. */
export const SIDES = ["picture", "sound", "music", "effects"] as const;
export type Side = (typeof SIDES)[number];
/** A workflow's side; none (no workflow yet) is picture. */
const SIDE: Partial<Record<Kind, Side>> = { voice: "sound", music: "music", sound: "effects" };
export const sideOf = (w?: Pick<Workflow, "media" | "sound">): Side =>
  SIDE[kindOf(w) ?? "image"] ?? "picture";

/** A workflow whose voice params take designed voices only (`only {voice: [design]}`: Qwen3-TTS Voice design). */
const designsOnly = (w: Workflow) =>
  Object.values(w.nodes ?? {}).some((n) =>
    Object.values(n.only ?? {}).some((a) => a.length === 1 && a[0] === "design"),
  );

/** The tabs the switch offers, in its order: only those some workflow has. */
export const createKinds = (ws: Pick<Workflow, "media" | "sound">[]): Kind[] =>
  KINDS.filter((k) => ws.some((w) => kindOf(w) === k));

/** The family a kind opens on before any run (owner, 2026-11-27: "MiniMax default" for Music). */
const FIRST: Partial<Record<Kind, string>> = { music: "minimax-music3", sound: "stable-audio-3" };

/** The kind's templates a tab opens or its model popup lists: not a take action (Repaint, Cover, Extend, Vary,
 * Add sound: offered on a take, `action`), the kind's default family first. */
export const kindTemplates = (ws: Workflow[], k: Kind) =>
  ws
    .filter((w) => kindOf(w) === k && w.source !== "mine" && !w.action)
    .sort((a, b) => +(b.family === FIRST[k]) - +(a.family === FIRST[k]));

/** The workflow the switch opens for `k`: the open one when it is of `k`, else the one of the session's newest run
 * of `k` (runs newest first), else the kind's first template (its default family first, a take action or one that
 * only designs voices, its `only` voice [design], last: Design a voice opens it), else its first workflow; undefined
 * when none. */
export function workflowFor(
  ws: Workflow[],
  runs: Pick<S<"Run">, "workflow_id">[],
  k: Kind,
  current?: string,
): string | undefined {
  const of = ws.filter((w) => kindOf(w) === k);
  const has = (id?: string) => of.some((w) => w.id === id);
  if (has(current)) return current;
  return (
    runs.find((r) => has(r.workflow_id))?.workflow_id ??
    (
      kindTemplates(ws, k).find((w) => !designsOnly(w)) ??
      of.find((w) => w.source !== "mine") ??
      of[0]
    )?.id
  );
}

/** An audio take's sound line: "44.1 kHz stereo" (mono when one channel or absent). */
export const soundText = (a?: Pick<S<"AudioInfo">, "sample_rate" | "channels">) =>
  a?.sample_rate
    ? `${+(a.sample_rate / 1000).toFixed(1)} kHz ${a.channels === 2 ? "stereo" : "mono"}`
    : "";

/** A frames param's grid: `min + k × step` up to `max`. */
export const gridOf = (p: Param) => ({
  min: p.min ?? 1,
  step: p.step ?? 1,
  max: p.max ?? Infinity,
});

/** Why a frame count is off its param's grid (worded like studio's 422 after `<node>.<param>: `), or null. */
export function frameError(p: Param, v: unknown): string | null {
  const g = gridOf(p);
  if (typeof v !== "number" || !Number.isInteger(v)) return `${String(v)} is not an integer`;
  if (v < g.min || v > g.max || (v - g.min) % g.step)
    return `${v} is not on the frame grid ${g.min} + ${g.step}k up to ${g.max}`;
  return null;
}

/** The grid's frame count nearest to `n` (ties go up), within its bounds. */
export function snapFrames(p: Param, n: number): number {
  const g = gridOf(p);
  const k = Math.round((n - g.min) / g.step);
  const top = Math.floor((g.max - g.min) / g.step);
  return g.min + Math.max(0, Math.min(top, k)) * g.step;
}

/** Seconds as the duration readouts show them: one decimal ("2.0 s"). */
export const secondsText = (s: number) => `${s.toFixed(1)} s`;

/** A clip's length in seconds: frames / fps (0 without a frame rate). */
export const seconds = (frames: number, fps: number) => (fps > 0 ? frames / fps : 0);

/** The frame count on the grid nearest to `s` seconds at `fps`. */
export const framesFor = (p: Param, s: number, fps: number) => snapFrames(p, s * fps);

/** The Duration slider's label: "49 frames · 2.0 s". */
export const durationText = (frames: number, fps: number) =>
  `${frames} frames · ${secondsText(seconds(frames, fps))}`;

/** What a take or run output shows small: its picture (its thumbnail: from API 1.48 a small WebP, a run output's
 * its take's; else an image itself, a video's poster; audio has none) and a video's or audio's length ("2.0 s"). */
export function thumbOf(t: S<"Take"> | S<"RunOutput">): { src?: string; length?: string } {
  const audio = "audio" in t ? t.audio : undefined;
  if (t.kind === "audio") return { length: audio && secondsText(audio.duration_s) };
  const src = t.thumb_url ?? (t.kind === "video" ? t.video?.poster_url : t.url);
  return { src, length: t.video ? secondsText(t.video.duration_s) : undefined };
}

/** An audio take's format from its type ("mp3", "wav"). */
export const audioFormat = (contentType: string) =>
  contentType === "audio/mpeg"
    ? "mp3"
    : contentType === "audio/mp4"
      ? "m4a"
      : contentType.replace(/^audio\/(x-)?/, "");

/** The take's line in a details panel: "2.0 s · 24 fps · 49 frames · audio"; "" for an image. */
export const videoLine = (v?: S<"VideoInfo">) =>
  v
    ? [
        secondsText(v.duration_s),
        `${+v.fps.toFixed(3)} fps`,
        `${v.frames} frames`,
        v.has_audio ? "audio" : "no audio",
      ].join(" · ")
    : "";
