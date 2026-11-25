"""helmstudio plugin media-tools (contracts/README.md "Plugins: takes", alpha.2 items 11 and 13): Assistant tools that
make a new take from your takes (the original stays) and pull frames from a video for the Assistant to look at.

Stdlib HTTP server on MEDIA_TOOLS_HOST:MEDIA_TOOLS_PORT (127.0.0.1:8422):

    GET  /health            → 200 {"ok": true}
    POST /tools/<tool>      {"input": {...}} → 200 {"result", "line", "take_ids"?, "images"?}

Errors are {"detail": "..."}: 422 bad input, 404 a take that is not yours, 502 studio did not answer, 500 ffmpeg or
Pillow failed. Every read and write goes through studio's plugin API with the call's pass (Authorization), at
X-Helm-Api (or HELM_API_URL): never a user token, never the database. ffmpeg and ffprobe come from PATH (FFMPEG,
FFPROBE override) and always run as argument lists, never through a shell, with fixed codecs and time limits."""

import base64
import json
import os
import re
import shutil
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

from jsonschema import Draft202012Validator, FormatChecker
from PIL import Image, ImageOps

HERE = Path(__file__).parent
TOOLS = {t["name"]: t for t in json.loads((HERE / "plugin.json").read_text())["tools"]}
CHECK = {
    n: Draft202012Validator(t["input"], format_checker=FormatChecker()) for n, t in TOOLS.items()
}
MAX_BODY = 16_000
MAX_IN = 2 << 30  # a video at most 2 GB (studio's cap); an image or audio 50 MB
MAX_SECONDS = 600  # an input's length
BUDGET = 27.0  # seconds of work per call (pipelines waits 30)
Image.MAX_IMAGE_PIXELS = 50_000_000  # Pillow refuses past it (DecompressionBombError)
API_RE = re.compile(r"^https?://(localhost|127\.0\.0\.1|\[::1\])(:\d{1,5})?$")
VIDEO = [
    "-c:v",
    "libx264",
    "-preset",
    "veryfast",
    "-crf",
    "18",
    "-pix_fmt",
    "yuv420p",
    "-movflags",
    "+faststart",
]
AAC = ["-c:a", "aac", "-b:a", "192k"]


class Fail(Exception):
    """A call's failure: its status and plain detail."""

    def __init__(self, code: int, detail: str) -> None:
        super().__init__(detail)
        self.code, self.detail = code, detail[:300]


# --- studio's plugin API (API 1.70), as the call's pass


class Studio:
    def __init__(self, api: str, token: str, deadline: float = float("inf")) -> None:
        self.api, self.token, self.deadline = api.rstrip("/"), token, deadline

    def in_time(self, need: float = 3.0) -> float:
        """Seconds left of the call's budget; Fail when fewer than `need` are (pipelines would give up first and the
        take would land with no line and no Undo)."""
        left = self.deadline - time.monotonic()
        if left < need:
            raise Fail(500, "it took too long; try a shorter part")
        return left

    def _ask(self, method: str, path: str, body: dict | None = None) -> dict:
        req = urllib.request.Request(f"{self.api}/v1/studio/plugin{path}", method=method,
                                     data=None if body is None else json.dumps(body).encode(),
                                     headers={"Authorization": f"Bearer {self.token}",
                                              "Content-Type": "application/json"})  # fmt: skip
        try:
            with urllib.request.urlopen(req, timeout=10) as r:
                return json.loads(r.read())
        except urllib.error.HTTPError as e:
            try:
                detail = json.loads(e.read()).get("detail") or f"HTTP {e.code}"
            except ValueError:
                detail = f"HTTP {e.code}"
            if e.code == 404:
                raise Fail(404, "that take is not one of yours, or it is gone") from None
            raise Fail(422 if e.code == 422 else 502, f"studio: {detail}") from None
        except (OSError, ValueError):
            raise Fail(502, "studio did not answer") from None

    def take(self, take_id: str) -> dict:
        return self._ask("GET", f"/takes/{take_id}")

    def fetch(self, take: dict, dest: Path) -> Path:
        """The take's file into dest (at most MAX_IN bytes)."""
        cap = MAX_IN if take["kind"] == "video" else 50 << 20
        if take["bytes"] > cap:
            raise Fail(422, "the take is too large for media tools")
        try:
            with urllib.request.urlopen(take["url"], timeout=20) as r, dest.open("wb") as f:
                n = 0
                while chunk := r.read(1 << 20):
                    n += len(chunk)
                    if n > cap:
                        raise Fail(422, "the take is too large for media tools")
                    self.in_time(0)
                    f.write(chunk)
        except OSError:
            raise Fail(502, "could not read the take's file") from None
        return dest

    def make(self, body: dict, file: Path, poster: Path | None = None) -> dict:
        """A new take: create it, PUT its file (and a video's poster), complete it."""
        self.in_time()
        up = self._ask("POST", "/takes", body | {"bytes": file.stat().st_size})
        _put(up["upload_url"], file, body["content_type"], self.in_time())
        if poster is not None:
            _put(
                up["poster_upload_url"],
                poster,
                body["video"]["poster"]["content_type"],
                self.in_time(),
            )
        self.in_time(1)  # not completed, the pending take is discarded and never shown
        return self._ask("POST", f"/takes/{up['take_id']}/complete")


