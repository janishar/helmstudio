import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { Review } from "@/components/pipelines/review";
import { initial, serverApi } from "@/lib/server-api";

export const metadata: Metadata = { title: "Review" };

/** Route /pipelines/batches/[id]/review: every parked row of the batch with its candidates. An unknown (or another
 * user's) batch is a 404; if the API can't answer, Review loads it itself. */
export default async function Page({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  const api = await serverApi();
  const path = { params: { path: { batch_id: id } } };
  const [b, r] = await Promise.all([
    api.GET("/v1/studio/batches/{batch_id}", path).catch(() => null),
    initial(api.GET("/v1/studio/batches/{batch_id}/review", path)),
  ]);
  if (b?.response.status === 404) notFound();
  const batch = b?.response.ok ? b.data : undefined;
  return <Review id={id} initial={batch && r ? { batch, review: r } : undefined} />;
}
