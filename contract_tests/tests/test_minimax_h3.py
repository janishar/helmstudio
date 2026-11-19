"""MiniMax H3 video (API 1.21, contracts/README.md "How the pieces meet", the minimax-h3 bullet; the native runtime)
through Kong.

Without a GPU: when a live native runner publishes `minimax-h3-text-to-video`, its three templates and node schemas
are the canonical fixtures (contracts/fixtures/registry/minimax-h3.*.valid.json), the model is Mac only (runtime
native), and studio refuses a canvas side over the max and a length off the 5 + 17k grid (422, nothing queued);
otherwise these skip.

Opt-in (MINIMAX_H3=1, the Mac native runner joined to the stack BASE_URL points at, as test_minimax): the runner's
refusals (contracts/fixtures/video/minimax-h3.json: references with a first frame, a side off the 32 px grid, a canvas
over 768 x 1344), each failing its run before the h3 child starts, then one tiny text-to-video (256 x 256, 22 frames,
2 steps; under a minute warm). A released run may unload the torch and mlx runners' weights first (the hand-over)."""

import json
import os

import pytest
from conftest import headers, make_client, sign_in
from helpers import STUDIO, TERMINAL, new_session, png_bytes, ref, start_run, upload, wait_for
from test_qwen import events_of, get
from test_user_workflows import CONTRACTS

FAMILY = "minimax-h3"
T2V, I2V, REFS = (f"{FAMILY}-{w}" for w in ("text-to-video", "image-to-video", "references"))
REG = CONTRACTS / "fixtures" / "registry"
TIMEOUT = 20 * 60
gpu = pytest.mark.skipif(
    os.environ.get("MINIMAX_H3") != "1", reason="set MINIMAX_H3=1 with the mac native runner up"
)


@pytest.fixture(scope="module")
def h3_user(new_user) -> dict:
    return new_user("MiniMaxH3")


@pytest.fixture(scope="module")
def hapi(h3_user):
    """User MiniMaxH3; a 401 signs in again (a run can outlast the 15-minute access token)."""
    with make_client(headers(h3_user["access_token"])) as c:

        def renew(r):
            if r.status_code == 401 and "/auth/" not in r.request.url.path:
                fresh = sign_in(h3_user["email"], h3_user["password"])["access_token"]
                c.headers["Authorization"] = f"Bearer {fresh}"

        c.event_hooks["response"].append(renew)
        yield c


@pytest.fixture(scope="module")
def live(hapi) -> dict[str, dict]:
    """The three published minimax-h3 workflows by id, or skip."""
    r = hapi.get(f"{STUDIO}/workflows/{T2V}")
    if r.status_code == 404:
        pytest.skip(f"no live native runner publishes {T2V}")
    return {wid: get(hapi, f"{STUDIO}/workflows/{wid}") for wid in (T2V, I2V, REFS)}


@pytest.fixture(scope="module")
def sid(hapi, live) -> str:
    return new_session(hapi, "Contract minimax-h3")["id"]


def canonical(name: str) -> dict:
    return json.loads((REG / f"{FAMILY}.{name}.valid.json").read_text())


# ---------------------------------------------------------------- the registry (no GPU)


def test_templates_are_the_canonical_ones(live):
    want = canonical("workflows")["workflows"]
    assert [w["id"] for w in want] == [T2V, I2V, REFS]

    def shape(nodes: dict) -> dict:
        return {n: (v["kind"], v.get("in") or {}, v.get("params") or {}) for n, v in nodes.items()}

    nodes = {n["kind"]: n for n in canonical("nodes")["nodes"]}
    for w in want:
        got = live[w["id"]]
        assert (got["title"], got.get("action"), got["family"], got["env"], got["media"]) == (
            w["title"],
            w.get("action"),
            FAMILY,
            "native",
            "video",
        )
        assert shape(got["nodes"]) == shape(w["nodes"]), w["id"]
        assert got["studio"] == w["studio"], w["id"]
        for kind, schema in got["node_schemas"].items():
            for key, value in nodes[kind].items():  # what the fixture states is what is served
                assert schema.get(key) == value, (w["id"], kind, key)


