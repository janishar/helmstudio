import type { Metadata } from "next";
import { notFound } from "next/navigation";

/** Dock sections that arrive in a later milestone, by route segment. */
const LATER: Record<string, string> = {
  timeline: "Timeline",
  models: "Models",
};

type Props = { params: Promise<{ section: string[] }> };

/** Only a single known segment is a later section; anything deeper is a 404 inside the dock. */
const later = (s: string[]) => (s.length === 1 ? LATER[s[0]] : undefined);

export async function generateMetadata({ params }: Props): Promise<Metadata> {
  return { title: later((await params).section) ?? "Not found" };
}

/** A quiet placeholder for a section that is not built yet; any other segment is a 404. */
export default async function LaterPage({ params }: Props) {
  const title = later((await params).section);
  if (!title) notFound();
  return (
    <main
      id="main"
      className="flex flex-1 flex-col items-center justify-center gap-2 p-6 text-center"
    >
      <h1 className="font-display text-[22px] font-bold">{title}</h1>
      <p className="text-t2">Arrives in a later milestone.</p>
    </main>
  );
}
