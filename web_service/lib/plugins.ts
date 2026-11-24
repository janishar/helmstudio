// Plugins page texts (docs/mock/Plugins.dc.html; contracts/README.md "Plugins", API 1.61).
import type { S } from "@/lib/api";

type Plugin = S<"Plugin">;

export const KIND: Record<Plugin["kind"], string> = {
  "model-provider": "Model provider",
  "assistant-tools": "Assistant tools",
};

/** The address without the scheme: "host.docker.internal:8420" (the url as is when it doesn't parse). */
export function hostOf(url: string): string {
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
}

/** The Status column: running → "Running" over its host; on but no answer → "Not running" over "enabled · start it";
 * off → "Off". */
export function statusOf(p: Plugin): { text: string; sub?: string; ok: boolean } {
  if (p.status === "running") return { text: "Running", sub: hostOf(p.url), ok: true };
  if (p.status === "not_running")
    return { text: "Not running", sub: "enabled · start it", ok: false };
  return { text: "Off", ok: false };
}

/** The provider's default title: "<title> (personal)" for a personal plugin, else its title (the API's own default). */
export const providerTitle = (p: Plugin) => (p.personal ? `${p.title} (personal)` : p.title);

/** After Rescan: "1 new plugin: Web search. It stays Off." / "2 new plugins: A, B. They stay Off." / "No new plugins." */
export function rescanLine(r: S<"PluginRescan">): string {
  const titles = r.new.map((n) => r.plugins.find((p) => p.name === n)?.title ?? n);
  if (!titles.length) return "No new plugins.";
  const one = titles.length === 1;
  return `${titles.length} new plugin${one ? "" : "s"}: ${titles.join(", ")}. ${one ? "It stays" : "They stay"} Off.`;
}

/** The Add-as-provider refusal that empties the key field (red); any other error shows as is (amber, Try again). */
export const KEY_REFUSED = "The key doesn't match";
