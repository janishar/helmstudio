import type { Metadata } from "next";
import { Gallery, PAGE } from "@/components/gallery/gallery";
import { initial, serverApi } from "@/lib/server-api";

export const metadata: Metadata = { title: "Gallery" };

/** Route /gallery: every take of the user, with search, filters, uploads and the take actions. The first page (no
 * filter) and the workflows ("Use in…") are read here; if that fails, Gallery loads it itself. */
export default async function Page() {
  const api = await serverApi();
  const [first, w] = await Promise.all([
    initial(api.GET("/v1/studio/takes", { params: { query: { limit: PAGE } } })),
    initial(api.GET("/v1/studio/workflows")),
  ]);
  return <Gallery initial={first ?? undefined} workflows={w?.workflows} />;
}
