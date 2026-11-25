"""Plugins (API 1.61; contracts/README.md "Plugins") on the test stack: the repo's plugins/ and the fake plugins
(contract_tests/fake_plugins: the fake OpenAI server as a personal model provider and as assistant tools). Admin only;
found off; on and off; Add as provider is Only you (no other account sees or uses it); Remove and Rescan; a plugin's
tools in the assistant (run at once, a risk flag asks on a plugin card, a failure is a plain line, off removes them)."""

import uuid

import pytest
from conftest import make_client  # a plugin's client sends only its pass, no user token
from helpers import STUDIO, ok, png_bytes, upload, wait_for
from test_assistant import TAB, assistant, screen  # noqa: F401  (assistant is a fixture)
from test_director import SERVER_HOST, fake, fake_ctl, provider, server_body  # noqa: F401

PLUGINS = f"{STUDIO}/admin/plugins"
THREADS = f"{STUDIO}/assistant/threads"
SETTINGS = f"{STUDIO}/admin/assistant"


def plugin(admin, name: str) -> dict:
    return next(p for p in ok(admin.get(PLUGINS))["plugins"] if p["name"] == name)


@pytest.fixture
def on(admin):
    """on(name): turn a fake plugin on; every one turned on here is off again after."""
    turned = []

    def turn(name: str) -> dict:
        turned.append(name)
        return ok(admin.put(f"{PLUGINS}/{name}", json={"enabled": True}))

    yield turn
    for name in turned:
        admin.put(f"{PLUGINS}/{name}", json={"enabled": False})


def test_admin_only(api):
    for method, path, body in (("GET", PLUGINS, None), ("POST", f"{PLUGINS}/rescan", None),
                               ("PUT", f"{PLUGINS}/fake-tools", {"enabled": True}), ("DELETE", f"{PLUGINS}/fake-tools", None),
                               ("POST", f"{PLUGINS}/fake-provider/provider", {"key": "k"})):  # fmt: skip
        r = api.request(method, path, json=body)
        assert r.status_code == 403 and r.json()["detail"] == "admin role required", (method, path)


def test_found_off_then_on(admin, on, fake_ctl):  # noqa: F811
    got = ok(admin.post(f"{PLUGINS}/rescan"))
    names = {p["name"] for p in got["plugins"]}
    assert {"claude-code-bridge", "web-search", "fake-provider", "fake-tools"} <= names, names
    bridge = next(p for p in got["plugins"] if p["name"] == "claude-code-bridge")
    assert bridge["kind"] == "model-provider" and bridge["personal"] is True and bridge["models"]
    ws = next(p for p in got["plugins"] if p["name"] == "web-search")
    assert ws["kind"] == "assistant-tools" and ws["tools"][0]["risk"]["sends_off_machine"] is True
    tools = plugin(admin, "fake-tools")
    if not tools["enabled"]:
        assert tools["status"] == "off"
    assert on("fake-tools")["status"] == "running"  # its health path on the fake answers
    assert admin.put(f"{PLUGINS}/no-such-plugin", json={"enabled": True}).status_code == 404
    assert admin.put(f"{PLUGINS}/fake-tools", json={}).status_code == 422
    assert (
        admin.put(f"{PLUGINS}/fake-tools", json={"enabled": True, "settings": {}}).status_code
        == 422
    )
    off = ok(admin.put(f"{PLUGINS}/fake-tools", json={"enabled": False}))
    assert (off["enabled"], off["status"]) == (False, "off")


