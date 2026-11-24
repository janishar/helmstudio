"""The fake OpenAI-compatible server (contracts/README.md, Model sources: the fake). Test tooling only: stdlib, no
key, nothing real is ever called. It serves http on PORT (a local server's base_url) and https on TLS_PORT (a remote
provider's: remote base_urls must be https) with a self-signed certificate made at start, written to CERT_DIR for
pipelines_service to trust (SSL_CERT_FILE).

  GET  /v1/models            the scripted models and FAKE_MODELS; API 1.52 (plan 27, a key's check): a bearer key
                             starting `refused-` is 401, `forbidden-` 403, `down-` 503; any other key is accepted
  POST /v1/chat/completions  OpenAI's shape; the next scripted response for body.model, else the prompt's script:
                             the JSON after the `ECHO-TURN <n>:` of its last `This is turn <n> of` line, else after
                             the last `ECHO-ANSWER:` (echo-ask's rules), else "ok". With `tools`, a {say, calls} turn
                             is answered as native tool_calls; `tools` that is not an array is refused (400).
  POST /__script             {model: [response, …]} queued per model; each {content?, tool_calls?, usage?},
                             {status, body} (a failure) or {delay_s} (sleep first; with nothing else: then the prompt's
                             script)
  GET  /__requests           every request received: {model, body, authorization, t0, t1, status}
  POST /__reset              forget scripts and requests

  python server.py                       serve
  python server.py call METHOD PATH [-]  one request to the running server (JSON body on stdin with -): for tests that
                                         reach it with docker exec, since it publishes no host port
"""

import json
import os
import re
import ssl
import subprocess
import sys
import threading
import time
import urllib.request
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PORT = int(os.environ.get("PORT", "8000"))
TLS_PORT = int(os.environ.get("TLS_PORT", "8443"))
CERT_DIR = os.environ.get("CERT_DIR", "/certs")
NAME = os.environ.get("TLS_NAME", "fake-openai")
MODELS = [m for m in os.environ.get("FAKE_MODELS", "fake-small,fake-large").split(",") if m]

lock = threading.Lock()
scripts: dict[str, list[dict]] = {}
requests: list[dict] = []
TURN = re.compile(r"This is turn (\d+) of")
decoder = json.JSONDecoder()


def after(text: str, marker: str):
    """The first JSON value after the last `marker` in text, or None."""
    i = text.rfind(marker)
    if i < 0:
        return None
    try:
        return decoder.raw_decode(text[i + len(marker) :].lstrip())[0]
    except ValueError:
        return None


def from_prompt(body: dict) -> dict:
    """The prompt's script as a response: {content, tool_calls}."""
    text = "\n".join(
        m["content"] if isinstance(m.get("content"), str) else json.dumps(m.get("content"))
        for m in body.get("messages", [])
    )
    value = None
    if turns := TURN.findall(text):
        value = after(text, f"ECHO-TURN {turns[-1]}:")
    if value is None:
        value = after(text, "ECHO-ANSWER:")
    if value is None:
        return {"content": "ok"}
    if body.get("tools") and isinstance(value, dict) and {"say", "calls"} <= set(value):
        return {"content": value["say"], "tool_calls": native(value["calls"])}
    return {"content": value if isinstance(value, str) else json.dumps(value)}


def native(calls: list[dict]) -> list[dict]:
    return [
        {
            "id": f"call_{uuid.uuid4().hex[:12]}",
            "type": "function",
            "function": {"name": c["tool"], "arguments": json.dumps(c.get("args", {}))},
        }
        for c in calls
    ]


