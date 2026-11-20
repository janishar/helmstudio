// Every page and /api call: signed-out users go to /signin?next=…; an expiring access token is refreshed here, once,
// so route handlers and server components always see a usable token in the cookies.
import { NextResponse, type NextRequest } from "next/server";
import { needsRefresh } from "@/lib/jwt";
import {
  ACCESS_COOKIE,
  PATH_HEADER,
  REFRESH_COOKIE,
  authPost,
  clientIp,
  publicOrigin,
  refreshResult,
  sessionCookies,
  type RefreshResult,
} from "@/lib/session";

/** Pages that never need a session (/signin/expired clears a dead session's cookies). */
const PUBLIC = new Set(["/signin", "/signin/expired", "/welcome"]);
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

/** Next.js proxy, run before every matched request: public pages and valid tokens pass, an expiring token is
 * refreshed, and a signed-out visitor sees the landing page at / and goes to /signin from anywhere else (a 401 for
 * /api), with both cookies cleared; a signed-in user at /welcome goes to Create (/). When auth_service
 * can't answer, the request passes with its cookies kept, so an outage doesn't sign anyone out. */
export async function proxy(req: NextRequest) {
  const { pathname, search } = req.nextUrl;
  // Kong's X-Forwarded-Host has no port (nginx $host) while the browser's Origin has one (127.0.0.1:8300):
  // Next's Server Actions CSRF check compares the two, so hand it the host with the forwarded port.
  if (req.headers.has("x-forwarded-host"))
    req.headers.set("x-forwarded-host", new URL(publicOrigin(req)).host);
  req.headers.set(PATH_HEADER, pathname + search); // set, never passed through: the browser can't choose it
  // Carries on with the rewritten headers.
  const next = () => NextResponse.next({ request: { headers: req.headers } });
  const welcome = pathname === "/welcome"; // signed in: Create; signed out: the landing page, cookies as they are
  if (PUBLIC.has(pathname) && !welcome) return next();
  const access = req.cookies.get(ACCESS_COOKIE)?.value;
  const refresh = req.cookies.get(REFRESH_COOKIE)?.value;
  const pass = () => (welcome ? NextResponse.redirect(new URL("/", publicOrigin(req))) : next());
  if (access && !needsRefresh(access)) return pass();
  if (welcome && !refresh) return next();

  const result = refresh ? await refreshOnce(refresh, clientIp(req.headers)) : "refused";
  if (result === "unavailable") return next();
  if (result !== "refused") {
    req.cookies.set(ACCESS_COOKIE, result.access_token); // this request's handlers see the new token
    req.cookies.set(REFRESH_COOKIE, result.refresh_token);
    const res = pass();
    for (const [name, value, options] of sessionCookies(result))
      res.cookies.set(name, value, options);
    return res;
  }
  if (welcome) return next();

  const res = pathname.startsWith("/api/")
    ? NextResponse.json({ detail: "signed out" }, { status: 401 })
    : pathname === "/"
      ? NextResponse.rewrite(new URL("/welcome", req.url), { request: { headers: req.headers } })
      : NextResponse.redirect(
          new URL(`/signin?next=${encodeURIComponent(pathname + search)}`, publicOrigin(req)),
        );
  res.cookies.delete(ACCESS_COOKIE);
  res.cookies.delete(REFRESH_COOKIE);
  return res;
}

/** Everything except static assets and /files, whose links carry their own signature. */
export const config = {
  matcher: ["/((?!_next/static|_next/image|icon.svg|robots.txt|files/).*)"],
};