def test_add_as_provider_is_only_you(admin, admin_session, api, on, fake_ctl):  # noqa: F811
    url = f"{PLUGINS}/fake-provider/provider"
    admin.put(f"{PLUGINS}/fake-provider", json={"enabled": False})
    r = admin.post(url, json={"key": "any-key", "host": SERVER_HOST})
    assert r.status_code == 409 and r.json()["detail"] == "turn Fake provider on first"
    on("fake-provider")
    on("fake-tools")
    assert (
        admin.post(f"{PLUGINS}/fake-tools/provider", json={"key": "k"}).status_code == 409
    )  # not a model provider
    r = admin.post(url, json={"key": "refused-key", "host": SERVER_HOST})
    assert (
        r.status_code == 422
        and r.json()["detail"] == "The key doesn't match: copy it again from the plugin's .env"
    )
    assert (
        admin.post(url, json={"key": "k", "default_model": "nope", "host": SERVER_HOST}).status_code
        == 422
    )
    assert (
        admin.post(url, json={"key": "k", "base_url": "http://x"}).status_code == 422
    )  # never a URL

    me = admin_session["user"]["id"]
    made = ok(admin.post(url, json={"key": "any-key", "host": SERVER_HOST}), 201)
    name = made["name"]
    assert name == "fake-provider-" + me.replace("-", "")[:8]
    assert (made["owner_id"], made["plugin"], made["title"], made["kind"]) == (
        me,
        "fake-provider",
        "Fake provider",
        "server",
    )
    assert made["base_url"] == "http://fake-openai:8000/v1" and made["models"] == [
        "fake-small",
        "fake-large",
    ]
    assert plugin(admin, "fake-provider")["provider"] == name
    try:
        # the owner sees and may use it; another account never does
        assert name in [p["name"] for p in ok(admin.get(f"{STUDIO}/admin/providers"))["providers"]]
        assert name in [s["name"] for s in ok(admin.get(f"{STUDIO}/model-sources"))["sources"]]
        assert name not in [s["name"] for s in ok(api.get(f"{STUDIO}/model-sources"))["sources"]]
        # session naming runs for every account: never an Only-you provider
        r = admin.put(
            f"{STUDIO}/admin/session-names",
            json={"mode": "remote", "provider": name, "model": "fake-small"},
        )
        assert r.status_code == 422 and "only yours" in r.json()["detail"]

        # the assistant: the owner's own choice; everyone else keeps the deployment's
        model = f"shared-{uuid.uuid4().hex[:8]}"
        with provider(admin, f"srv-{model}", server_body(model)) as shared:
            ok(admin.put(SETTINGS, json={"enabled": True, "provider": shared}))
            mine = ok(
                admin.put(SETTINGS, json={"enabled": True, "provider": name, "model": "fake-small"})
            )
            assert (mine["provider"], mine["personal"]) == (name, True)
            assert ok(admin.get(SETTINGS))["provider"] == name
            assert ok(admin.get(THREADS))["status"]["provider"]["title"] == "Fake provider"
            assert ok(api.get(THREADS))["status"]["provider"]["title"] == "Fake server"
            back = ok(
                admin.put(SETTINGS, json={"enabled": True, "provider": shared})
            )  # a shared one clears it
            assert (back["provider"], back["personal"]) == (shared, False)
            ok(admin.put(SETTINGS, json={"enabled": False}))

        # off hides its provider; on brings it back
        admin.put(f"{PLUGINS}/fake-provider", json={"enabled": False})
        assert name not in [s["name"] for s in ok(admin.get(f"{STUDIO}/model-sources"))["sources"]]
        on("fake-provider")
        assert name in [s["name"] for s in ok(admin.get(f"{STUDIO}/model-sources"))["sources"]]
    finally:
        # Remove: its provider goes too; the folder stays, so Rescan finds it again, off
        ok(admin.delete(f"{PLUGINS}/fake-provider"), 204)
    assert name not in [p["name"] for p in ok(admin.get(f"{STUDIO}/admin/providers"))["providers"]]
    assert admin.delete(f"{PLUGINS}/fake-provider").status_code == 404
    again = ok(admin.post(f"{PLUGINS}/rescan"))
    assert "fake-provider" in again["new"]
    assert plugin(admin, "fake-provider")["enabled"] is False


