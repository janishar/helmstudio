// "Last frame in…" / "First frame in…" on a video take (API 1.37, docs/mock/H3Options.dc.html frame 7: the Create
// strip, the Gallery, a Timeline clip): makes the frame take (POST …/takes/{id}/frame, contracts/README.md Frames),
// then lists that image take's Use in… offers. Two existing calls, no contract of its own.
"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { api, message, ok, type S } from "@/lib/api";
import { offerLabel } from "@/lib/source";

/** An image take of a video take's first or last frame: at once, or by a media job polled until it ends. */
export async function frameTake(takeId: string, at: "first" | "last"): Promise<string> {
  const r = await ok(
    api.POST("/v1/studio/takes/{take_id}/frame", {
      params: { path: { take_id: takeId } },
      body: { at },
    }),
  );
  if (r.take) return r.take.id;
  let job = r.job!;
  while (job.status === "queued" || job.status === "running") {
    await new Promise((res) => setTimeout(res, 1000));
    job = await ok(
      api.GET("/v1/studio/media-jobs/{job_id}", { params: { path: { job_id: job.id } } }),
    );
  }
  if (job.status !== "succeeded" || !job.take_id)
    throw new Error(job.failure?.detail ?? `Making the ${at} frame ${job.status}.`);
  return job.take_id;
}

type Got = { at: "first" | "last"; frame?: string; offers?: S<"TakeOffer">[]; error?: string };

/** The two actions and, once one is picked, the frame take's offers: each a link (`href`, the Gallery and Timeline:
 * `/?use=<frame>&w=<workflow>`) or a call (`onUse`, Create: the offer applied in place). */
export function FrameIn({
  takeId,
  href,
  onUse,
  className = "act",
  start,
}: {
  takeId: string;
  /** opens this one at once (a clip's right-click menu: Last frame in…) */
  start?: "first" | "last";
  href?: (frameId: string, o: S<"TakeOffer">) => string;
  onUse?: (o: S<"TakeOffer">) => void;
  className?: string;
}) {
  const [got, setGot] = useState<Got | null>(null);
  useEffect(() => {
    if (start) void Promise.resolve().then(() => open(start, true));
    // eslint-disable-next-line react-hooks/exhaustive-deps -- only a new start opens it
  }, [start]);
  async function open(at: "first" | "last", keep = false) {
    if (got?.at === at && !keep) return setGot(null);
    setGot({ at });
    try {
      const frame = await frameTake(takeId, at);
      const r = await ok(
        api.GET("/v1/studio/takes/{take_id}/uses", { params: { path: { take_id: frame } } }),
      );
      setGot((g) => (g?.at === at ? { at, frame, offers: r.uses } : g));
    } catch (e) {
      setGot((g) => (g?.at === at ? { at, error: message(e) } : g));
    }
  }
  const name = got?.at === "last" ? "Last frame" : "First frame";
  return (
    <>
      {(["last", "first"] as const).map((at) => (
        <button
          key={at}
          type="button"
          className={className}
          aria-expanded={got?.at === at}
          onClick={() => void open(at)}
        >
          {at === "last" ? "Last" : "First"} frame in…
        </button>
      ))}
      {got && (
        <div role="menu" aria-label={`${name} in`} className="flex flex-col gap-1 pl-3">
          <span className="px-1 text-[12px] text-t2">{name} in</span>
          {got.error ? (
            <span role="alert" className="mono text-[12px] text-bad">
              {got.error}
            </span>
          ) : !got.offers ? (
            <span className="mono text-[12px] text-t2">Making the {got.at} frame…</span>
          ) : !got.offers.length ? (
            <span className="text-[12px] text-t2">No workflow takes this frame.</span>
          ) : (
            got.offers.map((o) =>
              href ? (
                <Link
                  key={`${o.workflow_id} ${o.param}`}
                  role="menuitem"
                  href={href(got.frame!, o)}
                  className={className}
                >
                  {offerLabel(o)}
                </Link>
              ) : (
                <button
                  key={`${o.workflow_id} ${o.param}`}
                  type="button"
                  role="menuitem"
                  className={className}
                  onClick={() => {
                    setGot(null);
                    onUse?.(o);
                  }}
                >
                  {offerLabel(o)}
                </button>
              ),
            )
          )}
        </div>
      )}
    </>
  );
}
