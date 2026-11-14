"""Input: raw images (multipart, CSV, links), guidelines, uploads, pose suggestions, processing events, questions and
Start."""

import json

from helpers import (
    CATALOG,
    GUIDELINE,
    answer_all,
    detail,
    image,
    new_session,
    ok,
    raw_files,
    wait_processed,
)


def test_raw_multipart_sorts_into_skus(fresh):
    s, skus = fresh["session"], fresh["skus"]
    raw = s["inputs"]["raw"]["items"]
    assert len(raw) == 1 and raw[0]["option"] == "folder" and raw[0]["image_count"] == 12
    assert len(raw[0]["upload_ids"]) == 12
    assert [u["kind"] for u in s["uploads"]].count("raw") == 12
    by_cat = {c: [k["code"] for k in skus if k["category"] == c] for c in ("rings", "necklaces", "anklets")}
    assert by_cat == {
        "rings": [f"R-{n:03d}" for n in range(1, 8)],
        "necklaces": [f"N-{n:03d}" for n in range(1, 5)],
        "anklets": ["A-001"],
    }
    assert all(k["status"] == "Not run" and len(k["raw_urls"]) == 1 for k in skus)
    assert len({k["group_key"] for k in skus if k["category"] == "rings"}) == 2  # two logical batches
    assert s["rules"]["background"]  # read from the guideline txt
    checks = sorted(q["check"] for q in fresh["questions"])
    assert checks == ["duplicate_name", "guidelines_vs_images"]
    assert all(q["blocking"] and q["answer"] is None and len(q["options"]) == 2 for q in fresh["questions"])


def test_raw_folder_paths_make_one_sku_per_sub_folder(api):
    """One sub-folder per product whose files share names: paths keep them apart, one SKU per folder."""
    sid = new_session(api)
    paths = [f"Rings/{sku}/Untitled-{n}.jpg" for sku in ("ring-a", "ring-b") for n in (1, 2)]
    files = [("files", (p.rsplit("/", 1)[1], image((40 * i, 90, 120)), "image/png")) for i, p in enumerate(paths)]
    ok(api.post(f"{CATALOG}/sessions/{sid}/raw", files=files,
                data={"kind": "folder", "paths": paths}), 202)
    d = wait_processed(api, sid)
    assert sorted(len(k["raw_urls"]) for k in d["skus"]) == [2, 2]
    assert sorted(u["name"] for u in d["session"]["uploads"] if u["kind"] == "raw") == sorted(paths)
    bad = [("files", ("a.png", image(), "image/png"))]
    for p in (["../a.png"], ["/abs/a.png"], ["a.png", "b.png"]):
        r = api.post(f"{CATALOG}/sessions/{new_session(api)}/raw", files=bad, data={"paths": p})
        assert r.status_code == 422, p


def test_raw_rejects(api):
    sid = new_session(api)
    url = f"{CATALOG}/sessions/{sid}/raw"
    assert api.post(url, files=[("files", ("x.png", b"not an image", "image/png"))]).status_code == 422
    assert api.post(url, files=raw_files(["a.png"]), data={"kind": "zip"}).status_code == 422
    assert api.post(url, json={"kind": "website", "url": "not a link"}).status_code == 422
    assert api.post(url, json={"kind": "csv", "csv_text": "sku,name\nR1,Ring\n"}).status_code == 422
    assert detail(api, sid)["session"]["processing"]["status"] == "idle"


def test_raw_csv(api):
    sid = new_session(api)
    sheet = (
        "SKU,Name,Category,Image_URL\nR1,Solitaire Halo,Rings,https://cdn.example.com/r1.jpg\n"
        "R1,Solitaire Halo,Rings,https://cdn.example.com/r1b.jpg\nN1,Pearl Choker,Necklaces,https://cdn.example.com/n1.jpg\n"
    )
    files = [("files", ("sheet.csv", sheet.encode(), "text/csv"))]
    r = ok(api.post(f"{CATALOG}/sessions/{sid}/raw", files=files, data={"kind": "csv"}), 202)
    assert (r["image_count"], r["expected_skus"], r["item"]["option"]) == (3, 2, "csv")
    d = wait_processed(api, sid)
    assert sorted((k["code"], k["name"]) for k in d["skus"]) == [("N-001", "Pearl Choker"), ("R-001", "Solitaire Halo")]
    assert d["questions"] == []  # found == expected, no guideline


