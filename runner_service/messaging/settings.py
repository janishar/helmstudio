"""KV `host_settings`, key <host> (contracts/README.md "Hosts admin"): the admin's after-run policy, idle unload,
memory reserve and pins for every env of this host, written only by studio. This runner watches its key and applies
a new version at once when idle, else right after its run (never mid-run, never a restart); a deleted key brings
back its env (RUNNER_AFTER_RUN, RUNNER_IDLE_UNLOAD_S, RUNNER_RESERVE_GB, RUNNER_PIN). What is in effect is the hosts
key's `policy`."""

import asyncio
import contextlib
import json
import logging

import config
from config import ENV, HOST
from core.graph import GPU
from core.residency import ident, ram_gb
from messaging import schemas

log = logging.getLogger("settings")

BUCKET = "host_settings"
RETRY = 60  # seconds between tries to open the bucket


class Settings:
    def __init__(self, consumer, host) -> None:
        self.consumer, self.host = consumer, host
        self.pending: tuple | None = None  # (value or None,) waiting for the run to end
        self.defaults()

    def defaults(self) -> None:
        self.version = None
        self.after_run = config.AFTER_RUN
        self.idle_unload_min = round(config.IDLE_UNLOAD_S / 60)
        self.reserve_gb = config.RESERVE_GB
        self.pins: set[tuple] | None = None  # None: RUNNER_PIN

    def policy(self) -> dict:
        """The hosts key's `policy`."""
        return {"after_run": self.after_run, "idle_unload_min": self.idle_unload_min,
                "reserve_gb": self.reserve_gb, "settings_version": self.version}  # fmt: skip

    async def offer(self, value: dict | None) -> None:
        """A new key value (None: deleted): applied now when idle, else after the run."""
        if value is not None and (errs := schemas.registry_errors("settings", value)):
            log.error("host_settings %s is off-contract, ignored: %s", HOST, "; ".join(errs[:3]))
            return
        if value is not None:  # KV puts can land out of order: never go back to an older version
            newest = max([self.version or 0] + [x["version"] for x in self.pending or () if x])
            if value["version"] <= newest:
                log.info(
                    "host_settings %s v%d is older than v%d, ignored",
                    HOST,
                    value["version"],
                    newest,
                )
                return
        if self.consumer.ctx is not None:
            self.pending = (value,)
            return
        await self.apply(value)

    async def run_ended(self) -> None:
        """The run ended: apply what came meanwhile."""
        if self.pending is not None:
            (value,), self.pending = self.pending, None
            await self.apply(value)

    async def apply(self, value: dict | None) -> None:
        if value is None:
            self.defaults()
        else:
            self.version, self.after_run = value["version"], value["after_run"]
            self.idle_unload_min, self.reserve_gb = value["idle_unload_min"], value["reserve_gb"]
            self.pins = {ident(p) for p in value["pins"] if p["env"] == ENV}
        c, res = self.consumer, self.consumer.residency
        c.after_run = self.after_run
        if self.after_run == "unload":  # cached node results would let the next run load nothing
            c.cache.clear()
        c.idle_unload_s = self.idle_unload_min * 60 if value is not None else config.IDLE_UNLOAD_S
        if res is not None:
            res.budget_gb, res.pin_refs = ram_gb() - self.reserve_gb, self.pins
            if self.after_run != "keep_warm":  # unload: everything; keep_pinned: what is not pinned
                await asyncio.get_running_loop().run_in_executor(
                    GPU, res.unload, self.after_run == "keep_pinned"
                )
        log.info("host settings applied: %s", self.policy())
        await self.host.put()

    async def watch(self, js, stop: asyncio.Event) -> None:
        """Follow the key until `stop`; a missing bucket is tried again every RETRY seconds."""
        while not stop.is_set():
            try:
                kv = await js.key_value(BUCKET)
                watcher = await kv.watch(HOST)
            except Exception:  # noqa: BLE001 (no bucket yet: deploy creates it)
                log.warning("bucket %s not available; retrying in %d s", BUCKET, RETRY)
                with contextlib.suppress(TimeoutError):
                    await asyncio.wait_for(stop.wait(), RETRY)
                continue
            task = asyncio.create_task(self._follow(watcher))
            await stop.wait()
            await watcher.stop()
            task.cancel()
            return

    async def _follow(self, watcher) -> None:
        async for entry in watcher:
            if entry is None:  # the end of the initial values
                continue
            deleted = getattr(entry, "operation", None) in ("DEL", "PURGE") or not entry.value
            try:
                await self.offer(None if deleted else json.loads(entry.value))
            except Exception:
                log.exception("host_settings %s could not be applied", HOST)
