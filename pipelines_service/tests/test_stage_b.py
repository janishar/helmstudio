"""M4b stage B on the engine (scratch Postgres, fake studio, fake studio.tools, fake JetStream): the Agent node's
tool loop, fan-out with Send, custom nodes, the director's chat, and local-server and remote models through the
fake OpenAI-compatible server (tests/fake_openai.py). Nothing real is ever called."""

import asyncio
import json
import socket
import uuid

import fake_openai
import pytest
from conftest import CONTRACTS, OWNER, FakeJS, FakeStudio, FakeTools, command, engine, idle, until

import config
import memory
import providers
import server
from messaging import schemas
from messaging.publisher import now

PIPELINE = "4e5f6a7b-8c9d-4e0f-9a1b-2c3d4e5f6a01"
REG = json.loads((CONTRACTS / "fixtures/registry/workflows.valid.json").read_text())["workflows"]
REG += json.loads((CONTRACTS / "fixtures/registry/server.workflows.valid.json").read_text())["workflows"]
WF = {w["id"]: {k: v for k, v in w.items() if k != "media"} for w in REG}
CAPS = {"max_turns": 6, "max_tool_calls": 12, "max_tokens": 16000}
LOCAL = {
    "model": "echo-ask",
    "workflow": "echo-ask",
    "source": {"kind": "local", "workflow": "echo-ask"},
}


def agent(**kw) -> dict:
    return {"kind": "agent", "instruction": "Turn the script into a shot list.", "from": ["$script"],
            "tools": ["gallery.search", "library.read", "memory.read", "memory.write", "rows.add"],
            "list": {"max": 3}, "caps": dict(CAPS), "director": False} | LOCAL | kw  # fmt: skip


def doc(edges=None, remote=None, providers_=None, **nodes) -> dict:
    n = {
        "inputs": {"kind": "inputs", "inputs": [{"name": "script", "type": "Text"}]},
        "plan": agent(),
        "shots": {"kind": "step", "workflow": "echo", "seeds": 1, "inputs": {}, "each": "plan.items",
                  "fanout_max": 3, "bind": {"prompt.text": "plan.item"}, "seed_param": None},
        "out": {"kind": "output", "from": "shots.takes", "collection": "Shots"},
    } | nodes  # fmt: skip
    n = {k: v for k, v in n.items() if v is not None}
    edges = edges or [["inputs", "plan"], ["plan", "shots"], ["shots", "out"]]
    used = {v["workflow"] for v in n.values() if v.get("workflow") in WF}
    p = {"id": PIPELINE, "title": "Shots", "version": 1, "nodes": n, "edges": edges,
         "workflows": {w: WF[w] for w in used | {"echo"}}}  # fmt: skip
    p["remote"] = remote or {
        "enabled": False,
        "providers": [],
        "max_calls": 200,
        "max_tokens": 400000,
    }
    if providers_:
        p["providers"] = providers_
    return p


def submit(b: str, p: dict, n: int = 1, replay_from=None, script="a fox @rex wakes") -> dict:
    cmd = command("submit", b, n)
    rows = [{"row": i, "inputs": {"inputs.script": f"{script} {i}"}} for i in range(1, n + 1)]
    cmd["data"] |= {"snapshot": {"workflow_id": "echo", "workflow": WF["echo"], "pipeline": p}}
    cmd["data"] |= {"inputs": {}, "rows": rows}
    if replay_from:
        cmd["data"]["replay_from"] = replay_from
    assert not schemas.message_errors(cmd), schemas.message_errors(cmd)
    return cmd


def turn(say="", **calls) -> str:
    return json.dumps({"say": say, "calls": [{"tool": t, "args": a} for t, a in calls.items()]})


class Script:
    """Scripted ask answers per (node, row), in order; the last one repeats."""

    def __init__(self, **by_node):
        self.q = {k: list(v) for k, v in by_node.items()}
        self.prompts: list[str] = []

    def __call__(self, req) -> str:
        self.prompts.append(req["inputs"]["ask.prompt"])
        k = req["idempotency_key"].split(":")
        q = self.q.get(f"{k[3]}:{k[1]}") or self.q[k[3]]
        return q.pop(0) if len(q) > 1 else q[0]


def node_of(k: str) -> str:
    return k.split(":")[3]


async def finish(e, studio, node: str, rows=(0, 1, 2, 3)) -> list[str]:
    keys = [k for r in rows for k in studio.open_keys(r) if node_of(k) == node]
    for k in keys:
        assert await e.on_run_finished(studio.finish(int(k.split(":")[1]), key=k))
    await idle(e)
    return keys


