"""Thumbnails (API 1.48, contracts/README.md "Thumbnails"): a run's video take gets a WebP thumbnail of its poster
from the runner (Take.thumb_url and RunOutput.thumb_url, url stays the file); an upload gets one from the thumbs
media job within seconds; a small picture keeps the original as its thumbnail."""

import io
import os

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
