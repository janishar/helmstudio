"""Media jobs (contracts/README.md, Timeline: media jobs) on small clips ffmpeg makes here: a stream-copy export, its
fallback to conform when a probe differs, a conform export (scale, pad, fps change, an image, a dissolve, gain, an
extra audio track), cancel mid-run, hostile names and arguments, exact frames, and the fake's stream facts. No NATS,
no S3, no GPU. Skipped without ffmpeg."""

import asyncio
import copy
import io
import json
import subprocess
import tempfile
import types
from pathlib import Path

import pytest
from conftest import CONTRACTS, FakeJS, FakeKV, FakeMsg, FakeStore
from PIL import Image

from core import media
from core.media import MediaError
from messaging import media as media_jobs
from messaging import schemas
from packs.echo import mp4

pytestmark = pytest.mark.skipif(media.detect() is None, reason="needs ffmpeg >= 6 with libx264")

BASE = json.loads((CONTRACTS / "fixtures/messages/cmd.media.job.valid.json").read_text())
OWNER = BASE["owner_id"]
U = f"u/{OWNER}/"
PREFIX = f"{U}exports/{BASE['job_id']}/"
HEAD = ["-hide_banner", "-nostdin", "-loglevel", "error", "-y", "-progress", "pipe:1", "-nostats"]
TAIL = [
    "-c:a",
    "aac",
    "-b:a",
    "192k",
    "-ar",
    "48000",
    "-ac",
    "2",
    "-movflags",
    "+faststart",
    "-f",
    "mp4",
    "{out}",
]
TARGET = {"width": 256, "height": 128, "fps": 24, "rate": "24/1", "sample_rate": 48000}
CONFORM = "scale=256:128:force_original_aspect_ratio=decrease,pad=256:128:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=24/1,format=yuv420p,settb=AVTB"


def ff(*args: str) -> None:
    subprocess.run(["ffmpeg", "-v", "error", "-y", *args], check=True)


@pytest.fixture(scope="module")
def clips(tmp_path_factory) -> dict[str, bytes]:
    """a: 1 s 256x128 at 24 with sound (twice: copyable; x264's header data differs by content); c: 1 s 320x240 at 30 with sound;
    d: a PNG; t: 25 distinct frames (testsrc2) without sound."""
    d = tmp_path_factory.mktemp("clips")
    x264 = [
        "-c:v",
        "libx264",
        "-preset",
        "ultrafast",
        "-pix_fmt",
        "yuv420p",
        "-c:a",
        "aac",
        "-ar",
        "48000",
    ]
    for name, src, freq in (("a", "testsrc2=s=256x128:r=24:d=1", 440), ("c", "testsrc=s=320x240:r=30:d=1", 880)):
        ff("-f", "lavfi", "-i", src, "-f", "lavfi", "-i", f"sine=f={freq}:d=1:r=48000", *x264, "-shortest",
           str(d / f"{name}.mp4"))  # fmt: skip
    ff("-f", "lavfi", "-i", "color=c=orange:s=200x100", "-frames:v", "1", str(d / "d.png"))
    ff("-f", "lavfi", "-i", "testsrc2=s=128x64:r=24:d=1.0417", "-frames:v", "25", "-c:v", "libx264",
       "-pix_fmt", "yuv420p", str(d / "t.mp4"))  # fmt: skip
    return {n: (d / f"{n}.{'png' if n == 'd' else 'mp4'}").read_bytes() for n in "acdt"}


def pcm16(path: Path) -> bytes:
    """A file's sound as 16-bit stereo samples."""
    return subprocess.run(["ffmpeg", "-v", "error", "-i", str(path), "-f", "s16le", "-ac", "2", "-"],
                          capture_output=True, check=True).stdout  # fmt: skip


def store_of(clips, **named) -> FakeStore:
    s = FakeStore()
    for key, clip in named.items():
        s.objects[f"{U}{key}"] = (clips[clip], "video/mp4")
    return s


def facts(data: bytes) -> dict:
    with tempfile.NamedTemporaryFile(suffix=".mp4") as f:
        f.write(data)
        f.flush()
        return media.probe(f.name)["stream"]


def command(inputs: list[tuple[str, str]], export: dict | None = None, frame: dict | None = None) -> dict:
    cmd = copy.deepcopy(BASE)
    data = {
        "kind": "frame" if frame else "export",
        "inputs": [{"key": f"{U}{k}", "kind": kind} for k, kind in inputs],
    }
    if frame:
        data |= {"output_prefix": f"{U}frames/{BASE['job_id']}/", "frame": frame}
    else:
        data |= {"output_prefix": PREFIX, "export": {"timeline_id": BASE["data"]["export"]["timeline_id"],
                 "revision": 1, "target": TARGET, "video_bitrate": 1000000} | export}  # fmt: skip
    cmd["data"] = data
    assert schemas.message_errors(cmd) == []
    return cmd


async def run(cmd, store, encoders=("libx264",), kv=None, during=None):
    js, kv = FakeJS(), kv or FakeKV()
    jobs = media_jobs.MediaJobs(js=js, cancels_kv=kv, store=store, encoders=list(encoders))
    msg = FakeMsg(cmd, js, subject="cmd.media.fake.fake")
    task = asyncio.create_task(jobs.handle(msg))
    if during:
        await during(js, kv)
    await task
    events = js.events()
    for e in events:
        assert schemas.message_errors(e) == [], e
    assert msg.log[-1][0] == "ack"
    return events


