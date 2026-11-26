"""KV `host_settings`, key <host> (contracts/README.md "Hosts admin"): the admin's after-run policy, idle unload,
memory reserve and pins for every env of this host, written only by studio. This runner reads its key every POLL
seconds and applies a new version at once when idle, else right after its run (never mid-run, never a restart); a deleted key brings
back its env (RUNNER_AFTER_RUN, RUNNER_IDLE_UNLOAD_S, RUNNER_RESERVE_GB, RUNNER_PIN). What is in effect is the hosts
key's `policy`."""

import asyncio
import contextlib
import json
import logging

from nats.js.errors import NotFoundError

import config
from config import ENV, HOST
from core.graph import GPU
from core.residency import gpu_limit_gb, ident, ram_gb
from messaging import schemas

log = logging.getLogger("settings")

BUCKET = "host_settings"
RETRY = 60  # seconds between tries to open the bucket
POLL = 5  # seconds between reads of the key
MAC_FLOOR_GB = 4  # auto: a GPU limit leaves macOS at least this much RAM (owner, plan 35)


class Settings:
    def __init__(self, consumer, host) -> None:
        self.consumer, self.host = consumer, host
        self.pending: tuple | None = None  # (value or None,) waiting for the run to end
        self.rescan = None  # main's: rebuild the models and loras keys
        self.gpu_limit_gb = (
            gpu_limit_gb()
        )  # the hosts key's machine.gpu_limit_gb; read again on each apply
        self.defaults()

    def defaults(self) -> None:
        self.version = None
        self.after_run = config.AFTER_RUN
        self.idle_unload_min = round(config.IDLE_UNLOAD_S / 60)
        self.reserve_gb = config.RESERVE_GB
        self.reserve_mode = "custom"
        self.pins: set[tuple] | None = None  # None: RUNNER_PIN
        config.ROOTS, config.DOWNLOADS = [], 0  # API 1.78: HELMSTUDIO_MODELS alone

    def policy(self) -> dict:
        """The hosts key's `policy`."""
        return {"after_run": self.after_run, "idle_unload_min": self.idle_unload_min,
                "reserve_gb": self.reserve_gb, "reserve_mode": self.reserve_mode,
                "settings_version": self.version}  # fmt: skip

    def budget(self, total: float) -> float:
        """The budget cut from `total` (the device's memory; CUDA: VRAM) under the policy (contracts/README.md,
        Automatic budget): auto = the macOS GPU limit when set, at most RAM - MAC_FLOOR_GB, else total -
        RUNNER_RESERVE_GB; custom = total - reserve_gb. Never negative."""
        if self.reserve_mode == "auto" and self.gpu_limit_gb:
            return max(0.0, min(self.gpu_limit_gb, ram_gb() - MAC_FLOOR_GB))
        reserve = config.RESERVE_GB if self.reserve_mode == "auto" else self.reserve_gb
        return max(0.0, total - reserve)

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
        before = (list(config.ROOTS), config.DOWNLOADS)
        if value is None:
            self.defaults()
        else:
            self.version, self.after_run = value["version"], value["after_run"]
            self.idle_unload_min, self.reserve_gb = value["idle_unload_min"], value["reserve_gb"]
            self.reserve_mode = value.get("reserve_mode", "custom")
            self.pins = {ident(p) for p in value["pins"] if p["env"] == ENV}
            config.ROOTS = list(
                value.get("folders", [])
            )  # API 1.78: the model folders in priority order
            config.DOWNLOADS = value.get("downloads", 0)
        moved = before != (config.ROOTS, config.DOWNLOADS)
        c, res = self.consumer, self.consumer.residency
        c.after_run = self.after_run
        if self.after_run == "unload":  # cached node results would let the next run load nothing
            c.cache.clear()
        c.idle_unload_s = self.idle_unload_min * 60 if value is not None else config.IDLE_UNLOAD_S
        self.gpu_limit_gb = await asyncio.to_thread(
            gpu_limit_gb
        )  # the owner may have changed it since
        if res is not None:  # on CUDA the reserve is cut from VRAM
            res.budget_gb = self.budget(res.total_gb or ram_gb())
            res.pin_refs = self.pins
            if self.after_run != "keep_warm":  # unload: everything; keep_pinned: what is not pinned
                await asyncio.get_running_loop().run_in_executor(
                    GPU, res.unload, self.after_run == "keep_pinned"
                )
        log.info("host settings applied: %s", self.policy())
        if moved and self.rescan:  # the models and loras keys as the new folders hold them
            await self.rescan()
        else:
            await self.host.put()

    async def watch(self, js, stop: asyncio.Event) -> None:
        """Read the key every POLL seconds until `stop` and offer each new revision (a direct get of its own key: a
        remote host's NATS user may create no consumer on the bucket). A missing bucket is tried again every RETRY
        seconds."""
        kv, seen = None, 0  # the revision offered last; 0 = none or deleted
        while not stop.is_set():
            try:
                kv = kv or await js.key_value(BUCKET)
                try:
                    entry = await kv.get(HOST)
                    rev, value = entry.revision, entry.value
                except NotFoundError:  # absent or deleted
                    rev, value = 0, None
            except Exception:  # noqa: BLE001 (no bucket yet: deploy creates it; NATS away)
                log.warning("bucket %s not available; retrying in %d s", BUCKET, RETRY)
                kv, wait = None, RETRY
            else:
                wait = POLL
                if rev != seen:
                    seen = rev
                    try:
                        await self.offer(json.loads(value) if value else None)
                    except Exception:
                        log.exception("host_settings %s could not be applied", HOST)
            with contextlib.suppress(TimeoutError):
                await asyncio.wait_for(stop.wait(), wait)
