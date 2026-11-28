"use client";
// The memory chip (docs/mock/Direct.dc.html, PipelineGraph.dc.html: "21 / 64 GB"): the runner hosts, polled, and
// the button with its popover of what each env of the host holds. Create and the pipeline editor show it.
import { useEffect, useState, useSyncExternalStore } from "react";
import { Cpu } from "lucide-react";
import { api, ok, type S } from "@/lib/api";
import type { HostMemory } from "@/lib/hosts";
import { onBack } from "@/lib/health";

/** One hosts poll for every part of the page that reads them (Create, Magic, the Timeline and the status tile under
 * the rail: docs/mock/ErrorStates.dc.html g1, "no new call"); it runs while one reads it and forgets when none does. */
const shared = {
  hosts: null as S<"Host">[] | null,
  subs: new Set<() => void>(),
  /** bumped when the last reader goes: an answer for an earlier round is dropped */
  round: 0,
  stop: () => undefined as void,
};
function tick() {
  if (document.hidden) return;
  const round = shared.round;
  ok(api.GET("/v1/studio/hosts"))
    .then((h) => {
      if (round !== shared.round) return;
      shared.hosts = h.hosts;
      shared.subs.forEach((f) => f());
    })
    .catch(() => undefined);
}
function subscribe(f: () => void) {
  shared.subs.add(f);
  if (shared.subs.size === 1) {
    const t = setInterval(tick, 10_000);
    document.addEventListener("visibilitychange", tick);
    // the studio is back (lib/health.ts): read them at once
    const off = onBack(tick);
    shared.stop = () => {
      clearInterval(t);
      off();
      document.removeEventListener("visibilitychange", tick);
    };
  }
  tick(); // a new reader reads them now (a page opened since the last poll)
  return () => {
    shared.subs.delete(f);
    if (shared.subs.size) return;
    shared.stop();
    shared.round++;
    shared.hosts = null;
  };
}

/** The runner hosts, refreshed every 10 s while the tab is visible (they rewrite their state that often) and at once
 * when it shows again; null until the first answer. */
export function useHosts() {
  return useSyncExternalStore(
    subscribe,
    () => shared.hosts,
    () => null,
  );
}

/** The planned peak in GB of the run `body` would make (POST /v1/studio/runs/plan, API 1.25), asked 400 ms after the
 * form settles; the workflow's last answer stays until the next. Null before one, with no plan (the whole host budget) and when
 * the form does not resolve yet (its 422s are Generate's to show). */
export function usePlannedPeak(body: S<"RunRequest"> | null) {
  return usePlan(body).gb;
}

/** usePlannedPeak with the host the plan is for (Auto's host when `body` has none; null = none or no answer yet) and
 * (docs/mock/ErrorStates.dc.html b1) the plan's `reason`: why no host is eligible, for this very request only.
 * `again` asks once more when it changes (the live hosts: a runner that comes up or stops). */
export function usePlan(
  body: S<"RunRequest"> | null,
  again = "",
): { gb: number | null; host: string | null; reason: string | null } {
  const key = body && JSON.stringify(body);
  // the answer and the workflow it was for: another workflow's never shows while this one's is asked
  const [got, setGot] = useState<{
    wf: string;
    key: string;
    gb: number | null;
    host: string | null;
    reason: string | null;
  } | null>(null);
  useEffect(() => {
    if (!key) return;
    const req = JSON.parse(key) as S<"RunRequest">;
    let stop = false;
    const t = setTimeout(() => {
      ok(api.POST("/v1/studio/runs/plan", { body: req }))
        .then(
          (p) =>
            !stop &&
            setGot({
              wf: req.workflow_id,
              key,
              gb: p.peak_gb,
              host: p.host,
              reason: p.reason ?? null,
            }),
        )
        .catch(
          () => !stop && setGot({ wf: req.workflow_id, key, gb: null, host: null, reason: null }),
        );
    }, 400);
    return () => {
      stop = true;
      clearTimeout(t);
    };
  }, [key, again]);
  return body && got?.wf === body.workflow_id
    ? { gb: got.gb, host: got.host, reason: got.key === key ? got.reason : null }
    : { gb: null, host: null, reason: null };
}

/** The Generate button's estimate (docs/mock/TextToVideo.dc.html "≈ 40 s · 21 GB"): the last run's time, then the
 * planned peak; null with neither. `upTo`: a choice switched nodes off, and the plan is the whole workflow's (it has
 * no figure per stage), so the peak is a ceiling: "up to 46 GB". */
export function estimateText(took: string | null, gb: number | null, upTo = false) {
  const mem = gb != null ? `${upTo ? "up to " : ""}${Math.round(gb)} GB` : "";
  return took ? `≈ ${[took, mem].filter(Boolean).join(" · ")}` : mem || null;
}

/** The memory button: used / budget of the host that runs the workflow, across its envs; its popover lists what
 * each env holds. */
export function Memory({ memory }: { memory: HostMemory }) {
  const resident = memory.envs.flatMap((e) => e.resident ?? []);
  const gb = (n: number) => +n.toFixed(1);
  return (
    <>
      <button
        type="button"
        popoverTarget="memory"
        aria-label={`Memory on ${memory.host}: ${gb(memory.used_gb)} of ${gb(memory.budget_gb)} gigabytes used, ${resident.length} loaded${memory.busy ? ", busy" : ""}`}
        className="bevel mono flex h-10 items-center gap-2 rounded-[10px] border border-s3 bg-s1 px-3 text-[12px]"
        style={{ anchorName: "--memory" } as React.CSSProperties}
      >
        <Cpu size={16} strokeWidth={1.75} aria-hidden />
        {Math.round(memory.used_gb)} / {Math.round(memory.budget_gb)} GB
      </button>
      <div
        id="memory"
        popover="auto"
        role="dialog"
        aria-label="Memory"
        className="pop w-72 text-[13px]"
        style={
          {
            positionAnchor: "--memory",
            positionArea: "bottom span-left",
            marginTop: 6,
          } as React.CSSProperties
        }
      >
        <div className="flex flex-col gap-2">
          <span className="heading">
            {memory.host} · {gb(memory.used_gb)} of {gb(memory.budget_gb)} GB
            {memory.busy ? " · busy" : ""}
          </span>
          {memory.envs.map((e) => (
            <div key={e.env} className="flex flex-col gap-1">
              <span className="text-t2">
                {e.env} · {e.state}
              </span>
              {e.resident?.length ? (
                e.resident.map((r, i) => (
                  <div key={i} className="flex justify-between gap-3">
                    <span className="min-w-0 truncate">
                      {r.component} · {r.variant} · {r.precision}
                      {r.loras?.length ? ` · ${r.loras.length} LoRA` : ""}
                    </span>
                    <span className="mono text-t2">{gb(r.gb)} GB</span>
                  </div>
                ))
              ) : (
                <span className="text-t2">Nothing loaded.</span>
              )}
            </div>
          ))}
        </div>
      </div>
    </>
  );
}
