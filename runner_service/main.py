"""runner_service: `python main.py`. RUNNER_HOST / RUNNER_ENV say which runner: fake/fake (the compose fake
runner, the echo pack) or mac/torch (native, `make runners`, the torch packs).

Loads the packs of its env, indexes the host's shared LoRAs, stores the starter looks' thumbnails, checks and publishes its registry values, keeps its hosts key fresh (with memory and
resident components when it has a residency manager), then pulls cmd.run.<host>.<env> from CMD (durable
runner-<host>-<env>) and runs one command at a time (consumer.Consumer). SIGINT/SIGTERM stop pulling, stop the
current run at its next cancel point (reported failed), unload every component, delete the hosts key and exit."""

import asyncio
import contextlib
import importlib
import logging
import signal
import time

import nats
import nats.errors
from nats.js.api import AckPolicy, ConsumerConfig, DeliverPolicy

import config
import health
from config import ENV, HOST, INBOX_PREFIX, NATS_URL
from consumer import ACK_WAIT, Consumer
from core import looks, lora, registry
from core.graph import GPU
from core.residency import POLICIES, Residency, ram_gb
from messaging.hosts import Host
from storage import Store

log = logging.getLogger("runner")

DURABLE = f"runner-{HOST}-{ENV}"
SUBJECT = f"cmd.run.{HOST}.{ENV}"
IDLE_CHECK = 30  # seconds between keep_warm idle checks


def residency_for(packs) -> tuple[Residency | None, str]:
    """(the residency manager, the device) when a loaded pack has weight components, else (None, "cpu")."""
    runtimes = {p.runtime for p in packs if p.spec.get("components")}
    if not runtimes:
        return None, "cpu"
    if len(runtimes) > 1:
        raise ValueError(f"packs of one env use several runtimes: {sorted(runtimes)}")
    loader = importlib.import_module(f"core.loaders_{runtimes.pop()}")
    if config.AFTER_RUN not in POLICIES:
        raise ValueError(f"RUNNER_AFTER_RUN must be one of {POLICIES}")
    budget = ram_gb() - config.RESERVE_GB
    res = Residency(budget, free=loader.free, measure=loader.measure_gb, pins=config.PINS)
    return res, loader.device()


async def idle_unload(consumer: Consumer, host: Host, stop: asyncio.Event) -> None:
    """keep_warm: unload all but pinned components once the runner has been idle RUNNER_IDLE_UNLOAD_S."""
    res, loop = consumer.residency, asyncio.get_running_loop()
    while not stop.is_set():
        with contextlib.suppress(TimeoutError):
            await asyncio.wait_for(stop.wait(), IDLE_CHECK)
        idle = time.monotonic() - consumer.idle_since
        if consumer.ctx is None and res.entries and idle > config.IDLE_UNLOAD_S:
            log.info("idle %.0f s: unloading", idle)
            await loop.run_in_executor(GPU, res.unload, True)
            await host.put()


async def main() -> None:
    logging.basicConfig(
        level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s: %(message)s"
    )
    values, impls, packs = (
        registry.build()
    )  # an off-contract pack stops the runner before it connects
    residency, device = residency_for(packs)
    store = Store()
    library = lora.Library(
        config.MODELS, config.CACHE, store=store, host=HOST, cache_gb=config.LORA_CACHE_GB
    )
    blocks = registry.lora_blocks(packs)
    values["loras"] = registry.scanned_value("loras", await asyncio.to_thread(library.scan, blocks))
    values["looks"] = registry.scanned_value("looks", looks.entries(packs, library, config.MODELS))
    await looks.put_thumbs(packs, store)
    nc = await nats.connect(
        NATS_URL, name=DURABLE, max_reconnect_attempts=-1, inbox_prefix=INBOX_PREFIX
    )
    js = nc.jetstream()
    await registry.publish(js, values)
    host = Host(await js.key_value("hosts"), extra=residency.state if residency else None)
    consumer = Consumer(
        js=js,
        store=store,
        cancels_kv=await js.key_value("cancels"),
        host=host,
        impls=impls,
        node_schemas=registry.node_schemas(values),
        residency=residency,
        device=device,
        after_run=config.AFTER_RUN,
        loras=library,
    )
    # create or update: a changed config here replaces the old one on the server
    await js.add_consumer(
        "CMD",
        ConsumerConfig(
            name=DURABLE,
            durable_name=DURABLE,
            filter_subject=SUBJECT,
            ack_policy=AckPolicy.EXPLICIT,
            ack_wait=ACK_WAIT,
            max_deliver=1,  # a run is retried by studio's dispatcher, never by redelivery
            max_ack_pending=1,
            deliver_policy=DeliverPolicy.ALL,
        ),
    )
    sub = await js.pull_subscribe_bind(durable=DURABLE, stream="CMD")

    stop = asyncio.Event()

    def on_signal() -> None:
        stop.set()
        consumer.stopping = True

    loop = asyncio.get_running_loop()
    for sig in (signal.SIGINT, signal.SIGTERM):
        loop.add_signal_handler(sig, on_signal)
    beat = asyncio.create_task(host.beat(stop))
    idle = asyncio.create_task(idle_unload(consumer, host, stop)) if residency else None
    server = await health.serve(nc)
    log.info("runner %s.%s ready on %s (device %s)", HOST, ENV, SUBJECT, device)
    try:
        while not stop.is_set():
            try:
                msgs = await sub.fetch(1, timeout=2)
            except nats.errors.TimeoutError:
                continue
            except Exception:
                log.exception("fetch failed; retrying")
                with contextlib.suppress(TimeoutError):
                    await asyncio.wait_for(stop.wait(), 2)
                continue
            for m in msgs:
                await consumer.handle(m)
    finally:
        log.info("shutting down")
        server.close()
        stop.set()
        await beat
        if idle:
            await idle
        if residency:
            await loop.run_in_executor(GPU, residency.unload)
        await host.offline()
        # close, not drain: every publish was awaited (JetStream acks); draining the pull subscription took 30 s
        await nc.close()


if __name__ == "__main__":
    asyncio.run(main())
