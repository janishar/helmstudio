import type { Metadata } from "next";
import { ModelsScreen } from "@/components/models/models";

export const metadata: Metadata = { title: "Models" };

/** Route /models: every model and its presence per host (`?tab=loras`: the LoRA library), for everyone; the admin's
 * download and delete controls are shown to admins only (studio answers 403 to anyone else). */
export default async function ModelsPage({
  searchParams,
}: {
  searchParams: Promise<{ tab?: string }>;
}) {
  return <ModelsScreen tab={(await searchParams).tab === "loras" ? "loras" : "models"} />;
}
