"use client";
// The frame of every signed-in page: the floating dock on the left, the page beside it.
import Link from "next/link";
import { usePathname } from "next/navigation";
import { createContext, use, useEffect, useRef, useState } from "react";
import {
  Blocks,
  Bot,
  Boxes,
  Clapperboard,
  Images,
  Settings,
  ShieldCheck,
  Sparkles,
  Workflow,
} from "lucide-react";
import {
  AssistantDrawer,
  shortcut,
  useAssistant,
  type AssistantApi,
} from "@/components/assistant/assistant";
import { ToolsContext } from "@/components/assistant/tools";
import { MarkPaths } from "@/components/kit";
import { ApiError, api, ok, type S } from "@/lib/api";

/** The signed-in user (null when auth can't be reached). */
const UserContext = createContext<S<"User"> | null>(null);
/** The signed-in user, for client components under the shell. */
export const useUser = () => use(UserContext);
/** The drawer's state (plan 32: the Assistant page opens it after Save). */
const AssistantContext = createContext<AssistantApi | null>(null);
export const useAssistantApi = () => use(AssistantContext);

/** Dock items: route, label, glyph and the tile's gradient. Hub and System follow for admins (plan 33), as a group. */
const ITEMS = [
  { href: "/", label: "Create", Icon: Sparkles, tile: "linear-gradient(155deg,#a393ff,#4a2fe0)" },
  {
    href: "/pipelines",
    label: "Pipelines",
    Icon: Workflow,
    tile: "linear-gradient(155deg,#6cc4ff,#1f5fe0)",
  },
  {
    href: "/gallery",
    label: "Gallery",
    Icon: Images,
    tile: "linear-gradient(155deg,#ffb07a,#e0457a)",
  },
  {
    href: "/timeline",
    label: "Timeline",
    Icon: Clapperboard,
    tile: "linear-gradient(155deg,#ffe07a,#f08a1c)",
  },
  {
    href: "/models",
    label: "Models",
    Icon: Boxes,
    tile: "linear-gradient(155deg,#7af5d6,#12967f)",
  },
];
/** plan 33 37.9: everything AI (Hub) and the deployment (System), admins only */
const ADMIN_ITEMS = [
  { href: "/hub", label: "Hub", Icon: Blocks, tile: "linear-gradient(155deg,#d8f58c,#3c9a2e)" },
  {
    href: "/system",
    label: "System",
    Icon: ShieldCheck,
    tile: "linear-gradient(155deg,#c9ced9,#5b6478)",
  },
];
type Item = (typeof ITEMS)[number];

/** One page in the dock. */
function DockLink({ it: { href, label, Icon, tile }, path }: { it: Item; path: string }) {
  return (
    <Link
      href={href}
      aria-current={isCurrent(href, path) ? "page" : undefined}
      className="dock-item flex h-16 w-[58px] shrink-0 flex-col items-center justify-center gap-[5px] rounded-2xl text-[10px] font-medium text-t2 aria-[current]:text-t1"
    >
      <span className="tile" style={{ "--tile": tile } as React.CSSProperties}>
        <Icon size={20} strokeWidth={1.75} aria-hidden />
      </span>
      {label}
    </Link>
  );
}

/** True when `path` is the item's page or below it ("/" only matches itself). */
export const isCurrent = (href: string, path: string) =>
  href === "/" ? path === "/" : path === href || path.startsWith(href + "/");

/** The user the layout loaded; when it could not (auth restarting), /auth/me is asked again at 1, 2, 4, 8 and 16 s
 * (about 30 s in all) until it answers. A 401 goes to /signin (`ok`). */
export function useMe(user: S<"User"> | null): S<"User"> | null {
  const [late, setLate] = useState<S<"User"> | null>(null);
  useEffect(() => {
    if (user) return;
    let wait = 1000;
    let timer: ReturnType<typeof setTimeout>;
    const ask = () =>
      ok(api.GET("/auth/me")).then(setLate, (e) => {
        if (!(e instanceof ApiError && e.status === 401) && (wait *= 2) <= 16000)
          timer = setTimeout(ask, wait);
      });
    timer = setTimeout(ask, wait);
    return () => {
      clearTimeout(timer);
      wait = Infinity; // a failure after unmount asks no more
    };
  }, [user]);
  return user ?? late;
}

/** Frame around every signed-in page. The layout renders it with no user when /auth/me failed (auth restarting): it
 * asks again for about 30 s, so the admin items appear without a reload. */
