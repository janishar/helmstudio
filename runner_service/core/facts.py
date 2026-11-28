"""A video or audio take's facts and frame grid (API 1.104, contracts/README.md "Take facts"; plan 42): what the
Assistant cannot hear or see in one still. Blocking ffmpeg calls on a local file: callers run them in a thread.

- facts: `seconds` (the file's real length), `sound_seconds`, integrated loudness `lufs` and true peak `peak_db`
  (ebur128), `silence_start` / `silence_end` (seconds under SILENCE_DB at each end), `end_lufs` (the loudness of the
  last END seconds) and `abrupt` (the sound stops within ABRUPT_LU of its loudness, no fade or silence), and for a
  video its scene `cuts` (seconds, at most MAX_CUTS). A take with no sound has the sound facts null.
- grid: GRID frames at floor(F * (2k + 1) / (2 * GRID)) in one JPEG of at most SIDE px, in time order left to right,
  each with its time printed at its top left."""

import io
import math
import re
import subprocess
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

from core.media import FFMPEG, MediaError, last_line

GRID, SIDE, MAX_CUTS = 12, 1024, 32
SILENCE_DB, END, ABRUPT_LU, SCENE = -50, 0.5, 10, 0.3
_NUM = r"(-?\d+(?:\.\d+)?|-?inf)"


def _ffmpeg(args: list[str], timeout: float = 600) -> subprocess.CompletedProcess:
    r = subprocess.run([FFMPEG or "ffmpeg", "-hide_banner", "-nostats", "-nostdin", "-protocol_whitelist", "file", *args],
                       capture_output=True, timeout=timeout, check=False)  # fmt: skip
    if r.returncode:
        raise MediaError("ffmpeg", last_line(r.stderr.decode(errors="replace")))
    return r


def _num(text: str) -> float | None:
    v = float(text)
    return None if math.isinf(v) else round(v, 2)


def _loud(path: Path, tail: bool) -> tuple[float | None, float | None]:
    """(integrated LUFS, true peak dBTP) of the first sound, or of its last END seconds."""
    pre = ["-sseof", f"-{END}"] if tail else []
    err = _ffmpeg([*pre, "-i", str(path), "-map", "0:a:0", "-af", "ebur128=peak=true", "-f", "null", "-"])
    text = err.stderr.decode(errors="replace").rsplit("Summary:", 1)[-1]
    i, p = re.search(rf"I:\s*{_NUM} LUFS", text), re.search(rf"Peak:\s*{_NUM} dBFS", text)
    return (_num(i.group(1)) if i else None), (_num(p.group(1)) if p else None)


def facts(path: Path, info: dict) -> dict:
    """The facts of a probed file (core.media.probe's `info`)."""
    seconds = round(info["seconds"], 2)
    out = {"seconds": seconds, "sound_seconds": None, "lufs": None, "peak_db": None, "silence_start": None,
           "silence_end": None, "end_lufs": None, "abrupt": None}  # fmt: skip
    if info["audio"] is not None:
        a = info["audio"]
        out["sound_seconds"] = round(float(a.get("duration") or seconds), 2)
        out["lufs"], out["peak_db"] = _loud(path, False)
        out["end_lufs"] = _loud(path, True)[0] if out["sound_seconds"] > END else out["lufs"]
        err = _ffmpeg(["-i", str(path), "-map", "0:a:0", "-af", f"silencedetect=n={SILENCE_DB}dB:d=0.1", "-f", "null",
                       "-"]).stderr.decode(errors="replace")  # fmt: skip
        starts = [float(x) for x in re.findall(r"silence_start: (-?[\d.]+)", err)]
        ends = [float(x) for x in re.findall(r"silence_end: (-?[\d.]+)", err)]
        length = out["sound_seconds"]
        quiet = [(st, ends[i] if i < len(ends) else length) for i, st in enumerate(starts)]
        head = quiet[0][1] if quiet and quiet[0][0] <= 0.01 else 0.0
        tail = length - quiet[-1][0] if quiet and quiet[-1][1] >= length - 0.05 else 0.0
        out["silence_start"], out["silence_end"] = round(min(head, length), 2), round(max(tail, 0.0), 2)
        loud, end = out["lufs"], out["end_lufs"]
        out["abrupt"] = bool(loud is not None and end is not None and loud > -70 and out["silence_end"] == 0
                             and end >= loud - ABRUPT_LU)  # fmt: skip
    if info["video"] is not None:
        out["cuts"] = cuts(path)
    return out


