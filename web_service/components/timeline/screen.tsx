"use client";
// The Timeline route's client entry: the editor (its maths, preview engine and lanes) loads only here, on demand.
import dynamic from "next/dynamic";

const Editor = dynamic(() => import("@/components/timeline/editor").then((m) => m.TimelineEditor), {
  ssr: false,
  loading: () => (
    <main id="main" className="flex flex-1 items-center justify-center p-6 text-t2">
      Loading the timeline…
    </main>
  ),
});

export function TimelineScreen({ id }: { id?: string }) {
  return <Editor id={id} />;
}
