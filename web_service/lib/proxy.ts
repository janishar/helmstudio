// BFF pass-through to Kong: responses (zips, SSE) are streamed. Request bodies are read whole first: with proxy.ts
// present, Next drops 16 KB chunks from a streamed req.body (seen on 15.5: uploads arrived corrupt and short of their
// Content-Length). proxy.ts buffers an /api body only up to proxyClientMaxBodySize (10 MB by default) and cuts the
// rest, so the /api route refuses larger bodies (MAX_BODY); byte uploads go through /files, which proxy.ts skips.
import "server-only";
import type { NextRequest } from "next/server";
import { ACCESS_COOKIE, clientIp, kongHeaders, kongUrl } from "@/lib/session";

/** The largest /api request body: Next's default proxyClientMaxBodySize. Raise both together if an endpoint needs more. */
export const MAX_BODY = 10 * 1024 * 1024;

/** The browser's request headers that reach Kong; all others stay here. */
const REQUEST_HEADERS = ["content-type", "accept", "last-event-id", "range", "if-none-match"];
/** Kong's response headers that don't reach the browser. */
const DROP_RESPONSE = new Set([
  "content-encoding",
  "content-length",
  "transfer-encoding",
  "connection",
  "keep-alive",
  "set-cookie",
  // Kong's version and topology stay inside (x-kong-* by prefix below)
  "server",
  "via",
]);

/** Forwards a request to Kong and streams the answer back, or a 502 when Kong can't be reached. `withAuth` adds
 * the app's key, the user's token and the browser's IP. */
export async function proxy(req: NextRequest, path: string, withAuth: boolean): Promise<Response> {
  const headers = withAuth
    ? kongHeaders(req.cookies.get(ACCESS_COOKIE)?.value, undefined, clientIp(req.headers))
    : new Headers();
  for (const h of REQUEST_HEADERS) {
    const v = req.headers.get(h);
    if (v) headers.set(h, v);
  }
  const body =
    req.method !== "GET" && req.method !== "HEAD" && req.body ? await req.arrayBuffer() : undefined;
  let upstream: Response;
  try {
    upstream = await fetch(`${kongUrl()}${path}${req.nextUrl.search}`, {
      method: req.method,
      headers,
      body,
      redirect: "manual",
      cache: "no-store",
      signal: req.signal,
    });
  } catch (err) {
    console.error(`proxy ${req.method} ${path}:`, err);
    return Response.json({ detail: "the API is unreachable" }, { status: 502 });
  }
  const out = new Headers();
  upstream.headers.forEach(
    (v, k) => !DROP_RESPONSE.has(k) && !k.startsWith("x-kong-") && out.set(k, v),
  );
  // an uncompressed body arrives as sent, so its length holds (a <video>'s Range answers want it)
  const length = upstream.headers.get("content-length");
  if (length && !upstream.headers.has("content-encoding")) out.set("content-length", length);
  // the user's own data: no browser or shared cache keeps it unless the API says otherwise
  if (withAuth && !out.has("cache-control")) out.set("cache-control", "private, no-store");
  return new Response(upstream.body, {
    status: upstream.status,
    statusText: upstream.statusText,
    headers: out,
  });
}

/** The BFF only exposes the API, the signed-in user's own auth routes and the admin's user management (auth_service
 * answers 403 without the admin role); sign-in/refresh/out are server actions, API keys never pass. */
export function allowed(path: string): boolean {
  // fetch would resolve dot segments (/v1/../auth/…), and Kong may decode an escaped / or % into one
  const bad = (s: string) => {
    try {
      const d = decodeURIComponent(s);
      return d === "." || d === ".." || /[/%]/.test(d);
    } catch {
      return true;
    }
  };
  if (path.split("/").some(bad)) return false;
  return (
    // exactly Kong's studio route: any other /v1 path falls through to Kong's catch-all (this app)
    /^\/v1\/studio\//.test(path) ||
    path === "/auth/me" ||
    path === "/auth/password" ||
    /^\/auth\/admin\/users(\/[0-9a-f-]{36}\/(roles|disabled|password))?$/.test(path)
  );
}
