import type { Metadata } from "next";
import { CatalogAgent } from "@/components/catalog/agent";

export const metadata: Metadata = { title: "Catalog agent" };

/** Route /sessions/<id>: the Catalog Agent with that session open (the newest when the id isn't one of the user's). */
export default async function Page({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  return <CatalogAgent initialId={id} />;
}
