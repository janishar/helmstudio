"""Video inputs (API 1.12) on the fake runner's `echo-image-to-video` (the fake ltx-image-to-video: prompt →
echo/keyframes (rows of {image, seconds, strength}, 1..8) → echo/sample (steps bound by role) → output, with
echo/duration's `auto` length): a first-frame run (parents, made_with), keyframe rows refused at Create (past the
clip, a frame used twice, the cap, range, no rows), auto length (resolved.params, the take's made_with and reuse; past
the predicted clip fails the run), stage-bound steps, a video take as a first frame, "Use in…" data, a batch over an
image column, a pipeline image → gate → clip, a LoRA mode on a bake-only family (needs a live ltx host), isolation.
Skipped when the fake runner has no `echo-image-to-video` (before API 1.12). The fake predicts 2 s for any prompt."""

import pytest
from helpers import (
    NO_SUCH_ID,
    STUDIO,
    new_session,
    ok,
    png_bytes,
    ref,
    start_run,
    take_of_run,
    upload,
    wait_run,
)
from test_batches import BATCHES, finish, fresh, lines, rows_of, wait_batch
from test_pipelines import create, echo_step, inputs_node, parked, pick
from test_pipelines import submit as submit_pipeline

WF = "echo-image-to-video"
KF = "keyframes.images"
FRAMES, FPS = 17, 8  # the last frame is 16, at 2 s
CLIP = {
    "prompt.text": "a red fox from a still",
    "sample.delay_ms": 50,
    "duration.frames": FRAMES,
    "duration.fps": FPS,
}


def row(take_id: str, seconds: float | None = None, strength: float | None = None) -> dict:
    """One keyframe row; a field left out takes its default (0 s, strength 1)."""
    out = ref(take_id)
    if seconds is not None:
        out["seconds"] = seconds
    if strength is not None:
        out["strength"] = strength
    return out


@pytest.fixture(scope="module", autouse=True)
def i2v(api) -> dict:
    ids = {w["id"] for w in ok(api.get(f"{STUDIO}/workflows"))["workflows"]}
    if WF not in ids:
        pytest.skip("the fake runner has no echo-image-to-video workflow (before API 1.12)")
    return ok(api.get(f"{STUDIO}/workflows/{WF}"))


@pytest.fixture(scope="module")
def sid(api) -> str:
    return new_session(api, "Contract video inputs")["id"]


@pytest.fixture(scope="module")
def stills(api, anon) -> list[dict]:
    """Two of A's uploaded PNGs."""
    return [upload(api, anon, png_bytes(40, 30), "first.png"), upload(api, anon, png_bytes(30, 40))]


def clip(client, sid: str, inputs: dict) -> tuple[dict, dict]:
    """A finished echo-image-to-video run with these inputs over CLIP: (run, take)."""
    run = start_run(client, sid, CLIP | inputs, WF)
    done = wait_run(client, run["id"])
    assert done["status"] == "succeeded", done
    return done, take_of_run(client, run["id"])


def refused(client, sid: str, inputs: dict, workflow_id: str = WF) -> str:
    r = client.post(
        f"{STUDIO}/sessions/{sid}/runs", json={"workflow_id": workflow_id, "inputs": CLIP | inputs}
    )
    assert r.status_code == 422, f"{inputs}: {r.status_code} {r.text}"
    return r.json()["detail"]


@pytest.fixture(scope="module")
def first(api, sid, stills) -> dict:
    """A's clip from the first still at frame 0."""
    a = stills[0]["id"]
    return clip(api, sid, {KF: [row(a, 0, 1)]})[1]


# ---------------------------------------------------------------- schemas


def test_schemas(i2v):
    s = i2v["node_schemas"]
    kinds = {n: x["kind"] for n, x in i2v["nodes"].items()}
    params = {n: {p["name"]: p for p in s[k]["params"]} for n, k in kinds.items()}
    images = params["keyframes"]["images"]
    assert (images["type"], images.get("min"), images["max"], images["default"]) == (
        "rows",
        1,
        8,
        [],
    )
    assert images["item"]["image"] == "Image" and "mention" not in images
    assert images["item"]["seconds"]["position"] == "seconds"
    steps = params["sample"]["steps"]
    assert steps["values"] == [2, 4]
    assert steps["values_by"] == {"param": "role", "values": {"stage1": [4], "stage2": [2]}}
    assert params["duration"]["frames"]["auto"] == {"min_s": 1, "max_s": 5}
    assert i2v["media"] == "video"


# ---------------------------------------------------------------- first frame and keyframes


