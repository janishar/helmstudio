"""Processing: the Next step card, runs, review, regenerate, typed requests, SKU overrides, stats and download. The
tests walk one started session (fixture `ready`) forward in file order: sample → batch → all → approve → download."""

import csv
import io
import zipfile

import pytest
from helpers import (CATALOG, answer_all, detail, image, live_images, new_id, new_session, ok, processed, run, started,
                     wait_idle, wait_processed)


def next_step(api, sid: str) -> dict:
    return ok(api.get(f"{CATALOG}/sessions/{sid}/next-step"))


def sku(d: dict, code: str) -> dict:
    return next(k for k in d["skus"] if k["code"] == code)


def images_of(d: dict, code: str) -> list[dict]:
    """The live images of a SKU."""
    sid = sku(d, code)["id"]
    return [i for (k, _), i in live_images(d).items() if k == sid]


def test_before_start(api):
    d = processed(api, names=["ring-01.png"], guideline=False)
    sid = d["session"]["id"]
    card = next_step(api, sid)
    assert card["options"] == [] and card["state"] == "next"
    assert api.post(f"{CATALOG}/sessions/{sid}/runs", json={"scope": "all"}).status_code == 409
    assert api.post(f"{CATALOG}/sessions/{sid}/requests", json={"text": "run the rest of the rings"}).status_code == 409


def test_first_card(api, ready):
    card = next_step(api, ready["session"]["id"])
    assert (card["heading"], card["state"]) == ("Next step · Rings", "next")
    assert [o["label"] for o in card["options"]] == ["Run sample · Rings", "Run sample · Anklets"]
    assert card["options"][0]["action"] == {"type": "run", "scope": "sample", "category": "rings"}
    assert all(o["credits"] > 0 for o in card["options"])
    rings = next(c for c in ok(api.get(f"{CATALOG}/library"))["categories"] if c["id"] == "rings")
    assert all(o["worst_credits"] == o["credits"] * rings["max_attempts"] for o in card["options"][:1])


def test_run_rejects(api, ready):
    sid = ready["session"]["id"]
    assert api.post(f"{CATALOG}/sessions/{sid}/runs", json={"scope": "batch", "category": "rings"}).status_code == 422
    assert api.post(f"{CATALOG}/sessions/{sid}/runs", json={"scope": "everything"}).status_code == 422
    assert api.post(f"{CATALOG}/sessions/{sid}/runs", json={"scope": "sample", "category": "hats"}).status_code in (409, 422)


def test_sample_run(api, ready):
    sid = ready["session"]["id"]
    r = ok(api.post(f"{CATALOG}/sessions/{sid}/runs", json={"scope": "sample", "category": "rings"}), 202)
    poses = sku(ready, "R-001")["brief"]["poses"]
    assert r["run"]["scope"] == "sample" and r["run"]["credits_quoted"] > 0
    assert [k["code"] for k in r["skus"]] == ["R-001"] and r["skus"][0]["status"] in ("Queued", "Running")
    assert sorted(i["shot"] for i in r["images"]) == sorted(poses)
    assert all(i["status"] == "generating" and i["url"] is None and i["revision"] == 1 for i in r["images"])
    assert next_step(api, sid)["state"] in ("in_progress", "next")
    d = wait_idle(api, sid)
    assert d["runs"][0]["status"] == "succeeded" and d["runs"][0]["finished_at"]
    imgs = images_of(d, "R-001")
    assert all(i["status"] == "to_review" and i["url"] and i["review"]["passed"] for i in imgs)
    assert sku(d, "R-001")["status"] == "To review"
    card = next_step(api, sid)
    assert card["line"].startswith("R-001 is ready on the right")
    assert [o["action"]["scope"] for o in card["options"]] == ["batch", "all"]
    assert card["options"][0]["label"].startswith("Run next batch · ")
    assert card["options"][1]["label"] == "Run all Rings"
    files = [e for e in ok(api.get(f"{CATALOG}/sessions/{sid}/events"))["events"] if e["type"] == "image"]
    assert files and files[-1]["data"]["url"]


