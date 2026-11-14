"""Products: the library's 34 products in 3 families, sorting raw photos into products (fake mode reads the file
names), the families filter, furniture rendered without a model, moving SKUs to another product, editing a SKU's
subcategory, per-product attempts, garment models with turnaround views, men's wear on men's models, intimates and
fabrics without a model, and the quality metrics by product."""

from helpers import CATALOG, detail, live_images, ok, processed, raw_files, run, started, wait_idle

PRODUCTS = [
    ("jewellery", "rings", "R"), ("jewellery", "mens-rings", "MR"), ("jewellery", "earrings", "E"),
    ("jewellery", "anklets", "A"), ("jewellery", "necklace-sets", "NS"), ("jewellery", "pendants", "PD"),
    ("jewellery", "necklaces", "N"), ("jewellery", "bracelets", "B"), ("jewellery", "bangles", "BG"),
    ("jewellery", "nose-pins", "NP"), ("jewellery", "head-jewellery", "HJ"), ("jewellery", "brooches", "BR"),
    ("jewellery", "rakhis", "RK"), ("jewellery", "coins", "CN"),
    ("furniture", "sofas", "SO"), ("furniture", "tv-units", "TV"), ("furniture", "side-tables", "ST"),
    ("furniture", "beds", "BD"), ("furniture", "seating", "CH"), ("furniture", "mattresses", "MT"),
    ("furniture", "dining", "DN"), ("furniture", "storage", "SG"), ("furniture", "office", "OF"),
    ("furniture", "outdoor", "OD"), ("furniture", "decor", "DC"), ("garments", "sets", "SE"), ("garments", "tops", "TP"),
    ("garments", "bottoms", "BT"), ("garments", "one-piece", "OP"), ("garments", "sarees", "SR"),
    ("garments", "layers", "LY"), ("garments", "drapes", "DR"), ("garments", "intimates", "IS"),
    ("garments", "fabrics", "FB"),
]
NO_MODEL = ("coins", "intimates", "fabrics")  # shot product-only (intimates on an invisible mannequin)
GOALS = {
    "jewellery": {"count", "motif", "finish", "placement", "background", "identity"},
    "garments": {"pieces", "colour", "print", "details", "drape", "background", "identity"},
    "furniture": {"shape", "material", "colour", "background", "extras"},
}
# one photo per product family member; the file name names the product
MIXED = ["ring-01.png", "earring-01.png", "sofa-01.png", "tv-unit-01.png", "kurta-pant-01.png",
         "kurti-01.png"]


def test_library_lists_products_by_family(lib):
    assert [f["id"] for f in lib["families"]] == ["jewellery", "furniture", "garments"]
    assert [(c["family"], c["id"], c["code_prefix"]) for c in lib["categories"]] == PRODUCTS
    for c in lib["categories"]:
        no_model = c["family"] == "furniture" or c["id"] in NO_MODEL
        assert c["uses_model"] is not no_model and (c["default_model"] is None) is no_model, c["id"]
        poses = {p["id"] for p in lib["poses"] if p["category"] == c["id"]}
        assert c["default_poses"] and set(c["default_poses"]) <= poses, c["id"]
    bgs = {b["id"]: b for b in lib["backgrounds"]}
    sofas = next(c for c in lib["categories"] if c["id"] == "sofas")
    assert "furniture" in bgs[sofas["default_background"]].get("families", ["furniture"])


def test_raw_photos_sort_into_products(api):
    d = processed(api, names=MIXED, guideline=False)
    got = {k["code"]: k for k in d["skus"]}
    assert {c: k["category"] for c, k in got.items()} == {
        "R-001": "rings", "E-001": "earrings", "SO-001": "sofas", "TV-001": "tv-units",
        "SE-001": "sets", "TP-001": "tops"}
    assert "counts" in got["R-001"]["attributes"] and "counts" not in got["SO-001"]["attributes"]
    assert "pieces" in got["SE-001"]["attributes"] and "dimensions_cm" in got["SO-001"]["attributes"]


def test_families_filter_narrows_products(api, lib):
    family = {c["id"]: c["family"] for c in lib["categories"]}
    d = processed(api, names=["ring-01.png", "photo-01.png", "photo-02.png"], guideline=False, families=["furniture"])
    assert d["skus"] and {family[k["category"]] for k in d["skus"]} == {"furniture"}
    sid = d["session"]["id"]
    bad = api.post(f"{CATALOG}/sessions/{sid}/raw", files=raw_files(["x.png"]), data={"families": ["hats"]})
    assert bad.status_code == 422