def test_first_frame_run(api, stills, first):
    a = stills[0]["id"]
    assert (first["kind"], first["parents"]) == ("video", [a])
    assert first["made_with"]["inputs"][KF] == [row(a, 0, 1)]
    assert first["video"]["frames"] == FRAMES
    children = {c["id"]: c for c in ok(api.get(f"{STUDIO}/takes/{a}"))["children"]}
    assert children[first["id"]]["kind"] == "video"
    reuse = ok(api.post(f"{STUDIO}/takes/{first['id']}/reuse"))
    assert reuse["inputs"][KF] == [row(a, 0, 1)]


def test_first_and_last_frame(api, sid, stills):
    a, b = (t["id"] for t in stills)
    # s = F / fps lands on the last frame; a row without fields is at 0 s, strength 1
    _, take = clip(api, sid, {KF: [row(a), row(b, FRAMES / FPS, 0.5)]})
    assert take["parents"] == [a, b]  # row order


@pytest.mark.parametrize(
    ("rows", "detail"),
    [
        ([("a", 0), ("b", 2.25)], f"{KF}[2].seconds: frame 18 is past the clip's last frame 16"),
        ([("a", 0), ("b", 0.05)], f"{KF}[2].seconds: frame 0 is already used by row 1"),
        ([("a", 1), ("b", 2), ("a", 1.0)], f"{KF}[3].seconds: frame 8 is already used by row 1"),
        ([("a", -1)], f"{KF}[1].seconds"),  # below its range
        ([("a", 0)] * 9, f"{KF}: at most 8 items"),
        ([], f"{KF}: at least 1 items"),
    ],
    ids=["past-the-clip", "same-frame", "same-frame-later", "range", "cap", "none"],
)
def test_keyframe_rows_are_422(api, stills, rows, detail):
    ids = {"a": stills[0]["id"], "b": stills[1]["id"]}
    s = new_session(api)["id"]
    got = refused(api, s, {KF: [row(ids[k], sec) for k, sec in rows]})
    assert detail in got, got
    assert ok(api.get(f"{STUDIO}/sessions/{s}/runs"))["runs"] == []  # nothing was created


def test_other_bad_keyframes_are_422(api, sid, stills):
    a = stills[0]["id"]
    assert f"{KF}[1]: unknown field mask" in refused(api, sid, {KF: [row(a) | {"mask": ref(a)}]})
    assert f"{KF}[1].image: take {NO_SUCH_ID} not found" in refused(
        api, sid, {KF: [row(NO_SUCH_ID)]}
    )
    detail = refused(api, sid, {KF: [row(a)], "duration.frames": 18})  # off the frame grid
    assert "duration.frames: 18 is not on the frame grid 9 + 8k up to" in detail, detail


def test_a_video_take_is_not_a_first_frame(api, sid, first):
    vid = first["id"]
    detail = refused(api, sid, {KF: [row(vid)]})
    assert f"{KF}[1].image: take {vid} is not an image" in detail, detail
    r = api.post(f"{STUDIO}/takes/{vid}/use", json={"workflow_id": WF})
    assert r.status_code == 422 and f"take {vid} is not an image" in r.json()["detail"], r.text


# ---------------------------------------------------------------- auto length


def test_auto_length(api, sid, stills, i2v):
    a = stills[0]["id"]
    run, take = clip(api, sid, {KF: [row(a)], "duration.frames": "auto", "duration.fps": 24})
    frames = run["resolved"]["params"]["duration.frames"]
    spec = {p["name"]: p for p in i2v["node_schemas"]["echo/duration"]["params"]}["frames"]
    assert (frames - spec["min"]) % spec["step"] == 0 and 24 <= frames <= 5 * 24
    assert frames == 41  # the fake predicts 2 s: 48 frames floored onto 9 + 8k
    assert (take["video"]["frames"], take["video"]["fps"]) == (frames, 24)
    assert take["made_with"]["inputs"]["duration.frames"] == frames
    assert take["made_with"]["typed"]["duration.frames"] == "auto"
    reuse = ok(api.post(f"{STUDIO}/takes/{take['id']}/reuse"))
    assert reuse["inputs"]["duration.frames"] == "auto"  # reuse asks for auto again


def test_auto_length_checks_keyframes_on_the_resolved_clip(api, sid, stills):
    a, b = (t["id"] for t in stills)
    auto = {"duration.frames": "auto", "duration.fps": 24}
    # studio cannot know the length: only the frame-twice rule runs at Create ...
    detail = refused(api, sid, auto | {KF: [row(a), row(b, 0.01)]})
    assert f"{KF}[2].seconds: frame 0 is already used by row 1" in detail, detail
    # ... and the runner fails a frame past the predicted 41 with the same words
    run = start_run(api, sid, CLIP | auto | {KF: [row(a), row(b, 4.0)]}, WF)
    done = wait_run(api, run["id"])
    assert (done["status"], done["outputs"]) == ("failed", [])
    assert f"{KF}[2].seconds: frame 96 is past the clip's last frame 40" in done["error"], done


