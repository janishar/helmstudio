// Signed file links (`/files/…?exp=&sig=`) when the web app is reached directly; behind Kong /files goes to files_service.
import type { NextRequest } from "next/server";
import { proxy } from "@/lib/proxy";

/** Never cached by Next: every request goes to Kong. */
export const dynamic = "force-dynamic";

/** Passes a signed file link on without the app's key or a token; dot segments are refused with a 403. */
export async function GET(req: NextRequest, { params }: { params: Promise<{ path: string[] }> }) {
  const segs = (await params).path;
  if (segs.some((s) => s === "." || s === ".."))
    return Response.json({ detail: "invalid link" }, { status: 403 });
  return proxy(req, "/files/" + segs.map(encodeURIComponent).join("/"), false);
}
