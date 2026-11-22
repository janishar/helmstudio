"""MiniMax H3 video (API 1.21, contracts/README.md "How the pieces meet", the minimax-h3 bullet; the native runtime)
through Kong.

Without a GPU: when a live native runner publishes `minimax-h3-text-to-video`, its three templates and node schemas
are the canonical fixtures (contracts/fixtures/registry/minimax-h3.*.valid.json), the model is Mac only (runtime
native), and studio refuses a canvas side over the max and a length off the 5 + 17k grid (422, nothing queued);
otherwise these skip.

Opt-in (MINIMAX_H3=1, the Mac native runner joined to the stack BASE_URL points at, as test_minimax): the runner's
refusals (contracts/fixtures/video/minimax-h3.json: a side off the 32 px grid, a canvas over 768 x 1344, audio
without an image or video reference), each failing its run before the h3 child starts, then one tiny text-to-video
(256 x 256, 22 frames, 2 steps; under a minute warm). A released run may unload the torch and mlx runners' weights
first (the hand-over).

API 1.22 (contracts/README.md, Media inputs: rows of media), no GPU: studio refuses every media-rows case of
contracts/fixtures/video/media-rows.json (a short or long take or part, out past the end, the wrong kind, an unknown
take, more than 15 s in all, references while the first frame is wired) and the counts (9 images, 3 videos, 3 audio)
with 422 before anything is queued; the takes are uploads (WAV) and admin imports with the fixture's facts. "Use in…"
offers an image take H3 Image to video (first frame) and References, never Text to video, and a video take never
H3 References. Another user's video or audio take in a rows param is `take <id> not found`. Opt-in: an @element in
Image to video adds its name and text only (the run succeeds), and one tiny Ref2VA run with an image and a 2 s audio
reference (512 x 512, 22 frames, 2 steps)."""

import json
import os
import uuid

import pytest
from conftest import headers, make_client, sign_in
from helpers import STUDIO, TERMINAL, new_session, ok, png_bytes, ref, start_run, upload, wait_for
from test_elements import element
from test_qwen import events_of, get
from test_user_workflows import CONTRACTS
from test_video_source import import_video, wav

FAMILY = "minimax-h3"
T2V, I2V, REFS = (f"{FAMILY}-{w}" for w in ("text-to-video", "image-to-video", "references"))
REG = CONTRACTS / "fixtures" / "registry"
TIMEOUT = 20 * 60
gpu = pytest.mark.skipif(
    os.environ.get("MINIMAX_H3") != "1", reason="set MINIMAX_H3=1 with the mac native runner up"
)


@pytest.fixture(scope="module")
def h3_user(new_user) -> dict:
    return new_user("MiniMaxH3")


@pytest.fixture(scope="module")
def hapi(h3_user):
    """User MiniMaxH3; a 401 signs in again (a run can outlast the 15-minute access token)."""
    with make_client(headers(h3_user["access_token"])) as c:

        def renew(r):
            if r.status_code == 401 and "/auth/" not in r.request.url.path:
                fresh = sign_in(h3_user["email"], h3_user["password"])["access_token"]
                c.headers["Authorization"] = f"Bearer {fresh}"

        c.event_hooks["response"].append(renew)
        yield c


@pytest.fixture(scope="module")
def live(hapi) -> dict[str, dict]:
    """The three published minimax-h3 workflows by id, or skip."""
    r = hapi.get(f"{STUDIO}/workflows/{T2V}")
    if r.status_code == 404:
        pytest.skip(f"no live native runner publishes {T2V}")
    return {wid: get(hapi, f"{STUDIO}/workflows/{wid}") for wid in (T2V, I2V, REFS)}


@pytest.fixture(scope="module")
def sid(hapi, live) -> str:
    return new_session(hapi, "Contract minimax-h3")["id"]


def canonical(name: str) -> dict:
    return json.loads((REG / f"{FAMILY}.{name}.valid.json").read_text())


# ---------------------------------------------------------------- the registry (no GPU)