def test_furniture_has_no_model_and_renders(api):
    d = started(api, names=["ring-01.png", "sofa-01.png"], guideline=False)
    sid, sofa = d["session"]["id"], next(k for k in d["skus"] if k["code"] == "SO-001")
    ring = next(k for k in d["skus"] if k["code"] == "R-001")
    assert sofa["brief"]["model"] is None and ring["brief"]["model"]
    assert sofa["brief"]["background"] and sofa["brief"]["poses"]
    assert ok(api.get(f"{CATALOG}/sessions/{sid}/next-step"))["heading"] == "Next step · Rings"
    d = run(api, sid, scope="sample", category="sofas")
    imgs = [i for (k, _), i in live_images(d).items() if k == sofa["id"]]
    assert sorted(i["shot"] for i in imgs) == sorted(sofa["brief"]["poses"])
    for i in imgs:
        assert i["status"] == "to_review" and i["url"]
        assert {g["goal"] for g in i["review"]["goals"]} <= GOALS["furniture"]


def test_move_skus_to_another_product(api, lib):
    d = started(api, names=["ring-01.png", "ring-02.png"], guideline=False)
    sid, r1 = d["session"]["id"], next(k for k in d["skus"] if k["code"] == "R-001")
    sofas = next(c for c in lib["categories"] if c["id"] == "sofas")
    url = f"{CATALOG}/skus/{r1['id']}"
    assert api.patch(url, json={"category": "hats"}).status_code == 422
    assert api.patch(url, json={"category": "sofas", "model": "m001"}).status_code == 422
    ok(api.patch(url, json={"category": "sofas", "apply_to_category": True}))
    skus = detail(api, sid)["skus"]
    assert sorted(k["code"] for k in skus) == ["R-001", "R-002"]  # codes are kept
    for k in skus:
        assert k["category"] == "sofas" and k["brief"]["model"] is None
        assert k["brief"]["poses"] == sofas["default_poses"]
    assert api.patch(url, json={"model": "m001"}).status_code == 422  # a sofa takes no model
    ok(api.patch(url, json={"category": "rings"}))
    back = next(k for k in detail(api, sid)["skus"] if k["id"] == r1["id"])
    assert back["category"] == "rings" and back["brief"]["model"]


def test_moved_sku_leaves_another_familys_background(api, lib):
    d = started(api, names=["kurti-01.png"], guideline=False)
    (kurti,) = d["skus"]
    bgs = {b["id"]: b for b in lib["backgrounds"]}
    assert bgs[kurti["brief"]["background"]].get("families") == ["garments"]  # the garment studio
    sofas = next(c for c in lib["categories"] if c["id"] == "sofas")
    r = ok(api.patch(f"{CATALOG}/skus/{kurti['id']}", json={"category": "sofas"}))
    moved = next(k for k in r["skus"] if k["id"] == kurti["id"])
    assert moved["brief"]["background"] == sofas["default_background"] and moved["code"] == "TP-001"


def test_edit_sku_subcategory(api, api_b, lib):
    d = started(api, names=["kurti-01.png"], guideline=False)
    (top,) = d["skus"]
    subs = {c["id"]: [s["id"] for s in c["subcategories"]] for c in lib["categories"]}
    assert top["category"] == "tops" and subs["tops"] and subs["sets"]
    url = f"{CATALOG}/skus/{top['id']}"
    (got,) = ok(api.patch(url, json={"subcategory": subs["tops"][-1]}))["skus"]
    assert got["subcategory"] == subs["tops"][-1]
    (stored,) = detail(api, d["session"]["id"])["skus"]
    assert stored["subcategory"] == got["subcategory"]
    for bad in ("hats", subs["sets"][0], "Not An Id", 3):  # unknown, another product's, not an id
        assert api.patch(url, json={"subcategory": bad}).status_code == 422, bad
    assert api_b.patch(url, json={"subcategory": None}).status_code == 404
    (got,) = ok(api.patch(url, json={"subcategory": None}))["skus"]
    assert got["subcategory"] is None  # null clears it
    ok(api.patch(url, json={"subcategory": subs["tops"][0]}))
    (got,) = ok(api.patch(url, json={"category": "sets"}))["skus"]
    assert got["category"] == "sets" and got["subcategory"] is None  # a move without it clears it
    (got,) = ok(api.patch(url, json={"category": "tops", "subcategory": subs["tops"][0]}))["skus"]
    assert (got["category"], got["subcategory"]) == ("tops", subs["tops"][0])  # one of the target product


