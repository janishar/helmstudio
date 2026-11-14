import type { Metadata } from "next";
import { Gallery } from "@/components/gallery/gallery";

export const metadata: Metadata = { title: "Gallery" };

/** Route /gallery: every take of the user, with search, filters, uploads and the take actions. */
export default function Page() {
  return <Gallery />;
}
