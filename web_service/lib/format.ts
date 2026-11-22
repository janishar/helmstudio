// Display helpers shared by the pages.

/** A size in decimal megabytes, the one rule for uploads and the Gallery (plan 20 gaps): "1.9", "0.05" below 0.1 MB. */
export function mb(bytes: number): string {
  const m = bytes / 1e6;
  return m < 0.1 ? m.toFixed(2) : m.toFixed(1);
}

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
  try {
    const url = new URL(u);
    return url.pathname.startsWith("/files/") ? url.pathname + url.search : u;
  } catch {
    return undefined; // "http://" and the like: no link rather than a render error
  }
}
