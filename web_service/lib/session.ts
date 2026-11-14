// Server-only: cookies holding the user's tokens, and calls to Kong with the web app's own API key.
import "server-only";
import { readFileSync } from "node:fs";
import type { NextRequest } from "next/server";
import type { components } from "@/gen/api";

/** What sign-in and refresh answer: both tokens and the user. */
export type Session = components["schemas"]["Session"];

/** Cookie holding the access token. */
export const ACCESS_COOKIE = "helm_at";
/** Cookie holding the refresh token. */
export const REFRESH_COOKIE = "helm_rt";
/** Lifetime of both cookies in seconds: 30 days. */
const REFRESH_TTL = 30 * 24 * 3600;

/** Kong's base URL, without a trailing slash. */
export const kongUrl = () => (process.env.KONG_URL ?? "http://127.0.0.1:8300").replace(/\/$/, "");

// WEB_API_KEY, or the file WEB_API_KEY_FILE names (a compose secret)
let apiKey: string | undefined;
/** The key, read once and then kept for the life of the process. */
const webApiKey = () =>
  (apiKey ??= process.env.WEB_API_KEY_FILE
    ? readFileSync(process.env.WEB_API_KEY_FILE, "utf8").trim()
    : (process.env.WEB_API_KEY ?? ""));

/** The browser's IP as Kong saw it: Kong sets X-Real-IP and is the only way in, so a browser can't forge it. */
export const clientIp = (h: Headers) => h.get("x-real-ip") ?? undefined;

/** Headers every call to Kong carries: the app's key, the user's bearer token when there is one and the browser's
 *  IP (X-Forwarded-For: Kong trusts it from this app only, so sign-in limits apply per browser, not per web app). */
export function kongHeaders(access?: string, extra?: HeadersInit, ip?: string): Headers {
  const h = new Headers(extra);
  h.set("x-api-key", webApiKey());
  if (access) h.set("authorization", `Bearer ${access}`);
  if (ip) h.set("x-forwarded-for", ip);
  return h;
}

/** Options for the auth cookies: httpOnly, lax, and secure unless COOKIE_SECURE=false. */
export const cookieOptions = (maxAge: number) => ({
  httpOnly: true,
  secure: process.env.COOKIE_SECURE !== "false",
  sameSite: "lax" as const,
  path: "/",
  maxAge,
});

/** The cookies a fresh session sets, as [name, value, options]. */
export function sessionCookies(s: Pick<Session, "access_token" | "refresh_token">) {
  return [
    [ACCESS_COOKIE, s.access_token, cookieOptions(REFRESH_TTL)],
    [REFRESH_COOKIE, s.refresh_token, cookieOptions(REFRESH_TTL)],
  ] as const;
}

/** How long a server-side read from Kong may take before it counts as failed: a hung upstream must not hang the page
 * (the callers' existing fallbacks take over). */
export const UPSTREAM_TIMEOUT_MS = 5_000;
/** authPost's limit, longer: a refresh aborted after auth_service rotated the token would leave the browser holding a
 * spent one, and resending it revokes the whole family. */
const AUTH_POST_TIMEOUT_MS = 15_000;

/** POST a JSON body to an /auth route on behalf of the browser at `ip`; answers {status, body}. */
export async function authPost(path: string, body: unknown, ip?: string) {
  const r = await fetch(`${kongUrl()}${path}`, {
    method: "POST",
    headers: kongHeaders(undefined, { "content-type": "application/json" }, ip),
    body: JSON.stringify(body),
    cache: "no-store",
    signal: AbortSignal.timeout(AUTH_POST_TIMEOUT_MS),
  });
  const text = await r.text();
  let json: unknown = null;
  try {
    json = text ? JSON.parse(text) : null;
  } catch {
    /* non-JSON error page */
  }
  return { status: r.status, body: json };
}

/** An error body's message: `{detail}` or Kong's `{message}`. */
export function detailOf(body: unknown, fallback: string): string {
  const b = body as { detail?: unknown; message?: unknown } | null;
  if (typeof b?.detail === "string") return b.detail;
  if (typeof b?.message === "string") return b.message;
  return fallback;
}

/** The request header that tells server components the page's path and query (Next doesn't expose the URL). */
export const PATH_HEADER = "x-helm-path";

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
