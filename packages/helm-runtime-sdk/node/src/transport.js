// HTTP transport for the helmstudio runtime SDK: fetch only, no Node built-ins,
// so the same file can serve a browser build once M6 decides browser access.

// The one typed error shape across languages (docs/design/04-packages.md §4).
const KINDS = {
  400: "Invalid", 413: "Invalid", 416: "Invalid", 422: "Invalid",
  401: "Unauthenticated",
  403: "Forbidden", 421: "Forbidden",
  404: "NotFound", 410: "NotFound",
  409: "Conflict",
  429: "QuotaExceeded", 507: "QuotaExceeded",
  501: "Unsupported",
  503: "Unavailable",
};

export class HelmError extends Error {
  constructor(status, code, message, details) {
    super(`${code} (${status}): ${message}`);
    this.status = status;
    this.code = code;
    this.details = details || {};
  }

  get kind() {
    if (this.status === 0) return "Unavailable";
    return KINDS[this.status] || "Internal";
  }
}

function value(v) {
  if (v instanceof Date) return v.toISOString();
  return String(v);
}

async function* events(response) {
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  let ev = { id: "", name: "", data: [] };
  try {
    for (;;) {
      const { done, value: chunk } = await reader.read();
      if (done) return;
      buffer += decoder.decode(chunk, { stream: true });
      let nl;
      while ((nl = buffer.indexOf("\n")) >= 0) {
        const line = buffer.slice(0, nl).replace(/\r$/, "");
        buffer = buffer.slice(nl + 1);
        if (line === "") {
          if (ev.name || ev.data.length) {
            const data = ev.data.join("\n");
            yield { id: ev.id, name: ev.name, data, json: () => (data ? JSON.parse(data) : null) };
          }
          ev = { id: "", name: "", data: [] };
        } else if (line.startsWith(":")) {
          continue;
        } else if (line.startsWith("id:")) {
          ev.id = line.slice(3).trim();
        } else if (line.startsWith("event:")) {
          ev.name = line.slice(6).trim();
        } else if (line.startsWith("data:")) {
          ev.data.push(line.slice(5).replace(/^ /, ""));
        }
      }
    }
  } finally {
    reader.releaseLock();
  }
}

export class Transport {
  constructor(base, token, { fetch: fetchImpl } = {}) {
    this.base = base.replace(/\/+$/, "");
    this.token = token;
    this.fetch = fetchImpl || globalThis.fetch.bind(globalThis);
  }

  async request(method, path, { query = {}, headers = {}, expect, json, raw, contentType }) {
    const params = new URLSearchParams();
    for (const [k, v] of Object.entries(query)) {
      if (v === undefined || v === null) continue;
      if (Array.isArray(v)) v.forEach((x) => params.append(k, value(x)));
      else params.append(k, value(v));
    }
    const qs = params.toString();
    const h = { Authorization: `Bearer ${this.token}` };
    for (const [k, v] of Object.entries(headers)) {
      if (v !== undefined && v !== null) h[k] = value(v);
    }
    let body;
    if (json !== undefined) body = JSON.stringify(json);
    else if (raw !== undefined) body = raw;
    if (contentType) h["Content-Type"] = contentType;

    let response;
    try {
      response = await this.fetch(this.base + path + (qs ? `?${qs}` : ""), { method, headers: h, body });
    } catch (e) {
      throw new HelmError(0, "unavailable", String(e && e.message ? e.message : e));
    }
    if (!response.ok) {
      const text = await response.text();
      let doc;
      try {
        doc = JSON.parse(text);
      } catch {
        throw new HelmError(response.status, "unexpected_response", text);
      }
      throw new HelmError(response.status, doc.error || "unexpected_response", doc.message || "", doc.details);
    }
    if (expect === "sse") return events(response);
    if (expect === "raw") return response;
    if (expect === "json") return response.json();
    await response.arrayBuffer();
    return undefined;
  }
}