def test_review_single_and_bulk(api, ready):
    sid = ready["session"]["id"]
    a, b, *rest = images_of(detail(api, sid), "R-001")
    r = ok(api.patch(f"{CATALOG}/images/{a['id']}", json={"status": "approved"}))
    assert r["image"]["status"] == "approved" and r["sku"]["status"] == "To review"
    r = ok(api.patch(f"{CATALOG}/images/{b['id']}", json={"status": "rejected"}))
    assert r["sku"]["status"] == "Redo"
    assert api.patch(f"{CATALOG}/images/{b['id']}", json={"status": "maybe"}).status_code == 422
    ok(api.patch(f"{CATALOG}/images/{b['id']}", json={"status": "approved"}))
    r = ok(api.post(f"{CATALOG}/sessions/{sid}/approve", json={"sku_ids": [sku(ready, "R-001")["id"]]}))
    assert r["approved"] == len(rest) and r["skus"][0]["status"] == "Approved"
    again = ok(api.post(f"{CATALOG}/sessions/{sid}/approve", json={"sku_ids": [sku(ready, "R-001")["id"]]}))
    assert again["approved"] == 0
    assert api.post(f"{CATALOG}/sessions/{sid}/approve", json={}).status_code == 422
    assert api.post(f"{CATALOG}/sessions/{sid}/approve", json={"category": "rings", "sku_ids": [a["sku_id"]]}).status_code == 422


def test_regenerate_keeps_old_revision(api, ready):
    sid = ready["session"]["id"]
    old = images_of(detail(api, sid), "R-001")[0]
    r = ok(api.post(f"{CATALOG}/sessions/{sid}/regenerate", json={"image_ids": [old["id"]], "feedback": "ring looks too big"}), 202)
    assert r["run"]["scope"] == "regenerate" and r["run"]["feedback"] == "ring looks too big"
    (new,) = r["images"]
    assert (new["shot"], new["revision"], new["status"], new["feedback"]) == (old["shot"], 2, "generating", "ring looks too big")
    d = wait_idle(api, sid)
    revs = sorted((i for i in d["images"] if i["sku_id"] == old["sku_id"] and i["shot"] == old["shot"]), key=lambda i: i["revision"])
    assert [(i["revision"], i["status"]) for i in revs] == [(1, "approved"), (2, "to_review")]
    assert api.post(f"{CATALOG}/sessions/{sid}/regenerate", json={"image_ids": [old["id"]], "feedback": ""}).status_code == 422
    ok(api.post(f"{CATALOG}/sessions/{sid}/approve", json={"image_ids": [revs[1]["id"]]}))


def test_batch_then_typed_run_all(api, ready):
    sid = ready["session"]["id"]
    option = next_step(api, sid)["options"][0]
    d = run(api, sid, **{k: v for k, v in option["action"].items() if k != "type"})
    group = option["action"]["group_key"]
    batch = [k for k in d["skus"] if k["group_key"] == group and k["code"] != "R-001"]
    assert batch and all(k["status"] == "To review" for k in batch)
    card = next_step(api, sid)
    assert [o["label"] for o in card["options"]] == ["Run all Rings", "Run sample · Anklets"]

    runs = len(d["runs"])
    q = ok(api.post(f"{CATALOG}/sessions/{sid}/requests", json={"text": "run the rest of the rings"}))
    assert q["action"] == "run" and not q["update"].get("run") and q["update"]["quote"]["credits"] > 0
    assert "credits" in q["reply"] and len(detail(api, sid)["runs"]) == runs  # quoted first, nothing queued
    assert q["update"]["quote"]["worst_credits"] >= q["update"]["quote"]["credits"]
    stale = {"text": "run the rest of the rings", "confirm_credits": q["update"]["quote"]["credits"] + 1}
    assert ok(api.post(f"{CATALOG}/sessions/{sid}/requests", json=stale))["update"]["quote"] == q["update"]["quote"]
    r = ok(api.post(f"{CATALOG}/sessions/{sid}/requests",
                    json={"text": "run the rest of the rings", "confirm_credits": q["update"]["quote"]["credits"]}))
    assert r["action"] == "run" and r["update"]["run"]["scope"] == "all"
    assert r["update"]["run"]["credits_quoted"] == q["update"]["quote"]["credits"]
    assert len(r["update"]["images"]) == q["update"]["quote"]["images"]
    d = wait_idle(api, sid)
    assert len(d["runs"]) == runs + 1
    assert all(k["status"] != "Not run" for k in d["skus"] if k["category"] == "rings")
    again = ok(api.post(f"{CATALOG}/sessions/{sid}/requests", json={"text": "run the rest of the rings"}))
    assert again["action"] == "reply" and not again["update"].get("run")
    card = next_step(api, sid)
    assert card["heading"] == "Next step · Anklets"
    assert [o["label"] for o in card["options"]] == ["Run sample · Anklets", "Run sample · Necklaces & chains"]


