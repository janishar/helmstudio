"use client";
// "Add to timeline" (plan 9.1, 15.1): one take or a multi-selection in order goes to the sequence opened last, or to
// a new one made from them (docs/mock/TimelineDialogs.dc.html "Added to timeline"). Small on purpose: Gallery and
// Create import it, the editor stays lazy.
import Link from "next/link";
import { useState } from "react";
import { api, message, ok } from "@/lib/api";

const LAST = "helm.timeline";

/** The sequence opened last in this browser (a convenience: a cleared store falls back to the newest). */
export function lastTimeline(): string | null {
  try {
    return localStorage.getItem(LAST);
  } catch {
    return null;
  }
}
export function rememberTimeline(id: string) {
  try {
    localStorage.setItem(LAST, id);
  } catch {
    /* private window */
  }
}

/** Adds the takes (in order) to the last opened sequence, else the newest, else a new one made from them. */
export async function addToTimeline(takeIds: string[]): Promise<{ id: string; name: string }> {
  const { timelines } = await ok(api.GET("/v1/studio/timelines"));
  const last = lastTimeline();
  const to = timelines.find((t) => t.id === last) ?? timelines[0];
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
}: {
  takeIds: string[];
  className?: string;
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
          addToTimeline(takeIds)
            .then((r) => setState({ ...r, n }))
            .catch((e) => setState({ error: message(e) }));
        }}
      >
        Add to timeline
      </button>
      {state && "id" in state && (
        <p role="status" className="col-span-full m-0 text-[13px]">
          Added {state.n === 1 ? "1 clip" : `${state.n} clips`} to {state.name} ·{" "}
          <Link href={`/timeline?t=${encodeURIComponent(state.id)}`} className="underline">
            Open
          </Link>
        </p>
      )}
      {state && "error" in state && (
        <p role="alert" className="col-span-full m-0 text-[13px] text-bad">
          {state.error}
        </p>
      )}
    </>
  );
}
