"""Takes and the gallery: every run output is a take with what made it; filters, search, pagination, rename and
star, soft delete, reuse."""

import io
import uuid

import pytest
from helpers import (
    NO_SUCH_ID,
    QUICK,
    STUDIO,
    all_takes,
    new_session,
    ok,
    start_run,
    take_of_run,
    wait_run,
)
from PIL import Image

TOKEN = uuid.uuid4().hex[:12]  # a word only this module's prompts contain


@pytest.fixture(scope="module")
def gallery(api) -> dict:
    """User A's session with five finished echo runs, oldest first: {"session_id", "runs", "takes"}."""
    sid = new_session(api, "Contract gallery")["id"]
    prompts = [
        f"{TOKEN} zebra crossing\nsecond line",
        f"{TOKEN} zebra stripes",
        f"{TOKEN} " + "w" * 100,
        f"{TOKEN} lone heron",
        f"{TOKEN} night owl",
    ]
    runs = []
    for p in prompts:
        run = start_run(api, sid, QUICK | {"prompt.text": p})
        assert wait_run(api, run["id"])["status"] == "succeeded"
        runs.append(run)
    takes = [take_of_run(api, r["id"]) for r in runs]
    return {"session_id": sid, "runs": runs, "takes": takes, "prompts": prompts}


def test_run_output_is_a_take(api, anon, echo_done):
    rid, sid = echo_done["run"]["id"], echo_done["session"]["id"]
    (out,) = ok(api.get(f"{STUDIO}/runs/{rid}"))["outputs"]
    take = ok(api.get(f"{STUDIO}/takes/{out['take_id']}"))
    (event,) = [e["data"] for e in echo_done["events"] if e["type"] == "output"]
    assert event["take_id"] == take["id"]
    title = ok(api.get(f"{STUDIO}/workflows/echo"))["title"]
    assert {
        k: take[k] for k in ("kind", "source", "title", "content_type", "session_id", "run_id")
    } == {
        "kind": "image",
        "source": "create",
        "title": "a red fox",
        "content_type": "image/png",
        "session_id": sid,
        "run_id": rid,
    }
    assert (take["starred"], take["parents"]) == (False, [])
    assert "key" not in take  # only signed links
    assert take["made_with"] == {
        "workflow_id": "echo",
        "workflow_title": title,
        "inputs": {
            "prompt.text": "a red fox",
            "echo.steps": 3,
            "echo.delay_ms": 50,
            "echo.fail": False,
            "echo.refs": [],
        },
        "prompt": "a red fox",
        "seed": None,  # echo has no seed node
        "model": None,
        "host": "fake",  # API 1.18: the run's host
    }
    for link in (take["url"], take["thumb_url"]):
        r = anon.get(link)
        assert r.status_code == 200 and r.headers["content-type"] == "image/png"
        assert len(r.content) == take["bytes"]
        size = Image.open(io.BytesIO(r.content)).size
        assert (take["width"], take["height"]) in ((None, None), size)  # as the runner reported


def test_titles(gallery):
    titles = [t["title"] for t in gallery["takes"]]
    assert titles[0] == f"{TOKEN} zebra crossing"  # the first line
    assert len(titles[2]) <= 80 and gallery["prompts"][2].startswith(titles[2])
    assert [t["made_with"]["prompt"] for t in gallery["takes"]] == gallery["prompts"]


def test_newest_first_and_session_list(api, gallery):
    sid, ids = gallery["session_id"], [t["id"] for t in gallery["takes"]]
    listed = all_takes(api, session_id=sid)
    assert [t["id"] for t in listed] == ids[::-1]
    assert listed == all_takes(api, f"{STUDIO}/sessions/{sid}/takes")
    created = [t["created_at"] for t in listed]
    assert created == sorted(created, reverse=True)


def test_filters(api, gallery):
    sid, ids = gallery["session_id"], {t["id"] for t in gallery["takes"]}

    def found(**params) -> set:
        return {t["id"] for t in all_takes(api, session_id=sid, **params)}

    assert found(kind="image") == ids and found(kind="video") == set()
    assert found(source="create") == ids and found(source="upload") == set()
    assert found(starred="true") == set() and found(starred="false") == ids
    assert ok(api.get(f"{STUDIO}/takes", params={"session_id": NO_SUCH_ID}))["takes"] == []


def test_search(api, gallery):
    t = gallery["takes"]

    def search(q: str) -> list:
        return [x["id"] for x in all_takes(api, q=q)]

    assert search(f"{TOKEN} ZEBRA") == [
        t[1]["id"],
        t[0]["id"],
    ]  # every word, any case, newest first
    assert search(f"{TOKEN} zebra stripes") == [t[1]["id"]]
    assert search(f"heron {TOKEN}") == [t[3]["id"]]  # words in any order
    assert search(f"{TOKEN} giraffe") == []
    assert set(search(TOKEN)) == {x["id"] for x in t}
    renamed = ok(api.patch(f"{STUDIO}/takes/{t[4]['id']}", json={"title": f"moon {TOKEN}x"}))
    assert search(f"{TOKEN}x moon") == [renamed["id"]]  # the title is searched too


