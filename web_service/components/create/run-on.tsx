"use client";
// The Run on chip (docs/mock/RunOn.dc.html): right after the model chip on every Create composer. Auto is today's
// placement; a host row is greyed with why it cannot take the run; a chosen host that went offline warns.
import { Monitor, TriangleAlert } from "lucide-react";
import { machineReason, type AddonNeed, type Machine, type VariantPick } from "@/lib/run-on";

export function RunOnChip({
  id = "run-on",
  list,
  chosen,
  env,
  picks,
  needs = [],
  why,
  onPick,
}: {
  id?: string;
  /** the connected machines (lib/run-on machines) */
  list: Machine[];
  /** the machine Run on is set to; undefined = Auto */
  chosen: Machine | undefined;
  /** the workflow's env and chosen variants: what a host row is greyed by */
  env: string | undefined;
  picks: VariantPick[];
  /** the add-ons the nodes that run need (API 1.36): a host without one is greyed ("enhancer not downloaded on gcp") */
  needs?: AddonNeed[];
  /** why no runner can take it (docs/mock/ErrorStates.dc.html b1): the chip warns, its tooltip says it */
  why?: string;
  /** "" = Auto */
  onPick: (host: string) => void;
}) {
  const offline = !!chosen && !chosen.live;
  const warn = offline || !!why;
  const reasons = new Map(list.map((x) => [x.host, machineReason(x, env, picks, needs)]));
  const able = list.filter((x) => !reasons.get(x.host));
  // board e: "Mac · busy"; under Auto only when every machine that can take it is busy
  const busy = chosen ? !!chosen.busy && chosen.live : able.length > 0 && able.every((x) => x.busy);
  const anyBusy = able.some((x) => x.busy);
  const label = `${chosen?.label ?? "Auto"}${busy && !warn ? " · busy" : ""}`;
  const pick = (host: string) => {
    onPick(host);
    document.getElementById(id)?.hidePopover?.();
  };
  return (
    <>
      <button
        type="button"
        popoverTarget={id}
        className={`chip bevel rounded-[10px] ${warn ? "warn" : "bg-s4"}`}
        // the chip says only the machine ("Auto", "NVIDIA"); its name and tooltip say Run on
        aria-label={`Run on ${label}`}
        title={
          offline
            ? `${chosen.host} is offline. Pick Auto, or wait for it to come back.`
            : why
              ? `Run on: ${chosen?.label ?? "Auto"}. ${why}`
              : chosen
                ? `Run on: ${chosen.sub}`
                : "Run on: Auto picks a free machine"
        }
        style={{ anchorName: `--${id}` } as React.CSSProperties}
      >
        {warn ? (
          <TriangleAlert size={14} aria-hidden />
        ) : (
          <Monitor size={14} strokeWidth={1.75} aria-hidden />
        )}
        {label}
      </button>
      <div
        id={id}
        popover="auto"
        role="menu"
        aria-label="Run on"
        className="pop w-85 p-2"
        style={
          {
            positionAnchor: `--${id}`,
            positionArea: "top span-right",
            marginBottom: 6,
          } as React.CSSProperties
        }
      >
        <div className="flex flex-col gap-0.5">
          <span className="heading px-3 pt-1 pb-0.5">Run on</span>
          <button
            type="button"
            role="menuitemradio"
            aria-checked={!chosen}
            className="mi"
            onClick={() => pick("")}
          >
            <span>
              Auto<small>picks a free machine</small>
            </span>
            {/* board b1: "no runner for mlx"; e: the busy machine Auto would wait for */}
            {env && list.length > 0 && !list.some((x) => x.envs.includes(env)) ? (
              <em>no runner for {env}</em>
            ) : (
              busy && <em>{able.map((x) => x.host).join(", ")} busy</em>
            )}
          </button>
          {list.map((x) => {
            const why = reasons.get(x.host);
            return (
              <button
                key={x.host}
                type="button"
                role="menuitemradio"
                aria-checked={x.host === chosen?.host}
                aria-disabled={why ? true : undefined}
                className="mi"
                onClick={() => !why && pick(x.host)}
              >
                <span>
                  {x.label}
                  <small>{x.sub}</small>
                </span>
                {why ? <em>{why}</em> : anyBusy && <em>{x.busy ? "busy" : "free"}</em>}
              </button>
            );
          })}
        </div>
      </div>
    </>
  );
}
