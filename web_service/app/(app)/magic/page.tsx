import type { Metadata } from "next";
import { MagicScreen } from "@/components/magic/screen";

export const metadata: Metadata = { title: "Magic" };

/** Route /magic: lanes in time (plan 39; boards kept in studio). */
export default function MagicPage() {
  return <MagicScreen />;
}