def test_auto_is_not_a_value_of_other_params(api, sid, stills):
    detail = refused(api, sid, {KF: [row(stills[0]["id"])], "sample.steps": "auto"})
    assert "sample.steps" in detail, detail


# ---------------------------------------------------------------- stage-bound steps


def test_steps_are_bound_by_the_stage(api, sid, stills):
    a = stills[0]["id"]
    first_frame = {KF: [row(a)]}
    for inputs, detail in (
        ({"sample.steps": 3}, "sample.steps: must be one of 2, 4"),  # not in values at all
        ({"sample.steps": 2}, "sample.steps: must be one of 4"),
        ({"sample.role": "stage2", "sample.steps": 4}, "sample.steps: must be one of 2"),
        ({"sample.role": "stage2"}, "sample.steps: must be one of 2"),  # the default 4 is stage1's
    ):
        got = refused(api, sid, first_frame | inputs)
        assert detail in got, f"{inputs}: {got}"
    _, take = clip(api, sid, first_frame | {"sample.role": "stage2", "sample.steps": 2})
    assert take["made_with"]["inputs"]["sample.steps"] == 2


def test_a_user_workflow_changing_the_role_is_param_invalid(api, i2v):
    doc = {k: i2v[k] for k in ("title", "family", "env", "nodes")}
    doc["nodes"]["sample"]["params"] = doc["nodes"]["sample"]["params"] | {"role": "stage2"}
    v = ok(api.post(f"{STUDIO}/workflows/validate", json=doc))
    problems = {(p["code"], p.get("node"), p.get("param")) for p in v["problems"]}
    assert ("param_invalid", "sample", "steps") in problems, v["problems"]


# ---------------------------------------------------------------- "Use in…", batches, pipelines


def test_use_in_offers_image_to_video(api, stills):
    """The client offers the workflows with one image input (GET /workflows + node_schemas); for an image take
    the image-to-video templates are those whose input is a rows param with a `position: seconds` field."""
    a = stills[0]["id"]
    offered = []
    for w in ok(api.get(f"{STUDIO}/workflows", params={"media": "video"}))["workflows"]:
        full = ok(api.get(f"{STUDIO}/workflows/{w['id']}"))
        inputs = [
            (node, p)
            for node, n in full["nodes"].items()
            for p in full["node_schemas"][n["kind"]]["params"]
            if p["type"] == "image" or "Image" in (p.get("item") or {}).values()
        ]
        if len(inputs) == 1 and any(
            isinstance(f, dict) and f.get("position") == "seconds"
            for f in (inputs[0][1].get("item") or {}).values()
        ):
            offered.append((w["id"], f"{inputs[0][0]}.{inputs[0][1]['name']}"))
    assert (WF, KF) in offered, offered
    for wid, param in offered:
        want = {
            "workflow_id": wid,
            "inputs": {param: [ref(a)]},
        }  # row 1: the take at 0 s, strength 1
        assert ok(api.post(f"{STUDIO}/takes/{a}/use", json={"workflow_id": wid})) == want


def test_batch_over_an_image_column(api, stills):
    """Gallery multi-select "Use in…": one take id per line on the keyframe rows."""
    ids = [t["id"] for t in stills]
    body = {"workflow_id": WF, "rows": lines(ids, KF), "inputs": CLIP}
    batch = ok(api.post(BATCHES, json=body), 202)
    try:
        assert wait_batch(api, batch["id"])["status"] == "done"
        rows = rows_of(api, batch["id"])
        assert [r["inputs"][KF] for r in rows] == [[ref(t)] for t in ids]
        for r, tid in zip(rows, ids, strict=True):
            (out,) = r["take_ids"]
            t = ok(api.get(f"{STUDIO}/takes/{out}"))
            assert (t["kind"], t["parents"], t["batch_id"]) == ("video", [tid], batch["id"])
    finally:
        finish(api, batch["id"])


