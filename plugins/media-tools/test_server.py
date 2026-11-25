"""`uv run pytest`: every tool against a fake studio on loopback, with tiny media made here (Pillow, ffmpeg's lavfi
test sources). No network beyond 127.0.0.1."""

import base64
import io
import json
import shutil
import subprocess
import threading
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

import pytest
from PIL import Image

import server

needs_ffmpeg = pytest.mark.skipif(
    not (shutil.which("ffmpeg") and shutil.which("ffprobe")), reason="no ffmpeg"
)
PASS = "eyJvIjoxfQ.c2ln"


class Studio:
    """The plugin API as studio answers it for one owner: takes by id, files by name, new takes recorded."""

    def __init__(self) -> None:
        self.takes, self.files, self.made, self.auth = {}, {}, [], []
        me = self

        class H(BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def send(self, code, body=b"", ctype="application/json"):
                body = json.dumps(body).encode() if isinstance(body, dict) else body
                self.send_response(code)
                self.send_header("Content-Type", ctype)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def do_GET(self):
                if self.path.startswith("/files/"):
                    return self.send(
                        200, me.files[self.path.removeprefix("/files/")], "application/octet-stream"
                    )
                me.auth.append(self.headers.get("Authorization"))
                t = me.takes.get(self.path.removeprefix("/v1/studio/plugin/takes/"))
                return self.send(200, t) if t else self.send(404, {"detail": "not found"})

            def do_PUT(self):
                n = int(self.headers["Content-Length"])
                me.files[self.path.removeprefix("/files/")] = self.rfile.read(n)
                self.send(204)

            def do_POST(self):
                me.auth.append(self.headers.get("Authorization"))
                n = int(self.headers.get("Content-Length") or 0)
                body = json.loads(self.rfile.read(n) or b"{}")
                if self.path == "/v1/studio/plugin/takes":
                    if any(p not in me.takes for p in body["parents"]):
                        return self.send(404, {"detail": "not found"})
                    tid = str(uuid.uuid4())
                    me.made.append(body | {"id": tid})
                    up = {
                        "take_id": tid,
                        "upload_url": f"{me.url}/files/{tid}",
                        "expires_at": "2026-11-25T00:00:00Z",
                    }
                    if body["kind"] == "video":
                        up["poster_upload_url"] = f"{me.url}/files/{tid}.poster"
                    return self.send(201, up)
                tid = self.path.split("/")[-2]
                body = next(m for m in me.made if m["id"] == tid)
                assert len(me.files[tid]) == body["bytes"]
                return self.send(200, {"id": tid, "title": body["title"]})

        self.srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.url = f"http://127.0.0.1:{self.srv.server_address[1]}"

    def add(self, kind: str, data: bytes, ctype: str, **info) -> str:
        tid = str(uuid.uuid4())
        self.files[tid] = data
        self.takes[tid] = {"id": tid, "kind": kind, "title": f"{kind} one", "content_type": ctype, "bytes": len(data),
                           "width": info.pop("width", None), "height": info.pop("height", None),
                           "url": f"{self.url}/files/{tid}"} | info  # fmt: skip
        return tid


@pytest.fixture
def st():
    s = Studio()
    yield s
    s.srv.shutdown()


def call(st, tool, inp, api=None):
    return server.answer(tool, {"input": inp}, {"X-Helm-Api": api or st.url.replace("127.0.0.1", "localhost"),
                                                "Authorization": f"Bearer {PASS}"})  # fmt: skip


def png(w=40, h=30, mode="RGB") -> bytes:
    out = io.BytesIO()
    Image.new(mode, (w, h), (200, 10, 10) if mode == "RGB" else (200, 10, 10, 128)).save(out, "PNG")
    return out.getvalue()


def lavfi(tmp: Path, name: str, src: str, *args: str) -> bytes:
    out = tmp / name
    subprocess.run(
        ["ffmpeg", "-nostdin", "-v", "error", "-y", "-f", "lavfi", "-i", src, *args, str(out)],
        check=True,
    )
    return out.read_bytes()


def test_edit_image_applies_each_step_in_order(st):
    tid = st.add("image", png(), "image/png", width=40, height=30)
    code, body = call(st, "edit_image", {"take_id": tid, "crop": {"x": 0, "y": 0, "width": 20, "height": 10},
                                         "rotate": 90, "resize": {"height": 40}, "extend": {"left": 2, "color": "#ffffff"},
                                         "format": "jpeg"})  # fmt: skip
    assert code == 200, body
    made = st.made[-1]
    assert (
        made["parents"] == [tid]
        and made["tool"] == "edit_image"
        and made["content_type"] == "image/jpeg"
    )
    img = Image.open(io.BytesIO(st.files[made["id"]]))
    assert img.format == "JPEG" and img.size == (made["width"], made["height"]) == (
        22,
        40,
    )  # 10×20 → 20×40, +2
    assert body["take_ids"] == [made["id"]] and body["result"]["show"] == f"[[take:{made['id']}]]"
    assert body["line"].startswith("Cropped, rotated 90°") and st.auth == [f"Bearer {PASS}"] * 3
    # transparent canvas keeps alpha
    code, _ = call(st, "edit_image", {"take_id": tid, "extend": {"top": 5, "color": "transparent"}})
    assert code == 200 and Image.open(io.BytesIO(st.files[st.made[-1]["id"]])).mode == "RGBA"


def test_bad_input_and_foreign_takes_are_refused(st):
    tid = st.add("image", png(), "image/png", width=40, height=30)
    assert call(st, "edit_image", {"take_id": tid})[0] == 422  # nothing to do
    assert call(st, "edit_image", {"take_id": "not-an-id", "rotate": 90})[0] == 422
    assert call(st, "edit_image", {"take_id": tid, "rotate": 45})[0] == 422
    assert (
        call(
            st, "edit_image", {"take_id": tid, "crop": {"x": 30, "y": 0, "width": 20, "height": 5}}
        )[0]
        == 422
    )
    assert (
        call(st, "edit_image", {"take_id": str(uuid.uuid4()), "rotate": 90})[0] == 404
    )  # another user's: 404
    assert call(st, "edit_video", {"take_id": tid, "mute": True})[0] == 422  # not a video
    assert call(st, "nope", {})[0] == 404
    # the size limit holds before Pillow allocates: 1 × 4000 resized to 8192 wide would be 268 gigapixels
    tall = st.add("image", png(1, 4000), "image/png", width=1, height=4000)
    code, body = call(st, "edit_image", {"take_id": tall, "resize": {"width": 8192}})
    assert code == 422 and "50 MP" in body["detail"]
    assert (
        call(
            st,
            "edit_image",
            {"take_id": tid, "extend": {"left": 4096, "right": 4096, "top": 4096, "bottom": 4096}},
        )[0]
        == 422
    )  # 8232 × 8222
    code, body = call(st, "edit_image", {"take_id": tid, "rotate": 90}, api="http://10.0.0.5:8300")
    assert (
        code == 422 and "this machine" in body["detail"]
    )  # the API is the stack on this machine only
    code, body = server.answer(
        "edit_image", {"input": {"take_id": tid, "rotate": 90}}, {"X-Helm-Api": st.url}
    )
    assert code == 401
    assert not st.made


@needs_ffmpeg
def test_video_tools(st, tmp_path):
    clip = lavfi(tmp_path, "v.mp4", "testsrc=duration=2:size=64x48:rate=10", "-f", "lavfi", "-i",
                 "sine=duration=2", "-shortest", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac")  # fmt: skip
    vid = st.add(
        "video", clip, "video/mp4", width=64, height=48, duration_s=2.0, fps=10.0, has_audio=True
    )
    sound = st.add("audio", lavfi(tmp_path, "a.m4a", "sine=frequency=880:duration=3", "-c:a", "aac"), "audio/mp4",
                   duration_s=3.0)  # fmt: skip

    code, body = call(
        st, "edit_video", {"take_id": vid, "trim": {"start": 0.5, "end": 1.5}, "speed": 2}
    )
    assert code == 200, body
    made = st.made[-1]
    assert (
        made["kind"] == "video"
        and made["video"]["has_audio"]
        and 0.4 <= made["video"]["duration_s"] <= 0.7
    )
    assert made["id"] + ".poster" in st.files and (made["width"], made["height"]) == (64, 48)

    code, body = call(st, "edit_video", {"take_id": vid, "mute": True})
    assert code == 200 and not st.made[-1]["video"]["has_audio"]
    code, body = call(st, "edit_video", {"take_id": vid, "audio_take_id": sound})
    assert (
        code == 200 and st.made[-1]["parents"] == [vid, sound] and st.made[-1]["video"]["has_audio"]
    )

    code, body = call(st, "frame_image", {"take_id": vid, "at": 1})
    assert code == 200 and st.made[-1]["content_type"] == "image/png"
    assert Image.open(io.BytesIO(st.files[st.made[-1]["id"]])).size == (64, 48)

    n = len(st.made)
    code, body = call(st, "frames", {"take_id": vid, "count": 3, "size": 256})
    assert code == 200 and len(st.made) == n  # frames make no take
    assert [f["timecode"] for f in body["result"]["frames"]] == ["0:00.33", "0:01.00", "0:01.67"]
    assert len(body["images"]) == 3 and base64.b64decode(body["images"][0])[:2] == b"\xff\xd8"
    code, body = call(st, "frames", {"take_id": vid, "at": [0.2, 5]})
    assert code == 200 and len(body["images"]) == 2
    code, body = call(st, "frames", {"take_id": vid, "scenes": True})
    assert (
        code == 200 and body["result"]["frames"][0]["at"] == 0.0 and "scene changes" in body["line"]
    )


@needs_ffmpeg
def test_audio_tools(st, tmp_path):
    a = st.add("audio", lavfi(tmp_path, "a.wav", "sine=duration=2"), "audio/wav", duration_s=2.0)
    b = st.add("audio", lavfi(tmp_path, "b.wav", "sine=frequency=880:duration=1", "-ac", "2", "-ar", "22050"),
               "audio/wav", duration_s=1.0)  # fmt: skip
    code, body = call(
        st, "edit_audio", {"take_id": a, "trim": {"start": 0.5, "end": 1.5}, "volume_db": -6}
    )
    assert code == 200, body
    made = st.made[-1]
    assert (
        made["content_type"] == "audio/mp4"
        and 0.9 <= made["duration_s"] <= 1.1
        and "-6 dB" in body["line"]
    )
    code, body = call(st, "join_audio", {"take_ids": [a, b]})
    assert code == 200, body
    assert st.made[-1]["parents"] == [a, b] and 2.9 <= st.made[-1]["duration_s"] <= 3.2
    assert (
        call(st, "edit_audio", {"take_id": a, "trim": {"start": 3, "end": 4}})[0] == 422
    )  # outside the take


def test_out_of_time_makes_no_take(st):
    """Past the budget the call gives up before it makes a take: pipelines would already have given up on it."""
    tid = st.add("image", png(), "image/png", width=40, height=30)
    studio = server.Studio(st.url, PASS, deadline=server.time.monotonic() + 1)
    with pytest.raises(server.Fail, match="too long"):
        server.edit_image(
            studio, {"take_id": tid, "rotate": 90}, Path(server.tempfile.mkdtemp()), studio.deadline
        )
    assert not st.made


def test_signed_links_go_to_the_stack_inside_the_network():
    """In the container: a link signed under the public base (X-Helm-Api) is read at HELM_API_URL, same path and sig."""
    st = server.Studio("http://kong:8000", PASS, public="http://127.0.0.1:8310")
    assert (
        st.here("http://127.0.0.1:8310/files/u/a/b.png?exp=1&sig=x")
        == "http://kong:8000/files/u/a/b.png?exp=1&sig=x"
    )
    assert st.here("http://elsewhere/files/x") == "http://elsewhere/files/x"
    assert (
        server.Studio("http://127.0.0.1:8300", PASS).here("http://127.0.0.1:8300/f")
        == "http://127.0.0.1:8300/f"
    )