def test_typed_requests(api, ready):
    sid = ready["session"]["id"]
    runs = len(detail(api, sid)["runs"])
    some = [i["id"] for i in images_of(detail(api, sid), "R-001")[:2]]
    for body in ({"text": "make me a video"}, {"text": "make me a video", "image_ids": some},
                 {"text": "what's the weather like?"}):
        r = ok(api.post(f"{CATALOG}/sessions/{sid}/requests", json=body))
        assert r["action"] == "not_understood" and r["reply"] and not r["update"].get("run"), body
    assert len(detail(api, sid)["runs"]) == runs  # nothing ran, nothing spent

    r = ok(api.post(f"{CATALOG}/sessions/{sid}/requests", json={"text": "show me the necklace poses"}))
    assert r["action"] == "open_pose_editor" and r["update"]["pose_editor"]["category"] == "necklaces"
    r = ok(api.post(f"{CATALOG}/sessions/{sid}/requests", json={"text": "approve the rings"}))
    assert r["action"] == "approve" and r["update"]["images"]
    d = detail(api, sid)
    assert all(k["status"] == "Approved" for k in d["skus"] if k["category"] == "rings")
    r = ok(api.post(f"{CATALOG}/sessions/{sid}/requests", json={"text": "approve the rings"}))
    assert r["action"] == "reply" and "waiting for approval" in r["reply"]

    target = images_of(d, "R-002")[0]
    body = {"text": "make the stone bigger", "image_ids": [target["id"]]}
    q = ok(api.post(f"{CATALOG}/sessions/{sid}/requests", json=body))
    assert q["action"] == "feedback" and q["update"]["quote"]["images"] == 1 and not q["update"].get("run")
    r = ok(api.post(f"{CATALOG}/sessions/{sid}/requests", json=body | {"confirm_credits": q["update"]["quote"]["credits"]}))
    assert r["action"] == "feedback" and r["update"]["run"]["scope"] == "regenerate"
    assert [i["revision"] for i in r["update"]["images"]] == [2]
    wait_idle(api, sid)
    assert api.post(f"{CATALOG}/sessions/{sid}/requests", json={"text": ""}).status_code == 422


def test_sku_override(api, ready, lib):
    n1 = sku(ready, "N-001")
    other = next(b["id"] for b in lib["backgrounds"] if b["id"] != n1["brief"]["background"])
    r = ok(api.patch(f"{CATALOG}/skus/{n1['id']}", json={"background": other, "apply_to_category": True}))
    assert {k["code"] for k in r["skus"]} == {"N-001", "N-002", "N-003", "N-004"}
    assert all(k["brief"]["background"] == other for k in r["skus"])
    r = ok(api.patch(f"{CATALOG}/skus/{n1['id']}", json={"poses": ["necklace-packshot"], "confirmed": True}))
    assert [k["code"] for k in r["skus"]] == ["N-001"] and r["skus"][0]["confirmed"]
    assert api.patch(f"{CATALOG}/skus/{n1['id']}", json={"model": "no-such-model"}).status_code == 422
    assert api.patch(f"{CATALOG}/skus/{n1['id']}", json={"poses": ["ring-packshot", "nope"]}).status_code == 422
    assert api.patch(f"{CATALOG}/skus/{n1['id']}", json={}).status_code == 422


