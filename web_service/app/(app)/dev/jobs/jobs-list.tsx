"use client";
import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { useDevViews } from "@/components/app-shell";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { EmptyState, ErrorNote, PageHeader, SkeletonRows, StatusPill } from "@/components/kit";
import { api, message, ok, type S } from "@/lib/api";
import { duration, when } from "@/lib/format";

/** True while a job is unfinished. */
export const active = (status: string) => status === "queued" || status === "running";

/** The caller's newest 100 jobs, newest first; refreshes every 3 s while one is unfinished. */
export function JobsList() {
  const dev = useDevViews();
  const [jobs, setJobs] = useState<S<"Job">[] | null>(null);
  const [error, setError] = useState("");
  const load = useCallback(() => {
    ok(api.GET("/v1/catalog/jobs"))
      .then((r) => {
        setJobs(r.jobs);
        setError("");
      })
      .catch((e) => setError(message(e)));
  }, []);
  useEffect(load, [load]);
  const live = !!jobs?.some((j) => active(j.status));
  useEffect(() => {
    if (!live) return;
    const t = setInterval(load, 3000);
    return () => clearInterval(t);
  }, [live, load]);

  if (!dev) return <NoDevRole />;
  return (
    <>
      <PageHeader
        title="Jobs (dev)"
        description="Your processing jobs and runs, newest first. Open one to see its nodes, images and events."
        actions={
          <Button variant="outline" onClick={load}>
            Refresh
          </Button>
        }
      />
      {error ? (
        <ErrorNote onRetry={load}>Couldn’t load your jobs: {error}</ErrorNote>
      ) : !jobs ? (
        <SkeletonRows />
      ) : !jobs.length ? (
        <EmptyState title="No jobs yet">
          Start processing a session to see its jobs here.
        </EmptyState>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Job</TableHead>
              <TableHead>Project</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Started</TableHead>
              <TableHead className="text-right">Took</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {jobs.map((j) => (
              <TableRow key={j.id}>
                <TableCell className="font-medium whitespace-nowrap">
                  <Link href={`/dev/jobs/${j.id}`} className="hover:underline">
                    {j.kind === "run" ? `Run · ${j.scope}` : "Processing"}
                  </Link>
                  <span className="ml-2 font-mono text-xs text-muted-foreground">
                    {j.id.slice(0, 8)}
                  </span>
                </TableCell>
                <TableCell className="max-w-64 truncate">
                  <Link href={`/sessions/${j.session_id}`} className="hover:underline">
                    {j.project_name}
                  </Link>
                </TableCell>
                <TableCell>
                  <StatusPill status={j.status} />
                </TableCell>
                <TableCell className="whitespace-nowrap text-muted-foreground">
                  {when(j.created_at)}
                </TableCell>
                <TableCell className="tabular text-right text-muted-foreground">
                  {duration(Date.parse(j.updated_at) - Date.parse(j.created_at))}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </>
  );
}

/** What a user without the dev or admin role sees on the dev pages. */
export function NoDevRole() {
  return (
    <EmptyState title="Developers only">
      The Jobs view needs the developer role. Ask the admin.
    </EmptyState>
  );
}
