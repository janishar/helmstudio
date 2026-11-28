"""Magic isolation (API 1.80, plan 39 phase C; contracts/README.md "Magic"): user B gets 404 on every route of one of
A's boards and its snapshot, with the same body as for a random id; B's list holds none of A's boards; a document of
A's naming B's take is refused exactly as one naming a random id, so nothing about B's take leaks. Fake mode, no GPU."""

import uuid

from helpers import ok, png_bytes, upload
from test_magic import BOARDS, board_doc, edits


def same(a, b, a_id: str, b_id: str) -> None:
    assert (a.status_code, a.text.replace(a_id, "<id>")) == (
        b.status_code,
        b.text.replace(b_id, "<id>"),
    ), a.text


def test_another_users_board_is_404(api, api_b):
    b = ok(api.post(BOARDS, json={"name": "A's board", "doc": board_doc()}), 201)
    snap = ok(api.post(f"{BOARDS}/{b['id']}/snapshots"), 201)
    bodies = {
        "edits": edits(1, {"op": "unlink", "block": "v1"}),
        "undo": {"revision": 1},
        "redo": {"revision": 1},
        "restore": {"revision": 1, "to": 1},
    }
    for board, sid in ((b["id"], snap["id"]), (str(uuid.uuid4()), str(uuid.uuid4()))):
        base = f"{BOARDS}/{board}"
        calls = [
            ("GET", base, None),
            ("GET", f"{base}?view=agent", None),
            ("PATCH", base, {"name": "x"}),
            ("GET", f"{base}/revisions", None),
            ("GET", f"{base}/snapshots", None),
            ("POST", f"{base}/snapshots", {}),
            ("PATCH", f"{base}/snapshots/{sid}", {"name": "x"}),
            ("DELETE", f"{base}/snapshots/{sid}", None),
            ("DELETE", base, None),
        ]
        calls += [("POST", f"{base}/{k}", v) for k, v in bodies.items()]
        for method, path, body in calls:
            r = api_b.request(method, path, json=body)
            assert r.status_code == 404 and r.json() == {"detail": "not found"}, (
                method,
                path,
                r.text,
            )
    assert all(x["id"] != b["id"] for x in ok(api_b.get(BOARDS))["boards"])
    # A's board is untouched
    assert ok(api.get(f"{BOARDS}/{b['id']}"))["name"] == "A's board"
    assert len(ok(api.get(f"{BOARDS}/{b['id']}/snapshots"))["snapshots"]) == 1


def test_a_take_of_another_user_tells_nothing(api, api_b, anon):
    theirs = upload(api_b, anon, png_bytes(32, 32), "b.png")["id"]
    nobody = str(uuid.uuid4())

    def create(tid: str):
        d = board_doc()
        d["blocks"][0]["takes"] = [tid]
        return api.post(BOARDS, json={"doc": d})

    same(create(theirs), create(nobody), theirs, nobody)
    assert create(theirs).status_code == 422
    b = ok(api.post(BOARDS, json={"doc": board_doc()}), 201)

    def add(tid: str):
        op = {
            "op": "add",
            "mode": "insert",
            "block": {
                "lane": "lines",
                "at": 300,
                "length": 24,
                "kind": "voice",
                "name": "x",
                "request": {"kind": "voice"},
                "takes": [tid],
                "effects": [],
            },
        }
        return api.post(f"{BOARDS}/{b['id']}/edits", json=edits(1, op))

    same(add(theirs), add(nobody), theirs, nobody)
    assert ok(api.get(f"{BOARDS}/{b['id']}"))["revision"] == 1