def test_run_all_review_download(api, anon, ready):
    sid = ready["session"]["id"]
    assert api.post(f"{CATALOG}/sessions/{sid}/download").status_code == 200  # rings are approved already
    d = run(api, sid, scope="all")
    assert all(k["status"] in ("To review", "Approved") for k in d["skus"])
    assert api.post(f"{CATALOG}/sessions/{sid}/runs", json={"scope": "all"}).status_code == 409  # nothing left
    card = next_step(api, sid)
    assert (card["state"], card["options"]) == ("review", [])
    for category in ("rings", "necklaces", "anklets"):
        assert ok(api.post(f"{CATALOG}/sessions/{sid}/approve", json={"category": category}))["approved"] > 0
    card = next_step(api, sid)
    assert card["state"] == "done" and card["options"] == [
        {"label": "Download approved images", "action": {"type": "download"}, "credits": None, "worst_credits": None}]

    d = detail(api, sid)
    summary = next(s for s in ok(api.get(f"{CATALOG}/sessions"))["sessions"] if s["id"] == sid)
    live = live_images(d)
    approved = sum(i["status"] == "approved" for i in d["images"])  # every approved revision
    assert summary["counts"] == {"skus": 12, "not_run": 0, "running": 0, "to_review": 0, "approved": 12,
                                 "images_approved": approved}
    stats = ok(api.get(f"{CATALOG}/stats"))
    assert stats["sessions"] >= 1 and stats["skus"] >= 12 and stats["images_approved"] >= approved
    assert stats["credits_quoted"] >= sum(r["credits_quoted"] for r in d["runs"])

    dl = ok(api.post(f"{CATALOG}/sessions/{sid}/download"))
    assert (dl["file_name"], dl["skus"], dl["images"]) == ("catalog_approved.zip", 12, len(live))
    r = anon.get(dl["url"])
    assert r.status_code == 200, r.text
    zf = zipfile.ZipFile(io.BytesIO(r.content))
    codes = {k["id"]: k["code"] for k in d["skus"]}
    want = {f"{codes[s]}/{shot}.png" for (s, shot) in live}
    assert set(zf.namelist()) == want | {"catalog.csv"}
    rows = list(csv.DictReader(io.StringIO(zf.read("catalog.csv").decode())))
    assert {r["file"] for r in rows} == want and {r["sku"] for r in rows} == set(codes.values())
    assert zf.read(min(want))[:8] == b"\x89PNG\r\n\x1a\n"

    assert anon.get(dl["url"].replace("sig=", "sig=0")).status_code == 403
    assert anon.get(dl["url"].split("?")[0]).status_code == 403
    r = ok(api.post(f"{CATALOG}/sessions/{sid}/requests", json={"text": "download"}))
    assert r["action"] == "download" and r["update"]["download"]["skus"] == 12


def test_download_needs_approvals(api):
    assert api.post(f"{CATALOG}/sessions/{new_session(api)}/download").status_code == 409


