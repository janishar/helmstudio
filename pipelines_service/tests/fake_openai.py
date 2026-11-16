"""A fake OpenAI-compatible server (contracts/README.md "The fake OpenAI-compatible server"): nothing real is ever
called in tests. Stdlib only, so it also runs on its own in the fake compose overlay and the demonstration:

    python fake_openai.py --port 8399 [--host 0.0.0.0]

- GET  /v1/models            the scripted models (and `fake-large`, `fake-small`)
- POST /v1/chat/completions  OpenAI's shape: answers choices[0].message {content, tool_calls} and usage
- POST /__script             {model: [response, ...]} queues responses per model; each is {content?, tool_calls?,
                             usage?} (tool_calls: [{name, arguments}], arguments a JSON value or its text),
                             {status, body, headers?} for a failure (a 307 with a Location header: a redirect),
                             {no_usage: true} to report no usage, or adds {delay_s} to answer late (a timeout); a
                             request whose `tools` is not a list is refused with 400, as a real provider does
- GET  /__requests           every request received: {path, headers, body}; POST /__reset clears both

An unscripted model answers `OK`. No key is needed; an Authorization header is recorded like any."""

import argparse
import json
import threading
import time
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

MODELS = ["fake-large", "fake-small"]


class Fake:
    def __init__(self) -> None:
        self.script: dict[str, list[dict]] = {}
        self.requests: list[dict] = []
        self.lock = threading.Lock()
        self.in_flight = self.max_in_flight = 0

    def next(self, model: str) -> dict:
        with self.lock:
            q = self.script.get(model) or []
            return q.pop(0) if q else {"content": "OK"}


def completion(model: str, r: dict) -> dict:
    calls = [{"id": c.get("id") or f"call_{uuid.uuid4().hex[:8]}", "type": "function",
              "function": {"name": c["name"], "arguments": c["arguments"] if isinstance(c["arguments"], str)
                           else json.dumps(c["arguments"])}} for c in r.get("tool_calls") or []]  # fmt: skip
    msg = {"role": "assistant", "content": r.get("content", "" if calls else "OK")}
    if calls:
        msg["tool_calls"] = calls
    usage = {"prompt_tokens": 10, "completion_tokens": 5} | (r.get("usage") or {})
    usage["total_tokens"] = usage["prompt_tokens"] + usage["completion_tokens"]
    out = {"id": f"chatcmpl-{uuid.uuid4().hex[:12]}", "object": "chat.completion", "created": int(time.time()),
           "model": model, "choices": [{"index": 0, "message": msg,
                                        "finish_reason": "tool_calls" if calls else "stop"}]}  # fmt: skip
    return out if r.get("no_usage") else out | {"usage": usage}


def handler(fake: Fake):
    class H(BaseHTTPRequestHandler):
        def log_message(self, *a) -> None:
            pass

        def _send(self, status: int, body, headers: dict | None = None) -> None:
            data = (body if isinstance(body, str) else json.dumps(body)).encode()
            self.send_response(status)
            for k, v in (headers or {}).items():
                self.send_header(k, v)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def _body(self) -> dict:
            n = int(self.headers.get("Content-Length") or 0)
            return json.loads(self.rfile.read(n) or b"{}")

        def do_GET(self) -> None:
            if self.path == "/__requests":
                return self._send(200, fake.requests)
            if self.path.rstrip("/").endswith("/models"):
                fake.requests.append(
                    {
                        "path": self.path,
                        "headers": {k.lower(): v for k, v in self.headers.items()},
                        "body": None,
                    }
                )
                ids = sorted(set(MODELS) | set(fake.script))
                return self._send(
                    200, {"object": "list", "data": [{"id": m, "object": "model"} for m in ids]}
                )
            self._send(404, {"error": {"message": "not found"}})

        def do_POST(self) -> None:
            body = self._body()
            if self.path == "/__script":
                with fake.lock:
                    for m, rs in body.items():
                        fake.script.setdefault(m, []).extend(rs)
                return self._send(200, {"ok": True})
            if self.path == "/__reset":
                with fake.lock:
                    fake.script.clear()
                    fake.requests.clear()
                    fake.max_in_flight = 0
                return self._send(200, {"ok": True})
            if not self.path.rstrip("/").endswith("/chat/completions"):
                return self._send(404, {"error": {"message": "not found"}})
            fake.requests.append(
                {
                    "path": self.path,
                    "headers": {k.lower(): v for k, v in self.headers.items()},
                    "body": body,
                }
            )
            if "tools" in body and not isinstance(body["tools"], list):
                return self._send(400, {"error": {"message": "tools must be an array"}})
            r = fake.next(body.get("model", ""))
            with fake.lock:
                fake.in_flight += 1
                fake.max_in_flight = max(fake.max_in_flight, fake.in_flight)
            try:
                if r.get("delay_s"):
                    time.sleep(r["delay_s"])
                if "status" in r:
                    return self._send(
                        r["status"],
                        r.get("body", {"error": {"message": "scripted"}}),
                        r.get("headers"),
                    )
                self._send(200, completion(body.get("model", ""), r))
            finally:
                with fake.lock:
                    fake.in_flight -= 1

    return H


def serve(host: str = "127.0.0.1", port: int = 0) -> tuple[ThreadingHTTPServer, Fake]:
    """Start the server in a daemon thread; returns it (server_address has the port) and its state."""
    fake = Fake()
    srv = ThreadingHTTPServer((host, port), handler(fake))
    srv.daemon_threads = True
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return srv, fake


if __name__ == "__main__":
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--host", default="127.0.0.1")
    ap.add_argument("--port", type=int, default=8399)
    a = ap.parse_args()
    srv = ThreadingHTTPServer((a.host, a.port), handler(Fake()))
    print(f"fake OpenAI server on http://{a.host}:{a.port}/v1", flush=True)
    srv.serve_forever()
