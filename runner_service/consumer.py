"""cmd.run.execute deliveries from the durable consumer runner-<host>-<env>, one at a time.

- invalid (contract, owner prefix, or a graph that does not fit this runner's node schemas) → copied to
  dlq.<subject>, reported failed when it still names its run and session, `term`;
- cancel key already written → cancelled;
- else running → the graph (KV cancels watched; `in_progress` every HEARTBEAT s) → succeeded (with the
  resolved seed), failed with a plain error, or cancelled;
- a node that passes no cancel point for its stage's stall limit is reported failed, the command acked, and the
  process exits (a GPU thread cannot be stopped; its supervisor restarts it);
- the command is always acked after the terminal status (a run is delivered once: studio, not redelivery,
  retries it). A shutdown stops the run at its next cancel point and reports it failed."""

import asyncio
import contextlib
import json
import logging
import mimetypes
import os
import time

from core import graph
from core.graph import GPU, Cache, Cancelled, Ctx, RunError
from messaging import cancels, schemas
from messaging.hosts import Host
from messaging.publisher import RunEvents

log = logging.getLogger(__name__)

ACK_WAIT = 30  # seconds
HEARTBEAT = 10  # seconds between in_progress() while a run executes
SHUTDOWN = "the runner shut down during the run"
STALLED = 70  # exit status after a stall


class Consumer:
    """Runs this runner's commands and knows the one in progress."""

    def __init__(
        self,
        *,
        js,
        store,
        cancels_kv,
        host: Host,
        impls: dict,
        node_schemas: dict,
        residency=None,
        device: str = "cpu",
        after_run: str = "keep_warm",
    ) -> None:
        self.js, self.store, self.cancels_kv, self.host = js, store, cancels_kv, host
        self.impls, self.node_schemas = impls, node_schemas
        self.residency, self.device, self.after_run = residency, device, after_run
        self.cache = Cache()
        if residency is not None:
            residency.on_short.append(self.cache.clear)
        self.stopping = False  # set on SIGTERM: the run stops at its next cancel point
        self.cancel_requested = False  # the current run's cancel key was written
        self.ctx: Ctx | None = None  # the run in progress
        self.stalled = False
        self.idle_since = time.monotonic()
        self.exit = os._exit  # tests replace it

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
        if self.stalled:  # the watchdog acked it
            return
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
        stop_watch = None
        nodes, resolved = graph.resolve(data["workflow"]["nodes"], self.node_schemas)
        ctx = self.ctx = Ctx(
            progress=events.progress,
            output=None,
            check=self.check,
            owner=cmd["owner_id"],
            residency=self.residency,
            device=self.device,
        )
        ctx.seed = resolved.get("seed")
        beat = asyncio.create_task(self._in_progress(msg, events))
        try:
            if await cancels.requested(self.cancels_kv, run_id):
                await events.status("cancelled")
                return
            stop_watch = await cancels.watch(self.cancels_kv, run_id, self._on_cancel)
            await events.status("running")
            log.info("run %s: running (seed %s)", run_id, ctx.seed)

            async def output(index: int, body: bytes, *, ext: str, **meta) -> None:
                key = f"{data['output_prefix']}{index}.{ext}"
                await self.store.put(key, body, mimetypes.types_map[f".{ext}"])
                await events.output(index=index, key=key, bytes=len(body), seed=ctx.seed, **meta)

            ctx._output = output
            try:
                await graph.run(nodes, self.impls, self.node_schemas, ctx, self.cache)
            except Cancelled:
                await self._end(events, "cancelled")
                return
            except RunError as e:
                await self._end(events, "failed", str(e))
                return
            except Exception as e:
                log.exception("run %s: node %s failed", run_id, ctx.node)
                await self._end(
                    events, "failed", f"{ctx.node}: internal error ({type(e).__name__})"
                )
                return
            await self._end(events, "succeeded", resolved=resolved)
        except Exception as e:  # the cancels KV or a publish broke: still try to end the run
            log.exception("run %s: runner error", run_id)
            with contextlib.suppress(Exception):
                await events.status("failed", f"runner error ({type(e).__name__})")
        finally:
            beat.cancel()
            if stop_watch:
                with contextlib.suppress(Exception):
                    await stop_watch()
            if self.residency is not None and not self.stalled:
                try:
                    await asyncio.get_running_loop().run_in_executor(
                        GPU, self.residency.after_run, self.after_run
                    )
                except Exception:
                    log.exception("after-run unload failed")
            self.ctx, self.idle_since = None, time.monotonic()
            self.host.run_id = None
            if not self.stalled:  # a stalled runner stays offline until it restarts
                await self.host.put()

    async def _end(self, events, status: str, error=None, resolved=None) -> None:
        """The terminal status, unless the stall watchdog already sent one."""
        log.info("run %s: %s%s", events.cmd["job_id"], status, f" ({error})" if error else "")
        if not self.stalled:
            await events.status(status, error, resolved)

    def _stalled_for(self) -> float | None:
        """Seconds past the running stage's stall limit since the last cancel point, or None."""
        ctx = self.ctx
        if ctx is None or ctx.impl is None:
            return None
        quiet = time.monotonic() - ctx.alive
        return quiet if quiet > ctx.impl.stall_s else None

    async def _in_progress(self, msg, events) -> None:
        """Tell NATS the run is alive every HEARTBEAT seconds, within ACK_WAIT, while it passes cancel points;
        past the stall limit, fail the run, ack it, go offline and exit."""
        while True:
            await asyncio.sleep(HEARTBEAT)
            if (quiet := self._stalled_for()) is not None:
                self.stalled = True
                node = self.ctx.node
                log.error("run %s stalled in %s for %.0f s: exiting", msg.subject, node, quiet)
                with contextlib.suppress(Exception):
                    await events.status(
                        "failed",
                        f"{node}: stalled, no progress for {quiet:.0f} s; runner restarting",
                    )
                with contextlib.suppress(Exception):
                    await msg.ack_sync()
                await self.host.offline()
                self.exit(STALLED)
                return
            try:
                await msg.in_progress()
            except Exception:
                log.warning("in_progress failed", exc_info=True)