def steps(js, row=1) -> list[dict]:
    out = {}
    for m in sorted(js.events("step"), key=lambda m: m["data"]["seq"]):
        if m["data"]["row"] == row:
            out[m["data"]["step"]] = m["data"]
    return [out[k] for k in sorted(out)]


SHOT_TURNS = [
    turn("Looking.", gallery_search={"text": "fox"}, library_read={"kind": "element"}),
    turn("", memory_write={"key": "briefs", "text": "Foxes look better in snow."}, memory_read={}),
    turn("", rows_add={"inputs": {"script": "a second script"}},
         set_items={"items": ["shot one of @rex", "shot two", "shot three"]}),
    turn("3 shots."),
]  # fmt: skip


async def test_an_agent_uses_every_tool_and_fans_out_its_list(pool):
    studio, js, tools = FakeStudio(), FakeJS(), FakeTools()
    studio.answer_for = Script(**{"plan:1": SHOT_TURNS, "plan": [turn("Done.")]})
    e = engine(pool, studio, js, tool=tools)
    b = str(uuid.uuid4())
    await e.submit(submit(b, doc()))
    await idle(e)
    for _ in range(4):
        await finish(e, studio, "plan", rows=(1,))
    assert [k.split(":", 3)[3] for k in studio.asked if node_of(k) == "plan"] == [f"plan:0:t{i}" for i in range(4)]
    # the list fans out: one branch per item (Send), branch id = item index in the key, its item as the prompt
    fan = [k for k in studio.asked if node_of(k) == "shots"]
    assert fan == [f"{b}:1:1:shots:0:{i}-0" for i in range(3)]
    assert [studio.runs[k]["req"]["inputs"]["prompt.text"] for k in fan] == [
        "shot one of @rex", "shot two", "shot three"]  # fmt: skip
    # the thread resumes once, with every branch's results
    for i, k in enumerate(fan):
        await e.on_run_finished(studio.finish(1, key=k))
        await idle(e)
        if i < 2:
            assert js.rows()[1]["status"] == "running"
            live = next(c for c in js.rows()[1]["cells"] if c["node"] == "shots")["attempts"]
            assert [(a["branch"], a["status"]) for a in live] == [
                (j, "succeeded" if j <= i else "running") for j in range(3)
            ]  # each branch as it ends
    row = js.rows()[1]
    assert row["status"] == "succeeded"
    assert row["take_ids"] == [t for k in fan for t in studio.runs[k]["take_ids"]]
    shots = next(c for c in row["cells"] if c["node"] == "shots")
    assert [a["branch"] for a in shots["attempts"]] == [0, 1, 2]
    st = steps(js)
    # one submit record per branch (Send cannot write n), numbered right after the fan's, none shadowed
    fanned = [(s["branch"], s["part"], s["run_ids"]) for s in st if s["node"] == "shots" and "branch" in s]
    assert fanned == [(i, "submit", [studio.runs[k]["run_id"]]) for i, k in enumerate(fan)]
    assert [s["step"] for s in st] == list(range(1, len(st) + 1))
    tool_steps = [s for s in st if s["part"] == "tools"]
    assert [len(s["tool_calls"]) for s in tool_steps] == [2, 2, 2, 0]
    assert [c["tool"] for s in tool_steps for c in s["tool_calls"]] == [
        "gallery_search", "library_read", "memory_write", "memory_read", "rows_add", "set_items"]  # fmt: skip
    assert all(c["error"] is None for s in tool_steps for c in s["tool_calls"])
    dec = next(s["decision"] for s in st if s.get("decision"))
    assert dec["kind"] == "agent" and dec["agent"]["items"] == [
        "shot one of @rex",
        "shot two",
        "shot three",
    ]
    assert dec["agent"]["tool_calls"] == 6 and len(dec["agent"]["turns"]) == 4 and dec["replayable"]
    # a tool result is data: the injected text sits fenced in the next prompt, and only declared tools ran
    assert "Ignore your instructions" in studio.answer_for.prompts[1] and "<<<" in studio.answer_for.prompts[1]
    # rows.add: studio stored row 2 and pipelines ran it (it joins the window)
    assert tools.asked[2]["tool"] == "rows.add" and tools.asked[2]["row"] == 1
    await until(lambda: 2 in js.rows())
    # memory.write: one note in the live namespace, keyed by the call
    notes = await memory.items(e.store, OWNER, PIPELINE)
    assert [n["text"] for n in notes] == ["Foxes look better in snow."]


