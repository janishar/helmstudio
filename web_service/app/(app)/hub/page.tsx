import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { InPipelines } from "@/components/admin/agents";
import { ProvidersAdmin } from "@/components/admin/providers";
import { AssistantScreen } from "@/components/assistant/page";
import { PluginsScreen } from "@/components/plugins/plugins";
import { initial, serverApi } from "@/lib/server-api";

export const metadata: Metadata = { title: "Hub" };

/** Route /hub (plan 33 37.9), admins only: the Assistant (`?setup=1` opens its setup form), `?tab=pipelines` (In
 * pipelines), `?tab=providers`, `?tab=plugins`. Anyone signed in who is not an admin gets a 404, as /system (the APIs
 * answer them 403 anyway). */
export default async function HubPage({
  searchParams,
}: {
  searchParams: Promise<{ tab?: string; setup?: string }>;
}) {
  const me = await initial((await serverApi()).GET("/auth/me"));
  if (me && !me.roles.includes("admin")) notFound();
  const { tab, setup } = await searchParams;
  if (tab === "pipelines") return <InPipelines />;
  if (tab === "providers") return <ProvidersAdmin />;
  if (tab === "plugins") return <PluginsScreen />;
  return <AssistantScreen setup={setup !== undefined} />;
}
