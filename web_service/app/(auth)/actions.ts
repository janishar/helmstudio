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
  type Session,
} from "@/lib/session";

/** A form's result: the error, plus what was typed so the form can keep it. */
export type AuthState = { error?: string; email?: string; name?: string } | undefined;

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

/** Sign-up form action: checks the fields, creates the account, sets the cookies and redirects home. */
export async function signup(_: AuthState, fd: FormData): Promise<AuthState> {
  const name = String(fd.get("name") ?? "").trim();
  const email = String(fd.get("email") ?? "").trim();
  const password = String(fd.get("password") ?? "");
  const back = { name, email };
  if (!name || !email) return { error: "Enter your name and email.", ...back };
  if (password.length < 10)
    return { error: "Use at least 10 characters for the password.", ...back };
  if (password !== fd.get("confirm")) return { error: "The passwords don't match.", ...back };
  const r = await authPost(
    "/auth/signup",
    { name, email, password },
    clientIp(await headers()),
  ).catch(() => null);
  if (!r) return { error: "Sign-up is unavailable right now. Try again shortly.", ...back };
  if (r.status !== 201) return { error: detailOf(r.body, "Sign-up failed."), ...back };
  await startSession(r.body as Session);
  redirect("/");
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
    }).catch(() => null);
  }
  jar.delete(ACCESS_COOKIE);
  jar.delete(REFRESH_COOKIE);
  redirect("/signin");
}
