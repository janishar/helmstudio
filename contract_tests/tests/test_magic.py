"""Magic boards (API 1.80, plan 39 phase C; contracts/README.md "Magic"): through Kong in fake mode, a new user makes
boards (empty, from a document), lists, renames and deletes them; edits by ops (a dry run storing nothing, an apply
with studio-made ids, all or nothing with the failing op's index, a stale base revision 409, a body that does not
fit), undo and redo, the history, snapshots (made, renamed, restored as a new revision, deleted), the agent view, and
the 100-board cap. Every JSON answer is checked against openapi.yaml with CONTRACT_SCHEMAS=1. No GPU."""

import pytest
from conftest import headers, make_client
from helpers import STUDIO, ok

BOARDS = f"{STUDIO}/magic/boards"


def block(bid: str, lane: str, at: int, length: int, kind: str, name: str) -> dict:
    return {
        "id": bid,
        "lane": lane,
        "at": at,
        "length": length,
        "kind": kind,
        "name": name,
        "request": {"kind": kind},
        "takes": [],
        "effects": [],
    }


def board_doc() -> dict:
    return {
        "target": {"width": 1280, "height": 720, "fps": 24, "sample_rate": 48000},
        "lanes": [
            {"id": "shots", "name": "Shots", "role": "shots"},
            {"id": "lines", "name": "Lines", "role": "voice"},
        ],
        "blocks": [
            block("s1", "shots", 0, 96, "video", "Shot 1"),
            block("s2", "shots", 96, 96, "video", "Shot 2"),
            block("v1", "lines", 10, 120, "voice", "Line 1"),
        ],
        "links": [{"block": "v1", "to": ["s1"]}],
        "transitions": [{"id": "t1", "between": ["s1", "s2"], "kind": "dissolve", "length": 12}],
        "assets": [],
        "notes": [],
    }


ADD = {
    "op": "add",
    "mode": "insert",
    "block": {
        "lane": "shots",
        "at": 96,
        "length": 48,
        "kind": "video",
        "name": "Shot 3",
        "request": {"kind": "video"},
        "takes": [],
        "effects": [],
    },
}


def edits(revision: int, *ops: dict, dry: bool = False) -> dict:
    return {"base_revision": revision, "ops": list(ops), "dry_run": dry}


@pytest.fixture(scope="module")
def maker(new_user):
    """A new user of their own (the 100-board cap counts only theirs)."""
    with make_client(headers(new_user("Magic")["access_token"])) as c:
        yield c


def test_a_board_is_named_from_its_first_prompt(maker):
    """API 1.91 (Magic, Names): "Untitled board" is default until a block has a prompt, then the prompt's first words;
    a typed name or a rename is the user's and kept."""
    b = ok(maker.post(BOARDS), 201)
    assert (b["name"], b["title_source"], b["named_by"]) == ("Untitled board", "default", None)
    base = f"{BOARDS}/{b['id']}"
    lane = {"op": "lane_add", "lane": {"id": "shots", "name": "Shots", "role": "shots"}}
    shot = ADD | {
        "block": ADD["block"]
        | {"at": 0, "request": {"kind": "video", "prompt": "a red fox in the snow"}}
    }
    named = ok(maker.post(f"{base}/edits", json=edits(1, lane, shot)))["board"]
    assert (named["name"], named["title_source"]) == ("A red fox in the", "prompt")
    assert named["session_id"]  # the board's session, made to ask the namer
    mine = ok(maker.patch(base, json={"name": "Fox"}))
    assert (mine["name"], mine["title_source"], mine["named_by"]) == ("Fox", "user", None)
    doc = board_doc()
    doc["blocks"][0]["request"] = {"kind": "video", "prompt": "gulls over a grey sea"}
    typed = ok(maker.post(BOARDS, json={"name": "Typed", "doc": doc}), 201)
    assert (typed["name"], typed["title_source"]) == ("Typed", "user")
    for x in (b, typed):
        ok(maker.delete(f"{BOARDS}/{x['id']}"), 204)


