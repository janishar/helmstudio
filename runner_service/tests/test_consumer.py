"""A run end to end on fakes: the events it publishes (order, seq, Nats-Msg-Id, contract), the PNG, the ack."""

import hashlib
import io
import json
import threading

import pytest
from conftest import IMPLS, FakeJS, FakeKV, FakeMsg, FakeQwen, command, consumer
from conftest import qwen_command as qwen_fixture
from PIL import Image

from config import HOST
from consumer import SHUTDOWN, STALLED
from core.pack import Node
from core.residency import Residency
from messaging import schemas
from messaging.hosts import Host

RUN = "3c9a7e21-5d4b-4f6a-8b1c-9e2d7f0a4b35"
SESSION = "0b5c1f8e-7a2d-4c1e-9a4f-3d6f2b1c9e10"
PREFIX = "u/7d1e5c2a-9b3f-4e8d-a1c6-2f4b8e9d0a13/runs/3c9a7e21-5d4b-4f6a-8b1c-9e2d7f0a4b35/"


async def run(cmd, js: FakeJS | None = None, setup=None):
    js = js or FakeJS()
    c = consumer(js)
    if setup:
        setup(c)
    msg = FakeMsg(cmd, js)
    await c.handle(msg)
    return js, c, msg


def kinds(js: FakeJS) -> list[str]:
    out = []
    for e in js.events():
        k = e["type"].removeprefix("evt.run.")
        out.append(e["data"]["status"] if k == "status" else k)
    return out


def check_events(js: FakeJS) -> None:
    """Every event is on contract, on its subject, numbered 1..n with a matching Nats-Msg-Id."""
    evts = [(s, json.loads(d), h) for s, d, h in js.sent if s.startswith("evt.")]
    for n, (subject, msg, headers) in enumerate(evts, 1):
        assert schemas.message_errors(msg) == []
        assert msg["job_id"] == RUN and msg["data"]["seq"] == n
        assert subject == f"evt.run.{HOST}.{SESSION}.{msg['type'].removeprefix('evt.run.')}"
        assert headers == {"Nats-Msg-Id": f"{RUN}:1:{n}"}


async def test_echo_run_succeeds_with_progress_and_a_png():
    js, c, msg = await run(command(steps=3))
    assert kinds(js) == ["running", "progress", "progress", "progress", "output", "succeeded"]
    check_events(js)
    evts = js.events()
    assert evts[0]["data"]["host"] == "fake"
    assert [(e["data"]["node"], e["data"]["step"], e["data"]["of"]) for e in evts[1:4]] == [
        ("echo", 1, 3),
        ("echo", 2, 3),
        ("echo", 3, 3),
    ]
    out = evts[4]["data"]
    data, content_type = c.store.objects[PREFIX + "0.png"]
    assert content_type == "image/png"
    image = Image.open(io.BytesIO(data))
    assert image.format == "PNG"
    assert out == {
        "seq": 5,
        "attempt": 1,
        "index": 0,
        "key": PREFIX + "0.png",
        "kind": "image",
        "width": image.width,
        "height": image.height,
        "bytes": len(data),
        "etag": hashlib.md5(data).hexdigest(),
    }
    assert msg.log == [("ack", len(js.sent))]  # acked after the terminal status
    assert c.host.run_id is None and json.loads(c.host.kv.data["fake.fake"])["state"] == "idle"


async def test_fail_reports_failed_after_the_last_step_without_output():
    js, c, msg = await run(command(steps=2, fail=True))
    assert kinds(js) == ["running", "progress", "progress", "failed"]
    check_events(js)
    assert js.events()[-1]["data"]["error"] == "echo: failed on purpose (fail=true)"
    assert c.store.objects == {}
    assert msg.log == [("ack", len(js.sent))]


async def test_cancel_key_written_mid_run_stops_at_the_next_step():
    holder = {}

    def on_publish(subject, body):
        if subject.endswith(".progress") and body["data"]["step"] == 2:
            holder["c"].cancels_kv.put_nowait(RUN)

    js = FakeJS(on_publish)
    js, c, msg = await run(command(steps=10, delay_ms=5), js, setup=lambda c: holder.update(c=c))
    got = kinds(js)  # the watch is asynchronous: the step in flight may still finish
    assert got[0] == "running" and got[-1] == "cancelled"
    assert got[1:-1] in (["progress"] * 2, ["progress"] * 3)
    check_events(js)
    assert c.store.objects == {}
    assert msg.log == [("ack", len(js.sent))]