def copy_export(clips) -> dict:
    sound = ("[1:a:0]atrim=end=1,asetpts=N/SR/TB[a0];[2:a:0]atrim=end=1,asetpts=N/SR/TB[a1];"
             "[a0][a1]concat=n=2:v=0:a=1[aout]")  # fmt: skip
    conform = (f"[0:v:0]trim=end=1,setpts=PTS-STARTPTS,{CONFORM}[v0];[1:v:0]trim=end=1,setpts=PTS-STARTPTS,"
               f"{CONFORM}[v1];[v0][v1]concat=n=2:v=1:a=0[vout];[0:a:0]atrim=end=1,asetpts=N/SR/TB[a0];"
               "[1:a:0]atrim=end=1,asetpts=N/SR/TB[a1];[a0][a1]concat=n=2:v=0:a=1[aout]")  # fmt: skip
    return {"mode": "copy", "frames": 48, "samples": 96000, "concat": [0, 1],
            "args": [*HEAD, "-f", "concat", "-safe", "0", "-i", "{list}", "-i", "{in:0}", "-i", "{in:1}",
                     "-filter_complex", sound, "-map", "0:v:0", "-c:v", "copy", "-map", "[aout]", *TAIL],
            "expect": [{"input": 0, "stream": facts(clips["a"])},
                       {"input": 1, "stream": facts(clips["a"])}],
            "conform_args": [*HEAD, "-i", "{in:0}", "-i", "{in:1}", "-filter_complex", conform,
                             "-map", "[vout]", "-map", "[aout]", "{venc}", *TAIL]}  # fmt: skip


async def test_stream_copy_concatenates_the_pictures_and_reencodes_the_sound(clips):
    hostile = 'runs/x/1/it\'s $(touch pwned); rm -rf ~ `id` "q".mp4'
    store = store_of(clips, **{hostile: "a", "runs/y/1/0.mp4": "a"})
    cmd = command([(hostile, "video"), ("runs/y/1/0.mp4", "video")], copy_export(clips))
    events = await run(cmd, store)
    assert [e["data"]["status"] for e in events if e["type"] == "evt.media.status"] == [
        "running",
        "succeeded",
    ], events[-1]["data"]
    end = events[-1]["data"]
    assert events[0]["data"]["encoder"] == "copy" and end["encoder"] == "copy"
    r = end["result"]
    assert r["mode"] == "copy" and "reason" not in r and r["key"] == PREFIX + "export.mp4"
    assert (r["width"], r["height"], r["video"]["frames"], r["video"]["has_audio"]) == (
        256,
        128,
        48,
        True,
    )
    assert r["video"]["stream"]["extradata_sha256"] == facts(clips["a"])["extradata_sha256"]  # copied, not encoded
    assert set(store.objects) >= {
        PREFIX + "export.mp4",
        PREFIX + "export.poster.png",
        PREFIX + "export.last.png",
    }
    assert not Path("pwned").exists()


async def test_a_copy_plan_whose_probe_differs_runs_the_conform_arguments(clips):
    store = store_of(clips, **{"runs/a/1/0.mp4": "a", "runs/c/1/0.mp4": "c"})
    cmd = command([("runs/a/1/0.mp4", "video"), ("runs/c/1/0.mp4", "video")], copy_export(clips))
    end = (await run(cmd, store))[-1]["data"]
    assert end["status"] == "succeeded" and end["encoder"] == "libx264"
    assert end["result"]["mode"] == "conform" and end["result"]["reason"].startswith("clip input 1: ")
    assert end["result"]["video"]["frames"] == 48


async def test_conform_with_an_image_a_dissolve_gain_and_an_extra_audio_track(clips):
    store = store_of(clips, **{"runs/a/1/0.mp4": "a", "runs/c/1/0.mp4": "c"})
    store.objects[f"{U}uploads/d.png"] = (clips["d"], "image/png")
    graph = (
        f"[0:v:0]trim=end=1,setpts=PTS-STARTPTS,{CONFORM}[v0];[1:v:0]trim=end=1,setpts=PTS-STARTPTS,{CONFORM}[v1];"
        f"[2:v:0]{CONFORM},trim=duration=0.5[v2];[v0][v1]xfade=transition=fade:duration=0.25:offset=0.75[x];"
        "[x][v2]concat=n=2:v=1:a=0[vout];"
        "[0:a:0]atrim=end=1,asetpts=N/SR/TB,volume=-6dB[a0];[1:a:0]atrim=end=1,asetpts=N/SR/TB,adelay=750:all=1[a1];"
        "[0:a:0]atrim=end=0.5,asetpts=N/SR/TB,volume=3dB,adelay=1250:all=1[a2];"
        "[a0][a1][a2]amix=inputs=3:normalize=0:duration=longest,apad=whole_dur=2.25,atrim=duration=2.25[aout]"
    )
    ex = {"mode": "conform", "frames": 54, "samples": 108000,
          "args": [*HEAD, "-fflags", "+bitexact", "-i", "{in:0}", "-i", "{in:1}", "-loop", "1", "-framerate", "24",
                   "-t", "0.5", "-i", "{in:2}", "-filter_complex", graph, "-map", "[vout]", "-map", "[aout]",
                   "-map_metadata", "-1", "{venc}", "-pix_fmt", "yuv420p", *TAIL]}  # fmt: skip
    cmd = command([("runs/a/1/0.mp4", "video"), ("runs/c/1/0.mp4", "video"), ("uploads/d.png", "image")], ex)
    end = (await run(cmd, store))[-1]["data"]
    assert end["status"] == "succeeded", end
    v = end["result"]["video"]
    assert (v["frames"], v["fps"], v["has_audio"], v["stream"]["frame_rate"]) == (
        54,
        24,
        True,
        "24/1",
    )
    assert v["poster"]["key"].endswith("export.poster.png") and v["last"]["width"] == 256
    last = Image.open(io.BytesIO(store.objects[v["last"]["key"]][0]))
    assert last.convert("RGB").getpixel((128, 64))[2] < 80  # the image (orange) ends it