def _put(url: str, file: Path, ctype: str, left: float = 60) -> None:
    with file.open("rb") as f:
        req = urllib.request.Request(url, method="PUT", data=f,
                                     headers={"Content-Type": ctype, "Content-Length": str(file.stat().st_size)})  # fmt: skip
        try:
            urllib.request.urlopen(req, timeout=min(60, left)).close()
        except (OSError, ValueError):
            raise Fail(502, "could not upload the new take") from None


# --- ffmpeg, as argument lists only


def _bin(name: str) -> str:
    path = os.environ.get(name.upper()) or shutil.which(name)
    if not path:
        raise Fail(500, "media tools need ffmpeg and ffprobe (brew install ffmpeg)")
    return path


def run(args: list[str], deadline: float) -> str:
    """ffmpeg with args (a list, never a shell string); its stderr. Fail with its last line, or on the time limit."""
    left = deadline - time.monotonic()
    if left <= 0:
        raise Fail(500, "it took too long; try a shorter part")
    try:
        p = subprocess.run([_bin("ffmpeg"), "-nostdin", "-hide_banner", "-y", *args], capture_output=True, text=True, check=False,
                           timeout=left, stdin=subprocess.DEVNULL)  # fmt: skip
    except subprocess.TimeoutExpired:
        raise Fail(500, "it took too long; try a shorter part") from None
    if p.returncode != 0:
        last = next(
            (x for x in reversed(p.stderr.strip().splitlines()) if x.strip()), "ffmpeg failed"
        )
        raise Fail(500, f"ffmpeg: {last.strip()}")
    return p.stderr


def probe(path: Path) -> dict:
    """{duration, width, height, fps, frames, audio} of a media file."""
    p = subprocess.run([_bin("ffprobe"), "-v", "error", "-show_streams", "-show_format", "-of", "json", str(path)],
                       capture_output=True, text=True, timeout=10, stdin=subprocess.DEVNULL, check=False)  # fmt: skip
    if p.returncode != 0:
        raise Fail(422, "the file is not a video or audio ffprobe can read")
    info = json.loads(p.stdout)
    v = next((s for s in info.get("streams", []) if s.get("codec_type") == "video"), None)
    out = {"duration": float(info.get("format", {}).get("duration") or 0), "audio": any(
        s.get("codec_type") == "audio" for s in info.get("streams", []))}  # fmt: skip
    if v:
        num, _, den = (v.get("avg_frame_rate") or "0/1").partition("/")
        fps = float(num) / float(den or 1) if float(den or 1) else 0.0
        out |= {"width": int(v["width"]), "height": int(v["height"]), "fps": round(fps, 3) or 24.0,
                "frames": int(v.get("nb_frames") or 0) or max(1, round(out["duration"] * (fps or 24)))}  # fmt: skip
    return out


