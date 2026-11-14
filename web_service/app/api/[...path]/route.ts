import type { NextRequest } from "next/server";
import { MAX_BODY, allowed, proxy } from "@/lib/proxy";

/** BFF route for the browser's API calls: allowed paths go to Kong with the user's token, anything else is a 404. */
async function handle(req: NextRequest, { params }: { params: Promise<{ path: string[] }> }) {
  const path = "/" + (await params).path.map(encodeURIComponent).join("/");
  if (!allowed(path)) return Response.json({ detail: "Not Found" }, { status: 404 });
  // a larger body would reach this handler cut short by proxy.ts (see lib/proxy.ts)
  if (Number(req.headers.get("content-length")) > MAX_BODY)
    return Response.json({ detail: "Too large." }, { status: 413 });
  return proxy(req, path, true);
}

export { handle as GET, handle as POST, handle as PUT, handle as PATCH, handle as DELETE };
