"""A fake Hugging Face Hub (stdlib only) speaking exactly what huggingface_hub 2.4 calls for a model download:

- GET  /api/models/<repo>/revision/<rev>          model_info: {id, sha, siblings [{rfilename, size, lfs?}]}
- GET  /api/models/<repo>/tree/<commit>?recursive list_repo_tree: [{type, oid, size, path, lfs?}]
- HEAD /<repo>/resolve/<rev>/<path>               ETag, X-Repo-Commit, X-Linked-Etag, X-Linked-Size
- GET  /<repo>/resolve/<rev>/<path>               the bytes; `Range: bytes=N-` → 206 with Content-Range

A `*.safetensors` file is an LFS file (its ETag is its SHA-256). A gated repo answers 401 (X-Error-Code GatedRepo)
without `Authorization: Bearer <token>`, 403 with another token. Knobs for the tests: `cut` (paths whose first GET
stops halfway, closing the connection), `corrupt` (paths served with other bytes than their advertised hash),
`delay` (seconds per 64 KB chunk). `requests` records (method, path, Range, had a token).

`python tests/fake_hub.py [port]` serves a small demo repo (helmstudio-test/tiny) for the fake compose overlay."""

import hashlib
import json
import re
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Self
from urllib.parse import unquote, urlsplit

CHUNK = 1 << 16


class Repo:
    def __init__(self, files: dict[str, bytes], *, commit: str | None = None, gated_token: str | None = None,
                 licence: str | None = None) -> None:  # fmt: skip
        self.files, self.gated_token, self.licence = files, gated_token, licence
        self.commit = commit or hashlib.sha1(json.dumps(sorted(files)).encode()).hexdigest()

    def entry(self, path: str) -> dict:
        data = self.files[path]
        out = {
            "type": "file",
            "oid": hashlib.sha1(data).hexdigest(),
            "size": len(data),
            "path": path,
        }
        if path.endswith(".safetensors"):
            out["lfs"] = {
                "oid": hashlib.sha256(data).hexdigest(),
                "size": len(data),
                "pointerSize": 134,
            }
        return out


class FakeHub:
    def __init__(self, repos: dict[str, Repo]) -> None:
        self.repos = repos
        self.cut: set[str] = set()
        self.corrupt: set[str] = set()
        self.delay = 0.0
        self.requests: list[tuple[str, str, str | None, bool]] = []
        hub = self

        class Handler(BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"

            def log_message(self, *a) -> None:
                pass

            def do_HEAD(self) -> None:
                hub.serve(self, head=True)

            def do_GET(self) -> None:
                hub.serve(self, head=False)

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.url = f"http://127.0.0.1:{self.server.server_address[1]}"

    def __enter__(self) -> Self:
        threading.Thread(target=self.server.serve_forever, daemon=True).start()
        return self

    def __exit__(self, *exc) -> None:
        self.server.shutdown()
        self.server.server_close()

    def error(self, h, code: int, error_code: str) -> None:
        body = json.dumps({"error": error_code}).encode()
        h.send_response(code)
        h.send_header("X-Error-Code", error_code)
        h.send_header("Content-Type", "application/json")
        h.send_header("Content-Length", str(len(body)))
        h.end_headers()
        h.wfile.write(body)

    def json(self, h, value) -> None:
        body = json.dumps(value).encode()
        h.send_response(200)
        h.send_header("Content-Type", "application/json")
        h.send_header("Content-Length", str(len(body)))
        h.end_headers()
        h.wfile.write(body)

    def serve(self, h, head: bool) -> None:
        url = urlsplit(h.path)
        path = unquote(url.path)
        auth = h.headers.get("Authorization", "")
        self.requests.append((h.command, path, h.headers.get("Range"), auth.startswith("Bearer ")))
        if m := re.fullmatch(r"/api/models/([^/]+/[^/]+)/(revision|tree)/([^/]+)", path):
            name, kind, rev = m.groups()
        elif m := re.fullmatch(r"/([^/]+/[^/]+)/resolve/([^/]+)/(.+)", path):
            (name, rev, file), kind = m.groups(), "resolve"
        else:
            return self.error(h, 404, "EntryNotFound")
        repo = self.repos.get(name)
        if repo is None:
            return self.error(h, 404, "RepoNotFound")
        if repo.gated_token and auth != f"Bearer {repo.gated_token}":
            return self.error(h, 403 if auth else 401, "GatedRepo")
        if rev not in (repo.commit, "main"):
            return self.error(h, 404, "RevisionNotFound")
        if kind == "revision":
            siblings = [{"rfilename": p, "size": e["size"], "blobId": e["oid"]} | (
                {"lfs": {"sha256": e["lfs"]["oid"], "size": e["size"], "pointerSize": 134}} if "lfs" in e else {})
                for p in sorted(repo.files) for e in [repo.entry(p)]]  # fmt: skip
            card = {"license": repo.licence} if repo.licence else {}
            return self.json(h, {"id": name, "sha": repo.commit, "siblings": siblings, "cardData": card,
                                 "gated": "manual" if repo.gated_token else False})  # fmt: skip
        if kind == "tree":
            return self.json(h, [repo.entry(p) for p in sorted(repo.files)])
        if file not in repo.files:
            return self.error(h, 404, "EntryNotFound")
        data, e = repo.files[file], repo.entry(file)
        if file in self.corrupt:
            data = bytes(len(data))
        start = int(m[1]) if (m := re.fullmatch(r"bytes=(\d+)-", h.headers.get("Range", ""))) else 0
        h.send_response(206 if start else 200)
        h.send_header("ETag", f'"{e["oid"]}"')
        h.send_header("X-Repo-Commit", repo.commit)
        if "lfs" in e:
            h.send_header("X-Linked-Etag", f'"{e["lfs"]["oid"]}"')
            h.send_header("X-Linked-Size", str(len(data)))
        h.send_header("Accept-Ranges", "bytes")
        h.send_header("Content-Length", str(len(data) - start))
        if start:
            h.send_header("Content-Range", f"bytes {start}-{len(data) - 1}/{len(data)}")
        h.end_headers()
        if head:
            return
        body = data[start:]
        if file in self.cut and not start:
            self.cut.discard(file)
            h.wfile.write(body[: len(body) // 2])
            h.wfile.flush()
            h.close_connection = True
            return
        for i in range(0, len(body), CHUNK):
            if self.delay:
                time.sleep(self.delay)
            try:
                h.wfile.write(body[i : i + CHUNK])
            except OSError:
                return


def demo() -> dict[str, Repo]:
    """The repo the fake compose overlay serves: two small 'weight' files and a config."""
    blob = bytes(range(256)) * 4096  # 1 MiB
    return {"helmstudio-test/tiny": Repo({"model_index.json": b'{"name": "tiny"}',
                                          "transformer/a.safetensors": blob,
                                          "vae/b.safetensors": blob[::-1]}, licence="apache-2.0")}  # fmt: skip


if __name__ == "__main__":
    hub = FakeHub(demo())
    hub.server.server_close()
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 8077
    hub.server = ThreadingHTTPServer(("0.0.0.0", port), hub.server.RequestHandlerClass)
    print(f"fake hub on :{port}", flush=True)
    hub.server.serve_forever()
