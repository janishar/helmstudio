"""Jewellery raw-photo views and the main-image gate: raw photos named "<code>_White Side.png", "<code>_Model.png" …
are view-tagged (attributes.sides, raw_views); a run whose main image (the packshot) fails review reports the SKU's
other shots failed without rendering them, and so does a later run of them while that main image is live; every
jewellery product has a Mood pose outside its defaults. Fake mode fails a review whose brief carries "fake-reject" (here a pose note), the only way to make a fake review fail."""

from collections import Counter

from helpers import CATALOG, live_images, ok, processed, run, started, wait_idle

CODE = "A18000028839"
VIEWS = {"White Hero": "hero", "White Side": "side", "Model": "model", "Mood": "mood", "Closeup": "close-up",
         "Flat": "flat", "3": None}
NAMES = [f"{CODE}_{v}.png" for v in VIEWS]
GATED = "Main image didn't pass review — fix or regenerate it, then run again"


def test_every_jewellery_product_has_a_mood_pose_outside_its_defaults(lib):
    for c in lib["categories"]:
        moods = [p for p in lib["poses"] if p["category"] == c["id"] and p["name"] == "Mood"]
        if c["family"] != "jewellery":
            assert not moods, c["id"]
            continue
        assert len(moods) == 1 and moods[0]["example_url"], c["id"]
        assert moods[0]["id"] not in c["default_poses"], c["id"]


def test_raw_photos_get_view_tags_from_their_names(api):
    d = processed(api, names=NAMES, guideline=False, families=["jewellery"])
    [sku] = d["skus"]
    assert sku["category"] == "rings" and len(sku["raw_urls"]) == len(NAMES)
    assert Counter(sku["attributes"]["sides"].values()) == Counter(VIEWS.values())
    assert Counter(sku["raw_views"]) == Counter(VIEWS.values())


def test_a_failed_main_image_fails_the_skus_other_shots(api, lib):
    d = started(api, names=NAMES, guideline=False, families=["jewellery"])
    sid, [sku] = d["session"]["id"], d["skus"]
    packshot = next(p["id"] for p in lib["poses"] if p["category"] == "rings" and p["name"] == "Packshot")
    assert packshot in sku["brief"]["poses"] and len(sku["brief"]["poses"]) > 1
    ok(api.patch(f"{CATALOG}/skus/{sku['id']}", json={"pose_notes": {packshot: "fake-reject"}}))
    d = run(api, sid, scope="sample", category="rings")
    imgs = {shot: i for (k, shot), i in live_images(d).items() if k == sku["id"]}
    assert set(imgs) == set(sku["brief"]["poses"])
    main = imgs.pop(packshot)
    assert main["status"] == "to_review" and main["review"]["passed"] is False
    for shot, i in imgs.items():
        assert (i["status"], i["error"], i["url"], i["attempts"]) == ("failed", GATED, None, []), shot
    assert "main image hasn't passed review" in ok(api.get(f"{CATALOG}/sessions/{sid}/next-step"))["line"]
    assert [r["status"] for r in d["runs"]] == ["succeeded"]
    # re-running a skipped shot without the main image: the live main image still has not passed, so it is skipped again
    gated = next(iter(imgs.values()))
    ok(api.post(f"{CATALOG}/sessions/{sid}/regenerate", json={"image_ids": [gated["id"]], "feedback": "try again"}), 202)
    again = live_images(wait_idle(api, sid))[(sku["id"], gated["shot"])]
    assert again["revision"] == gated["revision"] + 1 and (again["status"], again["error"]) == ("failed", GATED)
