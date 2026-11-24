"""The fake-run plugin's service (test stack only, API 1.67): the fake runner starts it while the plugin is on.
GET /health and POST /tools/ping on 0.0.0.0:8471 (inside the fake runner's container)."""

import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


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
        self.send(200 if self.path == "/health" else 404, {"ok": True})

    def do_POST(self):
        self.send(200, {"result": {"pong": True}, "line": "Pong"})


print("fake-run listening on 8471", flush=True)
ThreadingHTTPServer(("0.0.0.0", 8471), Handler).serve_forever()