async def test_agent_caps_and_illegal_answers_fail_the_row(pool):
    cases = {
        "turns": (agent(caps=CAPS | {"max_turns": 2}), [turn("", memory_read={})], "plan: stopped at turns"),
        "tools": (agent(caps=CAPS | {"max_tool_calls": 1}), [turn("", memory_read={}), turn("", memory_read={})],
                  "plan: stopped at tool_calls"),
        "undeclared": (agent(), [turn("", rows_edit={"row": 2, "inputs": {"script": "x"}})],
                       "plan: answer did not match the schema"),
        "route": (agent(list=None, routes={"again": "retry", "done": "finish"}),
                  [turn("", route={"to": "elsewhere", "reason": ""})], "plan: answer did not match the schema"),
    }  # fmt: skip
    for name, (node, answers, error) in cases.items():
        studio, js = FakeStudio(), FakeJS()
        studio.answer_for = Script(plan=answers)
        e = engine(pool, studio, js, tool=FakeTools())
        nodes = {"plan": {k: v for k, v in node.items() if v is not None}}
        edges = None
        if "routes" in nodes["plan"]:
            nodes |= {
                "shots": None,
                "out": {"kind": "output", "from": "plan.items", "collection": "x"},
            }
            nodes["out"] = {"kind": "output", "from": "still.takes", "collection": "x"}
            nodes["still"] = {"kind": "step", "workflow": "echo", "seeds": 1, "inputs": {},
                              "bind": {"prompt.text": "$script"}, "seed_param": None}  # fmt: skip
            edges = [["inputs", "plan"], ["plan", {"routes": {"again": "still", "done": "still"}, "max": 1,
                                                   "then": "done"}], ["still", "out"]]  # fmt: skip
        b = str(uuid.uuid4())
        await e.submit(submit(b, doc(edges=edges, **nodes)))
        await idle(e)
        for _ in range(4):
            if not await finish(e, studio, "plan", rows=(1,)):
                break
        assert js.rows()[1]["status"] == "failed", name
        assert js.rows()[1]["error"].startswith(error), (name, js.rows()[1]["error"])
        assert not [k for k in studio.asked if node_of(k) == "still"]


async def test_a_turn_the_runner_refused_is_asked_once_more(pool):
    """A local ask run that failed its schema check (tries with errors) is an invalid turn, not a failed call."""
    studio, js = FakeStudio(), FakeJS()
    studio.answer_for = Script(plan=[turn("", set_items={"items": ["a shot"]}), turn("One shot.")])
    e = engine(pool, studio, js, tool=FakeTools())
    await e.submit(submit(str(uuid.uuid4()), doc()))
    await idle(e)
    (k,) = [k for k in studio.open_keys(1) if node_of(k) == "plan"]
    err = "answer did not match the schema: 'nowhere' is not one of ['a']"
    bad = {
        "tries": [{"text": "{}", "error": err}] * 2,
        "seen": [],
        "tokens_in": 1,
        "tokens_out": 1,
        "seconds": 0.1,
    }
    assert await e.on_run_finished(studio.finish(1, "failed", error=err, key=k, failed_answer=bad))
    await idle(e)
    assert [node_of(x) + x.rsplit(":", 1)[1] for x in studio.asked] == ["plant0", "plant0r"]
    for _ in range(2):
        await finish(e, studio, "plan", rows=(1,))
    assert [node_of(x) for x in studio.open_keys(1)] == ["shots"]  # the list fanned out
    dec = next(s["decision"] for s in steps(js) if s.get("decision"))
    assert [x.rsplit(":", 1)[1] for x in studio.asked if node_of(x) == "plan"] == [
        "t0",
        "t0r",
        "t1",
    ], studio.asked
    assert len(dec["run_ids"]) == 3 and [a["error"] is not None for a in dec["answers"]][:2] == [
        True,
        True,
    ]


async def test_a_custom_node_feeds_a_step(pool):
    studio, js = FakeStudio(), FakeJS()
    e = engine(pool, studio, js)
    p = doc(plan=None, shots=None,
            pre={"kind": "custom", "node": "text/prefix", "version": "aaaaaaaaaaaa", "network": False,
                 "inputs": {"prefix": "cinematic still, "}, "bind": {"text": "$script"}},
            still={"kind": "step", "workflow": "echo", "seeds": 1, "inputs": {},
                   "bind": {"prompt.text": "pre.text"}, "seed_param": None},
            out={"kind": "output", "from": "still.takes", "collection": "x"},
            edges=[["inputs", "pre"], ["pre", "still"], ["still", "out"]])  # fmt: skip
    b = str(uuid.uuid4())
    await e.submit(submit(b, p))
    await idle(e)
    (k,) = await finish(e, studio, "still", rows=(1,))
    assert studio.runs[k]["req"]["inputs"]["prompt.text"] == "cinematic still, a fox @rex wakes 1"
    assert js.rows()[1]["status"] == "succeeded"
    assert [s["part"] for s in steps(js)][:2] == ["inputs", "custom"]