def test_raw_links(api):
    sid = new_session(api)
    r = ok(api.post(f"{CATALOG}/sessions/{sid}/raw", json={"kind": "website", "url": "https://shop.example.com/rings"}), 202)
    assert r["image_count"] is None and r["item"]["option"] == "website"
    assert api.post(f"{CATALOG}/sessions/{sid}/raw", json={"kind": "drive", "url": "https://x.example.com"}).status_code == 409
    d = wait_processed(api, sid)  # fake mode: placeholder images, no network
    assert d["skus"]


def test_guidelines_and_uploads(api):
    sid = new_session(api)
    files = {"file": ("brand_guidelines.txt", GUIDELINE, "text/plain")}
    item = ok(api.post(f"{CATALOG}/sessions/{sid}/guidelines", files=files), 201)["item"]
    assert item["option"] == "doc" and len(item["upload_ids"]) == 1
    r = ok(api.post(f"{CATALOG}/sessions/{sid}/guidelines", json={"url": "https://brand.example.com/book", "target": "details"}), 201)
    assert r["session"]["inputs"]["details"]["items"][0]["url"] == "https://brand.example.com/book"
    assert len(r["session"]["inputs"]["guidelines"]["items"]) == 1
    up = ok(api.post(f"{CATALOG}/sessions/{sid}/uploads", files={"file": ("me.png", image(), "image/png")},
                     data={"kind": "model", "name": "Our model"}), 201)
    assert (up["kind"], up["name"]) == ("model", "Our model") and up["url"].startswith("http")
    assert api.post(f"{CATALOG}/sessions/{sid}/uploads", files={"file": ("me.png", image(), "image/png")},
                    data={"kind": "hat"}).status_code == 422
    # an upload id is a valid model choice in this session
    pick = {"id": "mdl", "option": "pick", "label": "Our model", "ids": [up["id"]]}
    s = ok(api.patch(f"{CATALOG}/sessions/{sid}", json={"inputs": {"models": {"items": [pick]}}}))
    assert s["inputs"]["models"]["items"][0]["ids"] == [up["id"]]


def test_poses_suggest(api, lib):
    sid = new_session(api)
    body = {"briefs": [{"category": "rings", "text": "close-ups on the hand, and one clean packshot"}]}
    r = ok(api.post(f"{CATALOG}/sessions/{sid}/poses/suggest", json=body))
    ring_poses = {p["id"] for p in lib["poses"] if p["category"] == "rings"}
    assert r["poses"]["rings"] and set(r["poses"]["rings"]) <= ring_poses and r["reply"]
    assert api.post(f"{CATALOG}/sessions/{sid}/poses/suggest", json={"briefs": []}).status_code == 422


def test_events_polling_and_sse(api, fresh):
    sid = fresh["session"]["id"]
    page = ok(api.get(f"{CATALOG}/sessions/{sid}/events"))
    events = page["events"]
    assert events and page["last_seq"] == events[-1]["seq"]
    assert [e["seq"] for e in events] == sorted({e["seq"] for e in events})
    types = {e["type"] for e in events}
    assert {"status", "log", "step", "skus", "question"} <= types
    q = next(e for e in events if e["type"] == "question")
    assert q["data"]["id"] in {x["id"] for x in fresh["questions"]}
    later = ok(api.get(f"{CATALOG}/sessions/{sid}/events", params={"since": events[2]["seq"]}))
    assert [e["seq"] for e in later["events"]] == [e["seq"] for e in events[3:]]
    empty = ok(api.get(f"{CATALOG}/sessions/{sid}/events", params={"since": page["last_seq"]}))
    assert empty == {"events": [], "last_seq": page["last_seq"]}
    assert api.get(f"{CATALOG}/sessions/{sid}/events", params={"since": -1}).status_code == 422

    # SSE replays after Last-Event-ID: id = seq, event = type, data = the event
    want = events[-3:]
    sse = {"Accept": "text/event-stream", "Last-Event-ID": str(want[0]["seq"] - 1)}
    got: list[dict] = []
    with api.stream("GET", f"{CATALOG}/sessions/{sid}/events", headers=sse, timeout=20) as r:
        assert r.status_code == 200 and r.headers["content-type"].startswith("text/event-stream")
        frame: dict = {}
        for line in r.iter_lines():
            if line.startswith(("id:", "event:", "data:")):
                k, v = line.split(":", 1)
                frame[k] = v.strip()
            elif not line and frame:
                got.append(frame)
                frame = {}
                if len(got) == len(want):
                    break
    assert [(int(f["id"]), f["event"]) for f in got] == [(e["seq"], e["type"]) for e in want]
    assert json.loads(got[0]["data"])["seq"] == want[0]["seq"]


