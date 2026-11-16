import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { ApiKeysAdmin } from "@/components/admin/apikeys";
import { HostsAdmin } from "@/components/admin/hosts";
import { ProvidersAdmin } from "@/components/admin/providers";
import { UsersAdmin } from "@/components/admin/users";
import { initial, serverApi } from "@/lib/server-api";

export const metadata: Metadata = { title: "Admin" };

/** Route /admin: Users, `?tab=hosts`, `?tab=keys` (API keys) and `?tab=providers`. Anyone signed in who is not an
 * admin gets a 404 (the APIs answer them 403 anyway); when auth can't be reached the screens show their own errors.
 * The users list is read here; if that fails, the screen loads it itself and shows the error with Retry. */
export default async function AdminPage({
  searchParams,
}: {
  searchParams: Promise<{ tab?: string }>;
}) {
  const api = await serverApi();
  const me = await initial(api.GET("/auth/me"));
  if (me && !me.roles.includes("admin")) notFound();
  const tab = (await searchParams).tab;
  if (tab === "providers") return <ProvidersAdmin />;
  if (tab === "hosts") return <HostsAdmin />;
  if (tab === "keys") return <ApiKeysAdmin />;
  const list = await initial(api.GET("/auth/admin/users"));
  return <UsersAdmin initialUsers={list?.users} />;
}