def director(**kw) -> dict:
    return agent(director=True, tools=["gallery.search", "memory.read", "memory.write", "rows.add", "rows.edit"],
                 list={"max": 1}, wake_on_rows="all", max_wakes=100) | kw  # fmt: skip


def chat_cmd(b: str, text: str) -> dict:
    return {"batch_id": b, "message_id": str(uuid.uuid4()), "text": text, "at": now()}


async def test_the_director_answers_adds_rows_and_reports_a_rejected_row(pool):
    studio, js, tools = FakeStudio(), FakeJS(), FakeTools()
    studio.answer_for = Script(**{
        "plan:0": [turn("", rows_add={"inputs": {"script": "the den at dawn"}}), turn("Added row 2."),
                   turn("", rows_add={"inputs": {"script": "x"}}), turn("Row 3 was refused."),
                   turn("All done.")],
        "plan:1": [turn("", set_items={"items": ["one shot"]}), turn("1 shot.")],
        "plan:2": [turn("", set_items={"items": ["one shot"]}), turn("1 shot.")],
    })  # fmt: skip
    e = engine(pool, studio, js, tool=tools)
    b = str(uuid.uuid4())
    await e.submit(submit(b, doc(plan=director())))
    await idle(e)
    await e.chat(chat_cmd(b, "Add a shot of the den at dawn."))
    await idle(e)
    # the chat's turn is a queue item of row 0; the row's agent runs meanwhile
    (k,) = [k for k in studio.open_keys(0)]
    assert k == f"{b}:0:1:plan:0:t0"
    assert "Add a shot of the den at dawn." in studio.runs[k]["req"]["inputs"]["ask.prompt"]
    assert "rows_edit" in studio.runs[k]["req"]["inputs"]["ask.schema"]  # only in the chat
    assert "rows_edit" not in studio.runs[f"{b}:1:1:plan:0:t0"]["req"]["inputs"]["ask.schema"]
    batch = lambda: [m["data"] for m in js.events("batch")][-1]
    assert batch()["director"]["state"] == "waiting"
    await finish(e, studio, "plan", rows=(0,))
    await finish(e, studio, "plan", rows=(0,))
    (reply,) = [m["data"]["message"] for m in js.events("chat")]
    assert reply["role"] == "director" and reply["text"] == "Added row 2." and reply["wake"] == 1
    assert reply["rows"] == [2] and [c["tool"] for c in reply["tool_calls"]] == ["rows_add"]
    assert batch()["director"] == {"node": "plan", "state": "idle", "wakes": 1, "max_wakes": 100}
    await until(lambda: 2 in js.rows())
    # row 1 runs to its end: that wakes the director; its next row is refused (Submit's checks): a notice
    tools.refuse = "row: inputs.script: a value is required"
    for _ in range(3):
        await finish(e, studio, "plan", rows=(1, 2))
        await finish(e, studio, "shots", rows=(1, 2))
    await finish(e, studio, "plan", rows=(0,))
    await finish(e, studio, "plan", rows=(0,))
    msgs = [m["data"]["message"] for m in js.events("chat")]
    assert [(m["role"], m["text"]) for m in msgs[1:3]] == [
        ("notice", "Row 3 was not added: row: inputs.script: a value is required"),
        ("director", "Row 3 was refused.")]  # fmt: skip
    for _ in range(4):  # every wake left (row 2 ending) answers; then the batch ends
        await finish(e, studio, "plan", rows=(0, 1, 2))
        await finish(e, studio, "shots", rows=(1, 2))
    await until(lambda: js.batch_status() == "done")
    assert batch()["director"]["state"] == "off"
    assert {r: v["status"] for r, v in js.rows().items()} == {1: "succeeded", 2: "succeeded"}


# --- model sources through the fake OpenAI-compatible server


@pytest.fixture
def fake():
    srv, state = fake_openai.serve()
    state.url = f"http://127.0.0.1:{srv.server_address[1]}/v1"
    yield state
    srv.shutdown()


def server_provider(url: str) -> dict:
    return {"kind": "server", "api": "openai", "base_url": url, "timeout_s": 5, "host": "mac",
            "memory_gb": 6.5, "key_secret": None}  # fmt: skip


