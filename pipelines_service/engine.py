"""The batch engine: one LangGraph thread per row (graph.py), driven by commands (consumer.py) and run_finished
events (events.py).

- Two records here: `batches` / `batch_rows` (what was submitted, control, each row's last published state, the
  event seq) and LangGraph's checkpoints (where each thread is). Studio's batch record is built from the
  evt.pipeline.* events published here; each event is a whole state with a seq committed before the publish.
- Row 1 first: until row 1 has ended, only row 1 is driven; then every row (`released`), at most WINDOW rows of a
  batch asked of studio and not ended (each row end lets the next one go), so one user's batches add a bounded
  number of runs to studio's line-up. Ordering between runs is studio's dispatcher's job; at most DRIVE_CONCURRENCY
  rows are in flight here (a parked row holds nothing). A row whose drive fails (database or NATS down) is tried
  again with backoff until it parks, ends, or its batch no longer runs.
- Resumes of one thread are serialised by a per-row lock, held while its graph runs, so a run_finished that
  arrives before the thread has parked waits for the park and then resumes it. `self.tasks` (rows being driven)
  is read and changed under the batch's lock, and a drive reads the batch's control under that lock too, so a
  Pause, Resume or Stop never misses a row.
- Every row end is followed up (published, the batch ended or paused, the next rows driven) again whenever it is
  seen again: a redelivered run_finished, a retried drive, a restart (which publishes every row again).
- Pause starts no new rows (in-flight submits finish, studio holds their runs). Stop cancels rows that never
  started; the rest end with their (cancelled) runs; `stopped` when every row is final.
- Restart (`start`): every running batch is reported paused and waits for Resume; run_finished events still
  resume parked threads meanwhile. On Resume a thread continues from its last checkpoint: a submit that never
  recorded its run asks again under the same key and gets the same run.
- When a batch ends its threads are deleted; the batch and its snapshot stay (Retry)."""

import asyncio
import logging
import weakref
from collections.abc import Awaitable, Callable

from langgraph.types import Command
from psycopg.types.json import Jsonb

import graph

log = logging.getLogger(__name__)

RESTARTED = "the pipeline service restarted; press Resume"
PAUSED_BY_YOU = "paused by you"
FINAL_ROW = ("succeeded", "failed", "cancelled")
FINAL_BATCH = ("done", "failed", "stopped")
STREAK = 3  # rows failing in a row with one error pause the batch
WINDOW = 16  # rows of one batch asked of studio and not ended
BACKOFF_MAX = 30.0  # seconds between tries of a row whose drive failed


def thread_id(batch_id, row: int) -> str:
    return f"{batch_id}:{row}"


def _cfg(batch_id, row: int) -> dict:
    return {"configurable": {"thread_id": thread_id(batch_id, row)}}


def _lock(locks: weakref.WeakValueDictionary, key: str) -> asyncio.Lock:
    """The lock of `key`; it lives while someone holds or waits on it (so the dictionaries never grow)."""
    lock = locks.get(key)
    if lock is None:
        locks[key] = lock = asyncio.Lock()
    return lock


def row_data(r: dict) -> dict:
    """evt.pipeline.row data (without seq) from a batch_rows record."""
    d = {"row": r["row"], "attempt": r["attempt"], "status": r["status"]}
    if r["run_id"]:
        d["run_id"] = str(r["run_id"])
    if r["status"] == "succeeded" and r["take_ids"] is not None:
        d["take_ids"] = r["take_ids"]
    if r["error"]:
        d["error"] = r["error"][:2000]
    return d


