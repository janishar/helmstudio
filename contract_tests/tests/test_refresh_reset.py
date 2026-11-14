"""Refresh and Reset (spec "Memory, reuse, refresh and reset"): Refresh clears Input and keeps Generation and memory;
Reset clears Input and Generation and keeps memory."""

from helpers import CATALOG, detail, new_session, ok, raw_files, run, started, wait_processed


def test_refresh_then_reset(api):
    d = started(api, names=["ring-01.png", "necklace-01.png"])
    sid = d["session"]["id"]
    d = run(api, sid, scope="sample", category="rings")
    memory = ok(api.get(f"{CATALOG}/memory"))["memory"]
    assert {m["step"] for m in memory} >= {"guidelines"}

    r = ok(api.post(f"{CATALOG}/sessions/{sid}/refresh"))
    s = r["session"]
    assert (s["stage"], s["flow_position"]) == ("ready", "raw")
    assert all(v["items"] == [] and not v["skipped"] for v in s["inputs"].values())
    assert [k["id"] for k in r["skus"]] == [k["id"] for k in d["skus"]]
    assert len(r["runs"]) == len(d["runs"]) and len(r["images"]) == len(d["images"])
    assert ok(api.get(f"{CATALOG}/memory"))["memory"] == memory

    # the next batch: raw images are accepted again and number on from the last batch
    briefs = {k["id"]: k["brief"] for k in d["skus"]}
    ok(api.post(f"{CATALOG}/sessions/{sid}/raw", files=raw_files(["ring-02.png"])), 202)
    d = wait_processed(api, sid)
    assert [k["code"] for k in d["skus"] if k["category"] == "rings"] == ["R-001", "R-002"]
    r2 = next(k for k in d["skus"] if k["code"] == "R-002")
    assert r2["brief"]["resolution"] is None  # not set up until Start: no run takes it yet
    assert api.post(f"{CATALOG}/sessions/{sid}/runs", json={"scope": "skus", "sku_ids": [r2["id"]]}).status_code == 409
    assert all(o["action"].get("category") != "rings" for o in ok(api.get(f"{CATALOG}/sessions/{sid}/next-step"))["options"])
    # Start sets up the new batch with its own choices; the earlier batch keeps the setup it was processed with
    out = {"id": "out-2", "option": "pick", "label": "2K · 4:5", "resolution": "2K", "ratio": "4:5"}
    ok(api.patch(f"{CATALOG}/sessions/{sid}", json={"inputs": {"output": {"items": [out]}}}))
    d = wait_processed(api, sid)
    st = ok(api.post(f"{CATALOG}/sessions/{sid}/start"))
    assert {k["id"]: k["brief"] for k in st["skus"] if k["id"] in briefs} == briefs
    assert next(k for k in st["skus"] if k["id"] == r2["id"])["brief"]["ratio"] == "4:5"
    memory = ok(api.get(f"{CATALOG}/memory"))["memory"]  # this Start saved the new output choice

    r = ok(api.post(f"{CATALOG}/sessions/{sid}/reset"))
    s = r["session"]
    assert (s["stage"], s["flow_position"], s["messages"], s["rules"]) == ("setup", "raw", [], None)
    assert r["skus"] == r["questions"] == r["runs"] == r["images"] == []
    assert ok(api.get(f"{CATALOG}/sessions/{sid}/events"))["events"] == []
    assert ok(api.get(f"{CATALOG}/memory"))["memory"] == memory
    assert detail(api, sid)["skus"] == []

    # only the uploads memory names stay, so its items can be reused after the Reset, here and in a new session
    guidelines = next(m["items"] for m in memory if m["step"] == "guidelines")
    named = {u for item in guidelines for u in item.get("upload_ids", [])}
    assert named and {u["id"] for u in s["uploads"]} == named
    for target in (sid, new_session(api)):
        ok(api.patch(f"{CATALOG}/sessions/{target}", json={"inputs": {"guidelines": {"items": guidelines}}}))