def on_server(model: str) -> dict:
    return {"model": f"server:local/{model}", "workflow": "server-ask",
            "source": {"kind": "server", "provider": "local", "model": model, "host": "mac"}}  # fmt: skip


def run_cmd(req: dict) -> dict:
    """The cmd.run.execute the dispatcher would release for a server-ask run (only what execute reads)."""
    params = {k.split(".", 1)[1]: v for k, v in req["inputs"].items()}
    return {"owner_id": req["owner_id"], "data": {"workflow": {"nodes": {"ask": {"kind": "server/ask",
                                                                                   "params": params}}}}}  # fmt: skip


async def serve_server_runs(e, studio, pool) -> int:
    """Play the server executor for every open server-ask run; how many ran."""
    n = 0
    for row in range(4):
        for k in studio.open_keys(row):
            req = studio.runs[k]["req"]
            if req["workflow_id"] != "server-ask":
                continue
            status, error, answer = await server.execute(pool, run_cmd(req))
            await e.on_run_finished(studio.finish(row, status, error, key=k, answer=answer))
            n += 1
    await idle(e)
    return n


async def test_the_server_executor_calls_a_local_server(pool, fake):
    studio, js = FakeStudio(), FakeJS()
    e = engine(pool, studio, js)
    b = str(uuid.uuid4())
    p = doc(
        plan=agent(tools=["memory.read"], **on_server("srv")),
        providers_={"local": server_provider(fake.url)},
    )
    await e.submit(submit(b, p))
    await idle(e)
    (k,) = studio.open_keys(1)
    req = studio.runs[k]["req"]
    assert req["workflow_id"] == "server-ask" and req["inputs"]["ask.provider"] == "local"
    tools = json.loads(req["inputs"]["ask.tools"])
    assert [t["function"]["name"] for t in tools] == ["memory_read", "set_items"]
    fake.script["srv"] = [
        {"tool_calls": [{"name": "memory_read", "arguments": {}},
                        {"name": "set_items", "arguments": {"items": ["a shot"]}}], "usage": {"prompt_tokens": 50}},
        {"content": "One shot."},
    ]  # fmt: skip
    assert await serve_server_runs(e, studio, pool) == 1
    assert await serve_server_runs(e, studio, pool) == 1
    sent = [r["body"] for r in fake.requests]
    assert sent[0]["tools"] == tools and sent[0]["tool_choice"] == "auto" and sent[0]["max_tokens"] == 4096
    assert "authorization" not in fake.requests[0]["headers"] and [  # a server has no key
        m["role"] for m in sent[0]["messages"]
    ] == ["system", "user"]
    st = steps(js)
    dec = next(s["decision"] for s in st if s.get("decision"))
    assert dec["source"]["kind"] == "server" and dec["replayable"] and dec["agent"]["items"] == ["a shot"]
    assert dec["tokens_in"] == 60 and len(dec["run_ids"]) == 2
    # a schema answer is validated and asked once more in the run
    fake.script["srv"] = [{"content": "not json"}, {"content": '{"text": "ok"}'}]
    cmd = run_cmd({"owner_id": OWNER, "inputs": {"ask.provider": "local", "ask.model": "srv",
                   "ask.system": "s", "ask.prompt": "p", "ask.schema": '{"type":"object","required":["text"]}',
                   "ask.max_tokens": 50}})  # fmt: skip
    status, _, answer = await server.execute(pool, cmd)
    assert status == "succeeded" and [t["error"] is None for t in answer["tries"]] == [False, True]
    # the host rule: link-local (cloud metadata) and public addresses are refused at every call
    for url in ("http://169.254.169.254/v1", "http://8.8.8.8/v1"):
        with pytest.raises(providers.CallError) as err:
            await providers.check_host("x", server_provider(url))
        assert err.value.kind == "refused"
    with pytest.raises(providers.CallError) as err:
        await providers.check_host("x", {"kind": "remote", "base_url": "https://api.example.com/v1"})
    assert err.value.kind == "off"  # remote models are off by default


