import type { Metadata } from "next";
import { TimelineScreen } from "@/components/timeline/screen";

export const metadata: Metadata = { title: "Timeline" };

/** Route /timeline: the sequence editor (`?t=<id>`, else the one opened last, else the newest). */
export default async function TimelinePage({
  searchParams,
}: {
  searchParams: Promise<{ t?: string }>;
}) {
  return <TimelineScreen id={(await searchParams).t} />;
}