def test_templates_are_the_canonical_ones(live):
    want = canonical("workflows")["workflows"]
    assert [w["id"] for w in want] == [T2V, I2V, REFS]

    def shape(nodes: dict) -> dict:
        return {n: (v["kind"], v.get("in") or {}, v.get("params") or {}) for n, v in nodes.items()}

    nodes = {n["kind"]: n for n in canonical("nodes")["nodes"]}
    for w in want:
        got = live[w["id"]]
        assert (got["title"], got.get("action"), got["family"], got["env"], got["media"]) == (
            w["title"],
            w.get("action"),
            FAMILY,
            "native",
            "video",
        )
        assert shape(got["nodes"]) == shape(w["nodes"]), w["id"]
        assert got["studio"] == w["studio"], w["id"]
        for kind, schema in got["node_schemas"].items():
            for key, value in nodes[kind].items():  # what the fixture states is what is served
                assert schema.get(key) == value, (w["id"], kind, key)


def test_image_to_video_binds_new_shot_frames_into_its_image_params(live):
    """API 1.37: Workflow.keyframes is `studio.keyframes` (two image params) for H3 Image to video; none elsewhere."""
    assert live[I2V]["keyframes"] == {"first": "first.image", "last": "last.image"}
    assert live[T2V]["keyframes"] is None and live[REFS]["keyframes"] is None


def test_presets_replace_speed(live):
    """API 1.37: Draft, Fast, Default and Reference in that order (1.41: Turbo after Fast, by time); the templates open on Default;
    no speed param."""
    for w in live.values():
        assert w["studio"]["preset_order"] == ["Draft", "Fast", "Turbo", "Default", "Reference"]
        params = w["nodes"]["generate"]["params"]
        assert "speed" not in params
        assert all(
            params[k.split(".")[1]] == v for k, v in w["studio"]["presets"]["Default"].items()
        )


def test_native_host_and_a_mac_only_model(hapi, live):
    hosts = get(hapi, f"{STUDIO}/hosts")["hosts"]
    assert [h for h in hosts if h["env"] == "native" and h["state"] in ("idle", "busy")], hosts
    models = [m for m in get(hapi, f"{STUDIO}/models")["models"] if m["id"] == FAMILY]
    if not models:
        pytest.skip(f"{FAMILY} is not in the model catalog")
    assert [r["runtime"] for r in models[0]["runtimes"]] == ["native"], models[0]["runtimes"]


def test_turbo_needs_its_add_on_on_the_host_it_is_set_to(hapi, anon, sid, live):
    """API 1.41 (contracts/README.md Optional add-ons: an add-on per value): `lora: turbo` set to the live native
    host needs Turbo LoRA there, with a reference Turbo Ref LoRA: missing, the run is a 422 with the run check's text
    and nothing is queued; present, the plan has no reason (no run is made: it would render)."""
    hosts = sorted({h["host"] for h in get(hapi, f"{STUDIO}/hosts")["hosts"]
                    if h["env"] == "native" and h["state"] in ("idle", "busy")})  # fmt: skip
    (m,) = [m for m in get(hapi, f"{STUDIO}/models")["models"] if m["id"] == FAMILY] or pytest.skip(
        "no model"
    )
    img = upload(hapi, anon, png_bytes(64, 64))["id"]
    for host in hosts:
        for addon, wid, extra in [
            ("turbo", T2V, {}),
            ("turbo_ref", REFS, {"generate.refs": [ref(img)]}),
        ]:
            a = next(x for x in m["addons"] if x["component"] == addon)
            present = any(h["host"] == host and h["state"] == "present" for h in a["hosts"])
            body = {"workflow_id": wid, "host": host, "inputs": {"generate.lora": "turbo"} | extra}
            if present:
                assert ok(hapi.post(f"{STUDIO}/runs/plan", json=body))["reason"] is None, (
                    host,
                    addon,
                )
                continue
            r = hapi.post(f"{STUDIO}/sessions/{sid}/runs", json=body)
            assert r.status_code == 422, (r.status_code, r.text)
            assert r.json()["detail"] == f"generate: {a['title']} not downloaded on host {host}"


def refused(client, sid: str, workflow_id: str, inputs: dict) -> str:
    r = client.post(
        f"{STUDIO}/sessions/{sid}/runs", json={"workflow_id": workflow_id, "inputs": inputs}
    )
    assert r.status_code == 422, (r.status_code, r.text)
    return r.json()["detail"]


def test_studio_refuses_a_side_over_the_max_and_a_length_off_the_grid(hapi, sid):
    assert "canvas.width" in refused(hapi, sid, T2V, {"canvas.width": 1376})
    detail = refused(hapi, sid, T2V, {"duration.frames": 130})
    assert "duration.frames" in detail and "frame grid 5 + 17k up to 362" in detail, detail


