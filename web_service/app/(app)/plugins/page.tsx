import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { PluginsScreen } from "@/components/plugins/plugins";
import { initial, serverApi } from "@/lib/server-api";

export const metadata: Metadata = { title: "Plugins" };

/** Route /plugins: the plugins studio found in plugins/ (API 1.61), admins only. Anyone signed in who is not an admin
 * gets a 404, as on /admin (the APIs answer them 403 anyway). */
export default async function PluginsPage() {
  const me = await initial((await serverApi()).GET("/auth/me"));
  if (me && !me.roles.includes("admin")) notFound();
  return <PluginsScreen />;
}
