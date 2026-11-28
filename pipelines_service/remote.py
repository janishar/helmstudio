"""Remote calls (plan 16.3, 16.4; contracts/README.md "Model sources: a remote provider"): pipelines calls a remote
provider itself, never as a run. A node's `submit` queues the call here under the key an ask run would have had
(`queue`), its `wait` parks, and `run` sends it and hands the result to the engine (`deliver`), which resumes the
thread like a run_finished.

- Off by default twice over: the deployment's switch (config.REMOTE_MODELS) and the pipeline's `remote`
  (enabled, providers). A call refused by either never leaves the queue.
- At most `max_in_flight` (default 2) requests per provider over every batch; per batch at most `max_calls` calls
  and `max_tokens` tokens (over every row and the director's chat): a call is queued only while the tokens used
  plus those reserved by every unfinished call plus its own bound (its prompt's estimate and its max_tokens) stay
  within the cap, so calls queued together never overshoot it; a call past a cap fails at once (`remote cap
  reached: <n> calls`, kind cap). A provider that reports no usage is counted at that bound (providers.chat).
- Pause holds queued calls (only calls of a running batch are sent) and a call between its network retries (it
  goes back to the queue); Stop cancels the queued ones and aborts the ones in flight; a call in flight at a crash
  or a shutdown stays `sent` and is sent again after the restart, and a stored result is delivered again then.
- Network failures are retried (providers.chat_retried) and counted on the batch apart from verdicts."""

import asyncio
import contextlib
import logging

from psycopg.types.json import Jsonb

import config
import providers

log = logging.getLogger(__name__)

POLL = 1.0  # seconds between looks at the queue when nothing pokes it


def bound(req: dict) -> int:
    """A call's token bound: its prompt's estimate and its whole output allowance (as BOUND)."""
    return providers.estimate(req.get("system", "") + req.get("prompt", "")) + int(req.get("max_tokens", 0))


# bound() in SQL over remote_calls.request
BOUND = (
    "(length(coalesce(request->>'system', '') || coalesce(request->>'prompt', '')) + 3) / 4 "
    "+ coalesce((request->>'max_tokens')::int, 0)"
)


def _failed(error: str, kind: str, failures: int = 0) -> dict:
    return {"status": "failed", "take_ids": [], "error": error[:2000], "kind": kind, "network_failures": failures}