@pytest.mark.parametrize("codec", ["pcm_s16le", "aac"])
async def test_an_audio_take_on_an_audio_track_is_mixed_resampled_to_stereo_at_its_start(clips, tmp_path, codec):
    """1.32: an input of kind audio (a mono 24 kHz WAV, sine at 1/8 scale; 1.38 also an m4a, what the web uploads)
    mixed as studio's buildArgs mixes a sound: in 0.1..0.4 laid at 0.5 s, resampled to 48 kHz stereo, -6 dB; silence
    before it."""
    name = "w.wav" if codec == "pcm_s16le" else "w.m4a"
    ff(
        "-f",
        "lavfi",
        "-i",
        "sine=f=1000:d=1:r=24000",
        "-ac",
        "1",
        "-c:a",
        codec,
        str(tmp_path / name),
    )
    store = store_of(clips, **{"runs/t/1/0.mp4": "t"})
    store.objects[f"{U}runs/w/1/0.wav"] = ((tmp_path / name).read_bytes(), "audio/wav")
    silence = "anullsrc=r=48000:cl=stereo"
    graph = (f"[0:v:0]trim=start=0:end=1,setpts=PTS-STARTPTS,{CONFORM}[vout];"
             f"{silence},atrim=duration=1,asetpts=N/SR/TB[abase];"
             "[1:a:0]atrim=start=0.1:end=0.4,asetpts=N/SR/TB,aresample=48000:async=1:first_pts=0,"
             "aformat=sample_fmts=fltp:channel_layouts=stereo,apad,atrim=duration=0.3,volume=-6dB,asetpts=N/SR/TB[a0b];"
             f"{silence},atrim=duration=0.5,asetpts=N/SR/TB[a0p];[a0p][a0b]concat=n=2:v=0:a=1[a0];"
             "[abase][a0]amix=inputs=2:normalize=0:duration=longest,atrim=duration=1,asetpts=N/SR/TB[aout]")  # fmt: skip
    ex = {"mode": "conform", "frames": 24, "samples": 48000,
          "args": [*HEAD, "-i", "{in:0}", "-i", "{in:1}", "-filter_complex", graph, "-map", "[vout]", "-map", "[aout]",
                   "{venc}", *TAIL]}  # fmt: skip
    cmd = command([("runs/t/1/0.mp4", "video"), ("runs/w/1/0.wav", "audio")], ex)
    end = (await run(cmd, store))[-1]["data"]
    assert end["status"] == "succeeded", end
    assert end["result"]["video"]["has_audio"]
    (tmp_path / "out.mp4").write_bytes(store.objects[PREFIX + "export.mp4"][0])
    samples = memoryview(pcm16(tmp_path / "out.mp4")).cast("h")
    level = lambda a, b: max(abs(x) for x in samples[int(a * 48000) * 2 : int(b * 48000) * 2])
    assert level(0.05, 0.45) < 300 and level(0.55, 0.85) > 1000 and level(0.95, 1.0) < 300
    assert media.probe(tmp_path / "out.mp4")["audio"]["channels"] == 2


async def test_an_audio_input_that_is_not_a_wav_fails_input(clips):
    store = store_of(clips, **{"runs/t/1/0.mp4": "t", "runs/w/1/0.wav": "a"})
    ex = {"mode": "conform", "frames": 24, "samples": 48000,
          "args": [*HEAD, "-i", "{in:0}", "-i", "{in:1}", "-map", "0:v:0", "{venc}", *TAIL]}  # fmt: skip
    end = (await run(command([("runs/t/1/0.mp4", "video"), ("runs/w/1/0.wav", "audio")], ex), store))[-1]["data"]
    assert end["status"] == "failed" and end["failure"]["detail"] == "an input is not a audio", end


async def test_videotoolbox_encodes_when_the_host_has_it(clips):
    if "h264_videotoolbox" not in (media.detect() or {}).get("encoders", []):
        pytest.skip("no VideoToolbox here")
    store = store_of(clips, **{"runs/a/1/0.mp4": "a", "runs/c/1/0.mp4": "c"})
    cmd = command(
        [("runs/a/1/0.mp4", "video"), ("runs/c/1/0.mp4", "video")],
        copy_export(clips) | {"mode": "conform"},
    )
    cmd["data"]["export"]["args"] = cmd["data"]["export"].pop("conform_args")
    for k in ("concat", "expect"):
        del cmd["data"]["export"][k]
    end = (await run(cmd, store, encoders=("h264_videotoolbox", "libx264")))[-1]["data"]
    assert end["status"] == "succeeded" and end["encoder"] == "h264_videotoolbox"


