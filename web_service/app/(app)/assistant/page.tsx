import type { Metadata } from "next";
import { AssistantScreen } from "@/components/assistant/page";

export const metadata: Metadata = { title: "Assistant" };

/** Route /assistant (plan 32): the Assistant's own page, for everyone; an admin edits it, anyone else reads it.
 * `?setup=1` (the drawer's Set up) opens the setup form. */
export default async function AssistantPage({
  searchParams,
}: {
  searchParams: Promise<{ setup?: string }>;
}) {
  return <AssistantScreen setup={(await searchParams).setup !== undefined} />;
}
