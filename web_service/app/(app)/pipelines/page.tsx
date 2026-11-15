import type { Metadata } from "next";
import { PAGE, Pipelines } from "@/components/pipelines/pipelines";
import type { S } from "@/lib/api";
import { initial, serverApi } from "@/lib/server-api";

export const metadata: Metadata = { title: "Pipelines" };

/** Route /pipelines: the saved pipelines, the recent batches and New batch. The first page of batches, the pipelines
 * and the workflows are read here; if a read fails, Pipelines loads them itself. */
export default async function Page() {
  const api = await serverApi();
  const [b, w, p] = await Promise.all([
    initial(api.GET("/v1/studio/batches", { params: { query: { limit: PAGE } } })),
    initial(api.GET("/v1/studio/workflows")),
    initial(api.GET("/v1/studio/pipelines")),
  ]);
  return (
    <Pipelines
      initial={b ?? undefined}
      workflows={w?.workflows}
      pipelines={p?.pipelines as S<"Pipeline">[] | undefined}
    />
  );
}