async def test_cancel_mid_run_stops_ffmpeg_and_removes_the_temp_dir(clips, tmp_path, monkeypatch):
    monkeypatch.setattr(tempfile, "tempdir", str(tmp_path))
    monkeypatch.setattr(media_jobs, "PROGRESS", 0.05)
    store = FakeStore()
    store.objects[f"{U}uploads/d.png"] = (clips["d"], "image/png")
    ex = {"mode": "conform", "frames": 86400, "samples": 172800000,
          "args": [*HEAD, "-loop", "1", "-framerate", "24", "-t", "3600", "-i", "{in:0}",
                   "-vf", CONFORM, "-an", "{venc}", "-f", "mp4", "{out}"]}  # fmt: skip
    cmd = command([("uploads/d.png", "image")], ex)

    async def cancel_after_progress(js, kv):
        while not any(e["type"] == "evt.media.progress" for e in js.events()):
            await asyncio.sleep(0.05)
        await kv.put(cmd["job_id"], b"now")

    events = await asyncio.wait_for(run(cmd, store, during=cancel_after_progress), 30)
    assert events[-1]["data"]["status"] == "cancelled"
    assert events[-2]["type"] == "evt.media.progress" and events[-2]["data"]["total"] == 86400
    assert list(tmp_path.iterdir()) == [] and store.objects.keys() == {f"{U}uploads/d.png"}
    ps = await asyncio.create_subprocess_exec("pgrep", "-f", "framerate 24 -t 3600")
    assert await ps.wait() == 1  # no ffmpeg left


@pytest.mark.parametrize(
    "args, why",
    [
        (["-i", "/etc/passwd", "{out}"], "-i must name"),
        (["-f", "lavfi", "-i", "{in:0}", "{out}"], "format lavfi"),
        (["-i", "{in:0}", "-filter_complex", "movie=/etc/hosts[v]", "{out}"], "opens a file"),
        (["-i", "{in:0}", "-vf", "drawtext=textfile=/etc/hosts", "{out}"], "opens a file"),
        (["-i", "{in:0}", "-c:v", "libx264", "{out}"], "codec libx264"),
        (["-i", "{in:0}", "/tmp/other.mp4", "{out}"], "only output"),
        (["-i", "{in:0}", "{out}", "{out}"], "one output"),
        (["-i", "{in:7}", "{out}"], "-i must name"),
        (["-i", "{in:0}", "-progress", "/tmp/p.txt", "{out}"], "pipe:1"),
        (["-i", "{in:0}", "-filter_complex_script", "x", "{out}"], "not allowed"),
        (["-i", "{in:0}", "-map", "{list}", "{out}"], "placeholder"),
    ],
)
def test_arguments_outside_the_allowlist_are_refused(args, why, tmp_path):
    with pytest.raises(MediaError, match=why) as e:
        media.build(args, [tmp_path / "in0.mp4"], out=tmp_path / "o.mp4", venc_args=[], listfile=None)
    assert e.value.cls == "invalid"


def test_placeholders_become_runner_paths_and_concat_quotes(tmp_path):
    p = tmp_path / "it's.mp4"
    got = media.build(["-i", "{in:0}", "{venc}", "{out}"], [p], out=tmp_path / "o.mp4", venc_args=["-c:v", "x"],
                      listfile=None)  # fmt: skip
    assert got == [
        "-protocol_whitelist",
        "file",
        "-i",
        str(p),
        "-c:v",
        "x",
        "-fs",
        str(media.OUTPUT_LIMIT),
        str(tmp_path / "o.mp4"),
    ]
    assert media.concat_list([p]) == "file '" + str(tmp_path) + "/it'\\''s.mp4'\n"
    assert media.concat_list([p, p], ["2.041667", None]) == (
        f"file '{tmp_path}/it'\\''s.mp4'\nduration 2.041667\nfile '{tmp_path}/it'\\''s.mp4'\n"
    )


async def test_stream_copy_of_clips_whose_sound_runs_past_the_picture_keeps_the_rate():
    """The echo fake's MP4s (49 frames at 24, sound 2.048 s): each file's picture duration in the concat list, else
    the second picture starts at 2.048 s and the copy's rate is irregular (287/12; verify fails)."""
    from PIL import Image

    e = mp4.mp4(Image.new("RGB", (256, 128), (200, 50, 50)), 49, 24)
    clips = {"e": e}
    store = store_of(clips, **{"runs/e/1/0.mp4": "e", "runs/f/1/0.mp4": "e"})
    sound = "[1:a:0]atrim=end=2.041667,asetpts=N/SR/TB[a0];[2:a:0]atrim=end=2.041667,asetpts=N/SR/TB[a1];[a0][a1]concat=n=2:v=0:a=1[aout]"
    export = {"mode": "copy", "frames": 98, "samples": 196000, "concat": [0, 1],
              "args": [*HEAD, "-f", "concat", "-safe", "0", "-i", "{list}", "-i", "{in:0}", "-i", "{in:1}",
                       "-filter_complex", sound, "-map", "0:v:0", "-c:v", "copy", "-map", "[aout]", *TAIL],
              "expect": [{"input": 0, "stream": facts(e)}, {"input": 1, "stream": facts(e)}],
              "conform_args": [*HEAD, "-i", "{in:0}", "{venc}", *TAIL]}  # fmt: skip
    end = (await run(command([("runs/e/1/0.mp4", "video"), ("runs/f/1/0.mp4", "video")], export), store))[-1]
    r = end["data"]["result"]
    assert end["data"]["status"] == "succeeded" and r["mode"] == "copy", end["data"]
    assert (r["video"]["stream"]["frame_rate"], r["video"]["frames"]) == ("24/1", 98)


