// Every page and /api call: signed-out users go to /signin?next=…; an expiring access token is refreshed here, once,
// so route handlers and server components always see a usable token in the cookies.
import { NextResponse, type NextRequest } from "next/server";
import { needsRefresh } from "@/lib/jwt";
import {
  ACCESS_COOKIE,
  REFRESH_COOKIE,
  authPost,
  clientIp,
  sessionCookies,
  type Session,
} from "@/lib/session";

/** The library's static pictures (pose samples, home shots, models), cacheable once signed in. Set here and only on a
 * signed-in response: a header in next.config would also make the signed-out redirect to /signin cacheable. */
const LIBRARY = "/catalog/";
const LIBRARY_CACHE = "private, max-age=86400, stale-while-revalidate=604800"; // regenerated pictures keep their names

/** Pages that never need a session. */
const PUBLIC = new Set(["/signin"]); // "/signup" too once sign-up reopens

/** A refresh's result: a new session, "refused" when the token is no good, or "unavailable" when the answer says
 * nothing about the session. */
export type RefreshResult = Session | "refused" | "unavailable";

/** Reads auth_service's answer. Only a 401 means signed out. A 5xx, a rate limit or a broken 200 body doesn't. */
export function refreshResult(status: number, body: unknown): RefreshResult {
  if (status === 200) {
    const session = body as Session | null;
    return session?.access_token && session.refresh_token ? session : "unavailable";
  }
  return status === 401 ? "refused" : "unavailable";
}

// Refresh tokens are single use (reuse revokes the family), so parallel requests share one refresh.
// ponytail: per-process dedupe; with several web replicas, pin sessions or move this into a shared store.
const inflight = new Map<string, Promise<RefreshResult>>();
/** Refreshes a token once: callers within 30 s share the answer, except "unavailable", which the next request retries. */
function refreshOnce(token: string, ip?: string): Promise<RefreshResult> {
  let p = inflight.get(token);
  if (!p) {
    p = authPost("/auth/refresh", { refresh_token: token }, ip)
      .then((r) => refreshResult(r.status, r.body))
      .catch((): RefreshResult => "unavailable")
      .then((result) => {
        if (result === "unavailable") inflight.delete(token);
        return result;
      });
    inflight.set(token, p);
    // Only this attempt's own entry goes: a failed attempt's timer must not drop a later successful refresh, or a
    // parallel request would resend the spent token, which counts as reuse and revokes the family.
    const attempt = p;
    setTimeout(() => {
      if (inflight.get(token) === attempt) inflight.delete(token);
    }, 30_000);
  }
  return p;
}

/** The origin the browser used: behind Kong, X-Forwarded-*; Next would otherwise answer with its own localhost:port. */
export function publicOrigin(req: NextRequest): string {
  const h = req.headers;
  const proto =
    h.get("x-forwarded-proto")?.split(",")[0].trim() || req.nextUrl.protocol.replace(":", "");
  let host = h.get("x-forwarded-host")?.split(",")[0].trim() || h.get("host") || req.nextUrl.host;
  const port = h.get("x-forwarded-port")?.split(",")[0].trim();
  if (
    port &&
    !host.includes(":") &&
    !((proto === "http" && port === "80") || (proto === "https" && port === "443"))
  )
    host += `:${port}`;
  return `${proto}://${host}`;
}

/** Next.js proxy, run before every matched request: public pages and valid tokens pass, an expiring token is
 * refreshed, and a signed-out visitor goes to /signin (a 401 for /api) with both cookies cleared. When auth_service
 * can't answer, the request passes with its cookies kept, so an outage doesn't sign anyone out. */
export async function proxy(req: NextRequest) {
  const { pathname, search } = req.nextUrl;
  // Kong's X-Forwarded-Host has no port (nginx $host) while the browser's Origin has one (127.0.0.1:8080):
  // Next's Server Actions CSRF check compares the two, so hand it the host with the forwarded port.
  if (req.headers.has("x-forwarded-host"))
    req.headers.set("x-forwarded-host", new URL(publicOrigin(req)).host);
  // Carries on with the rewritten headers.
  const next = () => {
    const res = NextResponse.next({ request: { headers: req.headers } });
    if (pathname.startsWith(LIBRARY)) res.headers.set("Cache-Control", LIBRARY_CACHE); // reached only signed in
    return res;
  };
  if (PUBLIC.has(pathname)) return next();
  const access = req.cookies.get(ACCESS_COOKIE)?.value;
  const refresh = req.cookies.get(REFRESH_COOKIE)?.value;
  if (access && !needsRefresh(access)) return next();

  const result = refresh ? await refreshOnce(refresh, clientIp(req.headers)) : "refused";
  if (result === "unavailable") return next();
  if (result !== "refused") {
    req.cookies.set(ACCESS_COOKIE, result.access_token); // this request's handlers see the new token
    req.cookies.set(REFRESH_COOKIE, result.refresh_token);
    const res = next();
    for (const [name, value, options] of sessionCookies(result))
      res.cookies.set(name, value, options);
    return res;
  }

  const res = pathname.startsWith("/api/")
    ? NextResponse.json({ detail: "signed out" }, { status: 401 })
    : NextResponse.redirect(
        new URL(`/signin?next=${encodeURIComponent(pathname + search)}`, publicOrigin(req)),
      );
  res.cookies.delete(ACCESS_COOKIE);
  res.cookies.delete(REFRESH_COOKIE);
  return res;
}

/** Everything except static assets and /files, whose links carry their own signature. */
export const config = {
  matcher: ["/((?!_next/static|_next/image|icon.png|brand/|robots.txt|files/).*)"],
};
