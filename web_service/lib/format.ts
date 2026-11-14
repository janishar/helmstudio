// Display helpers shared by the pages.

/** An ISO time in the viewer's locale; an unparsable value comes back as it is, a missing one as "". */
export function when(iso: string | null | undefined): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}

/** Only relative or http(s) links go into src/href (a contract `*_url` is opaque but must not be javascript:). */
export function safeUrl(u: string | null | undefined): string | undefined {
  if (!u) return undefined;
  if (u.startsWith("/") && !u.startsWith("//")) return u;
  if (!/^https?:\/\//i.test(u)) return undefined;
  // the API signs /files/… links with its PUBLIC_BASE_URL (localhost:8100), which may not be the host this page is on
  // (127.0.0.1, a LAN name): same-origin path instead, so fetch() of a link (Edit & run again) isn't blocked as CORS
  const url = new URL(u);
  return url.pathname.startsWith("/files/") ? url.pathname + url.search : u;
}

/** Milliseconds as "850 ms", "12.3 s" or "4 min 05 s"; "" for a negative or missing span. */
export function duration(ms: number | null | undefined): string {
  if (ms == null || !(ms >= 0)) return "";
  if (ms < 1000) return `${Math.round(ms)} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
  const s = Math.round(ms / 1000);
  return `${Math.floor(s / 60)} min ${String(s % 60).padStart(2, "0")} s`;
}

/** US dollars for small LLM costs ("$0.0042"); "–" when unknown. */
export function usd(v: number | null | undefined): string {
  if (v == null) return "–";
  return `$${v < 1 ? v.toFixed(4) : v.toFixed(2)}`;
}
