"""Thumbnails (API 1.48, contracts/README.md "Thumbnails"): a run's video take gets a WebP thumbnail of its poster
from the runner (Take.thumb_url and RunOutput.thumb_url, url stays the file); an upload gets one from the thumbs
media job within seconds; a small picture keeps the original as its thumbnail. (API 1.104) A video or audio take gets its facts and a video its
frame grid from the facts media job (contracts/README.md "Take facts")."""

import io
import math
import os
import struct
import wave

from helpers import STUDIO, new_session, ok, start_run, upload, wait_for, wait_run
from PIL import Image

VIDEO = {"video.steps": 1, "video.delay_ms": 0, "prompt.text": "a fox, thumbnailed", "canvas.width": 1024,
         "canvas.height": 576, "duration.frames": 9, "duration.fps": 8}  # fmt: skip


def webp(anon, link: str) -> tuple[int, int]:
    r = anon.get(link)
    assert r.status_code == 200 and r.headers["content-type"] == "image/webp", r.headers
    return Image.open(io.BytesIO(r.content)).size


def test_a_run_video_take_has_a_webp_thumbnail_of_its_poster(api, anon):
    session = new_session(api, "Contract thumbs")
    run = wait_run(api, start_run(api, session["id"], VIDEO, "echo-video")["id"])
    assert run["status"] == "succeeded", run
    (out,) = run["outputs"]
    take = ok(api.get(f"{STUDIO}/takes/{out['take_id']}"))
    assert take["thumb_url"] != take["url"] and take["thumb_url"] != take["video"]["poster_url"]
    assert webp(anon, take["thumb_url"]) == (910, 512)  # short side 512
    # RunOutput.thumb_url: its take's; the full poster stays a PNG
    assert out["thumb_url"] and webp(anon, out["thumb_url"]) == (910, 512)
    assert anon.get(take["video"]["poster_url"]).headers["content-type"] == "image/png"


def test_an_upload_gets_a_thumbnail_from_the_thumbs_job(api, anon):
    buf = io.BytesIO()
    Image.frombytes("RGB", (1200, 800), os.urandom(1200 * 800 * 3)).save(buf, "PNG")
    take = upload(api, anon, buf.getvalue(), "big.png")
    assert take["thumb_url"] == take["url"]  # until the job made it: the original

    def made():
        t = ok(api.get(f"{STUDIO}/takes/{take['id']}"))
        return t["thumb_url"] != t["url"] and t

    t = wait_for(made, timeout=90, interval=1, what="the upload's thumbnail")
    assert webp(anon, t["thumb_url"]) == (768, 512)
    assert anon.get(t["url"]).content == buf.getvalue()  # the original is never touched


def test_a_small_upload_keeps_its_original_as_its_thumbnail(api, anon):
    buf = io.BytesIO()
    Image.new("RGB", (300, 200), "red").save(buf, "PNG")
    take = upload(api, anon, buf.getvalue(), "small.png")
    assert take["thumb_url"] == take["url"]


# ---- API 1.104: take facts (contracts/README.md, Take facts), made by the facts media job


def facts_of(api, take_id: str) -> dict:
    return wait_for(lambda: ok(api.get(f"{STUDIO}/takes/{take_id}"))["facts"], timeout=90, interval=1,
                    what="the take's facts")  # fmt: skip


def test_a_video_take_gets_its_facts_and_a_frame_grid(api, anon):
    session = new_session(api, "Contract facts")
    run = wait_run(api, start_run(api, session["id"], VIDEO, "echo-video")["id"])
    assert run["status"] == "succeeded", run
    f = facts_of(api, run["outputs"][0]["take_id"])
    assert (
        f["seconds"] > 0 and f["lufs"] in (None, -70) and not f["abrupt"]
    )  # the fake's clip is silent
    assert isinstance(f["cuts"], list) and 1 <= len(f["at"]) <= 12 and f["at"] == sorted(f["at"])
    r = anon.get(f["grid_url"])
    assert r.status_code == 200 and r.headers["content-type"] == "image/jpeg"
    assert max(Image.open(io.BytesIO(r.content)).size) <= 1024


def test_an_audio_take_gets_its_sound_facts_and_no_grid(api, anon):
    buf = io.BytesIO()
    with wave.open(buf, "wb") as w:  # 1 s of a 440 Hz tone, then 1 s of silence
        w.setnchannels(1), w.setsampwidth(2), w.setframerate(16000)
        tone = [int(8000 * math.sin(2 * math.pi * 440 * i / 16000)) for i in range(16000)]
        w.writeframes(struct.pack(f"<{len(tone)}h", *tone) + bytes(32000))
    take = upload(api, anon, buf.getvalue(), "tone.wav", content_type="audio/wav", duration_s=2.0)
    f = facts_of(api, take["id"])
    assert f["grid_url"] is None and f["at"] == [] and "cuts" not in f
    assert abs(f["seconds"] - 2.0) < 0.05 and f["lufs"] > -40 and 0.8 < f["silence_end"] < 1.1
    assert f["abrupt"] is False and f["silence_start"] == 0