def test_boards_and_edits(maker):
    api = maker
    empty = ok(api.post(BOARDS), 201)
    assert (
        empty["name"] == "Untitled board" and empty["revision"] == 1 and empty["doc"]["lanes"] == []
    )
    assert empty["doc"]["target"] == {"width": 1280, "height": 720, "fps": 24, "sample_rate": 48000}
    b = ok(api.post(BOARDS, json={"name": "The Last Tide", "doc": board_doc()}), 201)
    base = f"{BOARDS}/{b['id']}"
    listed = ok(api.get(BOARDS))["boards"]
    assert listed[0]["id"] == b["id"] and listed[0]["blocks"] == 3 and listed[0]["frames"] == 192
    assert ok(api.post(BOARDS, json={"name": ""}), 422)["detail"] == "name must be 1-80 characters"

    # a dry run: the studio-made id in the diff, the dissolve gone, nothing stored
    tried = ok(api.post(f"{base}/edits", json=edits(1, ADD, dry=True)))
    assert tried["dry_run"] is True and tried["revision"] == 1
    assert tried["diff"]["blocks"]["added"] == ["b1"] and tried["diff"]["transitions"][
        "removed"
    ] == ["t1"]
    assert tried["warnings"] == ["Line 1 runs 1.4 s past Shot 1"]
    assert ok(api.get(base))["revision"] == 1
    # stale
    r = api.post(f"{base}/edits", json=edits(2, ADD))
    assert (
        r.status_code == 409
        and r.json()["detail"] == "board changed since you opened it (now revision 1)"
    )
    # all or nothing
    r = api.post(f"{base}/edits", json=edits(1, ADD, {"op": "split", "id": "s1", "at": 500}))
    assert r.status_code == 422 and r.json() == {
        "detail": "The playhead isn't on it.",
        "op_index": 1,
    }
    r = api.post(f"{base}/edits", json=edits(1, {"op": "lane_move", "id": "lines", "by": 2}))
    assert r.status_code == 422 and r.json()["detail"] == "ops[0]: by must be -1 or 1"
    assert ok(api.get(base))["revision"] == 1

    # the apply, labelled web
    done = ok(
        api.post(
            f"{base}/edits",
            json=edits(1, ADD, {"op": "unlink", "block": "v1"}),
            headers={"X-Helm-Client": "web"},
        )
    )
    assert done["revision"] == 2 and done["warnings"] == [] and done["board"]["can_undo"] is True
    assert [x["id"] for x in done["board"]["doc"]["blocks"]] == ["s1", "b1", "s2", "v1"]
    hist = ok(api.get(f"{base}/revisions"))["revisions"]
    assert [(h["revision"], h["ops_summary"], h["via"]) for h in hist] == [
        (2, "add, unlink", "web"),
        (1, "create", "api"),
    ]

    # undo, redo
    undone = ok(api.post(f"{base}/undo", json={"revision": 2}))
    assert (
        undone["revision"] == 3 and len(undone["doc"]["blocks"]) == 3 and undone["can_redo"] is True
    )
    assert api.post(f"{base}/undo", json={"revision": 2}).status_code == 409
    redone = ok(api.post(f"{base}/redo", json={"revision": 3}))
    assert redone["revision"] == 4 and len(redone["doc"]["blocks"]) == 4
    assert ok(api.post(f"{base}/redo", json={"revision": 4}), 409)["detail"] == "nothing to redo"

    # snapshots: of revision 1, renamed, restored as a new revision, deleted
    snap = ok(api.post(f"{base}/snapshots", json={"revision": 1}), 201)
    assert snap["name"] == "Snapshot 1" and snap["revision"] == 1 and snap["blocks"] == 3
    snap = ok(api.patch(f"{base}/snapshots/{snap['id']}", json={"name": "Before Shot 3"}))
    assert [s["name"] for s in ok(api.get(f"{base}/snapshots"))["snapshots"]] == ["Before Shot 3"]
    back = ok(api.post(f"{base}/restore", json={"revision": 4, "to": snap["revision"]}))
    assert back["revision"] == 5 and len(back["doc"]["blocks"]) == 3
    assert (
        ok(api.post(f"{base}/restore", json={"revision": 5, "to": 99}), 422)["detail"]
        == "revision 99 is not kept"
    )
    ok(api.delete(f"{base}/snapshots/{snap['id']}"), 204)
    assert ok(api.get(f"{base}/snapshots"))["snapshots"] == []

    # the agent view
    agent = ok(api.get(base, params={"view": "agent"}))
    assert agent["revision"] == 5 and agent["frames"] == 192
    v1 = next(x for x in agent["blocks"] if x["id"] == "v1")
    assert v1["status"] == "draft" and v1["tied_to"] == ["s1"] and v1["at_s"] == 0.417
    # from 1.88 the whole request, to send back with changes
    assert v1["request"]["kind"] == "voice"
    assert agent["transitions"][0]["at_s"] == 4

    # rename, delete
    assert ok(api.patch(base, json={"name": "Tide"}))["name"] == "Tide"
    ok(api.delete(base), 204)
    assert api.get(base).status_code == 404
    assert all(x["id"] != b["id"] for x in ok(api.get(BOARDS))["boards"])


def test_a_hundred_boards(maker):
    have = len(ok(maker.get(BOARDS))["boards"])
    for _ in range(100 - have):
        ok(maker.post(BOARDS), 201)
    r = maker.post(BOARDS)
    assert r.status_code == 409 and r.json()["detail"] == "You have 100 boards; delete some first."
