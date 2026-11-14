import type { Metadata } from "next";
import { UsersAdmin } from "./users-admin";

export const metadata: Metadata = { title: "Users (admin)" };

/** Route /admin/users: the admin adds beta accounts and manages them (auth_service checks the admin role). */
export default function Page() {
  return <UsersAdmin />;
}
