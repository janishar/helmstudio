"""Plan 25 (API 1.51), model provider presets on pipelines' side: the key file status (stat only, several folders),
GET /models cut at 1000, a preset's `compat` (no temperature, strict schemas, no json_object retry), a server's
optional key, and the host rule on a preset's snapshot. Nothing real is ever called (tests/fake_openai.py)."""

import builtins
import json
import pathlib

import fake_openai
import pytest
from conftest import CONTRACTS

import config
import providers
from messaging import schemas

PRESETS = {p["id"]: p for p in json.loads((CONTRACTS / "providers/presets.json").read_text())["presets"]}


@pytest.fixture
def fake():
    srv, state = fake_openai.serve()
    state.url = f"http://127.0.0.1:{srv.server_address[1]}/v1"
    yield state
    srv.shutdown()


def server(url: str, **kw) -> dict:
    return {"kind": "server", "api": "openai", "base_url": url, "timeout_s": 5, "host": "mac", "memory_gb": 1,
            "key_secret": None} | kw  # fmt: skip


@pytest.fixture
def keys(monkeypatch, tmp_path):
    a, b = tmp_path / "a", tmp_path / "b"
    a.mkdir(), b.mkdir()
    monkeypatch.setattr(config, "PROVIDER_KEYS_DIRS", [a, b])
    return a, b


def test_key_status_is_stat_only_over_every_folder(keys, monkeypatch):
    a, b = keys
    (a / "deepseek").write_text("sk-test\n")
    (b / "fake-openai").write_text("placeholder")
    (a / "empty").write_text("")
    (a / "folder").mkdir()

    schemas.providers_key_errors("reply", {"found": True})  # the schemas load before open is barred

    def never(*_a, **_kw):
        raise AssertionError("the key file was opened")

    monkeypatch.setattr(builtins, "open", never)
    monkeypatch.setattr(pathlib.Path, "open", never)
    monkeypatch.setattr(pathlib.Path, "read_text", never)
    monkeypatch.setattr(pathlib.Path, "read_bytes", never)
    found = {n: providers.key_found(n) for n in ("deepseek", "fake-openai", "empty", "folder", "nope")}
    assert found == {
        "deepseek": True,
        "fake-openai": True,
        "empty": False,
        "folder": False,
        "nope": False,
    }
    for body, want in (
        (b'{"key_secret": "deepseek"}', {"found": True, "writable": True}),
        (b'{"key_secret": "nope"}', {"found": False, "writable": True}),
    ):
        got = providers.answer_key(body)
        assert got == want and not schemas.providers_key_errors("reply", got)
    bad = providers.answer_key(b'{"key_secret": "../a/deepseek"}')
    assert bad["code"] == "internal" and "sk-" not in json.dumps(bad)
    assert not schemas.providers_key_errors("reply", bad)


def test_the_key_is_read_from_the_first_folder_that_has_it(keys):
    _, b = keys
    (b / "groq").write_text("from-b\n")
    assert providers.api_key("groq", {"kind": "remote", "key_secret": "groq"}) == "from-b"
    with pytest.raises(providers.CallError, match="the key file nope is missing"):
        providers.api_key("x", {"kind": "remote", "key_secret": "nope"})


async def test_models_are_cut_at_1000_not_64(fake):
    for i in range(1100):
        fake.script[f"m-{i:04d}"] = []
    got = await providers.test({"name": "local", "provider": server(fake.url)})
    assert len(got["models"]) == 1000 and not schemas.providers_test_errors("reply", got)


async def test_compat_sends_no_temperature_and_a_strict_schema_without_a_retry(fake):
    schema = json.dumps({"type": "object", "properties": {"text": {"type": "string"}}, "required": ["text"]})
    req = {"model": "m", "system": "s", "prompt": "p", "max_tokens": 8, "schema": schema}
    p = server(fake.url, compat=PRESETS["anthropic"]["compat"])
    fake.script["m"] = [{"content": '{"text": "hi"}'}]
    await providers.chat("claude", p, req)
    body = fake.requests[-1]["body"]
    assert "temperature" not in body and body["response_format"]["json_schema"]["strict"] is True
    fake.script["m"] = [{"status": 400}, {"content": '{"text": "late"}'}]
    with pytest.raises(providers.CallError):
        await providers.chat("claude", p, req)
    assert fake.requests[-1]["body"]["response_format"]["type"] == "json_schema"  # no json_object retry
    assert "claude" not in providers._NO_JSON_SCHEMA
    await providers.chat("plain", server(fake.url), req | {"schema": None})  # without compat: as before
    assert fake.requests[-1]["body"]["temperature"] == 0


async def test_a_server_may_send_a_key(fake, keys):
    a, _ = keys
    (a / "llama-server").write_text("local-key\n")
    await providers.test({"name": "llama", "provider": server(fake.url, key_secret="llama-server")})
    assert fake.requests[-1]["headers"]["authorization"] == "Bearer local-key"
    await providers.test({"name": "llama", "provider": server(fake.url)})
    assert "authorization" not in fake.requests[-1]["headers"]


async def test_the_host_rule_holds_for_a_preset_snapshot(monkeypatch):
    def snap(pid: str) -> dict:
        pre = PRESETS[pid]
        return {"kind": pre["kind"], "api": "openai", "base_url": pre["base_url"], "timeout_s": 15,
                "key_secret": pre.get("key_secret")}  # fmt: skip

    monkeypatch.setattr(config, "REMOTE_MODELS", False)
    off = await providers.test({"name": "deepseek", "provider": snap("deepseek")})
    assert off["code"] == "off" and off["error"] == "remote models are off on this deployment"
    meta = snap("ollama") | {"base_url": "http://169.254.169.254:11434/v1"}
    assert (await providers.test({"name": "ollama", "provider": meta}))["code"] == "refused"


async def test_compat_headers_go_on_every_call(fake):
    """API 1.55: Anthropic's anthropic-version on chat, Test (/models) and the key check; none without compat."""
    p = server(fake.url, compat=PRESETS["anthropic"]["compat"])
    fake.script["m"] = [{"content": "hi"}]
    await providers.chat("claude", p, {"model": "m", "system": "s", "prompt": "p", "max_tokens": 8})
    await providers.test({"name": "claude", "provider": p})
    assert await providers.check_key("claude", p, "sk-test") == ("ok", "")
    sent = [r["headers"] for r in fake.requests[-3:]]
    assert [h.get("anthropic-version") for h in sent] == ["2023-06-01"] * 3
    await providers.test({"name": "plain", "provider": server(fake.url)})
    assert "anthropic-version" not in fake.requests[-1]["headers"]
