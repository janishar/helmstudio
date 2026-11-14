// Signed file links (`/files/…?exp=&sig=`, and an upload's signed PUT) when the web app is reached directly; behind Kong /files goes to files_service.
import type { NextRequest } from "next/server";
import { proxy } from "@/lib/proxy";

/** Passes a signed file link (GET, or an upload's PUT) on without the app's key or a token; dot segments are refused
 * with a 403. */
async function handle(req: NextRequest, { params }: { params: Promise<{ path: string[] }> }) {
  const segs = (await params).path;
  if (segs.some((s) => s === "." || s === ".."))
    return Response.json({ detail: "invalid link" }, { status: 403 });
  return proxy(req, "/files/" + segs.map(encodeURIComponent).join("/"), false);
}

export { handle as GET, handle as PUT };
