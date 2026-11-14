// Edge- and browser-safe helpers with no Node imports (used by proxy.ts, the auth actions and tests).

/** The `exp` (unix seconds) of a JWT, or null when the token isn't a readable JWT. Never verifies: Kong/api do. */
export function jwtExp(token: string | undefined): number | null {
  const part = token?.split(".")[1];
  if (!part) return null;
  try {
    const json = atob(part.replace(/-/g, "+").replace(/_/g, "/"));
    const exp = JSON.parse(json).exp;
    return typeof exp === "number" ? exp : null;
  } catch {
    return null;
  }
}

/** True when the access token must be refreshed before use (missing, or expiring within `skew` seconds). */
export function needsRefresh(
  access: string | undefined,
  now = Date.now() / 1000,
  skew = 30,
): boolean {
  if (!access) return true;
  const exp = jwtExp(access);
  return exp !== null && exp - skew <= now; // an opaque token is used as-is; a 401 sends the user to sign in
}

/** Only same-site relative paths are allowed as a post-sign-in target (no open redirects). A control character or
 * backslash anywhere is refused (URL parsers drop or rewrite them, so "/\t/evil.com" means //evil.com); the rest must
 * resolve on our own origin. */
export function safeNext(next: string | null | undefined): string {
  if (!next || !next.startsWith("/") || /[\u0000-\u001f\u007f\\]/.test(next)) return "/";
  const url = new URL(next, "http://x");
  return url.origin === "http://x" ? url.pathname + url.search + url.hash : "/";
}