export function AppShell({
  user,
  children,
}: {
  user: S<"User"> | null;
  children: React.ReactNode;
}) {
  const path = usePathname();
  const me = useMe(user);
  const admin = !!me?.roles.includes("admin");
  const a = useAssistant(me);
  const { reg } = a;
  useEffect(() => {
    reg.setPath(path);
  }, [reg, path]);
  const nav = useRef<HTMLElement>(null);
  // the cats (plan 30): a double-click on the logo; their chunk loads on the first one; they leave with Create
  const cats = useRef<typeof import("@/components/cats")>(null);
  useEffect(() => {
    if (path !== "/") cats.current?.stop();
  }, [path]);
  useEffect(() => {
    // a phone's dock is a bar that scrolls sideways: keep the current item (Settings is the last) in view, again once
    // the Assistant item arrives and pushes it along
    nav.current
      ?.querySelector("[aria-current=page]")
      ?.scrollIntoView?.({ block: "nearest", inline: "nearest" });
  }, [path, a.ready]);
  return (
    <UserContext value={me}>
      <AssistantContext value={a}>
        <ToolsContext value={reg}>
          <div
            className="flex min-h-dvh flex-col md:flex-row lg:h-dvh lg:overflow-hidden"
            data-drawer={(a.ready && a.open) || undefined}
          >
            {/* the rail (md+): the logo in its own panel, 14 px above the menu; a phone's bar has no logo. A window too short
                for every item scrolls the menu (owner, 2026-11-25: "make it scoll insted") */}
            <div className="contents md:z-10 md:m-3.5 md:mr-0 md:flex md:max-h-[calc(100dvh-28px)] md:shrink-0 md:flex-col md:gap-3.5 md:self-start">
              <div className="rail-logo panel hidden w-[76px] justify-center rounded-[30px] px-1 py-3.5 md:flex">
                <Link
                  href="/"
                  aria-label="helmstudio, home (Create)"
                  title="helmstudio"
                  // on Create a click is a no-op, so a double-click never navigates twice
                  onClick={(e) => path === "/" && e.preventDefault()}
                  onDoubleClick={() =>
                    void import("@/components/cats").then((m) => (cats.current = m).play())
                  }
                  className="tile text-on1"
                  style={
                    {
                      "--tile": "linear-gradient(155deg, var(--a1), var(--a2))",
                      borderRadius: "12px 12px 12px 5px",
                    } as React.CSSProperties
                  }
                >
                  <svg width={24} height={24} viewBox="0 0 24 24" aria-hidden>
                    <MarkPaths />
                  </svg>
                </Link>
              </div>
              <nav
                ref={nav}
                aria-label="Sections"
                className="panel z-10 m-3.5 mb-0 flex shrink-0 items-center gap-3.5 self-stretch overflow-x-auto rounded-[30px] px-3 py-1 md:m-0 md:min-h-0 md:w-[76px] md:shrink md:flex-col md:self-start md:overflow-y-auto md:overflow-x-hidden md:px-1 md:py-3.5 rail-scroll"
              >
                {ITEMS.map((it) => (
                  <DockLink key={it.href} it={it} path={path} />
                ))}
                {admin && (
                  <div
                    role="group"
                    aria-label="Admin only"
                    className="flex shrink-0 items-center gap-2 rounded-2xl bg-[color-mix(in_srgb,var(--s3)_55%,transparent)] px-1 md:flex-col md:px-0 md:py-1.5"
                  >
                    {ADMIN_ITEMS.map((it) => (
                      <DockLink key={it.href} it={it} path={path} />
                    ))}
                  </div>
                )}
                {a.ready && <span aria-hidden className="h-8 w-px shrink-0 bg-s4 md:h-px md:w-8" />}
                {a.ready && (
                  <button
                    type="button"
                    aria-expanded={a.open}
                    title={`Assistant (${shortcut()})`}
                    onClick={() => a.setOpen((o) => !o)}
                    className="dock-item relative flex h-16 w-[58px] shrink-0 flex-col items-center justify-center gap-[5px] rounded-2xl text-[10px] font-medium text-t2 aria-expanded:text-t1"
                  >
                    <span
                      className="tile"
                      style={
                        {
                          "--tile": "linear-gradient(155deg,#b9f0d0,#2f8f6a)",
                        } as React.CSSProperties
                      }
                    >
                      <Bot size={20} strokeWidth={1.75} aria-hidden />
                    </span>
                    Assistant
                    {a.cardWaits && !a.open && (
                      <span aria-label="1 card waits for you" className="dot" />
                    )}
                  </button>
                )}
                <Link
                  href="/settings"
                  aria-current={isCurrent("/settings", path) ? "page" : undefined}
                  className="dock-item flex h-16 w-[58px] shrink-0 flex-col items-center justify-center gap-[5px] rounded-2xl text-[10px] font-medium text-t2 aria-[current]:text-t1 md:mt-1.5"
                >
                  <span
                    className="tile"
                    style={
                      { "--tile": "linear-gradient(155deg,#a7b0c2,#4b5468)" } as React.CSSProperties
                    }
                  >
                    <Settings size={20} strokeWidth={1.75} aria-hidden />
                  </span>
                  Settings
                </Link>
              </nav>
            </div>
            {/* lg+: the window never scrolls; the dock and the side panel stay put and only <main> scrolls */}
            <div className="flex min-h-0 min-w-0 flex-1 flex-wrap content-start lg:flex-nowrap lg:overflow-hidden lg:[&>.aside]:max-h-[calc(100dvh-28px)] lg:[&>.aside]:shrink-0 lg:[&>.aside]:overflow-y-auto lg:[&>main]:min-h-0 lg:[&>main]:overflow-y-auto">
              {children}
            </div>
          </div>
          {a.ready && <AssistantDrawer a={a} />}
        </ToolsContext>
      </AssistantContext>
    </UserContext>
  );
}
