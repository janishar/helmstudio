"""Save with the video (API 1.79, plan 38 phase M; contracts/README.md "Music and sound"): POST /takes/{audio}/mux
with {video_take_id} is 202 with a media job of kind mux; the fake runner's media worker copies the picture with the
sound; the job's take is a new video take (source create, parents [video, audio], the video's made_with). The same
pair again answers the same job (running or done, while its take is kept); 422 when the take is not audio or the
other not a video; 404 for a take nobody has; 409 when no live host can run ffmpeg (the runner stopped, last). Fake
mode (echo-video), no GPU. Another user's take in either place is 404 in test_take_isolation.py."""

import subprocess

import pytest
from helpers import NO_SUCH_ID, STUDIO, new_session, ok, wait_for
from test_hosts import ENV, HOST, container
from test_timeline import video_inputs, wait_job
from test_video_source import VIDEO, made, wav


def mux(client, audio: str, video: str):
    return client.post(f"{STUDIO}/takes/{audio}/mux", json={"video_take_id": video})


@pytest.fixture(scope="module")
def pair(api, anon) -> dict:
    sid = new_session(api, "Contract mux")["id"]
    return {"video": made(api, sid, VIDEO, video_inputs(text="a fox to score")),
            "audio": wav(api, anon, seconds=1.0)}  # fmt: skip


@pytest.fixture(scope="module")
def done(api, pair) -> dict:
    job = ok(mux(api, pair["audio"]["id"], pair["video"]["id"]), 202)["job"]
    again = ok(mux(api, pair["audio"]["id"], pair["video"]["id"]), 202)["job"]
    assert again["id"] == job["id"], (job, again)
    return {"job": job, "done": wait_job(api, job["id"])}


def test_mux_is_202_with_a_mux_job(pair, done):
    job = done["job"]
    assert job["kind"] == "mux" and job["status"] in ("queued", "running", "succeeded"), job
    assert job["source_take_id"] == pair["audio"]["id"]
    assert job["video_take_id"] == pair["video"]["id"]
    assert job["timeline_id"] is None and job["revision"] is None


def test_its_take_is_the_video_with_the_sound_and_both_parents(api, pair, done):
    end, video = done["done"], pair["video"]
    assert end["status"] == "succeeded" and end["failure"] is None, end
    take = ok(api.get(f"{STUDIO}/takes/{end['take_id']}"))
    assert (take["kind"], take["source"]) == ("video", "create")
    assert take["parents"] == [video["id"], pair["audio"]["id"]]
    assert take["made_with"] == video["made_with"]
    assert (take["width"], take["height"]) == (video["width"], video["height"])
    v = take["video"]
    assert v["has_audio"] and abs(v["frames"] - video["video"]["frames"]) <= 1, v
    assert v["fps"] == video["video"]["fps"] and v["poster_url"]


def test_the_same_pair_again_answers_its_job(api, pair, done):
    again = ok(mux(api, pair["audio"]["id"], pair["video"]["id"]), 202)["job"]
    assert (again["id"], again["take_id"]) == (done["job"]["id"], done["done"]["take_id"])


def test_a_take_of_the_wrong_kind_is_422(api, pair):
    audio, video = pair["audio"]["id"], pair["video"]["id"]
    assert ok(mux(api, video, video), 422)["detail"] == f"take {video} is not audio"
    assert ok(mux(api, audio, audio), 422)["detail"] == f"take {audio} is not a video"
    assert ok(mux(api, audio, "nope"), 422)["detail"] == "video_take_id must be a take id"
    r = api.post(f"{STUDIO}/takes/{audio}/mux", json={"video_take_id": video, "extra": 1})
    assert r.status_code == 422, r.text


def test_a_take_nobody_has_is_404(api, pair):
    assert mux(api, NO_SUCH_ID, pair["video"]["id"]).status_code == 404
    assert mux(api, pair["audio"]["id"], NO_SUCH_ID).status_code == 404
    assert mux(api, "not-an-id", pair["video"]["id"]).status_code == 404


def docker(*args: str) -> None:
    subprocess.run(["docker", *args], capture_output=True, timeout=90, check=True)


def media_live(client) -> bool:
    hosts = ok(client.get(f"{STUDIO}/hosts"))["hosts"]
    return any((h["host"], h["env"]) == (HOST, ENV) and h["state"] != "offline" for h in hosts)


def test_no_live_ffmpeg_host_is_409(api, anon, pair):
    """Run last: the one fake runner stopped (its key goes offline after 30 s), then started again."""
    other = wav(api, anon, seconds=1.0)["id"]  # a pair with no job yet
    try:
        docker("stop", container(HOST))
    except (OSError, subprocess.SubprocessError) as e:
        pytest.skip(f"cannot stop the fake runner: {e}")
    try:
        wait_for(lambda: not media_live(api), timeout=90, interval=1, what=f"{HOST} offline")
        r = mux(api, other, pair["video"]["id"])
        assert (r.status_code, r.json()["detail"]) == (409, "no live host can export (ffmpeg)")
    finally:
        docker("start", container(HOST))
        wait_for(lambda: media_live(api), timeout=120, interval=1, what=f"{HOST} live")
