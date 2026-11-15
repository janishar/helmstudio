"""The batch engine: one LangGraph thread per row (graph.py, built from the batch's plan: pipeline.py), driven by
commands (consumer.py) and run_finished events (events.py).

- Two records here: `batches` / `batch_rows` (what was submitted, control, each row's last published state, the
  event seq, results and picks waiting for their thread) and LangGraph's checkpoints (where each thread is).
  Studio's batch record is built from the evt.pipeline.* events published here; each event is a whole state with
  a seq committed before the publish.
- A batch's graph is compiled once from its snapshot (cached per batch, dropped when it ends).
- Row 1 first: until row 1 has ended or parked at a gate, only row 1 is driven; then every row (`released`), at
  most WINDOW rows of a batch asked of studio and not ended or waiting (each row end or park lets the next one
  go). A picked row resumes through that window too (before queued rows), so a burst of picks never floods studio. At most DRIVE_CONCURRENCY rows run graph code at once; a parked row holds nothing.
- Only a drive task (`_drive`, one per row at a time, `self.tasks`) runs a row's graph, under the row's lock. A
  run_finished or a pick only stores its value (batch_rows.results / .pick) and pokes the row (a pick: through
  kick's window): the drive resumes
  the parked thread once it has every result of the step's visit, or the pick of that gate visit. A poke while the
  row is being driven makes the drive go round again (`self.again`), so nothing is missed. A drive that fails
  (database or NATS down) is tried again with backoff. Resumes go on while paused (the next runs are held by
  studio); a row that would start or continue on its own waits for Resume.
- Every row end is followed up (published, the batch ended or paused, the next rows driven) again whenever it is
  seen again: a redelivered run_finished, a retried drive, a restart (which publishes every row again).
- Pause starts no new rows. Stop cancels rows that never started and rows parked at a gate; the rest end with
  their (cancelled) runs; `stopped` when every row is final.
- Restart (`start`): every running batch is reported paused and waits for Resume; run_finished events and picks
  still resume parked threads meanwhile. On Resume every unfinished row is driven again from its checkpoint.
- Pipeline batches publish evt.pipeline.step for every graph step, read from the thread's checkpoint history
  (batch_rows.step_pub = the last one published), and `current` for the step a row is parked on.
- Retry from a step forks the thread from that step's checkpoint with the new attempt (aupdate_state); a retry
  from the start deletes the thread. When a batch ends its threads are deleted; the batch and its snapshot stay.
- Agent nodes (graph.py, agents.py): an LLM call waits like a step's run (its result carries the `answer`); a
  judge in Suggest parks like a gate (its cell and `parked` come from the thread). Every decision published in a
  step event is stored in `decisions` first: the replay source (Context.replay) for Resume, Retry and Run again
  with the same decisions (`replay_from`). Memory (memory.py): the namespace is copied onto the batch at Submit
  (every read of the batch uses that copy); memory write nodes and picks with a note write the live namespace."""

import asyncio
import logging
import weakref
from collections.abc import Awaitable, Callable
from datetime import UTC, datetime

from langgraph.store.postgres.aio import AsyncPostgresStore
from langgraph.types import Command
from psycopg.types.json import Jsonb

import graph
import memory
import pipeline

log = logging.getLogger(__name__)

RESTARTED = "the pipeline service restarted; press Resume"
PAUSED_BY_YOU = "paused by you"
STATE_GONE = "the state to retry from is gone; retry the whole row"
FINAL_ROW = ("succeeded", "failed", "cancelled")
FINAL_BATCH = ("done", "failed", "stopped")
STREAK = 3  # rows failing in a row with one error pause the batch
WINDOW = 16  # rows of one batch asked of studio and not ended or waiting
BACKOFF_MAX = 30.0  # seconds between tries of a row whose drive failed


def thread_id(batch_id, row: int) -> str:
    return f"{batch_id}:{row}"


def _cfg(batch_id, row: int, checkpoint_id: str | None = None) -> dict:
    c = {"thread_id": thread_id(batch_id, row)}
    if checkpoint_id:
        c |= {"checkpoint_ns": "", "checkpoint_id": checkpoint_id}
    return {"configurable": c}