def test_cursor_pages(api, gallery):
    sid = gallery["session_id"]
    first = ok(api.get(f"{STUDIO}/takes", params={"session_id": sid, "limit": 2}))
    assert len(first["takes"]) == 2 and first["next_cursor"]
    # a take made between pages must not shift the pages that follow
    newer = start_run(api, sid, QUICK | {"prompt.text": f"{TOKEN} between pages"})
    wait_run(api, newer["id"])
    rest = all_takes(api, session_id=sid, limit=2, cursor=first["next_cursor"])
    paged = [t["id"] for t in first["takes"] + rest]
    assert paged == [t["id"] for t in gallery["takes"]][::-1]
    assert all_takes(api, session_id=sid, limit=200)[0]["id"] == take_of_run(api, newer["id"])["id"]
    last = ok(api.get(f"{STUDIO}/takes", params={"session_id": sid, "limit": 200}))
    assert last["next_cursor"] is None


def test_bad_list_params(api):
    for params in (
        {"kind": "picture"},
        {"source": "batch"},
        {"starred": "maybe"},
        {"session_id": "not-a-uuid"},
        {"q": "x" * 201},
        {"cursor": "not a cursor"},
        {"limit": 0},
        {"limit": 201},
        {"limit": "ten"},
    ):
        r = api.get(f"{STUDIO}/takes", params=params)
        assert r.status_code == 422, f"{params}: {r.status_code} {r.text}"
    assert api.get(f"{STUDIO}/sessions/{NO_SUCH_ID}/takes").status_code == 404
    sid = new_session(api)["id"]
    assert api.get(f"{STUDIO}/sessions/{sid}/takes", params={"limit": 0}).status_code == 422
    assert ok(api.get(f"{STUDIO}/sessions/{sid}/takes")) == {"takes": [], "next_cursor": None}


def test_rename_and_star(api, gallery):
    tid = gallery["takes"][3]["id"]
    t = ok(api.patch(f"{STUDIO}/takes/{tid}", json={"title": "  Heron at dawn  "}))
    assert (t["title"], t["starred"]) == ("Heron at dawn", False)
    t = ok(api.patch(f"{STUDIO}/takes/{tid}", json={"starred": True}))
    assert (t["title"], t["starred"]) == ("Heron at dawn", True)
    assert ok(api.get(f"{STUDIO}/takes/{tid}")) == {
        **t,
        "children": [],
        "decisions": [],
    }  # children and decisions only on GET
    starred = all_takes(api, session_id=gallery["session_id"], starred="true")
    assert [x["id"] for x in starred] == [tid]
    for body in ({}, {"title": "   "}, {"title": "x" * 201}, {"starred": "yes"}, {"kind": "video"}):
        r = api.patch(f"{STUDIO}/takes/{tid}", json=body)
        assert r.status_code == 422, f"{body}: {r.status_code} {r.text}"
    assert api.patch(f"{STUDIO}/takes/{NO_SUCH_ID}", json={"starred": True}).status_code == 404


def test_reuse(api, gallery):
    take = gallery["takes"][1]
    reuse = ok(api.post(f"{STUDIO}/takes/{take['id']}/reuse"))
    assert reuse == {
        "workflow_id": "echo",
        "inputs": take["made_with"]["inputs"],
        "workflow_status": "current",
    }
    run = start_run(api, gallery["session_id"], reuse["inputs"], reuse["workflow_id"])
    assert wait_run(api, run["id"])["status"] == "succeeded"  # ready to post as a RunRequest
    assert take_of_run(api, run["id"])["made_with"]["inputs"] == reuse["inputs"]
    assert api.post(f"{STUDIO}/takes/{NO_SUCH_ID}/reuse").status_code == 404


def test_delete_is_soft_and_final(api):
    sid = new_session(api)["id"]
    run = start_run(api, sid, QUICK | {"prompt.text": f"{TOKEN} doomed"})
    wait_run(api, run["id"])
    tid = take_of_run(api, run["id"])["id"]
    assert api.delete(f"{STUDIO}/takes/{tid}").status_code == 204
    assert api.get(f"{STUDIO}/takes/{tid}").status_code == 404
    assert api.delete(f"{STUDIO}/takes/{tid}").status_code == 404
    assert api.patch(f"{STUDIO}/takes/{tid}", json={"starred": True}).status_code == 404
    assert api.post(f"{STUDIO}/takes/{tid}/reuse").status_code == 404
    assert tid not in {t["id"] for t in all_takes(api, q=f"{TOKEN} doomed")}
    assert all_takes(api, session_id=sid) == []
    assert ok(api.get(f"{STUDIO}/runs/{run['id']}"))["outputs"] == []  # the run drops it
    events = ok(api.get(f"{STUDIO}/sessions/{sid}/events", params={"since": 0}))["events"]
    assert [e for e in events if e["type"] == "output"] == []  # and so does the session's event log
    # the object itself goes with a later sweep; its signed link is not checked here


def test_deleting_a_session_keeps_its_takes(api):
    sid = new_session(api)["id"]
    run = start_run(api, sid, QUICK | {"prompt.text": f"{TOKEN} survivor"})
    wait_run(api, run["id"])
    tid = take_of_run(api, run["id"])["id"]
    assert api.delete(f"{STUDIO}/sessions/{sid}").status_code == 204
    take = ok(api.get(f"{STUDIO}/takes/{tid}"))
    assert (take["session_id"], take["run_id"]) == (None, None)
    assert take["made_with"]["prompt"] == f"{TOKEN} survivor"
    assert ok(api.post(f"{STUDIO}/takes/{tid}/reuse"))["workflow_id"] == "echo"


def test_unknown_take_ids(api):
    for tid in (NO_SUCH_ID, "not-a-uuid"):
        assert api.get(f"{STUDIO}/takes/{tid}").status_code == 404
        assert api.delete(f"{STUDIO}/takes/{tid}").status_code == 404
        assert api.post(f"{STUDIO}/takes/{tid}/reuse").status_code == 404
