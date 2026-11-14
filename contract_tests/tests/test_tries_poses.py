"""Every render try kept and signed, per-pose ratios and notes, regenerating at another ratio, the pose noun per
family and the added ratios. Fake mode renders at the asked ratio, so an image's pixel size shows the ratio it got."""

import io
import time

import pytest
from helpers import CATALOG, detail, live_images, ok, run, started, wait_idle
from PIL import Image

NOUNS = {"jewellery": ("shot", "shots"), "furniture": ("angle", "angles"), "garments": ("shot", "shots")}


def size_of(anon, url: str) -> tuple[int, int]:
    """The pixel size of a signed image link (fetched without credentials)."""
    r = anon.get(url)
    assert r.status_code == 200, r.text
    with Image.open(io.BytesIO(r.content)) as im:
        return im.size


def ratio_of(size: tuple[int, int]) -> float:
    return size[0] / size[1]


def ratio_value(ratio: str) -> float:
    w, h = ratio.split(":")
    return int(w) / int(h)


@pytest.fixture(scope="module")
def mixed(api) -> dict:
    """A started session with one ring, one sofa and one kurti."""
    return started(api, names=["ring-01.png", "sofa-01.png", "kurti-01.png"], guideline=False)


def test_library_has_the_added_ratios_and_a_pose_noun_per_family(lib):
    ratios = {r["id"]: r for r in lib["ratios"]}
    assert {"4:3", "3:2", "5:4", "21:9"} <= set(ratios)
    assert all(ratios[i]["use"] for i in ("4:3", "3:2", "5:4", "21:9"))
    for c in lib["categories"]:
        assert (c["pose_noun"]["singular"], c["pose_noun"]["plural"]) == NOUNS[c["family"]], c["id"]


def test_pose_ratios_and_notes_round_trip(api, api_b, mixed, lib):
    ring = next(k for k in mixed["skus"] if k["code"] == "R-001")
    assert (ring["brief"]["pose_ratios"], ring["brief"]["pose_notes"]) == ({}, {})
    first, second = ring["brief"]["poses"][:2]
    url = f"{CATALOG}/skus/{ring['id']}"
    r = ok(api.patch(url, json={"pose_ratios": {first: "4:3"}, "pose_notes": {second: "top view, stone facing up"}}))
    (got,) = r["skus"]
    assert got["brief"]["pose_ratios"] == {first: "4:3"}
    assert got["brief"]["pose_notes"] == {second: "top view, stone facing up"}
    sofa_pose = next(p["id"] for p in lib["poses"] if p["category"] == "sofas")
    for bad in ({"pose_ratios": {first: "7:3"}}, {"pose_ratios": {first: "wide"}},
                {"pose_ratios": {sofa_pose: "4:3"}}, {"pose_notes": {first: "x" * 501}},
                {"pose_notes": {first: " \n "}}):
        assert api.patch(url, json=bad).status_code == 422, bad
    assert api_b.patch(url, json={"pose_notes": {first: "mine"}}).status_code == 404


def test_a_pose_renders_at_its_own_ratio_and_every_try_is_signed(api, api_b, anon, mixed):
    sid = mixed["session"]["id"]
    ring = next(k for k in detail(api, sid)["skus"] if k["code"] == "R-001")
    d = run(api, sid, scope="sample", category="rings")
    imgs = {i["shot"]: i for (s, _), i in live_images(d).items() if s == ring["id"]}
    assert set(imgs) == set(ring["brief"]["poses"])
    for shot, img in imgs.items():
        want = ring["brief"]["pose_ratios"].get(shot) or ring["brief"]["ratio"]
        assert abs(ratio_of(size_of(anon, img["url"])) - ratio_value(want)) < 0.01, (shot, want)
        assert img["status"] == "to_review" and img["attempts"], shot
        assert [a["index"] for a in img["attempts"]] == list(range(1, len(img["attempts"]) + 1))
        assert sum(a["best"] for a in img["attempts"]) == 1
        for a in img["attempts"]:
            assert f"/images/{img['id']}/attempt_{a['index']}." in a["url"]
            if a["best"]:
                assert anon.get(a["url"]).content == anon.get(img["url"]).content  # the best try is the image
            assert anon.get(a["url"].replace("sig=", "sig=0")).status_code == 403
            assert anon.get(a["url"].split("?")[0]).status_code == 403
    # cacheable: the same image keeps its link from one read to the next (expiry rounded to the UTC day) and is
    # served with a year-long private Cache-Control; a refused link is not cacheable
    again = {i["id"]: i["url"] for i in detail(api, sid)["images"]}
    img = next(iter(imgs.values()))
    assert again[img["id"]] == img["url"] or int(time.time()) % 86400 < 5
    assert anon.get(img["url"]).headers["cache-control"] == "private, max-age=31536000, immutable"
    assert "cache-control" not in anon.get(img["url"].replace("sig=", "sig=0")).headers
    assert api_b.get(f"{CATALOG}/sessions/{sid}").status_code == 404
    generating = [i for i in d["images"] if i["status"] == "generating"]
    assert all(i["attempts"] == [] for i in generating)


