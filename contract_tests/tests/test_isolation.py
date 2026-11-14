"""Per-user isolation: user B gets a 404 on every id route of user A's objects, and sees none of them in lists."""

from helpers import CATALOG, image, ok, raw_files


def test_b_cannot_see_a(api, api_b, ready):
    a = ok(api.get(f"{CATALOG}/sessions/{ready['session']['id']}"))
    sid = a["session"]["id"]
    s = f"{CATALOG}/sessions/{sid}"
    sku_id = a["skus"][0]["id"]
    q_id = a["questions"][0]["id"]
    img = a["images"][0]["id"] if a["images"] else "00000000-0000-0000-0000-000000000000"
    calls = [
        ("GET", s, None),
        ("PATCH", s, {"json": {"project_name": "mine now"}}),
        ("POST", f"{s}/refresh", None),
        ("POST", f"{s}/reset", None),
        ("POST", f"{s}/raw", {"files": raw_files(["ring-01.png"])}),
        ("POST", f"{s}/raw", {"json": {"kind": "website", "url": "https://shop.example.com/"}}),
        ("POST", f"{s}/guidelines", {"json": {"url": "https://brand.example.com/"}}),
        ("POST", f"{s}/uploads", {"files": {"file": ("m.png", image(), "image/png")}, "data": {"kind": "model"}}),
        ("POST", f"{s}/poses/suggest", {"json": {"briefs": [{"category": "rings", "text": "hand"}]}}),
        ("GET", f"{s}/events", None),
        ("POST", f"{s}/start", None),
        ("GET", f"{s}/next-step", None),
        ("POST", f"{s}/runs", {"json": {"scope": "all"}}),
        ("POST", f"{s}/regenerate", {"json": {"image_ids": [img], "feedback": "darker"}}),
        ("POST", f"{s}/approve", {"json": {"category": "rings"}}),
        ("POST", f"{s}/requests", {"json": {"text": "approve the rings"}}),
        ("POST", f"{s}/download", None),
        ("POST", f"{CATALOG}/questions/{q_id}/answer", {"json": {"option": 1}}),
        ("PATCH", f"{CATALOG}/images/{img}", {"json": {"status": "rejected"}}),
        ("PATCH", f"{CATALOG}/skus/{sku_id}", {"json": {"confirmed": False}}),
    ]
    for method, path, kw in calls:
        r = api_b.request(method, path, **(kw or {}))
        assert r.status_code == 404, f"{method} {path}: {r.status_code} {r.text}"

    # B's own session can't reach A's objects either
    b_sid = ok(api_b.post(f"{CATALOG}/sessions"), 201)["session"]["id"]
    r = api_b.post(f"{CATALOG}/sessions/{b_sid}/regenerate", json={"image_ids": [img], "feedback": "darker"})
    assert r.status_code == 404
    r = api_b.post(f"{CATALOG}/sessions/{b_sid}/approve", json={"sku_ids": [sku_id]})
    assert r.status_code in (200, 404) and (r.status_code == 404 or r.json()["approved"] == 0)

    assert sid not in {x["id"] for x in ok(api_b.get(f"{CATALOG}/sessions"))["sessions"]}
    assert ok(api.get(s))["session"]["project_name"] == a["session"]["project_name"]  # nothing changed for A
