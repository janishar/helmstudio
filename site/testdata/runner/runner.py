"""Runs a sample in the studio's own environment, and says how it went.

GET /run?file=<absolute path> runs `python3 <file>` with the environment this
process was given — HELM_API, HELM_TOKEN, HELM_STAGE_DIR — and answers with the
exit code and the output.
"""

import argparse
import json
import subprocess
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        url = urlparse(self.path)
        if url.path == "/healthz":
            body = {"ok": True}
        elif url.path == "/run":
            file = parse_qs(url.query)["file"][0]
            done = subprocess.run([sys.executable, file], capture_output=True, text=True, timeout=120)
            body = {"code": done.returncode, "stdout": done.stdout, "stderr": done.stderr}
        else:
            self.send_response(404)
            self.end_headers()
            return
        data = json.dumps(body).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, format, *args):
        pass


parser = argparse.ArgumentParser()
parser.add_argument("--port", type=int, required=True)
print("runner: http://127.0.0.1:%d" % parser.parse_args().port, flush=True)
ThreadingHTTPServer(("127.0.0.1", parser.parse_args().port), Handler).serve_forever()