def _turn(api, text: str) -> tuple[str, dict]:
    tid = ok(api.post(THREADS, json={"client_id": TAB}), 201)["id"]
    ok(api.post(f"{THREADS}/{tid}/messages", json={"kind": "message", "client_id": TAB, "text": text,
                                                   "screen": screen()}), 202)  # fmt: skip
    return tid, wait_for(lambda: (g := ok(api.get(f"{THREADS}/{tid}")))["state"] in ("idle", "waiting") and g or None,
                         timeout=60, what="the assistant")  # fmt: skip


def _lines(got: dict) -> list[dict]:
    return [e["line"] for e in got["entries"] if e["kind"] == "line"]


def test_plugin_tools_in_the_assistant(admin, api, user_a, assistant, on):  # noqa: F811
    on("fake-tools")
    word = uuid.uuid4().hex[:8]
    fake("POST", "/__script", {assistant: [
        {"content": "Echoing.", "tool_calls": [{"tool": "fake-tools__echo", "args": {"text": word}}]},
        {"content": "Done."},
    ]})  # fmt: skip
    tid, got = _turn(api, "echo it")
    assert got["state"] == "idle"
    assert {"status": "done", "text": f"Echoed {word}"}.items() <= next(
        x for x in _lines(got) if word in x["text"]).items(), _lines(got)  # fmt: skip
    call = next(
        r
        for r in fake("GET", "/__requests")
        if r["model"] == "tool:echo" and r["body"]["input"]["text"] == word
    )
    assert call["owner"] == user_a["user"]["id"] and call["call"].startswith(tid + ":")
    assert (call["authorization"] or "").startswith("Bearer ")
    ok(api.delete(f"{THREADS}/{tid}"), 204)

    # a failure is a plain failed line; the model is told
    fake("POST", "/__script", {assistant: [
        {"content": "Trying.", "tool_calls": [{"tool": "fake-tools__echo", "args": {"text": f"fail-{word}"}}]},
        {"content": "It failed."},
    ]})  # fmt: skip
    tid, got = _turn(api, "echo a failure")
    bad = next(x for x in _lines(got) if x["status"] == "failed")
    assert "Fake tools" in bad["text"] and "fail" in bad["text"], bad
    ok(api.delete(f"{THREADS}/{tid}"), 204)

    # a risk flag asks first: a plugin card; Yes runs it
    fake("POST", "/__script", {assistant: [
        {"content": "Asking.", "tool_calls": [{"tool": "fake-tools__wipe", "args": {"text": word}}]},
        {"content": "Wiped."},
    ]})  # fmt: skip
    tid, got = _turn(api, "wipe it")
    assert got["state"] == "waiting", got
    card = next(e["card"] for e in got["entries"] if e["kind"] == "card")
    assert card["kind"] == "plugin" and "can't be undone" in card["tags"], card
    ok(api.post(f"{THREADS}/{tid}/messages", json={"kind": "answer", "client_id": TAB, "card_id": card["id"],
                                                   "answer": "yes"}), 202)  # fmt: skip
    got = wait_for(lambda: (g := ok(api.get(f"{THREADS}/{tid}")))["state"] == "idle" and g or None, timeout=60,
                   what="the answer after Yes")  # fmt: skip
    assert any(
        r["model"] == "tool:wipe" and r["body"]["input"]["text"] == word
        for r in fake("GET", "/__requests")
    )
    ok(api.delete(f"{THREADS}/{tid}"), 204)

    # off: the next input offers none of its tools
    admin.put(f"{PLUGINS}/fake-tools", json={"enabled": False})
    fake("POST", "/__script", {assistant: [{"content": "No tools."}]})
    tid, _ = _turn(api, "anything")
    last = [r for r in fake("GET", "/__requests") if r["model"] == assistant][-1]
    assert not any(
        "fake-tools__" in t["function"]["name"] for t in last["body"].get("tools") or []
    ), last["body"].get("tools")
    ok(api.delete(f"{THREADS}/{tid}"), 204)


