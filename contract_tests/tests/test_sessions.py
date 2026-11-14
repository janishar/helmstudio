"""Sessions (create, list, get, patch), the library, favorites, estimate, auth."""

import pytest
from helpers import CATALOG, detail, new_id, new_session, ok

STEPS = ["raw", "guidelines", "output", "poses", "models", "backgrounds", "details"]


def test_create_get_list(api):
    d = ok(api.post(f"{CATALOG}/sessions", json={"project_name": "Diwali drop"}), 201)
    s = d["session"]
    assert (s["project_name"], s["stage"], s["flow_position"]) == ("Diwali drop", "setup", "raw")
    assert sorted(s["inputs"]) == sorted(STEPS)
    assert all(v == {"items": [], "skipped": False} for v in s["inputs"].values())
    assert s["processing"]["status"] == "idle" and s["rules"] is None
    assert d["skus"] == d["questions"] == d["runs"] == d["images"] == []
    assert detail(api, s["id"])["session"]["id"] == s["id"]

    default = ok(api.post(f"{CATALOG}/sessions"), 201)["session"]
    assert default["project_name"] == "Untitled project"
    listed = ok(api.get(f"{CATALOG}/sessions"))["sessions"]
    assert [x["id"] for x in listed[:2]] == [default["id"], s["id"]]  # newest first
    assert listed[1]["sku_count"] == 0 and listed[1]["counts"]["skus"] == 0


def test_patch(api):
    sid = new_session(api)
    item = {"id": "out-1", "option": "pick", "label": "2K · 4:5", "resolution": "2K", "ratio": "4:5"}
    body = {
        "project_name": "Renamed",
        "flow_position": "poses",
        "messages": [{"id": "m1", "kind": "agent", "text": "hello"}],
        "inputs": {"output": {"items": [item]}, "models": {"skipped": True}},
    }
    s = ok(api.patch(f"{CATALOG}/sessions/{sid}", json=body))
    assert (s["project_name"], s["flow_position"]) == ("Renamed", "poses")
    assert s["messages"] == body["messages"]
    assert s["inputs"]["output"]["items"][0]["ratio"] == "4:5"
    assert s["inputs"]["models"] == {"items": [], "skipped": True}
    assert s["inputs"]["guidelines"] == {"items": [], "skipped": False}  # steps not sent are untouched
    s = ok(api.patch(f"{CATALOG}/sessions/{sid}", json={"inputs": {"output": {"items": []}}}))
    assert s["inputs"]["output"]["items"] == [] and s["project_name"] == "Renamed"


@pytest.mark.parametrize(
    "body",
    [
        {},
        {"stage": "ready"},
        {"flow_position": "nowhere"},
        {"inputs": {"output": {"items": [{"id": "x", "option": "pick", "label": "x", "ratio": "wide"}]}}},
        {"inputs": {"models": {"items": [{"id": "x", "option": "pick", "label": "x", "upload_ids": [new_id()]}]}}},
    ],
)
def test_patch_rejects(api, body):
    sid = new_session(api)
    assert api.patch(f"{CATALOG}/sessions/{sid}", json=body).status_code == 422


def test_unknown_and_malformed_ids_are_404(api):
    for path in (f"{CATALOG}/sessions/{new_id()}", f"{CATALOG}/sessions/not-a-uuid"):
        assert api.get(path).status_code == 404
    assert api.post(f"{CATALOG}/questions/{new_id()}/answer", json={"option": 0}).status_code == 404
    assert api.patch(f"{CATALOG}/images/nope", json={"status": "approved"}).status_code == 404
    assert api.patch(f"{CATALOG}/skus/{new_id()}", json={"confirmed": True}).status_code == 404


def test_auth_required(no_token, anon):
    for client in (no_token, anon):
        assert client.get(f"{CATALOG}/sessions").status_code == 401
        assert client.post(f"{CATALOG}/sessions", json={}).status_code == 401
        assert client.get(f"{CATALOG}/library").status_code == 401
    bad = no_token.get(f"{CATALOG}/sessions", headers={"Authorization": "Bearer not-a-token"})
    assert bad.status_code == 401


def test_library(lib):
    assert [c["id"] for c in lib["categories"]][:3] == ["rings", "mens-rings", "earrings"]
    pose_ids = {p["id"] for p in lib["poses"]}
    assert {"ring-packshot", "ring-hand-close-up"} <= pose_ids
    for c in lib["categories"]:
        assert set(c["default_poses"]) <= pose_ids
    assert lib["defaults"] == {"resolution": "2K", "ratio": "1:1"}
    assert {r["id"] for r in lib["resolutions"]} == {"1K", "2K", "4K"}
    assert lib["models"] and lib["backgrounds"]


def test_favorites(api, lib):
    model = lib["models"][0]["id"]
    assert model in ok(api.put(f"{CATALOG}/favorites/models/{model}"))["models"]
    assert model in ok(api.put(f"{CATALOG}/favorites/models/{model}"))["models"]  # idempotent
    assert model in ok(api.get(f"{CATALOG}/library"))["favorites"]["models"]
    assert model not in ok(api.delete(f"{CATALOG}/favorites/models/{model}"))["models"]
    assert api.put(f"{CATALOG}/favorites/models/no-such-model").status_code == 404
    assert api.put(f"{CATALOG}/favorites/hats/{model}").status_code == 404


def test_estimate(api, lib):
    per = {r["id"]: r["credits_per_image"] for r in lib["resolutions"]}
    e = ok(api.get(f"{CATALOG}/estimate", params={"images": 4, "resolution": "4K"}))
    assert e == {"images": 4, "resolution": "4K", "credits_per_image": per["4K"], "credits": 4 * per["4K"]}
    assert ok(api.get(f"{CATALOG}/estimate", params={"images": 1}))["resolution"] == "2K"
    assert api.get(f"{CATALOG}/estimate", params={"images": -1}).status_code == 422
    assert api.get(f"{CATALOG}/estimate", params={"images": 1, "resolution": "9K"}).status_code == 422


def test_memory_shape(api):
    memory = ok(api.get(f"{CATALOG}/memory"))["memory"]
    assert len({m["step"] for m in memory}) == len(memory)
    assert all(m["step"] != "raw" for m in memory)
