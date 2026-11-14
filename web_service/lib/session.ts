// Server-only: cookies holding the user's tokens, and calls to Kong with the web app's own API key.
import "server-only";
import { readFileSync } from "node:fs";
import type { components } from "@/gen/api";

/** What sign-in, sign-up and refresh answer: both tokens and the user. */
export type Session = components["schemas"]["Session"];

/** Cookie holding the access token. */
export const ACCESS_COOKIE = "ora_at";
/** Cookie holding the refresh token. */
export const REFRESH_COOKIE = "ora_rt";
/** Lifetime of both cookies in seconds: 30 days. */
const REFRESH_TTL = 30 * 24 * 3600;

/** Kong's base URL, without a trailing slash. */
export const kongUrl = () => (process.env.KONG_URL ?? "http://127.0.0.1:8100").replace(/\/$/, "");

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

/** POST a JSON body to an /auth route on behalf of the browser at `ip`; answers {status, body}. */
export async function authPost(path: string, body: unknown, ip?: string) {
  const r = await fetch(`${kongUrl()}${path}`, {
    method: "POST",
    headers: kongHeaders(undefined, { "content-type": "application/json" }, ip),
    body: JSON.stringify(body),
    cache: "no-store",
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