# ---------------------------------------------------------------- rows of media (API 1.22, no GPU)

ROWS = json.loads((CONTRACTS / "fixtures" / "video" / "media-rows.json").read_text())


@pytest.fixture(scope="module")
def media(admin, hapi, anon, h3_user, live) -> dict[str, str]:
    """fixture take id → a take of the h3 user with its facts (videos are admin imports, audio WAV uploads)."""
    owner, out = h3_user["user"]["id"], {}
    for fid, t in ROWS["takes"].items():
        if t["kind"] == "video":
            w, h, n, fps, snd = t["width"], t["height"], t["frames"], t["fps"], t["has_audio"]
            out[fid] = import_video(admin, anon, owner, w, h, n, fps, snd)["id"]
        elif t["kind"] == "audio":
            out[fid] = wav(hapi, anon, t["duration_s"])["id"]
        else:
            out[fid] = upload(hapi, anon, png_bytes(64, 64))["id"]
    return out


def swap(v, ids: dict[str, str]):
    """The fixture's take ids in a value (or a text) replaced by the live ones."""
    text = json.dumps(v)
    for a, b in ids.items():
        text = text.replace(a, b)
    return json.loads(text)


@pytest.mark.parametrize(
    "case", [c for c in ROWS["cases"] if c["expect"]["error"]], ids=lambda c: c["description"]
)
def test_studio_refuses_media_rows(hapi, sid, media, case):
    """Each refusal of media-rows.json is studio's 422, word for word, before anything is queued (no GPU)."""
    ids = media | {"11111111-0000-4000-8000-000000000099": str(uuid.uuid4())}
    detail = refused(
        hapi, sid, case["workflow"], {"prompt.text": "a dance"} | swap(case["inputs"], ids)
    )
    assert detail == swap(case["expect"]["error"], ids)


def test_studio_refuses_more_rows_than_the_caps(hapi, sid, media):
    img, vid, aud = (media[f"11111111-0000-4000-8000-0000000000{n}"] for n in ("25", "22", "24"))
    for param, item, cap in (
        ("generate.refs", ref(img), 9),
        ("generate.videos", {"video": {"take_id": vid}}, 3),
        ("generate.audios", {"audio": {"take_id": aud}}, 3),
    ):
        detail = refused(hapi, sid, REFS, {"prompt.text": "a dance", param: [item] * (cap + 1)})
        assert f"{param}: at most {cap} items" in detail, detail


def test_use_in_offers_for_h3(hapi, media):
    """An image take: H3 Image to video's first frame and References (reference 1), never Text to video (its refs are
    not placed); a video take: never References (rows of media are not offered into)."""
    img, vid = (media[f"11111111-0000-4000-8000-0000000000{n}"] for n in ("25", "21"))
    offers = get(hapi, f"{STUDIO}/takes/{img}/uses")["uses"]
    h3 = [(o["workflow_id"], o["param"], o["inputs"]) for o in offers if o["family"] == FAMILY]
    assert h3 == [
        (I2V, "first.image", {"first.image": {"take_id": img}}),
        (REFS, "generate.refs", {"generate.refs": [ref(img)]}),
    ], offers
    r = hapi.post(f"{STUDIO}/takes/{img}/use", json={"workflow_id": I2V})
    assert r.status_code == 200 and r.json()["inputs"] == {"first.image": {"take_id": img}}, r.text
    offers = get(hapi, f"{STUDIO}/takes/{vid}/uses")["uses"]
    assert not [o for o in offers if o["family"] == FAMILY], offers


def test_another_users_media_in_rows_is_not_found(api_b, media):
    """User B cannot name the h3 user's video or audio takes in a rows param (studio's `take <id> not found`, as for
    a media param) nor list their offers."""
    sid_b = new_session(api_b, "Contract B h3 rows")["id"]
    vid, aud = (media[f"11111111-0000-4000-8000-0000000000{n}"] for n in ("21", "24"))
    for param, field, tid in (("generate.videos", "video", vid), ("generate.audios", "audio", aud)):
        detail = refused(
            api_b, sid_b, REFS, {"prompt.text": "x", param: [{field: {"take_id": tid}}]}
        )
        assert detail == f"{param}[1].{field}: take {tid} not found", detail
        assert api_b.get(f"{STUDIO}/takes/{tid}/uses").status_code == 404