class Engine:
    def __init__(
        self,
        pool,
        saver,
        publisher,
        create_run: Callable[[dict], Awaitable[dict]],
        concurrency: int = 8,
        window: int = WINDOW,
    ) -> None:
        self.pool, self.saver, self.publisher, self.create_run = pool, saver, publisher, create_run
        self.graph = graph.build().compile(checkpointer=saver)
        self.row_locks: weakref.WeakValueDictionary = weakref.WeakValueDictionary()
        self.batch_locks: weakref.WeakValueDictionary = weakref.WeakValueDictionary()
        self.tasks: dict[
            str, asyncio.Task
        ] = {}  # thread id → the task driving it (under the batch's lock)
        self.slots = asyncio.Semaphore(concurrency)
        self.window = window

    # --- database

    async def _one(self, sql: str, args=()) -> dict | None:
        async with self.pool.connection() as conn:
            return await (await conn.execute(sql, args)).fetchone()

    async def _all(self, sql: str, args=()) -> list[dict]:
        async with self.pool.connection() as conn:
            return await (await conn.execute(sql, args)).fetchall()

    async def _batch(self, batch_id) -> dict | None:
        return await self._one("SELECT * FROM batches WHERE batch_id = %s", (batch_id,))

    async def _row(self, batch_id, row: int) -> dict | None:
        return await self._one(
            "SELECT * FROM batch_rows WHERE batch_id = %s AND row = %s", (batch_id, row)
        )

    async def _rows(self, batch_id) -> list[dict]:
        return await self._all(
            "SELECT * FROM batch_rows WHERE batch_id = %s ORDER BY row", (batch_id,)
        )

    # --- events: the seq is taken and the state read in one committed transaction, then published

    async def _publish_batch(self, batch_id) -> None:
        async with self.pool.connection() as conn, conn.transaction():
            b = await (
                await conn.execute(
                    "UPDATE batches SET seq = seq + 1, updated_at = now() WHERE batch_id = %s "
                    "RETURNING *",
                    (batch_id,),
                )
            ).fetchone()
        data = {"seq": b["seq"], "status": b["status"]}
        if b["reason"]:
            data["reason"] = b["reason"][:2000]
        await self.publisher.publish("batch", b, data)

    async def _publish_row(self, batch_id, row: int) -> None:
        async with self.pool.connection() as conn, conn.transaction():
            b = await (
                await conn.execute(
                    "UPDATE batches SET seq = seq + 1, updated_at = now() WHERE batch_id = %s "
                    "RETURNING *",
                    (batch_id,),
                )
            ).fetchone()
            r = await (
                await conn.execute(
                    "SELECT * FROM batch_rows WHERE batch_id = %s AND row = %s",
                    (batch_id, row),
                )
            ).fetchone()
        await self.publisher.publish("row", b, {"seq": b["seq"]} | row_data(r))

    async def _publish_rows(self, batch_id, final_too: bool = False) -> None:
        for r in await self._rows(batch_id):
            if final_too or r["status"] not in FINAL_ROW:
                await self._publish_row(batch_id, r["row"])

    # --- commands

    async def submit(self, cmd: dict) -> None:
        """Store the batch, its snapshot and rows (once), report its status and drive it. A second delivery
        continues the batch it already has."""
        d = cmd["data"]
        b = d["batch_id"]
        async with _lock(self.batch_locks, b):
            async with self.pool.connection() as conn, conn.transaction():
                cur = await conn.execute(
                    "INSERT INTO batches (batch_id, owner_id, session_id, snapshot, trace) "
                    "VALUES (%s, %s, %s, %s, %s) ON CONFLICT (batch_id) DO NOTHING",
                    (
                        b,
                        cmd["owner_id"],
                        d["session_id"],
                        Jsonb(d["snapshot"]),
                        Jsonb(cmd["trace"]),
                    ),
                )
                if cur.rowcount:
                    async with conn.cursor() as c:
                        await c.executemany(
                            "INSERT INTO batch_rows (batch_id, row, inputs) VALUES (%s, %s, %s)",
                            [(b, r["row"], Jsonb(r["inputs"])) for r in d["rows"]],
                        )
            await self._publish_batch(b)
        await self.kick(b)

    async def pause(self, batch_id) -> None:
        async with _lock(self.batch_locks, batch_id):
            if await self._one(
                "UPDATE batches SET control = 'pause', status = 'paused', reason = %s "
                "WHERE batch_id = %s AND status <> ALL(%s) AND control <> 'stop' RETURNING 1",
                (PAUSED_BY_YOU, batch_id, list(FINAL_BATCH)),
            ):
                await self._publish_batch(batch_id)

    async def resume(self, batch_id) -> None:
        async with _lock(self.batch_locks, batch_id):
            if not await self._one(
                "UPDATE batches SET control = 'run', status = 'running', reason = NULL "
                "WHERE batch_id = %s AND status <> ALL(%s) AND control <> 'stop' RETURNING 1",
                (batch_id, list(FINAL_BATCH)),
            ):
                return
            await self._publish_batch(batch_id)
            await self._publish_rows(batch_id)
        await self.kick(batch_id)

    async def stop(self, batch_id) -> None:
        """Rows never started (and not being driven now) end cancelled; the others end with their runs,
        which studio has cancelled. A second delivery publishes the cancelled rows again."""
        async with _lock(self.batch_locks, batch_id):
            if not await self._one(
                "UPDATE batches SET control = 'stop' WHERE batch_id = %s AND status <> ALL(%s) "
                "RETURNING 1",
                (batch_id, list(FINAL_BATCH)),
            ):
                return
            for r in await self._rows(batch_id):
                if r["status"] == "queued" and thread_id(batch_id, r["row"]) not in self.tasks:
                    await self._end_row(batch_id, r["row"], r["attempt"], {"status": "cancelled"})
                elif r["status"] != "cancelled" or r["run_id"]:
                    continue
                await self._publish_row(batch_id, r["row"])
        await self._after_row(batch_id)

    async def retry(self, batch_id, row: int, attempt: int) -> None:
        """Run a row again as `attempt` (a new key, so a new run); a done batch runs again until it ends. A
        second delivery (the first may have failed after its update) publishes and drives the row again."""
        async with _lock(self.batch_locks, batch_id):
            r = await self._row(batch_id, row)
            if (
                not r
                or r["attempt"] > attempt
                or (r["attempt"] == attempt and r["status"] != "queued")
            ):
                return
            await self._one(
                "UPDATE batch_rows SET attempt = %s, status = 'queued', run_id = NULL, take_ids = NULL, "
                "error = NULL WHERE batch_id = %s AND row = %s AND attempt < %s RETURNING 1",
                (attempt, batch_id, row, attempt),
            )
            await self._one(
                "UPDATE batches SET status = 'running', control = 'run', reason = NULL "
                "WHERE batch_id = %s AND status = 'done' RETURNING 1",
                (batch_id,),
            )
            await self._publish_batch(batch_id)
            await self._publish_row(batch_id, row)
        await self.kick(batch_id)  # the older attempt's thread is deleted when the row is driven

    async def start(self) -> None:
        """After a (re)start: running batches are reported paused for the user's Resume, a stop in progress
        finishes, every row is published again (a row's end may never have been), and a batch whose rows have
        all ended ends."""
        for b in await self._all(
            "SELECT batch_id, control FROM batches WHERE status <> ALL(%s) ORDER BY created_at",
            (list(FINAL_BATCH),),
        ):
            batch_id = str(b["batch_id"])
            if b["control"] == "stop":
                await self.stop(batch_id)
                continue
            async with _lock(self.batch_locks, batch_id):
                if await self._one(
                    "UPDATE batches SET control = 'pause', status = 'paused', reason = %s "
                    "WHERE batch_id = %s AND status = 'running' RETURNING 1",
                    (RESTARTED, batch_id),
                ):
                    await self._publish_batch(batch_id)
                await self._publish_rows(batch_id, final_too=True)
            await self.kick(batch_id)

    async def close(self) -> None:
        """Stop driving rows (as a crash would: threads continue from their checkpoints next time)."""
        tasks = list(self.tasks.values())
        for t in tasks:
            t.cancel()
        await asyncio.gather(*tasks, return_exceptions=True)

    # --- rows

    async def kick(self, batch_id) -> None:
        """Drive the batch's released queued rows not being driven yet, if it may run, keeping at most
        `window` rows asked of studio and not ended; end the batch when every row has ended (also when that
        end was never followed up: a lost publish, a crash)."""
        async with _lock(self.batch_locks, batch_id):
            batch = await self._batch(batch_id)
            if not batch or batch["status"] in FINAL_BATCH:
                return
            rows = await self._rows(batch_id)
            ended = all(r["status"] in FINAL_ROW for r in rows)
            if not ended and batch["control"] == "run":
                busy = sum(
                    r["status"] == "running" or thread_id(batch_id, r["row"]) in self.tasks
                    for r in rows
                )
                for r in rows:
                    t = thread_id(batch_id, r["row"])
                    if r["status"] != "queued" or t in self.tasks:
                        continue
                    if (r["row"] != 1 and not batch["released"]) or busy >= self.window:
                        break
                    busy += 1
                    self.tasks[t] = task = asyncio.create_task(self._drive(batch_id, r["row"]))
                    task.add_done_callback(
                        lambda _, t=t, task=task: self.tasks.get(t) is task and self.tasks.pop(t)
                    )
        if ended:
            await self._after_row(batch_id)

    def _context(self, batch: dict) -> graph.Context:
        return graph.Context(
            create_run=self.create_run,
            owner_id=str(batch["owner_id"]),
            session_id=str(batch["session_id"]),
            workflow_id=batch["snapshot"]["workflow_id"],
        )

    async def _drive(self, batch_id, row: int) -> None:
        """Run the row's thread until it parks or ends: start it, or continue it from its checkpoint. A failure
        (database or NATS down) is tried again with backoff; each try reads the batch's control again."""
        t, delay = thread_id(batch_id, row), 1.0
        while True:
            try:
                async with self.slots, _lock(self.row_locks, t):
                    await self._drive_locked(batch_id, row)
                return
            except Exception:
                log.exception("row %s failed to run; trying again in %.0f s", t, delay)
            await asyncio.sleep(delay)
            delay = min(delay * 2, BACKOFF_MAX)

    async def _drive_locked(self, batch_id, row: int) -> None:
        t, cfg = thread_id(batch_id, row), _cfg(batch_id, row)
        r = await self._row(batch_id, row)
        if not r:
            return
        if (
            r["status"] in FINAL_ROW
        ):  # ended meanwhile, or by a try whose follow-up failed: follow it up
            await self._publish_row(batch_id, row)
            await self._after_row(batch_id)
            await self.kick(batch_id)
            return
        snap = await self.graph.aget_state(cfg)
        if snap.values and snap.values.get("attempt") != r["attempt"]:  # an older attempt's thread
            await self.saver.adelete_thread(t)
            snap = await self.graph.aget_state(cfg)
        if snap.next == ("wait",):
            await self._settle(batch_id, r, snap)
            return
        cancelled = False
        async with _lock(
            self.batch_locks, batch_id
        ):  # the control now, as kick and stop see self.tasks
            batch = await self._batch(batch_id)
            if batch["control"] != "run" and self.tasks.get(t) is asyncio.current_task():
                del self.tasks[t]  # not driven: kick (Resume) or stop sees this row again
            if batch["control"] == "stop":  # never started: cancelled
                cancelled = await self._end_row(
                    batch_id, row, r["attempt"], {"status": "cancelled"}
                )
                await self._publish_row(batch_id, row)
        if cancelled:
            await self._after_row(batch_id)
        if batch["control"] != "run":
            return
        if not snap.values:
            inp = graph.initial(str(batch_id), row, r["attempt"], r["inputs"])
        elif snap.next:
            inp = None  # continue from the last checkpoint
        else:
            inp = ...  # the thread ended but its end was never recorded
        if inp is not ...:
            await self.graph.ainvoke(inp, cfg, context=self._context(batch), durability="sync")
            snap = await self.graph.aget_state(cfg)
        await self._settle(batch_id, r, snap)

    async def on_run_finished(self, data: dict) -> bool:
        """Resume the thread waiting on this run with its result. False (drop it) when no thread waits on it:
        a second delivery, a result already taken from the studio.runs.create reply, an older attempt. A
        second delivery for a thread that ended on this run follows its end up again (its first follow-up may
        have failed)."""
        batch_id, row = data["batch_id"], data["row"]
        attempt = int(data["idempotency_key"].split(":")[2])
        async with _lock(self.row_locks, thread_id(batch_id, row)):
            batch, r = await self._batch(batch_id), await self._row(batch_id, row)
            if not batch or not r:
                return False
            cfg = _cfg(batch_id, row)
            snap = await self.graph.aget_state(cfg)
            v = snap.values
            if v.get("run_id") != data["run_id"] or v.get("attempt") != attempt:
                return False
            if snap.next != ("wait",):
                if not snap.next and v.get("status"):
                    await self._settle(batch_id, r, snap)
                return False
            result = {"status": data["status"], "take_ids": data["take_ids"]}
            result["error"] = data.get("error")
            await self.graph.ainvoke(
                Command(resume=result), cfg, context=self._context(batch), durability="sync"
            )
            await self._settle(batch_id, r, await self.graph.aget_state(cfg))
            return True

    async def _settle(self, batch_id, r: dict, snap) -> None:
        """Record and publish where the thread stands: parked on its run (running) or ended (then follow the
        end up: the batch may end or pause, more rows may go)."""
        v, row = snap.values, r["row"]
        if v.get("attempt") != r["attempt"]:
            return
        if snap.next == ("wait",):
            if r["status"] != "running" or str(r["run_id"]) != v["run_id"]:
                await self._one(
                    "UPDATE batch_rows SET status = 'running', run_id = %s WHERE batch_id = %s "
                    "AND row = %s AND attempt = %s AND status <> ALL(%s) RETURNING 1",
                    (v["run_id"], batch_id, row, r["attempt"], list(FINAL_ROW)),
                )
                await self._publish_row(batch_id, row)
        elif not snap.next and v.get("status"):
            await self._end_row(batch_id, row, r["attempt"], v)
            await self._publish_row(batch_id, row)
            await self._after_row(batch_id)
            await self.kick(batch_id)

    async def _end_row(self, batch_id, row: int, attempt: int, end: dict) -> bool:
        """Make the row final ({status, run_id?, take_ids?, error?}), once (the caller publishes it); counts
        failures in a row and releases every row when row 1 ends. False when it was already final."""
        status, error = end["status"], end.get("error")
        async with self.pool.connection() as conn, conn.transaction():
            if not await (
                await conn.execute(
                    "UPDATE batch_rows SET status = %s, run_id = %s, take_ids = %s, error = %s "
                    "WHERE batch_id = %s AND row = %s AND attempt = %s AND status <> ALL(%s) "
                    "RETURNING 1",
                    (
                        status,
                        end.get("run_id"),
                        Jsonb(end.get("take_ids") or []),
                        error,
                        batch_id,
                        row,
                        attempt,
                        list(FINAL_ROW),
                    ),
                )
            ).fetchone():
                return False
            if status == "failed":
                await conn.execute(
                    "UPDATE batches SET fail_streak = CASE WHEN fail_error = %s "
                    "THEN fail_streak + 1 ELSE 1 END, fail_error = %s WHERE batch_id = %s",
                    (error, error, batch_id),
                )
            else:
                await conn.execute(
                    "UPDATE batches SET fail_streak = 0, fail_error = NULL WHERE batch_id = %s",
                    (batch_id,),
                )
            if row == 1:
                await conn.execute(
                    "UPDATE batches SET released = true WHERE batch_id = %s", (batch_id,)
                )
        return True

    async def _after_row(self, batch_id) -> None:
        """After a row ended: the batch ends when every row has (its threads are deleted), or pauses when
        STREAK rows in a row failed with the same error. On a batch that has ended, its end is published and
        its threads deleted again (the first time may have failed)."""
        async with _lock(self.batch_locks, batch_id):
            batch = await self._batch(batch_id)
            rows = await self._rows(batch_id)
            if batch["status"] in FINAL_BATCH or all(r["status"] in FINAL_ROW for r in rows):
                status = batch["status"]
                if status not in FINAL_BATCH:
                    status = "stopped" if batch["control"] == "stop" else "done"
                    await self._one(
                        "UPDATE batches SET status = %s, reason = NULL WHERE batch_id = %s "
                        "RETURNING 1",
                        (status, batch_id),
                    )
                await self._publish_batch(batch_id)
                # ponytail: a crash between here and the deletes leaves those threads behind (harmless)
                for r in rows:
                    await self.saver.adelete_thread(thread_id(batch_id, r["row"]))
                log.info("batch %s %s", batch_id, status)
            elif batch["fail_streak"] >= STREAK and batch["control"] == "run":
                reason = f"{STREAK} rows in a row failed: {batch['fail_error']}"[:2000]
                await self._one(
                    "UPDATE batches SET control = 'pause', status = 'paused', reason = %s "
                    "WHERE batch_id = %s RETURNING 1",
                    (reason, batch_id),
                )
                await self._publish_batch(batch_id)
