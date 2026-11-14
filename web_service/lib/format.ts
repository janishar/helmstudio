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
  // the API signs /files/… links with its PUBLIC_BASE_URL (localhost:8300), which may not be the host this page is on
  // (127.0.0.1, a LAN name): same-origin path instead, so fetch() of a link isn't blocked as CORS
  const url = new URL(u);
  return url.pathname.startsWith("/files/") ? url.pathname + url.search : u;
}
