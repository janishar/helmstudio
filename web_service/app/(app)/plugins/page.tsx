import { redirect } from "next/navigation";

/** Old link (plan 33): Plugins is a tab of Hub. */
export default function PluginsPage() {
  redirect("/hub?tab=plugins");
}
