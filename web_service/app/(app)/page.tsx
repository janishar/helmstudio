import type { Metadata } from "next";
import { Create } from "@/components/create/create";

export const metadata: Metadata = { title: "Create" };

/** Route /: Create, on the session `?s=` names, else the newest; `?reuse=<take id>` fills the form with the
 * settings that made that take. */
export default async function Page({
  searchParams,
}: {
  searchParams: Promise<{ s?: string; reuse?: string }>;
}) {
  const { s, reuse } = await searchParams;
  return <Create initialSession={s} reuse={reuse} />;
}
