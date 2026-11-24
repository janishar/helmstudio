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
- **Keys**: a provider's `key_secret` names a file in config.PROVIDER_KEYS_DIRS (the first folder that has it), read
  at call time, never logged or echoed; a server usually has none (API 1.51: it may name one, sent the same way).
  `key_found` (the pipelines.providers.key responder) only stats the file. API 1.52 (plan 27): the admin pastes a key
  in Admin › Model providers; the pipelines.providers.key.write responder checks it on /models, then `write_key`
  writes it into the first key folder only (temp file and rename, 0600); `remove_key` deletes it there.
- **compat** (API 1.51, from a preset): `temperature: false` sends no temperature; `strict_schema: true` sends a schema
  with `strict: true` and never retries it as `json_object`; `headers` (API 1.55, schema allow-listed: anthropic-version)
  go on every call (chat, Test, Fetch, the key check) through `_http`.
- **thinking** (API 1.53, plan 28): the snapshot's `thinking[model]` {level, body, tokens}, resolved by studio from
  the preset's rules: `body` is merged into the call's body, `tokens` added to max_tokens (thinking spends output
  tokens: a 24-token title still has room). A 400 on a call that carried a body is asked once more without it; when that answers,
  the (provider, model) is remembered in this process (`_NO_THINKING`); the tokens stay.
