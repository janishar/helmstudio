"use client";
// The frame of every signed-in page. "/" is Catalog Agent full screen (its own rail carries the account links);
// every other page (Dashboard, Account, the dev Jobs view) gets a header bar: the mark, the nav, the theme toggle and the user menu.
import Link from "next/link";
import { usePathname } from "next/navigation";
import { createContext, use } from "react";
import { useTheme } from "next-themes";
import { LayoutDashboard, ListTree, LogOut, Monitor, Moon, Sun, User, Users } from "lucide-react";
import { cn } from "cn";
import { BetaBadge } from "@/components/kit";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { signout } from "@/app/(auth)/actions";
import type { S } from "@/lib/api";

/** The signed-in user (null when auth can't be reached). */
const UserContext = createContext<S<"User"> | null>(null);
/** The signed-in user, for client components under the shell. */
export const useUser = () => use(UserContext);
/** Whether to offer the dev views (Jobs): web_service's DEV_VIEWS and a user with the dev or admin role. */
const DevContext = createContext(false);
/** Whether to offer the dev views, for client components under the shell. */
export const useDevViews = () => use(DevContext);

/** The header's links. */
const NAV = [
  { href: "/", label: "Catalog agent" },
  { href: "/dashboard", label: "Dashboard" },
  { href: "/account", label: "Account" },
];

/** Frame around every signed-in page; `user` is null when auth can't be reached, `dev` (DEV_VIEWS) turns on the dev
 * views for users with the dev or admin role. */
export function AppShell({
  user,
  dev,
  children,
}: {
  user: S<"User"> | null;
  dev: boolean;
  children: React.ReactNode;
}) {
  const path = usePathname();
  const { theme, setTheme } = useTheme();
  dev &&= !!user?.roles.some((r) => r === "dev" || r === "admin");
  if (path === "/" || path.startsWith("/sessions/"))
    return (
      <UserContext value={user}>
        <DevContext value={dev}>
          <main id="main" className="dark h-dvh bg-surface-frame p-2 text-gray-100">
            {children}
          </main>
        </DevContext>
      </UserContext>
    );
  const initial = (user?.name || user?.email || "?").charAt(0).toUpperCase();
  return (
    <UserContext value={user}>
      <DevContext value={dev}>
        <div className="min-h-dvh bg-background">
          <header className="sticky top-0 z-30 flex h-14 items-center gap-2 border-b bg-bar px-4">
            <Link href="/" className="flex items-center gap-2 pr-2">
              <img src="/brand/mark.png" alt="" className="size-7" />
              <span className="hidden text-[15px] font-bold tracking-tight sm:inline">
                ORA Catalog Agent
              </span>
              <BetaBadge />
            </Link>
            <nav aria-label="Main" className="flex min-w-0 flex-1 gap-1 overflow-x-auto">
              {NAV.map((n) => (
                <Link
                  key={n.href}
                  href={n.href}
                  aria-current={path === n.href ? "page" : undefined}
                  className={cn(
                    "rounded-lg px-3 py-1.5 text-sm font-medium whitespace-nowrap transition-colors",
                    path === n.href
                      ? "bg-sidebar-accent text-sidebar-accent-foreground"
                      : "text-muted-foreground hover:text-foreground",
                  )}
                >
                  {n.label}
                </Link>
              ))}
            </nav>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" size="icon" aria-label="Theme">
                  <Sun className="dark:hidden" />
                  <Moon className="hidden dark:block" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuRadioGroup value={theme} onValueChange={setTheme}>
                  <DropdownMenuRadioItem value="light">
                    <Sun /> Light
                  </DropdownMenuRadioItem>
                  <DropdownMenuRadioItem value="dark">
                    <Moon /> Dark
                  </DropdownMenuRadioItem>
                  <DropdownMenuRadioItem value="system">
                    <Monitor /> System
                  </DropdownMenuRadioItem>
                </DropdownMenuRadioGroup>
              </DropdownMenuContent>
            </DropdownMenu>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" size="icon" aria-label="Account menu">
                  <span
                    aria-hidden
                    className="grid size-7 place-items-center rounded-full bg-accent text-xs font-semibold text-accent-foreground"
                  >
                    {initial}
                  </span>
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-56">
                <DropdownMenuLabel className="truncate font-normal">
                  <span className="block font-medium">{user?.name}</span>
                  <span className="block truncate text-xs text-muted-foreground">
                    {user?.email}
                  </span>
                </DropdownMenuLabel>
                <DropdownMenuSeparator />
                <DropdownMenuItem asChild>
                  <Link href="/dashboard">
                    <LayoutDashboard /> Dashboard
                  </Link>
                </DropdownMenuItem>
                <DropdownMenuItem asChild>
                  <Link href="/account">
                    <User /> Account
                  </Link>
                </DropdownMenuItem>
                {dev && (
                  <DropdownMenuItem asChild>
                    <Link href="/dev/jobs">
                      <ListTree /> Jobs (dev)
                    </Link>
                  </DropdownMenuItem>
                )}
                {user?.roles.includes("admin") && (
                  <DropdownMenuItem asChild>
                    <Link href="/admin/users">
                      <Users /> Users (admin)
                    </Link>
                  </DropdownMenuItem>
                )}
                <DropdownMenuSeparator />
                <DropdownMenuItem onSelect={() => signout()}>
                  <LogOut /> Sign out
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </header>
          <main id="main" className="mx-auto w-full max-w-6xl px-4 py-6 md:px-8 md:py-8">
            {children}
          </main>
        </div>
      </DevContext>
    </UserContext>
  );
}
