"""The server executor (contracts/README.md "Model sources: a local server"): for each host in
config.SERVER_HOSTS this service is the runner of env `server`. A local server's calls are batch-lane queue items
(runs of the built-in `server-ask` workflow) that the dispatcher releases on that host with no admission (its memory
is reserved while it is enabled); the release arrives here as cmd.run.<host>.server.

- At start it publishes registry `<host>.server.{nodes,workflows}` and keeps hosts key `<host>.server` fresh
  (every HEARTBEAT s; run_id and attempt while busy: the dispatcher's lease).
- Per command: the cancel key is checked first; `running`; one chat completion through providers.chat_retried
  (the host rule at every call); with a `schema` the answer is validated and asked once more in the run; with
  `tools` the native tool calls are recorded on the try; then `succeeded` with the `answer`, or `failed`.
  Everything after that (run_finished, decisions, replay) is stage A's.
- The provider is the one the run's batch was submitted with (snapshot providers): the owner's newest unfinished
  batch naming it."""

import asyncio
import contextlib
import json
import logging
import uuid

from nats.js.api import AckPolicy, ConsumerConfig, DeliverPolicy
from nats.js.errors import KeyDeletedError, KeyNotFoundError

import agents
import config
import providers
from messaging import schemas
from messaging.publisher import now

log = logging.getLogger(__name__)

HEARTBEAT = 10  # seconds
ACK_WAIT = 60
PUBLISHED_BY = f"pipelines {config.VERSION}"
NODES = [
    {"kind": "server/ask", "title": "Ask a local server", "family": "server",
     "help": "One call to an OpenAI-compatible server you run (its memory is reserved on the host). pipelines_service makes the call when the dispatcher releases the run.",
     "params": [
         {"name": "provider", "type": "string", "label": "Server", "default": "", "max": 32, "help": "The provider's name (admin settings)."},
         {"name": "model", "type": "string", "label": "Model", "default": "", "max": 128, "help": "The model name the server takes; empty = the provider's default."},
         {"name": "system", "type": "string", "label": "System", "default": "", "max": 4000, "help": "The system turn."},
         {"name": "prompt", "type": "string", "label": "Prompt", "default": "", "max": 32768, "help": "The filled template (pipelines writes it). Not scanned for mentions."},
         {"name": "schema", "type": "string", "label": "Answer schema", "default": "", "max": 16384, "help": "A JSON schema the answer must match (response_format json_schema); empty = plain text."},
         {"name": "tools", "type": "string", "label": "Tools", "default": "", "max": 16384, "help": "OpenAI tool definitions as a JSON array; empty = none. With tools the answer may be tool calls."},
         {"name": "max_tokens", "type": "int", "label": "Max tokens", "default": 800, "min": 16, "max": 4096},
     ],
     "inputs": [], "outputs": [{"name": "answer", "type": "Text"}]},
    {"kind": "answer", "title": "Answer", "family": None,
     "help": "Stores its text as the run's answer: no take. Ask workflows end with it.",
     "params": [], "inputs": [{"name": "text", "type": "Text"}], "outputs": []},
]  # fmt: skip
WORKFLOWS = [
    {"id": "server-ask", "title": "Ask a local server", "family": "server", "env": "server", "media": "text",
     "nodes": {"ask": {"kind": "server/ask", "params": {"max_tokens": 800}},
               "answer": {"kind": "answer", "in": {"text": "ask.answer"}}}},
]  # fmt: skip


def registry_values(host: str) -> dict[str, dict]:
    meta = {
        "schema_version": 1,
        "host": host,
        "env": "server",
        "published_at": now(),
        "published_by": PUBLISHED_BY,
    }
    values = {"nodes": meta | {"nodes": NODES}, "workflows": meta | {"workflows": WORKFLOWS}}
    for kind, v in values.items():
        if errs := schemas.registry_errors(kind, v):
            raise ValueError(f"{host}.server.{kind} is off-contract: {'; '.join(errs[:3])}")
    return values


async def provider_for(pool, owner_id: str, name: str) -> dict | None:
    """The provider as the owner's newest unfinished batch naming it was submitted with.
    ponytail: cmd.run.execute does not name the batch, so two unfinished batches whose Submits saw different
    settings of one provider use the newer one; carry the batch on the run if that ever matters."""
    async with pool.connection() as conn:
        r = await (await conn.execute(
            "SELECT snapshot->'pipeline'->'providers'->%s AS p FROM batches WHERE owner_id = %s "
            "AND status NOT IN ('done', 'failed', 'stopped') AND snapshot->'pipeline'->'providers' ? %s "
            "ORDER BY created_at DESC LIMIT 1",
            (name, owner_id, name),
        )).fetchone()  # fmt: skip
    return r and r["p"]