def _lock(locks: weakref.WeakValueDictionary, key: str) -> asyncio.Lock:
    """The lock of `key`; it lives while someone holds or waits on it (so the dictionaries never grow)."""
    lock = locks.get(key)
    if lock is None:
        locks[key] = lock = asyncio.Lock()
    return lock


def row_data(r: dict, is_pipeline: bool = False) -> dict:
    """evt.pipeline.row data (without seq) from a batch_rows record."""
    d = {"row": r["row"], "attempt": r["attempt"], "status": r["status"]}
    if r["run_id"]:
        d["run_id"] = str(r["run_id"])
    if r["status"] == "succeeded" and r["take_ids"] is not None:
        d["take_ids"] = r["take_ids"]
    if r["error"]:
        d["error"] = r["error"][:2000]
    if is_pipeline:
        d["node"] = r.get("node")
        d["cells"] = r.get("cells") or []
        d["parked"] = r.get("parked") if r["status"] == "waiting" else None
    return d


def _parked_at(snap) -> tuple[str, str, dict] | None:
    """(node, part, interrupt value) when the thread is parked on a step's wait or a gate."""
    if not snap.interrupts or not snap.next:
        return None
    nid, part = snap.next[0].split(".")
    return nid, part, snap.interrupts[0].value


def _picked(r: dict) -> bool:
    """The row waits at a gate whose pick is stored: it resumes once the window has room."""
    p, k = r["pick"], r["parked"]
    return (
        r["status"] == "waiting"
        and bool(p and k)
        and p["gate"] == k["gate"]
        and p["visit"] == k["visit"]
    )


def _resume_value(r: dict, at) -> dict | None:
    """What resumes a parked thread: every result of the visit's runs, or the pick of that gate visit."""
    _, part, val = at
    if part == "wait":
        res = r["results"] or {}
        if all(rid in res for rid in val["runs"]):
            return {rid: res[rid] for rid in val["runs"]}
    elif (p := r["pick"]) and p["gate"] == val["gate"] and p["visit"] == val["visit"]:
        out = {"take_ids": p["take_ids"]} if p.get("take_ids") else {"reject": True}
        return out | ({"agreed": p["agreed"]} if "agreed" in p else {})
    return None


