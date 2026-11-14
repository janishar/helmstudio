// Server components' first-render reads: straight to Kong with the user's token (no BFF hop), never cached.
import "server-only";
import { cache } from "react";
import { cookies, headers } from "next/headers";
import { redirect } from "next/navigation";
import createClient from "openapi-fetch";
import type { paths } from "@/gen/api";
import { safeNext } from "@/lib/jwt";
import {
  ACCESS_COOKIE,
  PATH_HEADER,
  UPSTREAM_TIMEOUT_MS,
  clientIp,
  kongHeaders,
  kongUrl,
} from "@/lib/session";

/** The typed client for this request: the app's key, the user's token and the browser's IP, as the BFF adds them. */
export const serverApi = cache(async () => {
  const [jar, h] = await Promise.all([cookies(), headers()]);
  return createClient<paths>({
    baseUrl: kongUrl(),
    headers: kongHeaders(jar.get(ACCESS_COOKIE)?.value, undefined, clientIp(h)),
    fetch: (req) =>
      fetch(req, { cache: "no-store", signal: AbortSignal.timeout(UPSTREAM_TIMEOUT_MS) }),
  });
});

/** A first-render read: the data, or null when the API can't answer (the page's client component then loads it
 * itself and shows the error with Retry). A 401 clears the session and goes to /signin, as the (app) layout does. */
export async function initial<T>(
  p: Promise<{ data?: T; error?: unknown; response: Response }>,
): Promise<T | null> {
  const r = await p.catch(() => null);
  if (r?.response.status === 401)
    redirect(
      `/signin/expired?next=${encodeURIComponent(safeNext((await headers()).get(PATH_HEADER)))}`,
    );
  return r?.response.ok ? (r.data ?? null) : null;
}
