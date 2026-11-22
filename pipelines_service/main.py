"""pipelines_service: `python main.py`. Runs batches (one workflow per row) as LangGraph threads.

Opens the `pipelines` database (migrations, then LangGraph's checkpoint tables), reports every batch it had
running as paused (engine.start), then pulls cmd.pipeline.> from CMD (durable `pipelines`, one at a time, in
order) and evt.studio.> from EVT (durable `pipelines-runs`), and answers pipelines.state.get and pipelines.memory
(LangGraph's Postgres store). LangSmith tracing is forced off (plan 16.4: no network call). SIGINT/SIGTERM stop pulling and exit; threads
continue from their checkpoints on the next start (after the user's Resume).

Stage B: publishes the custom nodes (registry `pipelines.engine.custom`), answers pipelines.providers.test, sends
queued remote calls (remote.py; only with PIPELINES_REMOTE_MODELS on and a pipeline that enables them) and is the
`server` env runner of each host in PIPELINES_SERVER_HOSTS (server.py). Agent tools reach studio by studio.tools.
API 1.28: answers pipelines.judge.test (judge_test.py). API 1.43: names untitled sessions (names.py). API 1.42: the
in-app assistant (assistant.py): pulls cmd.assistant.> from CMD (durable `pipelines-assistant`, chats handled
concurrently, each in order) and answers pipelines.assistant.memory (assistant_memory.py)."""

import asyncio
import contextlib
import logging
import os
import signal
from pathlib import Path

# plan 16.4: tracing forced off, before anything imports langsmith (it reads LANGSMITH_ then LANGCHAIN_, _V2 first)
for _v in (
    "LANGSMITH_TRACING_V2",
    "LANGSMITH_TRACING",
    "LANGCHAIN_TRACING_V2",
    "LANGCHAIN_TRACING",
):
    os.environ[_v] = "false"

import nats
import nats.errors
from langgraph.checkpoint.postgres.aio import AsyncPostgresSaver
from langgraph.checkpoint.serde.jsonplus import JsonPlusSerializer
from langgraph.store.postgres.aio import AsyncPostgresStore
from nats.js.api import AckPolicy, ConsumerConfig, DeliverPolicy
from psycopg.rows import dict_row
from psycopg_pool import AsyncConnectionPool

import assistant
import assistant_memory
import config
import consumer
import custom
import events
import health
import judge_test
import memory
import names
import providers
import server as server_env
import states
from engine import Engine
from messaging.publisher import Publisher
from messaging.studio import Studio

log = logging.getLogger("pipelines")

MIGRATIONS = Path(__file__).resolve().parent / "migrations"


async def open_pool(url: str) -> AsyncConnectionPool:
    """The Postgres pool (dict rows, autocommit, as AsyncPostgresSaver needs), waiting up to 30 s."""
    pool = AsyncConnectionPool(
        url,
        open=False,
        min_size=1,
        max_size=config.DRIVE_CONCURRENCY + 4,
        kwargs={"autocommit": True, "row_factory": dict_row},
    )
    await pool.open(wait=True, timeout=30)
    return pool


def saver_for(pool) -> AsyncPostgresSaver:
    # strict: restore only LangGraph's safe types (state is plain JSON), so a checkpoint row can't make the
    # service import and call arbitrary code
    return AsyncPostgresSaver(pool, serde=JsonPlusSerializer(allowed_msgpack_modules=None))


async def migrate(pool, saver: AsyncPostgresSaver) -> None:
    """migrations/*.sql in name order (every file on every start), then LangGraph's setup, under a lock."""
    async with pool.connection() as conn:
        await conn.execute("SELECT pg_advisory_lock(hashtext('pipelines_migrate'))")
        try:
            for f in sorted(MIGRATIONS.glob("*.sql")):
                await conn.execute(f.read_text())
            await saver.setup()
            await AsyncPostgresStore(pool).setup()  # memory
        finally:
            await conn.execute("SELECT pg_advisory_unlock(hashtext('pipelines_migrate'))")


