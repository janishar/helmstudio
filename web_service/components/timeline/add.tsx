"use client";
// "Add to timeline" (plan 9.1, 15.1): one take or a multi-selection in order goes to the sequence opened last, or to
// a new one made from them (docs/mock/TimelineDialogs.dc.html "Added to timeline"). Small on purpose: Gallery and
// Create import it, the editor stays lazy.
import { stored } from "@/lib/hydrated";
import Link from "next/link";
import { useState } from "react";
import { api, editTimeline, getTimeline, message, ok } from "@/lib/api";

const LAST = "helm.timeline";

/** The sequence opened last in this browser (a convenience: a cleared store falls back to the newest). */
export const lastTimeline = () => stored.get(LAST);
export const rememberTimeline = (id: string) => stored.set(LAST, id);

/** Adds the takes (in order) to the last opened sequence, else the newest, else a new one made from them. Audio
 * takes (API 1.32) go on its first audio track, at its end; a sequence without one gets A2 first. */
export async function addToTimeline(
  takeIds: string[],
  audio = false,
): Promise<{ id: string; name: string }> {
  const { timelines } = await ok(api.GET("/v1/studio/timelines"));
  const last = lastTimeline();
  const to = timelines.find((t) => t.id === last) ?? timelines[0];
  if (audio) {
    const path = (id: string) => ({ params: { path: { timeline_id: id } } });
    let tl = to
      ? await getTimeline(to.id)
      : await ok(api.POST("/v1/studio/timelines", { body: {} }));
    let track = tl.tracks.find((t) => t.kind === "audio")?.name;
    if (!track) {
      tl = await editTimeline(tl, [{ op: "add_track" }]);
      track = tl.tracks.find((t) => t.kind === "audio")!.name;
    }
    tl = await ok(
      api.POST("/v1/studio/timelines/{timeline_id}/clips", {
        ...path(tl.id),
        body: { take_ids: takeIds, track },
      }),
    );
    rememberTimeline(tl.id);
    return { id: tl.id, name: tl.name };
  }
  const tl = to
    ? await ok(
        api.POST("/v1/studio/timelines/{timeline_id}/clips", {
          params: { path: { timeline_id: to.id } },
          body: { take_ids: takeIds },
        }),
      )
    : await ok(api.POST("/v1/studio/timelines", { body: { take_ids: takeIds } }));
  rememberTimeline(tl.id);
  return { id: tl.id, name: tl.name };
}

/** The button and, once done, the note "Added N clips to <name> · Open". */
export function AddToTimeline({
  takeIds,
  className = "act",
  audio = false,
}: {
  takeIds: string[];
  className?: string;
  /** audio takes: onto an audio track */
  audio?: boolean;
}) {
  const [state, setState] = useState<
    { busy: true } | { id: string; name: string; n: number } | { error: string } | null
  >(null);
  return (
    <>
      <button
        type="button"
        className={className}
        disabled={!takeIds.length || (!!state && "busy" in state)}
        onClick={() => {
          const n = takeIds.length;
          setState({ busy: true });
          addToTimeline(takeIds, audio)
            .then((r) => setState({ ...r, n }))
            .catch((e) => setState({ error: message(e) }));
        }}
      >
        Add to timeline
      </button>
      {state && "id" in state && <AddedNote {...state} />}
      {state && "error" in state && (
        <p role="alert" className="col-span-full m-0 text-[13px] text-bad">
          {state.error}
        </p>
      )}
    </>
  );
}

/** "Added N clips to <name> · Open" (a menu's Add to timeline shows it too). */
export function AddedNote({ id, name, n }: { id: string; name: string; n: number }) {
  return (
    <p role="status" className="col-span-full m-0 text-[13px]">
      Added {n === 1 ? "1 clip" : `${n} clips`} to {name} ·{" "}
      <Link href={`/timeline?t=${encodeURIComponent(id)}`} className="underline">
        Open
      </Link>
    </p>
  );
}
