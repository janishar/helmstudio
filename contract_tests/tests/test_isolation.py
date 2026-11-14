"""Per-user isolation: user B gets a 404 for every id of user A's, sees none of them in lists, and reads A's output
file only through the signed link A was given."""

from urllib.parse import parse_qs, urlsplit

from helpers import STUDIO, ok


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
