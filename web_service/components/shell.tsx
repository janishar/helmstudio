"use client";
// The frame of every signed-in page: the floating dock on the left, the page beside it.
import Link from "next/link";
import { usePathname } from "next/navigation";
import { createContext, use } from "react";
import {
  Boxes,
  Clapperboard,
  Images,
  LogOut,
  Settings,
  ShieldCheck,
  Sparkles,
  Workflow,
} from "lucide-react";
import { ThemeSelect } from "@/components/kit";
import { signout } from "@/app/(auth)/actions";
import type { S } from "@/lib/api";

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

/** Frame around every signed-in page. */
export function AppShell({
  user,
  children,
}: {
  user: S<"User"> | null;
  children: React.ReactNode;
}) {
  const path = usePathname();
  const items = user?.roles.includes("admin") ? [...ITEMS, ADMIN] : ITEMS;
  return (
    <UserContext value={user}>
      <div className="flex min-h-dvh flex-col md:flex-row">
        <nav
          aria-label="Sections"
          className="panel z-10 m-3.5 mb-0 flex shrink-0 items-center gap-3.5 self-stretch overflow-x-auto rounded-[30px] px-3 py-1 md:mb-3.5 md:w-[68px] md:flex-col md:self-start md:overflow-visible md:px-1 md:py-3.5"
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
          <SettingsMenu user={user} />
        </nav>
        <div className="flex min-w-0 flex-1 flex-wrap content-start">{children}</div>
      </div>
    </UserContext>
  );
}

/** The Settings tile: who is signed in, the theme and Sign out. */
function SettingsMenu({ user }: { user: S<"User"> | null }) {
  return (
    <>
      <button
        type="button"
        popoverTarget="settings"
        className="dock-item flex h-16 w-[58px] shrink-0 flex-col items-center justify-center gap-[5px] text-[10px] font-medium text-t2 md:mt-1.5"
        style={{ anchorName: "--settings" } as React.CSSProperties}
      >
        <span
          className="tile"
          style={{ "--tile": "linear-gradient(155deg,#a7b0c2,#4b5468)" } as React.CSSProperties}
        >
          <Settings size={20} strokeWidth={1.75} aria-hidden />
        </span>
        Settings
      </button>
      <div
        id="settings"
        popover="auto"
        role="dialog"
        aria-label="Settings"
        className="pop w-[240px]"
        style={
          {
            positionAnchor: "--settings",
            positionArea: "right span-top",
            marginLeft: 8,
            positionTryFallbacks: "flip-block, bottom",
          } as React.CSSProperties
        }
      >
        <div className="flex flex-col gap-2">
          {user && (
            <div className="mb-1 min-w-0">
              <div className="truncate font-semibold">{user.name}</div>
              <div className="mono truncate text-[11px] text-t2">{user.email}</div>
            </div>
          )}
          <label htmlFor="theme" className="text-[13px]">
            Theme
          </label>
          <ThemeSelect id="theme" />
          <form action={signout} className="mt-2">
            <button type="submit" className="chip bevel w-full justify-center bg-s4">
              <LogOut size={16} aria-hidden /> Sign out
            </button>
          </form>
        </div>
      </div>
    </>
  );
}
