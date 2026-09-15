// The same-origin proxy a studio mounts at /helm/, so its page never holds a
// token. It behaves exactly as the Go and Python runtime SDKs' proxies do
// (docs/decisions.md M6 Q10; test/conformance holds all three to it):
//
//   /helm/api/v1/<studio-api path>  forwarded to HELM_API with HELM_TOKEN added;
//                                   only the studio API and the theme stream
//   /helm/sdk/v1/<file>             GET and HEAD, to HELM_SDK_BASE, no token
//   /helm/accent.css                the studio's hue for each theme
//
// The page's own Authorization, cookies, Origin and Referer are never
// forwarded, and the daemon's Set-Cookie never comes back.
//
//   import http from "node:http";
//   import { createProxy } from "@helmstudio/runtime/proxy";
//   const helmProxy = createProxy();
//   http.createServer(async (req, res) => {
//     if (await helmProxy(req, res)) return;
//     // …the studio's own routes
//   }).listen(port, "127.0.0.1");
//
// Uses fetch and Node's request and response objects; not for the browser.

export const PREFIX = "/helm/";

export const STUDIO_SEGMENTS = new Set([
  "me", "events", "kv", "sessions", "records", "assets", "assets:adopt", "gallery", "handoff", "inbox", "jobs", "theme",
]);

const REQUEST_HEADERS = ["Accept", "Content-Type", "Range", "If-Range", "If-Match", "If-None-Match", "Last-Event-ID"];
const RESPONSE_HEADERS = ["Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified", "Cache-Control", "Content-Disposition", "Allow"];
const METHODS = new Set(["GET", "HEAD", "POST", "PUT", "PATCH", "DELETE"]);
const HEX = /^#[0-9a-fA-F]{6}$/;

function luminance(hex) {
  const ch = [0, 1, 2].map((i) => {
    const c = parseInt(hex.slice(1 + 2 * i, 3 + 2 * i), 16) / 255;
    return c <= 0.04045 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4);
  });
  return 0.2126 * ch[0] + 0.7152 * ch[1] + 0.0722 * ch[2];
}

function on(hex) {
  const l = luminance(hex);
  const dark = (l + 0.05) / (luminance("#1a1400") + 0.05);
  const white = 1.05 / (l + 0.05);
  return dark >= white ? "#1a1400" : "#ffffff";
}

export function accentCSS(dark, light) {
  if (!dark || !light || !HEX.test(dark) || !HEX.test(light)) return null;
  dark = dark.toLowerCase();
  light = light.toLowerCase();
  const block = (h) => `--helm-studio-accent: ${h}; --helm-on-studio-accent: ${on(h)};`;
  return (
    `:root { ${block(dark)} }\n` +
    `@media (prefers-color-scheme: light) { :root:not([data-theme]) { ${block(light)} } }\n` +
    `:root[data-theme="light"] { ${block(light)} }\n`
  );
}

export function safePath(p) {
  const lower = p.toLowerCase();
  if (lower.includes("%2f") || lower.includes("%5c") || lower.includes("%2e") || p.includes("\\")) return false;
  const segs = p.replace(/^\//, "").split("/");
  return segs.every((s, i) => s !== "." && s !== ".." && !(s === "" && i !== segs.length - 1));
}

function send(res, status, headers, body) {
  res.writeHead(status, headers);
  res.end(body);
}

function notFound(res) {
  send(res, 404, { "Content-Type": "application/json", "X-Content-Type-Options": "nosniff" },
    JSON.stringify({ error: "not_found", message: "the helm proxy forwards only the studio API, the theme stream, the SDK files and accent.css" }) + "\n");
}

export function createProxy({ env = process.env, fetch: fetchImpl = globalThis.fetch } = {}) {
  const api = (env.HELM_API || "").replace(/\/+$/, "");
  const token = env.HELM_TOKEN || "";
  const sdkBase = (env.HELM_SDK_BASE || "").replace(/\/+$/, "");
  const css = accentCSS(env.HELM_ACCENT_DARK, env.HELM_ACCENT_LIGHT);

  async function forward(req, res, target, withToken, query) {
    if (!METHODS.has(req.method)) {
      send(res, 405, { "Content-Type": "application/json" }, JSON.stringify({ error: "method_not_allowed", message: `${req.method} is not forwarded` }) + "\n");
      return;
    }
    const headers = {};
    for (const h of REQUEST_HEADERS) {
      const v = req.headers[h.toLowerCase()];
      if (v !== undefined) headers[h] = v;
    }
    if (withToken && token) headers.Authorization = `Bearer ${token}`;
    const init = { method: req.method, headers, redirect: "manual" };
    if (req.method !== "GET" && req.method !== "HEAD") {
      init.body = req;
      init.duplex = "half";
    }
    const abort = new AbortController();
    init.signal = abort.signal;
    res.on("close", () => abort.abort());
    let upstream;
    try {
      upstream = await fetchImpl(target + (query ? `?${query}` : ""), init);
    } catch {
      // Never the error text: it can carry the upstream URL.
      if (!res.headersSent) {
        send(res, 503, { "Content-Type": "application/json" }, JSON.stringify({ error: "unavailable", message: "helmstudio did not answer" }) + "\n");
      }
      return;
    }
    const out = { "X-Content-Type-Options": "nosniff" };
    for (const h of RESPONSE_HEADERS) {
      const v = upstream.headers.get(h);
      if (v !== null) out[h] = v;
    }
    res.writeHead(upstream.status, out);
    if (req.method === "HEAD" || !upstream.body) {
      res.end();
      return;
    }
    try {
      for await (const chunk of upstream.body) {
        if (!res.write(chunk)) await new Promise((r) => res.once("drain", r));
      }
    } catch {
      // the page went away, or the daemon did
    }
    res.end();
  }

  return async function handle(req, res) {
    const [path, query = ""] = req.url.split(/\?(.*)/s);
    if (!path.startsWith(PREFIX)) return false;
    const rest = path.slice(PREFIX.length - 1);
    if (!safePath(rest)) {
      notFound(res);
      return true;
    }
    if (rest === "/accent.css") {
      if (!css || (req.method !== "GET" && req.method !== "HEAD")) notFound(res);
      else send(res, 200, { "Content-Type": "text/css; charset=utf-8", "Cache-Control": "no-cache", "X-Content-Type-Options": "nosniff" }, req.method === "HEAD" ? undefined : css);
      return true;
    }
    if (rest.startsWith("/api/v1/")) {
      const sub = rest.slice("/api/v1/".length);
      if (!api || !STUDIO_SEGMENTS.has(sub.split("/")[0])) notFound(res);
      else await forward(req, res, `${api}/${sub}`, true, query);
      return true;
    }
    if (rest.startsWith("/sdk/v1/")) {
      if (!sdkBase || (req.method !== "GET" && req.method !== "HEAD")) notFound(res);
      else await forward(req, res, `${sdkBase}/${rest.slice("/sdk/v1/".length)}`, false, query);
      return true;
    }
    notFound(res);
    return true;
  };
}
