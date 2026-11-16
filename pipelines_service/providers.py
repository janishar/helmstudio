"""Calls to model providers (plan 16.4, contracts/README.md "Model sources"): a local server you run, or a remote
provider. Only this module makes them, through `ChatOpenAI(base_url=…)` from langchain-openai, and a client is
built only inside a call, after the checks (so with remote models off and no server named nothing here ever
opens a connection):

- **The host rule**, at every call after DNS resolution and again at every connection (the client connects only to
  the address it checked, so DNS rebinding is caught; redirects are never followed: a 3xx is an `answer`
  failure): a server's base_url must resolve only to loopback or private addresses (link-local, so cloud metadata,
  and everything else refused); a remote one must be https, needs the deployment's switch (config.REMOTE_MODELS)
  and must resolve only to public addresses (config.REMOTE_ALLOW_PRIVATE, the fake overlay's, lets it be private
  or loopback; never link-local).
- **Keys**: a remote provider's `key_secret` names a file in config.PROVIDER_KEYS_DIR, read at call time, never
  logged or echoed; a server has none.
- **Failures** are classed: `network` (connect, DNS, TLS, timeout, 5xx, 429; retried 1 s, 4 s, 16 s or
  Retry-After ≤ 60 s, counted apart from verdicts) or `answer` (another 4xx, a body that is not a chat completion:
  stage A's one re-ask). `refused` and `off` are final.

Also the `pipelines.providers.test` responder (studio's admin Test button)."""

import asyncio
import ipaddress
import json
import logging
import socket
import time
from urllib.parse import urlsplit

import httpcore2  # the SDK's own HTTP stack (openai depends on httpx2)
import httpx2
import openai
from langchain_core.messages import HumanMessage, SystemMessage
from langchain_openai import ChatOpenAI

import config
from messaging import schemas

log = logging.getLogger(__name__)

