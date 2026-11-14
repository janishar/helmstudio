"""Per-user isolation: user B gets a 404 for every id of user A's, sees none of them in lists, reads A's output
file only through the signed link A was given, and cannot upload under A's prefix."""

from urllib.parse import parse_qs, urlsplit

from helpers import STUDIO, all_takes, ok, png_bytes


def test_b_gets_404_for_a_ids(api, api_b, echo_done):
    sid, rid = echo_done["session"]["id"], echo_done["run"]["id"]
    calls = [
        ("GET", f"{STUDIO}/sessions/{sid}", None),
        ("PATCH", f"{STUDIO}/sessions/{sid}", {"title": "mine now"}),
        ("DELETE", f"{STUDIO}/sessions/{sid}", None),
        ("GET", f"{STUDIO}/sessions/{sid}/runs", None),
        ("POST", f"{STUDIO}/sessions/{sid}/runs", {"workflow_id": "echo"}),
        ("GET", f"{STUDIO}/sessions/{sid}/events", None),
        ("GET", f"{STUDIO}/runs/{rid}", None),
        ("POST", f"{STUDIO}/runs/{rid}/cancel", None),  # 404, not the 409 of an ended run
    ]
    for method, path, body in calls:
        r = api_b.request(method, path, json=body)
        assert r.status_code == 404, f"{method} {path}: {r.status_code} {r.text}"
    r = api_b.get(f"{STUDIO}/sessions/{sid}/events", headers={"Accept": "text/event-stream"})
    assert r.status_code == 404

    assert sid not in {s["id"] for s in ok(api_b.get(f"{STUDIO}/sessions"))["sessions"]}
    a = ok(api.get(f"{STUDIO}/sessions/{sid}"))
    assert a == echo_done["session"]  # nothing changed for A
    assert ok(api.get(f"{STUDIO}/runs/{rid}"))["status"] == "succeeded"


def test_output_file_needs_its_signed_link(anon, echo_done):
    (out,) = [e["data"] for e in echo_done["events"] if e["type"] == "output"]
    link = urlsplit(out["url"])
    q = {k: v[0] for k, v in parse_qs(link.query).items()}
    assert link.path == f"/files/{out['key']}" and {"exp", "sig"} <= set(q)
    tampered_sig = q["sig"][:-1] + ("0" if q["sig"][-1] != "0" else "1")
    for params in (
        {},
        {"exp": q["exp"]},
        {"exp": q["exp"], "sig": tampered_sig},
        {"exp": str(int(q["exp"]) + 60), "sig": q["sig"]},
    ):
        assert anon.get(link.path, params=params).status_code == 403, params
    other = f"/files/{out['key'].rsplit('/', 1)[0]}/1.png"  # a sibling key with A's signature
    assert anon.get(other, params={"exp": q["exp"], "sig": q["sig"]}).status_code == 403
    assert anon.get(link.path, params=q).status_code == 200


def _upload(client, data: bytes) -> dict:
    body = {"filename": "mine.png", "content_type": "image/png", "bytes": len(data)}
    return ok(client.post(f"{STUDIO}/uploads", json=body), 201)


def test_b_gets_404_for_a_takes(api, api_b, echo_done):
    (out,) = ok(api.get(f"{STUDIO}/runs/{echo_done['run']['id']}"))["outputs"]
    take = ok(api.get(f"{STUDIO}/takes/{out['take_id']}"))
    pending = _upload(api, png_bytes(4, 4))["take_id"]  # A's upload, not completed
    sid = echo_done["session"]["id"]
    for tid in (take["id"], pending):
        for method, path, body in (
            ("GET", f"{STUDIO}/takes/{tid}", None),
            ("PATCH", f"{STUDIO}/takes/{tid}", {"starred": True}),
            ("DELETE", f"{STUDIO}/takes/{tid}", None),
            ("POST", f"{STUDIO}/takes/{tid}/reuse", None),
            ("POST", f"{STUDIO}/uploads/{tid}/complete", None),
        ):
            r = api_b.request(method, path, json=body)
            assert r.status_code == 404, f"{method} {path}: {r.status_code} {r.text}"
    assert api_b.get(f"{STUDIO}/sessions/{sid}/takes").status_code == 404
    assert ok(api_b.get(f"{STUDIO}/takes", params={"session_id": sid}))["takes"] == []
    seen = {t["id"] for t in all_takes(api_b, limit=200)}
    seen |= {t["id"] for t in all_takes(api_b, q="a red fox", limit=200)}
    assert take["id"] not in seen and pending not in seen
    assert ok(api.get(f"{STUDIO}/takes/{take['id']}")) == take  # nothing changed for A
    assert api.post(f"{STUDIO}/uploads/{pending}/complete").status_code == 409  # still A's


def test_b_cannot_put_under_a_prefix(api, api_b, anon, user_a):
    data = png_bytes(4, 4)
    a_owner = user_a["user"]["id"]
    a_up = _upload(api, data)
    b_link = urlsplit(_upload(api_b, data)["upload_url"])
    b_owner = b_link.path.split("/")[3]  # /files/u/<owner>/uploads/<take_id>.png
    assert b_owner != a_owner
    q = {k: v[0] for k, v in parse_qs(b_link.query).items()}
    a_key_path = urlsplit(a_up["upload_url"]).path
    for path in (b_link.path.replace(f"/u/{b_owner}/", f"/u/{a_owner}/"), a_key_path):
        r = anon.put(path, params=q, content=data, headers={"Content-Type": "image/png"})
        assert r.status_code == 403, f"{path}: {r.status_code} {r.text}"
    # A's upload is untouched: still not received
    assert api.post(f"{STUDIO}/uploads/{a_up['take_id']}/complete").status_code == 409
