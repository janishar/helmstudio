import type { Metadata } from "next";
import { PAGE, Pipelines } from "@/components/pipelines/pipelines";
import { initial, serverApi } from "@/lib/server-api";

export const metadata: Metadata = { title: "Pipelines" };

/** Route /pipelines: the recent batches and New batch. The first page of batches and the workflows are read here; if
 * a read fails, Pipelines loads them itself. */
export default async function Page() {
  const api = await serverApi();
  const [b, w] = await Promise.all([
    initial(api.GET("/v1/studio/batches", { params: { query: { limit: PAGE } } })),
    initial(api.GET("/v1/studio/workflows")),
  ]);
  return <Pipelines initial={b ?? undefined} workflows={w?.workflows} />;
}