# ---------------------------------------------------------------- runs (MINIMAX_H3=1)


def finished(client, run: dict) -> dict:
    return wait_for(
        lambda: (r := get(client, f"{STUDIO}/runs/{run['id']}"))["status"] in TERMINAL and r,
        timeout=TIMEOUT,
        interval=2,
        what=f"{FAMILY} run {run['id']}",
    )


@gpu
@pytest.mark.parametrize(
    "case",
    [
        "a side off the 32 px grid",
        "1344 × 1344 is over the area",
        "audio alone",
    ],
)
def test_runner_refusals(hapi, anon, sid, case):
    """The run fails with h3_generate_error's text (contracts/fixtures/video/minimax-h3.json) before the child starts.
    References with a first frame are studio's 422 from API 1.22 (test_studio_refuses_media_rows)."""
    fixture = json.loads((CONTRACTS / "fixtures" / "video" / "minimax-h3.json").read_text())
    (c,) = [c for c in fixture["cases"] if c["description"] == case]
    node, refs, first, _, width, height, *rest = c["args"]
    audios = rest[2] if len(rest) > 2 else []
    assert node == "generate" and not refs and not first
    inputs = {"prompt.text": "a red fox", "canvas.width": width, "canvas.height": height}
    workflow_id = T2V
    if audios:
        workflow_id = REFS
        inputs |= {
            "generate.audios": [{"audio": {"take_id": wav(hapi, anon, s)["id"]}} for s in audios]
        }
    done = finished(hapi, start_run(hapi, sid, inputs, workflow_id))
    assert (done["status"], done["outputs"]) == ("failed", []), done
    assert c["expect"] in done["error"], done


@gpu
def test_tiny_text_to_video(hapi, sid):
    inputs = {
        "prompt.text": "A red fox trots through fresh snow, soft wind",
        "seed.seed": 42,
        "canvas.width": 256,
        "canvas.height": 256,
        "duration.frames": 22,
        "generate.steps": 2,
    }
    run = start_run(hapi, sid, inputs, T2V)
    done = finished(hapi, run)
    assert done["status"] == "succeeded", done
    phases = {
        e["data"].get("phase") for e in events_of(hapi, sid, run["id"]) if e["type"] == "progress"
    }
    assert "sampling" in phases, phases
    (out,) = done["outputs"]
    take = get(hapi, f"{STUDIO}/takes/{out['take_id']}")
    assert (take["kind"], take["content_type"], take["width"], take["height"]) == (
        "video",
        "video/mp4",
        256,
        256,
    )
    v = take["video"]
    assert (v["frames"], v["fps"], v["has_audio"]) == (22, 24, True) and v["poster_url"], v
    assert take["made_with"]["workflow_id"] == T2V


@gpu
def test_image_to_video_mention_adds_text_only(hapi, anon, sid):
    """In Image to video (first frame wired) an @element adds its name and text, never references (API 1.22,
    contracts/fixtures/mentions/resolve.minimax-h3-image-to-video.json): the run succeeds."""
    still = upload(hapi, anon, png_bytes(256, 256))["id"]
    hero = element(hapi, f"hero{uuid.uuid4().hex[:6]}", [still], text="a tall woman in a red coat")
    inputs = {
        "prompt.text": f"@{hero['handle']} turns to the camera",
        "first.image": {"take_id": still},
        "seed.seed": 42,
        "canvas.width": 256,
        "canvas.height": 256,
        "duration.frames": 22,
        "generate.steps": 2,
    }
    done = finished(hapi, start_run(hapi, sid, inputs, I2V))
    assert done["status"] == "succeeded", done
    mw = get(hapi, f"{STUDIO}/takes/{done['outputs'][0]['take_id']}")["made_with"]
    assert mw["inputs"].get("generate.refs", []) == [], mw["inputs"]
    assert mw["inputs"]["prompt.text"].endswith(f"{hero['name']}: a tall woman in a red coat"), mw