def test_stop_run(api, api_b):
    d = started(api)
    sid = d["session"]["id"]
    run_id = ok(api.post(f"{CATALOG}/sessions/{sid}/runs", json={"scope": "all"}), 202)["run"]["id"]
    stop = f"{CATALOG}/sessions/{sid}/runs/{run_id}/stop"
    assert api_b.post(stop).status_code == 404
    assert api.post(f"{CATALOG}/sessions/{sid}/runs/{new_id()}/stop").status_code == 404
    r = api.post(stop)
    if r.status_code == 409:
        pytest.skip("the fake run finished before Stop reached it")
    stopped = ok(r)
    run_row = stopped["run"]
    assert (run_row["status"], run_row["error"]) == ("failed", "Stopped") and run_row["finished_at"]
    assert {k["id"] for k in stopped["skus"]} == set(run_row["sku_ids"])
    assert api.post(stop).status_code == 409
    d = wait_idle(api, sid)
    mine = [i for i in d["images"] if i["run_id"] == run_id]
    assert all(i["status"] != "generating" for i in mine)
    assert not {i["id"] for i in d["images"]} & set(stopped["removed_image_ids"])
    assert sorted(run_row["image_ids"]) == sorted(i["id"] for i in mine)
    assert all(k["status"] not in ("Queued", "Running") for k in d["skus"])
    events = ok(api.get(f"{CATALOG}/sessions/{sid}/events"))["events"]
    assert any(e["type"] == "status" and e["work_id"] == run_id and e["data"].get("error") == "Stopped" for e in events)


def test_stop_images(api, api_b):
    d = started(api)
    sid = d["session"]["id"]
    accepted = ok(api.post(f"{CATALOG}/sessions/{sid}/runs", json={"scope": "all"}), 202)
    run_id = accepted["run"]["id"]
    by_sku: dict[str, list[str]] = {}
    for img in accepted["images"]:
        by_sku.setdefault(img["sku_id"], []).append(img["id"])
    first, row = list(by_sku.values())[:2]
    stop = f"{CATALOG}/sessions/{sid}/images/stop"
    assert api_b.post(stop, json={"image_ids": first[:1]}).status_code == 404
    assert api.post(stop, json={"image_ids": [new_id()]}).status_code == 404
    r = api.post(stop, json={"image_ids": first[:1]})  # one tile
    if r.status_code == 409:
        pytest.skip("the fake run rendered that image before Stop reached it")
    one = ok(r)
    assert one["removed_image_ids"] == first[:1] and [x["id"] for x in one["runs"]] == [run_id]
    assert api.post(stop, json={"image_ids": first[:1]}).status_code == 409  # stopped already
    r = api.post(stop, json={"image_ids": row})  # a SKU row
    if r.status_code == 409:
        pytest.skip("the fake run rendered that SKU before Stop reached it")
    stopped = ok(r)
    if stopped["runs"][0]["status"] == "failed":
        pytest.skip("the fake run finished every other image before Stop reached it")
    d = wait_idle(api, sid)
    ids = {i["id"] for i in d["images"]}
    assert not ids & set(stopped["removed_image_ids"] + one["removed_image_ids"])
    run_row = next(x for x in d["runs"] if x["id"] == run_id)
    assert run_row["status"] == "succeeded" and set(run_row["image_ids"]) <= ids
    assert all(i["status"] != "generating" for i in d["images"])


def described(api, models: dict, backgrounds: dict) -> dict:
    """A started one-ring session whose models and backgrounds steps carry these items' fields (ids, text): GET
    /sessions/{id}."""
    sid = processed(api, names=["ring-01.png"], guideline=False)["session"]["id"]
    items = {step: {"items": [{"id": "x", "option": "library", "label": "x"} | item]}
             for step, item in (("models", models), ("backgrounds", backgrounds))}
    ok(api.patch(f"{CATALOG}/sessions/{sid}", json={"inputs": items}))
    answer_all(api, wait_processed(api, sid))  # the input change re-ran the checks
    ok(api.post(f"{CATALOG}/sessions/{sid}/start"))
    return detail(api, sid)


def test_a_described_model_and_background_alone_render_a_sample(api):
    d = described(api, {"ids": [], "text": " South Indian woman in her late 20s "},
                  {"ids": [], "text": "sunlit marble terrace with soft palm shadows"})
    brief = sku(d, "R-001")["brief"]
    assert brief["model"] is None and brief["model_description"] == "South Indian woman in her late 20s"
    assert brief["background"] is None and brief["background_description"] == "sunlit marble terrace with soft palm shadows"
    d = run(api, d["session"]["id"], scope="sample", category="rings")
    assert d["runs"][0]["status"] == "succeeded"
    assert all(i["status"] == "to_review" and i["url"] for i in images_of(d, "R-001"))


