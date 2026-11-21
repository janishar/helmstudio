"use client";
// The Run on chip (docs/mock/RunOn.dc.html): right after the model chip on every Create composer. Auto is today's
// placement; a host row is greyed with why it cannot take the run; a chosen host that went offline warns.
import { Monitor, TriangleAlert } from "lucide-react";
import { machineReason, type Machine } from "@/lib/run-on";
import type { S } from "@/lib/api";

export function RunOnChip({
  id = "run-on",
  list,
  chosen,
  env,
  picks,
  onPick,
}: {
  id?: string;
  /** the connected machines (lib/run-on machines) */
  list: Machine[];
  /** the machine Run on is set to; undefined = Auto */
  chosen: Machine | undefined;
  /** the workflow's env and chosen variants: what a host row is greyed by */
  env: string | undefined;
  picks: { m: S<"Model">; v: S<"ModelVariant"> }[];
  /** "" = Auto */
  onPick: (host: string) => void;
}) {
  const offline = !!chosen && !chosen.live;
  const label = chosen?.label ?? "Auto";
  const pick = (host: string) => {
    onPick(host);
    document.getElementById(id)?.hidePopover?.();
  };
  return (
    <>
      <button
        type="button"
        popoverTarget={id}
        className={`chip bevel rounded-[10px] ${offline ? "warn" : "bg-s4"}`}
        // the chip says only the machine ("Auto", "NVIDIA"); its name and tooltip say Run on
        aria-label={`Run on ${label}`}
        title={
          offline
            ? `${chosen.host} is offline. Pick Auto, or wait for it to come back.`
            : chosen
              ? `Run on: ${chosen.sub}`
              : "Run on: Auto picks a free machine"
        }
        style={{ anchorName: `--${id}` } as React.CSSProperties}
      >
        {offline ? (
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
          </button>
          {list.map((x) => {
            const why = machineReason(x, env, picks);
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
                {why && <em>{why}</em>}
              </button>
            );
          })}
        </div>
      </div>
    </>
  );
}
