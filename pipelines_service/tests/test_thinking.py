"""Plan 28 (API 1.53), thinking per model on pipelines' side: the snapshot's fragment merged into the body and its
tokens added to max_tokens, the one retry without the fragment on a 400 (remembered), nothing new without
`thinking`, and Test reading an OpenRouter-shaped `reasoning` object. Nothing real is ever called."""

import json

import fake_openai
import pytest

import providers
from messaging import schemas

OFF = {"level": "off", "body": {"thinking": {"type": "disabled"}}, "tokens": 0}
HIGH = {
    "level": "high",
    "body": {"thinking": {"type": "enabled"}, "reasoning_effort": "high"},
    "tokens": 8192,
}


@pytest.fixture
def fake():
    srv, state = fake_openai.serve()
    state.url = f"http://127.0.0.1:{srv.server_address[1]}/v1"
    providers._NO_THINKING.clear()
    providers._NO_JSON_SCHEMA.clear()
    yield state
    srv.shutdown()


def server(url: str, **kw) -> dict:
    return {"kind": "server", "api": "openai", "base_url": url, "timeout_s": 5, "host": "mac", "memory_gb": 1,
            "key_secret": None} | kw  # fmt: skip


REQ = {"model": "m", "system": "s", "prompt": "p", "max_tokens": 24}


async def test_the_fragment_is_merged_and_the_tokens_added(fake):
    p = server(fake.url, thinking={"m": HIGH, "other": OFF})
    assert not schemas.providers_test_errors("request", {"name": "ds", "provider": p})
    await providers.chat("ds", p, REQ)
    body = fake.requests[-1]["body"]
    assert body["thinking"] == {"type": "enabled"} and body["reasoning_effort"] == "high"
    assert body["max_tokens"] == 24 + 8192
    await providers.chat("ds", p, REQ | {"model": "other"})
    body = fake.requests[-1]["body"]
    assert body["thinking"] == {"type": "disabled"} and body["max_tokens"] == 24


async def test_without_thinking_nothing_new_is_sent(fake):
    await providers.chat("ds", server(fake.url), REQ)
    body = fake.requests[-1]["body"]
    assert "thinking" not in body and "reasoning_effort" not in body and body["max_tokens"] == 24
    # a default's room only: body {} sends nothing, the tokens still grow
    await providers.chat("ds", server(fake.url, thinking={"m": HIGH | {"body": {}}}), REQ)
    body = fake.requests[-1]["body"]
    assert "thinking" not in body and body["max_tokens"] == 24 + 8192


async def test_a_400_with_a_fragment_is_asked_once_without_it_and_remembered(fake, caplog):
    p = server(fake.url, thinking={"m": HIGH})
    fake.script["m"] = [{"status": 400}, {"content": "titled"}]
    got = await providers.chat("ds", p, REQ)
    assert got["text"] == "titled"
    first, second = (r["body"] for r in fake.requests[-2:])
    assert "thinking" in first and "thinking" not in second and "reasoning_effort" not in second
    assert second["max_tokens"] == 24 + 8192  # the room stays
    assert ("ds", "m") in providers._NO_THINKING
    assert "thinking setting refused, sent without it" in caplog.text
    n = len(fake.requests)
    await providers.chat("ds", p, REQ)  # remembered: sent without it at once
    assert len(fake.requests) == n + 1 and "thinking" not in fake.requests[-1]["body"]
    # a 400 without a fragment is the answer failure it always was, asked once
    fake.script["m"] = [{"status": 400}]
    with pytest.raises(providers.CallError) as e:
        await providers.chat("ds", p, REQ)
    assert e.value.kind == "answer" and len(fake.requests) == n + 2


async def test_json_schema_falls_back_first_and_keeps_the_fragment(fake):
    schema = json.dumps({"type": "object", "properties": {"title": {"type": "string"}}})
    p = server(fake.url, thinking={"m": OFF})
    fake.script["m"] = [{"status": 400}, {"content": '{"title": "a b"}'}]
    await providers.chat("ds", p, REQ | {"schema": schema})
    last = fake.requests[-1]["body"]
    assert last["response_format"]["type"] == "json_object" and last["thinking"] == {
        "type": "disabled"
    }
    assert ("ds", "m") not in providers._NO_THINKING


def test_a_remote_client_sends_the_fragment_in_the_body():
    p = {
        "kind": "remote",
        "api": "openai",
        "base_url": "https://x.test/v1",
        "timeout_s": 5,
        "key_secret": None,
    }
    llm = providers._client("ds", p, "m", 100, None, HIGH["body"])
    assert llm.max_tokens == 100 and llm.extra_body == HIGH["body"]
    assert providers._client("ds", p, "m", 100, None).extra_body is None


async def test_test_reads_an_openrouter_reasoning_object(fake):
    fake.script["__models"] = [{"data": [
        {"id": "anthropic/claude-sonnet-5.5", "reasoning": {"supported_efforts": ["low", "medium", "high", "max"],
                                                            "default_effort": "medium", "mandatory": True}},
        {"id": "openai/gpt-6.2-sol", "reasoning": {"supported_efforts": ["none", "low", "high", "BAD", 3]}},
        {"id": "meta/llama-4", "reasoning": "yes"},
        {"id": "plain"},
    ]}]  # fmt: skip
    got = await providers.test({"name": "openrouter", "provider": server(fake.url)})
    assert got["models"] == [
        "anthropic/claude-sonnet-5.5",
        "openai/gpt-6.2-sol",
        "meta/llama-4",
        "plain",
    ]
    assert got["reasoning"] == {
        "anthropic/claude-sonnet-5.5": {
            "efforts": ["low", "medium", "high", "max"],
            "mandatory": True,
        },
        "openai/gpt-6.2-sol": {"efforts": ["none", "low", "high"], "mandatory": False},
    }
    assert not schemas.providers_test_errors("reply", got)
    plain = await providers.test({"name": "local", "provider": server(fake.url)})
    assert "reasoning" not in plain


async def test_a_400_that_is_not_the_fragment_is_not_remembered(fake):
    # review: a 400 for another reason (a prompt too long) fails both ways; the setting (Off) must not be dropped for good
    p = server(fake.url, thinking={"m": OFF | {"body": {"thinking": {"type": "disabled"}}}})
    fake.script["m"] = [{"status": 400}, {"status": 400}]
    with pytest.raises(providers.CallError):
        await providers.chat("ds", p, REQ)
    assert ("ds", "m") not in providers._NO_THINKING
    await providers.chat("ds", p, REQ)
    assert fake.requests[-1]["body"]["thinking"] == {"type": "disabled"}
