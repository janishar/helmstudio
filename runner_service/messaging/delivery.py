"""The steps every CMD delivery shares (runs, model jobs, media jobs): the pull consumer, the pull loop, decoding a
command, dead-lettering an invalid one and keeping a long one alive. Each handler keeps its own ack logic."""

import asyncio
import contextlib
import json
import logging

import nats.errors
from nats.js.api import AckPolicy, ConsumerConfig, DeliverPolicy

from messaging import schemas

log = logging.getLogger("delivery")


async def durable(js, name: str, subject: str, ack_wait: float, max_deliver: int):
    """Create or update (a changed config replaces the old one on the server) the CMD durable `name` on `subject`,
    one delivery at a time, and bind a pull subscription to it."""
    await js.add_consumer(
        "CMD",
        ConsumerConfig(
            name=name,
            durable_name=name,
            filter_subject=subject,
            ack_policy=AckPolicy.EXPLICIT,
            ack_wait=ack_wait,
            max_deliver=max_deliver,
            max_ack_pending=1,
            deliver_policy=DeliverPolicy.ALL,
        ),
    )
    return await js.pull_subscribe_bind(durable=name, stream="CMD")


async def pull(sub, handle, stop: asyncio.Event, what: str) -> None:
    """Fetch and `await handle(msg)` one delivery at a time until `stop`; a failed fetch waits 2 s (or `stop`)."""
    while not stop.is_set():
        try:
            msgs = await sub.fetch(1, timeout=2)
        except nats.errors.TimeoutError:
            continue
        except Exception:
            log.exception("%s fetch failed; retrying", what)
            with contextlib.suppress(TimeoutError):
                await asyncio.wait_for(stop.wait(), 2)
            continue
        for m in msgs:
            await handle(m)


def decode(msg, type_: str, what: str) -> tuple[dict | None, list[str]]:
    """(the command, its problems): not JSON, off-contract, or not a `type_` (`not a <what>: <type>`)."""
    try:
        cmd = json.loads(msg.data)
    except ValueError:
        cmd = None
    errs = schemas.message_errors(cmd) if isinstance(cmd, dict) else ["not a JSON object"]
    if not errs and cmd["type"] != type_:
        errs = [f"not a {what}: {cmd['type']}"]
    return cmd, errs


async def dead_letter(js, msg, errs: list[str], report=None) -> None:
    """Copy an invalid delivery to dlq.<subject>, `await report()` when given, then `term` it (each step logged when
    it fails, never raised)."""
    log.error("rejecting %s: %s", msg.subject, "; ".join(errs[:3]))
    try:
        await js.publish(f"dlq.{msg.subject}", msg.data)
    except Exception:
        log.exception("could not dead-letter a %s message", msg.subject)
    if report is not None:
        await report()
    try:
        await msg.term()
    except Exception:  # NATS away: logged (a redelivery is rejected again)
        log.warning("could not term a %s message", msg.subject, exc_info=True)


async def keep_alive(msg, every: float) -> None:
    """`in_progress` every `every` seconds until cancelled (a failure is ignored: the next one tries again)."""
    while True:
        await asyncio.sleep(every)
        with contextlib.suppress(Exception):
            await msg.in_progress()