SUBJECT = "pipelines.providers.test"
DELAYS = [1.0, 4.0, 16.0]  # seconds before each network retry (tests set them short)
RETRY_AFTER_MAX = 60.0
ALLOWED = [ipaddress.ip_network(n) for n in
           ("127.0.0.0/8", "::1/128", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7")]  # fmt: skip


class CallError(Exception):
    def __init__(self, kind: str, message: str, retry_after: float | None = None) -> None:
        super().__init__(message)
        self.kind, self.retry_after = kind, retry_after  # network | answer | refused | off


def _addr(ip: str):
    a = ipaddress.ip_address(ip.split("%")[0])
    return getattr(a, "ipv4_mapped", None) or a


def allowed_ip(ip: str, kind: str = "server") -> bool:
    """A server: loopback or private, never link-local. A remote: public only (REMOTE_ALLOW_PRIVATE: private and
    loopback too), never link-local, unspecified or multicast."""
    a = _addr(ip)
    if a.is_link_local or a.is_unspecified or a.is_multicast:
        return False
    if kind != "remote":
        return any(a in n for n in ALLOWED)
    return a.is_global or (config.REMOTE_ALLOW_PRIVATE and (a.is_private or a.is_loopback))


async def resolve(name: str, kind: str, host: str, port: int) -> list[str]:
    """The host's addresses when every one passes the host rule (CallError network | refused)."""
    try:
        infos = await asyncio.get_running_loop().getaddrinfo(host, port, type=socket.SOCK_STREAM)
    except OSError as e:
        raise CallError("network", f"{name}: cannot resolve {host}: {e}") from None
    ips = list(dict.fromkeys(i[4][0] for i in infos))
    if not ips or not all(allowed_ip(ip, kind) for ip in ips):
        what = "a public" if kind == "remote" else "a loopback or private"
        raise CallError("refused", f"{name}: {host} is not {what} address")
    return ips


async def check_host(name: str, p: dict) -> None:
    """The host rule before a call (CallError refused | off | network); `_Pinned` checks it again at connect."""
    u = urlsplit(p["base_url"])
    if u.username or u.password or u.query or u.fragment:
        raise CallError("refused", f"{name}: the base URL may hold no user, query or fragment")
    if p["kind"] == "remote":
        if not config.REMOTE_MODELS:
            raise CallError("off", "remote models are off on this deployment")
        if u.scheme != "https":
            raise CallError("refused", f"{name}: a remote provider must use https")
    if u.scheme not in ("http", "https") or not u.hostname:
        raise CallError("refused", f"{name}: the base URL must be http or https")
    await resolve(name, p["kind"], u.hostname, u.port or (443 if u.scheme == "https" else 80))


class _Pinned(httpcore2.AsyncNetworkBackend):
    """Connects only to an address that passes the host rule, resolved here at connect time (TLS still checks the
    name: httpcore sends the request's host as SNI)."""

    def __init__(self, name: str, kind: str) -> None:
        self.name, self.kind, self.inner = name, kind, httpcore2.AnyIOBackend()

    async def connect_tcp(self, host, port, timeout=None, local_address=None, socket_options=None):
        ip = (await resolve(self.name, self.kind, host, port))[0]
        return await self.inner.connect_tcp(ip, port, timeout=timeout, local_address=local_address,
                                            socket_options=socket_options)  # fmt: skip

    async def connect_unix_socket(self, *a, **kw):
        raise CallError("refused", f"{self.name}: no unix sockets")

    async def sleep(self, seconds: float) -> None:
        await self.inner.sleep(seconds)


def api_key(name: str, p: dict) -> str:
    """The provider's key (a server has none). Never logged; an error names the file, never its content."""
    if p["kind"] != "remote" or not p.get("key_secret"):
        return "none"
    f = config.PROVIDER_KEYS_DIR / p["key_secret"]
    try:
        return f.read_text().strip()
    except OSError:
        raise CallError("answer", f"{name}: the key file {p['key_secret']} is missing") from None


async def _no_auth(request) -> None:
    request.headers.pop("Authorization", None)


def _http(name: str, p: dict):
    """The HTTP client of one call: no redirects followed, no proxy from the environment, connections only to
    addresses that pass the host rule (_Pinned). A provider without a key (a server) sends no Authorization at
    all: the SDK insists on some api_key, so the header it builds from the "none" placeholder is dropped here."""
    keyless = p["kind"] != "remote" or not p.get("key_secret")
    transport = httpx2.AsyncHTTPTransport()
    transport._pool._network_backend = _Pinned(
        name, p["kind"]
    )  # ponytail: httpx has no public hook for this

    async def no_redirect(response) -> None:
        if response.is_redirect:
            raise CallError(
                "answer", f"{name}: a redirect (HTTP {response.status_code}) is never followed"
            )

    hooks = {"request": [_no_auth] if keyless else [], "response": [no_redirect]}
    return openai.DefaultAsyncHttpxClient(
        transport=transport, follow_redirects=False, event_hooks=hooks
    )


def _client(name: str, p: dict, model: str, max_tokens: int, http) -> ChatOpenAI:
    # langchain-openai sends max_completion_tokens; local servers (Ollama, llama-server) read max_tokens
    limit = (
        {"max_tokens": max_tokens}
        if p["kind"] == "remote"
        else {"extra_body": {"max_tokens": max_tokens}}
    )
    return ChatOpenAI(
        base_url=p["base_url"],
        api_key=api_key(name, p),
        model=model,
        temperature=0,
        timeout=p["timeout_s"],
        max_retries=0,  # retries are ours: counted, classed and held by Pause and Stop
        http_async_client=http,
        **limit,
    )


def _classify(name: str, e: Exception) -> CallError:
    cause = e
    while cause is not None:  # the host rule refused at connect (wrapped by httpx and the SDK)
        if isinstance(cause, CallError):
            return cause
        cause = cause.__cause__ or cause.__context__
    if isinstance(e, (openai.APIConnectionError, asyncio.TimeoutError, TimeoutError)):
        return CallError("network", f"{name}: network error: {type(e).__name__}")
    if isinstance(e, openai.APIStatusError):
        code = e.status_code
        if code == 429 or code >= 500:
            after = None
            try:
                after = min(float(e.response.headers.get("retry-after")), RETRY_AFTER_MAX)
            except TypeError, ValueError:
                pass
            return CallError("network", f"{name}: network error: HTTP {code}", after)
        if code == 401:
            return CallError("answer", f"{name}: the key was refused")
        return CallError("answer", f"{name}: HTTP {code}")
    return CallError("answer", f"{name}: the answer is not a chat completion ({type(e).__name__})")


async def chat(name: str, p: dict, req: dict) -> dict:
    """One chat completion: req {model, system, prompt, max_tokens, schema? (JSON text), tools? (OpenAI tool
    definitions)} → {text, tool_calls [{id, name, arguments}], tokens_in, tokens_out, seconds}; CallError else."""
    await check_host(name, p)
    kw = {}
    if req.get(
        "tools"
    ):  # a list; an ask's `tools` param (JSON text) is parsed here for every caller
        tools = json.loads(req["tools"]) if isinstance(req["tools"], str) else req["tools"]
        kw = {"tools": tools, "tool_choice": "auto"}
    elif req.get("schema"):
        schema = json.loads(req["schema"])
        kw = {"response_format": {"type": "json_schema",
                                  "json_schema": {"name": "answer", "schema": schema, "strict": False}}}  # fmt: skip
    msgs = [SystemMessage(req["system"]), HumanMessage(req["prompt"])]
    t = time.monotonic()
    try:
        async with _http(name, p) as http:
            llm = _client(name, p, req["model"], req["max_tokens"], http)
            msg = await llm.bind(**kw).ainvoke(msgs)
    except Exception as e:  # noqa: BLE001 - every failure is classed (network or answer)
        raise _classify(name, e) from None
    # the calls as the model wrote them: parsed ones re-serialised, unparsable ones as sent (an invalid turn)
    raw = [(c["id"], c["name"], json.dumps(c["args"])) for c in msg.tool_calls]
    raw += [(c["id"], c["name"], c["args"] or "") for c in msg.invalid_tool_calls]
    calls = [{"id": str(i or n)[:200], "name": (name_ or "")[:64], "arguments": a[:16384]}
             for n, (i, name_, a) in enumerate(raw[:4])]  # fmt: skip
    text = msg.content if isinstance(msg.content, str) else json.dumps(msg.content)
    usage = msg.usage_metadata or {}
    if not usage.get("input_tokens") and not usage.get("output_tokens"):
        # no usage reported: counted as the prompt's estimate and the whole output allowance, so caps still hold
        usage = {
            "input_tokens": estimate(req["system"] + req["prompt"]),
            "output_tokens": req["max_tokens"],
        }
    out = {"text": text[:32768], "tool_calls": calls, "tokens_in": usage.get("input_tokens", 0),
           "tokens_out": usage.get("output_tokens", 0), "seconds": round(time.monotonic() - t, 3)}  # fmt: skip
    if len(raw) > 4:  # never cut silently: the turn is invalid (asked once more)
        out["error"] = f"the answer has {len(raw)} tool calls; at most 4 per turn"
    return out


def estimate(text: str) -> int:
    """A text's token bound for the caps: 4 characters a token, rounded up (as PROMPT_TOKENS)."""
    return -(-len(text) // 4)


class Held(Exception):
    """Pause came during a network retry: the call goes back to its queue (`failures` so far)."""

    def __init__(self, failures: int) -> None:
        super().__init__("held")
        self.failures = failures


async def chat_retried(name: str, p: dict, req: dict, hold=None) -> tuple[dict, int]:
    """chat() with a network failure retried (DELAYS, or Retry-After): (answer, network failures). The last
    CallError is raised with `.failures` set. `hold` (async, → bool) is asked before each retry: True raises Held."""
    failures = 0
    while True:
        try:
            return await chat(name, p, req), failures
        except CallError as e:
            if e.kind != "network" or failures >= len(DELAYS):
                e.failures = failures + (e.kind == "network")
                raise
            await asyncio.sleep(e.retry_after if e.retry_after is not None else DELAYS[failures])
            failures += 1
            if hold and await hold():
                raise Held(failures) from None


def answer_of(got: dict) -> dict:
    """A chat() result as a run's `answer` (defs answer): one try, native tool calls kept."""
    t = {"text": got["text"], "error": got.get("error")}
    if got["tool_calls"]:
        t["tool_calls"] = got["tool_calls"]
    return {"tries": [t], "seen": [], "tokens_in": got["tokens_in"], "tokens_out": got["tokens_out"],
            "seconds": got["seconds"]}  # fmt: skip


# --- pipelines.providers.test


async def test(req: dict) -> dict:
    """GET <base_url>/models, then with `model` one completion of at most 8 tokens."""
    name, p, t = req["name"], req["provider"], time.monotonic()
    secs = lambda: round(time.monotonic() - t, 3)
    try:
        await check_host(name, p)
        client = openai.AsyncOpenAI(base_url=p["base_url"], api_key=api_key(name, p), timeout=p["timeout_s"],
                                    max_retries=0, http_client=_http(name, p))  # fmt: skip
        try:
            models = [m.id[:128] async for m in client.models.list()][:64]
        except Exception as e:  # noqa: BLE001 - classed as a call's
            raise _classify(name, e) from None
        finally:
            await client.close()
        out = {"models": models, "seconds": 0}
        if req.get("model"):
            got = await chat(name, p, {"model": req["model"], "system": "Answer with OK.",
                                       "prompt": "Answer with OK.", "max_tokens": 8})  # fmt: skip
            out["answer"] = got["text"][:200]
        return out | {"seconds": secs()}
    except CallError as e:
        return {"error": str(e)[:2000], "code": e.kind, "seconds": secs()}
    except Exception:
        log.exception("%s %s failed", SUBJECT, name)
        return {"error": f"{name}: the test failed", "code": "internal", "seconds": secs()}


async def answer_test(body: bytes) -> dict:
    try:
        req = json.loads(body)
    except ValueError:
        req = None
    if not isinstance(req, dict) or schemas.providers_test_errors("request", req):
        return {"error": "not a pipelines.providers.test request", "code": "internal"}
    return await test(req)


async def serve(nc):
    """Subscribe the test responder; returns the subscription."""

    async def on(msg) -> None:
        await msg.respond(json.dumps(await answer_test(msg.data)).encode())

    return await nc.subscribe(SUBJECT, queue="pipelines", cb=on)
