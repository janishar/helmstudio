"use client";
// The memory chip (docs/mock/Direct.dc.html, PipelineGraph.dc.html: "21 / 64 GB"): the runner hosts, polled, and
// the button with its popover of what each env of the host holds. Create and the pipeline editor show it.
import { useEffect, useState } from "react";
import { Cpu } from "lucide-react";
import { api, ok, type S } from "@/lib/api";
import type { HostMemory } from "@/lib/hosts";

/** The runner hosts, refreshed every 10 s while the tab is visible (they rewrite their state that often) and at once
 * when it shows again; null until the first answer. */
export function useHosts() {
  const [hosts, setHosts] = useState<S<"Host">[] | null>(null);
  useEffect(() => {
    let stop = false;
    const tick = () => {
      if (document.hidden) return;
      ok(api.GET("/v1/studio/hosts"))
        .then((h) => !stop && setHosts(h.hosts))
        .catch(() => undefined);
    };
    tick();
    const t = setInterval(tick, 10_000);
    document.addEventListener("visibilitychange", tick);
    return () => {
      stop = true;
      clearInterval(t);
      document.removeEventListener("visibilitychange", tick);
    };
  }, []);
  return hosts;
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
