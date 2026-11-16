"""A fake Hugging Face Hub (test tooling, stdlib only): the endpoints huggingface_hub's snapshot_download and the
runner's pre-listing use, so model downloads are proven without the internet (contracts/README.md, Model jobs: Tests).

Service `fake-hub` of deploy/compose/fake.yml (no host port): HF_ENDPOINT=http://fake-hub:8000.

  GET  /api/models/<repo>/revision/<rev>        {id, sha, siblings [{rfilename, size, lfs}], gated, private}
  GET  /api/models/<repo>/tree/<rev>[/<dir>]    [{type, oid, size, path, lfs {oid, size, pointerSize}}]
  POST /api/models/<repo>/paths-info/<rev>      the same items for `paths`
  HEAD /<repo>/resolve/<rev>/<path>             ETag, X-Repo-Commit, X-Linked-Etag, X-Linked-Size, Content-Length
  GET  /<repo>/resolve/<rev>/<path>             the bytes; Range: bytes=N- → 206 (resume)
  GET  /__requests                              every resolve GET: {repo, path, range, start, status, auth, done}
                                                (done: the whole body was sent)
  GET  /__manifest                              repo → path → {size, sha256}: what a finished download must hold
  POST /__reset                                 forget the requests
  POST /__token {"present": bool}               write or remove the token file (TOKEN_DIR/hf_token) runners read

Repos (revision `main` or the commit sha REPOS[...]["sha"]; model.yaml names the sha):
  helm-test/echo-tiny         transformer 64 KiB, vae 32 KiB, config.json
  helm-test/echo-tiny-plus    transformer in 2 shards of 12 MiB at SLOW_BPS (1 MiB/s: ~24 s, so a cancel or a
                              restart lands between or in them; a resume must not fetch a finished shard again), the same vae
                              bytes as echo-tiny (a shared component by content hash)
  helm-test/echo-tiny-gated   transformer 16 KiB, the same vae; 401 GatedRepo without `Bearer <TOKEN>`
The token is a placeholder, never a real one. `python server.py call METHOD PATH [-]` makes one request from inside
the container (docker exec), as fake_openai does."""

import hashlib
import json
import os
import struct
import sys
import threading
import time
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import unquote, urlparse

PORT = int(os.environ.get("PORT", "8000"))
SLOW_BPS = int(os.environ.get("FAKE_HUB_SLOW_BPS", str(1024 * 1024)))
TOKEN = "placeholder-not-a-token"
TOKEN_DIR = Path(os.environ.get("TOKEN_DIR", "/hubsecrets"))


def stream(seed: str, n: int) -> bytes:
    """n deterministic pseudo-random bytes."""
    out, i = bytearray(), 0
    while len(out) < n:
        out += hashlib.sha256(f"{seed}:{i}".encode()).digest()
        i += 1
    return bytes(out[:n])


