"use client";
// The media viewer (plan 05-ui "Media viewer"): a take's media, and its full screen: Esc closes, the arrow keys step
// through the list it was opened from, video plays with the native controls.

import { useEffect, useRef } from "react";
import { X } from "lucide-react";
import { safeUrl } from "@/lib/format";
import type { S } from "@/lib/api";

type Take = S<"Take">;

/** A take's image, video (native controls) or audio. */
export function Media({
  take: t,
  url,
  className,
}: {
  take: Take;
  url?: string;
  className: string;
}) {
  if (t.kind === "image") return <img src={url} alt={t.title} className={className} />;
  if (t.kind === "video") return <video src={url} controls className={className} />;
  return <audio src={url} controls className="w-full" />;
}

/** The media viewer's full screen (plan 5 "Media viewer"): Esc closes, the arrow keys step through the list. */
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
      <Media take={take} url={safeUrl(take.url)} className="max-h-dvh max-w-dvw object-contain" />
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