async def test_cancel_key_written_before_start_runs_nothing():
    js, _, msg = await run(command(), setup=lambda c: c.cancels_kv.data.update({RUN: b"t"}))
    assert kinds(js) == ["cancelled"]
    check_events(js)
    assert msg.log == [("ack", 1)]


async def test_runner_error_outside_the_graph_still_ends_the_run(monkeypatch):
    monkeypatch.setattr("messaging.publisher.RETRY_FOR", 0)

    async def broken_watch(key, ignore_deletes=False):
        raise TimeoutError

    def setup(c):
        c.cancels_kv.watch = broken_watch

    js, _, msg = await run(command(), setup=setup)
    assert kinds(js) == ["failed"]
    assert js.events()[-1]["data"]["error"] == "runner error (TimeoutError)"
    check_events(js)
    assert msg.log == [("ack", 1)]


async def test_shutdown_stops_the_run_and_reports_it_failed():
    holder = {}

    def on_publish(subject, body):
        if subject.endswith(".progress"):
            holder["c"].stopping = True

    js = FakeJS(on_publish)
    js, _, msg = await run(command(steps=5), js, setup=lambda c: holder.update(c=c))
    assert kinds(js) == ["running", "progress", "failed"]
    assert js.events()[-1]["data"]["error"] == SHUTDOWN
    assert msg.log == [("ack", len(js.sent))]


async def test_off_contract_command_is_dead_lettered_and_termed():
    bad = command()
    del bad["data"]["session_id"]
    js, _, msg = await run(bad)
    # no session: nothing to report on
    assert [s for s, _, _ in js.sent] == ["dlq.cmd.run.fake.fake"]
    assert js.sent[0][1] == msg.data
    assert msg.log == [("term", 1)]


async def test_garbage_is_dead_lettered_and_termed():
    js, _, msg = await run(b"not json")
    assert [s for s, _, _ in js.sent] == ["dlq.cmd.run.fake.fake"]
    assert msg.log == [("term", 1)]


async def test_graph_that_does_not_fit_the_node_schemas_is_rejected_and_reported():
    bad = command()
    bad["data"]["workflow"]["nodes"]["echo"]["kind"] = "echo/missing"
    js, _, msg = await run(bad)
    assert [s for s, _, _ in js.sent] == [
        "dlq.cmd.run.fake.fake",
        f"evt.run.{HOST}.{SESSION}.status",
    ]
    check_events(js)
    status = js.events()[0]["data"]
    assert status["status"] == "failed" and "unknown node kind" in status["error"]
    assert msg.log == [("term", 2)]


@pytest.mark.parametrize(
    ("edit", "error"),
    [
        (
            lambda n: n["echo"].update(bypass=True),
            "rejected: echo: a bypassed node reached the runner",
        ),
        (
            lambda n: n["output"]["in"].update(media="prompt.text"),
            "rejected: output.media: wants Media",
        ),
    ],
)
async def test_a_user_graph_breaking_a_rule_fails_naming_the_node(edit, error):
    bad = command()
    edit(bad["data"]["workflow"]["nodes"])
    js, _, _ = await run(bad)
    assert js.events()[0]["data"]["error"].startswith(error)


async def test_a_failed_term_does_not_take_the_runner_down():
    msg = FakeMsg(b"not json", FakeJS())

    async def term():
        raise TimeoutError

    msg.term = term
    await consumer(msg.js).handle(msg)  # no exception: main's loop goes on


async def test_a_publish_that_fails_once_is_retried_with_the_same_msg_id(monkeypatch):
    monkeypatch.setattr("messaging.publisher.RETRY_EVERY", 0)
    js, failed = FakeJS(), []
    publish = js.publish

    async def flaky(subject, data, headers=None):
        if subject.endswith(".progress") and not failed:
            failed.append(headers)
            raise TimeoutError
        await publish(subject, data, headers)

    js.publish = flaky
    js, _, _ = await run(command(steps=2), js)
    assert kinds(js) == ["running", "progress", "progress", "output", "succeeded"]
    check_events(js)
    assert failed == [{"Nats-Msg-Id": f"{RUN}:1:2"}]


