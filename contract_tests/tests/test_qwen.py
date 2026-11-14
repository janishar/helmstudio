"""Opt-in (QWEN=1): the qwen-image workflow on the native Mac torch runner (host mac, env torch), through Kong.
Turbo at int8, 768x768: one run with a fixed seed, then one with a random seed (the model is warm by then), then
one multi-reference edit of an uploaded image (canvas fixed at 768x768). A cold
model load is most of the time, so each run may take up to 15 minutes.

The runner must have joined the stack BASE_URL points at (`make runners` joins the dev stack's NATS and S3 ports;
for the test stack, run it against NATS 4323 and S3 8434). QWEN_PROMPT changes the prompt."""

import io
import os

import pytest
from conftest import headers, make_client, sign_in
from helpers import STUDIO, TERMINAL, new_session, ok, ref, start_run, upload, wait_for
from PIL import Image

pytestmark = pytest.mark.skipif(
    os.environ.get("QWEN") != "1", reason="set QWEN=1 with the mac torch runner up"
)

TIMEOUT = 15 * 60
SEED = 1234
SIZE = 768
INPUTS = {
    "prompt.text": os.environ.get("QWEN_PROMPT", "a red fox sitting in fresh snow, photograph"),
    "model.variant": "turbo",
    "model.precision": "int8",
    "canvas.width": SIZE,
    "canvas.height": SIZE,
}
MAX_SEED = 2**53 - 1


@pytest.fixture(scope="module")
def qwen_user(new_user) -> dict:
    return new_user("Qwen")


@pytest.fixture(scope="module")
def qapi(qwen_user):
    """User Qwen; a 401 signs in again (a run can outlast the 15-minute access token)."""
    with make_client(headers(qwen_user["access_token"])) as c:

        def renew(r):
            if r.status_code == 401 and "/auth/" not in r.request.url.path:
                fresh = sign_in(qwen_user["email"], qwen_user["password"])["access_token"]
                c.headers["Authorization"] = f"Bearer {fresh}"

        c.event_hooks["response"].append(renew)
        yield c


def get(client, path: str) -> dict:
    r = client.get(path)
    if r.status_code == 401:  # renewed by the hook: once more
        r = client.get(path)
    return ok(r)


def events_of(client, sid: str, run_id: str) -> list[dict]:
    """The run's stored SessionEvents (the JSON form of the events route, paged)."""
    out, since = [], 0
    while page := get(client, f"{STUDIO}/sessions/{sid}/events?since={since}"):
        if not page["events"]:
            return [e for e in out if e["run_id"] == run_id]
        out += page["events"]
        since = page["last_seq"]


def run_qwen(client, sid: str, inputs: dict) -> tuple[dict, list[dict]]:
    """Start a qwen-image run and wait for its end: (the finished run, its events)."""
    run = start_run(client, sid, inputs, workflow_id="qwen-image")
    assert run["host"] == "mac", run
    done = wait_for(
        lambda: (r := get(client, f"{STUDIO}/runs/{run['id']}"))["status"] in TERMINAL and r,
        timeout=TIMEOUT,
        interval=2,
        what=f"qwen-image run {run['id']}",
    )
    assert done["status"] == "succeeded", done
    return done, events_of(client, sid, run["id"])


def check_run(done: dict, events: list[dict], anon) -> int:
    """Phases, one PNG of the requested size, and one seed reported everywhere; returns the seed."""
    progress = [e["data"] for e in events if e["type"] == "progress"]
    assert progress and all(p.get("phase") for p in progress), progress
    sampling = [p for p in progress if p["phase"] == "sampling"]
    assert sampling and all(p["node"] == "sample" for p in sampling), progress
    assert sampling[-1]["step"] == sampling[-1]["of"], sampling
    assert all(p.get("eta_s", 0) >= 0 for p in progress)

    seed = done["resolved"]["seed"]
    assert isinstance(seed, int) and 0 <= seed <= MAX_SEED, done
    (end,) = [
        e["data"] for e in events if e["type"] == "status" and e["data"]["status"] in TERMINAL
    ]
    assert end["resolved"]["seed"] == seed, end

    (out,) = done["outputs"]
    assert (out["kind"], out["seed"]) == ("image", seed), out
    assert (out["width"], out["height"]) in ((None, None), (SIZE, SIZE)), out
    r = anon.get(out["url"])
    assert r.status_code == 200 and r.headers["content-type"] == "image/png", r.status_code
    img = Image.open(io.BytesIO(r.content))
    assert (img.format, img.size) == ("PNG", (SIZE, SIZE))
    return seed


def test_qwen_image_is_published(qapi):
    w = get(qapi, f"{STUDIO}/workflows/qwen-image")
    assert (w["family"], w["env"]) == ("qwen-image", "torch")
    hosts = get(qapi, f"{STUDIO}/hosts")["hosts"]
    mac = [h for h in hosts if (h["host"], h["env"]) == ("mac", "torch")]
    assert len(mac) == 1 and mac[0]["state"] in ("idle", "busy"), hosts
    assert "memory" in mac[0], mac[0]  # the torch runner reports its budget


def test_qwen_turbo_fixed_seed(qapi, anon):
    sid = new_session(qapi, "Contract qwen")["id"]
    done, events = run_qwen(qapi, sid, INPUTS | {"seed.seed": SEED})
    assert check_run(done, events, anon) == SEED


def test_qwen_turbo_random_seed(qapi, anon):
    sid = new_session(qapi, "Contract qwen random")["id"]
    done, events = run_qwen(qapi, sid, INPUTS | {"seed.seed": None})
    check_run(done, events, anon)
    assert done["inputs"]["seed.seed"] is None  # as requested; the seed used is in resolved


def test_qwen_turbo_edit_with_reference(qapi, anon):
    """An uploaded reference as <image1>: an edit of the canvas size whose take lists the reference as its parent."""
    buf = io.BytesIO()
    Image.linear_gradient("L").resize((512, 384)).convert("RGB").save(buf, "PNG")
    reference = upload(qapi, anon, buf.getvalue(), "gradient.png")["id"]
    sid = new_session(qapi, "Contract qwen edit")["id"]
    inputs = INPUTS | {
        "prompt.text": "<image1> repainted as a watercolor of a red fox in snow",
        "seed.seed": SEED,
        "canvas.mode": "fixed",
        "encode.refs": [ref(reference)],
    }
    done, events = run_qwen(qapi, sid, inputs)
    assert any(e["data"].get("phase") == "encoding" for e in events if e["type"] == "progress")
    check_run(done, events, anon)
    take = get(qapi, f"{STUDIO}/takes/{done['outputs'][0]['take_id']}")
    assert take["parents"] == [reference]
    assert take["made_with"]["inputs"]["encode.refs"] == [ref(reference)]
