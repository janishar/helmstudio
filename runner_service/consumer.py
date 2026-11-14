"""cmd.run.execute deliveries from the durable consumer runner-<host>-<env>, one at a time.

- invalid (contract, owner prefix, or a graph that does not fit this runner's node schemas) → copied to
  dlq.<subject>, reported failed when it still names its run and session, `term`;
- cancel key already written → cancelled;
- else running → the graph (KV cancels watched; `in_progress` every HEARTBEAT s) → succeeded, failed with a
  plain error, or cancelled;
- the command is always acked after the terminal status (a run is delivered once: studio, not redelivery,
  retries it). A shutdown stops the run at its next cancel point and reports it failed."""

import asyncio
import contextlib
import json
import logging
import mimetypes

from core import graph
from core.graph import Cancelled, Ctx, RunError
from messaging import cancels, schemas
from messaging.hosts import Host
from messaging.publisher import RunEvents

log = logging.getLogger(__name__)

ACK_WAIT = 30  # seconds
HEARTBEAT = 10  # seconds between in_progress() while a run executes
SHUTDOWN = "the runner shut down during the run"


class Consumer:
    """Runs this runner's commands and knows the one in progress."""

    def __init__(
        self, *, js, store, cancels_kv, host: Host, impls: dict, node_schemas: dict
    ) -> None:
        self.js, self.store, self.cancels_kv, self.host = js, store, cancels_kv, host
        self.impls, self.node_schemas = impls, node_schemas
        self.stopping = False  # set on SIGTERM: the run stops at its next cancel point
        self.cancel_requested = False  # the current run's cancel key was written

    def check(self) -> None:
        """A cancel point of the current run."""
        if self.stopping:
            raise RunError(SHUTDOWN)
        if self.cancel_requested:
            raise Cancelled

    def _on_cancel(self) -> None:
        self.cancel_requested = True

    async def handle(self, msg) -> None:
        """Validate one delivery, run it, ack it; or dead-letter, report and term it."""
        try:
            cmd = json.loads(msg.data)
        except ValueError:
            cmd = None
        errs = schemas.message_errors(cmd) if isinstance(cmd, dict) else ["not a JSON object"]
        if not errs and cmd["type"] != "cmd.run.execute":
            errs = [f"not a run command: {cmd['type']}"]
        if not errs:
            errs = graph.validate(cmd["data"]["workflow"]["nodes"], self.node_schemas)
        if errs:
            await self.reject(msg, cmd, errs)
            return
        try:
            await self.run(msg, cmd)
        except Exception:
            log.exception("run %s: could not report its end", cmd["job_id"])
        try:
            await msg.ack_sync()
        except Exception:
            log.exception("run %s: ack failed", cmd["job_id"])

    async def reject(self, msg, cmd, errs: list[str]) -> None:
        """Dead-letter an invalid command, report its run failed when it still names one, term it."""
        log.error("rejecting %s: %s", msg.subject, "; ".join(errs[:3]))
        try:
            await self.js.publish(f"dlq.{msg.subject}", msg.data)
        except Exception:
            log.exception("could not dead-letter a %s message", msg.subject)
        if isinstance(cmd, dict) and cmd.get("type") == "cmd.run.execute":
            try:
                await RunEvents(self.js, cmd).status("failed", "rejected: " + "; ".join(errs[:3]))
            except Exception:
                log.warning(
                    "could not report rejected run %s failed", cmd.get("job_id"), exc_info=True
                )
        try:
            await msg.term()
        except Exception:  # NATS away: the command is not redelivered anyway (max_deliver 1)
            log.warning("could not term a %s message", msg.subject, exc_info=True)

    async def run(self, msg, cmd: dict) -> None:
        """Execute one valid command and publish its events, terminal status included."""
        run_id, data = cmd["job_id"], cmd["data"]
        events = RunEvents(self.js, cmd)
        self.cancel_requested = False
        self.host.run_id = run_id
        await self.host.put()
        beat = asyncio.create_task(self._in_progress(msg))
        stop_watch = None
        try:
            if await cancels.requested(self.cancels_kv, run_id):
                await events.status("cancelled")
                return
            stop_watch = await cancels.watch(self.cancels_kv, run_id, self._on_cancel)
            await events.status("running")

            async def output(index: int, body: bytes, *, ext: str, **meta) -> None:
                key = f"{data['output_prefix']}{index}.{ext}"
                await self.store.put(key, body, mimetypes.types_map[f".{ext}"])
                await events.output(index=index, key=key, bytes=len(body), **meta)

            ctx = Ctx(events.progress, output, self.check)
            try:
                await graph.run(data["workflow"]["nodes"], self.impls, ctx)
            except Cancelled:
                await events.status("cancelled")
                return
            except RunError as e:
                await events.status("failed", str(e))
                return
            except Exception as e:
                log.exception("run %s: node %s failed", run_id, ctx.node)
                await events.status("failed", f"{ctx.node}: internal error ({type(e).__name__})")
                return
            await events.status("succeeded")
        except Exception as e:  # the cancels KV or a publish broke: still try to end the run
            log.exception("run %s: runner error", run_id)
            with contextlib.suppress(Exception):
                await events.status("failed", f"runner error ({type(e).__name__})")
        finally:
            beat.cancel()
            if stop_watch:
                with contextlib.suppress(Exception):
                    await stop_watch()
            self.host.run_id = None
            await self.host.put()

    async def _in_progress(self, msg) -> None:
        """Tell NATS the run is alive every HEARTBEAT seconds, within ACK_WAIT."""
        while True:
            await asyncio.sleep(HEARTBEAT)
            try:
                await msg.in_progress()
            except Exception:
                log.warning("in_progress failed", exc_info=True)
