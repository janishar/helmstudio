import type { Metadata } from "next";
import { redirect } from "next/navigation";
import { CatalogAgent } from "@/components/catalog/agent";

export const metadata: Metadata = { title: "Catalog agent" };

/** Route /: the Catalog Agent on the newest session (its URL becomes /sessions/<id>). An old /?s=<id> link goes to
 * /sessions/<id>. */
export default async function Page({ searchParams }: { searchParams: Promise<{ s?: string }> }) {
  const { s } = await searchParams;
  if (s) redirect(`/sessions/${encodeURIComponent(s)}`);
  return <CatalogAgent />;
}