def _tc(t: float) -> str:
    m, s = divmod(max(t, 0.0), 60)
    return f"{int(m)}:{s:05.2f}"


def _last(info: dict) -> float:
    """The time of a video's last whole frame: a seek past it gives no frame."""
    return max(info["duration"] - 1.5 / (info.get("fps") or 24), 0.0)


def _short(title: str) -> str:
    return title if len(title) <= 120 else title[:119] + "…"


def _span(trim: dict | None, length: float) -> tuple[float, float]:
    start, end = (trim["start"], min(trim["end"], length)) if trim else (0.0, length)
    if end - start <= 0.04:
        raise Fail(422, f"trim must lie inside the take (0 to {length:.2f} s), end after start")
    return start, end


def _atempo(speed: float) -> str:
    """atempo takes 0.5-2 per step: 0.25 → 0.5,0.5; 4 → 2,2."""
    steps = []
    while speed > 2:
        steps.append(2.0)
        speed /= 2
    while speed < 0.5:
        steps.append(0.5)
        speed /= 0.5
    return ",".join(f"atempo={x:g}" for x in [*steps, speed])


# --- the tools


def _fits(w: int, h: int) -> None:
    """Checked before Pillow allocates: resize keeps the aspect from one side, so a 1 × 4000 crop resized to 8192 wide
    would ask for 268 gigapixels."""
    if w * h > 50_000_000 or max(w, h) > 16384:
        raise Fail(422, "the result would be over 50 MP or 16384 px")


def edit_image(st: Studio, inp: dict, work: Path, deadline: float) -> dict:
    src = st.take(inp["take_id"])
    if src["kind"] != "image":
        raise Fail(422, "edit_image needs an image take")
    try:
        img = ImageOps.exif_transpose(Image.open(st.fetch(src, work / "in")))
    except (OSError, Image.DecompressionBombError):
        raise Fail(422, "the take is not an image Pillow can read (or is over 50 MP)") from None
    done = []
    if c := inp.get("crop"):
        if c["x"] + c["width"] > img.width or c["y"] + c["height"] > img.height:
            raise Fail(422, f"crop must lie inside the image ({img.width} × {img.height})")
        img = img.crop((c["x"], c["y"], c["x"] + c["width"], c["y"] + c["height"]))
        done.append("cropped")
    if r := inp.get("rotate"):
        img = img.rotate(-r, expand=True)
        done.append(f"rotated {r}°")
    if f := inp.get("flip"):
        img = ImageOps.mirror(img) if f == "horizontal" else ImageOps.flip(img)
        done.append(f"flipped {f}")
    if z := inp.get("resize"):
        w = z.get("width") or max(1, round(img.width * z["height"] / img.height))
        h = z.get("height") or max(1, round(img.height * z["width"] / img.width))
        _fits(w, h)
        img = img.resize((w, h), Image.Resampling.LANCZOS)
        done.append(f"resized to {w} × {h}")
    if e := inp.get("extend"):
        color = e.get("color", "#000000")
        if color == "transparent":
            img, fill = img.convert("RGBA"), (0, 0, 0, 0)
        else:
            fill = tuple(int(color[i : i + 2], 16) for i in (1, 3, 5))
            fill = fill + (255,) if img.mode == "RGBA" else fill
            img = img if img.mode in ("RGB", "RGBA") else img.convert("RGB")
        _fits(
            img.width + e.get("left", 0) + e.get("right", 0),
            img.height + e.get("top", 0) + e.get("bottom", 0),
        )
        img = ImageOps.expand(
            img, (e.get("left", 0), e.get("top", 0), e.get("right", 0), e.get("bottom", 0)), fill
        )
        done.append("extended")
    fmt = inp.get("format") or {"image/png": "png", "image/webp": "webp"}.get(
        src["content_type"], "jpeg"
    )
    if inp.get("format"):
        done.append(f"as {fmt.upper()}")
    _fits(*img.size)
    if fmt == "jpeg" and img.mode != "RGB":
        img = img.convert("RGB")
    out = work / f"out.{fmt}"
    img.save(out, format=fmt.upper(), **({"quality": 92} if fmt != "png" else {}))
    return _made(st, "edit_image", inp, src, out, "image", f"image/{fmt}", done, img.size)


