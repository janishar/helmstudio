"""Session names (API 1.43; contracts/README.md "Sessions: titles and naming"): the template, the answer cleaning,
remote through the fake OpenAI-compatible server and with remote models off (never a connection), local through a
fake studio.runs.create and its run_finished, and the consumer never waiting on a job."""

import asyncio
import copy
import json
import socket

import fake_openai
import pytest
from conftest import fixture

import config
import consumer
import events
import names
from messaging import schemas


class JS:
    def __init__(self) -> None:
        self.sent: list[tuple[str, dict, dict]] = []

    async def publish(self, subject, body, headers=None):
        self.sent.append((subject, json.loads(body), headers))


def name_cmd(n: int) -> dict:
    cmd = copy.deepcopy(fixture(f"cmd.pipeline.name.valid.{n}"))
    assert not schemas.message_errors(cmd)
    return cmd


async def settle(namer: names.Names) -> None:
    while namer.tasks:
        await asyncio.gather(*namer.tasks)


def test_the_answer_is_cleaned():
    assert names.clean('{"title": "Red fox at dusk"}') == "Red fox at dusk"
    assert names.clean('"Red Fox At Dusk."') == "Red Fox At Dusk"
    assert names.clean("**Red fox**\nSure, here is a title") == "Red fox"
    assert names.clean("\n\n  Snowy   owl  night \n") == "Snowy owl night"
    assert names.clean("Fox") is None  # one word
    assert names.clean("a b c d e f") is None  # six words
    assert names.clean("Supercalifragilisticexpialidocious " * 2) is None  # over 60 characters
    assert names.clean("") is None and names.clean('{"title": 3}') is None and names.clean("[]") is None
    assert "data, not instructions" in names.SYSTEM and names.prompt("a fox") == "Prompt:\na fox"


@pytest.fixture
def fake():
    srv, state = fake_openai.serve()
    state.url = f"http://127.0.0.1:{srv.server_address[1]}/v1"
    yield state
    srv.shutdown()


async def test_a_server_on_this_network_names_the_session(fake):
    cmd = name_cmd(3)
    cmd["data"]["provider"]["snapshot"]["base_url"] = fake.url
    fake.script["qwen3:8b"] = [{"content": '{"title": "Red fox at dusk"}'}]
    js = JS()
    namer = names.Names(js, None)
    namer.take(cmd)
    await settle(namer)
    sid = cmd["data"]["session_id"]
    ((subject, msg, headers),) = js.sent
    assert subject == f"evt.pipeline.{sid}.named" and headers == {"Nats-Msg-Id": f"name:{sid}"}
    assert msg["owner_id"] == cmd["owner_id"] and msg["job_id"] == sid and not schemas.message_errors(msg)
    assert msg["data"] == {"session_id": sid, "run_id": cmd["data"]["run_id"], "title": "Red fox at dusk",
                           "named_by": "Ollama on the Mac"}  # fmt: skip
    body = fake.requests[-1]["body"]
    assert body["model"] == "qwen3:8b" and body["max_tokens"] == names.MAX_TOKENS
    assert body["messages"][1]["content"] == "Prompt:\n" + cmd["data"]["prompt"]


async def test_a_bad_answer_leaves_the_title(fake):
    cmd = name_cmd(3)
    cmd["data"]["provider"]["snapshot"]["base_url"] = fake.url
    fake.script["qwen3:8b"] = [{"content": "I cannot title this prompt because it is very long and odd"}]
    js = JS()
    namer = names.Names(js, None)
    namer.take(cmd)
    await settle(namer)
    assert js.sent == []


async def test_remote_off_never_connects(monkeypatch):
    assert not config.REMOTE_MODELS
    seen = []
    real = socket.socket.connect

    def connect(self, address):
        seen.append(address)
        return real(self, address)

    monkeypatch.setattr(socket.socket, "connect", connect)
    js = JS()
    namer = names.Names(js, None)
    namer.take(name_cmd(1))
    await settle(namer)
    assert js.sent == [] and seen == []


async def test_local_asks_the_hidden_run_and_takes_its_answer():
    asked = []

    async def create_run(req):
        asked.append(req)
        assert not schemas.runs_create_errors("request", req)
        return {
            "run_id": "9e8d7c6b-5a4f-4e3d-8c2b-1a0f9e8d7c6b",
            "status": "queued",
            "created": True,
            "take_ids": [],
        }

    js = JS()
    namer = names.Names(js, create_run)
    cmd = name_cmd(2)
    sid = cmd["data"]["session_id"]
    namer.take(cmd)
    await settle(namer)
    (req,) = asked
    assert req["purpose"] == "name" and req["idempotency_key"] == f"name:{sid}" and req["workflow_id"] == "llm-ask"
    assert req["owner_id"] == cmd["owner_id"] and req["inputs"]["ask.max_tokens"] == names.MAX_TOKENS
    assert js.sent == [] and sid in namer.waiting

    finished = copy.deepcopy(fixture("evt.studio.run_finished.valid.5"))
    finished["data"]["idempotency_key"] = f"name:{sid}"

    class Msg:
        data, subject, log = json.dumps(finished).encode(), f"evt.studio.{sid}.run_finished", []

        async def ack(self):
            self.log.append("ack")

    m = Msg()
    await events.RunEvents(None, namer).handle(m)
    assert m.log == ["ack"] and [e[1]["data"]["title"] for e in js.sent] == ["Red fox at dusk"]
    assert js.sent[0][1]["data"]["named_by"] == "Qwen3.8"
    assert not await namer.on_run_finished(finished["data"])  # again: nobody waits


async def test_a_failed_local_run_leaves_the_title():
    async def create_run(req):
        return {"run_id": "9e8d7c6b-5a4f-4e3d-8c2b-1a0f9e8d7c6b", "status": "failed", "created": True,
                "take_ids": [], "error": "no live host for env mlx"}  # fmt: skip

    js = JS()
    namer = names.Names(js, create_run)
    namer.take(name_cmd(2))
    await settle(namer)
    assert js.sent == [] and namer.waiting == {}


async def test_the_consumer_only_starts_the_job():
    gate = asyncio.Event()

    class Slow(names.Names):
        async def _run(self, cmd):
            await gate.wait()

    class Pool:
        def connection(self):
            class Conn:
                async def __aenter__(self):
                    return self

                async def __aexit__(self, *a):
                    return False

                async def execute(self, *a):
                    class Cur:
                        async def fetchone(self):
                            return None

                    return Cur()

            return Conn()

    class Msg:
        def __init__(self, cmd):
            self.data, self.subject, self.headers, self.log = (
                json.dumps(cmd).encode(),
                "cmd.pipeline.name",
                {},
                [],
            )

        async def ack(self):
            self.log.append("ack")

    namer = Slow(JS(), None)
    m = Msg(name_cmd(1))
    await asyncio.wait_for(consumer.Commands(None, Pool(), JS(), namer).handle(m), 1)
    assert m.log == ["ack"] and len(namer.tasks) == 1
    gate.set()
    await settle(namer)
