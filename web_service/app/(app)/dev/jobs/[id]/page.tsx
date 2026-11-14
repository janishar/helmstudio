import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { JobView } from "./job-view";

export const metadata: Metadata = { title: "Job (dev)" };

/** Route /dev/jobs/<id>: one of the caller's jobs, its nodes and events; a 404 unless DEV_VIEWS is on. */
export default async function Page({ params }: { params: Promise<{ id: string }> }) {
  if (process.env.DEV_VIEWS !== "true") notFound();
  const { id } = await params;
  return <JobView id={id} />;
}
