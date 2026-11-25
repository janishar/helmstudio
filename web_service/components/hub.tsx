"use client";
// Hub (plan 33 37.9; docs/mock/Assistant.dc.html frames 23-26): the admin's one place for everything AI. Its header
// (Hub, the On tag, the Assistant's on/off switch for everyone) stays above the tabs Assistant · In pipelines ·
// Providers · Plugins. The page itself is admin only (app/(app)/hub/page.tsx).
import Link from "next/link";
import { useState } from "react";
import { Blocks } from "lucide-react";
import { ErrorNote } from "@/components/kit";
import { useAssistantApi } from "@/components/shell";
import { message } from "@/lib/api";
import { putAssistantEnabled } from "@/lib/assistant";

export type HubTab = "assistant" | "pipelines" | "providers" | "plugins";

const TABS: [HubTab, string][] = [
  ["assistant", "Assistant"],
  ["pipelines", "In pipelines"],
  ["providers", "Providers"],
  ["plugins", "Plugins"],
];

/** A tab's address: /hub is the Assistant tab. */
export const hubHref = (tab: HubTab) => (tab === "assistant" ? "/hub" : `/hub?tab=${tab}`);

export function HubTabs({ current }: { current: HubTab }) {
  return (
    <div role="tablist" aria-label="Hub sections" className="flex flex-wrap gap-2">
      {TABS.map(([id, label]) => (
        <Link
          key={id}
          href={hubHref(id)}
          role="tab"
          aria-selected={current === id}
          className={`chip ${current === id ? "bg-t1 font-semibold text-panel" : "bevel"}`}
        >
          {label}
        </Link>
      ))}
    </div>
  );
}

export function Switch({
  on,
  label,
  disabled,
  onClick,
}: {
  on: boolean;
  label: string;
  disabled?: boolean;
  onClick?: () => void;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={on}
      aria-label={label}
      disabled={disabled}
      className="sw"
      onClick={onClick}
    >
      <span />
    </button>
  );
}

/** The header every tab shares: Hub, its state, the switch (children), the tabs, then the tab's line. Under .apage. */
export function Head({
  tab,
  chip,
  line,
  children,
}: {
  tab: HubTab;
  chip: "On" | "Off" | "Not set up";
  line: React.ReactNode;
  children?: React.ReactNode;
}) {
  const tone =
    chip === "On"
      ? { boxShadow: "inset 0 0 0 1px var(--a1)", color: "var(--a1)" }
      : chip === "Off"
        ? { boxShadow: "inset 0 0 0 1px var(--ln)", color: "var(--t2)" }
        : { boxShadow: "inset 0 0 0 1px #f2c14e", color: "#f2c14e" };
  return (
    <>
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="pt">
          <i className="lgp" aria-hidden>
            <Blocks size={18} strokeWidth={2} />
          </i>
          Hub
        </h1>
        <span className="stp" style={tone}>
          {chip}
        </span>
        <div className="grow" />
        {children}
      </div>
      <HubTabs current={tab} />
      <div className="text-[13px] text-t2">{line}</div>
    </>
  );
}

/** Head for the tabs that do not load the Assistant's settings: its state from the drawer's status. */
export function HubHead({ tab, line }: { tab: HubTab; line: React.ReactNode }) {
  const a = useAssistantApi();
  const st = a?.status;
  const on = !!st?.enabled;
  const [error, setError] = useState("");
  return (
    <>
      <Head tab={tab} chip={on ? "On" : st?.set_up ? "Off" : "Not set up"} line={line}>
        <Switch
          on={on}
          label="Assistant on"
          disabled={!st?.set_up}
          onClick={() =>
            void putAssistantEnabled(!on).then(
              () => setError(""),
              (e) => setError(message(e)),
            )
          }
        />
      </Head>
      {error && <ErrorNote>{error}</ErrorNote>}
    </>
  );
}
