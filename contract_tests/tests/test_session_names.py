"""Plan 21 (API 1.43; contracts/README.md "Deleting runs", "Sessions: titles and naming"): DELETE a finished run (its
409s, 404 for another user and twice, gone from the list and the replay, its takes kept), who wrote a title, the
admin's session-names setting, and an untitled session named end to end by a server on the fake OpenAI-compatible
server (studio → cmd.pipeline.name → pipelines → evt.pipeline.<session>.named → studio)."""

import json
import uuid

import pytest
from helpers import NO_SUCH_ID, STUDIO, new_session, ok, start_run, take_of_run, wait_for, wait_run

NAMES = f"{STUDIO}/admin/session-names"
FAST = {"echo.steps": 2, "echo.delay_ms": 50}


def test_delete_a_finished_run(api, api_b):
    sid = new_session(api)["id"]
    run = start_run(api, sid, {"prompt.text": "a fox", **FAST})
    assert api.delete(f"{STUDIO}/runs/{run['id']}").status_code == 409  # queued or running
    assert wait_run(api, run["id"])["status"] == "succeeded"
    take = take_of_run(api, run["id"])
    assert api_b.delete(f"{STUDIO}/runs/{run['id']}").status_code == 404
    for rid in (NO_SUCH_ID, "nope"):
        assert api.delete(f"{STUDIO}/runs/{rid}").status_code == 404
    assert api.delete(f"{STUDIO}/runs/{run['id']}").status_code == 204
    assert api.delete(f"{STUDIO}/runs/{run['id']}").status_code == 404
    assert api.get(f"{STUDIO}/runs/{run['id']}").status_code == 404
    assert ok(api.get(f"{STUDIO}/sessions/{sid}/runs"))["runs"] == []
    page = ok(api.get(f"{STUDIO}/sessions/{sid}/events", params={"since": 0}))
    assert page["events"] == [] and page["last_seq"] > 0
    assert ok(api.get(f"{STUDIO}/takes/{take['id']}"))["id"] == take["id"]  # its take stays


def test_who_wrote_the_title(api):
    def source(body):
        return ok(api.post(f"{STUDIO}/sessions", json=body), 201)["title_source"]

    assert source({}) == "default" and source({"title": " "}) == "default"
    assert source({"title": "Foxes"}) == "user"
    assert source({"title": "a red fox", "from_prompt": True}) == "prompt"
    s = ok(api.post(f"{STUDIO}/sessions"), 201)
    renamed = ok(api.patch(f"{STUDIO}/sessions/{s['id']}", json={"title": "Mine"}))
    assert renamed["title_source"] == "user" and renamed["named_by"] is None
    assert ok(api.get(f"{STUDIO}/sessions"))["naming"]["mode"] in ("off", "local", "remote")


def test_the_setting_is_the_admins(api, admin):
    assert api.get(NAMES).status_code == 403
    assert api.put(NAMES, json={"mode": "off"}).status_code == 403
    now = ok(admin.get(NAMES))
    for body in ({"mode": "cloud"}, {"mode": "remote"}, {"mode": "local", "provider": "x"},
                 {"mode": "remote", "provider": f"no-{uuid.uuid4().hex[:8]}"}):  # fmt: skip
        assert admin.put(NAMES, json=body).status_code == 422, body
    if not now["local_available"]:
        assert (
            admin.put(NAMES, json={"mode": "local"}).json()["detail"]
            == "Qwen3.8 27B is not downloaded"
        )
    assert ok(admin.put(NAMES, json={"mode": "off"}))["mode"] == "off"


@pytest.fixture
def fake_server(admin):
    name = f"srv-{uuid.uuid4().hex[:8]}"
    body = {"kind": "server", "title": "Fake server", "base_url": "http://fake-openai:8000/v1", "models": ["namer"],
            "host": "fake", "memory_gb": 0.5, "timeout_s": 30}  # fmt: skip
    ok(admin.put(f"{STUDIO}/admin/providers/{name}", json=body), 201)
    yield name
    admin.put(NAMES, json={"mode": "off"})
    admin.delete(f"{STUDIO}/admin/providers/{name}")


