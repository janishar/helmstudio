"""cmd.model.job deliveries from the durable models-<host>-<env> (contracts/README.md "Models and model jobs"), one
at a time, in a worker beside the runs: disk and network on a worker thread, never the GPU thread, never a weight
holder, so a download does not block generation.

- invalid → copied to dlq.<subject> and `term`;
- cancel key `<job_id>` already written → cancelled;
- else `running` → the job (`+WIP` every WIP seconds; a download watches its cancel key and sends
  evt.model.progress at each file's start and end, at most every PROGRESS seconds between and at least every HEARD
  seconds) → a rescan of the
  models and loras keys → one terminal status → ack (after the status is acked). A redelivery (the runner
  restarted: delivery + 1, seq from 1) resumes a download from the files already on disk."""

import asyncio
import contextlib
import hashlib
import json
import logging
import shutil
import time

from config import ENV, HOST
from core import download, models
from core import pack as packs
from core.download import Cancelled, JobError
from messaging import cancels, schemas
from messaging.publisher import envelope, send

log = logging.getLogger("models")

DURABLE = f"models-{HOST}-{ENV}"
SUBJECT = f"cmd.model.{HOST}.{ENV}"
ACK_WAIT = 60  # seconds
WIP = 10  # seconds between +WIP
PROGRESS = 2.0  # seconds between progress events within a file
HEARD = 30.0  # seconds: a progress event at least this often, so studio knows the job is alive
POLL = 0.25  # seconds between looks at a running download


class ModelEvents:
    """evt.model.status / evt.model.progress of one delivery of a job: seq from 1 per delivery (KIND media: the
    evt.media ones)."""

    KIND = "model"

    def __init__(self, js, cmd: dict, delivery: int) -> None:
        self.js, self.cmd, self.delivery, self.seq = js, cmd, delivery, 0

    async def publish(self, kind: str, data: dict) -> dict:
        cmd, seq = self.cmd, self.seq + 1
        msg = envelope(cmd, f"evt.{self.KIND}.{kind}", {"seq": seq, "delivery": self.delivery} | data)
        self.seq = seq
        await send(self.js, f"evt.{self.KIND}.{HOST}.{kind}", msg, f"{cmd['job_id']}:{self.delivery}:{seq}")
        return msg

    async def status(self, status: str, failure: JobError | None = None, result: dict | None = None):
        data: dict = {"status": status}
        if failure is not None:
            data["failure"] = {"class": failure.cls, "detail": str(failure) or failure.cls}
        if result is not None:
            data["result"] = result
        return await self.publish("status", data)


