"""The listen tool's route (API 1.107, contracts/README.md "The listen tool") on the fake stack: POST
/takes/{id}/listen makes one run of echo-listen whose answer is the fake's and whose transcript is reported; the
refusals (an image, another user's take, a bad question); a text workflow is never a take's use."""

import io
import wave

from helpers import STUDIO, new_session, ok, start_run, upload, wait_run

VIDEO = {"video.steps": 1, "video.delay_ms": 0, "prompt.text": "a fox, listened to", "canvas.width": 1024,
         "canvas.height": 576, "duration.frames": 25}  # fmt: skip


def wav() -> bytes:
    buf = io.BytesIO()
    with wave.open(buf, "wb") as w:
        w.setnchannels(1), w.setsampwidth(2), w.setframerate(16000)
        w.writeframes(bytes(32000))  # 1 s of silence
    return buf.getvalue()


def test_listen_answers_with_a_transcript_and_never_as_a_use(api, anon):
    new_session(api, "Contract listen")  # an upload has no session: the caller's newest
    take = upload(api, anon, wav(), "line.wav", content_type="audio/wav", duration_s=1.0)
    run = ok(
        api.post(f"{STUDIO}/takes/{take['id']}/listen", json={"question": "Does it end abruptly?"}),
        202,
    )
    assert run["workflow_id"] == "echo-listen" and run["status"] in ("queued", "running")
    done = wait_run(api, run["id"])
    assert done["status"] == "succeeded", done.get("error")
    assert done["answer"]["text"] == "ECHO-LISTEN: Does it end abruptly?" and done["outputs"] == []
    assert done["resolved"]["texts"] == {"listen.transcript": "ECHO-TRANSCRIPT"}
    quiet = ok(
        api.post(
            f"{STUDIO}/takes/{take['id']}/listen", json={"question": "Music?", "transcript": False}
        ),
        202,
    )
    assert "texts" not in (wait_run(api, quiet["id"]).get("resolved") or {})
    uses = ok(api.get(f"{STUDIO}/takes/{take['id']}/uses"))["uses"]
    assert all(u["workflow_id"] != "echo-listen" for u in uses), uses


def test_listen_hears_a_video_take(api):
    session = new_session(api, "Contract listen")
    made = wait_run(api, start_run(api, session["id"], VIDEO, "echo-video")["id"])
    take_id = made["outputs"][0]["take_id"]
    run = ok(api.post(f"{STUDIO}/takes/{take_id}/listen", json={"question": "Any sound?"}), 202)
    assert run["session_id"] == session["id"]  # the take's own session
    done = wait_run(api, run["id"])
    assert done["status"] == "succeeded", (
        done.get("error"),
        ok(api.get(f"{STUDIO}/takes/{take_id}"))["kind"],
        made["outputs"],
    )
    assert done["answer"]["text"] == "ECHO-LISTEN: Any sound?"


def test_listen_refusals(api, api_b, anon):
    take = upload(api, anon, wav(), "line.wav", content_type="audio/wav", duration_s=1.0)
    path = f"{STUDIO}/takes/{take['id']}/listen"
    assert api_b.post(path, json={"question": "x"}).status_code == 404  # another user's take
    assert api.post(f"{STUDIO}/takes/not-a-uuid/listen", json={"question": "x"}).status_code == 404
    r = api.post(path, json={"question": " "})
    assert r.status_code == 422 and r.json()["detail"] == "question must be 1-500 characters"
    assert api.post(path, json={"question": "x" * 501}).status_code == 422
    assert api.post(path, json={"question": "x", "nope": 1}).status_code == 422
