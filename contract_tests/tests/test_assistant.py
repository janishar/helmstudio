"""The in-app assistant through studio and pipelines (API 1.44; plan 18, 22.3 and 22.11) on the fake stack: the admin's
settings, a chat whose turn calls a studio read tool (studio.tools as the thread's owner) and answers, its entries and
events, and isolation (another user's chat and memory item are 404). The model is the fake OpenAI-compatible server
as a local server, scripted per model (test_director's `fake`); nothing real is ever called."""

import time
import uuid

import pytest
from helpers import STUDIO, ok, png_bytes, upload, wait_for
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
    assert status["thinks"] is False  # API 1.54: the fake model has no preset, so no thinking rule
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


def test_the_window_per_model_and_the_cache_hit_rate(api, admin, assistant):
    """API 1.66 (alpha.2 item 30): the chat window is the model's own; each reply's cache hits are kept and the
    admin reads the hit rate per provider and model."""
    now = ok(admin.get(SETTINGS))
    assert now["window_tokens"] == 60000
    body = {"enabled": True, "provider": now["provider"]}
    assert admin.put(SETTINGS, json=body | {"window_tokens": 100}).status_code == 422
    assert ok(admin.put(SETTINGS, json=body | {"window_tokens": 128000}))["window_tokens"] == 128000
    fake("POST", "/__script", {assistant: [{"content": "Cached.", "usage": {
        "prompt_tokens": 1000, "prompt_cache_hit_tokens": 900, "prompt_cache_miss_tokens": 100}}]})  # fmt: skip
    chat = ok(api.post(THREADS, json={"client_id": TAB}), 201)
    url = f"{THREADS}/{chat['id']}"
    msg = {"kind": "message", "client_id": TAB, "text": "hi", "screen": screen()}
    ok(api.post(f"{url}/messages", json=msg), 202)
    wait_for(lambda: ok(api.get(url))["state"] == "idle", timeout=60, what="the assistant's answer")
    assert api.get(f"{SETTINGS}/cache").status_code == 403
    got = ok(admin.get(f"{SETTINGS}/cache"))
    (row,) = [m for m in got["models"] if m["model"] == assistant]
    assert got["days"] == 30 and row == {"provider": now["provider"], "model": assistant, "calls": 1,
                                         "prompt_tokens": 1000, "hit_tokens": 900, "hit_rate": 0.9}  # fmt: skip
    ok(api.delete(url), 204)


def test_steer_while_it_answers(api, assistant):
    """API 1.59 (board Assistant frame 8): a message while the turn works is a steer: read at the next step (the next
    model call has it as the user's words), or, when the answer ends first, sent as the next message."""
    fake("POST", "/__script", {assistant: [
        {"delay_s": 4, "content": "Looking.", "tool_calls": [{"tool": "search_takes", "args": {"limit": 3}}]},
        {"content": "Switched to Turbo."},
        {"delay_s": 4, "content": "Done."},
        {"content": "Added the bus."},
    ]})  # fmt: skip
    tid = ok(api.post(THREADS, json={"client_id": TAB}), 201)["id"]
    url = f"{THREADS}/{tid}"

    def post(text: str) -> dict:
        return ok(api.post(f"{url}/messages", json={"kind": "message", "client_id": TAB, "text": text,
                                                    "screen": screen()}), 202)  # fmt: skip

    def idle():
        got = ok(api.get(url))
        return got if got["state"] == "idle" else None

    def steer_of(got: dict, text: str) -> str | None:
        return next(e.get("steer") for e in got["entries"] if e.get("text") == text)

    before = len([r for r in fake("GET", "/__requests") if r["model"] == assistant])
    assert post("Make a rainy London still").get("steer") is not True
    time.sleep(1.5)  # the turn is in its first model call
    sent = post("Use Turbo instead")
    assert sent["steer"] is True and sent["entry"]["steer"] == "waiting"
    got = wait_for(idle, timeout=60, what="the steered answer")
    assert steer_of(got, "Use Turbo instead") == "read"
    calls = [r for r in fake("GET", "/__requests") if r["model"] == assistant][before:]
    assert len(calls) == 2 and "User: Use Turbo instead" in str(calls[1]["body"]["messages"])
    assert any("Switched to Turbo." in (e.get("text") or "") for e in got["entries"])

    # the answer ends before its next step: the steer is sent as the next message, a new turn
    post("Write the prompt")
    time.sleep(1.5)  # past its first step: the model call runs
    assert post("Add a red bus behind her")["steer"] is True
    got = wait_for(lambda: (g := idle()) and any("Added the bus." in (e.get("text") or "") for e in g["entries"]) and g,
                   timeout=60, what="the next message's answer")  # fmt: skip
    assert steer_of(got, "Add a red bus behind her") == "next"
    calls = [r for r in fake("GET", "/__requests") if r["model"] == assistant][before:]
    assert len(calls) == 4 and "User: Add a red bus behind her" in str(calls[3]["body"]["messages"])

    # Stop on an idle chat does nothing
    assert (
        ok(api.post(f"{url}/messages", json={"kind": "stop", "client_id": TAB}), 202)["entry"]
        is None
    )
    ok(api.delete(url), 204)


def test_attached_images_reach_a_model_that_sees(api, api_b, anon, admin, fake_ctl):  # noqa: F811
    """API 1.65 (board Assistant frame 12): a message's attached images are the owner's takes (else 422), stored on
    its entry; a model whose preset's vision matches it (ollama: llava) gets them as image parts, a Looked at line
    shows, and status.sees says so."""
    model = f"llava-{uuid.uuid4().hex[:8]}"
    was = ok(admin.get(SETTINGS))  # put back after: a later module's assistant may still be on
    was = {k: was[k] for k in ("enabled", "provider", "model") if was.get(k) is not None}
    with provider(admin, f"srv-{model}", server_body(model) | {"preset": "ollama"}) as name:
        ok(admin.put(SETTINGS, json={"enabled": True, "provider": name}))
        try:
            fake("POST", "/__script", {model: [{"content": "Red, flat light."}]})
            assert ok(api.get(THREADS))["status"]["sees"] is True
            mine = upload(api, anon, png_bytes(1600, 900))["id"]
            theirs = upload(api_b, anon, png_bytes(8, 8))["id"]
            tid = ok(api.post(THREADS, json={"client_id": TAB}), 201)["id"]
            url = f"{THREADS}/{tid}"
            body = {
                "kind": "message",
                "client_id": TAB,
                "text": "What light is this?",
                "screen": screen(),
            }
            r = api.post(f"{url}/messages", json=body | {"takes": [theirs]})
            assert r.status_code == 422 and r.json()["detail"] == f"takes: {theirs} is not one of your takes"
            sent = ok(api.post(f"{url}/messages", json=body | {"takes": [mine]}), 202)
            assert (
                sent["entry"]["takes"] == [mine] and sent["entry"]["text"] == "What light is this?"
            )
            got = wait_for(
                lambda: (g := ok(api.get(url)))["state"] == "idle" and g,
                timeout=60,
                what="the answer",
            )
            lines = [e["line"]["text"] for e in got["entries"] if e["kind"] == "line"]
            assert lines == ["Looked at ref"], got["entries"]
            (call,) = [r for r in fake("GET", "/__requests") if r["model"] == model]
            user = call["body"]["messages"][1]["content"]
            assert f"(attached: [[take:{mine}]])" in user[0]["text"]
            assert user[1]["image_url"]["url"].startswith("data:image/jpeg;base64,/9j/")
            ok(api.delete(url), 204)
        finally:
            ok(admin.put(SETTINGS, json=was if was.get("provider") else {"enabled": False}))