def test_a_picked_and_described_model_and_background_render_a_sample(api, lib):
    rings = next(c for c in lib["categories"] if c["id"] == "rings")
    d = described(api, {"ids": [rings["default_model"]], "text": "hair in a low bun"},
                  {"ids": [rings["default_background"]], "text": "warm dusk light"})
    brief = sku(d, "R-001")["brief"]
    assert (brief["model"], brief["model_description"]) == (rings["default_model"], "hair in a low bun")
    assert (brief["background"], brief["background_description"]) == (rings["default_background"], "warm dusk light")
    d = run(api, d["session"]["id"], scope="sample", category="rings")
    assert d["runs"][0]["status"] == "succeeded"
    assert all(i["status"] == "to_review" for i in images_of(d, "R-001"))


def test_a_models_or_backgrounds_item_needs_a_pick_or_a_description(api):
    sid = new_session(api)
    for step, item in (("models", {"ids": [], "text": "  "}), ("backgrounds", {"ids": [], "text": "x" * 501})):
        body = {"inputs": {step: {"items": [{"id": "x", "option": "library", "label": "x"} | item]}}}
        assert api.patch(f"{CATALOG}/sessions/{sid}", json=body).status_code == 422, item



def test_a_brief_shot_the_library_lacks_becomes_a_custom_shot_and_renders(api):
    sid = processed(api, names=["ring-01.png"], guideline=False)["session"]["id"]
    up = ok(api.post(f"{CATALOG}/sessions/{sid}/uploads", files={"file": ("swing.png", image(), "image/png")},
                     data={"kind": "pose_ref"}), 201)["id"]
    brief = {"category": "rings", "text": "one swing shot, like this photo", "upload_ids": [up]}
    s = ok(api.post(f"{CATALOG}/sessions/{sid}/poses/suggest", json={"briefs": [brief]}))
    [swing] = s["custom_poses"]
    assert swing["category"] == "rings" and s["poses"]["rings"][-1] == swing["id"] and s["refs"] == {up: swing["id"]}
    item = {"id": "b", "option": "brief", "label": "x", "poses": s["poses"], "custom_poses": s["custom_poses"],
            "pose_refs": s["refs"], "briefs": {"rings": brief["text"]}, "upload_ids": [up]}
    ok(api.patch(f"{CATALOG}/sessions/{sid}", json={"inputs": {"poses": {"items": [item]}}}))
    answer_all(api, wait_processed(api, sid))
    ok(api.post(f"{CATALOG}/sessions/{sid}/start"))
    b = sku(detail(api, sid), "R-001")["brief"]
    assert swing["id"] in b["poses"] and b["shots_brief"] == "one swing shot, like this photo"
    d = run(api, sid, scope="sample", category="rings")
    assert d["runs"][0]["status"] == "succeeded"
    [shot] = [i for i in images_of(d, "R-001") if i["shot"] == swing["id"]]
    assert shot["status"] == "to_review" and shot["url"]


def test_described_shots_set_the_exact_shots_and_their_number(api):
    sid = processed(api, names=["ring-01.png"], guideline=False)["session"]["id"]
    text = "3 shots: hand close-up, sitting on a swing, flat lay with dried flowers"
    s = ok(api.post(f"{CATALOG}/sessions/{sid}/poses/suggest",
                    json={"briefs": [{"category": "rings", "text": text, "exact": True}]}))
    assert len(s["poses"]["rings"]) == 3 and [c["name"] for c in s["custom_poses"]] == ["Sitting on a swing"]
    assert s["poses"]["rings"][1] == s["custom_poses"][0]["id"]
