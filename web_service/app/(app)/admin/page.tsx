import type { Metadata } from "next";
import { UsersAdmin } from "@/components/admin/users";
import { initial, serverApi } from "@/lib/server-api";

export const metadata: Metadata = { title: "Admin" };

/** Route /admin: user management (auth_service answers 403 to anyone but the admin). The list is read here; if that
 * fails, the screen loads it itself and shows the error with Retry. */
export default async function AdminPage() {
  const api = await serverApi();
  const list = await initial(api.GET("/auth/admin/users"));
  return <UsersAdmin initialUsers={list?.users} />;
}