def test_regenerate_at_another_ratio(api, api_b, anon, mixed):
    sid = mixed["session"]["id"]
    ring = next(k for k in detail(api, sid)["skus"] if k["code"] == "R-001")
    old = next(i for (s, _), i in live_images(detail(api, sid)).items() if s == ring["id"])
    url = f"{CATALOG}/sessions/{sid}/regenerate"
    body = {"image_ids": [old["id"]], "feedback": "wider, please"}
    for bad in ("7:3", "wide"):
        assert api.post(url, json=body | {"ratio": bad}).status_code == 422, bad
    assert api_b.post(url, json=body | {"ratio": "21:9"}).status_code == 404
    (new,) = ok(api.post(url, json=body | {"ratio": "21:9"}), 202)["images"]
    assert (new["revision"], new["attempts"]) == (old["revision"] + 1, [])
    d = wait_idle(api, sid)
    new = next(i for i in d["images"] if i["id"] == new["id"])
    assert new["status"] == "to_review" and new["feedback"] == "wider, please" and new["attempts"]
    assert abs(ratio_of(size_of(anon, new["url"])) - 21 / 9) < 0.01
    still = next(i for i in d["images"] if i["id"] == old["id"])  # the old revision keeps its own tries
    assert still["attempts"] and still["url"]
    sku = next(k for k in d["skus"] if k["id"] == ring["id"])
    assert sku["brief"]["ratio"] == ring["brief"]["ratio"]  # the regenerate ratio is not written to the brief


def test_the_brand_keeps_another_try_as_the_image(api, api_b, anon):
    # fake mode: "fake-reject" in a pose note fails every review, so the shot renders every try it may
    d = started(api, names=["ring-01.png"], guideline=False)
    sid, (ring,) = d["session"]["id"], d["skus"]
    pose = ring["brief"]["poses"][0]
    ok(api.patch(f"{CATALOG}/skus/{ring['id']}", json={"pose_notes": {pose: "fake-reject"}}))
    d = run(api, sid, scope="sample", category="rings")
    img = next(i for (_, shot), i in live_images(d).items() if shot == pose)
    assert len(img["attempts"]) > 1
    # every try was stored as it rendered and keeps the reviewer's verdict (the tile showed each while generating)
    assert all(a["review"] and not a["review"]["passed"] and a["url"] for a in img["attempts"])
    other = next(a["index"] for a in img["attempts"] if not a["best"])  # one the reviewer did not keep
    url = f"{CATALOG}/images/{img['id']}/pick"
    assert api_b.post(url, json={"attempt": other}).status_code == 404
    assert api.post(url, json={"attempt": 99}).status_code == 422
    assert api.post(url, json={"attempt": 0}).status_code == 422
    got = ok(api.post(url, json={"attempt": other}))["image"]
    (best,) = [a for a in got["attempts"] if a["best"]]
    assert best["index"] == other and best["picked"]
    assert got["status"] == img["status"] and got["review"] == best["review"]
    assert anon.get(got["url"]).content == anon.get(best["url"]).content  # the picked try is the image
    stored = next(i for i in detail(api, sid)["images"] if i["id"] == img["id"])
    assert next(a for a in stored["attempts"] if a["index"] == other)["picked"]
