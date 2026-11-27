"use client";
// The studio's and the runners' state on every page (docs/mock/ErrorStates.dc.html a1, a2, g1, g2): the banner at the
// top of the page column while the studio isn't answering, and the status tile under the rail with its popover while
// something is down. Hidden when all is well.
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import { Boxes, CircleCheck, LoaderCircle, RefreshCw, ServerCrash, Server } from "lucide-react";
import { openMenu, type Item } from "@/components/context-menu";
import { useHosts } from "@/components/create/memory-chip";
import { arm, retryNow, statusOf, useHealth, type StatusRow } from "@/lib/health";

/** The banner: "Studio isn't answering. Retrying… next try in 8 s" with Retry now, then "Back online" for 3 s. */
export function Banner() {
  const h = useHealth();
  useEffect(arm, []);
  const [now, setNow] = useState(0);
  useEffect(() => {
    if (!h.down) return;
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, [h.down]);
  if (!h.down && !h.back) return null;
  if (!h.down)
    return (
      <div role="status" className="ban ok">
        <CircleCheck size={16} aria-hidden />
        <b>Back online</b>
      </div>
    );
  const left = h.nextAt != null && now ? Math.max(0, Math.ceil((h.nextAt - now) / 1000)) : null;
  return (
    <div role="status" className="ban">
      <LoaderCircle size={16} className="spin" aria-hidden />
      <span>
        <b>Studio isn’t answering. Retrying…</b>{" "}
        {left != null && <span className="mono text-t2">next try in {left} s</span>}
      </span>
      <span className="grow" />
      <button type="button" className="chip h-8 bg-s4" onClick={retryNow}>
        <RefreshCw size={14} strokeWidth={1.75} aria-hidden />
        Retry now
      </button>
    </div>
  );
}

/** The Mac app's bridge (desktop/src/ui/studio-preload.js) for this Mac's runners, when it offers them. */
type Mac = {
  localHost?: string;
  restartRunners?: () => void;
  restartRunner?: (env: string) => void;
  showLog?: () => void;
};
const mac = () => (globalThis as { helmDesktop?: Mac }).helmDesktop;

/** The status tile under the rail and its popover: Studio, each runner per host, each NVIDIA host. */
export function StatusTile({ admin }: { admin: boolean }) {
  const down = useHealth().down;
  const hosts = useHosts();
  const router = useRouter();
  const [now, setNow] = useState(0);
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);
  // when the hosts last answered, for "checked 2 s ago" (set as a new answer is seen)
  const [mark, setMark] = useState<{ h: unknown; at: number }>({ h: null, at: 0 });
  if (hosts && now && mark.h !== hosts) setMark({ h: hosts, at: now });
  const { tile, rows } = statusOf(hosts, down, now || Date.parse("2000-01-01"));
  if (!tile || !now) return null;
  const d = mac();
  const ago = mark.at ? Math.max(0, Math.round((now - mark.at) / 1000)) : null;
  const rowMenu = (e: React.MouseEvent, r: StatusRow) => {
    const here = !!r.env && r.host === d?.localHost;
    const items: Item[] = [
      here &&
        d?.restartRunner && {
          label: "Restart runner",
          icon: RefreshCw,
          onPick: () => d.restartRunner!(r.env!),
        },
      here && d?.showLog && { label: "Show log", onPick: () => d.showLog!() },
      admin && {
        label: "Open in Hosts",
        icon: Server,
        onPick: () => router.push("/system?tab=hosts"),
      },
      !admin && { label: "Open Models", icon: Boxes, onPick: () => router.push("/models") },
    ];
    openMenu(e, { title: [r.name, r.sub].filter(Boolean).join(" · "), items });
  };
  return (
    <>
      <button
        type="button"
        popoverTarget="status"
        className={`rail-health ${tile === "Studio down" || tile === "Runner down" || tile === "No runner" ? "bad" : ""}`}
        title={`Status: ${tile}`}
        style={{ anchorName: "--status" } as React.CSSProperties}
      >
        <span className="hdot" aria-hidden />
        <ServerCrash aria-hidden />
        <span className="hl">{tile}</span>
      </button>
      <div
        id="status"
        popover="auto"
        role="dialog"
        aria-label="Status"
        className="pop hpop"
        style={
          {
            positionAnchor: "--status",
            positionArea: "right span-top",
            marginLeft: 10,
          } as React.CSSProperties
        }
      >
        <span className="hh">
          Status{ago != null && <span className="mono">checked {ago} s ago</span>}
        </span>
        {rows.map((r) => (
          <div
            key={r.key}
            className="hrw"
            role="group"
            aria-label={`${r.name}${r.sub ? ` on ${r.sub}` : ""}: ${r.text}`}
            onContextMenu={(e) => rowMenu(e, r)}
          >
            <span className={`sd ${r.dot}`} aria-hidden />
            <span className="hn">
              {r.name}
              {r.sub && <small>{r.sub}</small>}
            </span>
            <span className={`hs ${r.dot === "bad" ? "bad" : ""}`}>{r.text}</span>
          </div>
        ))}
        {!d?.restartRunners && (
          <p className="hint">Runners on a Mac restart from helmstudio there.</p>
        )}
        <div className="hft">
          {d?.restartRunners && (
            <button type="button" className="chip h-8 bg-s4" onClick={() => d.restartRunners!()}>
              <RefreshCw size={14} strokeWidth={1.75} aria-hidden />
              Restart runners
            </button>
          )}
          <Link
            href="/models"
            className="chip h-8 bg-s4"
            onClick={() => document.getElementById("status")?.hidePopover?.()}
          >
            <Boxes size={14} strokeWidth={1.75} aria-hidden />
            Open Models
          </Link>
        </div>
      </div>
    </>
  );
}
