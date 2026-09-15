"""A studio server that only mounts the helm proxy, for test/conformance.

Reads HELM_API, HELM_TOKEN, HELM_SDK_BASE and HELM_ACCENT_* like a studio
would, listens on a free loopback port, and prints "listening <port>".
"""

import os
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from helm_runtime_sdk.proxy import Proxy  # noqa: E402

proxy = Proxy.from_env()


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def _any(self):
        if not proxy.handle_http(self):
            self.send_response(418)
            self.send_header("Content-Length", "0")
            self.end_headers()

    do_GET = do_HEAD = do_POST = do_PUT = do_PATCH = do_DELETE = do_OPTIONS = _any

    def log_message(self, *args):
        pass


server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
server.daemon_threads = True
print("listening %d" % server.server_address[1], flush=True)
server.serve_forever()
