import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { PipelineEditor } from "@/components/pipelines/editor";
import type { S } from "@/lib/api";
import { initial, serverApi } from "@/lib/server-api";

export const metadata: Metadata = { title: "Pipeline" };

/** Route /pipelines/[id]: one of the caller's pipelines in the editor, with the workflows a step may run and the
 * node palette (its rules need both), the ask workflows its agent nodes may run, the
 * local servers and remote providers they may name and the custom nodes pipelines_service registered. An unknown (or another user's) pipeline is a 404. */
export default async function Page({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  const api = await serverApi();
  const [p, w, n, sources, custom] = await Promise.all([
    api.GET("/v1/studio/pipelines/{pipeline_id}", { params: { path: { pipeline_id: id } } }),
    initial(api.GET("/v1/studio/workflows")),
    initial(api.GET("/v1/studio/nodes")),
    initial(api.GET("/v1/studio/model-sources")),
    initial(api.GET("/v1/studio/custom-nodes")),
  ]);
  if (p.response.status === 404) notFound();
  const pipeline = await initial(Promise.resolve(p)); // a 401 signs out, as every first read does
  if (!pipeline) throw new Error(`GET pipeline: ${p.response.status}`);
  // the ask workflows a judge, write or decide may run: never listed, read by id (each family's `<family>-ask`,
  // and the llm pack's `llm-ask`); one that is not there is left out
  const families = [...new Set((w?.workflows ?? []).map((x) => x.family).filter(Boolean))];
  const asks = (
    await Promise.all(
      [...families.map((f) => `${f}-ask`), "llm-ask"].map((id) =>
        api
          .GET("/v1/studio/workflows/{workflow_id}", { params: { path: { workflow_id: id } } })
          .then((r) => r.data ?? null)
          .catch(() => null),
      ),
    )
  ).filter((x): x is S<"Workflow"> => !!x);
  return (
    <PipelineEditor
      pipeline={pipeline as S<"Pipeline">}
      workflows={w?.workflows ?? []}
      asks={asks}
      nodes={n}
      sources={sources}
      custom={custom?.nodes ?? []}
    />
  );
}
