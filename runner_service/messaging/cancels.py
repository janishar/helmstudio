"""KV `cancels`: studio writes a run's id as a key to cancel it (the value, the request time, is ignored). The
runner reads the key before it starts a run and watches it while the run executes. Both calls are retried as an
event publish is (publisher.retried): a NATS restart just as a run starts must not fail it."""

import asyncio
from collections.abc import Awaitable, Callable

from nats.js.errors import KeyDeletedError, KeyNotFoundError

from messaging.publisher import retried


async def requested(kv, run_id: str) -> bool:
    """Whether the run's cancel key exists now."""

    async def get() -> bool:
        try:
            await kv.get(run_id)
        except KeyNotFoundError, KeyDeletedError:
            return False
        return True

    return await retried(get)


async def watch(kv, run_id: str, on_cancel: Callable[[], None]) -> Callable[[], Awaitable[None]]:
    """Call `on_cancel` when the run's cancel key is written. Returns the coroutine function that
    stops watching."""
    watcher = await retried(lambda: kv.watch(run_id, ignore_deletes=True))

    async def loop() -> None:
        async for entry in watcher:  # None marks the end of the initial values
            if entry is not None:
                on_cancel()

    task = asyncio.create_task(loop())

    async def stop() -> None:
        await watcher.stop()
        await task

    return stop
