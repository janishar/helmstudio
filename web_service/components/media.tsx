"use client";
// The media viewer (plan 05-ui "Media viewer"): a take's media, and its full screen: Esc closes, the arrow keys step
// through the list it was opened from, video plays with the native controls.

import { useEffect, useRef } from "react";
import { ImageOff, Play, X } from "lucide-react";
import { safeUrl } from "@/lib/format";
import { thumbOf } from "@/lib/media";
import type { S } from "@/lib/api";

type Take = S<"Take">;

/** A take's (or a run output's) image, video (native controls, its poster until it plays, fetched with Range
 * requests and only its metadata until then) or audio. */
export function Media({
  take: t,
  url,
  alt,
  className,
  style,
  videoRef,
  autoPlay,
  onPlaying,
}: {
  take: Take | S<"RunOutput">;
  url?: string;
  alt?: string;
  className: string;
  style?: React.CSSProperties;
  videoRef?: React.Ref<HTMLVideoElement>;
  autoPlay?: boolean;
  /** the video started (true) or stopped (false) playing */
  onPlaying?: (on: boolean) => void;
}) {
  const label = alt ?? ("title" in t ? t.title : "The take");
  if (t.kind === "image")
    return <img src={url} alt={label} className={className} style={style} fetchPriority="high" />;
  if (t.kind === "video")
    return (
      <video
        ref={videoRef}
        src={url}
        poster={safeUrl(thumbOf(t).src)}
        aria-label={label}
        controls
        playsInline
        preload="metadata"
        autoPlay={autoPlay}
        onPlay={() => onPlaying?.(true)}
        onPause={() => onPlaying?.(false)}
        className={className}
        style={style}
      />
    );
  return <audio src={url} controls className="w-full" />;
}

/** A take's or run output's picture in a list: an image's thumbnail, a video's poster with its length and a play
 * mark (the video itself is never fetched here), else its kind. */
export function Thumb({
  take: t,
  className,
  imgClassName = "size-full object-cover",
  badge = true,
}: {
  take: Take | S<"RunOutput">;
  className?: string;
  imgClassName?: string;
  /** a video's length and play mark over the poster (false: the caller's label says it) */
  badge?: boolean;
}) {
  const { src, length } = thumbOf(t);
  // a sound tile with its length (docs/mock/VideoSource.dc.html: an uploaded audio take)
  if (t.kind === "audio")
    return (
      <span className={`relative block ${className ?? ""}`}>
        <span aria-hidden className={`wave block ${imgClassName}`} />
        {badge && length && (
          <span className="mono absolute top-1 left-1 rounded bg-s2/90 px-1 text-[10px] leading-4 text-t1">
            {length}
          </span>
        )}
      </span>
    );
  if (!src)
    return (
      <span
        className={`flex flex-col items-center justify-center gap-1 text-t2 ${className ?? ""}`}
      >
        <ImageOff size={16} aria-hidden />
        {t.kind}
      </span>
    );
  return (
    <span className={`relative block ${className ?? ""}`}>
      <img src={safeUrl(src)} alt="" loading="lazy" decoding="async" className={imgClassName} />
      {badge && length && (
        <span className="mono absolute top-1 left-1 flex items-center gap-0.5 rounded bg-s2/90 px-1 text-[10px] leading-4 text-t1">
          <Play size={9} aria-hidden className="fill-current" />
          {length}
        </span>
      )}
    </span>
  );
}

/** The media viewer's full screen (plan 5 "Media viewer"): Esc closes, the arrow keys step through the list; a video
 * plays at once. */
export function FullScreen({
  take,
  onClose,
  onStep,
}: {
  take: Take;
  onClose: () => void;
  onStep: (delta: number) => void;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    ref.current?.showModal();
    // the stage or preview copy behind the dialog stops: one copy of the clip (and its audio) plays
    document.querySelectorAll("video").forEach((v) => {
      if (!ref.current?.contains(v)) v.pause();
    });
  }, []);
  return (
    <dialog
      ref={ref}
      aria-label={take.title}
      onClose={onClose}
      onKeyDown={(e) => {
        if (e.key === "ArrowLeft" || e.key === "ArrowRight") {
          e.preventDefault();
          onStep(e.key === "ArrowLeft" ? -1 : 1);
        }
      }}
      onClick={(e) => e.target === e.currentTarget && ref.current?.close()}
      className="m-0 h-dvh max-h-none w-dvw max-w-none place-items-center bg-black p-0 open:grid backdrop:bg-black"
    >
      <Media
        key={take.id}
        take={take}
        url={safeUrl(take.url)}
        autoPlay
        // a video fills the screen (a 448 × 256 clip would play postage-stamp size); an image keeps its own size
        className={
          take.kind === "video"
            ? "h-dvh w-dvw object-contain"
            : "max-h-dvh max-w-dvw object-contain"
        }
      />
      <button
        type="button"
        aria-label="Close full screen"
        onClick={() => ref.current?.close()}
        className="absolute top-3 right-3 grid size-8 place-items-center rounded-lg bg-s3 text-t1"
      >
        <X aria-hidden size={16} />
      </button>
    </dialog>
  );
}

/** F opens the take shown full screen while `on` (not while typing or in a dialog). */
export function useFullScreenKey(on: boolean, open: () => void) {
  const openRef = useRef(open);
  useEffect(() => {
    openRef.current = open;
  });
  useEffect(() => {
    if (!on) return;
    const onKey = (e: KeyboardEvent) => {
      const el = e.target as HTMLElement;
      if (
        e.key !== "f" ||
        e.metaKey ||
        e.ctrlKey ||
        e.altKey ||
        el.closest("input, textarea, dialog")
      )
        return;
      openRef.current();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [on]);
}
