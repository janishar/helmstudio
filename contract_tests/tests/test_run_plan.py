"""API 1.25: POST /v1/studio/runs/plan (a run's planned peak before it is sent: the Generate button's "≈ 40 s · 21 GB")
and the Studio block's section help and preset order (StudioLayout.inspector[].help, preset_order)."""

import json
from pathlib import Path

from helpers import NO_SUCH_ID, QUICK, STUDIO, new_session, ok, start_run, wait_run

PLAN = f"{STUDIO}/runs/plan"
WF = f"{STUDIO}/workflows"
FIXTURES = Path(__file__).parents[2] / "contracts" / "fixtures" / "http"


def queued(client) -> int:
    return len(ok(client.get(f"{STUDIO}/queue"))["runs"])


def test_plan_names_the_host_and_stores_nothing(api):
    """echo loads no model, so it has no memory plan: the fake host, no peak; nothing is queued."""
    before = queued(api)
    plan = ok(api.post(PLAN, json={"workflow_id": "echo", "inputs": QUICK}))
    assert plan == {"host": "fake", "peak_gb": None, "reason": None}
    assert queued(api) == before


def test_run_set_to_a_host(api):
    """API 1.35 (Run on): the plan and the run on that host only; a host that is not connected is a 422 at create
    (nothing stored) and the plan's reason; a malformed name is a 422 on both."""
    assert ok(api.post(PLAN, json={"workflow_id": "echo", "host": "fake"}))["host"] == "fake"
    sid = new_session(api, "Contract Run on")["id"]
    run = wait_run(api, start_run(api, sid, QUICK, host="fake")["id"])
    assert (run["status"], run["host"]) == ("succeeded", "fake"), run
    body = {"workflow_id": "echo", "inputs": QUICK, "host": "nosuchhost"}
    assert ok(api.post(PLAN, json=body)) == {
        "host": None,
        "peak_gb": None,
        "reason": "host nosuchhost is offline",
    }
    r = api.post(f"{STUDIO}/sessions/{sid}/runs", json=body)
    assert r.status_code == 422 and r.json()["detail"] == "host nosuchhost is offline", r.text
    assert len(ok(api.get(f"{STUDIO}/sessions/{sid}/runs"))["runs"]) == 1
    for url in (PLAN, f"{STUDIO}/sessions/{sid}/runs"):
        assert api.post(url, json=body | {"host": "Fake"}).status_code == 422


def test_plan_answers_the_runs_422s(api):
    r = api.post(PLAN, json={"workflow_id": "echo", "inputs": {"echo.steps": 0}})
    assert r.status_code == 422 and r.json()["detail"] == "echo.steps: must be between 1 and 200", (
        r.text
    )
    r = api.post(PLAN, json={"workflow_id": "no-such-workflow"})
    assert r.status_code == 422 and "unknown workflow no-such-workflow" in r.json()["detail"], (
        r.text
    )
    assert api.post(PLAN, json={"inputs": {}}).status_code == 422  # workflow_id is required


def test_plan_of_another_users_workflow_is_an_unknown_workflow(api, api_b):
    wid = ok(api.post(WF, json={"based_on": "echo", "title": "Contract plan A"}), 201)["id"]
    try:
        assert ok(api.post(PLAN, json={"workflow_id": wid}))["host"] == "fake"
        r = api_b.post(PLAN, json={"workflow_id": wid})
        assert r.status_code == 422 and f"unknown workflow {wid}" in r.json()["detail"], r.text
        r = api_b.post(PLAN, json={"workflow_id": NO_SUCH_ID})
        assert r.status_code == 422, r.text
    finally:
        assert api.delete(f"{WF}/{wid}").status_code == 204


def test_studio_help_and_preset_order_are_kept(api):
    body = json.loads((FIXTURES / "WorkflowCreate.valid.1.json").read_text())
    studio = body["studio"]
    w = ok(api.post(WF, json=body), 201)
    try:
        assert w["studio"]["preset_order"] == studio["preset_order"]
        assert w["studio"]["inspector"][0]["help"] == studio["inspector"][0]["help"]
        assert ok(api.get(f"{WF}/{w['id']}"))["studio"]["preset_order"] == studio["preset_order"]
    finally:
        assert api.delete(f"{WF}/{w['id']}").status_code == 204
    for order in ([studio["preset_order"][0]], studio["preset_order"] + ["Draft"]):
        r = api.post(WF, json=body | {"studio": studio | {"preset_order": order}})
        assert (
            r.status_code == 422 and r.json()["detail"] == "studio.preset_order: every preset once"
        ), r.text