async def test_an_export_of_empty_slots_only_reads_no_input():
    black = "color=c=black:s=256x128:r=24/1:d=1,format=yuv420p[vout];anullsrc=r=48000:cl=stereo,atrim=duration=1[aout]"
    export = {"mode": "conform", "frames": 24, "samples": 48000,
              "args": [*HEAD, "-filter_complex", black, "-map", "[vout]", "{venc}", "-r", "24/1",
                       "-map", "[aout]", *TAIL]}  # fmt: skip
    end = (await run(command([], export), FakeStore()))[-1]["data"]
    assert end["status"] == "succeeded" and end["result"]["video"]["frames"] == 24, end


async def test_refused_arguments_and_wrong_kinds_fail_the_job(clips):
    store = store_of(clips, **{"runs/a/1/0.mp4": "a"})
    store.objects[f"{U}uploads/d.png"] = (clips["d"], "image/png")
    bad = {
        "mode": "conform",
        "frames": 24,
        "samples": 48000,
        "args": ["-i", "/etc/passwd", "{out}"],
    }
    end = (await run(command([("runs/a/1/0.mp4", "video")], bad), store))[-1]["data"]
    assert end["failure"]["class"] == "invalid"
    ok = {"mode": "conform", "frames": 24, "samples": 48000, "args": ["-i", "{in:0}", "{out}"]}
    end = (await run(command([("uploads/d.png", "video")], ok), store))[-1]["data"]
    assert end["failure"] == {"class": "input", "detail": "an input is not a video"}
    end = (await run(command([("runs/missing.mp4", "video")], ok), store))[-1]["data"]
    assert end["failure"] == {"class": "input", "detail": "input 0 is missing"}


def decoded(data: bytes, w: int, h: int) -> list[bytes]:
    raw = subprocess.run(["ffmpeg", "-v", "error", "-i", "pipe:0", "-f", "rawvideo", "-pix_fmt", "rgb24", "pipe:1"],
                         input=data, capture_output=True, check=True).stdout  # fmt: skip
    n = w * h * 3
    return [raw[i : i + n] for i in range(0, len(raw), n)]


@pytest.mark.parametrize("at, index", [("first", 0), ("last", 24)])
async def test_a_frame_job_writes_exactly_that_frame(clips, at, index):
    store = store_of(clips, **{"runs/t/1/0.mp4": "t"})
    events = await run(command([("runs/t/1/0.mp4", "video")], frame={"at": at, "index": index}), store)
    assert not any(e["type"] == "evt.media.progress" for e in events)
    r = events[-1]["data"]["result"]
    assert (r["width"], r["height"]) == (128, 64) and r["key"].endswith("/frame.png")
    png = Image.open(io.BytesIO(store.objects[r["key"]][0]))
    frames = decoded(clips["t"], 128, 64)
    assert len(frames) == 25 and png.mode == "RGB"
    assert png.tobytes() == frames[index] and frames[index] != frames[index - 1]


async def test_a_frame_past_the_end_fails_input(clips):
    store = store_of(clips, **{"runs/t/1/0.mp4": "t"})
    end = (await run(command([("runs/t/1/0.mp4", "video")], frame={"at": "last", "index": 25}), store))[-1]["data"]
    assert end["failure"] == {"class": "input", "detail": "the video has 25 frames, no frame 25"}


def test_the_fake_reports_the_stream_facts_ffprobe_reads():
    clip = mp4.mp4(Image.new("RGB", (64, 32), (200, 40, 40)), 9, 24)
    assert facts(clip) == mp4.stream(64, 32, 24)


def test_detect_names_the_hosts_key_media():
    value = media.detect()
    assert value["encoders"][-1] == "libx264" and value["ffmpeg"][0].isdigit()
    assert value["thumbs"] is True  # API 1.48


LOOP = {"mode": "conform", "frames": 86400, "samples": 172800000,
        "args": [*HEAD, "-loop", "1", "-framerate", "24", "-t", "3600", "-i", "{in:0}",
                 "-vf", CONFORM, "-an", "{venc}", "-f", "mp4", "{out}"]}  # fmt: skip


