"use server";
import { cookies, headers } from "next/headers";
import { redirect } from "next/navigation";
import { safeNext } from "@/lib/jwt";
import {
  ACCESS_COOKIE,
  REFRESH_COOKIE,
  authPost,
  clientIp,
  detailOf,
  kongHeaders,
  kongUrl,
  sessionCookies,
  UPSTREAM_TIMEOUT_MS,
  type Session,
} from "@/lib/session";

/** A form's result: the error, plus what was typed so the form can keep it. */
export type AuthState = { error?: string; email?: string } | undefined;

/** Stores a new session's tokens in the cookies. */
async function startSession(s: Session) {
  const jar = await cookies();
  for (const [name, value, options] of sessionCookies(s)) jar.set(name, value, options);
}

/** Sign-in form action: sets the cookies and redirects to `next`, or returns the error. */
export async function signin(_: AuthState, fd: FormData): Promise<AuthState> {
  const email = String(fd.get("email") ?? "").trim();
  const password = String(fd.get("password") ?? "");
  if (!email || !password) return { error: "Enter your email and password.", email };
  const r = await authPost("/auth/signin", { email, password }, clientIp(await headers())).catch(
    () => null,
  );
  if (!r) return { error: "Sign-in is unavailable right now. Try again shortly.", email };
  if (r.status !== 200) return { error: detailOf(r.body, "Sign-in failed."), email };
  await startSession(r.body as Session);
  redirect(safeNext(String(fd.get("next") ?? "")));
}

/** Signs out at auth_service (ignoring a failure), clears the cookies and redirects to /signin. */
export async function signout() {
  const jar = await cookies();
  const access = jar.get(ACCESS_COOKIE)?.value;
  if (access) {
    await fetch(`${kongUrl()}/auth/signout`, {
      method: "DELETE",
      headers: kongHeaders(access, undefined, clientIp(await headers())),
      cache: "no-store",
      signal: AbortSignal.timeout(UPSTREAM_TIMEOUT_MS),
    }).catch(() => null);
  }
  jar.delete(ACCESS_COOKIE);
  jar.delete(REFRESH_COOKIE);
  redirect("/signin");
}

/** The password form's result: done, the wrong current password, a broken rule, or another error. */
export type PasswordState =
  { done?: boolean; wrong?: boolean; rule?: "length" | "common"; error?: string } | undefined;

/** A call to Kong with the user's access token, or null when Kong can't be reached. */
const kong = (method: string, path: string, access?: string, ip?: string, body?: unknown) =>
  fetch(`${kongUrl()}${path}`, {
    method,
    headers: kongHeaders(access, body ? { "content-type": "application/json" } : undefined, ip),
    body: body ? JSON.stringify(body) : undefined,
    cache: "no-store",
    signal: AbortSignal.timeout(UPSTREAM_TIMEOUT_MS * 3),
  }).catch(() => null);

/** Where a refused session goes: both cookies cleared, then /signin, back to /settings. */
const EXPIRED = "/signin/expired?next=%2Fsettings";

/** Settings' password form: PUT /auth/password. The change ends every session, this one too, so this browser signs in
 * again with the new password and keeps working; the user's other sessions stay signed out. The 5th wrong current
 * password in 15 minutes ends this session, which then goes to /signin. */
export async function changePassword(_: PasswordState, fd: FormData): Promise<PasswordState> {
  const current = String(fd.get("current") ?? "");
  const next = String(fd.get("new") ?? "");
  if (!current || next.length < 10 || next !== String(fd.get("confirm") ?? ""))
    return { error: "Password not changed." };
  const access = (await cookies()).get(ACCESS_COOKIE)?.value;
  const ip = clientIp(await headers());
  const me = await kong("GET", "/auth/me", access, ip);
  if (me?.status === 401) redirect(EXPIRED);
  const email = me?.ok
    ? ((await me.json().catch(() => null)) as { email?: string } | null)?.email
    : undefined;
  if (!email) return { error: "Password not changed. Try again shortly." };
  const r = await kong("PUT", "/auth/password", access, ip, {
    current_password: current,
    new_password: next,
  });
  if (r?.status === 204) {
    const s = await authPost("/auth/signin", { email, password: next }, ip).catch(() => null);
    if (s?.status !== 200) redirect(EXPIRED); // changed, but this browser could not sign in again
    await startSession(s.body as Session);
    return { done: true };
  }
  const detail = r ? detailOf(await r.json().catch(() => null), "") : "";
  if (r?.status === 401) {
    if (detail !== "current password is incorrect") redirect(EXPIRED);
    // the 5th wrong one revoked this session
    if ((await kong("GET", "/auth/me", access, ip))?.status === 401) redirect(EXPIRED);
    return { wrong: true };
  }
  if (r?.status === 422 && detail === "password is too common") return { rule: "common" };
  if (r?.status === 422 && detail.startsWith("password must be at least"))
    return { rule: "length" };
  return {
    error: detail ? `Password not changed: ${detail}.` : "Password not changed. Try again shortly.",
  };
}