def cuts(path: Path) -> list[float]:
    """Scene cuts (seconds) of the first video stream, at most MAX_CUTS (scored on a 320 px copy)."""
    err = _ffmpeg(["-i", str(path), "-map", "0:v:0", "-vf", f"scale=320:-2,select='gt(scene\\,{SCENE})',showinfo",
                   "-f", "null", "-"]).stderr.decode(errors="replace")  # fmt: skip
    return [round(float(t), 2) for t in re.findall(r"pts_time:([\d.]+)", err)][:MAX_CUTS]


def _layout(w: int, h: int, n: int) -> tuple[int, int, int, int]:
    """(columns, rows, tile width, tile height): the largest tiles that fit n frames in SIDE x SIDE."""
    best = (0.0, 1, n)
    for cols in range(1, n + 1):
        rows = math.ceil(n / cols)
        s = min(SIDE / (cols * w), SIDE / (rows * h), 1.0)
        best = max(best, (s, cols, rows))
    s, cols, rows = best
    return cols, rows, max(1, int(w * s)), max(1, int(h * s))


def stamp(t: float) -> str:
    m, s = divmod(t, 60)
    return f"{int(m)}:{s:04.1f}"


def grid(path: Path, info: dict) -> tuple[bytes, int, int, list[float]]:
    """(JPEG, width, height, each frame's seconds) of a probed video: GRID frames (fewer for a shorter clip)."""
    v, frames = info["video"], info["frames"]
    if not v or frames < 1:
        raise MediaError("input", "the video has no frames")
    num, _, den = v.get("r_frame_rate", "0/1").partition("/")
    fps = float(num) / float(den or 1) if float(num or 0) > 0 else frames / max(info["seconds"], 1e-6)
    n = min(GRID, frames)
    picks = sorted({frames * (2 * k + 1) // (2 * n) for k in range(n)})
    cols, rows, tw, th = _layout(int(v["width"]), int(v["height"]), len(picks))
    select = "+".join(f"eq(n\\,{f})" for f in picks)
    raw = _ffmpeg(["-i", str(path), "-map", "0:v:0", "-vf", f"select='{select}',scale={tw}:{th}", "-fps_mode",
                   "passthrough", "-pix_fmt", "rgb24", "-f", "rawvideo", "pipe:1"]).stdout  # fmt: skip
    size = tw * th * 3
    if len(raw) < size * len(picks):
        raise MediaError("input", "the video has fewer frames than it says")
    sheet = Image.new("RGB", (cols * tw, rows * th), (0, 0, 0))
    draw, font = ImageDraw.Draw(sheet), ImageFont.load_default(size=max(10, th // 9))
    times = [round(f / fps, 2) for f in picks]
    for i, t in enumerate(times):
        x, y = (i % cols) * tw, (i // cols) * th
        sheet.paste(Image.frombytes("RGB", (tw, th), raw[i * size : (i + 1) * size]), (x, y))
        label = stamp(t)
        box = draw.textbbox((x + 4, y + 3), label, font=font)
        draw.rectangle((box[0] - 3, box[1] - 2, box[2] + 3, box[3] + 2), fill=(0, 0, 0))
        draw.text((x + 4, y + 3), label, fill=(255, 255, 255), font=font)
    buf = io.BytesIO()
    sheet.save(buf, "JPEG", quality=85)
    return buf.getvalue(), sheet.width, sheet.height, times