async def test_an_export_past_its_deadline_is_stopped(clips, tmp_path, monkeypatch):
    """A long (or hung) ffmpeg cannot hold the env's one media worker: it is stopped at its deadline."""
    monkeypatch.setattr(tempfile, "tempdir", str(tmp_path))
    monkeypatch.setattr(media_jobs, "DEADLINE_BASE", 1.0)
    monkeypatch.setattr(media_jobs, "DEADLINE_PER_FRAME", 0.0)
    store = FakeStore()
    store.objects[f"{U}uploads/d.png"] = (clips["d"], "image/png")
    events = await asyncio.wait_for(run(command([("uploads/d.png", "image")], LOOP), store), 30)
    end = events[-1]["data"]
    assert end["status"] == "failed" and end["failure"]["class"] == "ffmpeg", end
    assert "timed out" in end["failure"]["detail"] and list(tmp_path.iterdir()) == []
    frames = [e["data"]["frames"] for e in events if e["type"] == "evt.media.progress"]
    assert frames == sorted(set(frames))  # progress only when the count moved: a stall goes silent


@pytest.mark.parametrize("where", ["input", "prefix", "dots"])
def test_keys_outside_the_owners_prefix_dead_letter_the_command(where):
    """Never another user's objects: the schema's objectKey and foreign_keys refuse the command (DLQ, term)."""
    cmd = command([("runs/a/1/0.mp4", "video")], {"mode": "conform", "frames": 24, "samples": 48000,
                                                  "args": ["-i", "{in:0}", "{out}"]})  # fmt: skip
    other = "u/bbbbbbbb-0000-4000-8000-000000000009/"
    if where == "input":
        cmd["data"]["inputs"][0]["key"] = other + "runs/a/1/0.mp4"
    elif where == "prefix":
        cmd["data"]["output_prefix"] = other + "exports/x/"
    else:
        cmd["data"]["inputs"][0]["key"] = f"{U}runs/../../{other}runs/a/1/0.mp4"
    assert schemas.message_errors(cmd) != []


async def test_inputs_past_the_jobs_budget_fail_input(clips, monkeypatch):
    monkeypatch.setattr(media_jobs, "BUDGET", len(clips["a"]) + 10)
    store = store_of(clips, **{"runs/a/1/0.mp4": "a", "runs/b/1/0.mp4": "a"})
    ok = {
        "mode": "conform",
        "frames": 24,
        "samples": 48000,
        "args": ["-i", "{in:0}", "-i", "{in:1}", "{out}"],
    }
    end = (await run(command([("runs/a/1/0.mp4", "video"), ("runs/b/1/0.mp4", "video")], ok), store))[-1]["data"]
    assert end["failure"] == {
        "class": "input",
        "detail": "input 1 is past the job's room on disk",
    }, end


async def test_a_cancel_during_the_downloads_stops_before_the_next(clips):
    kv = FakeKV()
    cmd = command([("runs/a/1/0.mp4", "video"), ("runs/b/1/0.mp4", "video")],
                  {"mode": "conform", "frames": 24, "samples": 48000,
                   "args": ["-i", "{in:0}", "-i", "{in:1}", "{out}"]})  # fmt: skip

    class Slow(FakeStore):
        async def download(self, key, dest, limit, stop=None):
            await kv.put(cmd["job_id"], b"now")
            for _ in range(100):
                if stop and stop():
                    break
                await asyncio.sleep(0.01)
            return await super().download(key, dest, limit, stop)

    store = Slow()
    for k in ("runs/a/1/0.mp4", "runs/b/1/0.mp4"):
        store.objects[f"{U}{k}"] = (clips["a"], "video/mp4")
    events = await asyncio.wait_for(run(cmd, store, kv=kv), 10)
    assert events[-1]["data"]["status"] == "cancelled" and store.reads == [f"{U}runs/a/1/0.mp4"]


def test_venc_per_encoder_and_nvenc_only_when_it_encodes(monkeypatch):
    assert media.venc("h264_nvenc", 8_000_000) == ["-c:v", "h264_nvenc", "-preset", "p5", "-profile:v", "high",
                                                   "-b:v", "8000000", "-maxrate", "8000000", "-bufsize", "16000000"]  # fmt: skip
    assert media.venc("libx264", 10)[:4] == ["-c:v", "libx264", "-preset", "medium"]
    runs = []
    monkeypatch.setattr(media.subprocess, "run", lambda argv, **kw: runs.append(argv) or
                        types.SimpleNamespace(returncode=1))  # fmt: skip
    assert media.usable("h264_nvenc") is False and "h264_nvenc" in runs[0]  # listed, but no GPU encodes
    assert media.usable("libx264")


# ---- API 1.48: thumbnails (contracts/README.md, Thumbnails); PIL only


def thumbs_command(keys: list[str]) -> dict:
    cmd = copy.deepcopy(BASE)
    cmd["data"] = {"kind": "thumbs", "inputs": [{"key": f"{U}{k}", "kind": "image"} for k in keys],
                   "output_prefix": f"{U}thumbs/{BASE['job_id']}/"}  # fmt: skip
    assert schemas.message_errors(cmd) == []
    return cmd


def picture(size, fmt="PNG") -> bytes:
    import os

    buf = io.BytesIO()
    Image.frombytes("RGB", size, os.urandom(size[0] * size[1] * 3)).save(buf, fmt)
    return buf.getvalue()


