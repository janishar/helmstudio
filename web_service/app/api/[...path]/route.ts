import type { NextRequest } from "next/server";
import { allowed, proxy } from "@/lib/proxy";

/** Never cached by Next: every request goes to Kong. */
export const dynamic = "force-dynamic";

/** BFF route for the browser's API calls: allowed paths go to Kong with the user's token, anything else is a 404. */
async function handle(req: NextRequest, { params }: { params: Promise<{ path: string[] }> }) {
  const path = "/" + (await params).path.map(encodeURIComponent).join("/");
  if (!allowed(path)) return Response.json({ detail: "Not Found" }, { status: 404 });
  return proxy(req, path, true);
}

export { handle as GET, handle as POST, handle as PUT, handle as PATCH, handle as DELETE };