async def test_nats_away_as_a_run_starts_does_not_fail_it(monkeypatch):
    """resilience batch-nats: NATS restarting between the delivery and the cancel watch (its read and its consumer
    create time out) once failed the run, `runner error (TimeoutError)`; both calls are retried as a publish is."""
    monkeypatch.setattr("messaging.publisher.RETRY_EVERY", 0)
    failed = []

    def setup(c):
        get, watch = c.cancels_kv.get, c.cancels_kv.watch

        async def flaky_get(key):
            if "get" not in failed:
                failed.append("get")
                raise TimeoutError
            return await get(key)

        async def flaky_watch(key, ignore_deletes=False):
            if "watch" not in failed:
                failed.append("watch")
                raise TimeoutError
            return await watch(key, ignore_deletes=ignore_deletes)

        c.cancels_kv.get, c.cancels_kv.watch = flaky_get, flaky_watch

    js, _, msg = await run(command(steps=2), setup=setup)
    assert failed == ["get", "watch"]
    assert kinds(js) == ["running", "progress", "progress", "output", "succeeded"]
    check_events(js)
    assert msg.log == [("ack", len(js.sent))]


def qwen_command(seed=None) -> dict:
    cmd = qwen_fixture()
    cmd["data"]["workflow"]["nodes"]["seed"]["params"]["seed"] = seed
    return cmd


def qwen_consumer(q: FakeQwen, js: FakeJS, **kw):
    res = kw.pop("residency", None) or Residency(52)
    host = Host(FakeKV(), extra=res.state)
    return consumer(js, impls=q.impls, node_schemas=q.schemas, residency=res, **kw), host


async def test_qwen_graph_reports_phases_and_the_resolved_seed(tmp_path, monkeypatch):
    q, js = FakeQwen(tmp_path, monkeypatch), FakeJS()
    c, host = qwen_consumer(q, js)
    c.host = host
    await c.handle(FakeMsg(qwen_command(), js))
    assert kinds(js) == ["running", "progress", "progress", "progress", "output", "succeeded"]
    check_events(js)
    evts = js.events()
    assert [(e["data"]["node"], e["data"]["phase"]) for e in evts[1:4]] == [
        ("encode", "encode"),
        ("sample", "sample"),
        ("decode", "decode"),
    ]
    seed = evts[-1]["data"]["resolved"]["seed"]
    assert 0 <= seed <= 2**53 - 1 and evts[4]["data"]["seed"] == seed
    assert q.ran() == ["encode", "sample", "decode"]
    value = json.loads(host.kv.data["fake.fake"])
    assert schemas.registry_errors("host", value) == [] and value["state"] == "idle"
    assert {r["component"] for r in value["resident"]} == {"text_encoder", "transformer", "vae"}
    assert not any(r["pinned"] for r in value["resident"])  # nothing in use after the run

    js2 = FakeJS()
    c.js = js2
    await c.handle(FakeMsg(qwen_command(seed=seed + 1), js2))
    assert q.ran() == ["sample", "decode"]  # a new seed: the prompt is not encoded again
    assert js2.events()[-1]["data"]["resolved"] == {"seed": seed + 1}


async def test_after_run_unload_empties_the_residency(tmp_path, monkeypatch):
    q, js = FakeQwen(tmp_path, monkeypatch), FakeJS()
    c, _ = qwen_consumer(q, js, after_run="unload")
    await c.handle(FakeMsg(qwen_command(seed=3), js))
    assert kinds(js)[-1] == "succeeded" and not c.residency.entries
    assert (
        len(c.cache) == 0
    )  # cached node results go too: the same run again loads its components again


async def test_a_run_that_does_not_fit_fails_with_numbers(tmp_path, monkeypatch):
    q, js = FakeQwen(tmp_path, monkeypatch), FakeJS()
    c, _ = qwen_consumer(q, js, residency=Residency(1))
    await c.handle(FakeMsg(qwen_command(seed=3), js))
    assert kinds(js) == ["running", "failed"]
    assert "does not fit in memory" in js.events()[-1]["data"]["error"]


async def test_a_stalled_node_fails_the_run_acks_and_exits(monkeypatch):
    monkeypatch.setattr("consumer.HEARTBEAT", 0.01)
    release, exits = threading.Event(), []

    def stuck(ctx, params, inputs):
        release.wait(5)  # never passes a cancel point
        return {"image": None}

    impls = dict(IMPLS)
    impls["echo/echo"] = Node(stuck, stall_s=0.05)
    js = FakeJS()
    c = consumer(js, impls=impls)

    def exit(code):
        exits.append(code)
        release.set()

    c.exit = exit
    msg = FakeMsg(command(), js)
    await c.handle(msg)
    assert exits == [STALLED]
    assert kinds(js) == ["running", "failed"]
    assert "echo: stalled" in js.events()[-1]["data"]["error"]
    assert msg.log == [("ack", 2)]  # acked once, by the watchdog
    assert "fake.fake" not in c.host.kv.data  # offline


