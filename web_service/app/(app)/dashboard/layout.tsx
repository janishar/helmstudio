import type { Metadata } from "next";

export const metadata: Metadata = { title: "Dashboard" };

/** Names the tab ("Dashboard · ORA Catalog Agent"): the page itself is a client component, which can't export metadata. */
export default function DashboardLayout({ children }: { children: React.ReactNode }) {
  return children;
}
