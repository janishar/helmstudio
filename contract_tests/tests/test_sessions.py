"""Studio sessions: create, list, get, rename, delete."""

from helpers import NO_SUCH_ID, STUDIO, new_session, ok


def test_create_list_get(api):
    s = new_session(api, "  First light  ")
    assert s["title"] == "First light" and s["created_at"] and s["updated_at"]
    untitled = ok(api.post(f"{STUDIO}/sessions"), 201)
    assert untitled["title"] == "Untitled"
    assert ok(api.post(f"{STUDIO}/sessions", json={"title": "   "}), 201)["title"] == "Untitled"
    ids = [x["id"] for x in ok(api.get(f"{STUDIO}/sessions"))["sessions"]]
    assert ids.index(untitled["id"]) < ids.index(s["id"])  # newest first
    assert ok(api.get(f"{STUDIO}/sessions/{s['id']}")) == s


def test_rename(api):
    s = new_session(api)
    renamed = ok(api.patch(f"{STUDIO}/sessions/{s['id']}", json={"title": " Renamed "}))
    assert (renamed["id"], renamed["title"]) == (s["id"], "Renamed")
    assert ok(api.get(f"{STUDIO}/sessions/{s['id']}"))["title"] == "Renamed"
    for body in ({"title": "  "}, {"title": "x" * 201}, {}):
        assert api.patch(f"{STUDIO}/sessions/{s['id']}", json=body).status_code == 422, body
    assert api.post(f"{STUDIO}/sessions", json={"title": "x" * 201}).status_code == 422


def test_delete(api):
    s = new_session(api)
    assert api.delete(f"{STUDIO}/sessions/{s['id']}").status_code == 204
    assert api.get(f"{STUDIO}/sessions/{s['id']}").status_code == 404
    assert api.delete(f"{STUDIO}/sessions/{s['id']}").status_code == 404
    assert s["id"] not in {x["id"] for x in ok(api.get(f"{STUDIO}/sessions"))["sessions"]}


def test_unknown_and_malformed_ids(api):
    for sid in (NO_SUCH_ID, "not-a-uuid"):
        assert api.get(f"{STUDIO}/sessions/{sid}").status_code == 404
        assert api.patch(f"{STUDIO}/sessions/{sid}", json={"title": "x"}).status_code == 404
        assert api.delete(f"{STUDIO}/sessions/{sid}").status_code == 404
        assert api.get(f"{STUDIO}/sessions/{sid}/runs").status_code == 404
        assert api.get(f"{STUDIO}/sessions/{sid}/events").status_code == 404
