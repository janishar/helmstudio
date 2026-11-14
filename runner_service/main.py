"""runner_service (the fake runner, host `fake`, env `fake`): `python main.py`.

Checks and publishes its registry values, keeps its hosts key fresh, then pulls cmd.run.<host>.<env> from CMD
(durable runner-<host>-<env>) and runs one command at a time (consumer.Consumer). SIGINT/SIGTERM stop pulling,
stop the current run at its next cancel point (reported failed), delete the hosts key and exit."""

import asyncio
import contextlib
import logging
import signal

import nats
import nats.errors
from nats.js.api import AckPolicy, ConsumerConfig, DeliverPolicy

import health
from config import ENV, HOST, NATS_URL
from consumer import ACK_WAIT, Consumer
from core import registry
from messaging.hosts import Host
from storage import Store

log = logging.getLogger("runner")

DURABLE = f"runner-{HOST}-{ENV}"
SUBJECT = f"cmd.run.{HOST}.{ENV}"


async def main() -> None:
    logging.basicConfig(
        level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s: %(message)s"
    )
    values = registry.values()  # off-contract values stop the runner before it connects
    nc = await nats.connect(NATS_URL, name=DURABLE, max_reconnect_attempts=-1)
    js = nc.jetstream()
    await registry.publish(js, values)
    host = Host(await js.key_value("hosts"))
    consumer = Consumer(
        js=js,
        store=Store(),
        cancels_kv=await js.key_value("cancels"),
        host=host,
        impls=registry.IMPLS,
        node_schemas=registry.node_schemas(values),
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
    server = await health.serve(nc)
    log.info("runner %s.%s ready on %s", HOST, ENV, SUBJECT)
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
        await host.offline()
        await nc.drain()


if __name__ == "__main__":
    asyncio.run(main())
