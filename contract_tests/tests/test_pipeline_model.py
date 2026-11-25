"""Plan 33 (API 1.74; contracts/README.md "Model sources: the pipelines default model"): the admin's default for a
pipeline write, decide or agent step on Auto. Admin only, its 422s, ModelSourceList.auto, and a decide on Auto run end
to end on a fake server (and a fake remote, under the deployment's switch) default: egress at Submit, the decision's
source. Every test puts the setting back to local. The 409 (a remote default while the switch is off) is in
studio_service/tests/pipeline_model_test.go: the test stack's switch does not change."""

import uuid
from contextlib import contextmanager

import pytest
from helpers import STUDIO, ok
from test_batches import finish
from test_director import (  # noqa: F401 (fixtures)
    PIPES,
    admin_view,
    all_ended,
    decided,
    decisions,
    keep,
    remote,
    server,
    submit,
    tag,
)
from test_pipelines import create

MODEL = f"{STUDIO}/admin/pipeline-model"


@contextmanager
def default(admin, body: dict):
    got = ok(admin.put(MODEL, json=body))
    try:
        yield got
    finally:
        assert ok(admin.put(MODEL, json={"mode": "local"}))["mode"] == "local"


def test_the_setting_is_the_admins(api, admin):
    assert api.get(MODEL).status_code == 403
    assert api.put(MODEL, json={"mode": "local"}).status_code == 403
    assert ok(admin.get(MODEL))["mode"] == "local"
    for body in ({"mode": "agent"}, {"mode": "remote"}, {"mode": "local", "provider": "x"},
                 {"mode": "assistant", "model": "m"},
                 {"mode": "remote", "provider": f"no-{uuid.uuid4().hex[:8]}"}):  # fmt: skip
        r = admin.put(MODEL, json=body)
        assert r.status_code == 422 and isinstance(r.json()["detail"], str), body
    assert ok(api.get(f"{STUDIO}/model-sources"))["auto"] is None


def run_auto(api, admin, kind: str, name: str, model: str) -> None:
    """A decide on Auto with the default on provider `name`: validate and Submit list it; the decision ran there."""
    with default(admin, {"mode": "remote", "provider": name}) as got:
        assert (got["mode"], got["provider"], got["model"]) == ("remote", name, model)
        auto = ok(api.get(f"{STUDIO}/model-sources"))["auto"]
        assert (auto["kind"], auto["provider"], auto["model"]) == (kind, name, model)
        d = decided(
            f"Contract auto {kind}", "auto"
        )  # no pipeline remote switch: the admin's default needs none
        v = ok(api.post(f"{PIPES}/validate", json=d))
        assert v["can_run"] and v["models"]["check"] == f"{kind}:{name}/{model}", v
        (eg,) = v["egress"]
        assert (eg["node"], eg["where"], eg["provider"], eg["model"]) == (
            "check",
            kind,
            name,
            model,
        )
        p = create(api, d)
        b = submit(api, p["id"], [keep(f"auto {tag()}")])
        try:
            (r,) = all_ended(api, b["id"])
        finally:
            finish(api, b["id"])
        assert b["egress"] == v["egress"] and r["status"] == "succeeded", r
        (dd,) = decisions(api, b["id"], 1)
        assert (dd["source"]["kind"], dd["source"]["provider"], dd["source"]["model"]) == (
            kind,
            name,
            model,
        )
        assert api.delete(f"{PIPES}/{p['id']}").status_code == 204


def test_auto_on_a_server_default(api, admin, server):  # noqa: F811
    run_auto(api, admin, "server", server["name"], server["model"])


def test_auto_on_a_remote_default(api, admin, remote):  # noqa: F811
    run_auto(api, admin, "remote", remote["name"], remote["model"])


def test_same_as_assistant_needs_the_assistant(admin):
    a = ok(admin.get(f"{STUDIO}/admin/assistant"))
    if a["provider"] is not None:
        pytest.skip("the test stack's Assistant is set up")
    r = admin.put(MODEL, json={"mode": "assistant"})
    assert (r.status_code, r.json()["detail"]) == (422, "the Assistant is not set up")
