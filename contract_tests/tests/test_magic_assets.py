"""Magic's assets and storyboard (API 1.83, plan 39 phase E; contracts/README.md "Magic: assets and the storyboard"):
through Kong in fake mode, a board frame served by the image models runs with its style, framing and who and where in
the prompt (the board's @handle as the asset's name) and keeps its style on the run; Make views makes a view whose
take joins the asset's sheet, named; a shot made from the frame takes its words and goes out of date when the frame
changes; a picture of your own joins a block, pinned; the asset ops refuse a handle in use; deleting an asset takes
its picks and writes its mentions as its name; user B 404 on another user's views. Every JSON answer is checked
against openapi.yaml with CONTRACT_SCHEMAS=1. No GPU."""

from helpers import STUDIO, ok, wait_for
from test_magic import BOARDS


def story_doc() -> dict:
    return {
        "target": {"width": 1280, "height": 720, "fps": 24, "sample_rate": 48000},
        "lanes": [
            {"id": "story", "name": "Storyboard", "role": "storyboard"},
            {"id": "shots", "name": "Shots", "role": "shots"},
        ],
        "blocks": [
            {
                "id": "f1",
                "lane": "story",
                "at": 0,
                "length": 48,
                "kind": "frame",
                "name": "Frame 1",
                "request": {
                    "kind": "frame",
                    "style": "rough",
                    "prompt": "@mara waits",
                    "framing": {"size": "wide", "angle": "low", "move": "push_in"},
                    "refs": [{"asset": "a1"}],
                    "advanced": {"echo.delay_ms": 0},
                },
                "takes": [],
                "effects": [],
            }
        ],
        "links": [],
        "transitions": [],
        "assets": [
            {"id": "a1", "kind": "character", "handle": "mara", "name": "Mara", "sheet": []}
        ],
        "notes": [],
    }


def test_frames_views_and_assets(api, api_b):
    kinds = {k["kind"]: k for k in ok(api.get(f"{STUDIO}/magic/kinds"))["kinds"]}
    assert [m["workflow_id"] for m in kinds["frame"]["models"]] == ["echo"] and "off" not in kinds[
        "frame"
    ]

    b = ok(api.post(BOARDS, json={"name": "Story", "doc": story_doc()}), 201)
    base = f"{BOARDS}/{b['id']}"
    assert b["asset_runs"] == []

    # the frame runs on the image model: its style, framing and words, @mara as her name
    r = ok(api.post(f"{base}/run", json={"revision": 1, "blocks": ["f1"]}), 202)
    run = ok(api.get(f"{STUDIO}/runs/{r['started'][0]['run_id']}"))
    assert run["workflow_id"] == "echo"
    assert run["inputs"]["prompt.text"] == (
        "A rough storyboard drawing: simple shapes, grey shading. Wide shot, from a low angle. Mara waits."
    )
    assert r["board"]["runs"][0]["style"] == "rough"

    def status(block: str, want: str):
        def check():
            board = ok(api.get(base))
            return board if board["status"][block]["status"] == want else None

        return wait_for(check, what=f"{block} {want}")

    board = status("f1", "done")
    frame_take = board["doc"]["blocks"][0]["takes"][0]

    # Make views: a view's take joins the sheet, named
    views = f"{base}/assets/a1/views"
    rev = board["revision"]
    assert api.post(views, json={"revision": rev + 5, "views": ["Front"]}).status_code == 409
    r = api.post(f"{base}/assets/nobody/views", json={"revision": rev, "views": ["Front"]})
    assert r.status_code == 422 and r.json()["detail"] == "unknown asset nobody"
    assert api_b.post(views, json={"revision": rev, "views": ["Front"]}).status_code == 404
    # API 1.98: a size out of bounds
    r = api.post(views, json={"revision": rev, "views": ["Front"], "width": 8, "height": 1024})
    assert r.status_code == 422
    v = ok(api.post(views, json={"revision": rev, "views": ["Front"]}), 202)
    assert [s["view"] for s in v["started"]] == ["Front"] and v["board"]["asset_runs"][0][
        "view"
    ] == "Front"
    view_run = ok(api.get(f"{STUDIO}/runs/{v['started'][0]['run_id']}"))
    assert view_run["inputs"]["prompt.text"].startswith("Mara. Front view, facing the camera. A single full-body figure")

    def viewed():
        board = ok(api.get(base))
        sheet = board["doc"]["assets"][0]["sheet"]
        return board if sheet else None

    board = wait_for(viewed, what="the view on the sheet")
    asset = board["doc"]["assets"][0]
    front = asset["sheet"][0]
    assert asset["views"] == {front: "Front"} and board["takes"][front]["url"]

    # a shot made from the frame: the frame's words and move
    edited = ok(
        api.post(
            f"{base}/edits",
            json={
                "base_revision": board["revision"],
                "ops": [
                    {
                        "op": "add",
                        "mode": "insert",
                        "block": {
                            "lane": "shots",
                            "at": 0,
                            "length": 24,
                            "kind": "video",
                            "name": "Shot 1",
                            "from_frame": "f1",
                            "request": {"kind": "video", "model": "echo-image-to-video"},
                            "takes": [],
                            "effects": [],
                        },
                    }
                ],
            },
        )
    )
    r = ok(
        api.post(f"{base}/run", json={"revision": edited["revision"], "blocks": ["b1"]}),
        202,
    )
    shot_run = ok(api.get(f"{STUDIO}/runs/{r['started'][0]['run_id']}"))
    assert shot_run["inputs"]["prompt.text"] == (
        "Wide shot, from a low angle. Mara waits. The camera slowly pushes in."
    )
    assert frame_take in str(shot_run["inputs"])
    board = status("b1", "done")

    # the frame changes: the shot made from it is out of date
    frame = board["doc"]["blocks"][0]
    req = dict(frame["request"], prompt="@mara runs")
    edited = ok(
        api.post(
            f"{base}/edits",
            json={
                "base_revision": board["revision"],
                "ops": [{"op": "block", "id": "f1", "set": {"request": req}}],
            },
        )
    )
    assert edited["board"]["status"]["b1"] == {"status": "out_of_date"}

    # asset ops: a handle in use; a picture of your own pinned; delete takes the picks, writes the name
    r = api.post(
        f"{base}/edits",
        json={
            "base_revision": edited["revision"],
            "ops": [
                {"op": "asset_add", "asset": {"kind": "prop", "handle": "mara", "name": "Mara 2"}}
            ],
        },
    )
    assert r.status_code == 422 and r.json()["detail"] == "That handle is taken."
    edited = ok(
        api.post(
            f"{base}/edits",
            json={
                "base_revision": edited["revision"],
                "ops": [
                    {
                        "op": "asset_add",
                        "asset": {"kind": "prop", "handle": "umbrella", "name": "Umbrella"},
                    },
                    {"op": "asset", "id": "a1", "set": {"note": "yellow raincoat"}},
                    {"op": "take_add", "id": "f1", "take": front},
                    {"op": "asset_delete", "id": "a1"},
                ],
            },
        )
    )
    doc = edited["board"]["doc"]
    f1 = doc["blocks"][0] if doc["blocks"][0]["id"] == "f1" else doc["blocks"][1]
    assert (
        f1["pinned"] == front and f1["request"]["prompt"] == "Mara runs" and "refs" in f1["request"]
    )
    assert f1["request"]["refs"] == [] and [a["handle"] for a in doc["assets"]] == ["umbrella"]