def edit_video(st: Studio, inp: dict, work: Path, deadline: float) -> dict:
    src = st.take(inp["take_id"])
    if src["kind"] != "video":
        raise Fail(422, "edit_video needs a video take")
    if (src.get("duration_s") or 0) > MAX_SECONDS:
        raise Fail(422, "media tools take videos of at most 10 minutes")
    path = st.fetch(src, work / "in.mp4")
    if (length := probe(path)["duration"]) > MAX_SECONDS:  # studio's duration_s may be null
        raise Fail(422, "media tools take videos of at most 10 minutes")
    start, end = _span(inp.get("trim"), length)
    args, done = ["-ss", f"{start:.3f}", "-to", f"{end:.3f}", "-i", str(path)], []
    if inp.get("trim"):
        done.append(f"trimmed {_tc(start)}–{_tc(end)}")
    parents, speed = [src["id"]], inp.get("speed", 1)
    vf, af = ([f"setpts=PTS/{speed:g}"], [_atempo(speed)]) if speed != 1 else ([], [])
    if speed != 1:
        done.append(f"{speed:g}× speed")
    if aid := inp.get("audio_take_id"):
        snd = st.take(aid)
        if snd["kind"] != "audio":
            raise Fail(422, "audio_take_id must be an audio take")
        args += ["-i", str(st.fetch(snd, work / "sound"))]
        maps, af = ["-map", "0:v:0", "-map", "1:a:0", "-shortest"], []
        parents.append(aid)
        done.append(f"sound from {_short(snd['title'])}")
    elif inp.get("mute"):
        maps, af = ["-map", "0:v:0", "-an"], []
        done.append("muted")
    else:
        maps = ["-map", "0:v:0", "-map", "0:a?"]
    out = work / "out.mp4"
    run([*args, *maps, *(["-filter:v", ",".join(vf)] if vf else []), *(["-filter:a", ",".join(af)] if af else []),
         *VIDEO, *AAC, "-t", f"{(end - start) / speed:.3f}", str(out)], deadline)  # fmt: skip
    info = probe(out)
    poster = work / "poster.jpg"
    run(
        [
            "-ss",
            f"{info['duration'] / 10:.3f}",
            "-i",
            str(out),
            "-frames:v",
            "1",
            "-q:v",
            "3",
            str(poster),
        ],
        deadline,
    )
    video = {"duration_s": round(info["duration"], 3), "fps": info["fps"], "frames": info["frames"],
             "has_audio": info["audio"], "poster": {"content_type": "image/jpeg", "bytes": poster.stat().st_size,
                                                     "width": info["width"], "height": info["height"]}}  # fmt: skip
    return _made(st, "edit_video", inp, src, out, "video", "video/mp4", done, (info["width"], info["height"]),
                 parents=parents, video=video, poster=poster)  # fmt: skip


def frame_image(st: Studio, inp: dict, work: Path, deadline: float) -> dict:
    src = st.take(inp["take_id"])
    if src["kind"] != "video":
        raise Fail(422, "frame_image needs a video take")
    path = st.fetch(src, work / "in.mp4")
    at = min(inp["at"], _last(probe(path)))
    out = work / "frame.png"
    run(["-ss", f"{at:.3f}", "-i", str(path), "-frames:v", "1", str(out)], deadline)
    with Image.open(out) as img:
        size = img.size
    return _made(
        st, "frame_image", inp, src, out, "image", "image/png", [f"frame at {_tc(at)}"], size
    )


