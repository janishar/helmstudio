"""Magic: Send to Timeline (API 1.82, plan 39 phase G; contracts/README.md "Magic: Send to Timeline"): through Kong
in fake mode, two shots run into takes, a dry run's plan (what goes, nothing stored), a send with no bakes (201, the
new sequence at once, the shots on V1 in order), a send with a wipe (202, one bake media job on the fake runner's
ffmpeg, the sequence made when it is in: the baked clip between the shots, its take source bake), the sends list
with each revision, Remove from list (the sequence stays), and user B 404 on another user's board's send, sends and
remove. Every JSON answer is checked against openapi.yaml with CONTRACT_SCHEMAS=1. No GPU."""

from helpers import STUDIO, ok, wait_for
from test_magic import BOARDS
from test_magic_runs import shot


def send_shot(bid: str, at: int, prompt: str) -> dict:
    """A shot on Echo keyframes (no frames: text to video), so test_magic_runs' Echo video estimate stays the pack's."""
    b = shot(bid, at, prompt)
    b["request"] = {"kind": "video", "model": "echo-image-to-video", "prompt": prompt}
    return b


def send_doc() -> dict:
    return {
        "target": {"width": 1280, "height": 720, "fps": 24, "sample_rate": 48000},
        "lanes": [{"id": "shots", "name": "Shots", "role": "shots"}],
        "blocks": [
            send_shot("s1", 0, "a fox at the bus stop"),
            send_shot("s2", 24, "the bus pulls away"),
        ],
        "links": [],
        "transitions": [],
        "assets": [],
        "notes": [],
    }


def test_a_board_is_sent_as_a_new_sequence(api, api_b):
    b = ok(api.post(BOARDS, json={"name": "Tide", "doc": send_doc()}), 201)
    base = f"{BOARDS}/{b['id']}"
    ok(api.post(f"{base}/run", json={"revision": 1, "lane": "shots"}), 202)

    def done():
        board = ok(api.get(base))
        st = board["status"]
        return board if st["s1"]["status"] == st["s2"]["status"] == "done" else None

    board = wait_for(done, what="both shots' takes")
    rev = board["revision"]

    dry = ok(api.post(f"{base}/send", json={"revision": rev, "dry_run": True}))
    assert "send" not in dry
    plan = dry["plan"]
    assert plan["name"] == "Tide · send 1" and plan["revision"] == rev and plan["bakes"] == 0
    assert plan["goes"] == [
        {"lane": "shots", "name": "Shots", "kind": "video", "blocks": 2, "track": "V1", "linked": 0}
    ]
    assert ok(api.get(f"{base}/sends")) == {"sends": []}
    assert api.post(f"{base}/send", json={"revision": rev + 5}).status_code == 409
    for r in (
        api_b.post(f"{base}/send", json={"revision": rev}),
        api_b.get(f"{base}/sends"),
    ):
        assert r.status_code == 404 and r.json() == {"detail": "not found"}

    # no bakes: the sequence at once
    sent = ok(api.post(f"{base}/send", json={"revision": rev}), 201)["send"]
    assert sent["status"] == "done" and sent["name"] == "Tide · send 1" and sent["bakes"] == 0
    seq = ok(api.get(f"{STUDIO}/timelines/{sent['timeline_id']}"))
    takes = [c["take_id"] for c in seq["tracks"][0]["clips"]]
    blocks = {x["id"]: x for x in board["doc"]["blocks"]}
    assert takes == [blocks["s1"]["takes"][-1], blocks["s2"]["takes"][-1]]
    assert seq["name"] == "Tide · send 1" and seq["frames"] == 48

    # a wipe: baked by a media job, the sequence once it is in
    edited = ok(
        api.post(
            f"{base}/edits",
            json={
                "base_revision": rev,
                "ops": [{"op": "transition", "between": ["s1", "s2"], "kind": "wipe"}],
            },
        )
    )
    rev = edited["revision"]
    plan = ok(api.post(f"{base}/send", json={"revision": rev, "dry_run": True}))["plan"]
    assert plan["bakes"] == 1 and plan["host"] and plan["bake_seconds"] > 0
    assert plan["joins"] == [
        {
            "transition": edited["board"]["doc"]["transitions"][0]["id"],
            "kind": "wipe",
            "how": "baked",
        }
    ]
    baking = ok(api.post(f"{base}/send", json={"revision": rev}), 202)["send"]
    assert baking["status"] == "baking" and baking["name"] == "Tide · send 2"
    assert [j["kind"] for j in baking["jobs"]] == ["bake"] and baking["jobs"][0][
        "send_id"
    ] == baking["id"]

    def baked():
        sends = ok(api.get(f"{base}/sends"))["sends"]
        newest = next(s for s in sends if s["id"] == baking["id"])
        assert newest["status"] != "failed", newest
        return newest if newest["status"] == "done" else None

    newest = wait_for(baked, what="the bake")
    assert newest["bakes"] == newest["baked"] == 1 and newest["revision"] == rev
    seq = ok(api.get(f"{STUDIO}/timelines/{newest['timeline_id']}"))
    v1 = seq["tracks"][0]["clips"]
    assert len(v1) == 3 and seq["frames"] == 48
    bake = ok(api.get(f"{STUDIO}/takes/{v1[1]['take_id']}"))
    assert (
        bake["source"] == "bake"
        and bake["kind"] == "video"
        and bake["title"] == "Tide · Shot 1 → Shot 2 · Wipe"
    )

    # the list, newest first; Remove keeps the sequence
    sends = ok(api.get(f"{base}/sends"))["sends"]
    assert [s["name"] for s in sends][:2] == ["Tide · send 2", "Tide · send 1"]
    assert api_b.delete(f"{base}/sends/{newest['id']}").status_code == 404
    assert api.delete(f"{base}/sends/{newest['id']}").status_code == 204
    assert newest["id"] not in [s["id"] for s in ok(api.get(f"{base}/sends"))["sends"]]
    ok(api.get(f"{STUDIO}/timelines/{newest['timeline_id']}"))
