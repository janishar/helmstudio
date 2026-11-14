// GET /signin/expired?next=…: a session the API refused (a server component can't change cookies), so both cookies
// go and the browser lands on /signin with the same `next`.
import { NextResponse, type NextRequest } from "next/server";
import { safeNext } from "@/lib/jwt";
import { ACCESS_COOKIE, REFRESH_COOKIE } from "@/lib/session";
import { publicOrigin } from "@/proxy";

export function GET(req: NextRequest) {
  const next = safeNext(req.nextUrl.searchParams.get("next"));
  const res = NextResponse.redirect(
    new URL(`/signin?next=${encodeURIComponent(next)}`, publicOrigin(req)),
  );
  res.cookies.delete(ACCESS_COOKIE);
  res.cookies.delete(REFRESH_COOKIE);
  return res;
}
