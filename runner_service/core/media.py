"""ffmpeg on this host for media jobs (contracts/README.md, Timeline: media jobs) and video outputs: what it can do
(the hosts key's `media`), ffprobe's stream facts (defs videoStream), an exact frame as a PNG, the `{venc}`
arguments, and the check of studio's argument list (placeholders substituted, everything else refused). Calls are
blocking: callers run them in a thread."""

import hashlib
import json
import re
import shutil
import subprocess
import sys
from pathlib import Path

FFMPEG, FFPROBE = shutil.which("ffmpeg"), shutil.which("ffprobe")
H264 = ("h264_videotoolbox", "libx264")  # preferred first: the Mac's media engine, then software
NEEDS = ("xfade", "apad", "amix")  # filters the export graph uses


class MediaError(Exception):
    """A job failure of class `cls` (input, invalid, ffmpeg, verify, internal); the text is one line without paths."""

    def __init__(self, cls: str, detail: str) -> None:
        super().__init__(detail[:500])
        self.cls = cls


def _out(*args: str) -> str:
    return subprocess.run(args, capture_output=True, text=True, check=False, timeout=30).stdout


def detect() -> dict | None:
    """{ffmpeg: version, encoders: [...]} when ffmpeg >= 6 has xfade, apad, amix, aac and an H.264 encoder, else
    None (this env runs no media jobs). h264_videotoolbox only on macOS."""
    if not (FFMPEG and FFPROBE):
        return None
    m = re.search(r"version\s+n?(\d+(?:\.\d+)*)", _out(FFMPEG, "-hide_banner", "-version"))
    if not m or int(m.group(1).split(".")[0]) < 6:
        return None
    names = lambda text: {ln.split()[1] for ln in text.splitlines() if len(ln.split()) > 2}
    filters, encoders = (
        names(_out(FFMPEG, "-hide_banner", "-filters")),
        names(_out(FFMPEG, "-hide_banner", "-encoders")),
    )
    h264 = [
        e for e in H264 if e in encoders and (e != "h264_videotoolbox" or sys.platform == "darwin")
    ]
    if not h264 or "aac" not in encoders or not all(f in filters for f in NEEDS):
        return None
    return {"ffmpeg": m.group(1)[:40], "encoders": h264}


def venc(encoder: str, bitrate: int) -> list[str]:
    """`{venc}`: the video encoder arguments (README, Timeline: media jobs)."""
    if encoder == "h264_videotoolbox":
        return ["-c:v", encoder, "-profile:v", "high", "-allow_sw", "1", "-b:v", str(bitrate)]
    return ["-c:v", "libx264", "-preset", "medium", "-profile:v", "high", "-b:v", str(bitrate),
            "-maxrate", str(bitrate), "-bufsize", str(2 * bitrate)]  # fmt: skip


def _null(v):
    return None if v in (None, "", "unknown", "0:1", "N/A") else v


def probe(path: Path) -> dict:
    """ffprobe of a file: {container, video (first video stream or None), audio (first audio or None), stream (the
    video's defs videoStream facts or None), frames (video packets counted)}. MediaError input when unreadable."""
    r = subprocess.run([FFPROBE, "-v", "error", "-protocol_whitelist", "file", "-show_data_hash", "sha256", "-count_packets", "-show_format",
                        "-show_streams", "-of", "json", str(path)],
                       capture_output=True, text=True, check=False, timeout=300)  # fmt: skip
    try:
        d = json.loads(r.stdout) if r.returncode == 0 else None
    except ValueError:
        d = None
    if not d or "format" not in d:
        raise MediaError("input", "ffprobe cannot read the file")
    fmt = d["format"]
    first = fmt.get("format_name", "").split(",")[0]
    if first == "mov":  # one demuxer for both: the brand tells them apart
        first = "mov" if fmt.get("tags", {}).get("major_brand", "").strip() == "qt" else "mp4"
    pick = lambda t: next((s for s in d.get("streams", []) if s.get("codec_type") == t), None)
    v, a = pick("video"), pick("audio")
    stream = None
    if v is not None:
        h = v.get("extradata_hash", "")
        stream = {
            "container": first,
            "codec": v.get("codec_name", ""),
            "profile": v.get("profile", ""),
            "level": int(v.get("level", 0)),
            "pix_fmt": v.get("pix_fmt", ""),
            "width": v["width"],
            "height": v["height"],
            "sar": _null(v.get("sample_aspect_ratio")),
            "field_order": _null(v.get("field_order")),
            "frame_rate": v.get("r_frame_rate", "0/0"),
            "time_base": v.get("time_base", "0/0"),
            "color": {
                k: _null(v.get(f"color_{k}")) for k in ("range", "space", "primaries", "transfer")
            },
            "extradata_sha256": h.split(":")[1]
            if h.startswith("SHA256:")
            else hashlib.sha256(b"").hexdigest(),
        }
    frames = int(v.get("nb_read_packets", 0)) if v else 0
    return {"container": first, "video": v, "audio": a, "stream": stream, "frames": frames}


