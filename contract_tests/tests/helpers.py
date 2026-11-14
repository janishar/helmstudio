"""Plain helpers (no fixtures): test images, polling, and the session steps most tests build on."""

import io
import os
import time
import uuid

import httpx
from PIL import Image

JOB_TIMEOUT = float(os.environ.get("JOB_TIMEOUT", "120"))
CATALOG = "/v1/catalog"

# Raw photos the fake sorter files by name: 7 rings (two logical groups; R-004..R-006 are lifestyle shots, so the
# guidelines conflict comes up), 4 necklaces (N-003 and N-004 share a name: the duplicate question) and 1 anklet.
RAW_NAMES = [f"ring-{n:02d}.png" for n in range(1, 8)] + [f"necklace-{n:02d}.png" for n in range(1, 5)] + [
    "anklet-01.png"
]
GUIDELINE = b"Brand guidelines\nPlain studio white backgrounds for every product.\nWarm, natural light.\n"


def image(color=(180, 40, 40), size=(48, 64), fmt="PNG") -> bytes:
    buf = io.BytesIO()
    Image.new("RGB", size, color).save(buf, fmt)
    return buf.getvalue()


def raw_files(names: list[str] = RAW_NAMES) -> list:
    """The multipart `files` parts of POST .../raw, one distinct PNG per name."""
    return [("files", (n, image((20 * i % 255, 90, 160)), "image/png")) for i, n in enumerate(names)]


def wait_for(fn, timeout: float = JOB_TIMEOUT, interval: float = 0.5, what: str = "condition"):
    """fn() until it returns something truthy; AssertionError after `timeout` seconds."""
    deadline = time.monotonic() + timeout
    while True:
        if value := fn():
            return value
        if time.monotonic() > deadline:
            raise AssertionError(f"timed out after {timeout}s waiting for {what}")
        time.sleep(interval)


def ok(r: httpx.Response, status: int = 200) -> dict:
    """The JSON body of a response that must have `status`."""
    assert r.status_code == status, f"{r.request.method} {r.request.url.path}: {r.status_code} {r.text}"
    return r.json()


def new_session(client: httpx.Client, name: str = "Contract drop") -> str:
    return ok(client.post(f"{CATALOG}/sessions", json={"project_name": name}), 201)["session"]["id"]


def detail(client: httpx.Client, sid: str) -> dict:
    return ok(client.get(f"{CATALOG}/sessions/{sid}"))


def wait_processed(client: httpx.Client, sid: str) -> dict:
    """Poll until the session's processing job finished; it must have succeeded."""
    d = wait_for(
        lambda: (d := detail(client, sid))["session"]["processing"]["status"] in ("succeeded", "failed") and d,
        what=f"processing of {sid}",
    )
    assert d["session"]["processing"]["status"] == "succeeded", d["session"]["processing"]
    return d


def processed(client: httpx.Client, names: list[str] = RAW_NAMES, guideline: bool = True,
              families: list[str] | None = None) -> dict:
    """A new session with the guideline txt and raw photos (sorted into `families` when given), processing finished:
    GET /sessions/{id}."""
    sid = new_session(client)
    if guideline:
        files = {"file": ("brand_guidelines.txt", GUIDELINE, "text/plain")}
        ok(client.post(f"{CATALOG}/sessions/{sid}/guidelines", files=files), 201)
    ok(client.post(f"{CATALOG}/sessions/{sid}/raw", files=raw_files(names),
                   data={"kind": "folder"} | ({"families": families} if families else {})), 202)
    return wait_processed(client, sid)


def answer_all(client: httpx.Client, d: dict, option: int = 0) -> None:
    for q in d["questions"]:
        if q["answer"] is None:
            ok(client.post(f"{CATALOG}/questions/{q['id']}/answer", json={"option": option}))


def started(client: httpx.Client, **kw) -> dict:
    """A processed session with every question answered and Start done: GET /sessions/{id}."""
    d = processed(client, **kw)
    answer_all(client, d)
    ok(client.post(f"{CATALOG}/sessions/{d['session']['id']}/start"))
    return detail(client, d["session"]["id"])


def wait_idle(client: httpx.Client, sid: str) -> dict:
    """Poll until no run of the session is queued or running: GET /sessions/{id}."""
    def idle():
        d = detail(client, sid)
        return all(r["status"] in ("succeeded", "failed") for r in d["runs"]) and d

    return wait_for(idle, what=f"runs of {sid}")


def run(client: httpx.Client, sid: str, **body) -> dict:
    """POST .../runs (202), then wait for it: GET /sessions/{id}."""
    ok(client.post(f"{CATALOG}/sessions/{sid}/runs", json=body), 202)
    return wait_idle(client, sid)


def live_images(d: dict) -> dict[tuple[str, str], dict]:
    """(sku_id, shot) → its newest revision."""
    out: dict[tuple[str, str], dict] = {}
    for img in d["images"]:
        key = (img["sku_id"], img["shot"])
        if key not in out or img["revision"] > out[key]["revision"]:
            out[key] = img
    return out


def new_id() -> str:
    return str(uuid.uuid4())