def test_answer_and_start(api, fresh):
    sid = fresh["session"]["id"]
    r = api.post(f"{CATALOG}/sessions/{sid}/start")
    assert r.status_code == 409  # blocking questions open
    q = next(q for q in fresh["questions"] if q["check"] == "guidelines_vs_images")
    a = ok(api.post(f"{CATALOG}/questions/{q['id']}/answer", json={"option": 0}))
    assert a["question"]["answer"] == 0 and a["question"]["answered_at"]
    assert {k["code"] for k in a["skus"]} == {"R-004", "R-005", "R-006"}
    assert all(k["brief"]["background"] == "white" for k in a["skus"])
    assert api.post(f"{CATALOG}/questions/{q['id']}/answer", json={"option": 1}).status_code == 409
    assert api.post(f"{CATALOG}/questions/{q['id']}/answer", json={"option": 2}).status_code == 422
    assert api.post(f"{CATALOG}/sessions/{sid}/start").status_code == 409  # one still open
    answer_all(api, detail(api, sid))
    st = ok(api.post(f"{CATALOG}/sessions/{sid}/start"))
    assert (st["session"]["stage"], st["session"]["flow_position"]) == ("ready", "done")
    assert st["summary"]["total"] == 12
    assert [(c["category"], c["count"], c["first_code"], c["last_code"]) for c in st["summary"]["by_category"]] == [
        ("rings", 7, "R-001", "R-007"), ("anklets", 1, "A-001", "A-001"), ("necklaces", 4, "N-001", "N-004")]
    assert st["summary"]["sample_sku_id"] == st["skus"][0]["id"]
    for k in st["skus"]:
        b = k["brief"]
        assert b["model"] and b["background"] and b["poses"] and (b["resolution"], b["ratio"]) == ("2K", "1:1")
    memory = {m["step"]: m for m in ok(api.get(f"{CATALOG}/memory"))["memory"]}
    assert memory["guidelines"]["project_name"] == "Contract drop"
    # reusing the remembered guideline in a new session copies its file into that session
    other = new_session(api)
    reused = [i | {"reused": True} for i in memory["guidelines"]["items"]]
    s = ok(api.patch(f"{CATALOG}/sessions/{other}", json={"inputs": {"guidelines": {"items": reused}}}))
    (got,) = s["inputs"]["guidelines"]["items"]
    assert got["upload_ids"] != reused[0]["upload_ids"]
    assert got["upload_ids"] == [u["id"] for u in s["uploads"] if u["kind"] == "guideline"]
    # started: new raw images wait for Refresh
    assert api.post(f"{CATALOG}/sessions/{sid}/raw", files=raw_files(["ring-99.png"])).status_code == 409


def test_a_question_whose_cause_is_gone_closes(api, lib):
    d = wait_processed(api, processed_sid(api))
    sid = d["session"]["id"]
    anklet_pose = next(p["id"] for p in lib["poses"] if p["category"] == "anklets")
    ring_pose = next(p["id"] for p in lib["poses"] if p["category"] == "rings")
    pick = {"id": "poses-1", "option": "pick", "label": "Poses", "poses": {"rings": [ring_pose, anklet_pose]}}
    ok(api.patch(f"{CATALOG}/sessions/{sid}", json={"inputs": {"poses": {"items": [pick]}}}))
    d = wait_processed(api, sid)
    assert [q for q in d["questions"] if q["check"] == "pose_vs_product" and q["answer"] is None]
    pick["poses"] = {"rings": [ring_pose]}  # the brand fixes the poses instead of answering
    ok(api.patch(f"{CATALOG}/sessions/{sid}", json={"inputs": {"poses": {"items": [pick]}}}))
    d = wait_processed(api, sid)
    assert not [q for q in d["questions"] if q["check"] == "pose_vs_product"]
    answer_all(api, d)
    ok(api.post(f"{CATALOG}/sessions/{sid}/start"))


def processed_sid(api) -> str:
    """A session with two rings, processing queued."""
    sid = new_session(api)
    ok(api.post(f"{CATALOG}/sessions/{sid}/raw", files=raw_files(["ring-01.png", "ring-02.png"])), 202)
    return sid


def test_library_picks_must_exist(api):
    sid = new_session(api)
    for step, item in [("output", {"resolution": "9K"}), ("output", {"ratio": "7:3"}), ("models", {"ids": ["m999"]}),
                       ("backgrounds", {"ids": ["nowhere"]}), ("poses", {"poses": {"rings": ["ring-nope"]}})]:
        body = {"inputs": {step: {"items": [{"id": "x", "option": "pick", "label": "x"} | item]}}}
        assert api.patch(f"{CATALOG}/sessions/{sid}", json=body).status_code == 422, item


def test_start_without_skus(api):
    assert api.post(f"{CATALOG}/sessions/{new_session(api)}/start").status_code == 409
