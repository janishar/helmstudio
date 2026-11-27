"""cmd.media.job deliveries from the durable media-<host>-<env> (contracts/README.md, Timeline: media jobs), one at a
time, in a worker beside the runs and model jobs: ffmpeg on the CPU or the media engine, no model, no GPU, never a
weight holder.

- invalid → copied to dlq.<subject> and `term`; cancel key `<job_id>` already written → cancelled;
- else `running` → each input downloaded once into a 0700 temp dir under a name the runner makes (in<n>.<ext>),
  probed and refused (input) when it is not the kind the command says → export: the plan's copy re-checked against
  `expect` (any fact differs: conform_args), the placeholders substituted and the argument list checked (invalid),
  ffmpeg in its own process group with progress from `-progress pipe:1`, the output verified, poster (a tenth in)
  and last frame written; frame: that exact frame as a PNG (each with its thumbnail, API 1.48); thumbs: each input's
  thumbnail (README, Thumbnails); mux (API 1.79, README Music and sound: Save with the video): the video's picture
  copied with the audio as its AAC stereo sound fitted to the picture's length, poster at frame 0 and the last frame;
  bake (API 1.82, README Magic: Send to Timeline): an export's conform with studio's own graph, bake.mp4 and its pictures
  → uploads under output_prefix → one terminal status →
  ack (after the status is acked). The temp dir is removed on every path. A redelivery starts over."""

import asyncio
import contextlib
import hashlib
import json
import logging
import os
import shutil
import signal
import tempfile
import time
from fractions import Fraction
from pathlib import Path

from PIL import Image

from config import ENV, HOST
from core import images, media
from core.download import Cancelled
from core.media import MediaError
from messaging import cancels, schemas
from messaging.models import ModelEvents

log = logging.getLogger("media")

DURABLE = f"media-{HOST}-{ENV}"
SUBJECT = f"cmd.media.{HOST}.{ENV}"
ACK_WAIT = 60  # seconds
WIP = 10  # seconds between +WIP
PROGRESS = 2.0  # seconds: progress at most this often, and only when the frame count moved (a stall goes silent)
POLL = 0.25  # seconds between looks at ffmpeg and the cancel flag
KILL_AFTER = 10.0  # seconds from SIGTERM to SIGKILL
LIMIT = 8 << 30  # bytes: the largest input
BUDGET = 32 << 30  # bytes: every input of one job together
RESERVE = 2 << 30  # bytes: the disk space a download never takes
DEADLINE_BASE, DEADLINE_PER_FRAME, DEADLINE_MAX = (
    600.0,
    1.0,
    6 * 3600.0,
)  # seconds: how long ffmpeg may run
IMAGE_EXT = {"png": "png", "mjpeg": "jpg", "webp": "webp", "bmp": "bmp", "tiff": "tif"}


class MediaEvents(ModelEvents):
    KIND = "media"