def test_pipeline_image_gate_first_frame(api):
    """inputs → still (echo) → review (keep 1) → clip (echo-image-to-video, first frame = the pick) → out."""
    step_inputs = {k: v for k, v in CLIP.items() if k != "prompt.text"}  # the prompt is bound
    clip_step = {"kind": "step", "workflow": WF, "inputs": step_inputs,
                 "bind": {"prompt.text": "$prompt", KF: "review.picked"}}  # fmt: skip
    doc = {
        "title": "Contract first frame",
        "nodes": {
            "inputs": inputs_node("prompt"),
            "still": echo_step({"prompt.text": "$prompt"}),
            "review": {"kind": "gate", "title": "Pick one", "takes": "still", "keep": 1},
            "clip": clip_step,
            "out": {"kind": "output", "from": "clip.takes", "collection": "Contract first frame"},
        },
        "edges": [["inputs", "still"], ["still", "review"], ["review", "clip"], ["clip", "out"]],
    }
    unbound = {
        **doc,
        "nodes": doc["nodes"] | {"clip": clip_step | {"bind": {"prompt.text": "$prompt"}}},
    }
    problems = ok(api.post(f"{STUDIO}/pipelines/validate", json=unbound))["problems"]
    assert {(p["code"], p["node"], p.get("port")) for p in problems} == {
        ("unbound_input", "clip", KF)
    }, problems
    p = create(api, doc)
    assert p["problems"] == [], p["problems"]
    bid = submit_pipeline(api, p["id"], fresh(["a red fox"]))["id"]
    try:
        (r,) = parked(api, bid, [1])
        cand = r["parked"]["candidates"][0]
        pick(api, bid, 1, [cand])
        assert wait_batch(api, bid)["counts"]["succeeded"] == 1
        ((tid,),) = [x["take_ids"] for x in rows_of(api, bid)]
        t = ok(api.get(f"{STUDIO}/takes/{tid}"))
        assert (t["kind"], t["parents"]) == ("video", [cand])  # lineage: clip → picked image
        assert t["made_with"]["inputs"][KF] == [row(cand, 0, 1)]  # resolved: item defaults filled
        assert t["made_with"]["pipeline"]["node"] == "clip"
    finally:
        finish(api, bid)


# ---------------------------------------------------------------- LoRA mode on a bake-only family


def test_adapter_row_on_a_bake_only_family_is_422(api):
    """A family whose lora-stack offers only `bake` (ltx) refuses `mode: adapter` before anything runs. The fake
    family offers both, so this needs a live host publishing such a stack."""
    for w in ok(api.get(f"{STUDIO}/workflows"))["workflows"]:
        full = ok(api.get(f"{STUDIO}/workflows/{w['id']}"))
        for node, n in full["nodes"].items():
            if not n["kind"].endswith("/lora-stack"):
                continue
            rows = {p["name"]: p for p in full["node_schemas"][n["kind"]]["params"]}["rows"]
            if rows["item"]["mode"]["enum"] != ["bake"]:
                continue
            s = new_session(api)["id"]
            bad = [{"lora": {"lora_id": "0" * 64}, "strength": 1, "mode": "adapter"}]
            r = api.post(f"{STUDIO}/sessions/{s}/runs",
                         json={"workflow_id": w["id"], "inputs": {f"{node}.rows": bad}})  # fmt: skip
            assert r.status_code == 422, r.text
            assert f"{node}.rows[1].mode: must be one of bake" in r.json()["detail"], r.text
            assert ok(api.get(f"{STUDIO}/sessions/{s}/runs"))["runs"] == []
            return
    pytest.skip("no live host publishes a bake-only lora-stack (ltx on mlx)")


# ---------------------------------------------------------------- isolation


def test_b_cannot_use_a_takes_as_frames(api_b, stills, first):
    a = stills[0]["id"]
    b_sid = new_session(api_b, "Contract B video inputs")["id"]
    for tid in (a, first["id"]):  # an image and a video: the same words, nothing leaks
        detail = refused(api_b, b_sid, {KF: [row(tid)]})
        assert f"{KF}[1].image: take {tid} not found" in detail, detail
        assert api_b.post(f"{STUDIO}/takes/{tid}/use", json={"workflow_id": WF}).status_code == 404
    body = {"workflow_id": WF, "rows": lines([a], KF), "inputs": CLIP}
    r = api_b.post(BATCHES, json=body)
    assert r.status_code == 422 and f"row 1: {KF}[1].image: take {a} not found" in r.text, r.text
    table = {"columns": [KF], "rows": [[[row(a, 1.0)]]]}
    r = api_b.post(BATCHES, json={"workflow_id": WF, "rows": {"table": table}, "inputs": CLIP})
    assert r.status_code == 422 and f"take {a} not found" in r.text, r.text
    assert ok(api_b.get(f"{STUDIO}/sessions/{b_sid}/runs"))["runs"] == []
