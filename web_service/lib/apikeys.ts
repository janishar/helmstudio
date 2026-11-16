// Admin › API keys (docs/mock/ApiKeys.dc.html; contracts/README.md "Hosts admin", API keys): the list and revoke.
// Keys are made only on the server (`auth apikey <client>`), which prints the full key once.
import type { S } from "@/lib/api";

type Key = S<"ApiKey">;

/** The command the board shows, and its example. */
export const CREATE_COMMAND = "auth apikey <client>";

/** The tiles' counts. */
export const keyCounts = (keys: Key[]) => ({
  active: keys.filter((k) => !k.revoked_at).length,
  revoked: keys.filter((k) => k.revoked_at).length,
});

/** Active keys first (newest first, as the API lists them), then the revoked ones. */
export const keyOrder = (keys: Key[]) => keys.toSorted((a, b) => +!!a.revoked_at - +!!b.revoked_at);

/** The table's Last used: "never", "now" within the minute the server updates it in, else the date. */
export function lastUsed(iso: string | null, now: number, when: (iso: string) => string): string {
  if (!iso) return "never";
  return now - Date.parse(iso) < 60_000 ? "now" : when(iso);
}

/** What the board explains on the two known clients' rows. */
export const CLIENT_NOTE: Record<string, string> = {
  web: "Held by the web backend-for-frontend. The browser never sees this key.",
  helm: "Sent by the helm command line tool, together with the token of the user signed in with helm login.",
};
