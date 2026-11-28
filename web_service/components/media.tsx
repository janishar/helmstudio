"use client";
// The media viewer (plan 05-ui "Media viewer"): a take's media, and its full screen: Esc closes, the arrow keys step
// through the list it was opened from, video plays with the native controls.

import { useEffect, useRef, useState } from "react";
import { AudioLines, ImageOff, Mic, Play, X } from "lucide-react";
import { clock, safeUrl } from "@/lib/format";
import { thumbOf } from "@/lib/media";
import { coverOf, soundOf } from "@/lib/takes";
import type { S } from "@/lib/api";

type Take = S<"Take">;

/** A take's (or a run output's) image (its thumbnail, already loaded by the lists, painted under it until the full
 * file arrives: API 1.48), video (native controls, its full-size poster until it plays, fetched with Range requests
 * and only its metadata until then) or audio. */
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
  const [loaded, setLoaded] = useState<string>();
  if (t.kind === "image") {
    const thumb = safeUrl(t.thumb_url);
    const under =
      thumb && thumb !== url && loaded !== url
        ? {
            backgroundImage: `url("${thumb}")`,
            backgroundSize: className.includes("object-cover") ? "cover" : "contain",
            backgroundPosition: "center",
            backgroundRepeat: "no-repeat",
          }
        : undefined;
    return (
      <img
        src={url}
        alt={label}
        className={className}
        style={{ ...under, ...style }}
        fetchPriority="high"
        onLoad={() => setLoaded(url)}
      />
    );
  }
  if (t.kind === "video")
    return (
      <video
        ref={videoRef}
        src={url}
        poster={safeUrl(t.video?.poster_url)}
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

/** The badge's fill and text by class (styles/tokens.css kind-*). */
const KIND_LOOK = {
  music: { label: "Music", k: "var(--kind-music)", on: "var(--on-kind-music)" },
  sound: { label: "Sound", k: "var(--kind-sound)", on: "var(--on-kind-sound)" },
  voice: { label: "Voice", k: "var(--kind-voice)", on: "var(--on-kind-voice)" },
} as const;

/** The vinyl disc that peeks out of a music cover (the board's .disc). */
const DISC: React.CSSProperties = {
  background:
    "radial-gradient(circle, #e9e2f0 0 14%, #0d0b10 14% 16%, transparent 16%), repeating-radial-gradient(circle, #141018 0 2px, #262030 2px 3px)",
  boxShadow: "-6px 0 14px rgba(0,0,0,.45)",
};

/** An audio take's look (docs/mock/CreateMusicSound.dc.html frame 7, owner 2026-11-27): music an album cover (its
 * gradient from the prompt, a vinyl disc, the title), sound and voice one glowing orb (sky, amber); with `badge` the
 * kind badge, the length and the play mark on the card's hover (`group`). */
function SoundLook({
  take: t,
  sound,
  className,
  imgClassName,
  badge,
}: {
  take: Take;
  sound: S<"SoundClass">;
  className?: string;
  imgClassName: string;
  badge: boolean;
}) {
  const look = KIND_LOOK[sound];
  const length = t.audio ? clock(t.audio.duration_s) : "";
  const tag = (
    <span
      className={`absolute rounded-md px-1.5 text-[10px] leading-4 font-semibold ${sound === "music" ? "bottom-[22px] left-2" : "top-1.5 right-1.5"}`}
      style={{ background: look.k, color: look.on }}
    >
      {look.label}
    </span>
  );
  return (
    <span className={`relative block ${className ?? ""}`} data-sound={sound}>
      {sound === "music" ? (
        <span
          aria-hidden
          className={`relative block overflow-hidden ${imgClassName}`}
          style={{ background: coverOf(t) }}
        >
          <span
            className={`absolute top-1/2 aspect-square h-[74%] -translate-y-1/2 rounded-full ${badge ? "-right-[15%]" : "-left-[14%]"}`}
            style={DISC}
          />
          {badge && (
            <span className="absolute top-2.5 right-[35%] left-2.5 line-clamp-2 font-display text-[16px] leading-[18px] font-bold text-white [text-shadow:0_1px_3px_rgba(0,0,0,.45)]">
              {t.title}
            </span>
          )}
        </span>
      ) : (
        <span
          aria-hidden
          className={`relative block overflow-hidden ${imgClassName}`}
          style={{
            background: `radial-gradient(55% 75% at 50% 40%, color-mix(in srgb, ${look.k} 22%, transparent), transparent 72%), var(--s1)`,
          }}
        >
          <span
            className="absolute top-[40%] left-1/2 grid aspect-square h-[42%] max-h-[50px] -translate-1/2 place-items-center rounded-full"
            style={{
              color: look.on,
              background: `radial-gradient(circle at 34% 30%, color-mix(in srgb, ${look.k} 30%, #fff) 0, ${look.k} 42%, color-mix(in srgb, ${look.k} 45%, #000) 100%)`,
              boxShadow: `0 0 0 6px color-mix(in srgb, ${look.k} 16%, transparent), 0 0 30px 4px color-mix(in srgb, ${look.k} 45%, transparent)`,
            }}
          >
            {sound === "sound" ? (
              <AudioLines strokeWidth={2.2} className="size-2/5" />
            ) : (
              <Mic strokeWidth={2.2} className="size-2/5" />
            )}
          </span>
        </span>
      )}
      {badge && (
        <>
          {tag}
          {length && (
            <span
              className={`mono absolute text-[10px] leading-4 ${sound === "music" ? "bottom-1.5 left-2 text-white/85" : "right-0 bottom-2.5 left-0 text-center text-[11px] text-[var(--t1b)]"}`}
            >
              {length}
            </span>
          )}
          <span
            aria-hidden
            className="absolute top-1/2 left-1/2 z-[3] hidden size-9 -translate-1/2 place-items-center rounded-full bg-t1 text-ground shadow-[0_6px_14px_-4px_rgba(0,0,0,.6)] group-hover:grid"
          >
            <Play size={16} className="fill-current" />
          </span>
        </>
      )}
    </span>
  );
}

/** A take's or run output's picture in a list: an image's thumbnail, a video's poster with its length and a play
 * mark (the video itself is never fetched here), an audio take's cover or orb by its class, else its kind. */
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
  const sound = soundOf(t);
  if (sound)
    return (
      <SoundLook
        take={t as Take}
        sound={sound}
        className={className}
        imgClassName={imgClassName}
        badge={badge}
      />
    );
  // a run output's sound tile with its length (docs/mock/VideoSource.dc.html: an uploaded audio take)
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
