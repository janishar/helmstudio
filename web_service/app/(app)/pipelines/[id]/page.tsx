import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { PipelineEditor } from "@/components/pipelines/editor";
import type { S } from "@/lib/api";
import { initial, serverApi } from "@/lib/server-api";

export const metadata: Metadata = { title: "Pipeline" };

/** Route /pipelines/[id]: one of the caller's pipelines in the editor, with the workflows a step may run and the
 * node palette (its rules need both). An unknown (or another user's) pipeline is a 404. */
export default async function Page({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  const api = await serverApi();
  const [p, w, n] = await Promise.all([
    api.GET("/v1/studio/pipelines/{pipeline_id}", { params: { path: { pipeline_id: id } } }),
    initial(api.GET("/v1/studio/workflows")),
    initial(api.GET("/v1/studio/nodes")),
  ]);
  if (p.response.status === 404) notFound();
  const pipeline = await initial(Promise.resolve(p)); // a 401 signs out, as every first read does
  if (!pipeline) throw new Error(`GET pipeline: ${p.response.status}`);
  return (
    <PipelineEditor pipeline={pipeline as S<"Pipeline">} workflows={w?.workflows ?? []} nodes={n} />
  );
}
