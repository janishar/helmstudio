"""KV `hosts`, key <host>.<env>: this runner's live state (idle, or busy with a run id; with a residency manager
also its memory and resident components). Rewritten every HEARTBEAT seconds and on every change; studio treats a
key older than 30 s as offline. Deleted on shutdown, so the host goes offline at once."""

import asyncio
import contextlib
import json
import logging

from config import ENV, HOST, PUBLISHED_BY
from messaging import schemas
from messaging.publisher import now

log = logging.getLogger(__name__)

HEARTBEAT = 10  # seconds
KEY = f"{HOST}.{ENV}"


class Host:
    """This runner's hosts key."""

    def __init__(self, kv, extra=None) -> None:
        self.kv, self.extra = (
            kv,
            extra,
        )  # extra() → more fields (the residency manager's memory, resident)
        self.run_id: str | None = None

    def value(self) -> dict:
        """The key's value now. ValueError when off-contract."""
        value = {
            "schema_version": 1,
            "host": HOST,
            "env": ENV,
            "state": "busy" if self.run_id else "idle",
            "run_id": self.run_id,
            "updated_at": now(),
            "published_by": PUBLISHED_BY,
        }
        if self.extra:
            value |= self.extra()
        if errs := schemas.registry_errors("host", value):
            raise ValueError(f"hosts value is off-contract: {'; '.join(errs[:3])}")
        return value

    async def put(self) -> None:
        """Write the key; a failure is logged (the next beat tries again)."""
        try:
            await self.kv.put(KEY, json.dumps(self.value()).encode())
        except Exception:
            log.warning("could not write hosts key %s", KEY, exc_info=True)

    async def beat(self, stop: asyncio.Event) -> None:
        """Rewrite the key every HEARTBEAT seconds until `stop` is set."""
        while not stop.is_set():
            await self.put()
            with contextlib.suppress(TimeoutError):
                await asyncio.wait_for(stop.wait(), HEARTBEAT)

    async def offline(self) -> None:
        """Delete the key: the host is offline."""
        try:
            await self.kv.delete(KEY)
        except Exception:
            log.warning("could not delete hosts key %s", KEY, exc_info=True)