async def test_a_video_output_stores_the_mp4_and_its_poster_and_reports_the_video_block():
    """A Video value ({mp4, poster, frames, fps, has_audio}) into the core output: <n>.poster.png first, then
    <n>.mp4 (video/mp4), then evt.run.output kind video with the video block (contracts/README.md, Video)."""
    poster = Image.new("RGB", (64, 32), "red")

    def clip(ctx, params, inputs):
        return {"video": {"mp4": b"\x00\x00\x00\x18ftypisom", "poster": poster, "frames": 49, "fps": 24,
                          "has_audio": True}}  # fmt: skip

    schema = {"kind": "t/clip", "title": "Clip", "family": "t", "params": [], "inputs": [],
              "outputs": [{"name": "video", "type": "Video"}]}  # fmt: skip
    from conftest import SCHEMAS

    cmd = command()
    cmd["data"]["workflow"]["nodes"] = {"clip": {"kind": "t/clip", "params": {}},
                                        "output": {"kind": "output", "params": {}, "in": {"media": "clip.video"}}}  # fmt: skip
    js = FakeJS()
    c = consumer(
        js, impls=IMPLS | {"t/clip": Node(clip)}, node_schemas=SCHEMAS | {"t/clip": schema}
    )
    await c.handle(FakeMsg(cmd, js))
    assert kinds(js) == ["running", "output", "succeeded"]
    check_events(js)
    png, ct = c.store.objects[PREFIX + "0.poster.png"]
    assert ct == "image/png" and Image.open(io.BytesIO(png)).size == (64, 32)
    assert c.store.objects[PREFIX + "0.mp4"] == (b"\x00\x00\x00\x18ftypisom", "video/mp4")
    assert list(c.store.objects) == [PREFIX + "0.poster.png", PREFIX + "0.mp4"]
    out = js.events()[1]["data"]
    assert (out["kind"], out["key"], out["width"], out["height"]) == (
        "video",
        PREFIX + "0.mp4",
        64,
        32,
    )
    assert out["video"] == {"duration_s": 49 / 24, "fps": 24, "frames": 49, "has_audio": True,
                            "poster": {"key": PREFIX + "0.poster.png", "width": 64, "height": 32, "bytes": len(png)}}  # fmt: skip


async def test_an_echo_video_run_writes_its_last_frame_and_stream_facts():
    """From 1.17 a video output also writes <n>.last.png before the event and reports video.last and video.stream."""
    cmd = command()
    cmd["data"]["workflow"]["nodes"] = {
        "prompt": {"kind": "prompt", "params": {"text": "hi"}},
        "canvas": {"kind": "echo/canvas", "params": {"width": 64, "height": 64}},
        "duration": {"kind": "echo/duration", "params": {"frames": 9, "fps": 24}},
        "video": {"kind": "echo/video", "params": {"steps": 1, "delay_ms": 0, "fail": False, "refs": []},
                  "in": {"text": "prompt.text", "canvas": "canvas.canvas", "duration": "duration.duration"}},
        "output": {"kind": "output", "params": {}, "in": {"media": "video.video"}}}  # fmt: skip
    js = FakeJS()
    c = consumer(js)
    await c.handle(FakeMsg(cmd, js))
    assert kinds(js)[-1] == "succeeded", js.events()[-1]
    check_events(js)
    video = next(e for e in js.events() if e["type"] == "evt.run.output")["data"]["video"]
    assert list(c.store.objects) == [
        PREFIX + "0.poster.png",
        PREFIX + "0.last.png",
        PREFIX + "0.mp4",
    ]
    assert video["last"]["key"] == PREFIX + "0.last.png" and video["stream"]["width"] == 64


async def test_runner_unload_frees_everything_when_idle_and_is_busy_during_a_run():
    import main

    freed = []
    res = Residency(48, free=lambda: freed.append(1), pins={"transformer"})
    res.entries[("m", "v", "transformer", "int8")] = [
        19.2,
        object(),
        None,
        0.0,
    ]  # pinned: still unloaded
    js = FakeJS()
    c = consumer(js, residency=res)
    c.host.extra = res.state
    c.cache["k"] = {"x": 1}
    reply = await main.unload_reply(c, c.host, {"for_env": "torch"})
    assert (
        reply == {"unloaded_gb": 19.2, "resident": [], "freed": [
            {"component": "transformer", "model": "m", "variant": "v", "precision": "int8"}]}
        and not res.entries and not c.cache and freed
    )  # fmt: skip
    assert (
        schemas.errors(
            "https://helmstudio.local/contracts/messages/req.runner.unload.schema.json#/$defs/reply",
            reply,
        )
        == []
    )
    assert (
        json.loads(c.host.kv.data["fake.fake"])["resident"] == []
    )  # the hosts key was rewritten first
    c.ctx = object()  # a run in progress
    assert await main.unload_reply(c, c.host, {"for_env": "torch"}) == {
        "error": "a run is in progress",
        "code": "busy",
    }


