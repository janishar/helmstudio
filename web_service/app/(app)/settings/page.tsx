import type { Metadata } from "next";
import { SettingsScreen } from "@/components/settings";

export const metadata: Metadata = { title: "Settings" };

/** Route /settings: the account, the theme, the password and Sign out (docs/mock/Settings.dc.html). */
export default function SettingsPage() {
  return <SettingsScreen />;
}