async def pull(sub, handle, stop: asyncio.Event, batch: int) -> None:
    """Fetch and handle deliveries one by one until `stop`."""
    while not stop.is_set():
        try:
            msgs = await sub.fetch(batch, timeout=2)
        except nats.errors.TimeoutError:
            continue
        except Exception:
            log.exception("fetch failed; retrying")
            with contextlib.suppress(TimeoutError):
                await asyncio.wait_for(stop.wait(), 2)
            continue
        for m in msgs:
            await handle(m)


async def durable(js, stream: str, name: str, subject: str, ack_wait: int, max_ack: int, **kw):
    """Create or update a durable pull consumer and bind to it."""
    await js.add_consumer(
        stream,
        ConsumerConfig(
            name=name,
            durable_name=name,
            filter_subject=subject,
            ack_policy=AckPolicy.EXPLICIT,
            ack_wait=ack_wait,
            max_ack_pending=max_ack,
            deliver_policy=DeliverPolicy.ALL,
            **kw,
        ),
    )
    return await js.pull_subscribe_bind(durable=name, stream=stream)


async def main() -> None:
    logging.basicConfig(
        level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s: %(message)s"
    )
    pool = await open_pool(config.DATABASE_URL)
    nc = await nats.connect(
        config.NATS_URL,
        name="pipelines",
        max_reconnect_attempts=-1,
        inbox_prefix=config.INBOX_PREFIX,
    )
    try:
        saver = saver_for(pool)
        await migrate(pool, saver)
        js = nc.jetstream()
        store = AsyncPostgresStore(pool)
        studio = Studio(nc)
        engine = Engine(
            pool,
            saver,
            Publisher(js),
            studio.create_run,
            config.DRIVE_CONCURRENCY,
            store=store,
            tool=studio.tool,
        )
        await custom.publish(js)
        await engine.start()
        cmds = await durable(
            js,
            "CMD",
            "pipelines",
            "cmd.pipeline.>",
            consumer.ACK_WAIT,
            1,  # one command at a time, in order
            max_deliver=-1,  # redelivered until handled (every handler is idempotent)
        )
        runs = await durable(js, "EVT", "pipelines-runs", "evt.studio.>", events.ACK_WAIT, 64)
        namer = names.Names(js, studio.create_run)  # API 1.43: session names
        helper = assistant.Assistant(pool, saver, store, js, studio.tool)
        asks = await durable(js, "CMD", "pipelines-assistant", "cmd.assistant.>", assistant.ACK_WAIT, 16,
                             max_deliver=-1)  # fmt: skip
        server = await health.serve(nc, pool)
        state_sub = await states.serve(nc, engine)
        memory_sub = await memory.serve(nc, store)
        test_sub = await providers.serve(nc)
        judge_sub = await judge_test.serve(nc, store)
        assistant_sub = await assistant_memory.serve(nc, store)
        executors = [server_env.Executor(js, pool, h.strip()) for h in config.SERVER_HOSTS]
        server_subs = [await x.start() for x in executors]

        stop = asyncio.Event()
        loop = asyncio.get_running_loop()
        for sig in (signal.SIGINT, signal.SIGTERM):
            loop.add_signal_handler(sig, stop.set)
        log.info("pipelines_service ready")
        await asyncio.gather(
            pull(cmds, consumer.Commands(engine, pool, js, namer).handle, stop, 1),
            pull(runs, events.RunEvents(engine, namer).handle, stop, 16),
            assistant.pull(asks, assistant.Commands(helper, pool, js).handle, stop),
            engine.remote.run(stop),
            *[pull(sub, x.handle, stop, 1) for x, sub in zip(executors, server_subs)],
            *[x.beat(stop) for x in executors],
        )
        log.info("shutting down")
        server.close()
        await state_sub.unsubscribe()
        await memory_sub.unsubscribe()
        await test_sub.unsubscribe()
        await judge_sub.unsubscribe()
        await assistant_sub.unsubscribe()
        await engine.close()
    finally:
        # close, not drain: every publish was awaited; draining pull subscriptions is slow
        await nc.close()
        await pool.close()


if __name__ == "__main__":
    asyncio.run(main())
