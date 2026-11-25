"""The fake-hostkey plugin's service (test stack only, API 1.73): the fake runner starts it while the plugin is on. On
first start it makes its key in $HELM_HOME/fake-hostkey.env (as the Claude Code bridge's run.sh does); GET /health,
and GET /v1/models only with that key (else 401), on 0.0.0.0:8472 inside the fake runner's container."""

import json
import os
import secrets
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

ENV = Path(os.environ["HELM_HOME"]) / "fake-hostkey.env"
if not ENV.exists():
    ENV.write_text(f"OTHER=not-the-key\nFAKE_HOSTKEY_KEY={secrets.token_urlsafe(24)}\n")
KEY = next(
    v
    for k, _, v in (x.partition("=") for x in ENV.read_text().splitlines())
    if k == "FAKE_HOSTKEY_KEY"
)


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def send(self, code, value):
        data = json.dumps(value).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        if self.path == "/health":
            return self.send(200, {"ok": True})
        if self.headers.get("Authorization") != f"Bearer {KEY}":
            return self.send(401, {"error": "bad key"})
        self.send(200, {"object": "list", "data": [{"id": "fake-small", "object": "model"}]})


print("fake-hostkey listening on 8472", flush=True)
ThreadingHTTPServer(("0.0.0.0", 8472), Handler).serve_forever()