def test_native_host_and_a_mac_only_model(hapi, live):
    hosts = get(hapi, f"{STUDIO}/hosts")["hosts"]
    assert [h for h in hosts if h["env"] == "native" and h["state"] in ("idle", "busy")], hosts
    models = [m for m in get(hapi, f"{STUDIO}/models")["models"] if m["id"] == FAMILY]
    if not models:
        pytest.skip(f"{FAMILY} is not in the model catalog")
    assert [r["runtime"] for r in models[0]["runtimes"]] == ["native"], models[0]["runtimes"]


def refused(client, sid: str, workflow_id: str, inputs: dict) -> str:
    r = client.post(
        f"{STUDIO}/sessions/{sid}/runs", json={"workflow_id": workflow_id, "inputs": inputs}
    )
    assert r.status_code == 422, (r.status_code, r.text)
    return r.json()["detail"]


def test_studio_refuses_a_side_over_the_max_and_a_length_off_the_grid(hapi, sid):
    assert "canvas.width" in refused(hapi, sid, T2V, {"canvas.width": 1376})
    detail = refused(hapi, sid, T2V, {"duration.frames": 130})
    assert "duration.frames" in detail and "frame grid 5 + 17k up to 362" in detail, detail


# ---------------------------------------------------------------- runs (MINIMAX_H3=1)


def finished(client, run: dict) -> dict:
    return wait_for(
        lambda: (r := get(client, f"{STUDIO}/runs/{run['id']}"))["status"] in TERMINAL and r,
        timeout=TIMEOUT,
        interval=2,
        what=f"{FAMILY} run {run['id']}",
    )


@gpu
@pytest.mark.parametrize(
    "case",
    [
        "references with a first frame",
        "a side off the 32 px grid",
        "1344 × 1344 is over the area",
    ],
)
def test_runner_refusals(hapi, anon, sid, case):
    """The run fails with h3_generate_error's text (contracts/fixtures/video/minimax-h3.json) before the child starts."""
    fixture = json.loads((CONTRACTS / "fixtures" / "video" / "minimax-h3.json").read_text())
    (c,) = [c for c in fixture["cases"] if c["description"] == case]
    node, refs, first, _, width, height = c["args"]
    assert node == "generate"
    inputs = {"prompt.text": "a red fox", "canvas.width": width, "canvas.height": height}
    workflow_id = T2V
    if refs or first:
        img = upload(hapi, anon, png_bytes(64, 64))["id"]
        workflow_id = I2V if first else REFS
        inputs |= {"generate.refs": [ref(img)] * refs} | (
            {"first.image": {"take_id": img}} if first else {}
        )
    done = finished(hapi, start_run(hapi, sid, inputs, workflow_id))
    assert (done["status"], done["outputs"]) == ("failed", []), done
    assert c["expect"] in done["error"], done


@gpu
def test_tiny_text_to_video(hapi, sid):
    inputs = {
        "prompt.text": "A red fox trots through fresh snow, soft wind",
        "seed.seed": 42,
        "canvas.width": 256,
        "canvas.height": 256,
        "duration.frames": 22,
        "generate.steps": 2,
    }
    run = start_run(hapi, sid, inputs, T2V)
    done = finished(hapi, run)
    assert done["status"] == "succeeded", done
    phases = {
        e["data"].get("phase") for e in events_of(hapi, sid, run["id"]) if e["type"] == "progress"
    }
    assert "sampling" in phases, phases
    (out,) = done["outputs"]
    take = get(hapi, f"{STUDIO}/takes/{out['take_id']}")
    assert (take["kind"], take["content_type"], take["width"], take["height"]) == (
        "video",
        "video/mp4",
        256,
        256,
    )
    v = take["video"]
    assert (v["frames"], v["fps"], v["has_audio"]) == (22, 24, True) and v["poster_url"], v
    assert take["made_with"]["workflow_id"] == T2V
