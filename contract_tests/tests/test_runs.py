"""Runs of the echo workflow on the fake runner: success, failure, cancel, invalid inputs."""

import io

from helpers import (
    NO_SUCH_ID,
    QUICK,
    SLOW,
    STUDIO,
    new_session,
    ok,
    run_events,
    sse,
    start_run,
    statuses,
    wait_run,
)
from PIL import Image


def test_created_run(echo_done):
    run, sid = echo_done["run"], echo_done["session"]["id"]
    assert (run["session_id"], run["workflow_id"], run["inputs"], run["status"]) == (
        sid,
        "echo",
        QUICK,
        "queued",
    )
    assert (
        run["progress"],
        run["error"],
        run["outputs"],
        run["started_at"],
        run["finished_at"],
    ) == (None, None, [], None, None)
    assert run["host"] == "fake"


def test_events_in_order(echo_done):
    events = echo_done["events"]
    seqs = [e["seq"] for e in events]
    assert seqs == sorted(set(seqs)), seqs  # strictly increasing
    assert statuses(events) == ["queued", "running", "succeeded"]
    assert [e["type"] for e in events] == [
        "status",
        "status",
        "progress",
        "progress",
        "progress",
        "output",
        "status",
    ]
    progress = [e["data"] for e in events if e["type"] == "progress"]
    # phase and eta_s are optional extras a runner may add
    assert [{k: p[k] for k in ("node", "step", "of")} for p in progress] == [
        {"node": "echo", "step": n, "of": 3} for n in (1, 2, 3)
    ]
    running = events[1]["data"]
    assert (running["host"], running["error"]) == ("fake", None)


def test_output_png(echo_done, anon):
    run_id, owner = echo_done["run"]["id"], echo_done["owner_id"]
    (out,) = [e["data"] for e in echo_done["events"] if e["type"] == "output"]
    assert (out["index"], out["kind"], out["key"]) == (
        0,
        "image",
        f"u/{owner}/runs/{run_id}/1/0.png",
    )
    r = anon.get(out["url"])  # a signed link: no API key, no token
    assert r.status_code == 200 and r.headers["content-type"] == "image/png"
    assert (
        r.headers["content-security-policy"] == "sandbox"
        and r.headers["x-content-type-options"] == "nosniff"
    )
    img = Image.open(io.BytesIO(r.content))
    assert img.format == "PNG"
    assert (out["width"], out["height"]) in ((None, None), img.size)


def test_finished_run(api, echo_done):
    run_id, sid = echo_done["run"]["id"], echo_done["session"]["id"]
    run = ok(api.get(f"{STUDIO}/runs/{run_id}"))
    assert (run["status"], run["error"], run["host"]) == ("succeeded", None, "fake")
    assert {k: run["progress"][k] for k in ("node", "step", "of")} == {
        "node": "echo",
        "step": 3,
        "of": 3,
    }
    assert run["started_at"] and run["finished_at"]
    assert [(o["index"], o["key"], o["kind"]) for o in run["outputs"]] == [
        (0, f"u/{echo_done['owner_id']}/runs/{run_id}/1/0.png", "image")
    ]
    assert ok(api.get(f"{STUDIO}/sessions/{sid}/runs"))["runs"][0]["id"] == run_id
    assert api.post(f"{STUDIO}/runs/{run_id}/cancel").status_code == 409  # already ended


def test_defaults_fill_the_rest(api):
    sid = new_session(api)["id"]
    run = start_run(api, sid, {"echo.steps": 1, "echo.delay_ms": 0})
    events = run_events(api, sid, run["id"])
    assert statuses(events)[-1] == "succeeded"
    assert run["inputs"] == {"echo.steps": 1, "echo.delay_ms": 0}  # as requested, not resolved


def test_failing_run(api):
    sid = new_session(api)["id"]
    run = start_run(api, sid, QUICK | {"echo.fail": True})
    events = run_events(api, sid, run["id"])
    assert statuses(events) == ["queued", "running", "failed"]
    assert [e["data"]["step"] for e in events if e["type"] == "progress"] == [1, 2, 3]
    assert not [e for e in events if e["type"] == "output"]
    assert events[-1]["data"]["error"]
    done = ok(api.get(f"{STUDIO}/runs/{run['id']}"))
    assert (done["status"], done["outputs"], done["error"]) == (
        "failed",
        [],
        events[-1]["data"]["error"],
    )


def test_cancel_mid_run(api):
    sid = new_session(api)["id"]
    run = start_run(api, sid, SLOW)
    # it is running
    sse(api, sid, lambda fs: any(f["event"] == "progress" for f in fs), last_event_id=0)
    assert api.delete(f"{STUDIO}/sessions/{sid}").status_code == 409  # a run in progress
    cancelling = ok(api.post(f"{STUDIO}/runs/{run['id']}/cancel"), 202)
    assert cancelling["id"] == run["id"] and cancelling["status"] in (
        "queued",
        "running",
        "cancelled",
    )
    assert api.post(f"{STUDIO}/runs/{run['id']}/cancel").status_code in (202, 409)  # harmless again
    done = wait_run(api, run["id"])
    assert (done["status"], done["outputs"]) == ("cancelled", [])
    assert done["progress"]["step"] < 200
    events = run_events(api, sid, run["id"])
    assert statuses(events)[-1] == "cancelled"
    assert not [e for e in events if e["type"] == "output"]
    assert api.post(f"{STUDIO}/runs/{run['id']}/cancel").status_code == 409
    assert api.delete(f"{STUDIO}/sessions/{sid}").status_code == 204


def test_invalid_inputs(api):
    sid = new_session(api)["id"]
    url = f"{STUDIO}/sessions/{sid}/runs"
    bad = [
        ("echo.steps", 0),
        ("echo.steps", 201),
        ("echo.steps", 2.5),
        ("echo.steps", "ten"),
        ("echo.delay_ms", -1),
        ("echo.fail", "yes"),
        ("echo.nope", 1),
        ("nowhere.steps", 1),
        ("prompt.text", "x" * 4001),
        # null is only a seed's (random); echo has no seed param
        ("echo.steps", None),
        ("echo.fail", None),
        ("prompt.text", None),
        ("echo.steps", 1e300),  # no int beyond 2^53-1
    ]
    for key, value in bad:
        r = api.post(url, json={"workflow_id": "echo", "inputs": {key: value}})
        assert r.status_code == 422, f"{key}={value!r}: {r.status_code} {r.text}"
        assert key in r.json()["detail"], r.json()
    for body in (
        {"workflow_id": "echo", "inputs": {"steps": 3}},
        {"inputs": {}},
        {"workflow_id": "no-such-workflow"},
        {"workflow_id": "echo", "inputs": []},
    ):
        assert api.post(url, json=body).status_code == 422, body
    assert ok(api.get(f"{STUDIO}/sessions/{sid}/runs"))["runs"] == []  # nothing was created


def test_unknown_ids(api):
    assert (
        api.post(f"{STUDIO}/sessions/{NO_SUCH_ID}/runs", json={"workflow_id": "echo"}).status_code
        == 404
    )
    for rid in (NO_SUCH_ID, "not-a-uuid"):
        assert api.get(f"{STUDIO}/runs/{rid}").status_code == 404
        assert api.post(f"{STUDIO}/runs/{rid}/cancel").status_code == 404
