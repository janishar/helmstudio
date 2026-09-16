"""lantern studio's server: its page, its stylesheet, and helmstudio's proxy.

The proxy is mounted at /helm/, so the page reaches helm-css, its own hue and
the theme stream from its own origin, and never holds a token.
"""

import argparse
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from helm_runtime_sdk.proxy import Proxy

HERE = os.path.dirname(os.path.abspath(__file__))
PAGES = {"/": ("index.html", "text/html; charset=utf-8"), "/studio.css": ("studio.css", "text/css; charset=utf-8")}

# HELM_API, HELM_SDK_BASE and the hue are set by helmstudio, or by helm dev.
proxy = Proxy.from_env()


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if proxy.handle_http(self):
            return
        if self.path == "/healthz":
            return self.reply(200, b"ok", "text/plain")
        if self.path in PAGES:
            name, kind = PAGES[self.path]
            with open(os.path.join(HERE, name), "rb") as f:
                return self.reply(200, f.read(), kind)
        self.reply(404, b"not found", "text/plain")

    do_HEAD = do_GET

    def reply(self, status, body, kind):
        self.send_response(status)
        self.send_header("Content-Type", kind)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        if self.command != "HEAD":
            self.wfile.write(body)

    def log_message(self, format, *args):
        pass


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, required=True)
    args = parser.parse_args()
    print("lantern studio: http://127.0.0.1:%d" % args.port, flush=True)
    ThreadingHTTPServer(("127.0.0.1", args.port), Handler).serve_forever()


if __name__ == "__main__":
    main()
