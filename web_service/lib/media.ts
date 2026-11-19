// Media: what a workflow's takes are (the Image / Video switch), the frame grid of a `frames` param (the Duration
// slider), and how a take or a run output is shown small (contracts/README.md "Video").
import type { S } from "@/lib/api";

type Workflow = S<"Workflow">;
type Param = S<"ParamSchema">;
export type MediaKind = NonNullable<Workflow["media"]>; // text: an ask workflow (never listed)

/** A workflow's media; absent (an API before 1.11) is image. */
export const mediaOf = (w: Pick<Workflow, "media">): MediaKind => w.media ?? "image";

/** The media the switch offers, in its order: only those some workflow has. */
export const mediaKinds = (ws: Pick<Workflow, "media">[]): MediaKind[] =>
  (["image", "video"] as const).filter((m) => ws.some((w) => mediaOf(w) === m));

/** The workflow the switch opens for `m`: the open one when it is of `m`, else the one of the session's newest run of
 * `m` (runs newest first), else the first template of `m`, else the first workflow of `m`; undefined when none. */
export function workflowFor(
  ws: Workflow[],
  runs: Pick<S<"Run">, "workflow_id">[],
  m: MediaKind,
  current?: string,
): string | undefined {
  const of = ws.filter((w) => mediaOf(w) === m);
  const has = (id?: string) => of.some((w) => w.id === id);
  if (has(current)) return current;
  return (
    runs.find((r) => has(r.workflow_id))?.workflow_id ??
    (of.find((w) => w.source !== "mine") ?? of[0])?.id
  );
}

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

/** What a take or run output shows small: its picture (an image's thumbnail, a video's poster; audio has none) and
 * a video's or audio's length ("2.0 s"). */
export function thumbOf(t: S<"Take"> | S<"RunOutput">): { src?: string; length?: string } {
  const audio = "audio" in t ? t.audio : undefined;
  if (t.kind === "audio") return { length: audio && secondsText(audio.duration_s) };
  const src = "thumb_url" in t ? t.thumb_url : t.kind === "video" ? t.video?.poster_url : t.url;
  return { src, length: t.video ? secondsText(t.video.duration_s) : undefined };
}

/** An audio take's format from its type ("mp3", "wav"). */
export const audioFormat = (contentType: string) =>
  contentType === "audio/mpeg" ? "mp3" : contentType.replace(/^audio\/(x-)?/, "");

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