def test_an_untitled_session_is_named_once(api, admin, fake_server):
    got = ok(admin.put(NAMES, json={"mode": "remote", "provider": fake_server}))
    assert (got["provider"], got["model"]) == (fake_server, "namer")
    naming = ok(api.get(f"{STUDIO}/sessions"))["naming"]
    assert naming == {"mode": "remote", "named_by": "Fake server", "remote": False}
    sid = ok(api.post(f"{STUDIO}/sessions"), 201)["id"]
    answer = "ECHO-ANSWER: " + json.dumps({"title": "Red fox at dusk"})
    run = start_run(api, sid, {"prompt.text": answer, **FAST})
    assert wait_run(api, run["id"])["status"] == "succeeded"
    named = wait_for(
        lambda: (s := ok(api.get(f"{STUDIO}/sessions/{sid}")))["title_source"] == "model" and s,
        what="the session's name",
    )
    assert named["title"] == "Red fox at dusk" and named["named_by"] == "Fake server"
    events = ok(api.get(f"{STUDIO}/sessions/{sid}/events", params={"since": 0}))["events"]
    titled = [e["data"] for e in events if e["type"] == "session"]
    # the prompt's first words at once (owner, 2026-11-25), then the namer's title
    assert [d["title_source"] for d in titled] == ["prompt", "model"] and titled[-1][
        "title"
    ] == "Red fox at dusk"
    # once per session: a rename stays, a later run asks nothing
    ok(api.patch(f"{STUDIO}/sessions/{sid}", json={"title": "Mine"}))
    again = start_run(api, sid, {"prompt.text": answer, **FAST})
    assert wait_run(api, again["id"])["status"] == "succeeded"
    assert ok(api.get(f"{STUDIO}/sessions/{sid}"))["title"] == "Mine"


def test_with_no_namer_an_untitled_session_takes_its_first_words(api):
    # owner, 2026-11-25: naming off (the default) names a New session from its first prompt's first ~5 words
    sid = ok(api.post(f"{STUDIO}/sessions"), 201)["id"]
    run = start_run(api, sid, {"prompt.text": "old fisherman on a pier, at dawn", **FAST})
    assert wait_run(api, run["id"])["status"] == "succeeded"
    s = ok(api.get(f"{STUDIO}/sessions/{sid}"))
    assert (s["title"], s["title_source"], s["named_by"]) == (
        "Old fisherman on a pier",
        "prompt",
        None,
    )
    events = ok(api.get(f"{STUDIO}/sessions/{sid}/events", params={"since": 0}))["events"]
    assert [e["data"] for e in events if e["type"] == "session"] == [
        {"title": "Old fisherman on a pier", "title_source": "prompt", "named_by": None}
    ]


def test_an_untitled_sequence_is_named_once(api, admin, fake_server):
    """API 1.76 (alpha.5 item 14): an untitled sequence takes its first take's prompt, then the namer's title through
    its session; a rename stays."""
    ok(admin.put(NAMES, json={"mode": "remote", "provider": fake_server}))
    sid = ok(api.post(f"{STUDIO}/sessions", json={"title": "Shots"}), 201)["id"]
    answer = "ECHO-ANSWER: " + json.dumps({"title": "Gulls over the harbour"})
    run = start_run(api, sid, {"prompt.text": answer, **FAST})
    assert wait_run(api, run["id"])["status"] == "succeeded"
    take = take_of_run(api, run["id"])
    tl = ok(api.post(f"{STUDIO}/timelines", json={"take_ids": [take["id"]]}), 201)
    assert tl["title_source"] == "prompt" and tl["name"].startswith("ECHO-ANSWER:")
    url = f"{STUDIO}/timelines/{tl['id']}"
    named = wait_for(
        lambda: (t := ok(api.get(url)))["title_source"] == "model" and t, what="the sequence's name"
    )
    assert (named["name"], named["named_by"]) == ("Gulls over the harbour", "Fake server")
    renamed = ok(api.patch(url, json={"name": "Harbour"}))
    assert (renamed["name"], renamed["title_source"], renamed["named_by"]) == (
        "Harbour",
        "user",
        None,
    )
