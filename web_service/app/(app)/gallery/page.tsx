import type { Metadata } from "next";
import { Gallery, PAGE } from "@/components/gallery/gallery";
import { initial, serverApi } from "@/lib/server-api";

export const metadata: Metadata = { title: "Gallery" };

/** Route /gallery: every take of the user, with search, filters, uploads and the take actions. The first page (no
 * filter) is read here; if that fails, Gallery loads it itself. */
export default async function Page() {
  const api = await serverApi();
  const first = await initial(api.GET("/v1/studio/takes", { params: { query: { limit: PAGE } } }));
  return <Gallery initial={first ?? undefined} />;
}