def test_subcategory_never_spreads_with_apply_to_category(api, lib):
    d = started(api, names=["kurti-01.png", "kurti-02.png"], guideline=False)
    one, two = sorted(d["skus"], key=lambda k: k["code"])
    subs = next([s["id"] for s in c["subcategories"]] for c in lib["categories"] if c["id"] == "tops")
    pick = next(s for s in subs if s not in (one["subcategory"], two["subcategory"]))
    body = {"subcategory": pick, "model": one["brief"]["model"], "apply_to_category": True}
    ok(api.patch(f"{CATALOG}/skus/{one['id']}", json=body))
    got = {k["id"]: k for k in detail(api, d["session"]["id"])["skus"]}
    assert got[one["id"]]["subcategory"] == pick and got[two["id"]]["subcategory"] == two["subcategory"]


def test_kind_question_moves_the_sku(api):
    def kind_question(name: str) -> tuple[str, dict, dict]:
        """A processed session with one raw photo `name`: its id, its one SKU and that SKU's open kind question."""
        d = processed(api, names=[name], guideline=False)
        (sku,) = d["skus"]
        (q,) = [q for q in d["questions"] if q["check"] == "kind"]
        assert q["answer"] is None and q["sku_ids"] == [sku["id"]]
        return d["session"]["id"], sku, q

    # fake mode: "kindcheck" in a file name is a garment that may be a kurta set; the second option moves it
    _, sku, q = kind_question("kurta-kindcheck-01.png")
    assert q["options"][1] == "Sets › Kurta Sets"
    (moved,) = ok(api.post(f"{CATALOG}/questions/{q['id']}/answer", json={"option": 1}))["skus"]
    assert (moved["id"], moved["code"]) == (sku["id"], sku["code"])  # code and SKU stay
    assert (moved["category"], moved["subcategory"]) == ("sets", "kurta-sets")
    # "kindother": a kind we don't shoot; the first option keeps it where it is
    sid, sku, q = kind_question("top-kindother-01.png")
    ok(api.post(f"{CATALOG}/questions/{q['id']}/answer", json={"option": 0}))
    (kept,) = detail(api, sid)["skus"]
    assert (kept["category"], kept["code"]) == (sku["category"], sku["code"])
    # jewellery too: an earring that may be a diamond ring moves to Rings › Diamond rings
    _, sku, q = kind_question("earring-kindcheck-01.png")
    assert q["options"] == ["Earrings › Earrings", "Rings › Diamond rings"]
    (moved,) = ok(api.post(f"{CATALOG}/questions/{q['id']}/answer", json={"option": 1}))["skus"]
    assert (moved["code"], moved["category"], moved["subcategory"]) == (sku["code"], "rings", "diamond-rings")


def test_per_product_max_attempts(lib):
    res = {r["id"] for r in lib["resolutions"]}
    for c in lib["categories"]:
        # every product follows MAX_REGENERATIONS (none overrides max_attempts today)
        assert c["max_attempts"] == lib["max_attempts"] and c["default_resolution"] in res, c["id"]


def test_garment_models_have_turnaround_views(lib):
    models = {m["id"]: m for m in lib["models"]}
    assert len(models) == 22
    assert [models[f"m0{n}"]["name"] for n in range(11, 17)] == ["Melo", "Nalu", "Anzi", "Sefi", "Duva", "Veli"]
    assert [models[f"m0{n}"]["name"] for n in range(17, 23)] == ["Arjun", "Kabir", "Rohan", "Vikram", "Dev", "Imran"]
    assert {i for i, m in models.items() if m.get("wearer") == "men"} == {f"m0{n}" for n in range(17, 23)}
    full = {i for i, m in models.items() if m.get("full_body_url")}
    assert full == set(models) - {"m004", "m007"}
    garments = [c for c in lib["categories"] if c["family"] == "garments"]
    for c in (c for c in garments if c["uses_model"]):  # the default models have a turnaround sheet
        assert c["default_model"] in full and models[c["default_model"]]["wearer"] == "women", c["id"]
        assert c["default_men_model"] in full and models[c["default_men_model"]]["wearer"] == "men", c["id"]
    ids = {c["id"] for c in garments}
    shown = [p["example_url"] for p in lib["poses"] if p["category"] in ids and p["example_url"]]
    assert {u.split("/")[3] for u in shown if u.startswith("/catalog/ora-models/")} >= {"m002", "m005", "m009", "m012"}


