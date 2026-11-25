import { redirect } from "next/navigation";

/** Old links (plan 33): /admin is System; its Model providers and AI agents tabs moved to Hub. */
export default async function AdminPage({
  searchParams,
}: {
  searchParams: Promise<{ tab?: string }>;
}) {
  const tab = (await searchParams).tab;
  if (tab === "providers") redirect("/hub?tab=providers");
  if (tab === "agents") redirect("/hub?tab=pipelines");
  redirect(tab === "hosts" || tab === "keys" ? `/system?tab=${tab}` : "/system");
}
