"use client";
// An unexpected render error in a signed-in page: the dock (the layout above) stays, the page offers Retry.
import { useEffect } from "react";
import { ErrorNote } from "@/components/kit";

export default function PageError({ error, retry }: { error: unknown; retry: () => void }) {
  useEffect(() => console.error(error), [error]);
  return (
    <main id="main" className="flex flex-1 flex-col items-center justify-center gap-2 p-6">
      <ErrorNote onRetry={retry}>Something went wrong on this page.</ErrorNote>
    </main>
  );
}
