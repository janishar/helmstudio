import type { Metadata } from "next";
import { ProvidersAdmin } from "@/components/admin/providers";
import { UsersAdmin } from "@/components/admin/users";
import { initial, serverApi } from "@/lib/server-api";

export const metadata: Metadata = { title: "Admin" };

/** Route /admin: user management (auth_service answers 403 to anyone but the admin), and `?tab=providers` the model
 * providers (studio answers 403 likewise). The users list is read here; if that fails, the screen loads it itself and
 * shows the error with Retry. */
export default async function AdminPage({
  searchParams,
}: {
  searchParams: Promise<{ tab?: string }>;
}) {
  if ((await searchParams).tab === "providers") return <ProvidersAdmin />;
  const api = await serverApi();
  const list = await initial(api.GET("/auth/admin/users"));
  return <UsersAdmin initialUsers={list?.users} />;
}