class Remote:
    def __init__(self, engine) -> None:
        self.e = engine
        self.poke = asyncio.Event()
        self.lock = asyncio.Lock()
        self.flying: dict[str, tuple[str, str, asyncio.Task]] = {}  # key → (batch, provider, task)
        self.stopping: set[str] = set()  # keys Stop aborts (any other cancel is a shutdown: sent again later)

    async def queue(self, batch: dict, key: str, row: int, req: dict) -> dict | None:
        """Queue one call (once per key): its result when it is known already (done, refused, past a cap),
        else None (the thread waits; `deliver` resumes it)."""
        snap = batch["snapshot"].get("pipeline") or {}
        rem, name = snap.get("remote") or {}, req["provider"]
        prov = (snap.get("providers") or {}).get(name)
        if not config.REMOTE_MODELS:
            return _failed("remote models are off on this deployment", "off")
        if not rem.get("enabled") or name not in rem.get("providers", []):
            return _failed(f"remote models are not enabled for {name} in this pipeline", "off")
        if not prov or prov["kind"] != "remote":
            return _failed(f"{name} is not a remote provider of this batch", "off")
        # ponytail: one lock for the check and the insert (one pipelines process); a per-batch row lock in
        # Postgres if this service ever runs twice
        async with self.lock:
            return await self._queue(batch, key, row, req, rem, name)

    async def _queue(self, batch, key, row, req, rem, name) -> dict | None:
        old = await self.e._one("SELECT status, result FROM remote_calls WHERE key = %s", (key,))
        if old:
            return old["result"] if old["status"] == "done" else None
        b = await self.e._one(
            "SELECT remote_tokens, (SELECT count(*) FROM remote_calls WHERE batch_id = %s) AS calls, "
            "(SELECT coalesce(sum(" + BOUND + "), 0) FROM remote_calls WHERE batch_id = %s AND status <> 'done') "
            "AS reserved FROM batches WHERE batch_id = %s",
            (batch["batch_id"], batch["batch_id"], batch["batch_id"]),
        )
        if b["calls"] >= rem["max_calls"]:
            return _failed(f"remote cap reached: {rem['max_calls']} calls", "cap")
        if b["remote_tokens"] + b["reserved"] + bound(req) > rem["max_tokens"]:
            return _failed(f"remote cap reached: {rem['max_tokens']} tokens", "cap")
        await self.e._one(
            "INSERT INTO remote_calls (key, batch_id, row, provider, request) VALUES (%s, %s, %s, %s, %s) "
            "ON CONFLICT (key) DO NOTHING RETURNING 1",
            (key, batch["batch_id"], row, name, Jsonb(req)),
        )
        self.poke.set()
        return None

    async def run(self, stop: asyncio.Event) -> None:
        """Send queued calls of running batches, oldest first, within each provider's in-flight limit. At start, a
        call left `sent` is queued again and a stored result is delivered again (deliver drops it unless the thread
        still waits on it)."""
        await self.e._one("UPDATE remote_calls SET status = 'queued' WHERE status = 'sent' RETURNING 1")
        for c in await self.e._all(
            "SELECT c.key, c.batch_id, c.row, c.result FROM remote_calls c JOIN batches b USING (batch_id) "
            "WHERE c.status = 'done' AND b.status NOT IN ('done', 'failed', 'stopped')"
        ):
            try:
                await self.e.deliver(str(c["batch_id"]), c["row"], c["key"], c["result"])
            except Exception:
                log.exception("remote call %s: delivering its result again failed", c["key"])
        while not stop.is_set():
            try:
                await self._start_some()
            except Exception:
                log.exception("remote queue failed; trying again")
            with contextlib.suppress(TimeoutError):
                await asyncio.wait_for(self.poke.wait(), POLL)
            self.poke.clear()
        tasks = [t for _, _, t in self.flying.values()]
        for t in tasks:
            t.cancel()  # a shutdown: they stay `sent`
        await asyncio.gather(*tasks, return_exceptions=True)

    async def _start_some(self) -> None:
        calls = await self.e._all(
            "SELECT c.key, c.batch_id, c.row, c.provider, c.request, b.snapshot FROM remote_calls c "
            "JOIN batches b USING (batch_id) WHERE c.status = 'queued' AND b.control = 'run' "
            "ORDER BY c.created_at LIMIT 64"
        )
        for c in calls:
            prov = c["snapshot"]["pipeline"]["providers"][c["provider"]]
            busy = sum(p == c["provider"] for _, p, _ in self.flying.values())
            if c["key"] in self.flying or busy >= prov.get("max_in_flight", 2):
                continue
            await self.e._one("UPDATE remote_calls SET status = 'sent' WHERE key = %s RETURNING 1", (c["key"],))
            task = asyncio.create_task(self._send(c, prov))
            self.flying[c["key"]] = (str(c["batch_id"]), c["provider"], task)

    async def _held(self, batch_id) -> bool:
        b = await self.e._one("SELECT control FROM batches WHERE batch_id = %s", (batch_id,))
        return not b or b["control"] != "run"

    async def _send(self, c: dict, prov: dict) -> None:
        req, name, key = c["request"], c["provider"], c["key"]
        try:
            got, failures = await providers.chat_retried(name, prov, req, hold=lambda: self._held(c["batch_id"]))
            result = {"status": "succeeded", "take_ids": [], "error": None, "answer": providers.answer_of(got),
                      "network_failures": failures}  # fmt: skip
        except providers.Held as h:  # paused between retries: back to the queue, its failures counted now
            async with self.e.pool.connection() as conn, conn.transaction():
                await conn.execute("UPDATE remote_calls SET status = 'queued' WHERE key = %s AND status = 'sent'",
                                   (key,))  # fmt: skip
                await conn.execute(
                    "UPDATE batches SET network_failures = network_failures + %s WHERE batch_id = %s",
                    (h.failures, c["batch_id"]),
                )
            return
        except providers.CallError as e:
            result = _failed(str(e), "network" if e.kind == "network" else "answer" if e.kind == "answer" else "off",
                             getattr(e, "failures", 0))  # fmt: skip
        except asyncio.CancelledError:
            if key not in self.stopping:
                raise  # a shutdown: it stays `sent`, sent again after the restart
            result = {"status": "cancelled", "take_ids": [], "error": None, "network_failures": 0}
        finally:
            self.flying.pop(key, None)
            self.poke.set()
        self.stopping.discard(key)
        await self._done(c, result)

    async def _done(self, c: dict, result: dict) -> None:
        """Store the result and the batch's counters, then resume the thread waiting on it."""
        answer = result.get("answer") or {}
        tokens = answer.get("tokens_in", 0) + answer.get("tokens_out", 0)
        async with self.e.pool.connection() as conn, conn.transaction():
            cur = await conn.execute(
                "UPDATE remote_calls SET status = 'done', result = %s WHERE key = %s AND status <> 'done' RETURNING 1",
                (Jsonb(result), c["key"]),
            )
            if cur.rowcount:
                await conn.execute(
                    "UPDATE batches SET remote_calls = remote_calls + %s, remote_tokens = remote_tokens + %s, "
                    "network_failures = network_failures + %s WHERE batch_id = %s",
                    (int(result["status"] != "cancelled"), tokens, result.get("network_failures", 0), c["batch_id"]),
                )
        await self.e._publish_batch(str(c["batch_id"]))
        await self.e.deliver(str(c["batch_id"]), c["row"], c["key"], result)

    async def stop_batch(self, batch_id: str) -> None:
        """Stop: queued calls are cancelled, calls in flight aborted; each thread gets `cancelled`."""
        for key, (b, _, task) in list(self.flying.items()):
            if b == str(batch_id):
                self.stopping.add(key)
                task.cancel()
        for c in await self.e._all(
            "SELECT key, batch_id, row FROM remote_calls WHERE batch_id = %s AND status = 'queued'",
            (batch_id,),
        ):
            await self._done(c, {"status": "cancelled", "take_ids": [], "error": None, "network_failures": 0})