@gpu
def test_tiny_ref2va_with_an_audio_reference(hapi, anon, sid):
    """Ref2VA with an image and a 2 s audio reference (speed rule: 512 x 512, 22 frames, 2 steps): one video with
    sound; the audio take is a parent."""
    img = upload(hapi, anon, png_bytes(512, 512))["id"]
    sound = wav(hapi, anon, 2.5)["id"]
    inputs = {
        "prompt.text": "<Picture 1> sways gently to the beat of <Audio 1>",
        "generate.refs": [ref(img)],
        "generate.audios": [{"audio": {"take_id": sound, "in": 0, "out": 2}}],
        "seed.seed": 42,
        "canvas.width": 512,
        "canvas.height": 512,
        "duration.frames": 22,
        "generate.steps": 2,
    }
    done = finished(hapi, start_run(hapi, sid, inputs, REFS))
    assert done["status"] == "succeeded", done
    (out,) = done["outputs"]
    take = get(hapi, f"{STUDIO}/takes/{out['take_id']}")
    assert (take["kind"], take["width"], take["height"], take["video"]["frames"]) == (
        "video",
        512,
        512,
        22,
    )
    assert {img, sound} <= set(take["parents"]), take["parents"]


def real_video(admin, anon, owner: str, fps: int) -> dict:
    """An imported take with a real H.264 + AAC MP4 (ffmpeg testsrc2 + a sine, 640 x 360, 3 s at `fps`)."""
    import subprocess
    import tempfile

    from helpers import ok
    from test_video_source import IMPORTS

    with tempfile.TemporaryDirectory() as tmp:
        path = f"{tmp}/clip.mp4"
        subprocess.run(["ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", f"testsrc2=size=640x360:rate={fps}:duration=3",
                        "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=3", "-c:v", "libx264",
                        "-pix_fmt", "yuv420p", "-c:a", "aac", "-ac", "2", "-shortest", "-movflags", "+faststart", path],
                       check=True)  # fmt: skip
        with open(path, "rb") as f:
            mp4 = f.read()
    poster = png_bytes(640, 360)
    body = {"owner_id": owner, "key": f"contract:media:{uuid.uuid4().hex[:12]}", "kind": "video",
            "filename": "clip.mp4", "content_type": "video/mp4", "bytes": len(mp4), "width": 640, "height": 360,
            "created_at": "2024-03-01T10:00:00Z", "starred": False, "title": f"real {fps} fps", "legacy": {"studio": "contract"},
            "parents": [], "video": {"duration_s": 3.0, "fps": fps, "frames": 3 * fps, "has_audio": True,
                                     "poster": {"content_type": "image/png", "bytes": len(poster), "width": 640, "height": 360}}}  # fmt: skip
    t = ok(admin.post(IMPORTS, json=body), 201)
    for link, data, ct in (
        (t["upload_url"], mp4, "video/mp4"),
        (t["poster_upload_url"], poster, "image/png"),
    ):
        assert anon.put(link, content=data, headers={"Content-Type": ct}).status_code == 204
    return ok(admin.post(f"{IMPORTS}/{t['take_id']}/complete"))


@gpu
@pytest.mark.parametrize("fps,frames", [(25, 58), (30, 70)])
def test_sound_video_part_at_the_56_frame_edge(admin, hapi, anon, h3_user, sid, fps, frames):
    """A sound video part that reads exactly 56 frames at 24 fps (the smallest h3 takes) from a 25 / 30 fps take runs
    (512 x 512, 56 frames, 2 steps); one frame less is refused before the child."""
    vid = real_video(admin, anon, h3_user["user"]["id"], fps)["id"]
    row = lambda n: [
        {"video": {"take_id": vid, "in": 0.4, "out": round(0.4 + n / fps, 6)}, "sound": "keep"}
    ]
    inputs = {"prompt.text": "The scene of <Video 1>, with its sound", "seed.seed": 42, "canvas.width": 512,
              "canvas.height": 512, "duration.frames": 56, "generate.steps": 2}  # fmt: skip
    short = finished(
        hapi, start_run(hapi, sid, inputs | {"generate.videos": row(frames - 1)}, REFS)
    )
    assert short["status"] == "failed" and "reads 55" in json.dumps(short), short
    done = finished(hapi, start_run(hapi, sid, inputs | {"generate.videos": row(frames)}, REFS))
    assert done["status"] == "succeeded", done
    take = get(hapi, f"{STUDIO}/takes/{done['outputs'][0]['take_id']}")
    assert (take["video"]["frames"], take["video"]["has_audio"]) == (56, True), take["video"]
    assert vid in take["parents"], take["parents"]
