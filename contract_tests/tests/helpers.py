"""Plain helpers (no fixtures): responses, polling, sign-in, sessions, runs and the event stream."""

import io
import json
import os
import time

import httpx
import schemas
from PIL import Image

JOB_TIMEOUT = float(os.environ.get("JOB_TIMEOUT", "60"))
STUDIO = "/v1/studio"
TERMINAL = {"succeeded", "failed", "cancelled"}
# a quick echo run: three steps of 50 ms
QUICK = {"prompt.text": "a red fox", "echo.steps": 3, "echo.delay_ms": 50}
# long enough to cancel mid-run (200 steps of 300 ms)
SLOW = {"prompt.text": "a slow fox", "echo.steps": 200, "echo.delay_ms": 300}
NO_SUCH_ID = "00000000-0000-4000-8000-000000000000"


def ok(r: httpx.Response, status: int = 200) -> dict:
    """The JSON body of a response that must have `status` (None for an empty body)."""
    assert r.status_code == status, (
        f"{r.request.method} {r.request.url.path}: {r.status_code} {r.text}"
    )
    return r.json() if r.content else None


def wait_for(fn, timeout: float = JOB_TIMEOUT, interval: float = 0.3, what: str = "condition"):
    """fn() until it returns something truthy; AssertionError after `timeout` seconds."""
    deadline = time.monotonic() + timeout
    while True:
        if value := fn():
            return value
        if time.monotonic() > deadline:
            raise AssertionError(f"timed out after {timeout}s waiting for {what}")
        time.sleep(interval)


def limited(client: httpx.Client, path: str, body: dict) -> httpx.Response:
    """POST to a rate-limited route (/auth/signin, /auth/refresh), waiting out Kong's per-IP limit (20/min) once:
    the suite signs in often."""
    r = client.post(path, json=body)
    if r.status_code == 429:
        time.sleep(min(float(r.headers.get("Retry-After", "60")), 61))
        r = client.post(path, json=body)
    return r


def signin(client: httpx.Client, email: str, password: str) -> httpx.Response:
    """POST /auth/signin (rate-limited)."""
    return limited(client, "/auth/signin", {"email": email, "password": password})


def new_session(client: httpx.Client, title: str = "Contract session") -> dict:
    return ok(client.post(f"{STUDIO}/sessions", json={"title": title}), 201)


def start_run(client: httpx.Client, sid: str, inputs: dict, workflow_id: str = "echo") -> dict:
    """POST .../runs: 202 and still queued (a failed run here means no live fake runner)."""
    run = ok(
        client.post(
            f"{STUDIO}/sessions/{sid}/runs", json={"workflow_id": workflow_id, "inputs": inputs}
        ),
        202,
    )
    assert run["status"] == "queued", f"run not queued (is the fake runner up?): {run}"
    return run


def wait_run(client: httpx.Client, run_id: str) -> dict:
    """Poll GET /runs/{id} until the run ended."""
    return wait_for(
        lambda: (r := ok(client.get(f"{STUDIO}/runs/{run_id}")))["status"] in TERMINAL and r,
        what=f"run {run_id}",
    )


def sse(
    client: httpx.Client,
    sid: str,
    until,
    *,
    last_event_id=None,
    since=None,
    timeout: float = JOB_TIMEOUT,
    path: str | None = None,
    schema: str = "SessionEvent",
) -> list[dict]:
    """Read the session's SSE stream (or the one at `path`) until until(frames) is true. A frame is
    {"id": int, "event": str, "data": dict}; with CONTRACT_SCHEMAS=1 each data is checked against `schema`."""
    path = path or f"{STUDIO}/sessions/{sid}/events"
    headers = {"Accept": "text/event-stream"}
    if last_event_id is not None:
        headers["Last-Event-ID"] = str(last_event_id)
    params = {"since": since} if since is not None else None
    frames, frame = [], {}
    deadline = time.monotonic() + timeout
    with client.stream(
        "GET",
        path,
        headers=headers,
        params=params,
        timeout=httpx.Timeout(timeout),
    ) as r:
        assert r.status_code == 200, f"events {sid}: {r.status_code} {r.read()}"
        assert r.headers["content-type"].startswith("text/event-stream"), r.headers["content-type"]
        for line in r.iter_lines():
            if time.monotonic() > deadline:
                raise AssertionError(f"timed out after {timeout}s on the event stream of {sid}")
            if line.startswith(":"):
                continue  # keep-alive
            if line:
                field, _, value = line.partition(":")
                frame[field] = value.removeprefix(" ")
                continue
            if "data" in frame:
                data = json.loads(frame["data"])
                schemas.check_value(data, f"#/components/schemas/{schema}", "SSE event")
                frames.append({"id": int(frame["id"]), "event": frame.get("event"), "data": data})
                if until(frames):
                    return frames
            frame = {}
    raise AssertionError(f"the event stream of {sid} ended by itself")


def ended(run_id: str):
    """An until() for sse(): the run's terminal status arrived."""
    return lambda frames: any(
        f["data"]["run_id"] == run_id
        and f["event"] == "status"
        and f["data"]["data"]["status"] in TERMINAL
        for f in frames
    )


def run_events(client: httpx.Client, sid: str, run_id: str, since: int = 0) -> list[dict]:
    """The run's SessionEvents from the stream, through its terminal status."""
    frames = sse(client, sid, ended(run_id), last_event_id=since)
    return [f["data"] for f in frames if f["data"]["run_id"] == run_id]


def statuses(events: list[dict]) -> list[str]:
    return [e["data"]["status"] for e in events if e["type"] == "status"]


def take_of_run(client: httpx.Client, run_id: str) -> dict:
    """The take of a finished run's only output."""
    (out,) = ok(client.get(f"{STUDIO}/runs/{run_id}"))["outputs"]
    return ok(client.get(f"{STUDIO}/takes/{out['take_id']}"))


def all_takes(client: httpx.Client, path: str = f"{STUDIO}/takes", **params) -> list[dict]:
    """Every take of a list route, following next_cursor."""
    takes, cursor = [], None
    while True:
        page = ok(client.get(path, params=params | ({"cursor": cursor} if cursor else {})))
        takes += page["takes"]
        if not (cursor := page["next_cursor"]):
            return takes


def png_bytes(width: int, height: int) -> bytes:
    buf = io.BytesIO()
    Image.new("RGB", (width, height), (200, 40, 40)).save(buf, "PNG")
    return buf.getvalue()


def upload(
    client: httpx.Client,
    anon: httpx.Client,
    data: bytes,
    filename: str = "ref.png",
    complete: bool = True,
) -> dict:
    """An uploaded PNG: the completed take, or (complete=False) the pending upload's POST /uploads answer."""
    body = {"filename": filename, "content_type": "image/png", "bytes": len(data)}
    up = ok(client.post(f"{STUDIO}/uploads", json=body), 201)
    if not complete:
        return up
    r = anon.put(up["upload_url"], content=data, headers={"Content-Type": "image/png"})
    assert r.status_code == 204, f"PUT upload: {r.status_code} {r.text}"
    return ok(client.post(f"{STUDIO}/uploads/{up['take_id']}/complete"))


def ref(take_id: str) -> dict:
    """One references row item: {"image": {"take_id": …}}."""
    return {"image": {"take_id": take_id}}