- **Failures** are classed: `network` (connect, DNS, TLS, timeout, 5xx, 429; retried 1 s, 4 s, 16 s or
  Retry-After ≤ 60 s, counted apart from verdicts) or `answer` (another 4xx, a body that is not a chat completion:
  stage A's one re-ask). `refused` and `off` are final.

Also the `pipelines.providers.test` responder (studio's admin Test button)."""

import asyncio
import ipaddress
import json
import logging
import os
import re
import socket
import tempfile
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
KEY_SUBJECT = "pipelines.providers.key"  # API 1.51: is a key file there (stat only)
WRITE_SUBJECT = "pipelines.providers.key.write"  # API 1.52: save or remove a key (core request/reply, never a stream)
KEY_NAME = re.compile(r"[a-z0-9][a-z0-9_.-]{0,63}")  # defs key_secret: no "/", no leading "."
KEY_CHARS = re.compile(r"[!-~]+")  # a key is printable ASCII without spaces (0x21-0x7E)
# our own fixed text for a key with any other character (a "…" from a cut copy, a curly quote, a non-breaking or
# zero-width space): the HTTP client cannot send it, so it is never "could not connect"; never the key or the character
KEY_CHARS_TEXT = "the key has a character keys never have (like … or a curly quote): copy it again from the provider"
CHECK_TIMEOUT = 15.0
MODELS_MAX = 1000  # GET /models ids kept (API 1.51; 64 before)
DELAYS = [1.0, 4.0, 16.0]  # seconds before each network retry (tests set them short)
RETRY_AFTER_MAX = 60.0
OUTSIDE = (
    "remote",
    "search",
)  # kinds that leave the machine (API 1.44: search, the assistant's web search)
DOCKER_HOST = (
    "host.docker.internal"  # its addresses here are the Docker host's: a server may live there
)
ALLOWED = [ipaddress.ip_network(n) for n in
           ("127.0.0.0/8", "::1/128", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7")]  # fmt: skip


class CallError(Exception):
    def __init__(self, kind: str, message: str, retry_after: float | None = None) -> None:
        super().__init__(message)
        self.kind, self.retry_after = kind, retry_after  # network | answer | refused | off


def _addr(ip: str):
    a = ipaddress.ip_address(ip.split("%")[0])
    return getattr(a, "ipv4_mapped", None) or a


def allowed_ip(ip: str, kind: str = "server", docker_host: frozenset = frozenset()) -> bool:
    """A server: loopback or private, or one of docker_host (what host.docker.internal resolves to here), never
    link-local. A remote: public only (REMOTE_ALLOW_PRIVATE: private and loopback too), never link-local, unspecified
    or multicast."""
    a = _addr(ip)
    if a.is_link_local or a.is_unspecified or a.is_multicast:
        return False
    if kind not in OUTSIDE:
        return any(a in n for n in ALLOWED) or a in docker_host
    return a.is_global or (config.REMOTE_ALLOW_PRIVATE and (a.is_private or a.is_loopback))


async def _lookup(host: str, port: int | None) -> list[str]:
    infos = await asyncio.get_running_loop().getaddrinfo(host, port, type=socket.SOCK_STREAM)
    return list(dict.fromkeys(i[4][0] for i in infos))


async def docker_host() -> frozenset:
    """The addresses host.docker.internal resolves to in this container now (empty when it does not): the Docker host
    on any engine. OrbStack answers 0.250.250.254, neither loopback nor private; Docker Desktop and Linux host-gateway
    answer a private address."""
    try:
        return frozenset(_addr(ip) for ip in await _lookup(DOCKER_HOST, None))
    except OSError:
        return frozenset()


async def resolve(name: str, kind: str, host: str, port: int) -> list[str]:
    """The host's addresses when every one passes the host rule (CallError network | refused)."""
    try:
        ips = await _lookup(host, port)
    except OSError as e:
        raise CallError("network", f"{name}: cannot resolve {host}: {e}") from None
    docker = frozenset()
    if kind not in OUTSIDE and not all(allowed_ip(ip, kind) for ip in ips):
        docker = await docker_host()  # looked up only when needed
    if not ips or not all(allowed_ip(ip, kind, docker) for ip in ips):
        what = "a public" if kind in OUTSIDE else "a loopback, private or Docker host"
        raise CallError("refused", f"{name}: {host} is not {what} address")
    return ips


async def check_host(name: str, p: dict) -> None:
    """The host rule before a call (CallError refused | off | network); `_Pinned` checks it again at connect."""
    u = urlsplit(p["base_url"])
    if u.username or u.password or u.query or u.fragment:
        raise CallError("refused", f"{name}: the base URL may hold no user, query or fragment")
    if p["kind"] in OUTSIDE:
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


def key_path(secret: str):
    """The key file of that name in the first key folder that has a regular file of it, else None (stat only)."""
    return next((d / secret for d in config.PROVIDER_KEYS_DIRS if (d / secret).is_file()), None)


def key_found(secret: str) -> bool:
    """A regular file of that name, not empty, in a key folder: stat only, the file is never opened."""
    f = key_path(secret)
    return f is not None and f.stat().st_size > 0


def api_key(name: str, p: dict) -> str:
    """The provider's key ("none" without key_secret: a server usually). Never logged; an error names the file, never
    its content."""
    if not p.get("key_secret"):
        return "none"
    f = key_path(p["key_secret"])
    try:
        return f.read_text().strip()
    except OSError, AttributeError:
        raise CallError("answer", f"{name}: the key file {p['key_secret']} is missing") from None


async def _no_auth(request) -> None:
    request.headers.pop("Authorization", None)


def _http(name: str, p: dict):
    """The HTTP client of one call: no redirects followed, no proxy from the environment, connections only to
    addresses that pass the host rule (_Pinned). A provider without a key (a server) sends no Authorization at
    all: the SDK insists on some api_key, so the header it builds from the "none" placeholder is dropped here."""
    keyless = not p.get("key_secret")
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
    headers = (p.get("compat") or {}).get("headers") or {}  # API 1.55: allow-listed by the schema
    return openai.DefaultAsyncHttpxClient(
        transport=transport, follow_redirects=False, event_hooks=hooks, headers=headers
    )


def _client(
    name: str, p: dict, model: str, max_tokens: int, http, body: dict | None = None
) -> ChatOpenAI:
    """body: the thinking fragment (API 1.53), merged into the request body as is."""
    # langchain-openai sends max_completion_tokens; local servers (Ollama, llama-server) read max_tokens
    limit = (
        {"max_tokens": max_tokens}
        if p["kind"] == "remote"
        else {"extra_body": {"max_tokens": max_tokens}}
    )
    if body:
        limit["extra_body"] = limit.get("extra_body", {}) | body
    return ChatOpenAI(
        base_url=p["base_url"],
        api_key=api_key(name, p),
        model=model,
        temperature=None if (p.get("compat") or {}).get("temperature") is False else 0,
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
    # a header (the key) the client cannot encode: local, before any request, never "could not connect"
    if isinstance(e, UnicodeError):
        return CallError("answer", f"{name}: {KEY_CHARS_TEXT}")
    if isinstance(e, (asyncio.TimeoutError, TimeoutError, openai.APITimeoutError)):
        return CallError("network", f"{name}: timed out")
    if isinstance(e, openai.APIConnectionError):
        return CallError("network", f"{name}: could not connect")
    if isinstance(e, openai.APIStatusError):
        code = e.status_code
        if code == 429 or code >= 500:
            after = None
            try:
                after = min(float(e.response.headers.get("retry-after")), RETRY_AFTER_MAX)
            except TypeError, ValueError:
                pass
            return CallError(
                "network", f"{name}: the provider is busy or down (HTTP {code})", after
            )
        if code == 401:
            return CallError("answer", f"{name}: the key was refused")
        return CallError("answer", f"{name}: HTTP {code}")
    return CallError("answer", f"{name}: the answer is not a chat completion ({type(e).__name__})")


async def chat(name: str, p: dict, req: dict) -> dict:
    """One chat completion: req {model, system, prompt, max_tokens, schema? (JSON text), tools? (OpenAI tool
    definitions)} → {text, tool_calls [{id, name, arguments}], tokens_in, tokens_out, seconds}; CallError else."""
    await check_host(name, p)
    strict = (p.get("compat") or {}).get(
        "strict_schema"
    ) is True  # API 1.51: never retried as json_object
    kw = {}
    if req.get(
        "tools"
    ):  # a list; an ask's `tools` param (JSON text) is parsed here for every caller
        tools = json.loads(req["tools"]) if isinstance(req["tools"], str) else req["tools"]
        kw = {"tools": tools, "tool_choice": "auto"}
    elif req.get("schema"):
        schema = json.loads(req["schema"])
        kw = {"response_format": {"type": "json_schema",
                                  "json_schema": {"name": "answer", "schema": schema, "strict": strict}}}  # fmt: skip
    msgs = [SystemMessage(req["system"]), HumanMessage(req["prompt"])]
    if "response_format" in kw and not strict and name in _NO_JSON_SCHEMA:
        kw, msgs = _json_object(req, msgs)
    think = (p.get("thinking") or {}).get(req["model"]) or {}
    limit = req["max_tokens"] + think.get("tokens", 0)
    body = {} if (name, req["model"]) in _NO_THINKING else think.get("body") or {}
    t = time.monotonic()
    try:
        async with _http(name, p) as http:
            dropped = False
            while True:
                llm = _client(name, p, req["model"], limit, http, body)
                try:
                    msg = await llm.bind(**kw).ainvoke(msgs)
                    if dropped:  # remembered only once the call without it answered (another 400 is not its fault)
                        _NO_THINKING.add((name, req["model"]))
                        log.warning(
                            "%s %s: thinking setting refused, sent without it", name, req["model"]
                        )
                    break
                except openai.BadRequestError:
                    # some OpenAI-compatible providers (DeepSeek) refuse response_format json_schema but take
                    # json_object: ask again once with the schema in the system prompt; the caller still validates
                    if not strict and kw.get("response_format", {}).get("type") == "json_schema":
                        _NO_JSON_SCHEMA.add(name)
                        kw, msgs = _json_object(req, msgs)
                    elif body:  # API 1.53: the thinking setting refused? once more without it
                        body, dropped = {}, True
                    else:
                        raise
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
            "output_tokens": limit,
        }
    out = {"text": text[:32768], "tool_calls": calls, "tokens_in": usage.get("input_tokens", 0),
           "tokens_out": usage.get("output_tokens", 0), "seconds": round(time.monotonic() - t, 3)}  # fmt: skip
    if len(raw) > 4:  # never cut silently: the turn is invalid (asked once more)
        out["error"] = f"the answer has {len(raw)} tool calls; at most 4 per turn"
    return out


_NO_JSON_SCHEMA: set[str] = (
    set()
)  # providers that refused response_format json_schema (this process)
_NO_THINKING: set[tuple[str, str]] = (
    set()
)  # (provider, model) that refused its thinking fragment (this process)


def _json_object(req: dict, msgs: list) -> tuple[dict, list]:
    """JSON mode without a schema: the schema goes into the system prompt instead."""
    system = f"{req['system']}\n\nAnswer with one JSON object that matches this JSON schema:\n{req['schema']}"
    return {"response_format": {"type": "json_object"}}, [SystemMessage(system), msgs[1]]


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


SEARCH_RESULTS, SEARCH_TEXT = 5, 500


async def search(name: str, p: dict, query: str) -> list[dict]:
    """The assistant's web search (API 1.44, kind search, api brave): GET <base_url>/web/search through the host
    rule, the same pinned client and failure classes as a chat. At most SEARCH_RESULTS results [{title, url,
    text}] cut to the contract's lengths; CallError else."""
    await check_host(name, p)
    headers = {"Accept": "application/json", "X-Subscription-Token": api_key(name, p)}
    try:
        async with _http(name, p) as http:
            r = await http.get(f"{p['base_url'].rstrip('/')}/web/search", headers=headers,
                               params={"q": query, "count": SEARCH_RESULTS}, timeout=p["timeout_s"])  # fmt: skip
            if r.status_code == 429 or r.status_code >= 500:
                raise CallError(
                    "network", f"{name}: the provider is busy or down (HTTP {r.status_code})"
                )
            if r.status_code >= 400:
                raise CallError("answer", f"{name}: HTTP {r.status_code}")
            found = ((r.json().get("web") or {}).get("results")) or []
    except CallError:
        raise
    except Exception as e:  # noqa: BLE001 - every failure is classed
        cause = e
        while cause is not None:  # the host rule refused at connect (wrapped by httpx)
            if isinstance(cause, CallError):
                raise cause from None
            cause = cause.__cause__ or cause.__context__
        if isinstance(e, (httpx2.TimeoutException, TimeoutError)):
            raise CallError("network", f"{name}: timed out") from None
        if isinstance(e, httpx2.TransportError):
            raise CallError("network", f"{name}: could not connect") from None
        raise CallError(
            "answer", f"{name}: the answer is not a search result ({type(e).__name__})"
        ) from None
    return [{"title": str(x.get("title") or "")[:200], "url": str(x.get("url") or "")[:500],
             "text": str(x.get("description") or "")[:SEARCH_TEXT]}
            for x in found[:SEARCH_RESULTS] if isinstance(x, dict)]  # fmt: skip


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
            models, reasoning = [], {}
            async for (
                m
            ) in client.models.list():  # ponytail: pages past MODELS_MAX are still read; cut here
                if len(models) < MODELS_MAX:
                    models.append(m.id[:128])
                    if r := reasoning_of(m.model_extra or {}):
                        reasoning[m.id[:128]] = r
        except Exception as e:  # noqa: BLE001 - classed as a call's
            raise _classify(name, e) from None
        finally:
            await client.close()
        out = {"models": models, "seconds": 0} | ({"reasoning": reasoning} if reasoning else {})
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


_EFFORT = re.compile(r"[a-z]{1,16}")


def reasoning_of(extra: dict) -> dict | None:
    """API 1.53: a /models entry's `reasoning` object (OpenRouter's) as {efforts, mandatory}; only supported_efforts
    and mandatory are read, anything else (and any other shape) is ignored."""
    r = extra.get("reasoning")
    if not isinstance(r, dict) or not isinstance(r.get("supported_efforts"), list):
        return None
    efforts = [e for e in r["supported_efforts"] if isinstance(e, str) and _EFFORT.fullmatch(e)][:8]
    return {"efforts": efforts, "mandatory": r.get("mandatory") is True}


async def answer_test(body: bytes) -> dict:
    try:
        req = json.loads(body)
    except ValueError:
        req = None
    if not isinstance(req, dict) or schemas.providers_test_errors("request", req):
        return {"error": "not a pipelines.providers.test request", "code": "internal"}
    return await test(req)


# --- pipelines.providers.key (API 1.51)


def answer_key(body: bytes) -> dict:
    """{found, writable} for a key_secret: stat only, never the file's content."""
    try:
        req = json.loads(body)
    except ValueError:
        req = None
    if not isinstance(req, dict) or schemas.providers_key_errors("request", req):
        return {"error": "not a pipelines.providers.key request", "code": "internal"}
    try:
        return {"found": key_found(req["key_secret"]), "writable": writable()}
    except OSError:
        return {"found": False, "writable": writable()}


# --- pipelines.providers.key.write (API 1.52, plan 27)


class KeyFileError(Exception):
    """A write that cannot happen: code readonly | not_file (our own text, never a key)."""

    def __init__(self, code: str, message: str) -> None:
        super().__init__(message)
        self.code = code


def writable() -> bool:
    """The first key folder is a writable directory: the only place a key is ever written."""
    d = config.PROVIDER_KEYS_DIRS[0] if config.PROVIDER_KEYS_DIRS else None
    return d is not None and d.is_dir() and os.access(d, os.W_OK | os.X_OK)


def _target(name: str):
    if not KEY_NAME.fullmatch(name):
        raise ValueError(
            "not a key file name"
        )  # the schema refuses it first; never a path out of the folder
    if not writable():
        raise KeyFileError("readonly", "keys are set in files on this deployment")
    f = config.PROVIDER_KEYS_DIRS[0] / name
    if f.is_dir() and not f.is_symlink():
        raise KeyFileError("not_file", f"deploy/secrets/providers/{name} is a folder, not a file")
    return f


def write_key(name: str, key: str) -> None:
    """Write the key into the first key folder: a temp file (mkstemp: 0600, a leading "." no key name has), fsync,
    rename over the name (a symlink of that name is replaced itself, never its target), fsync the folder. The temp
    file never stays behind."""
    f = _target(name)
    fd, tmp = tempfile.mkstemp(dir=f.parent, prefix=".tmp-")
    try:
        with os.fdopen(fd, "w") as out:
            out.write(key)
            out.flush()
            os.fsync(out.fileno())
        os.replace(tmp, f)
    except BaseException:
        try:
            os.unlink(tmp)
        except OSError:
            pass
        raise
    d = os.open(f.parent, os.O_RDONLY)
    try:
        os.fsync(d)
    finally:
        os.close(d)


def remove_key(name: str) -> None:
    """Delete the first key folder's file of that name (a regular file or a symlink; missing is fine)."""
    f = _target(name)
    try:
        os.unlink(f)
    except FileNotFoundError:
        pass


async def check_key(name: str, p: dict, key: str) -> tuple[str, str]:
    """GET <base_url>/models with the key in memory, under the host rule (pinned, no redirects, no proxy), 15 s:
    ("ok", ""), ("refused", text) for 401 or 403 or a key with a character outside 0x21-0x7E (no request), else
    ("unreachable", our own short reason)."""
    p = p | {
        "key_secret": name,
        "timeout_s": CHECK_TIMEOUT,
    }  # a key: the Authorization header is sent
    if not KEY_CHARS.fullmatch(key):
        return "refused", KEY_CHARS_TEXT
    try:
        # the whole check, the host rule's lookup too, within 15 s: studio waits 30 s for the answer
        async with asyncio.timeout(CHECK_TIMEOUT):
            await check_host(name, p)
            client = openai.AsyncOpenAI(base_url=p["base_url"], api_key=key, timeout=CHECK_TIMEOUT, max_retries=0,
                                        http_client=_http(name, p))  # fmt: skip
            try:
                await client.models.list()
            finally:
                await client.close()
    except openai.APIStatusError as e:
        if e.status_code in (401, 403):
            return "refused", "the provider refused this key; not saved"
        return "unreachable", f"HTTP {e.status_code}"
    except openai.APITimeoutError, TimeoutError:
        return "unreachable", "timeout"
    except Exception as e:  # noqa: BLE001 - classed; never str() of an SDK error (it may hold the provider's body)
        c = e if isinstance(e, CallError) else _classify(name, e)
        return "unreachable", str(c).removeprefix(f"{name}: ")[:300]
    return "ok", ""


async def answer_write(body: bytes) -> dict:
    """save: check (unless force), then write; remove: delete. Never the key in a reply or a log line."""
    try:
        req = json.loads(body)
    except ValueError:
        req = None
    if not isinstance(req, dict) or schemas.providers_key_write_errors("request", req):
        return {"error": "not a pipelines.providers.key.write request", "code": "internal"}
    name = req["key_secret"]
    try:
        if req["op"] == "remove":
            remove_key(name)
            log.info("key file %s removed", name)
            return {"found": key_found(name), "writable": writable()}
        key = req["key"].strip()
        _target(name)  # readonly or not_file before a call
        check, detail = (
            ("skipped", "") if req.get("force") else await check_key(name, req["provider"], key)
        )
        if check in ("ok", "skipped"):
            write_key(name, key)
            log.info("key file %s written (check %s)", name, check)
        out = {
            "found": key_found(name),
            "writable": writable(),
            "saved": check in ("ok", "skipped"),
            "check": check,
        }
        return out | ({"detail": detail} if detail else {})
    except KeyFileError as e:
        return {"error": str(e), "code": e.code}
    except Exception as e:  # noqa: BLE001 - the class name only: an error's text may hold anything
        log.error("key file %s: %s failed (%s)", name, req["op"], type(e).__name__)
        return {"error": f"{name}: the key file could not be changed", "code": "internal"}


def make_first_folder() -> None:
    """The first key folder is made at start when missing (0700); a read-only file system leaves it so."""
    if config.PROVIDER_KEYS_DIRS:
        try:
            config.PROVIDER_KEYS_DIRS[0].mkdir(mode=0o700, exist_ok=True)
        except OSError:
            pass


async def serve(nc):
    """Subscribe the test, key and (API 1.52) key write responders; returns the subscriptions."""
    make_first_folder()

    async def on(msg) -> None:
        await msg.respond(json.dumps(await answer_test(msg.data)).encode())

    async def on_key(msg) -> None:
        await msg.respond(json.dumps(answer_key(msg.data)).encode())

    async def on_write(msg) -> None:
        await msg.respond(json.dumps(await answer_write(msg.data)).encode())

    return [await nc.subscribe(SUBJECT, queue="pipelines", cb=on),
            await nc.subscribe(KEY_SUBJECT, queue="pipelines", cb=on_key),
            await nc.subscribe(WRITE_SUBJECT, queue="pipelines", cb=on_write)]  # fmt: skip