class MediaJobs:
    """Runs this runner's media jobs with `encoders` (the hosts key's media.encoders, preferred first)."""

    def __init__(self, *, js, cancels_kv, store, encoders: list[str]) -> None:
        self.js, self.cancels_kv, self.store, self.encoders = js, cancels_kv, store, encoders
        self.job: str | None = None

    async def handle(self, msg) -> None:
        try:
            cmd = json.loads(msg.data)
        except ValueError:
            cmd = None
        errs = schemas.message_errors(cmd) if isinstance(cmd, dict) else ["not a JSON object"]
        if not errs and cmd["type"] != "cmd.media.job":
            errs = [f"not a media job: {cmd['type']}"]
        if errs:
            log.error("rejecting %s: %s", msg.subject, "; ".join(errs[:3]))
            with contextlib.suppress(Exception):
                await self.js.publish(f"dlq.{msg.subject}", msg.data)
            with contextlib.suppress(Exception):
                await msg.term()
            return
        meta = getattr(msg, "metadata", None)
        events = MediaEvents(self.js, cmd, getattr(meta, "num_delivered", None) or 1)
        job_id, data = cmd["job_id"], cmd["data"]
        self.job, cancel = job_id, asyncio.Event()
        beat = asyncio.create_task(self._wip(msg))
        tmp = Path(tempfile.mkdtemp(prefix="media-"))  # 0700
        stop = failure = result = None
        extra: dict = {}
        try:
            if await cancels.requested(self.cancels_kv, job_id):
                status = "cancelled"
            else:
                stop = await cancels.watch(self.cancels_kv, job_id, cancel.set)
                if data["kind"] == "export":
                    planned = "copy" if data["export"]["mode"] == "copy" else self.encoders[0]
                    await events.publish("status", {"status": "running", "encoder": planned})
                    result, encoder = await self.export(data, events, tmp, cancel)
                    extra = {"encoder": encoder}
                elif (
                    data["kind"] == "bake"
                ):  # API 1.82: an export's conform with studio's own graph
                    await events.publish(
                        "status", {"status": "running", "encoder": self.encoders[0]}
                    )
                    result, encoder = await self.export(data, events, tmp, cancel, "bake")
                    extra = {"encoder": encoder}
                elif data["kind"] == "mux":
                    await events.publish("status", {"status": "running", "encoder": "copy"})
                    result = await self.mux(data, events, tmp, cancel)
                    extra = {"encoder": "copy"}
                else:
                    await events.publish("status", {"status": "running"})
                    job = self.thumbs if data["kind"] == "thumbs" else self.frame
                    result = await job(data, tmp, cancel)
                status = "succeeded"
        except Cancelled:
            status = "cancelled"
        except MediaError as e:
            status, failure = "failed", e
        except Exception as e:
            log.exception("media job %s failed", job_id)
            status, failure = (
                "failed",
                MediaError("internal", f"the runner failed ({type(e).__name__})"),
            )
        finally:
            beat.cancel()
            if stop:
                with contextlib.suppress(Exception):
                    await stop()
            shutil.rmtree(tmp, ignore_errors=True)
            self.job = None
        log.info(
            "media job %s: %s%s", job_id, status, f" ({failure.cls}: {failure})" if failure else ""
        )
        body: dict = {"status": status}
        if failure is not None:
            body["failure"] = {"class": failure.cls, "detail": str(failure) or failure.cls}
        if status == "succeeded":
            body |= extra | {"result": result}
        try:
            await events.publish("status", body)
            await msg.ack_sync()
        except Exception:  # not acked: redelivered after ACK_WAIT, and the job runs again
            log.exception("media job %s: could not report %s", job_id, status)

    async def _wip(self, msg) -> None:
        while True:
            await asyncio.sleep(WIP)
            with contextlib.suppress(Exception):
                await msg.in_progress()

    async def _fetch(
        self, inputs: list[dict], tmp: Path, cancel: asyncio.Event, done=None
    ) -> tuple[list[Path], list[dict]]:
        """Each input downloaded once (at most LIMIT each, BUDGET in all, never into the disk's last RESERVE; a cancel
        stops it between and during downloads), probed, checked against its kind and named in<n>.<ext> by its
        content; `done(n)` after each."""
        paths, infos, total = [], [], 0
        for n, it in enumerate(inputs):
            if cancel.is_set():
                raise Cancelled
            raw = tmp / f"in{n}"
            free = shutil.disk_usage(tmp).free - RESERVE
            limit = min(LIMIT, BUDGET - total, free)
            if limit < 1:
                raise MediaError(
                    "input",
                    f"no room for input {n} (inputs over {BUDGET >> 30} GB or the disk full)",
                )
            got = await self.store.download(it["key"], raw, limit, stop=cancel.is_set)
            if cancel.is_set():
                raise Cancelled
            if got is None:
                raise MediaError("input", f"input {n} is missing")
            if got[0] > limit:
                why = (
                    f"larger than {LIMIT >> 30} GB"
                    if limit == LIMIT
                    else "past the job's room on disk"
                )
                raise MediaError("input", f"input {n} is {why}")
            total += got[0]
            info = await asyncio.to_thread(media.probe, raw)
            media.check_kind(info, it["kind"])
            ext = (
                info["container"]
                if it["kind"] in ("video", "audio")
                else IMAGE_EXT[info["video"]["codec_name"]]
            )
            paths.append(raw.rename(tmp / f"in{n}.{ext}"))
            infos.append(info)
            if done:
                await done(n)
        return paths, infos

    async def export(
        self,
        data: dict,
        events: MediaEvents,
        tmp: Path,
        cancel: asyncio.Event,
        name: str = "export",
    ):
        """An export, or (API 1.82, `name` bake) a bake: the same conform, verify and pictures, named <name>.*."""
        ex = data[name]
        last = [time.monotonic()]

        async def fetched(
            _n: int,
        ) -> None:  # a download is progress too: frames 0 until ffmpeg runs
            if time.monotonic() - last[0] >= PROGRESS:
                await events.publish("progress", {"frames": 0, "total": ex["frames"]})
                last[0] = time.monotonic()

        paths, infos = await self._fetch(data["inputs"], tmp, cancel, fetched)
        mode, args, reason, listfile = ex.get("mode", "conform"), ex["args"], None, None
        if mode == "copy":
            for e in ex["expect"]:
                if e["input"] >= len(infos):
                    raise MediaError(
                        "invalid", f"expect names input {e['input']}, which is not an input"
                    )
                got = infos[e["input"]]["stream"] or {}
                if diff := next((k for k, v in e["stream"].items() if got.get(k) != v), None):
                    mode, args = "conform", ex["conform_args"]
                    reason = f"clip input {e['input']}: {diff} differs"
                    break
            if mode == "copy":
                if any(i >= len(paths) for i in ex["concat"]):
                    raise MediaError("invalid", "concat names an index that is not an input")
                listfile = tmp / "list.txt"
                listfile.write_text(media.concat_list([paths[i] for i in ex["concat"]],
                                                      [(infos[i]["video"] or {}).get("duration") for i in ex["concat"]]))  # fmt: skip
        encoder = "copy" if mode == "copy" else self.encoders[0]
        out = tmp / f"{name}.mp4"
        argv = media.build(args, paths, out=out, listfile=listfile,
                           venc_args=media.venc(self.encoders[0], ex["video_bitrate"]))  # fmt: skip
        await self._ffmpeg(argv, ex["frames"], events, cancel, tmp)
        if cancel.is_set():
            raise Cancelled
        info = await asyncio.to_thread(self.verify, out, ex)
        v, frames, target = info["video"], info["frames"], ex["target"]
        prefix = data["output_prefix"]
        pictures = {}
        job = name
        for pic, index in (("poster", frames // 10), ("last", frames - 1)):
            png = tmp / f"{pic}.png"
            try:
                await asyncio.to_thread(media.frame_png, out, index, png)
            except MediaError as e:  # the output, not an input, lacks the frame
                raise MediaError("verify", str(e)) from None
            key = f"{prefix}{job}.{pic}.png"
            body = png.read_bytes()
            if pic == "poster":  # API 1.48: its thumbnail first, export.thumb.webp
                thumb = await self._thumb(body, f"{prefix}{job}.thumb.webp")
            await self.store.put(key, body, "image/png")
            pictures[pic] = {
                "key": key,
                "width": v["width"],
                "height": v["height"],
                "bytes": len(body),
            }
        key = f"{prefix}{job}.mp4"
        size, etag = await self.store.upload(key, out, "video/mp4")
        fps = float(Fraction(target["rate"]))
        video = {"duration_s": frames / fps, "fps": fps, "frames": frames, "has_audio": info["audio"] is not None,
                 **pictures, "stream": info["stream"]}  # fmt: skip
        result = {"key": key, "width": v["width"], "height": v["height"], "bytes": size, "etag": etag,
                  "video": video} | ({"thumb": thumb} if thumb else {}) | ({"mode": mode} if name == "export" else {})  # fmt: skip
        if reason:
            result["reason"] = reason
        return result, encoder

    async def mux(self, data: dict, events: MediaEvents, tmp: Path, cancel: asyncio.Event) -> dict:
        """API 1.79: inputs[0]'s picture copied with inputs[1] as its only sound (core.media.mux's arguments, here on
        files with progress and cancel), mux.mp4 with mux.poster.png (frame 0) and mux.last.png."""
        (video, sound), (vinfo, _) = await self._fetch(data["inputs"][:2], tmp, cancel)
        total = vinfo["frames"]
        if total < 1:
            raise MediaError("input", "the video has no frames")
        out = tmp / "mux.mp4"
        argv = ["-hide_banner", "-nostdin", "-loglevel", "error", "-y", "-progress", "pipe:1", "-nostats",
                "-protocol_whitelist", "file", "-i", str(video), "-protocol_whitelist", "file", "-i", str(sound),
                "-map", "0:v:0", "-map", "1:a:0", "-c:v", "copy", "-af", "apad", "-c:a", "aac", "-ar", "48000",
                "-ac", "2", "-shortest", "-movflags", "+faststart", "-f", "mp4", str(out)]  # fmt: skip
        await self._ffmpeg(argv, total, events, cancel, tmp)
        if cancel.is_set():
            raise Cancelled
        try:
            info = await asyncio.to_thread(media.probe, out) if out.is_file() else None
        except MediaError:
            info = None
        if info is None or info["video"] is None or info["audio"] is None:
            raise MediaError("verify", "the output has no picture or no sound")
        v, frames = info["video"], info["frames"]
        if abs(frames - total) > 1:
            raise MediaError("verify", f"the output has {frames} frames, not {total}")
        prefix, pictures = data["output_prefix"], {}
        for name, index in (("poster", 0), ("last", frames - 1)):
            png = tmp / f"{name}.png"
            try:
                await asyncio.to_thread(media.frame_png, out, index, png)
            except MediaError as e:
                raise MediaError("verify", str(e)) from None
            key, body = f"{prefix}mux.{name}.png", png.read_bytes()
            await self.store.put(key, body, "image/png")
            pictures[name] = {
                "key": key,
                "width": v["width"],
                "height": v["height"],
                "bytes": len(body),
            }
        key = f"{prefix}mux.mp4"
        size, etag = await self.store.upload(key, out, "video/mp4")
        fps = float(Fraction(v.get("r_frame_rate", "0/1")))
        if fps <= 0:
            raise MediaError("verify", "the output has no frame rate")
        video = {"duration_s": frames / fps, "fps": fps, "frames": frames, "has_audio": True, **pictures,
                 "stream": info["stream"]}  # fmt: skip
        return {
            "key": key,
            "width": v["width"],
            "height": v["height"],
            "bytes": size,
            "etag": etag,
            "video": video,
        }

    @staticmethod
    def verify(out: Path, ex: dict) -> dict:
        """The output's probe when it has the target's size, frame rate and sample rate and is within one frame
        of `frames`, else MediaError verify."""
        if not out.is_file():
            raise MediaError("verify", "ffmpeg wrote no output")
        try:
            info = media.probe(out)
        except MediaError:
            raise MediaError("verify", "the output cannot be read") from None
        v, a, t = info["video"], info["audio"], ex["target"]
        if v is None or (v["width"], v["height"]) != (t["width"], t["height"]):
            raise MediaError("verify", f"the output is not {t['width']}x{t['height']}")
        if Fraction(v.get("r_frame_rate", "0/1")) != Fraction(t["rate"]):
            raise MediaError(
                "verify", f"the output's frame rate {v.get('r_frame_rate')} is not {t['rate']}"
            )
        if a is None or int(a.get("sample_rate", 0)) != t["sample_rate"]:
            raise MediaError("verify", f"the output's sound is not {t['sample_rate']} Hz")
        if abs(info["frames"] - ex["frames"]) > 1:
            raise MediaError(
                "verify", f"the output has {info['frames']} frames, not {ex['frames']}"
            )
        return info

    async def _ffmpeg(
        self, argv: list[str], total: int, events, cancel: asyncio.Event, tmp: Path
    ) -> None:
        """ffmpeg in its own process group (a list, never a shell), progress from its `frame=N` lines only when the
        count moved; a cancel stops the group (SIGTERM, KILL_AFTER, SIGKILL) and raises Cancelled; past its deadline
        (DEADLINE_BASE + DEADLINE_PER_FRAME × total, at most DEADLINE_MAX) it is stopped: MediaError ffmpeg; a
        non-zero exit is MediaError ffmpeg with its last error line."""
        proc = await asyncio.create_subprocess_exec(
            media.FFMPEG, *argv, stdin=asyncio.subprocess.DEVNULL, stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE, cwd=tmp, start_new_session=True,
        )  # fmt: skip
        state = {"frames": 0}
        err = bytearray()

        async def read_progress() -> None:
            async for line in proc.stdout:
                if line.startswith(b"frame="):
                    with contextlib.suppress(ValueError):
                        state["frames"] = int(line[6:].strip())

        async def read_err() -> None:
            while chunk := await proc.stderr.read(1 << 14):
                err.extend(chunk)
                del err[: max(0, len(err) - (1 << 16))]

        readers = asyncio.gather(read_progress(), read_err())
        waiter = asyncio.create_task(proc.wait())
        start = at = time.monotonic()
        deadline = start + min(DEADLINE_BASE + DEADLINE_PER_FRAME * total, DEADLINE_MAX)
        sent = None
        try:
            while not waiter.done():
                await asyncio.wait({waiter}, timeout=POLL)
                if cancel.is_set():
                    await self._stop(proc, waiter)
                    raise Cancelled
                n, now = state["frames"], time.monotonic()
                if now > deadline:
                    await self._stop(proc, waiter)
                    raise MediaError(
                        "ffmpeg", f"timed out after {round(now - start)} s at frame {n} of {total}"
                    )
                if n != sent and now - at >= PROGRESS:
                    d = {"frames": n, "total": total}
                    if 0 < n < total:
                        d["eta_s"] = round((now - start) / n * (total - n), 1)
                    await events.publish("progress", d)
                    sent, at = n, now
            await readers
        finally:
            if proc.returncode is None:  # cancelled from outside (shutdown) or a publish failed
                await self._stop(proc, waiter)
            readers.cancel()
        if proc.returncode:
            raise MediaError("ffmpeg", media.last_line(err.decode(errors="replace")))

    @staticmethod
    async def _stop(proc, waiter) -> None:
        for sig, wait in ((signal.SIGTERM, KILL_AFTER), (signal.SIGKILL, None)):
            with contextlib.suppress(ProcessLookupError):
                os.killpg(proc.pid, sig)
            done, _ = await asyncio.wait({waiter}, timeout=wait)
            if done:
                return

    async def frame(self, data: dict, tmp: Path, cancel: asyncio.Event) -> dict:
        paths, infos = await self._fetch(data["inputs"][:1], tmp, cancel)
        index, frames = data["frame"]["index"], infos[0]["frames"]
        if index >= frames:
            raise MediaError("input", f"the video has {frames} frames, no frame {index}")
        png = tmp / "frame.png"
        await asyncio.to_thread(media.frame_png, paths[0], index, png)
        body = png.read_bytes()
        with Image.open(png) as im:
            w, h = im.size
        key = f"{data['output_prefix']}frame.png"
        thumb = await self._thumb(body, f"{data['output_prefix']}frame.thumb.webp")  # API 1.48
        await self.store.put(key, body, "image/png")
        return {
            "key": key,
            "width": w,
            "height": h,
            "bytes": len(body),
            "etag": hashlib.md5(body).hexdigest(),
        } | ({"thumb": thumb} if thumb else {})

    async def _thumb(self, body: bytes, key: str) -> dict | None:
        """The thumbnail of the image file `body` written at `key` (README, Thumbnails), or None when it needs none."""
        made = await asyncio.to_thread(images.thumb_of, body)
        if made is None:
            return None
        webp, w, h = made
        await self.store.put(key, webp, "image/webp")
        return {"key": key, "width": w, "height": h, "bytes": len(webp)}

    async def thumbs(self, data: dict, tmp: Path, cancel: asyncio.Event) -> dict:
        """API 1.48, kind thumbs (README, Thumbnails): each input's thumbnail at <output_prefix><i>.webp, or `skip`
        when it needs none, or `error` when it cannot be read (the job goes on). Only PIL: no download to disk."""
        prefix, out = data["output_prefix"], []
        keys = {it["key"] for it in data["inputs"]}
        for i, it in enumerate(data["inputs"]):
            if cancel.is_set():
                raise Cancelled
            key = f"{prefix}{i}.webp"
            body = await self.store.get(it["key"], images.MAX_BYTES)
            if key in keys:  # never overwrite an input
                out.append({"index": i, "error": f"input {i} is an output key"})
            elif body is None:
                out.append({"index": i, "error": f"input {i} is missing"})
            elif len(body) > images.MAX_BYTES:
                out.append(
                    {"index": i, "error": f"input {i} is larger than {images.MAX_BYTES >> 20} MB"}
                )
            else:
                try:
                    made = await self._thumb(body, key)
                except ValueError as e:
                    out.append({"index": i, "error": f"input {i}: {e}"[:300]})
                    continue
                out.append({"index": i} | (made or {"skip": True}))
        return {"thumbs": out}
