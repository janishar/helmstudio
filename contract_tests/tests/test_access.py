"""Closed beta access: no public sign-up, only the admin creates accounts (never another admin), and the .env
admin's own account can't be changed through the API."""

import uuid

import httpx
from conftest import BASE_URL, admin_token, headers, make_client
from helpers import ok


def test_signup_is_closed():
    body = {"email": f"public-{uuid.uuid4().hex[:8]}@example.com", "password": "a long passphrase", "name": "Pub"}
    with httpx.Client(base_url=BASE_URL, headers=headers()) as c:  # no schema check: the route is out of the contract
        assert c.post("/auth/signup", json=body).status_code == 404


def test_admin_creates_accounts(api):
    email, password = f"tester-{uuid.uuid4().hex[:8]}@example.com", f"Tester-{uuid.uuid4().hex}"
    with make_client(headers(admin_token())) as admin:
        made = ok(admin.post("/auth/admin/users", json={"email": email, "password": password, "roles": ["user", "dev"]}), 201)
        assert (made["email"], made["name"], made["roles"]) == (email, email.split("@")[0], ["dev", "user"])
        assert admin.post("/auth/admin/users", json={"email": email, "password": password, "roles": ["user"]}).status_code == 409
        r = admin.post("/auth/admin/users", json={"email": "x" + email, "password": password, "roles": ["admin"]})
        assert r.status_code == 422 and "ADMIN_EMAIL" in r.json()["detail"]
        me = ok(admin.get("/auth/me"))
        r = admin.put(f"/auth/admin/users/{me['id']}/disabled", json={"disabled": True})
        assert r.status_code == 409  # the .env admin can't be disabled, re-roled or re-passworded here
        assert email in {u["email"] for u in ok(admin.get("/auth/admin/users"))["users"]}
    with make_client(headers()) as c:
        assert c.post("/auth/signin", json={"email": email, "password": password}).status_code == 200
    assert api.post("/auth/admin/users", json={"email": "y" + email, "password": password, "roles": ["user"]}).status_code == 403