IMAGE_CODECS = {"png", "mjpeg", "webp", "bmp", "tiff"}


def check_kind(info: dict, kind: str) -> None:
    """Refuse (input) a file that is not the media type the command says: a video = mp4/mov with a video stream
    that is not an image codec; an image = one still picture."""
    v = info["video"]
    if kind == "video":
        ok = (
            v is not None
            and info["container"] in ("mp4", "mov")
            and v.get("codec_name") not in IMAGE_CODECS
        )
    else:
        ok = v is not None and v.get("codec_name") in IMAGE_CODECS and info["frames"] <= 1
    if not ok:
        raise MediaError("input", f"an input is not a {kind}")


def frame_png(video: Path, index: int, dest: Path) -> None:
    """Frame `index` (from 0, in decode-output order, no frame dropped or duplicated) of `video` as an RGB PNG at
    its size. MediaError input when the video has no such frame."""
    # ponytail: select decodes up to the frame; seek to the keyframe before it if long exports make this slow
    r = subprocess.run([FFMPEG, "-v", "error", "-nostdin", "-y", "-protocol_whitelist", "file", "-i", str(video), "-map", "0:v:0",
                        "-vf", f"select=eq(n\\,{index})", "-fps_mode", "passthrough", "-frames:v", "1",
                        "-pix_fmt", "rgb24", "-f", "image2", "-c:v", "png", str(dest)],
                       capture_output=True, text=True, check=False, timeout=600)  # fmt: skip
    if r.returncode:
        raise MediaError("ffmpeg", last_line(r.stderr))
    if not dest.is_file() or dest.stat().st_size == 0:
        raise MediaError("input", f"the video has no frame {index}")


def last_line(stderr: str) -> str:
    """ffmpeg's last error line without local paths (never a path in a failure)."""
    lines = [ln.strip() for ln in stderr.splitlines() if ln.strip()]
    line = lines[-1] if lines else "ffmpeg failed"
    return re.sub(r"(/[^\s:'\"]+)+", "<file>", line)[:500]


# ffmpeg options a studio argument list may use: name (before any :stream specifier) → takes a value
OPTIONS = {
    "-hide_banner": False, "-nostdin": False, "-y": False, "-nostats": False, "-shortest": False,
    "-an": False, "-vn": False, "-sn": False, "-dn": False, "-loglevel": True, "-v": True,
    "-progress": True, "-f": True, "-safe": True, "-i": True, "-filter_complex": True, "-vf": True,
    "-af": True, "-filter": True, "-map": True, "-c": True, "-codec": True, "-vcodec": True, "-acodec": True,
    "-b": True, "-ar": True, "-ac": True, "-map_metadata": True, "-map_chapters": True, "-movflags": True,
    "-fflags": True, "-flags": True, "-loop": True, "-framerate": True, "-t": True, "-ss": True, "-to": True,
    "-pix_fmt": True, "-r": True, "-fps_mode": True, "-vsync": True, "-frames": True, "-profile": True,
    "-level": True, "-tag": True, "-threads": True, "-max_muxing_queue_size": True, "-avoid_negative_ts": True,
    "-video_track_timescale": True, "-color_range": True, "-colorspace": True, "-color_primaries": True,
    "-color_trc": True, "-g": True, "-bf": True, "-preset": True, "-crf": True, "-maxrate": True,
    "-bufsize": True, "-allow_sw": True, "-itsoffset": True, "-display_rotation": True,
}  # fmt: skip
CODECS = {"copy", "aac", "rawvideo", "pcm_s16le"}  # outside {venc}
FORMATS = {"concat", "mp4", "mov", "image2"}  # never lavfi (its sources can open files)
# filters (and filter options) that open a file, a device or a URL
DENY = re.compile(r"\b(a?movie|subtitles|ass|lut1d|lut3d|haldclutsrc|a?sendcmd|a?zmq|ladspa|lv2|frei0r|drawtext|"
                  r"lensfun|sofalizer|coreimagesrc|openclsrc|vidstab\w*|file|filename|textfile|fontfile)\b")  # fmt: skip
