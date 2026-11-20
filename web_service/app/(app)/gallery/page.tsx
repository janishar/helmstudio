import type { Metadata } from "next";
import { Gallery, PAGE } from "@/components/gallery/gallery";
import { initial, serverApi } from "@/lib/server-api";

export const metadata: Metadata = { title: "Gallery" };

/** Route /gallery: every take of the user, with search, filters, uploads and the take actions; `?batch=<id>` (the
 * Pipelines list's Open collection) starts filtered to that batch. The first page is read here; if that fails, Gallery
 * loads it itself. */
export default async function Page({
  searchParams,
}: {
  searchParams: Promise<{ batch?: string }>;
}) {
  const { batch } = await searchParams;
  const api = await serverApi();
  const [first, b] = await Promise.all([
    initial(
      api.GET("/v1/studio/takes", {
        params: { query: { limit: PAGE, ...(batch && { batch_id: batch }) } },
      }),
    ),
    batch
      ? initial(api.GET("/v1/studio/batches/{batch_id}", { params: { path: { batch_id: batch } } }))
      : null,
  ]);
  return (
    <Gallery
      initial={first ?? undefined}
      batch={batch ? { id: batch, title: b?.title ?? "" } : undefined}
    />
  );
}