async def execute(pool, cmd: dict) -> tuple[str, str | None, dict | None]:
    """One server-ask run: (status, error, answer)."""
    nodes = cmd["data"]["workflow"]["nodes"]
    params = next(n["params"] for n in nodes.values() if n["kind"] == "server/ask")
    name, model = params.get("provider", ""), params.get("model", "")
    prov = await provider_for(pool, cmd["owner_id"], name)
    if not prov or prov.get("kind") != "server":
        return "failed", f"no local server named {name!r} in this batch", None
    if not model:
        return "failed", f"{name}: no model named", None
    req = {"model": model, "system": params.get("system", ""), "prompt": params.get("prompt", ""),
           "max_tokens": params.get("max_tokens", 800)}  # fmt: skip
    if params.get("tools"):
        req["tools"] = json.loads(params["tools"])
    elif params.get("schema"):
        req["schema"] = params["schema"]
    tries, tin, tout, secs = [], 0, 0, 0.0
    for attempt in range(2):  # with a schema: one more try in the run
        try:
            got, _ = await providers.chat_retried(name, prov, req)
        except providers.CallError as e:
            return "failed", str(e)[:2000], None
        tin, tout, secs = tin + got["tokens_in"], tout + got["tokens_out"], secs + got["seconds"]
        t = providers.answer_of(got)["tries"][0]
        err = t["error"]  # too many tool calls
        if "schema" in req:
            _, err = agents.parse(got["text"], json.loads(req["schema"]))
        t["error"] = err
        tries.append(t)
        answer = {
            "tries": tries,
            "seen": [],
            "tokens_in": tin,
            "tokens_out": tout,
            "seconds": round(secs, 3),
        }
        if err is None:
            return "succeeded", None, answer
        req["prompt"] = f"{req['prompt']}\n\n{agents.REASK.format(error=err)}"[: agents.PROMPT_MAX]
    return "failed", tries[-1]["error"], answer


class Executor:
    """The `server` env runner of one host."""

    def __init__(self, js, pool, host: str) -> None:
        self.js, self.pool, self.host = js, pool, host
        self.run: tuple[str, int] | None = None  # (run_id, attempt) while busy

    def hosts_value(self) -> dict:
        v = {"schema_version": 1, "host": self.host, "env": "server", "state": "busy" if self.run else "idle",
             "run_id": self.run[0] if self.run else None, "updated_at": now(), "published_by": PUBLISHED_BY}  # fmt: skip
        if self.run:
            v["attempt"] = self.run[1]
        return v

    async def beat(self, stop: asyncio.Event) -> None:
        kv = await self.js.key_value("hosts")
        while not stop.is_set():
            with contextlib.suppress(Exception):
                await kv.put(f"{self.host}.server", json.dumps(self.hosts_value()).encode())
            with contextlib.suppress(TimeoutError):
                await asyncio.wait_for(stop.wait(), HEARTBEAT)
        with contextlib.suppress(Exception):
            await kv.delete(f"{self.host}.server")

    async def start(self):
        reg = await self.js.key_value("registry")
        for kind, v in registry_values(self.host).items():
            await reg.put(f"{self.host}.server.{kind}", json.dumps(v).encode())
        self.cancels = await self.js.key_value("cancels")
        name = f"pipelines-server-{self.host}"
        await self.js.add_consumer("CMD", ConsumerConfig(
            name=name, durable_name=name, filter_subject=f"cmd.run.{self.host}.server",
            ack_policy=AckPolicy.EXPLICIT, ack_wait=ACK_WAIT, max_ack_pending=1, max_deliver=1,
            deliver_policy=DeliverPolicy.ALL))  # fmt: skip
        return await self.js.pull_subscribe_bind(durable=name, stream="CMD")

    async def _status(self, cmd: dict, seq: int, data: dict) -> None:
        d = cmd["data"]
        msg = {"type": "evt.run.status", "version": 1, "id": str(uuid.uuid4()), "job_id": cmd["job_id"],
               "owner_id": cmd["owner_id"], "occurred_at": now(),
               "trace": {k: v for k, v in cmd["trace"].items() if k in ("traceparent", "tracestate", "correlation_id")},
               "data": {"seq": seq, "attempt": d.get("attempt", 1)} | data}  # fmt: skip
        if errs := schemas.message_errors(msg):
            raise ValueError(f"evt.run.status is off-contract: {'; '.join(errs[:3])}")
        await self.js.publish(f"evt.run.{d['session_id']}.status", json.dumps(msg).encode(),
                              headers={"Nats-Msg-Id": f"{d['run_id']}:{d.get('attempt', 1)}:{seq}"})  # fmt: skip

    async def _cancelled(self, run_id: str) -> bool:
        try:
            await self.cancels.get(run_id)
        except KeyNotFoundError, KeyDeletedError:
            return False
        return True

    async def handle(self, msg) -> None:
        try:
            cmd = json.loads(msg.data)
        except ValueError:
            cmd = None
        if (
            not isinstance(cmd, dict)
            or schemas.message_errors(cmd)
            or cmd.get("type") != "cmd.run.execute"
        ):
            log.error("rejecting %s: not a cmd.run.execute", msg.subject)
            await msg.term()
            return
        d = cmd["data"]
        self.run = (d["run_id"], d.get("attempt", 1))
        beat = asyncio.create_task(self._in_progress(msg))
        try:
            if await self._cancelled(d["run_id"]):
                await self._status(cmd, 1, {"status": "cancelled", "error": None})
                return
            await self._status(cmd, 1, {"status": "running", "host": self.host})
            status, error, answer = await execute(self.pool, cmd)
            if await self._cancelled(
                d["run_id"]
            ):  # ponytail: checked around the call, not during it
                status, error = "cancelled", None
            data = {"status": status, "error": error} | ({"answer": answer} if answer else {})
            await self._status(cmd, 2, data)
        except Exception:
            log.exception("server run %s failed", d["run_id"])
            with contextlib.suppress(Exception):
                await self._status(cmd, 2, {"status": "failed", "error": "the server call failed"})
        finally:
            beat.cancel()
            self.run = None
            with contextlib.suppress(Exception):
                await msg.ack()

    async def _in_progress(self, msg) -> None:
        while True:
            await asyncio.sleep(HEARTBEAT)
            with contextlib.suppress(Exception):
                await msg.in_progress()