def completion(model: str, resp: dict, body: dict) -> dict:
    content, calls = resp.get("content"), resp.get("tool_calls")
    if calls and "function" not in calls[0]:  # scripted as turn calls
        calls = native(calls)
    prompt = sum(len(str(m.get("content", ""))) for m in body.get("messages", [])) // 4
    usage = {"prompt_tokens": prompt, "completion_tokens": len(content or "") // 4 + 1}
    usage |= resp.get("usage", {})
    usage["total_tokens"] = usage["prompt_tokens"] + usage["completion_tokens"]
    message = {"role": "assistant", "content": content}
    if calls:
        message["tool_calls"] = calls
    return {
        "id": f"chatcmpl-{uuid.uuid4().hex[:12]}",
        "object": "chat.completion",
        "created": int(time.time()),
        "model": model,
        "choices": [
            {"index": 0, "message": message, "finish_reason": "tool_calls" if calls else "stop"}
        ],
        "usage": usage,
    }


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *_):  # quiet
        pass

    def reply(self, status: int, value) -> None:
        data = json.dumps(value).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def body(self):
        n = int(self.headers.get("Content-Length") or 0)
        return json.loads(self.rfile.read(n) or b"{}")

    def do_GET(self):
        if self.path.rstrip("/").endswith("/v1/models"):
            key = (self.headers.get("Authorization") or "").removeprefix("Bearer ")
            for prefix, status in (("refused-", 401), ("forbidden-", 403), ("down-", 503)):
                if key.startswith(prefix):
                    return self.reply(status, {"error": {"message": "scripted"}})
            with lock:
                names = sorted(set(MODELS) | set(scripts))
            return self.reply(
                200, {"object": "list", "data": [{"id": m, "object": "model"} for m in names]}
            )
        if self.path == "/__requests":
            with lock:
                return self.reply(200, list(requests))
        if (
            self.path == "/health"
        ):  # API 1.61: the fake plugins' health (contract_tests/fake_plugins)
            return self.reply(200, {"ok": True})
        self.reply(404, {"error": {"message": "not found"}})

    def do_POST(self):
        try:
            body = self.body()
        except ValueError:
            return self.reply(400, {"error": {"message": "not JSON"}})
        if self.path == "/__script":
            with lock:
                for model, items in body.items():
                    scripts.setdefault(model, []).extend(items)
            return self.reply(200, {"ok": True})
        if self.path == "/__reset":
            with lock:
                scripts.clear()
                requests.clear()
            return self.reply(200, {"ok": True})
        if self.path.startswith(
            "/tools/"
        ):  # API 1.61: the fake-tools plugin's tools (manifest toolCall/toolReply)
            name = self.path.removeprefix("/tools/")
            with lock:
                requests.append({"model": f"tool:{name}", "body": body, "authorization": self.headers.get("Authorization"),
                                 "owner": self.headers.get("X-Helm-Owner"), "call": self.headers.get("X-Helm-Call"),
                                 "t0": time.time(), "status": 200})  # fmt: skip
            text = str((body.get("input") or {}).get("text", ""))
            if text.startswith("fail"):
                return self.reply(500, {"detail": f"scripted failure: {text}"})
            return self.reply(
                200, {"result": {"echo": body.get("input")}, "line": f"Echoed {text}"[:300]}
            )
        if not self.path.rstrip("/").endswith("/chat/completions"):
            return self.reply(404, {"error": {"message": "not found"}})
        model = body.get("model", "")
        if "tools" in body and not isinstance(
            body["tools"], list
        ):  # as a real provider: tools is an array
            return self.reply(400, {"error": {"message": "tools must be an array"}})
        rec = {
            "model": model,
            "body": body,
            "authorization": self.headers.get("Authorization"),
            "t0": time.time(),
        }
        with lock:
            requests.append(rec)
            resp = scripts[model].pop(0) if scripts.get(model) else {}
        time.sleep(resp.get("delay_s", 0))
        if "status" in resp:
            rec |= {"t1": time.time(), "status": resp["status"]}
            return self.reply(resp["status"], resp.get("body", {"error": {"message": "scripted"}}))
        if not ({"content", "tool_calls"} & set(resp)):
            resp = from_prompt(body) | {k: v for k, v in resp.items() if k == "usage"}
        rec |= {"t1": time.time(), "status": 200}
        self.reply(200, completion(model, resp, body))


def cert() -> ssl.SSLContext:
    os.makedirs(CERT_DIR, exist_ok=True)
    crt, key = f"{CERT_DIR}/{NAME}.pem", f"{CERT_DIR}/{NAME}.key"
    if not os.path.exists(crt):
        subprocess.run(
            [
                "openssl",
                "req",
                "-x509",
                "-newkey",
                "rsa:2048",
                "-nodes",
                "-days",
                "30",
                "-keyout",
                key,
                "-out",
                crt,
                "-subj",
                f"/CN={NAME}",
                "-addext",
                f"subjectAltName=DNS:{NAME}",
            ],
            check=True,
            capture_output=True,
        )
    ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    ctx.load_cert_chain(crt, key)
    return ctx


def serve() -> None:
    plain = ThreadingHTTPServer(("0.0.0.0", PORT), Handler)
    tls = ThreadingHTTPServer(("0.0.0.0", TLS_PORT), Handler)
    tls.socket = cert().wrap_socket(tls.socket, server_side=True)
    threading.Thread(target=tls.serve_forever, daemon=True).start()
    print(
        f"fake openai on :{PORT} (http) and :{TLS_PORT} (https, {CERT_DIR}/{NAME}.pem)", flush=True
    )
    plain.serve_forever()


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