def test_mens_wear_gets_a_mens_model_and_the_drawer_can_change_the_wear(api, lib):
    models = {m["id"]: m for m in lib["models"]}
    d = started(api, names=["mens-kurta-01.png", "kurti-01.png"], guideline=False)
    men, women = sorted(d["skus"], key=lambda k: k["attributes"].get("wearer") != "men")
    assert men["attributes"]["wearer"] == "men" and women["attributes"].get("wearer") is None
    assert models[men["brief"]["model"]]["wearer"] == "men"  # fake mode: "mens" in the file name
    assert models[women["brief"]["model"]]["wearer"] == "women"
    url = f"{CATALOG}/skus/{men['id']}"
    assert api.patch(url, json={"wearer": "kids"}).status_code == 422
    (got,) = ok(api.patch(url, json={"wearer": "women"}))["skus"]
    assert got["attributes"]["wearer"] == "women" and models[got["brief"]["model"]]["wearer"] == "women"
    cats = {c["id"]: c for c in lib["categories"]}
    assert (cats["mens-rings"]["wearer"], cats["rakhis"]["wearer"], cats["rings"].get("wearer")) == ("men", "men", None)
    (ring,) = started(api, names=["ring-01.png"], guideline=False)["skus"]
    assert models[ring["brief"]["model"]]["wearer"] == "women"
    (moved,) = ok(api.patch(f"{CATALOG}/skus/{ring['id']}", json={"category": "mens-rings"}))["skus"]
    assert moved["attributes"]["wearer"] == "men" and models[moved["brief"]["model"]]["wearer"] == "men"


def test_intimates_and_fabrics_are_shot_without_a_model(api, lib):
    d = started(api, names=["bra-01.png", "dress-material-01.png"], guideline=False)
    got = {k["category"]: k for k in d["skus"]}
    assert set(got) == {"intimates", "fabrics"}
    cats = {c["id"]: c for c in lib["categories"]}
    for cat, sku in got.items():
        assert sku["brief"]["model"] is None and sku["brief"]["poses"] == cats[cat]["default_poses"]
        assert api.patch(f"{CATALOG}/skus/{sku['id']}", json={"model": "m001"}).status_code == 422
    assert [p["name"] for p in lib["poses"] if p["category"] == "intimates"][:3] == [
        "Ghost front", "Ghost back", "Ghost three-quarter"]


def test_metrics_by_product(api, api_b):
    d = started(api, names=["ring-01.png", "sofa-01.png"], guideline=False)
    sid, url = d["session"]["id"], f"{CATALOG}/metrics"
    assert ok(api.get(url, params={"session_id": sid}))["products"] == []
    run(api, sid, scope="sample", category="rings")
    d = run(api, sid, scope="sample", category="sofas")
    skus = {k["id"]: k["code"] for k in d["skus"]}
    rings = [i for (k, _), i in live_images(d).items() if skus[k] == "R-001"]
    sofas = [i for (k, _), i in live_images(d).items() if skus[k] == "SO-001"]
    ok(api.patch(f"{CATALOG}/images/{rings[0]['id']}", json={"status": "approved"}))
    ok(api.patch(f"{CATALOG}/images/{rings[1]['id']}", json={"status": "rejected"}))
    ok(api.post(f"{CATALOG}/sessions/{sid}/regenerate", json={"image_ids": [rings[1]["id"]], "feedback": "too dark"}), 202)
    wait_idle(api, sid)
    m = {p["product"]: p for p in ok(api.get(url, params={"session_id": sid}))["products"]}
    assert list(m) == ["rings", "sofas"]
    r, s = m["rings"], m["sofas"]
    assert (r["images"], r["approved"], r["rejected"], r["feedback_count"]) == (len(rings) + 1, 1, 1, 1)
    assert (r["first_pass_rate"], r["avg_tries"]) == (1, 1)  # fake renders pass review on the first try
    assert r["credits_per_approved"] > 0
    assert (s["images"], s["approved"], s["credits_per_approved"]) == (len(sofas), 0, None)
    assert {"rings", "sofas"} <= {p["product"] for p in ok(api.get(url))["products"]}
    ring = next(k for k in d["skus"] if k["code"] == "R-001")
    ok(api.patch(f"{CATALOG}/skus/{ring['id']}", json={"category": "necklaces"}))
    moved = {p["product"]: p for p in ok(api.get(url, params={"session_id": sid}))["products"]}
    assert moved["rings"]["images"] == r["images"] and "necklaces" not in moved  # history stays with its product
    assert ok(api_b.get(url, params={"session_id": sid}))["products"] == []  # another user's session
    assert api.get(url, params={"session_id": "nope"}).status_code == 422


def test_furniture_sorts_into_its_groups_and_types_with_a_tip(api, lib):
    d = processed(api, names=["shoe-rack-01.png", "dining-table-set-4-seater-01.png", "l-shape-sofa-01.png"],
                  guideline=False)
    got = {k["category"]: k["subcategory"] for k in d["skus"]}
    assert got == {"storage": "shoe-racks", "dining": "dining-sets", "sofas": "sectional-sofas"}
    tables = next(c for c in lib["categories"] if c["id"] == "side-tables")
    assert tables["name"] == "Tables" and tables["code_prefix"] == "ST"
    assert all(s["about"] for c in lib["categories"] if c["family"] == "furniture" for s in c["subcategories"])