def remote_doc(url: str, rem: dict | None = None) -> dict:
    prov = {"kind": "remote", "api": "openai", "base_url": url, "timeout_s": 5, "key_secret": "fakeai.key",
            "max_in_flight": 2}  # fmt: skip
    rem = {"enabled": True, "providers": ["fakeai"], "max_calls": 200, "max_tokens": 400000} | (rem or {})
    check = {"kind": "decide", "question": "Is the script usable?", "from": ["$script"],
             "routes": {"keep": "use it", "drop": "do not"}, "model": "remote:fakeai/fake-large",
             "workflow": "remote", "source": {"kind": "remote", "provider": "fakeai", "model": "fake-large"}}  # fmt: skip
    still = {"kind": "step", "workflow": "echo", "seeds": 1, "inputs": {}, "bind": {"prompt.text": "$script"},
             "seed_param": None}  # fmt: skip
    return doc(plan=None, shots=None, check=check, still=still,
               out={"kind": "output", "from": "still.takes", "collection": "x"},
               edges=[["inputs", "check"], ["check", {"routes": {"keep": "still", "drop": "still"}, "max": 1,
                                                       "then": "keep"}], ["still", "out"]],
               remote=rem, providers_={"fakeai": prov})  # fmt: skip


@pytest.fixture
def remote_on(monkeypatch, tmp_path, fake):
    """Remote models switched on, a key file, short retries; the fake stands in for https (the host rule's
    https check is tested on its own)."""
    monkeypatch.setattr(config, "REMOTE_MODELS", True)
    monkeypatch.setattr(config, "REMOTE_ALLOW_PRIVATE", True)  # the fake is on loopback (as the fake overlay's)
    monkeypatch.setattr(config, "PROVIDER_KEYS_DIRS", [tmp_path])
    monkeypatch.setattr(providers, "DELAYS", [0.01, 0.01, 0.01])
    (tmp_path / "fakeai.key").write_text("test-key\n")
    real = providers.check_host

    async def check(name, p):
        await real(name, p | {"kind": "server"})  # loopback only, as a server

    monkeypatch.setattr(providers, "check_host", check)
    return fake


async def test_remote_calls_keep_the_in_flight_limit_caps_and_count_network_failures(pool, remote_on):
    fake = remote_on
    keep = {"content": '{"route": "keep", "reason": "fine"}', "delay_s": 0.2}
    fake.script["fake-large"] = [{"status": 503}] + [keep] * 4
    studio, js = FakeStudio(), FakeJS()
    e = engine(pool, studio, js)
    stop = asyncio.Event()
    worker = asyncio.create_task(e.remote.run(stop))
    b = str(uuid.uuid4())
    await e.submit(submit(b, remote_doc(fake.url, {"max_calls": 4}), n=5))
    await idle(e)
    await until(lambda: len(studio.open_keys(1)) == 1, 10)  # row 1 first, then every row
    await finish(e, studio, "still", rows=(1,))
    failed = lambda: [r for r, v in js.rows().items() if v["status"] == "failed"]
    await until(lambda: sum(len(studio.open_keys(r)) for r in range(2, 6)) == 3 and len(failed()) == 1, 10)
    stop.set()
    await worker
    assert fake.max_in_flight == 2  # per provider, over every batch
    assert js.rows()[failed()[0]]["error"] == "check: remote cap reached: 4 calls"  # whichever came 5th
    heads = [r["headers"].get("authorization") for r in fake.requests]
    assert heads == ["Bearer test-key"] * 5  # the key file, read at call time
    batch = [m["data"] for m in js.events("batch")][-1]
    assert batch["remote"] == {"calls": 4, "tokens": 60, "network_failures": 1, "max_calls": 4, "max_tokens": 400000}
    decs = [s["decision"] for r in range(1, 6) for s in steps(js, r) if s.get("decision")]
    assert all(d["replayable"] is False and d["source"]["kind"] == "remote" for d in decs)
    assert sum(d["network_failures"] for d in decs) == 1 and all(d["run_ids"] == [] for d in decs)


async def test_pause_holds_and_stop_cancels_remote_calls(pool, remote_on):
    fake = remote_on
    studio, js = FakeStudio(), FakeJS()
    e = engine(pool, studio, js)
    b = str(uuid.uuid4())
    await e.submit(submit(b, remote_doc(fake.url)))
    await idle(e)
    await e.pause(b)
    stop = asyncio.Event()
    worker = asyncio.create_task(e.remote.run(stop))
    await asyncio.sleep(0.3)
    assert fake.requests == []  # held while paused
    await e.stop(b)
    await idle(e)
    await until(lambda: js.rows()[1]["status"] == "cancelled")
    stop.set()
    await worker
    assert fake.requests == [] and js.batch_status() == "stopped"