def safetensors(seed: str, n: int) -> bytes:
    """A valid safetensors file holding one BF16 tensor of n bytes."""
    head = json.dumps(
        {"weight": {"dtype": "BF16", "shape": [n // 2], "data_offsets": [0, n]}}
    ).encode()
    head += b" " * (-len(head) % 8)
    return struct.pack("<Q", len(head)) + head + stream(seed, n)


VAE = safetensors("vae", 32 * 1024)
REPOS = {
    "helm-test/echo-tiny": {
        "files": {
            "config.json": b'{"fake": "echo-tiny"}\n',
            "transformer/model.safetensors": safetensors("tiny", 64 * 1024),
            "vae/model.safetensors": VAE,
        },
    },
    "helm-test/echo-tiny-plus": {
        "files": {
            "config.json": b'{"fake": "echo-tiny-plus"}\n',
            "transformer/model-00001-of-00002.safetensors": safetensors("plus-1", 12 * 1024 * 1024),
            "transformer/model-00002-of-00002.safetensors": safetensors("plus-2", 12 * 1024 * 1024),
            "vae/model.safetensors": VAE,
        },
        "slow": {
            "transformer/model-00001-of-00002.safetensors",
            "transformer/model-00002-of-00002.safetensors",
        },
    },
    "helm-test/echo-tiny-gated": {
        "files": {
            "config.json": b'{"fake": "echo-tiny-gated"}\n',
            "transformer/model.safetensors": safetensors("gated", 16 * 1024),
            "vae/model.safetensors": VAE,
        },
        "gated": True,
    },
}
for _name, _repo in REPOS.items():
    _repo["sha"] = hashlib.sha1(_name.encode()).hexdigest()  # the fixed commit of every revision
    _repo.setdefault("slow", set())
    _repo["meta"] = {
        p: {
            "size": len(b),
            "sha256": hashlib.sha256(b).hexdigest(),
            "oid": hashlib.sha1(b"blob %d\0" % len(b) + b).hexdigest(),
        }
        for p, b in _repo["files"].items()
    }

REQUESTS: list[dict] = []
LOCK = threading.Lock()


def item(repo: dict, path: str) -> dict:
    m = repo["meta"][path]
    entry = {"type": "file", "oid": m["oid"], "size": m["size"], "path": path}
    if path.endswith(".safetensors"):  # LFS files: the Hub's SHA-256 is lfs.oid
        entry["lfs"] = {"oid": m["sha256"], "size": m["size"], "pointerSize": 134}
    return entry


def set_token(present: bool) -> None:
    path = TOKEN_DIR / "hf_token"
    if present:
        TOKEN_DIR.mkdir(parents=True, exist_ok=True)
        path.write_text(TOKEN + "\n")
        path.chmod(0o644)
    else:
        path.unlink(missing_ok=True)


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *args):
        pass

    def send(self, status: int, body=None, headers: dict | None = None, head: bool = False):
        data = (
            b"" if body is None else body if isinstance(body, bytes) else json.dumps(body).encode()
        )
        self.send_response(status)
        for k, v in (headers or {}).items():
            self.send_header(k, v)
        if not isinstance(body, bytes):
            self.send_header("Content-Type", "application/json")
        if "Content-Length" not in (headers or {}):
            self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        if not head:
            self.wfile.write(data)

    def error(self, status: int, code: str, message: str, head: bool = False):
        self.send(
            status, {"error": message}, {"X-Error-Code": code, "X-Error-Message": message}, head
        )

    def repo(self, name: str, rev: str, head: bool = False) -> dict | None:
        """The repo when it exists, `rev` is main or its sha and the caller may read it; else the error is sent."""
        repo = REPOS.get(name)
        if repo is None:
            return self.error(404, "RepoNotFound", f"Repository Not Found for url: {name}", head)
        if rev not in ("main", repo["sha"]):
            return self.error(404, "RevisionNotFound", f"Invalid rev id: {rev}", head)
        if repo.get("gated"):
            auth = self.headers.get("Authorization", "")
            if not auth:
                return self.error(401, "GatedRepo", f"Access to model {name} is restricted.", head)
            if auth != f"Bearer {TOKEN}":
                return self.error(403, "GatedRepo", f"Access to model {name} is restricted.", head)
        return repo

    def api(self, method: str, parts: list[str], body: dict) -> None:
        # /api/models/<org>/<name>/<op>/<rev>[/<dir>...]
        if len(parts) < 6 or parts[:2] != ["api", "models"]:
            return self.send(404, {"error": "not found"})
        name, op, rev, rest = "/".join(parts[2:4]), parts[4], unquote(parts[5]), "/".join(parts[6:])
        if (repo := self.repo(name, rev)) is None:
            return None
        paths = sorted(repo["files"])
        if op == "revision" and method == "GET":
            siblings = [
                {"rfilename": p, "size": (m := repo["meta"][p])["size"]}
                | ({"lfs": {"size": m["size"], "sha256": m["sha256"], "pointerSize": 134}}
                   if "lfs" in item(repo, p) else {})
                for p in paths
            ]  # fmt: skip
            gated = "manual" if repo.get("gated") else False
            return self.send(
                200,
                {
                    "id": name,
                    "sha": repo["sha"],
                    "siblings": siblings,
                    "gated": gated,
                    "private": False,
                },
            )
        if op == "tree" and method == "GET":
            prefix = rest.rstrip("/") + "/" if rest else ""
            return self.send(200, [item(repo, p) for p in paths if p.startswith(prefix)])
        if op == "paths-info" and method == "POST":
            want = body.get("paths") or []
            return self.send(200, [item(repo, p) for p in paths if p in want])
        return self.send(404, {"error": "not found"})

    def resolve(self, parts: list[str], head: bool) -> None:
        # /<org>/<name>/resolve/<rev>/<path...>
        name, rev, path = "/".join(parts[:2]), unquote(parts[3]), unquote("/".join(parts[4:]))
        if (repo := self.repo(name, rev, head)) is None:
            return None
        if path not in repo["files"]:
            return self.error(404, "EntryNotFound", f"{path} does not exist", head)
        data, meta = repo["files"][path], repo["meta"][path]
        etag = meta["sha256"] if "lfs" in item(repo, path) else meta["oid"]
        headers = {
            "ETag": f'"{etag}"',
            "X-Repo-Commit": repo["sha"],
            "X-Linked-Etag": f'"{etag}"',
            "X-Linked-Size": str(len(data)),
            "Accept-Ranges": "bytes",
            "Content-Type": "application/octet-stream",
        }
        start, rng = 0, self.headers.get("Range")
        if rng and rng.startswith("bytes="):
            start = int(rng[6:].split("-")[0] or 0)
        status = 206 if rng and not head else 200
        if not head:
            log = {"repo": name, "path": path, "range": rng, "start": start, "status": status,
                   "auth": bool(self.headers.get("Authorization")), "done": False}  # fmt: skip
            with LOCK:
                REQUESTS.append(log)
        if start >= len(data) and rng:
            return self.send(416, b"", {"Content-Range": f"bytes */{len(data)}"} | headers, head)
        body = data[start:]
        headers["Content-Length"] = str(len(body))
        if status == 206:
            headers["Content-Range"] = f"bytes {start}-{len(data) - 1}/{len(data)}"
        self.send_response(status)
        for k, v in headers.items():
            self.send_header(k, v)
        self.end_headers()
        if head:
            return None
        chunk = 64 * 1024
        slow = path in repo["slow"]
        try:
            for i in range(0, len(body), chunk):
                self.wfile.write(body[i : i + chunk])
                if slow:
                    time.sleep(chunk / SLOW_BPS)
            self.wfile.flush()
            log["done"] = True
        except BrokenPipeError, ConnectionResetError:
            pass  # a cancel or a restarted runner hung up: the client resumes with Range
        return None

    def route(self, method: str) -> None:
        url = urlparse(self.path)
        parts = [p for p in url.path.split("/") if p]
        size = int(self.headers.get("Content-Length") or 0)
        raw = self.rfile.read(size) if size else b""
        body = {}
        if raw and self.headers.get("Content-Type", "").startswith("application/json"):
            body = json.loads(raw)
        if url.path == "/__requests" and method == "GET":
            with LOCK:
                return self.send(200, list(REQUESTS))
        if url.path == "/__manifest" and method == "GET":
            return self.send(
                200, {n: {p: {"size": m["size"], "sha256": m["sha256"]} for p, m in r["meta"].items()}
                      for n, r in REPOS.items()},
            )  # fmt: skip
        if url.path == "/__reset" and method == "POST":
            with LOCK:
                REQUESTS.clear()
            return self.send(200, {"ok": True})
        if url.path == "/__token" and method == "POST":
            set_token(bool(body.get("present")))
            return self.send(200, {"present": (TOKEN_DIR / "hf_token").is_file()})
        if parts[:1] == ["api"]:
            return self.api(method, parts, body)
        if len(parts) >= 5 and parts[2] == "resolve" and method in ("GET", "HEAD"):
            return self.resolve(parts, method == "HEAD")
        return self.send(404, {"error": "not found"})

    def do_GET(self):
        self.route("GET")

    def do_HEAD(self):
        self.route("HEAD")

    def do_POST(self):
        self.route("POST")


def serve() -> None:
    set_token(True)  # a host with a token by default; tests remove it to see the gated failure
    print(f"fake hub on :{PORT}, token file {TOKEN_DIR}/hf_token", flush=True)
    ThreadingHTTPServer(("0.0.0.0", PORT), Handler).serve_forever()


def call(method: str, path: str, stdin: bool) -> None:
    data = sys.stdin.buffer.read() if stdin else None
    req = urllib.request.Request(f"http://127.0.0.1:{PORT}{path}", data=data, method=method)
    req.add_header("Content-Type", "application/json")
    with urllib.request.urlopen(req, timeout=10) as r:
        sys.stdout.write(r.read().decode())


if __name__ == "__main__":
    if sys.argv[1:2] == ["call"]:
        call(sys.argv[2], sys.argv[3], sys.argv[4:5] == ["-"])
    else:
        serve()
