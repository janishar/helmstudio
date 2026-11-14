import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { JobsList } from "./jobs-list";

export const metadata: Metadata = { title: "Jobs (dev)" };

/** Route /dev/jobs: the caller's processing jobs and runs; a 404 unless DEV_VIEWS is on. */
export default function Page() {
  if (process.env.DEV_VIEWS !== "true") notFound();
  return <JobsList />;
}
