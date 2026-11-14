import type { Metadata } from "next";

export const metadata: Metadata = { title: "Account" };

/** Names the tab ("Account · ORA Catalog Agent"): the page itself is a client component, which can't export metadata. */
export default function AccountLayout({ children }: { children: React.ReactNode }) {
  return children;
}
