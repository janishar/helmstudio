"""helmstudio plugin web-search (contracts/README.md "Plugins"): one Assistant tool, `search`, through DuckDuckGo with
the unofficial ddgs library. Stdlib HTTP server; listens on WEB_SEARCH_HOST:WEB_SEARCH_PORT (127.0.0.1:8421).

    GET  /health        → 200 {"ok": true}
    POST /tools/search  {"input": {"query", "limit"?}} → 200 {"result": {"results": [...]}, "line", "sources"}

Errors are {"detail": "..."}: 422 bad input, 429 rate-limited, 502 any other search failure. The bearer pass,
X-Helm-Owner and X-Helm-Call that pipelines sends are not needed here (ponytail: no verify endpoint yet)."""

import json
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from ddgs import DDGS
from ddgs.exceptions import DDGSException, RatelimitException

MAX_BODY = 16_000


def search(query: str, limit: int) -> list[dict]:
    return DDGS().text(query, max_results=limit, backend="duckduckgo")


def answer(body) -> tuple[int, dict]:
    """(status, JSON body) for one POST /tools/search body."""
    inp = body.get("input") if isinstance(body, dict) else None
    q = inp.get("query") if isinstance(inp, dict) else None
    limit = inp.get("limit", 5) if isinstance(inp, dict) else None
    if not isinstance(q, str) or not q.strip() or len(q) > 200:
        return 422, {"detail": "query must be text of 1 to 200 characters"}
    if type(limit) is not int or not 1 <= limit <= 10:
        return 422, {"detail": "limit must be a whole number from 1 to 10"}
    try:
        found = search(q.strip(), limit)
    except RatelimitException:
        return 429, {"detail": "DuckDuckGo is rate-limiting searches; try again in a minute"}
    except DDGSException as e:
        return 502, {"detail": f"DuckDuckGo search failed: {str(e) or type(e).__name__}"[:200]}
    results = [{"title": str(r.get("title") or "")[:200], "url": str(r.get("href") or "")[:500],
                "snippet": str(r.get("body") or "")[:500]} for r in found[:limit]]  # fmt: skip
    return 200, {"result": {"results": results}, "line": f'Searched the web · DuckDuckGo · "{q.strip()}"'[:300],
                 "sources": [{"title": r["title"], "url": r["url"]} for r in results[:5]]}  # fmt: skip


class Handler(BaseHTTPRequestHandler):
    def reply(self, code: int, body: dict) -> None:
        out = json.dumps(body, ensure_ascii=False).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(out)))
        self.end_headers()
        self.wfile.write(out)

    def do_GET(self):
        self.reply(*((200, {"ok": True}) if self.path == "/health" else (404, {"detail": "not found"})))

    def do_POST(self):
        if self.path != "/tools/search":
            return self.reply(404, {"detail": "not found"})
        n = int(self.headers.get("Content-Length") or 0)
        if n > MAX_BODY:
            return self.reply(413, {"detail": "the request is too large"})
        try:
            body = json.loads(self.rfile.read(n) or b"null")
        except ValueError:
            return self.reply(422, {"detail": "the body is not JSON"})
        self.reply(*answer(body))


if __name__ == "__main__":
    host, port = os.environ.get("WEB_SEARCH_HOST", "127.0.0.1"), int(os.environ.get("WEB_SEARCH_PORT", "8421"))
    print(f"web-search plugin on http://{host}:{port}", flush=True)
    ThreadingHTTPServer((host, port), Handler).serve_forever()
