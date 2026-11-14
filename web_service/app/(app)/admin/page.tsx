import type { Metadata } from "next";
import { UsersAdmin } from "@/components/admin/users";

export const metadata: Metadata = { title: "Admin" };

/** Route /admin: user management (auth_service answers 403 to anyone but the admin). */
export default function AdminPage() {
  return <UsersAdmin />;
}
