import type { Metadata } from "next";
import { Create } from "@/components/create/create";

export const metadata: Metadata = { title: "Create" };

/** Route /: Create, on the session `?s=` names, else the newest. */
export default async function Page({ searchParams }: { searchParams: Promise<{ s?: string }> }) {
  return <Create initialSession={(await searchParams).s} />;
}
