import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { BatchRun, ROWS } from "@/components/pipelines/batch";
import { serverApi, initial } from "@/lib/server-api";

export const metadata: Metadata = { title: "Batch" };

/** Route /pipelines/batches/[id]: one batch's run view. The batch and its first rows are read here; an unknown (or
 * another user's) batch is a 404; if the API can't answer, BatchRun loads them itself. */
export default async function Page({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  const api = await serverApi();
  const path = { path: { batch_id: id } };
  const [b, r] = await Promise.all([
    api.GET("/v1/studio/batches/{batch_id}", { params: path }).catch(() => null),
    initial(
      api.GET("/v1/studio/batches/{batch_id}/rows", {
        params: { ...path, query: { limit: ROWS } },
      }),
    ),
  ]);
  if (b?.response.status === 404) notFound();
  const batch = b?.response.ok ? b.data : undefined;
  return <BatchRun id={id} initial={batch && r ? { batch, page: r } : undefined} />;
}
