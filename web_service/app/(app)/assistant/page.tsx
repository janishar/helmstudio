import { redirect } from "next/navigation";

/** Old link (plan 33): the Assistant's page is Hub's first tab; `?setup=1` still opens the setup form. */
export default async function AssistantPage({
  searchParams,
}: {
  searchParams: Promise<{ setup?: string }>;
}) {
  redirect((await searchParams).setup !== undefined ? "/hub?setup=1" : "/hub");
}