class Engine:
    def __init__(
        self,
        pool,
        saver,
        publisher,
        create_run: Callable[[dict], Awaitable[dict]],
        concurrency: int = 8,
        window: int = WINDOW,
        store=None,
    ) -> None:
        self.pool, self.saver, self.publisher, self.create_run = pool, saver, publisher, create_run
        self.store = store if store is not None else AsyncPostgresStore(pool)  # memory
        self.graphs: dict[str, tuple[dict, object]] = {}  # batch id → (plan, compiled graph)
        self.row_locks: weakref.WeakValueDictionary = weakref.WeakValueDictionary()
        self.batch_locks: weakref.WeakValueDictionary = weakref.WeakValueDictionary()
        self.tasks: dict[str, asyncio.Task] = {}  # thread id → its drive (under the batch's lock)
        self.again: set[str] = set()  # thread ids poked while being driven
        self.unsettled: set[str] = set()  # parks recorded, maybe not yet published
        self.slots = asyncio.Semaphore(concurrency)
        self.window = window

    def graph(self, batch: dict) -> tuple[dict, object]:
        """The batch's plan and compiled graph (compiled once per batch)."""
        b = str(batch["batch_id"])
        if b not in self.graphs:
            plan = pipeline.plan(batch["snapshot"])
            self.graphs[b] = plan, graph.build(plan).compile(checkpointer=self.saver)
        return self.graphs[b]

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

    async def _next_seq(self, conn, batch_id) -> dict:
        return await (
            await conn.execute(
                "UPDATE batches SET seq = seq + 1, updated_at = now() WHERE batch_id = %s "
                "RETURNING *",
                (batch_id,),
            )
        ).fetchone()

    async def _publish_batch(self, batch_id) -> None:
        async with self.pool.connection() as conn, conn.transaction():
            b = await self._next_seq(conn, batch_id)
        data = {"seq": b["seq"], "status": b["status"]}
        if b["reason"]:
            data["reason"] = b["reason"][:2000]
        if b["memory_at"]:
            data["memory"] = memory.snapshot_counts(b["memory"], b["memory_at"])
        await self.publisher.publish("batch", b, data)

    async def _publish_row(self, batch_id, row: int) -> None:
        async with self.pool.connection() as conn, conn.transaction():
            b = await self._next_seq(conn, batch_id)
            r = await (
                await conn.execute(
                    "SELECT * FROM batch_rows WHERE batch_id = %s AND row = %s",
                    (batch_id, row),
                )
            ).fetchone()
        data = {"seq": b["seq"]} | row_data(r, "pipeline" in b["snapshot"])
        await self.publisher.publish("row", b, data)

    async def _publish_rows(self, batch_id, final_too: bool = False) -> None:
        for r in await self._rows(batch_id):
            if final_too or r["status"] not in FINAL_ROW:
                await self._publish_row(batch_id, r["row"])

    async def _publish_step(self, batch_id, r: dict, data: dict, step_pub: int | None) -> None:
        """One evt.pipeline.step; with step_pub, record it as the last finished step published."""
        async with self.pool.connection() as conn, conn.transaction():
            if dec := data.get("decision"):  # the replay source, stored before anyone can see it
                await conn.execute(
                    "INSERT INTO decisions (decision_id, batch_id, replay_key, decision) "
                    "VALUES (%s, %s, %s, %s) ON CONFLICT (decision_id) DO UPDATE SET decision = "
                    "EXCLUDED.decision, replay_key = EXCLUDED.replay_key",
                    (dec["id"], batch_id, dec["replay_key"], Jsonb(dec)),
                )
            b = await self._next_seq(conn, batch_id)
        base = {"seq": b["seq"], "row": r["row"], "attempt": r["attempt"]}
        await self.publisher.publish("step", b, base | data)
        if step_pub is not None:  # once published: a failed one is published on the next try
            await self._one(
                "UPDATE batch_rows SET step_pub = %s WHERE batch_id = %s AND row = %s "
                "AND attempt = %s RETURNING 1",
                (step_pub, batch_id, r["row"], r["attempt"]),
            )

    async def _publish_steps(self, batch_id, r: dict, plan: dict, g, snap) -> None:
        """Every graph step of the row's attempt not yet published, from the checkpoint history, then the
        step it is parked on (`current`)."""
        if not plan["pipeline"]:
            return
        done = (await self._row(batch_id, r["row"]))["step_pub"]
        new, seen = [], set()
        async for s in g.aget_state_history(_cfg(batch_id, r["row"])):
            v = s.values
            if v.get("attempt") != r["attempt"] or not v.get("last"):
                continue
            if v["n"] <= done:
                break
            if v["n"] not in seen:
                seen.add(v["n"])
                new.append(s)
        for s in reversed(new):
            v = s.values
            data = {"step": v["n"], **v["last"], "loops": self._loops(plan, v)}
            data["checkpoint_id"] = s.config["configurable"]["checkpoint_id"]
            await self._publish_step(batch_id, r, data, v["n"])

    def _loops(self, plan: dict, v: dict) -> dict:
        return {g: (v.get("loops") or {}).get(g, 0) for g in plan["gates"]}

    async def _publish_current(self, batch_id, r: dict, plan: dict, snap, at) -> None:
        if not plan["pipeline"]:
            return
        v, (nid, part, val) = snap.values, at
        if part == "wait":
            n = len(val["runs"])
            what, visit, runs = f"Waiting for {n} run{'s' * (n > 1)}", v["visits"][nid], val["runs"]
        else:
            what, visit, runs = "Waiting for a pick", val["visit"], []
        data = {"step": v.get("n", 0) + 1, "node": nid, "part": part, "visit": visit}
        data |= {"status": "current", "what": what, "changed": [], "edge": None}
        data |= {"loops": self._loops(plan, v), "run_ids": runs[:8], "take_ids": []}
        data |= {"error": None, "checkpoint_id": None}
        await self._publish_step(batch_id, r, data, None)

    # --- commands

    async def submit(self, cmd: dict) -> None:
        """Store the batch, its snapshot and rows (once), report its status and drive it. A pipeline the engine
        cannot run fails the batch. A second delivery continues the batch it already has."""
        d = cmd["data"]
        b = d["batch_id"]
        pid = (d["snapshot"].get("pipeline") or {}).get("id")
        async with _lock(self.batch_locks, b):
            # the memory snapshot every read of the batch uses (kept from the first delivery); Run again with the
            # same decisions reuses its source's, so the prompts and replay keys match the source's decisions
            snap, taken = None, None
            if pid and (src := d.get("replay_from")):
                old = await self._one(
                    "SELECT memory, memory_at FROM batches WHERE batch_id = %s AND owner_id = %s",
                    (src, cmd["owner_id"]),
                )
                if old and old["memory_at"]:
                    snap, taken = old["memory"], old["memory_at"]
            if pid and taken is None:
                snap, taken = (
                    await memory.items(self.store, cmd["owner_id"], pid),
                    datetime.now(UTC),
                )
            async with self.pool.connection() as conn, conn.transaction():
                cur = await conn.execute(
                    "INSERT INTO batches (batch_id, owner_id, session_id, snapshot, trace, replay_from, "
                    "memory, memory_at) VALUES (%s, %s, %s, %s, %s, %s, %s, %s) "
                    "ON CONFLICT (batch_id) DO NOTHING",
                    (
                        b,
                        cmd["owner_id"],
                        d["session_id"],
                        Jsonb(d["snapshot"]),
                        Jsonb(cmd["trace"]),
                        d.get("replay_from"),
                        Jsonb(snap) if pid else None,
                        taken,
                    ),
                )
                if cur.rowcount:
                    async with conn.cursor() as c:
                        await c.executemany(
                            "INSERT INTO batch_rows (batch_id, row, inputs, seeds) "
                            "VALUES (%s, %s, %s, %s)",
                            [
                                (b, r["row"], Jsonb(r["inputs"]), Jsonb(r.get("seeds") or {}))
                                for r in d["rows"]
                            ],
                        )
                    p = d["snapshot"].get("pipeline")
                    if bad := p and sorted(pipeline.problems(p), key=str):
                        code, node, port, _ = bad[0]
                        where = f" at {node}" + (f".{port}" if port else "") if node else ""
                        await conn.execute(
                            "UPDATE batches SET status = 'failed', reason = %s WHERE batch_id = %s",
                            (f"pipeline cannot run: {code}{where}", b),
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
        await self.kick(batch_id, every_open_row=True)

    async def stop(self, batch_id) -> None:
        """Rows never started (and not being driven now) and rows parked at a gate end cancelled; the others
        end with their runs, which studio has cancelled. A second delivery publishes the cancelled rows again."""
        async with _lock(self.batch_locks, batch_id):
            if not await self._one(
                "UPDATE batches SET control = 'stop' WHERE batch_id = %s AND status <> ALL(%s) "
                "RETURNING 1",
                (batch_id, list(FINAL_BATCH)),
            ):
                return
            for r in await self._rows(batch_id):
                t = thread_id(batch_id, r["row"])
                if (r["status"] == "queued" and t not in self.tasks) or r["status"] == "waiting":
                    await self._end_row(batch_id, r["row"], r["attempt"], {"status": "cancelled"})
                elif r["status"] != "cancelled" or r["run_id"]:
                    continue
                await self._publish_row(batch_id, r["row"])
        await self._after_row(batch_id)

    async def retry(
        self, batch_id, row: int, attempt: int, from_step: int | None = None, checkpoint_id=None
    ) -> None:
        """Run a row again as `attempt` (new keys, so new runs): from its start, or with from_step > 1 from the
        checkpoint of the step before it. A done batch runs again until it ends. A second delivery (the first
        may have failed after its update) publishes and drives the row again."""
        fork = checkpoint_id if (from_step or 1) > 1 else None
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
                "error = NULL, node = NULL, parked = NULL, cells = NULL, results = '{}', pick = NULL, "
                "step_pub = 0, fork = %s WHERE batch_id = %s AND row = %s AND attempt < %s RETURNING 1",
                (attempt, fork, batch_id, row, attempt),
            )
            await self._one(
                "UPDATE batches SET status = 'running', control = 'run', reason = NULL "
                "WHERE batch_id = %s AND status = 'done' RETURNING 1",
                (batch_id,),
            )
            await self._publish_batch(batch_id)
            await self._publish_row(batch_id, row)
        await self.kick(
            batch_id
        )  # the older attempt's thread is forked or deleted when the row is driven

    async def pick(self, d: dict) -> bool:
        """cmd.pipeline.pick: store the answer for a row parked on that gate visit and attempt; kick resumes the
        row's thread (paused or not) once the window has room. Anything else is dropped (False): another visit or
        attempt, a row not waiting (resumed already, cancelled), takes that are not 1..keep candidates."""
        b, row = d["batch_id"], d["row"]
        r = await self._row(b, row)
        p = r and r["parked"]
        if not r or r["status"] != "waiting" or r["attempt"] != d["attempt"] or not p:
            return False
        if p["gate"] != d["gate"] or p["visit"] != d["visit"]:
            return False
        ids = d.get("take_ids")
        if ids and (
            len(set(ids)) != len(ids)
            or len(ids) > p["keep"]
            or not set(ids) <= set(p["candidates"])
        ):
            log.warning(
                "pick for %s rejected: not 1..%d of the candidates", thread_id(b, row), p["keep"]
            )
            return False
        answer = {"gate": d["gate"], "visit": d["visit"]} | (
            {"take_ids": ids} if ids else {"reject": True}
        )
        if "agreed" in d:
            answer["agreed"] = d["agreed"]
        if d.get("note") and p.get("kind") == "judge":  # a judge example, seen from the next batch
            batch = await self._batch(b)
            pid = (batch["snapshot"].get("pipeline") or {}).get("id")
            iid, value = memory.judge_example(b, row, p, d)
            if pid and (err := await memory.put(self.store, batch["owner_id"], pid, iid, value)):
                log.warning("pipeline %s: the pick's note is not kept: %s", pid, err["error"])
        await self._one(
            "UPDATE batch_rows SET pick = %s WHERE batch_id = %s AND row = %s AND attempt = %s "
            "AND status = 'waiting' RETURNING 1",
            (Jsonb(answer), b, row, d["attempt"]),
        )
        await self.kick(b)
        return True

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

    async def state_get(self, req: dict) -> dict:
        """pipelines.state.get: the row's state at one of its thread's checkpoints, for the batch's owner."""
        gone = {"error": "this row's state is gone", "code": "not_found"}
        b = await self._one(
            "SELECT * FROM batches WHERE batch_id = %s AND owner_id = %s",
            (req["batch_id"], req["owner_id"]),
        )
        if not b or not await self._row(b["batch_id"], req["row"]):
            return gone
        cfg = _cfg(b["batch_id"], req["row"], req["checkpoint_id"])
        if not await self.saver.aget_tuple(cfg):
            return gone
        _, g = self.graph(b)
        snap = await g.aget_state(cfg)
        return {"checkpoint_id": req["checkpoint_id"], "state": snap.values}

    # --- rows

    def _poke(self, batch_id, row: int) -> None:
        """Drive the row (under the batch's lock): start its drive, or make the running one go round again."""
        t = thread_id(batch_id, row)
        if t in self.tasks:
            self.again.add(t)
            return
        self.tasks[t] = task = asyncio.create_task(self._drive(batch_id, row))
        task.add_done_callback(
            lambda _, t=t, task=task: self.tasks.get(t) is task and self.tasks.pop(t)
        )

    async def kick(self, batch_id, every_open_row: bool = False) -> None:
        """Drive the batch's picked rows (paused or not) and then, if it may run, its released queued rows,
        keeping at most `window` rows asked of studio and not ended or waiting (with every_open_row, on Resume,
        every running row too); end the batch when every row has ended (also when that end was never followed
        up: a lost publish, a crash)."""
        async with _lock(self.batch_locks, batch_id):
            batch = await self._batch(batch_id)
            if not batch or batch["status"] in FINAL_BATCH:
                return
            rows = await self._rows(batch_id)
            ended = all(r["status"] in FINAL_ROW for r in rows)
            if not ended and batch["control"] != "stop":
                driven = lambda r: thread_id(batch_id, r["row"]) in self.tasks
                busy = sum(  # asked of studio, or about to be
                    r["status"] == "running"
                    or (driven(r) and (r["status"] == "queued" or _picked(r)))
                    for r in rows
                )
                todo = [r for r in rows if _picked(r)]
                if batch["control"] == "run":
                    todo += [r for r in rows if r["status"] == "queued"]
                for r in todo:
                    if driven(r):
                        self.again.add(thread_id(batch_id, r["row"]))
                        continue
                    if (r["row"] != 1 and not batch["released"]) or busy >= self.window:
                        break
                    busy += 1
                    self._poke(batch_id, r["row"])
                if every_open_row:
                    for r in rows:
                        if r["status"] == "running":
                            self._poke(batch_id, r["row"])
        if ended:
            await self._after_row(batch_id)

    def _context(self, batch: dict) -> graph.Context:
        owner, pid = str(batch["owner_id"]), (batch["snapshot"].get("pipeline") or {}).get("id")

        async def put_note(item_id: str, value: dict) -> str | None:
            err = await memory.put(self.store, owner, pid, item_id, value)
            return err and err["error"]

        return graph.Context(
            self.create_run,
            owner,
            str(batch["session_id"]),
            replay=self._replay,
            replay_batch=str(batch["replay_from"]) if batch["replay_from"] else None,
            memory=batch["memory"] or [],
            put_note=put_note if pid else None,
        )

    async def _replay(self, key: str) -> dict | None:
        """The first stored decision (one that decided) under a replay key: the original, not a replay of it."""
        r = await self._one(
            "SELECT decision FROM decisions WHERE replay_key = %s AND decision->>'error' IS NULL "
            "ORDER BY created_at LIMIT 1",
            (key,),
        )
        return r and r["decision"]

    async def _drive(self, batch_id, row: int) -> None:
        """Run the row's thread until it parks or ends (again while it is poked meanwhile). A failure (database
        or NATS down) is tried again with backoff; each try reads the row and the batch's control again."""
        t, delay = thread_id(batch_id, row), 1.0
        while True:
            try:
                async with self.slots, _lock(self.row_locks, t):
                    await self._drive_locked(batch_id, row)
            except Exception:
                log.exception("row %s failed to run; trying again in %.0f s", t, delay)
                await asyncio.sleep(delay)
                delay = min(delay * 2, BACKOFF_MAX)
                continue
            async with _lock(self.batch_locks, batch_id):
                if self.tasks.get(t) is not asyncio.current_task():
                    return
                if t not in self.again:
                    del self.tasks[t]
                    return
                self.again.discard(t)

    async def _drive_locked(self, batch_id, row: int) -> None:
        t, cfg = thread_id(batch_id, row), _cfg(batch_id, row)
        r = await self._row(batch_id, row)
        if not r:
            return
        if r["status"] in FINAL_ROW:  # ended meanwhile, or by a try whose follow-up failed
            await self._publish_row(batch_id, row)
            await self._after_row(batch_id)
            await self.kick(batch_id)
            return
        batch = await self._batch(batch_id)
        plan, g = self.graph(batch)
        snap = await g.aget_state(cfg)
        if snap.values and snap.values.get("attempt") != r["attempt"]:  # an older attempt's thread
            if r["fork"]:
                if not (snap := await self._fork(batch_id, r, g)):
                    return
            else:
                await self.saver.adelete_thread(t)
                snap = await g.aget_state(cfg)
        at = _parked_at(snap)
        resume = _resume_value(r, at) if at else None
        if at and resume is None:
            await self._settle(batch_id, r, snap)
            return
        if resume is not None:  # a result or a pick resumes the thread, paused or not
            inp = Command(resume=resume)
        else:
            cancelled = False
            async with _lock(
                self.batch_locks, batch_id
            ):  # the control now, as kick and stop see it
                batch = await self._batch(batch_id)
                if batch["control"] != "run" and self.tasks.get(t) is asyncio.current_task():
                    del self.tasks[t]  # not driven: kick (Resume) or stop sees this row again
                    self.again.discard(t)
                if batch["control"] == "stop":  # never started (or never went on): cancelled
                    cancelled = await self._end_row(
                        batch_id, row, r["attempt"], {"status": "cancelled"}
                    )
                    await self._publish_row(batch_id, row)
            if cancelled:
                await self._after_row(batch_id)
            if batch["control"] != "run":
                return
            if not snap.values:
                inp = graph.initial(str(batch_id), row, r["attempt"], r["inputs"], r["seeds"])
            elif snap.next:
                inp = None  # continue from the last checkpoint
            else:
                inp = ...  # the thread ended but its end was never recorded
        if inp is not ...:
            await g.ainvoke(
                inp,
                {**cfg, "recursion_limit": graph.recursion_limit(plan)},
                context=self._context(batch),
                durability="sync",
            )
            snap = await g.aget_state(cfg)
        await self._settle(batch_id, r, snap)

    async def _fork(self, batch_id, r: dict, g):
        """Retry from a step: the thread continues from the checkpoint the retry names, as the new attempt (a
        step's wait or a gate's route is forked from the checkpoint before it, so its runs or pick are new).
        None when that checkpoint is gone: the row fails."""
        row = r["row"]
        src = None
        if await self.saver.aget_tuple(_cfg(batch_id, row, r["fork"])):
            src = await g.aget_state(_cfg(batch_id, row, r["fork"]))
        if not src or not src.values.get("last"):
            if await self._end_row(
                batch_id, row, r["attempt"], {"status": "failed", "error": STATE_GONE}
            ):
                await self._publish_row(batch_id, row)
                await self._after_row(batch_id)
                await self.kick(batch_id)
            return None
        if src.next and src.next[0].endswith((".wait", ".route")) and src.parent_config:
            src = await g.aget_state(src.parent_config)
        last = src.values["last"]
        as_node = last["node"]
        if last["part"] not in ("inputs", "output", "memory"):
            as_node += f".{last['part']}"
        reset = {"attempt": r["attempt"], "status": None, "error": None, "pick": None}
        await g.aupdate_state(src.config, reset, as_node=as_node)
        await self._one(
            "UPDATE batch_rows SET step_pub = %s, fork = NULL WHERE batch_id = %s AND row = %s "
            "AND attempt = %s RETURNING 1",
            (src.values["n"], batch_id, row, r["attempt"]),
        )
        return await g.aget_state(_cfg(batch_id, row))

    async def on_run_finished(self, data: dict) -> bool:
        """Store the run's result for the thread waiting on it and poke the row (its drive resumes the thread
        once every run of the visit has ended). False (drop it) when no thread waits on it: a second delivery,
        a result already taken from the studio.runs.create reply, an older attempt. A delivery for a thread that
        ended on this run follows its end up again (its first follow-up may have failed)."""
        batch_id, row = data["batch_id"], data["row"]
        attempt = int(data["idempotency_key"].split(":")[2])
        async with _lock(self.row_locks, thread_id(batch_id, row)):
            batch, r = await self._batch(batch_id), await self._row(batch_id, row)
            if not batch or not r or r["attempt"] != attempt:
                return False
            _, g = self.graph(batch)
            snap = await g.aget_state(_cfg(batch_id, row))
            v, at = snap.values, _parked_at(snap)
            if v.get("attempt") != attempt:
                return False
            rid = data["run_id"]
            if at and at[1] == "wait" and rid in at[2]["runs"] and rid not in r["results"]:
                result = {
                    k: data.get(k) for k in ("status", "take_ids", "error", "answer") if k in data
                }
                await self._one(
                    "UPDATE batch_rows SET results = results || %s WHERE batch_id = %s "
                    "AND row = %s AND attempt = %s RETURNING 1",
                    (Jsonb({rid: result}), batch_id, row, attempt),
                )
                async with _lock(self.batch_locks, batch_id):
                    self._poke(batch_id, row)
                return True
            if not snap.next and v.get("status"):
                async with _lock(self.batch_locks, batch_id):
                    self._poke(batch_id, row)
            return False

    async def _settle(self, batch_id, r: dict, snap) -> None:
        """Record and publish where the thread stands: its new graph steps, then parked on its runs (running),
        parked at a gate (waiting: the row-parked event; row 1 parking releases every row), or ended (then
        follow the end up: the batch may end or pause, more rows may go)."""
        v, row = snap.values, r["row"]
        t = thread_id(batch_id, row)
        if v.get("attempt") != r["attempt"]:
            return
        batch = await self._batch(batch_id)
        plan, g = self.graph(batch)
        await self._publish_steps(batch_id, r, plan, g, snap)
        at = _parked_at(snap)
        if at:
            nid, part, val = at
            if part == "wait":
                if (
                    r["status"] == "running"
                    and str(r["run_id"]) == v["run_id"]
                    and r["node"] == nid
                    and t not in self.unsettled
                ):
                    return
                status, parked, cells = "running", None, v.get("cells")
            else:
                if r["status"] == "waiting" and r["parked"] == val and t not in self.unsettled:
                    return
                status, parked = "waiting", val
                waiting = {
                    "visit": val["visit"],
                    "status": "waiting",
                    "candidates": val["candidates"],
                }  # a judge's waiting cell (with its verdict) is the thread's
                judge = val.get("kind") == "judge"
                cells = v.get("cells") if judge else graph._cell(v, nid, "gate", waiting)
            self.unsettled.add(t)  # until published and followed up (else the next try does it)
            async with self.pool.connection() as conn, conn.transaction():
                if not await (
                    await conn.execute(
                        "UPDATE batch_rows SET status = %s, run_id = %s, node = %s, parked = %s, "
                        "cells = %s WHERE batch_id = %s AND row = %s AND attempt = %s "
                        "AND status <> ALL(%s) RETURNING 1",
                        (
                            status,
                            v.get("run_id"),
                            nid,
                            Jsonb(parked) if parked else None,
                            Jsonb(cells or []),
                            batch_id,
                            row,
                            r["attempt"],
                            list(FINAL_ROW),
                        ),
                    )
                ).fetchone():
                    self.unsettled.discard(t)
                    return
                if status == "waiting" and row == 1:
                    await conn.execute(
                        "UPDATE batches SET released = true WHERE batch_id = %s", (batch_id,)
                    )
            await self._publish_current(batch_id, r, plan, snap, at)
            await self._publish_row(batch_id, row)
            if status == "waiting":  # its window slot is free
                await self.kick(batch_id)
            self.unsettled.discard(t)
        elif not snap.next and v.get("status"):
            await self._end_row(batch_id, row, r["attempt"], v)
            await self._publish_row(batch_id, row)
            await self._after_row(batch_id)
            await self.kick(batch_id)

    async def _end_row(self, batch_id, row: int, attempt: int, end: dict) -> bool:
        """Make the row final ({status, run_id?, take_ids?, error?, cells?, rejected?}), once (the caller publishes
        it); counts failures in a row (not a rejected row's) and releases every row when row 1 ends. False when it was already final."""
        status, error = end["status"], end.get("error")
        cells = end.get("cells")
        self.unsettled.discard(thread_id(batch_id, row))  # an ended row has no park left to publish
        async with self.pool.connection() as conn, conn.transaction():
            if not await (
                await conn.execute(
                    "UPDATE batch_rows SET status = %s, run_id = COALESCE(%s, run_id), take_ids = %s, "
                    "error = %s, node = NULL, parked = NULL, cells = COALESCE(%s, cells) "
                    "WHERE batch_id = %s AND row = %s AND attempt = %s AND status <> ALL(%s) "
                    "RETURNING 1",
                    (
                        status,
                        end.get("run_id"),
                        Jsonb(end.get("take_ids") or []),
                        error,
                        Jsonb(cells) if cells is not None else None,
                        batch_id,
                        row,
                        attempt,
                        list(FINAL_ROW),
                    ),
                )
            ).fetchone():
                return False
            if status == "failed" and end.get("rejected"):
                pass  # a reviewer's answer, not a broken step: the streak is left as it is
            elif status == "failed":
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
                self.graphs.pop(str(batch_id), None)
                log.info("batch %s %s", batch_id, status)
            elif batch["fail_streak"] >= STREAK and batch["control"] == "run":
                reason = f"{STREAK} rows in a row failed: {batch['fail_error']}"[:2000]
                await self._one(
                    "UPDATE batches SET control = 'pause', status = 'paused', reason = %s "
                    "WHERE batch_id = %s RETURNING 1",
                    (reason, batch_id),
                )
                await self._publish_batch(batch_id)