async def test_a_thumbs_job_writes_each_inputs_thumb_skips_small_ones_and_reports_broken_ones():
    store = FakeStore()
    for key, body in {"uploads/a.png": picture((1600, 900)), "uploads/b.png": picture((300, 300)),
                      "uploads/c.png": b"broken", "runs/r/1/0.poster.png": picture((900, 1600), "JPEG")}.items():  # fmt: skip
        store.objects[f"{U}{key}"] = (body, "image/png")
    inputs = [
        "uploads/a.png",
        "uploads/b.png",
        "uploads/c.png",
        "uploads/gone.png",
        "runs/r/1/0.poster.png",
    ]
    before = dict(store.objects)
    end = (await run(thumbs_command(inputs), store))[-1]["data"]
    assert end["status"] == "succeeded"
    prefix = f"{U}thumbs/{BASE['job_id']}/"
    t = end["result"]["thumbs"]
    assert t[0] == {"index": 0, "key": f"{prefix}0.webp", "width": 910, "height": 512,
                    "bytes": len(store.objects[f"{prefix}0.webp"][0])}  # fmt: skip
    assert t[1] == {"index": 1, "skip": True}
    assert t[2] == {"index": 2, "error": "input 2: not a readable image"}
    assert t[3] == {"index": 3, "error": "input 3 is missing"}
    assert (t[4]["width"], t[4]["height"]) == (512, 910)
    assert set(store.objects) - set(before) == {
        f"{prefix}0.webp",
        f"{prefix}4.webp",
    }  # only <prefix><i>.webp
    assert all(store.objects[k] == v for k, v in before.items())  # no input rewritten
    assert store.objects[f"{prefix}0.webp"][1] == "image/webp"


async def test_a_thumbs_job_naming_another_owners_key_is_dead_lettered():
    cmd = thumbs_command(["uploads/a.png"])
    cmd["data"]["inputs"][0]["key"] = "u/00000000-0000-4000-8000-000000000000/uploads/a.png"
    js = FakeJS()
    jobs = media_jobs.MediaJobs(js=js, cancels_kv=FakeKV(), store=FakeStore(), encoders=["libx264"])
    msg = FakeMsg(cmd, js, subject="cmd.media.fake.fake")
    await jobs.handle(msg)
    assert msg.log[-1][0] == "term" and js.sent[0][0] == "dlq.cmd.media.fake.fake"


async def test_a_frame_job_writes_the_frames_thumb(clips, tmp_path):
    big = tmp_path / "big.mp4"
    await asyncio.to_thread(subprocess.run, ["ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc2=s=1280x720:r=24:d=0.2",
                    "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", str(big)], check=True)  # fmt: skip
    store = FakeStore()
    store.objects[f"{U}runs/t/1/0.mp4"] = (big.read_bytes(), "video/mp4")
    r = (await run(command([("runs/t/1/0.mp4", "video")], frame={"at": "first", "index": 0}), store))[-1]
    thumb = r["data"]["result"]["thumb"]
    assert thumb["key"].endswith("/frame.thumb.webp") and (thumb["width"], thumb["height"]) == (
        910,
        512,
    )
    assert store.objects[thumb["key"]][1] == "image/webp"