def _audio_in(st: Studio, take_id: str, work: Path, name: str) -> tuple[dict, Path]:
    src = st.take(take_id)
    if src["kind"] not in ("audio", "video") or (
        src["kind"] == "video" and src.get("has_audio") is False
    ):
        raise Fail(422, "an audio take (or a video with sound) is needed")
    if (src.get("duration_s") or 0) > MAX_SECONDS:
        raise Fail(422, "media tools take sounds of at most 10 minutes")
    return src, st.fetch(src, work / name)


def edit_audio(st: Studio, inp: dict, work: Path, deadline: float) -> dict:
    src, path = _audio_in(st, inp["take_id"], work, "in")
    if (length := probe(path)["duration"]) > MAX_SECONDS:
        raise Fail(422, "media tools take sounds of at most 10 minutes")
    start, end = _span(inp.get("trim"), length)
    done = [f"trimmed {_tc(start)}–{_tc(end)}"] if inp.get("trim") else []
    af = []
    if (db := inp.get("volume_db")) is not None:
        af = [f"volume={db:g}dB"]
        done.append(f"{db:+g} dB")
    out = work / "out.m4a"
    run(["-ss", f"{start:.3f}", "-to", f"{end:.3f}", "-i", str(path), "-vn", *(["-filter:a", ",".join(af)] if af else []),
         *AAC, str(out)], deadline)  # fmt: skip
    return _sound(st, "edit_audio", inp, src, out, done, [src["id"]])


def join_audio(st: Studio, inp: dict, work: Path, deadline: float) -> dict:
    ins = [_audio_in(st, t, work, f"in{i}") for i, t in enumerate(inp["take_ids"])]
    n = len(ins)
    graph = "".join(
        f"[{i}:a:0]aresample=48000,aformat=channel_layouts=stereo[a{i}];" for i in range(n)
    )
    graph += "".join(f"[a{i}]" for i in range(n)) + f"concat=n={n}:v=0:a=1[out]"
    out = work / "out.m4a"
    run([*(x for _, p in ins for x in ("-i", str(p))), "-filter_complex", graph, "-map", "[out]", *AAC, str(out)],
        deadline)  # fmt: skip
    return _sound(
        st, "join_audio", inp, ins[0][0], out, [f"joined {n} sounds"], [s["id"] for s, _ in ins]
    )


def frames(st: Studio, inp: dict, work: Path, deadline: float) -> dict:
    src = st.take(inp["take_id"])
    if src["kind"] != "video":
        raise Fail(422, "frames needs a video take")
    path = st.fetch(src, work / "in.mp4")
    info = probe(path)
    length = info["duration"]
    if inp.get("at"):
        times, how = sorted({min(t, _last(info)) for t in inp["at"]}), "at the times asked"
    elif inp.get("scenes"):
        err = run(
            ["-i", str(path), "-vf", "select='gt(scene,0.3)',showinfo", "-an", "-f", "null", "-"],
            deadline,
        )
        found = [float(m) for m in re.findall(r"pts_time:([0-9.]+)", err)]
        times, how = ([0.0, *found][:8], "at scene changes")
    else:
        n = inp.get("count", 4)
        times, how = [length * (2 * k + 1) / (2 * n) for k in range(n)], f"{n} evenly spaced"
    side, images, got = inp.get("size", 768), [], []
    scale = f"scale=w='min(iw,{side})':h='min(ih,{side})':force_original_aspect_ratio=decrease"
    for i, t in enumerate(times):
        out = work / f"f{i}.jpg"
        run(
            [
                "-ss",
                f"{t:.3f}",
                "-i",
                str(path),
                "-frames:v",
                "1",
                "-vf",
                scale,
                "-q:v",
                "4",
                str(out),
            ],
            deadline,
        )
        images.append(base64.b64encode(out.read_bytes()).decode())
        got.append({"at": round(t, 3), "timecode": _tc(t)})
    return {"result": {"take_id": src["id"], "title": src["title"], "duration_s": round(length, 3), "frames": got,
                       "note": "the frames come with your next prompt, in this order"},
            "line": f"Frames of {_short(src['title'])} · {len(got)} {how}"[:300], "images": images}  # fmt: skip