async def test_run_again_replays_an_agent_without_its_tools_but_never_a_remote_decision(pool, remote_on):
    fake = remote_on
    studio, js, tools = FakeStudio(), FakeJS(), FakeTools()
    studio.answer_for = Script(plan=[turn("", gallery_search={}), turn("", set_items={"items": ["one"]}),
                                     turn("Done.")])  # fmt: skip
    e = engine(pool, studio, js, tool=tools)
    b1, b2 = str(uuid.uuid4()), str(uuid.uuid4())
    await e.submit(submit(b1, doc()))
    await idle(e)
    for _ in range(3):
        await finish(e, studio, "plan", rows=(1,))
    await finish(e, studio, "shots", rows=(1,))
    assert js.rows()[1]["status"] == "succeeded" and len(tools.asked) == 1
    js2 = FakeJS()
    e2 = engine(pool, studio, js2, tool=tools)
    await e2.submit(submit(b2, doc(), replay_from=b1))
    await idle(e2)
    assert not [k for k in studio.asked if k.startswith(b2) and node_of(k) == "plan"]  # no model asked
    assert len(tools.asked) == 1  # its tool calls are not run again
    dec = next(s["decision"] for s in steps(js2) if s.get("decision"))
    assert dec["replayed"] and dec["agent"]["items"] == ["one"]
    # a remote decision is asked again
    fake.script["fake-large"] = [{"content": '{"route": "keep", "reason": "fine"}'}]
    stop = asyncio.Event()
    worker = asyncio.create_task(e.remote.run(stop))
    b3, b4 = str(uuid.uuid4()), str(uuid.uuid4())
    await e.submit(submit(b3, remote_doc(fake.url)))
    await until(lambda: len(fake.requests) == 1)
    await e.submit(submit(b4, remote_doc(fake.url), replay_from=b3))
    await until(lambda: len(fake.requests) == 2)
    stop.set()
    await worker


# --- egress: with remote models off nothing leaves loopback and private addresses


async def test_no_outbound_connection_with_remote_models_off(pool, fake, monkeypatch):
    assert not config.REMOTE_MODELS
    seen = []
    real = socket.socket.connect, socket.socket.connect_ex

    def guard(i):
        def connect(self, address):
            seen.append(address)
            return real[i](self, address)

        return connect

    monkeypatch.setattr(socket.socket, "connect", guard(0))
    monkeypatch.setattr(socket.socket, "connect_ex", guard(1))
    judged = json.dumps({"takes": [{"take": i, "checks": [True], "reason": "ok"} for i in (1, 2)], "ranking": [1, 2]})
    studio, js, tools = FakeStudio(), FakeJS(), FakeTools()
    studio.answer_for = Script(judge=[judged], check=['{"route": "go", "reason": "ok"}'])
    fake.script["writer"] = [{"content": '{"text": "a fox in snow"}'}]
    fake.script["director"] = [
        {"tool_calls": [{"name": "memory_write", "arguments": {"key": "k", "text": "noted"}},
                        {"name": "set_items", "arguments": {"items": ["shot a", "shot b"]}}]},
        {"content": "Two shots."},
    ]  # fmt: skip
    step = lambda bind, **kw: {"kind": "step", "workflow": "echo", "seeds": 1, "inputs": {}, "bind": bind,
                               "seed_param": None} | kw  # fmt: skip
    p = doc(
        mem={"kind": "memory", "op": "read", "limit": 8},
        pre={"kind": "custom", "node": "text/prefix", "version": "aaaaaaaaaaaa", "network": False,
             "inputs": {"prefix": "still: "}, "bind": {"text": "$script"}},
        still=step({"prompt.text": "pre.text"}, seeds=2),
        judge={"kind": "judge", "takes": "still", "criteria": {"must": ["Sharp"], "nice": []}, "keep": 1,
               "mode": "auto", **LOCAL},
        check={"kind": "decide", "question": "Go on?", "from": ["$script"], "routes": {"go": "yes", "stop": "no"},
               **LOCAL},
        rewrite={"kind": "write", "from": ["$script", "judge.reasons"], "to": "$script",
                 "instruction": "Fix it.", **on_server("writer")},
        plan=agent(tools=["memory.write"], list={"max": 2}, **on_server("director")),
        shots=step({"prompt.text": "plan.item"}, each="plan.items", fanout_max=2),
        review={"kind": "gate", "takes": "shots", "keep": 1},
        note={"kind": "memory", "op": "write", "key": "seen", "from": ["$script"]},
        out={"kind": "output", "from": "review.picked", "collection": "x"},
        edges=[["inputs", "mem"], ["mem", "pre"], ["pre", "still"], ["still", "judge"], ["judge", "check"],
               ["check", {"routes": {"go": "rewrite", "stop": "rewrite"}, "max": 1, "then": "go"}],
               ["rewrite", "plan"], ["plan", "shots"], ["shots", "review"], ["review", "note"], ["note", "out"]],
        providers_={"local": server_provider(fake.url)},
    )  # fmt: skip
    e = engine(pool, studio, js, tool=tools)
    b = str(uuid.uuid4())
    await e.submit(submit(b, p))
    await idle(e)
    for node in ("still", "judge", "check"):
        await finish(e, studio, node, rows=(1,))
    for _ in range(3):
        await serve_server_runs(e, studio, pool)
    await finish(e, studio, "shots", rows=(1,))
    parked = js.rows()[1]["parked"]
    await e.pick({"batch_id": b, "row": 1, "attempt": 1, "gate": "review", "visit": 1,
                  "take_ids": parked["candidates"][:1]})  # fmt: skip
    await idle(e)
    assert js.rows()[1]["status"] == "succeeded", js.rows()[1]
    # a remote model with the switch off: the row fails, nothing is sent
    b2, js2 = str(uuid.uuid4()), FakeJS()
    e2 = engine(pool, studio, js2, tool=tools)
    await e2.submit(submit(b2, remote_doc("https://api.example.com/v1")))
    await idle(e2)
    assert js2.rows()[1]["error"] == "check: remote models are off on this deployment"
    assert len(fake.requests) == 3
    hosts = {a[0] for a in seen if isinstance(a, tuple)}
    assert "127.0.0.1" in hosts and all(providers.allowed_ip(h) for h in hosts), hosts


