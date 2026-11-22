"""The in-app assistant through studio and pipelines (API 1.44; plan 18, 22.3 and 22.11) on the fake stack: the admin's
settings, a chat whose turn calls a studio read tool (studio.tools as the thread's owner) and answers, its entries and
events, and isolation (another user's chat and memory item are 404). The model is the fake OpenAI-compatible server
as a local server, scripted per model (test_director's `fake`); nothing real is ever called."""

import uuid

import pytest
from helpers import STUDIO, ok, wait_for
from test_director import fake, fake_ctl, provider, server_body  # noqa: F401  (fake_ctl is a fixture)

THREADS = f"{STUDIO}/assistant/threads"
SETTINGS = f"{STUDIO}/admin/assistant"
TAB = "contract-tab-1"


def screen(route: str = "/gallery") -> dict:
    return {"route": route, "ids": {}, "roles": ["user"], "as_of": "2026-11-22T10:00:00Z", "hidden": False,
            "actions": ["navigate", "await", "search"], "fields": {}}  # fmt: skip


@pytest.fixture(scope="module")
def assistant(admin, fake_ctl):  # noqa: F811
    """The assistant on, its model `m-<tag>` on the fake as a local server; off again after."""
    model = f"asst-{uuid.uuid4().hex[:8]}"
    with provider(admin, f"srv-{model}", server_body(model)) as name:
        ok(admin.put(SETTINGS, json={"enabled": True, "provider": name}))
        try:
            yield model
        finally:
            ok(admin.put(SETTINGS, json={"enabled": False}))


def test_settings_are_the_admins(api, admin):
    assert api.get(SETTINGS).status_code == 403
    assert api.put(SETTINGS, json={"enabled": False}).status_code == 403
    got = ok(admin.get(SETTINGS))
    assert set(got["limits"]) == {
        "model_calls_per_day",
        "tokens_per_day",
        "look_at_per_hour",
        "messages_per_minute",
    }
    assert admin.put(SETTINGS, json={"enabled": True}).status_code == 422  # no provider


def test_a_turn_reads_through_studio(api, api_b, assistant):
    fake("POST", "/__script", {assistant: [
        {"content": "Looking.", "tool_calls": [{"tool": "search_takes", "args": {"limit": 3}}]},
        {"content": "You have no takes matching that yet."},
    ]})  # fmt: skip
    status = ok(api.get(THREADS))["status"]
    assert status["enabled"] and status["provider"]["remote"] is False
    chat = ok(api.post(THREADS, json={"client_id": TAB}), 201)
    tid = chat["id"]
    url = f"{THREADS}/{tid}"
    sent = ok(api.post(f"{url}/messages", json={"kind": "message", "client_id": TAB, "text": "Any foxes?",
                                                "screen": screen()}), 202)  # fmt: skip
    assert sent["entry"]["kind"] == "user" and sent["thread"]["state"] == "working"

    def answered():
        got = ok(api.get(url))
        return got if got["state"] == "idle" else None

    got = wait_for(answered, timeout=60, what="the assistant's answer")
    words = [e["text"] for e in got["entries"] if e["kind"] == "assistant"]
    assert any("no takes matching" in w for w in words), got["entries"]
    # the second model call saw the tool's result, the API's own TakePage
    calls = [r for r in fake("GET", "/__requests") if r["model"] == assistant]
    assert len(calls) == 2 and '"takes"' in str(calls[1]["body"]["messages"])
    events = ok(api.get(f"{url}/events", params={"since": 0}))
    assert {e["type"] for e in events["events"]} == {"entry", "thread"}
    assert events["last_seq"] == events["events"][-1]["seq"]

    # another user's chat and memory item are 404
    assert api_b.get(url).status_code == 404
    assert api_b.get(f"{url}/events").status_code == 404
    assert (
        api_b.post(f"{url}/messages", json={"kind": "continue", "client_id": TAB}).status_code
        == 404
    )
    assert api_b.delete(url).status_code == 404
    assert api_b.delete(f"{STUDIO}/assistant/memory/{uuid.uuid4()}").status_code == 404
    assert tid not in [t["id"] for t in ok(api_b.get(THREADS))["threads"]]
    assert ok(api_b.get(f"{STUDIO}/assistant/memory"))["counts"]["summary"] >= 0

    ok(api.delete(url), 204)
    assert api.get(url).status_code == 404