def _wav(rate: int, channels: int, frames: int) -> bytes:
    import wave

    buf = io.BytesIO()
    with wave.open(buf, "wb") as w:
        w.setnchannels(channels), w.setsampwidth(2), w.setframerate(rate)
        w.writeframes(bytes(range(256)) * (frames * 2 * channels // 256) + bytes(frames * 2 * channels % 256))
    return buf.getvalue()


def test_pcm_decodes_any_audio_at_the_rate_and_channels_asked():
    """API 1.79 (contracts/README.md, Music and sound): a mono WAV as stereo at 48 kHz and back to mono at 24 kHz."""
    src = _wav(24000, 1, 24000)  # 1 s mono
    stereo = media.pcm(src, 48000, 2)
    assert abs(len(stereo) - 48000 * 4) <= 4 * 64
    mono = media.pcm(_wav(48000, 2, 48000), 24000, 1)
    assert abs(len(mono) - 24000 * 2) <= 2 * 64
    with pytest.raises(MediaError) as e:
        media.pcm(b"not audio", 24000, 1)
    assert e.value.cls == "ffmpeg" and "/" not in str(e.value)


def test_mux_keeps_the_picture_and_fits_the_sound_to_its_length(tmp_path):
    """API 1.79 (Save with the video): H.264 copied, the sound AAC stereo, padded (shorter) or cut (longer) to the
    picture's length."""
    poster = Image.new("RGB", (64, 32), "red")
    clip = mp4.mp4(poster, 48, 24)  # 2 s
    for seconds in (0.5, 5):
        out = tmp_path / f"{seconds}.mp4"
        out.write_bytes(media.mux(clip, _wav(44100, 2, round(44100 * seconds))))
        info = media.probe(out)
        assert info["video"]["codec_name"] == "h264" and info["audio"]["codec_name"] == "aac"
        assert info["audio"]["channels"] == 2 and info["frames"] == 48
        assert abs(float(info["audio"]["duration"]) - 2.0) < 0.1


def mux_command(video: str, sound: str) -> dict:
    cmd = copy.deepcopy(BASE)
    cmd["data"] = {"kind": "mux", "output_prefix": f"{U}muxes/{BASE['job_id']}/",
                   "inputs": [{"key": f"{U}{video}", "kind": "video"}, {"key": f"{U}{sound}", "kind": "audio"}]}  # fmt: skip
    assert schemas.message_errors(cmd) == []
    return cmd


@pytest.mark.parametrize("seconds", [0.5, 5])
async def test_a_mux_job_keeps_the_picture_with_the_sound_fitted_to_its_length(clips, seconds):
    """API 1.79 (Save with the video): mux.mp4 with the picture copied, AAC stereo cut or padded to 1 s, its poster
    (frame 0) and last frame; no thumb (the schema has none for a mux)."""
    store = store_of(clips, **{"runs/v/1/0.mp4": "t"})
    store.objects[f"{U}runs/a/1/0.wav"] = (_wav(44100, 2, round(44100 * seconds)), "audio/wav")
    events = await run(mux_command("runs/v/1/0.mp4", "runs/a/1/0.wav"), store)
    end = events[-1]["data"]
    assert end["status"] == "succeeded" and end["encoder"] == "copy"
    r = end["result"]
    assert r["key"].endswith("/mux.mp4") and (r["width"], r["height"]) == (128, 64) and "thumb" not in r
    v = r["video"]
    assert v["frames"] == 25 and v["fps"] == 24 and v["has_audio"]
    frames = decoded(clips["t"], 128, 64)
    assert Image.open(io.BytesIO(store.objects[v["poster"]["key"]][0])).tobytes() == frames[0]
    assert Image.open(io.BytesIO(store.objects[v["last"]["key"]][0])).tobytes() == frames[24]
    with tempfile.NamedTemporaryFile(suffix=".mp4") as f:
        f.write(store.objects[r["key"]][0])
        f.flush()
        info = media.probe(f.name)
    assert info["video"]["codec_name"] == "h264" and info["audio"]["codec_name"] == "aac"
    assert info["audio"]["channels"] == 2 and int(info["audio"]["sample_rate"]) == 48000
    assert abs(float(info["audio"]["duration"]) - 25 / 24) < 0.1


async def test_a_mux_job_whose_sound_is_a_video_fails_input(clips):
    store = store_of(clips, **{"runs/v/1/0.mp4": "t", "runs/a/1/0.wav": "a"})
    end = (await run(mux_command("runs/v/1/0.mp4", "runs/a/1/0.wav"), store))[-1]["data"]
    assert end["status"] == "failed" and end["failure"]["class"] == "input"


def bake_command(kind: str, inputs: list[str], graph: str, frames: int) -> dict:
    cmd = copy.deepcopy(BASE)
    ins = [a for i in range(len(inputs)) for a in ("-i", f"{{in:{i}}}")]
    args = [*HEAD, *ins, "-filter_complex", graph, "-map", "[vout]", "{venc}", "-r", "24", "-pix_fmt", "yuv420p",
            "-map", "[aout]", *TAIL]  # fmt: skip
    cmd["data"] = {"kind": "bake", "output_prefix": f"{U}bakes/{BASE['job_id']}/",
                   "inputs": [{"key": f"{U}{k}", "kind": "video"} for k in inputs],
                   "bake": {"target": TARGET, "frames": frames, "samples": frames * 2000, "video_bitrate": 1000000,
                            "args": args}}  # fmt: skip
    assert schemas.message_errors(cmd) == []
    return cmd


async def test_a_bake_job_wipes_two_clip_ends_into_a_short_clip(clips):
    """API 1.82 (Magic: Send to Timeline): a wipe of the first clip's last half second into the second's first, the
    first's sound faded out: bake.mp4 at the target, exactly 12 frames, with its poster, last frame and thumb."""
    store = store_of(clips, **{"runs/x/1/0.mp4": "a", "runs/y/1/0.mp4": "a"})
    graph = (f"[0:v:0]trim=start=0.5:end=1,setpts=PTS-STARTPTS,{CONFORM}[v0];"
             f"[1:v:0]trim=start=0:end=0.5,setpts=PTS-STARTPTS,{CONFORM}[v1];"
             "[v0][v1]xfade=transition=wipeleft:duration=0.5:offset=0,trim=duration=0.5[vout];"
             "[0:a:0]atrim=start=0.5:end=1,asetpts=N/SR/TB,afade=t=out:st=0:d=0.5[aout]")  # fmt: skip
    events = await run(bake_command("wipe", ["runs/x/1/0.mp4", "runs/y/1/0.mp4"], graph, 12), store)
    end = events[-1]["data"]
    assert end["status"] == "succeeded" and end["encoder"] == "libx264", end
    r = end["result"]
    assert r["key"].endswith("/bakes/" + BASE["job_id"] + "/bake.mp4") and "mode" not in r
    v = r["video"]
    assert v["frames"] == 12 and v["fps"] == 24 and v["has_audio"]
    assert v["poster"]["key"].endswith("/bake.poster.png") and v["last"]["key"].endswith("/bake.last.png")
    assert r.get("thumb") is None or r["thumb"]["key"].endswith("/bake.thumb.webp")
    assert (r["width"], r["height"]) == (256, 128)


async def test_a_bake_whose_output_is_off_its_frames_fails_verify(clips):
    store = store_of(clips, **{"runs/x/1/0.mp4": "a"})
    graph = f"[0:v:0]setpts=PTS-STARTPTS,{CONFORM}[vout];[0:a:0]anull[aout]"
    end = (await run(bake_command("effects", ["runs/x/1/0.mp4"], graph, 12), store))[-1]["data"]
    assert end["status"] == "failed" and end["failure"]["class"] == "verify"
