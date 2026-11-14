"use client";
import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
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
import { when } from "@/lib/format";

/** The count columns, in order. */
const COLS = [
  ["skus", "SKUs"],
  ["not_run", "Not run"],
  ["running", "Running"],
  ["to_review", "To review"],
  ["approved", "Approved"],
  ["images_approved", "Images approved"],
] as const;

/** Route /dashboard: the caller's Catalog Agent sessions (newest first) with their SKU and image counts. */
export default function DashboardPage() {
  const [sessions, setSessions] = useState<S<"SessionSummary">[] | null>(null);
  const [error, setError] = useState("");
  const load = useCallback(() => {
    ok(api.GET("/v1/catalog/sessions"))
      .then((r) => setSessions(r.sessions))
      .catch((e) => setError(message(e)));
  }, []);
  useEffect(load, [load]);

  return (
    <>
      <PageHeader
        title="Dashboard"
        description="Your catalog sessions and where each one stands."
        actions={
          <Button asChild>
            <Link href="/">Open Catalog agent</Link>
          </Button>
        }
      />
      {error ? (
        <ErrorNote
          onRetry={() => {
            setError("");
            load();
          }}
        >
          Couldn’t load your sessions: {error}
        </ErrorNote>
      ) : !sessions ? (
        <SkeletonRows />
      ) : !sessions.length ? (
        <EmptyState
          title="No sessions yet"
          action={
            <Button asChild>
              <Link href="/">Start one</Link>
            </Button>
          }
        >
          Open Catalog agent to set up your first batch.
        </EmptyState>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Project</TableHead>
              <TableHead>Stage</TableHead>
              {COLS.map(([k, label]) => (
                <TableHead key={k} className="text-right">
                  {label}
                </TableHead>
              ))}
              <TableHead>Updated</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {sessions.map((x) => (
              <TableRow key={x.id}>
                <TableCell className="max-w-64 truncate font-medium">
                  <Link href={`/sessions/${x.id}`} className="hover:underline">
                    {x.project_name}
                  </Link>
                </TableCell>
                <TableCell>
                  <StatusPill status={x.stage} />
                </TableCell>
                {COLS.map(([k]) => (
                  <TableCell key={k} className="tabular text-right">
                    {x.counts[k]}
                  </TableCell>
                ))}
                <TableCell className="whitespace-nowrap text-muted-foreground">
                  {when(x.updated_at)}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </>
  );
}
