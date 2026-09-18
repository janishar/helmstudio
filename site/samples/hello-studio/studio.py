"""hello studio: makes a small image from a prompt, and records it.

Everything that touches helmstudio goes through the runtime SDK's client:
adopting the file the studio wrote, and recording a gallery item with the
parameters that made it. The rest is the studio's own business.
"""

import argparse
import hashlib
import json
import os
import struct
import zlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from helm_runtime_sdk import from_env

# HELM_API and HELM_TOKEN are set by helmstudio, or by helm dev.
helm = from_env()


def make_png(prompt, seed, size=256):
    """A gradient whose colours come from the prompt and the seed."""
    digest = hashlib.sha256(("%s|%d" % (prompt, seed)).encode()).digest()
    a, b = digest[0:3], digest[3:6]
    rows = []
    for y in range(size):
        row = bytearray([0])  # no filter
        for x in range(size):
            t = (x + y) / (2 * (size - 1))
            row += bytes(int(a[i] + (b[i] - a[i]) * t) for i in range(3))
        rows.append(bytes(row))

    def chunk(kind, data):
        body = kind + data
        return struct.pack(">I", len(data)) + body + struct.pack(">I", zlib.crc32(body) & 0xFFFFFFFF)

    header = struct.pack(">IIBBBBB", size, size, 8, 2, 0, 0, 0)
    return (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", header)
            + chunk(b"IDAT", zlib.compress(b"".join(rows))) + chunk(b"IEND", b""))


# helm:region sdk
def make(prompt, seed):
    """Write the image to the stage, adopt it, and record it."""
    stage = os.environ["HELM_STAGE_DIR"]
    path = os.path.join(stage, "hello-%d.png" % seed)
    with open(path, "wb") as f:
        f.write(make_png(prompt, seed))

    # Adopting moves the file into helmstudio's asset store without copying it.
    asset = helm.assets.adopt({"path": path, "kind": "image"})

    # The item is what the gallery shows, with what made it.
    item = helm.gallery.add({
        "kind": "image",
        "asset_id": asset["id"],
        "title": prompt,
        "params": {"prompt": prompt, "seed": seed, "width": 256, "height": 256},
    })
    return {"item_id": item["id"], "asset_id": asset["id"]}
# helm:endregion sdk


class Handler(BaseHTTPRequestHandler):
    def reply(self, status, body):
        data = json.dumps(body).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        if self.path == "/healthz":
            return self.reply(200, {"ok": True})
        if self.path == "/items":
            page = helm.gallery.query(limit=10)
            return self.reply(200, [{"id": i["id"], "title": i.get("title"), "params": i.get("params")}
                                    for i in page["items"]])
        return self.reply(200, {"studio": "hello studio", "make": "POST /make {prompt, seed}", "items": "GET /items"})

    def do_POST(self):
        if self.path != "/make":
            return self.reply(404, {"error": "not_found"})
        length = int(self.headers.get("Content-Length") or 0)
        body = json.loads(self.rfile.read(length) or b"{}")
        return self.reply(200, make(body.get("prompt", "a lighthouse at dusk"), int(body.get("seed", 42))))

    def log_message(self, format, *args):
        pass  # keep helm dev's output to what matters


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, required=True)
    args = parser.parse_args()
    print("hello studio: http://127.0.0.1:%d" % args.port, flush=True)
    ThreadingHTTPServer(("127.0.0.1", args.port), Handler).serve_forever()


if __name__ == "__main__":
    main()
