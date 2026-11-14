import { cookies } from "next/headers";
import { redirect } from "next/navigation";
import { AppShell } from "@/components/shell";
import { ACCESS_COOKIE, kongHeaders, kongUrl } from "@/lib/session";
import type { S } from "@/lib/api";

/** Rendered per request: every page depends on the signed-in user. */
export const dynamic = "force-dynamic";

/** Layout of every signed-in page: loads the user for the shell and sends a 401 to /signin. */
export default async function AppLayout({ children }: { children: React.ReactNode }) {
  const access = (await cookies()).get(ACCESS_COOKIE)?.value;
  const r = await fetch(`${kongUrl()}/auth/me`, {
    headers: kongHeaders(access),
    cache: "no-store",
  }).catch(() => null);
  if (r?.status === 401) redirect("/signin");
  const user: S<"User"> | null = r?.ok ? await r.json() : null; // auth unreachable: render, pages show their own errors
  return <AppShell user={user}>{children}</AppShell>;
}
