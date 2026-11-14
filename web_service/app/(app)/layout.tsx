import { cookies, headers } from "next/headers";
import { redirect } from "next/navigation";
import { AppShell } from "@/components/shell";
import { safeNext } from "@/lib/jwt";
import {
  ACCESS_COOKIE,
  PATH_HEADER,
  UPSTREAM_TIMEOUT_MS,
  kongHeaders,
  kongUrl,
} from "@/lib/session";
import type { S } from "@/lib/api";

/** Layout of every signed-in page: loads the user for the shell. A 401 clears the cookies (/signin/expired) and goes
 * to /signin, back to this page afterwards. Rendered per request: it reads cookies(). */
export default async function AppLayout({ children }: { children: React.ReactNode }) {
  const access = (await cookies()).get(ACCESS_COOKIE)?.value;
  const r = await fetch(`${kongUrl()}/auth/me`, {
    headers: kongHeaders(access),
    cache: "no-store",
    signal: AbortSignal.timeout(UPSTREAM_TIMEOUT_MS),
  }).catch(() => null);
  if (r?.status === 401)
    redirect(
      `/signin/expired?next=${encodeURIComponent(safeNext((await headers()).get(PATH_HEADER)))}`,
    );
  // auth unreachable, slow or answering junk: render, pages show their own errors
  const user: S<"User"> | null = r?.ok ? await r.json().catch(() => null) : null;
  return <AppShell user={user}>{children}</AppShell>;
}