def _sound(st, tool, inp, src, out, done, parents) -> dict:
    return _made(st, tool, inp, src, out, "audio", "audio/mp4", done, None, parents=parents,
                 duration=round(probe(out)["duration"], 3))  # fmt: skip


def _made(st, tool, inp, src, out, kind, ctype, done, size, parents=None, video=None, poster=None,
          duration=None) -> dict:  # fmt: skip
    """Make the new take and answer it: the original stays."""
    what = ", ".join(done) or tool.replace("_", " ")
    body = {"parents": parents or [src["id"]], "tool": tool, "input": inp, "kind": kind, "content_type": ctype,
            "title": f"{_short(src['title'])} · {what}"[:200]}  # fmt: skip
    if size:
        body |= {"width": size[0], "height": size[1]}
    if video:
        body["video"] = video
    if duration:
        body["duration_s"] = duration
    take = st.make(body, out, poster)
    return {"result": {"take_id": take["id"], "title": take["title"], "kind": kind,
                       "show": f"[[take:{take['id']}]]", "note": "a new take; the original stays"},
            "line": f"{what[0].upper() + what[1:]} · {_short(src['title'])}"[:300], "take_ids": [take["id"]]}  # fmt: skip


RUN = {"edit_image": edit_image, "edit_video": edit_video, "frame_image": frame_image, "edit_audio": edit_audio,
       "join_audio": join_audio, "frames": frames}  # fmt: skip


def answer(tool: str, body, headers) -> tuple[int, dict]:
    """(status, JSON body) for one POST /tools/<tool>."""
    if tool not in RUN:
        return 404, {"detail": "not found"}
    inp = body.get("input") if isinstance(body, dict) else None
    if not isinstance(inp, dict) or (err := next(CHECK[tool].iter_errors(inp), None)):
        return 422, {"detail": f"input: {err.message if inp is not None else 'missing'}"[:300]}
    api = os.environ.get("HELM_API_URL") or headers.get("X-Helm-Api") or ""
    token = (headers.get("Authorization") or "").removeprefix("Bearer ").strip()
    if not API_RE.match(api) and not os.environ.get("HELM_API_URL"):
        return 422, {"detail": "X-Helm-Api must be the stack on this machine"}
    if not token:
        return 401, {"detail": "no pass"}
    try:
        with tempfile.TemporaryDirectory(prefix="media-tools-") as tmp:
            os.chmod(tmp, 0o700)
            return 200, RUN[tool](
                Studio(api, token, deadline := time.monotonic() + BUDGET), inp, Path(tmp), deadline
            )
    except Fail as e:
        return e.code, {"detail": e.detail}


class Handler(BaseHTTPRequestHandler):
    def reply(self, code: int, body: dict) -> None:
        out = json.dumps(body, ensure_ascii=False).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(out)))
        self.end_headers()
        self.wfile.write(out)

    def log_message(
        self, *_
    ):  # no access log: studio's health checks would grow ~/.helm/logs/media-tools.log
        pass

    def do_GET(self):
        self.reply(
            *((200, {"ok": True}) if self.path == "/health" else (404, {"detail": "not found"}))
        )

    def do_POST(self):
        if not self.path.startswith("/tools/"):
            return self.reply(404, {"detail": "not found"})
        n = int(self.headers.get("Content-Length") or 0)
        if n > MAX_BODY:
            return self.reply(413, {"detail": "the request is too large"})
        try:
            body = json.loads(self.rfile.read(n) or b"null")
        except ValueError:
            return self.reply(422, {"detail": "the body is not JSON"})
        self.reply(*answer(self.path.removeprefix("/tools/"), body, self.headers))


if __name__ == "__main__":
    host, port = (
        os.environ.get("MEDIA_TOOLS_HOST", "127.0.0.1"),
        int(os.environ.get("MEDIA_TOOLS_PORT", "8422")),
    )
    print(f"media-tools plugin on http://{host}:{port}", flush=True)
    ThreadingHTTPServer((host, port), Handler).serve_forever()
