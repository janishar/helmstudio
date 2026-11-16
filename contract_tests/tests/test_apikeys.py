"""Application API keys (Admin › API keys, docs/mock/ApiKeys.dc.html; contracts/README.md "Hosts admin: API keys"):
an admin creates one (shown once), the list never carries a key, the key works through Kong, a revoked one is 401.
A non-admin's 403 is in test_admin_users.py."""

import re
import uuid

from conftest import make_client
from helpers import ok, wait_for

KEY = re.compile(r"^helm_[A-Za-z0-9_-]{43}$")


def listed(admin, key_id: str) -> dict:
    keys = ok(admin.get("/auth/admin/apikeys"))["api_keys"]
    assert all("key" not in k for k in keys), "a listed key carries its raw key"
    (k,) = [k for k in keys if k["id"] == key_id]
    return k


def test_create_list_use_revoke(admin, admin_session):
    client = f"contract-{uuid.uuid4().hex[:10]}"
    r = admin.post("/auth/admin/apikeys", json={"client": client})
    created = ok(r, 201)
    assert "no-store" in r.headers.get("cache-control", ""), r.headers
    assert KEY.fullmatch(created["key"]) and created["prefix"] == created["key"][:12], created
    assert (created["client"], created["revoked_at"]) == (client, None), created
    k = listed(admin, created["id"])
    assert (k["client"], k["prefix"], k["revoked_at"]) == (client, created["prefix"], None), k

    with make_client(
        {"X-API-Key": created["key"], "Authorization": f"Bearer {admin_session['access_token']}"}
    ) as c:
        assert (
            ok(c.get("/auth/me"))["id"] == admin_session["user"]["id"]
        )  # through Kong with the new key
        assert admin.delete(f"/auth/admin/apikeys/{created['id']}").status_code == 204
        assert listed(admin, created["id"])["revoked_at"] is not None
        wait_for(
            lambda: c.get("/auth/me").status_code == 401,
            timeout=60,
            interval=1,
            what="the revoked key refused",
        )
    assert (
        admin.delete(f"/auth/admin/apikeys/{created['id']}").status_code == 404
    )  # already revoked
    assert admin.delete(f"/auth/admin/apikeys/{uuid.uuid4()}").status_code == 404
    assert admin.post("/auth/admin/apikeys", json={"client": "Not A Client!"}).status_code == 422