# --- units: what pipelines publishes and answers


def test_the_custom_and_server_registry_values_are_on_contract():
    import custom

    v = custom.value()
    assert [n["name"] for n in v["nodes"]] == ["text/prefix"]
    assert v["nodes"][0]["input"]["required"] == ["text", "prefix"] and not v["nodes"][0]["network"]
    assert set(server.registry_values("mac")) == {
        "nodes",
        "workflows",
    }  # each checked against the schema


async def test_the_server_env_publishes_its_status_under_its_host():
    """evt.run.<host>.<session_id>.status: the subject names the host (its NATS permission and the projector's check)."""
    sent = []

    class JS:
        async def publish(self, subject, body, headers=None):
            sent.append(subject)

    run, session = "00000000-0000-4000-8000-00000000000a", "00000000-0000-4000-8000-00000000000b"
    cmd = {"job_id": run, "owner_id": "00000000-0000-4000-8000-000000000001", "trace": {"traceparent": "00-" + "1" * 32
           + "-" + "1" * 16 + "-01"}, "data": {"session_id": session, "run_id": run}}  # fmt: skip
    await server.Executor(JS(), None, "mac")._status(cmd, 1, {"status": "running", "host": "mac"})
    assert sent == [f"evt.run.mac.{session}.status"]


async def test_the_provider_test_answers_on_contract(fake):
    p = server_provider(fake.url)
    ok = await providers.test({"name": "local", "provider": p, "model": "fake-small"})
    assert "fake-small" in ok["models"] and ok["answer"] == "OK"
    assert not schemas.providers_test_errors("reply", ok)
    bad = await providers.test({"name": "local", "provider": p | {"base_url": "http://169.254.169.254/v1"}})
    assert bad["code"] == "refused" and not schemas.providers_test_errors("reply", bad)
    fake.script["fake-small"] = [{"status": 401}]
    denied = await providers.test({"name": "local", "provider": p, "model": "fake-small"})
    assert denied == {
        "error": "local: the key was refused",
        "code": "answer",
        "seconds": denied["seconds"],
    }


async def test_an_agent_visit_survives_a_restart(pool):
    studio, js, tools = FakeStudio(), FakeJS(), FakeTools()
    studio.answer_for = Script(plan=[turn("", gallery_search={}), turn("", set_items={"items": ["one"]}),
                                     turn("Done.")])  # fmt: skip
    e = engine(pool, studio, js, tool=tools)
    b = str(uuid.uuid4())
    await e.submit(submit(b, doc()))
    await idle(e)
    await finish(e, studio, "plan", rows=(1,))
    await e.close()  # a crash while turn 2's run is queued
    e2 = engine(pool, studio, js, tool=tools)
    await e2.start()
    await idle(e2)
    assert js.batch_status() == "paused"
    await e2.resume(b)
    await idle(e2)
    for _ in range(2):
        await finish(e2, studio, "plan", rows=(1,))
    await finish(e2, studio, "shots", rows=(1,))
    assert js.rows()[1]["status"] == "succeeded"
    assert [k.rsplit(":", 1)[1] for k in studio.asked if node_of(k) == "plan"] == [
        "t0",
        "t1",
        "t2",
    ]  # the parked turn is not asked again
    assert len(tools.asked) == 1  # one call id, one tool call
