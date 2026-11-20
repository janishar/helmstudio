import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { Execution } from "@/components/pipelines/execution";
import type { S } from "@/lib/api";
import { initial, serverApi } from "@/lib/server-api";

export const metadata: Metadata = { title: "Execution" };

/** Route /pipelines/batches/[id]/rows/[row]: one row's execution view (`?attempt=` an earlier attempt). 404 for an
 * unknown batch or row, another user's, or a batch of one workflow (it has no execution to show). */
export default async function Page({
  params,
  searchParams,
}: {
  params: Promise<{ id: string; row: string }>;
  searchParams: Promise<{ attempt?: string }>;
}) {
  const [{ id, row }, q] = await Promise.all([params, searchParams]);
  const attempt = /^\d+$/.test(q.attempt ?? "") ? Number(q.attempt) : undefined;
  const api = await serverApi();
  const path = { batch_id: id };
  const [b, x] = await Promise.all([
    initial(api.GET("/v1/studio/batches/{batch_id}", { params: { path } })),
    api.GET("/v1/studio/batches/{batch_id}/rows/{row}/execution", {
      params: { path: { ...path, row }, query: attempt ? { attempt } : {} },
    }),
  ]);
  if (x.response.status === 404 || x.response.status === 422) notFound();
  const exec = await initial(Promise.resolve(x));
  if (!b || !exec) throw new Error(`GET execution: ${x.response.status}`);
  const [p, w] = await Promise.all([
    b.pipeline_id
      ? initial(
          api.GET("/v1/studio/pipelines/{pipeline_id}", {
            params: { path: { pipeline_id: b.pipeline_id } },
          }),
        )
      : null,
    initial(api.GET("/v1/studio/workflows")),
  ]);
  return (
    <Execution
      batch={b}
      initial={exec as S<"RowExecution">}
      attempt={attempt}
      titles={p?.workflow_titles ?? {}}
      envs={Object.fromEntries((w?.workflows ?? []).map((x) => [x.id, x.env]))}
    />
  );
}
