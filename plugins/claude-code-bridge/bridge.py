"""Personal OpenAI-compatible bridge to the logged-in `claude -p` (subscription). Personal use only, never deploy.

POST /v1/chat/completions, GET /v1/models. Bearer token BRIDGE_TOKEN from $BRIDGE_ENV
(default ~/.helm/claude-code-bridge.env: outside plugins/, which the studio mounts). Stdlib only.
"""

import hmac
import json
import os
import subprocess
import sys
import threading
import time
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

HERE = Path(__file__).resolve().parent
ENV_FILE = Path(os.environ.get("BRIDGE_ENV") or "~/.helm/claude-code-bridge.env").expanduser()
ENV = dict(l.split("=", 1) for l in ENV_FILE.read_text().split() if "=" in l)
TOKEN = ENV["BRIDGE_TOKEN"]
HOST, PORT = ENV.get("BRIDGE_HOST", "127.0.0.1"), int(ENV.get("BRIDGE_PORT", "8420"))
TIMEOUT = int(ENV.get("BRIDGE_TIMEOUT", "180"))
MODELS = {"claude-sonnet": "sonnet", "claude-opus": "opus", "claude-haiku": "haiku"}
WORK = HERE / "work"  # empty cwd: no project CLAUDE.md to find
# every env var that would switch the CLI off the subscription login
DROP = ("ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_OAUTH_TOKEN",
        "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "AWS_BEARER_TOKEN_BEDROCK")
# quiet start (as Hermes' subscription plugin does): skips update, telemetry and other network checks, ~4.4 s -> ~1.4 s
QUIET = {"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "DISABLE_TELEMETRY": "1", "DISABLE_ERROR_REPORTING": "1",
         "DISABLE_AUTOUPDATER": "1", "DISABLE_FEEDBACK_COMMAND": "1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY": "1",
         "CLAUDE_CODE_DISABLE_CLAUDE_MDS": "1", "CLAUDE_CODE_DISABLE_TERMINAL_TITLE": "1"}
SLOTS = threading.BoundedSemaphore(int(ENV.get("BRIDGE_MAX_CONCURRENT", "4")))  # ~250 MB per claude process
LOCKDOWN = ["--safe-mode", "--tools", "", "--strict-mcp-config", "--mcp-config", '{"mcpServers":{}}',
            "--setting-sources", "", "--disable-slash-commands", "--no-session-persistence",
            "--permission-prompts", "none", "--output-format", "json"]


class Fail(Exception):
    def __init__(self, code, msg):
        super().__init__(msg)
        self.code = code


def text_of(content):
    if isinstance(content, list):
        return "\n".join(p.get("text", "") for p in content if isinstance(p, dict))
    return content or ""


def build(body):
    """OpenAI body -> (system, prompt, schema or None, tool names)."""
    system, turns = [], []
    for m in body.get("messages") or []:
        role, txt = m.get("role"), text_of(m.get("content"))
        if role in ("system", "developer"):
            system.append(txt)
        elif role == "tool":
            turns.append(f"[tool result {m.get('tool_call_id', '')}]\n{txt}")
        elif role == "assistant":
            calls = [f"[tool call {c.get('id')}] {c['function']['name']}({c['function'].get('arguments', '')})"
                     for c in m.get("tool_calls") or []]
            turns.append("[assistant]\n" + "\n".join([txt] + calls).strip())
        else:
            turns.append(f"[user]\n{txt}" if len(body["messages"]) > 2 else txt)
    schema, names = None, []
    tools = [t["function"] for t in body.get("tools") or [] if t.get("type") == "function"]
    if tools and body.get("tool_choice") != "none":
        names = [t["name"] for t in tools]
        system.append(
            "# How to use tools here\n"
            "The tools below are run by the caller, not by you: they are NOT in your own tool list, and calling one "
            "directly fails with \"No such tool available\". Never call them directly. To use one, put it in the "
            "`tool_calls` field of your JSON answer; the caller runs it and sends you the result in the next "
            "message. That is how every tool in this conversation works, and it always works.\n\n"
            "Tools (name, description, JSON schema of the arguments):\n" + json.dumps(tools, indent=1)
            + "\n\nAnswer as JSON: `text` is your reply to the user (may be empty when you call tools); "
            "`tool_calls` lists the tools to call now, each with its `name` and `arguments` object "
            "matching that tool's schema. Leave `tool_calls` empty when no tool is needed.")
        schema = {"type": "object", "required": ["text", "tool_calls"], "additionalProperties": False,
                  "properties": {"text": {"type": "string"}, "tool_calls": {"type": "array", "items": {
                      "type": "object", "required": ["name", "arguments"], "additionalProperties": False,
                      "properties": {"name": {"type": "string", "enum": names}, "arguments": {"type": "object"}}}}}}
    else:
        rf = body.get("response_format") or {}
        if rf.get("type") == "json_schema":
            schema = (rf.get("json_schema") or {}).get("schema") or {"type": "object"}
        elif rf.get("type") == "json_object":
            schema = {"type": "object"}
    # always a system prompt: without one the CLI uses its own long coding-agent prompt
    return "\n\n".join(system) or "You are a helpful assistant.", "\n\n".join(turns), schema, names


# claude -p --effort's levels; the studio's thinking rule (presets.json claude-code-bridge) sends low, medium or high
EFFORTS = ("low", "medium", "high", "xhigh", "max")


def effort(body):
    """The thinking level: OpenAI-style `reasoning_effort`, absent = the CLI's own default; anything else is 400."""
    level = body.get("reasoning_effort")
    if level is not None and level not in EFFORTS:
        raise Fail(400, f"reasoning_effort must be one of {', '.join(EFFORTS)}")
    return level


def run_claude(model, system, prompt, schema, level=None):
    cmd = ["claude", "-p", *LOCKDOWN, "--model", model]
    if level:
        cmd += ["--effort", level]
    cmd += ["--system-prompt", system]
    if schema is not None:
        cmd += ["--json-schema", json.dumps(schema)]
    env = {k: v for k, v in os.environ.items() if k not in DROP} | QUIET
    try:
        with SLOTS:  # over the cap: wait in arrival order (inside the request's own timeout)
            p = subprocess.run(cmd, input=prompt, capture_output=True, text=True, timeout=TIMEOUT, cwd=WORK, env=env)
    except subprocess.TimeoutExpired:
        raise Fail(504, f"claude did not answer in {TIMEOUT} s")
    try:
        out = json.loads(p.stdout)
    except ValueError:
        msg = (p.stderr or p.stdout).strip()[-500:] or f"claude exited {p.returncode}"
        raise Fail(503, msg)
    if out.get("is_error") or out.get("subtype") != "success":
        raise Fail(503, str(out.get("result") or out.get("subtype") or "claude failed")[:500])
    return out


def complete(body):
    model = body.get("model") or "claude-sonnet"
    level = effort(body)
    system, prompt, schema, names = build(body)
    if not prompt.strip():
        raise Fail(400, "no user message")
    t = time.monotonic()
    out = run_claude(MODELS.get(model, model), system, prompt, schema, level)
    u = out.get("usage") or {}
    tin = u.get("input_tokens", 0) + u.get("cache_read_input_tokens", 0) + u.get("cache_creation_input_tokens", 0)
    tout = u.get("output_tokens", 0)
    print(f"{time.strftime('%H:%M:%S')} {model} {time.monotonic() - t:.1f}s in={tin} out={tout} "
          f"cost_usd={out.get('total_cost_usd')}", flush=True)
    msg = {"role": "assistant", "content": out.get("result") or ""}
    finish = "stop"
    so = out.get("structured_output")
    if names:
        so = so or {}
        msg["content"] = so.get("text") or None
        calls = [{"id": "call_" + uuid.uuid4().hex[:24], "type": "function",
                  "function": {"name": c["name"], "arguments": json.dumps(c.get("arguments") or {})}}
                 for c in so.get("tool_calls") or [] if c.get("name") in names]
        if calls:
            msg["tool_calls"], finish = calls, "tool_calls"
    elif schema is not None:
        msg["content"] = json.dumps(so) if so is not None else msg["content"]
    return {"id": "chatcmpl-" + uuid.uuid4().hex, "object": "chat.completion", "created": int(time.time()),
            "model": model, "choices": [{"index": 0, "message": msg, "finish_reason": finish}],
            "usage": {"prompt_tokens": tin, "completion_tokens": tout, "total_tokens": tin + tout}}


class Handler(BaseHTTPRequestHandler):
    def send(self, code, obj):
        data = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def authed(self):
        got = self.headers.get("Authorization", "")
        if hmac.compare_digest(got.encode(), f"Bearer {TOKEN}".encode()):
            return True
        self.send(401, {"error": {"message": "bad or missing bearer token", "type": "auth"}, "detail": "unauthorized"})
        return False

    def do_GET(self):
        if self.path.rstrip("/") == "/health":  # the studio's Plugins status: no key, nothing about the plan
            return self.send(200, {"ok": True})
        if not self.authed():
            return
        if self.path.rstrip("/") == "/v1/models":
            return self.send(200, {"object": "list", "data": [
                {"id": m, "object": "model", "created": 0, "owned_by": "claude-cli"} for m in MODELS]})
        self.send(404, {"detail": "not found"})

    def do_POST(self):
        if not self.authed():
            return
        if self.path.rstrip("/") != "/v1/chat/completions":
            return self.send(404, {"detail": "not found"})
        try:
            body = json.loads(self.rfile.read(int(self.headers.get("Content-Length") or 0)) or b"{}")
            if body.get("stream"):
                raise Fail(400, "streaming is not supported")
            self.send(200, complete(body))
        except Fail as e:
            print(f"{time.strftime('%H:%M:%S')} error {e.code}: {e}", flush=True)
            self.send(e.code, {"error": {"message": str(e), "type": "bridge", "code": e.code}, "detail": str(e)})
        except (ValueError, KeyError, TypeError) as e:
            self.send(400, {"error": {"message": f"bad request: {e}", "type": "bridge"}, "detail": "bad request"})

    def log_message(self, fmt, *args):  # request lines only, never headers
        sys.stderr.write(f"{self.address_string()} {fmt % args}\n")


if __name__ == "__main__":
    WORK.mkdir(exist_ok=True)
    server = ThreadingHTTPServer((HOST, PORT), Handler)
    print(f"claude bridge on http://{HOST}:{PORT}/v1 (personal use only)", flush=True)
    server.serve_forever()
