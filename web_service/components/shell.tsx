"use client";
// The frame of every signed-in page: the floating dock on the left, the page beside it.
import Link from "next/link";
import { usePathname } from "next/navigation";
import { createContext, use, useEffect, useRef, useState } from "react";
import {
  Boxes,
  Clapperboard,
  Images,
  Settings,
  ShieldCheck,
  Sparkles,
  Workflow,
} from "lucide-react";
import { AssistantDrawer, shortcut, useAssistant } from "@/components/assistant/assistant";
import { ToolsContext } from "@/components/assistant/tools";
import { ApiError, api, ok, type S } from "@/lib/api";

/** The signed-in user (null when auth can't be reached). */
const UserContext = createContext<S<"User"> | null>(null);
/** The signed-in user, for client components under the shell. */
export const useUser = () => use(UserContext);

/** Dock items: route, label, glyph and the tile's gradient. Admin is added for admins. */
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
const ADMIN = {
  href: "/admin",
  label: "Admin",
  Icon: ShieldCheck,
  tile: "linear-gradient(155deg,#c9ced9,#5b6478)",
};

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
  const items = me?.roles.includes("admin") ? [...ITEMS, ADMIN] : ITEMS;
  const a = useAssistant(me);
  const { reg } = a;
  useEffect(() => {
    reg.setPath(path);
  }, [reg, path]);
  const nav = useRef<HTMLElement>(null);
  useEffect(() => {
    // a phone's dock is a bar that scrolls sideways: keep the current item (Settings is the last) in view, again once
    // the Assistant item arrives and pushes it along
    nav.current
      ?.querySelector("[aria-current=page]")
      ?.scrollIntoView?.({ block: "nearest", inline: "nearest" });
  }, [path, a.enabled]);
  return (
    <UserContext value={me}>
      <ToolsContext value={reg}>
        <div
          className="flex min-h-dvh flex-col md:flex-row lg:h-dvh lg:overflow-hidden"
          data-drawer={(a.enabled && a.open) || undefined}
        >
          <nav
            ref={nav}
            aria-label="Sections"
            className="panel z-10 m-3.5 mb-0 flex shrink-0 items-center gap-3.5 self-stretch overflow-x-auto rounded-[30px] px-3 py-1 md:mr-0 md:mb-3.5 md:w-[76px] md:flex-col md:self-start md:overflow-visible md:px-1 md:py-3.5"
          >
            {items.map(({ href, label, Icon, tile }) => (
              <Link
                key={href}
                href={href}
                aria-current={isCurrent(href, path) ? "page" : undefined}
                className="dock-item flex h-16 w-[58px] shrink-0 flex-col items-center justify-center gap-[5px] rounded-2xl text-[10px] font-medium text-t2 aria-[current]:text-t1"
              >
                <span className="tile" style={{ "--tile": tile } as React.CSSProperties}>
                  <Icon size={20} strokeWidth={1.75} aria-hidden />
                </span>
                {label}
              </Link>
            ))}
            {a.enabled && (
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
                    { "--tile": "linear-gradient(155deg,#d8f58c,#3c9a2e)" } as React.CSSProperties
                  }
                >
                  <Sparkles size={20} strokeWidth={1.75} aria-hidden />
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
          {/* lg+: the window never scrolls; the dock and the side panel stay put and only <main> scrolls */}
          <div className="flex min-h-0 min-w-0 flex-1 flex-wrap content-start lg:flex-nowrap lg:overflow-hidden lg:[&>.aside]:max-h-[calc(100dvh-28px)] lg:[&>.aside]:shrink-0 lg:[&>.aside]:overflow-y-auto lg:[&>main]:min-h-0 lg:[&>main]:overflow-y-auto">
            {children}
          </div>
        </div>
        {a.enabled && <AssistantDrawer a={a} />}
      </ToolsContext>
    </UserContext>
  );
}