async def test_the_hosts_key_lists_the_loads_before_the_terminal_status(tmp_path, monkeypatch):
    """studio frees the host on the terminal status and its GPU gate reads the key: by then it names what stays."""
    seen = {}

    def on_publish(subject, body):
        if body["type"] == "evt.run.status" and body["data"]["status"] == "succeeded":
            seen["value"] = json.loads(seen["host"].kv.data["fake.fake"])

    q, js = FakeQwen(tmp_path, monkeypatch), FakeJS(on_publish)
    c, host = qwen_consumer(q, js)
    c.host = seen["host"] = host
    await c.handle(FakeMsg(qwen_command(seed=3), js))
    assert {r["component"] for r in seen["value"]["resident"]} == {
        "text_encoder",
        "transformer",
        "vae",
    }
    assert seen["value"]["run_id"] == RUN  # still its lease
    assert json.loads(host.kv.data["fake.fake"])["state"] == "idle"


UNLOAD = "https://helmstudio.local/contracts/messages/req.runner.unload.schema.json#/$defs/"
LORA = "7f3e2a10-5b4c-4d8e-9f1a-2c3b4d5e6f70"


async def test_runner_unload_frees_the_named_components_and_replies_what_remains():
    """The hand-over: exactly the named entries go (a baked set by lora id and strength); a pinned or unknown one is
    skipped; `resident` is what the rewritten hosts key lists; asked again, nothing is freed."""
    import main

    res = Residency(48, pins={"vae"})
    baked = [{"lora_id": LORA, "name": "ink", "strength": 0.8, "bytes": 1000}]
    res.entries[("ltx-2.5", "distilled", "transformer", "int8")] = [19.2, object(), None, 1.0]
    res.entries[("ltx-2.5", "distilled", "transformer", "int8", (("ab", 0.8),))] = [
        19.4,
        object(),
        baked,
        2.0,
    ]
    res.entries[("ltx-2.5", "distilled", "vae", "bf16")] = [1.0, object(), None, 3.0]
    js = FakeJS()
    c = consumer(js, residency=res)
    c.host.extra = res.state
    c.cache["k"] = {"x": 1}
    name = {
        "component": "transformer",
        "model": "ltx-2.5",
        "variant": "distilled",
        "precision": "int8",
    }
    request = {"for_env": "torch", "run_id": RUN, "components": [
        name | {"loras": [{"lora_id": LORA, "strength": 0.8}]},
        {"component": "vae", "model": "ltx-2.5", "variant": "distilled", "precision": "bf16"},  # pinned: goes too
        name | {"precision": "int4"},  # not held
    ]}  # fmt: skip
    assert schemas.errors(UNLOAD + "request", request) == []
    reply = await main.unload_reply(c, c.host, request)
    assert schemas.errors(UNLOAD + "reply", reply) == []
    assert reply["unloaded_gb"] == 20.4 and reply["freed"] == request["components"][:2]
    assert [(r["component"], r.get("loras")) for r in reply["resident"]] == [("transformer", None)]
    assert reply["resident"] == json.loads(c.host.kv.data["fake.fake"])["resident"]
    assert not c.cache  # cached node results go too: the other env needs the memory
    again = await main.unload_reply(c, c.host, request)
    assert again["unloaded_gb"] == 0 and again["freed"] == [] and len(again["resident"]) == 1
    bad = await main.unload_reply(c, c.host, {"for_env": "torch", "components": []})
    assert bad["code"] == "internal" and len(res.entries) == 1


async def test_budget_gb_caps_the_run_and_ends_with_it(tmp_path, monkeypatch):
    """cmd.run.execute budget_gb (what the host's other envs leave) caps that run's residency budget."""
    q, js = FakeQwen(tmp_path, monkeypatch), FakeJS()
    c, _ = qwen_consumer(q, js)
    cmd = qwen_command(seed=3)
    cmd["data"]["budget_gb"] = 1.0
    await c.handle(FakeMsg(cmd, js))
    assert kinds(js) == ["running", "failed"]
    assert js.events()[-1]["data"]["error"].endswith("budget 1.0 GB")
    assert c.residency.cap is None and c.residency.limit == 52