class ModelJobs:
    """Runs this runner's model jobs. `rescan()` republishes the models and loras keys and the hosts key."""

    def __init__(self, *, js, cancels_kv, packs_, library, consumer, rescan, cache, endpoint, token_file):
        self.js, self.cancels_kv, self.packs, self.library = js, cancels_kv, packs_, library
        self.consumer, self.rescan, self.cache = consumer, rescan, cache
        self.endpoint, self.token_file = endpoint, token_file
        self.job: str | None = None  # the job in progress

    async def handle(self, msg) -> None:
        try:
            cmd = json.loads(msg.data)
        except ValueError:
            cmd = None
        errs = schemas.message_errors(cmd) if isinstance(cmd, dict) else ["not a JSON object"]
        if not errs and cmd["type"] != "cmd.model.job":
            errs = [f"not a model job: {cmd['type']}"]
        if errs:
            log.error("rejecting %s: %s", msg.subject, "; ".join(errs[:3]))
            with contextlib.suppress(Exception):
                await self.js.publish(f"dlq.{msg.subject}", msg.data)
            with contextlib.suppress(Exception):
                await msg.term()
            return
        meta = getattr(msg, "metadata", None)
        events = ModelEvents(self.js, cmd, getattr(meta, "num_delivered", None) or 1)
        job_id, data = cmd["job_id"], cmd["data"]
        self.job = job_id
        beat = asyncio.create_task(self._wip(msg))
        failure = result = None
        try:
            if await cancels.requested(self.cancels_kv, job_id):
                status = "cancelled"
            else:
                await events.status("running")
                log.info(
                    "model job %s: %s %s",
                    job_id,
                    data["kind"],
                    data.get("model") or data.get("lora_id"),
                )
                result = await getattr(self, data["kind"])(job_id, data, events)
                status = "succeeded"
        except Cancelled:
            status = "cancelled"
        except JobError as e:
            status, failure = "failed", e
        except Exception as e:
            log.exception("model job %s failed", job_id)
            status, failure = (
                "failed",
                JobError("internal", f"the runner failed ({type(e).__name__})"),
            )
        finally:
            beat.cancel()
            self.job = None
        try:
            await self.rescan()
        except Exception:
            log.exception("rescan after model job %s failed", job_id)
        log.info("model job %s: %s%s", job_id, status, f" ({failure.cls}: {failure})" if failure else "")
        try:
            await events.status(status, failure, result if status == "succeeded" else None)
            await msg.ack_sync()
        except Exception:  # not acked: redelivered after ACK_WAIT, and the job runs again (idempotent)
            log.exception("model job %s: could not report %s", job_id, status)

    async def _wip(self, msg) -> None:
        while True:
            await asyncio.sleep(WIP)
            with contextlib.suppress(Exception):
                await msg.in_progress()

    def find(self, model: str):
        p = next((p for p in self.packs if p.spec["id"] == model), None)
        if p is None:
            raise JobError("not_found", f"{model} is not published by {HOST}.{ENV}")
        return p

    @staticmethod
    def root(fid: str):
        """The connected folder of the list with id `fid` (API 1.78)."""
        r = next((r for t, r in packs.roots() if packs.folder_id(t) == fid), None)
        if r is None:
            raise JobError("not_found", f"folder {fid} is not a folder of {HOST}")
        if not r.is_dir():
            raise JobError("not_found", f"{models.folder_name(r)} is not connected on {HOST}")
        return r

    @staticmethod
    def within(root, fn, *a, **kw):
        """fn(*a, **kw) with this thread's model folders in `root` only (None: as the resolver reads them)."""
        if root is None:
            return fn(*a, **kw)
        with packs.rooted(root):
            return fn(*a, **kw)

    def addon(self, p, data: dict) -> str | None:
        """The job's add-on (API 1.36), checked: one of the model's."""
        if (c := data.get("addon")) is not None and c not in models.listed_addons(p):
            raise JobError("not_found", f"{p.spec['title']} has no add-on {c}")
        return c

    async def download(self, job_id: str, data: dict, events: ModelEvents) -> dict:
        p = self.find(data["model"])
        if addon := self.addon(p, data):
            vid, spec = None, p.component(addon)["addon"]
            src, what, more = spec.get("source"), f"{p.spec['title']} {spec['title']}", []
        else:
            vid = data.get("variant") or next(iter(p.spec.get("variants", {})), None)
            if vid not in p.spec.get("variants", {}):
                raise JobError("not_found", f"{p.spec['title']} has no variant {vid}")
            src, what = p.spec["variants"][vid].get("source"), f"{p.spec['title']} {vid}"
            more = p.spec["variants"][vid].get("sources", [])
        if src is None:
            raise JobError("not_downloadable", f"{what} has no download source")
        if src["must_accept"]:
            want = hashlib.sha256(models.licence_text(p, src["licence"]).encode()).hexdigest()
            if data.get("licence") != {"id": src["licence"], "sha256": want}:
                raise JobError("licence", f"licence {src['licence']} changed since it was accepted")
        gated = src["gated"] or any(s["gated"] for s in more)
        token = download.read_token(self.token_file) if gated else None
        log.info("model job %s: %s %s from %s, token: %s", job_id, p.spec["id"], addon or vid, src["repo"],
                 "present" if token else "absent")  # fmt: skip
        progress = download.Progress()
        # API 1.78: into the job's folder, else the downloads folder (else the next connected one)
        root = self.root(data["folder"]) if "folder" in data else packs.models_root()
        stop = await cancels.watch(self.cancels_kv, job_id, progress.cancel.set)
        task = asyncio.create_task(asyncio.to_thread(
            self.within, root, download.download, p, vid, endpoint=self.endpoint, token=token, cache=self.cache,
            host=HOST, progress=progress, addon=addon))  # fmt: skip
        try:
            sent, at, last = None, 0.0, None
            while True:
                await asyncio.wait({task}, timeout=POLL)
                if progress.total_bytes is not None:
                    d = progress.data()
                    edge = (d["files_done"], d.get("file"))
                    since = time.monotonic() - at
                    if edge != sent or (since >= PROGRESS and d["done_bytes"] != last) or since >= HEARD:
                        await events.publish("progress", d)
                        sent, at, last = edge, time.monotonic(), d["done_bytes"]
                if task.done():
                    return task.result()
        finally:
            progress.cancel.set()  # the thread stops at its next chunk when this coroutine is cancelled
            with contextlib.suppress(Exception):
                await stop()
            shutil.rmtree(self.cache / "hf-xet", ignore_errors=True)

    def _loaded(self, test) -> list[tuple]:
        res = self.consumer.residency
        return [k for k in (res.entries if res else ()) if test(k)]

    def targets(self, data: dict, d: str) -> tuple[list, bool]:
        """(the roots a delete works in: [None] = the copy a run loads, whether that copy is among them: the
        loaded and in-use checks apply only then). API 1.78 `folders`."""
        if not (ids := data.get("folders")):
            return [None], True
        roots = [self.root(i) for i in ids]
        used = packs.root_of(packs.place(d))
        return roots, used is not None and used[1] in roots

    async def delete(self, job_id: str, data: dict, events: ModelEvents) -> dict:
        p = self.find(data["model"])
        if addon := self.addon(p, data):
            return await self.delete_addon(job_id, p, addon, data)
        variants = p.spec.get("variants", {})
        if (v := data.get("variant")) is not None and v not in variants:
            raise JobError("not_found", f"{p.spec['title']} has no variant {v}")
        deleted = {v} if v else set(variants)
        roots, used = self.targets(data, variants[v or next(iter(variants))]["dir"])
        out = {"files": 0, "freed_bytes": 0}
        for r in roots:
            one = await self._delete(job_id, p, variants, deleted, r, used)
            out["files"] += one["files"]
            out["freed_bytes"] += one["freed_bytes"]
            if one.get("kept"):
                out["kept"] = out.get("kept", []) + [k for k in one["kept"] if k not in out.get("kept", [])]
        return out

    async def _delete(self, job_id, p, variants, deleted, root, checks) -> dict:
        """The variants `deleted` in `root` (None: where a run loads them); `checks`: refuse while loaded or used."""
        kept, only = self.within(root, models.kept_components, p, deleted)
        # residency names a shared component by its owner variant: one kept on disk stays loadable (studio's rule;
        # kept is not empty only when the one deleted variant is a lender)
        if checks and (loaded := self._loaded(lambda k: k[0] == p.spec["id"] and k[1] in deleted
                                              and k[2] not in kept and k[2] not in packs.addons(p))):  # fmt: skip
            raise JobError("in_use", f"{p.spec['title']} {loaded[0][1]} {loaded[0][2]} is loaded on "
                                     f"{HOST}.{ENV}: unload it first")  # fmt: skip
        if checks:
            self._unused(p)

        def work() -> tuple[int, int]:
            files = freed = 0
            for vid in variants:
                if vid in deleted:
                    # a variant in the first's folder deletes only its own files (models.own_globs); a lender
                    # (a folder of its own) keeps what others still load (`only`)
                    own = models.own_globs(p, vid)
                    f, b = models.delete_variant(p, vid, only if own is None else own)
                    files, freed = files + f, freed + b
            return files, freed

        try:
            files, freed = await asyncio.to_thread(self.within, root, work)
        except models.InUse as e:
            raise JobError("in_use", str(e)) from None
        log.info("model job %s: deleted %d files (%.1f GB), kept %s", job_id, files, freed / 1e9, kept)
        return {"files": files, "freed_bytes": freed} | ({"kept": kept} if kept else {})

    def _unused(self, p) -> None:
        res = self.consumer.residency
        if self.consumer.ctx is not None and res and any(k[0] == p.spec["id"] for k in res.used):
            raise JobError("in_use", f"a run on {HOST}.{ENV} uses {p.spec['title']}")

    async def delete_addon(self, job_id: str, p, c: str, data: dict) -> dict:
        """An add-on's files (API 1.36), refused while it is loaded or a run uses the model here (API 1.78: only
        when the copy a run loads is among the job's `folders`)."""
        roots, checks = self.targets(data, models.addon_dir(p, c))
        if checks and (loaded := self._loaded(lambda k: k[0] == p.spec["id"] and k[2] == c)):
            raise JobError(
                "in_use",
                f"{p.spec['title']} {loaded[0][1]} {c} is loaded on {HOST}.{ENV}: unload it first",
            )
        if checks:
            self._unused(p)
        files = freed = 0
        try:
            for r in roots:
                f, b = await asyncio.to_thread(self.within, r, models.delete_addon, p, c)
                files, freed = files + f, freed + b
        except models.InUse as e:
            raise JobError("in_use", str(e)) from None
        log.info("model job %s: deleted add-on %s (%d files, %.1f GB)", job_id, c, files, freed / 1e9)
        return {"files": files, "freed_bytes": freed}

    async def lora_delete(self, job_id: str, data: dict, events: ModelEvents) -> dict:
        sha, lib = data["lora_id"], self.library
        paths = [f for f, s in lib.files.items() if s == sha]
        if not paths:
            raise JobError("not_found", f"LoRA {sha[:12]} is not on host {HOST}")
        name = lib.shared[sha].stem if sha in lib.shared else paths[0].stem
        for p in self.packs:
            for x in (p.spec.get("lora") or {}).get("prefused", []):
                f = packs.place(p.spec["variants"][x["variant"]]["dir"]) / x["file"]
                if f in paths:
                    raise JobError("in_use", f"LoRA {name} is pre-fused into {p.spec['title']}")
        if self._loaded(lambda k: len(k) > 4 and any(s == sha for s, _ in k[4])):
            raise JobError("in_use", f"LoRA {name} is baked into a loaded component: unload it first")
        freed = 0
        for f in paths:
            if not any(models.inside(r, f) for r in packs.connected()):
                raise JobError("internal", f"LoRA {name} is outside the models root")
            freed += f.lstat().st_size
            await asyncio.to_thread(f.unlink, True)
        log.info("model job %s: deleted LoRA %s (%d files)", job_id, name, len(paths))
        return {"files": len(paths), "freed_bytes": freed}


async def pull(sub, jobs: ModelJobs, stop: asyncio.Event) -> None:
    """Fetch and handle model jobs one at a time until `stop`."""
    import nats.errors

    while not stop.is_set():
        try:
            msgs = await sub.fetch(1, timeout=2)
        except nats.errors.TimeoutError:
            continue
        except Exception:
            log.exception("model jobs fetch failed; retrying")
            with contextlib.suppress(TimeoutError):
                await asyncio.wait_for(stop.wait(), 2)
            continue
        for m in msgs:
            await jobs.handle(m)