def test_a_plugin_pass_opens_only_its_owners_takes(admin, api, api_b, anon, assistant, on):  # noqa: F811
    """API 1.70 (contracts/README.md "Plugins: takes"): the call's pass is studio's; with it, and no API key, the
    plugin reads one of the owner's takes and makes a new one from it (source tool); another user's take is 404, a
    forged pass 401, and the pass stops working once the plugin is off."""
    on("fake-tools")
    mine = upload(api, anon, png_bytes(6, 4), "mine.png")
    theirs = upload(api_b, anon, png_bytes(5, 5), "theirs.png")
    word = uuid.uuid4().hex[:8]
    fake("POST", "/__script", {assistant: [
        {"content": "Echoing.", "tool_calls": [{"tool": "fake-tools__echo", "args": {"text": word}}]},
        {"content": "Done."},
    ]})  # fmt: skip
    tid, _ = _turn(api, "echo it")
    call = next(
        r
        for r in fake("GET", "/__requests")
        if r["model"] == "tool:echo" and r["body"]["input"]["text"] == word
    )
    assert call["api"] and call["api"].startswith("http")  # X-Helm-Api: the stack's public base URL
    with (
        make_client({"Authorization": call["authorization"]}) as plug,
        make_client({"Authorization": "Bearer a.b"}) as forged,
    ):
        got = ok(plug.get(f"{STUDIO}/plugin/takes/{mine['id']}"))
        assert (
            got["id"] == mine["id"]
            and got["kind"] == "image"
            and (got["width"], got["height"]) == (6, 4)
        )
        assert anon.get(got["url"]).content == png_bytes(6, 4)
        assert plug.get(f"{STUDIO}/plugin/takes/{theirs['id']}").status_code == 404
        assert forged.get(f"{STUDIO}/plugin/takes/{mine['id']}").status_code == 401
        assert (
            api.get(f"{STUDIO}/plugin/takes/{mine['id']}").status_code == 401
        )  # a user token is no pass
        assert (
            plug.get(f"{STUDIO}/takes/{mine['id']}").status_code == 401
        )  # nor is a pass a user token

        data = png_bytes(4, 6)
        body = {"parents": [mine["id"]], "tool": "echo", "input": {"text": word}, "title": "mine · rotated",
                "kind": "image", "content_type": "image/png", "bytes": len(data), "width": 4, "height": 6}  # fmt: skip
        assert (
            plug.post(f"{STUDIO}/plugin/takes", json=body | {"parents": [theirs["id"]]}).status_code
            == 404
        )
        up = ok(plug.post(f"{STUDIO}/plugin/takes", json=body), 201)
        assert (
            anon.put(
                up["upload_url"], content=data, headers={"Content-Type": "image/png"}
            ).status_code
            == 204
        )
        made = ok(plug.post(f"{STUDIO}/plugin/takes/{up['take_id']}/complete"))
        assert (
            made["source"] == "tool"
            and made["parents"] == [mine["id"]]
            and made["made_with"] is None
        )
        assert made["tool"] == {"plugin": "fake-tools", "tool": "echo", "input": {"text": word},
                                "call": call["call"]}  # fmt: skip  # the pass's call: Undo vouches by it
        assert ok(api.get(f"{STUDIO}/takes/{made['id']}"))["tool"]["plugin"] == "fake-tools"
        assert api_b.get(f"{STUDIO}/takes/{made['id']}").status_code == 404
        ok(api.delete(f"{STUDIO}/takes/{made['id']}"), 204)  # the line's Undo
        admin.put(f"{PLUGINS}/fake-tools", json={"enabled": False})
        assert (
            plug.get(f"{STUDIO}/plugin/takes/{mine['id']}").status_code == 401
        )  # off: the pass opens nothing
    ok(api.delete(f"{THREADS}/{tid}"), 204)