SLOT = re.compile(r"\{in:(\d+)\}")


OUTPUT_LIMIT = (
    32 << 30
)  # bytes: the largest output ffmpeg may write (studio blocks an export estimated over 16 GiB)


def build(
    args: list[str], inputs: list[Path], *, out: Path, venc_args: list[str], listfile: Path | None
) -> list[str]:
    """Studio's argument list with `{in:n}`, `{list}`, `{out}` and `{venc}` substituted (whole arguments only),
    after checking it. MediaError invalid for an unknown option, an `-i` not followed by a placeholder, a codec
    other than CODECS outside {venc}, a format other than FORMATS, -progress other than pipe:1, a filter that opens
    a file, or any output other than one `{out}` at the end."""
    result, outs, i = [], 0, 0
    while i < len(args):
        a = args[i]
        if a == "{venc}":
            result += venc_args
        elif a == "{out}":
            outs += 1
            result += [
                "-fs",
                str(OUTPUT_LIMIT),
                str(out),
            ]  # ffmpeg stops writing past it (verify then fails)
        elif a.startswith("-"):
            name = a.split(":")[0]
            if name not in OPTIONS:
                raise MediaError("invalid", f"option {a[:40]} is not allowed")
            if OPTIONS[name]:
                if i + 1 >= len(args):
                    raise MediaError("invalid", f"option {a[:40]} has no value")
                value = args[i + 1]
                if name == "-i":
                    m = SLOT.fullmatch(value)
                    if value == "{list}" and listfile is not None:
                        value = str(listfile)
                    elif m and int(m.group(1)) < len(inputs):
                        value = str(inputs[int(m.group(1))])
                    else:
                        raise MediaError("invalid", "-i must name {in:<n>} of an input or {list}")
                elif name in ("-c", "-codec", "-vcodec", "-acodec") and value not in CODECS:
                    raise MediaError("invalid", f"codec {value[:40]} is not allowed")
                elif name == "-f" and value not in FORMATS:
                    raise MediaError("invalid", f"format {value[:40]} is not allowed")
                elif name == "-progress" and value != "pipe:1":
                    raise MediaError("invalid", "-progress must be pipe:1")
                elif name in ("-filter_complex", "-vf", "-af", "-filter") and DENY.search(value):
                    raise MediaError("invalid", "a filter that opens a file is not allowed")
                elif value.startswith("{") and value.endswith("}"):
                    raise MediaError("invalid", f"placeholder {value[:40]} is not allowed there")
                if name == "-i":  # every input reads local files only (a playlist cannot reach out)
                    result += ["-protocol_whitelist", "file"]
                result += [a, value]
                i += 1
            else:
                result.append(a)
        else:  # a positional argument is an output file
            raise MediaError("invalid", "the only output may be {out}")
        i += 1
    if outs != 1 or args[-1] != "{out}":
        raise MediaError("invalid", "the arguments must end with the one output {out}")
    return result


def concat_list(paths: list[Path], durations: list[str | None] | None = None) -> str:
    """The concat demuxer's list: `file '<path>'` lines, `'` written as `'\\''`, each with its picture's `duration`
    when known: without it the demuxer starts the next file at this one's longest stream (its sound, AAC padding
    included), leaving a gap in the picture's timestamps (an irregular frame rate)."""
    return "".join("file '" + str(p).replace("'", "'\\''") + "'\n" + (f"duration {d}\n" if d else "")
                   for p, d in zip(paths, durations or [None] * len(paths)))  # fmt: skip
