"""The root admin manages accounts: create, roles, disable, reset password. admin is never granted, the root account
is never changed through the API, and nobody else may call /auth/admin/*."""

import uuid

from conftest import headers, make_client, sign_in
from helpers import NO_SUCH_ID, STUDIO, limited, ok, signin, wait_for


def creds() -> tuple[str, str]:
    return f"made-{uuid.uuid4().hex[:10]}@example.com", f"Made-{uuid.uuid4().hex}"


def test_create_user(admin):
    email, password = creds()
    made = ok(
        admin.post(
            "/auth/admin/users",
            json={"email": f"  {email.upper()} ", "password": password, "roles": ["user", "dev"]},
        ),
        201,
    )
    assert (made["email"], made["name"], made["roles"], made["disabled"]) == (
        email,
        email.split("@")[0],
        ["dev", "user"],
        False,
    )
    assert made["id"] in {u["id"] for u in ok(admin.get("/auth/admin/users"))["users"]}
    assert sign_in(email, password)["user"]["id"] == made["id"]

    r = admin.post(
        "/auth/admin/users", json={"email": email, "password": password, "roles": ["user"]}
    )
    assert r.status_code == 409
    bad = [
        {"email": "x" + email, "password": password, "roles": ["admin"]},  # admin is never granted
        {"email": "x" + email, "password": password, "roles": []},
        {"email": "not an email", "password": password, "roles": ["user"]},
        {"email": "x" + email, "password": "short", "roles": ["user"]},
    ]
    for body in bad:
        assert admin.post("/auth/admin/users", json=body).status_code == 422, body


def test_set_roles_revokes_sessions(admin, new_user):
    u = new_user("Roles")
    uid = u["user"]["id"]
    assert ok(admin.put(f"/auth/admin/users/{uid}/roles", json={"roles": ["dev", "user", "dev"]}))[
        "roles"
    ] == ["dev", "user"]
    assert admin.put(f"/auth/admin/users/{uid}/roles", json={"roles": ["admin"]}).status_code == 422
    assert admin.put(f"/auth/admin/users/{uid}/roles", json={"roles": []}).status_code == 422
    with make_client(headers(u["access_token"])) as c:
        assert c.get("/auth/me").status_code == 401  # new tokens carry the new roles
    assert sign_in(u["email"], u["password"])["user"]["roles"] == ["dev", "user"]


def test_disable_revokes_sessions_and_enable(admin, new_user, no_token):
    u = new_user("Disabled")
    uid = u["user"]["id"]
    with make_client(headers(u["access_token"])) as c:
        ok(c.get(f"{STUDIO}/sessions"))
        assert (
            ok(admin.put(f"/auth/admin/users/{uid}/disabled", json={"disabled": True}))["disabled"]
            is True
        )
        assert c.get("/auth/me").status_code == 401
        wait_for(
            lambda: c.get(f"{STUDIO}/sessions").status_code == 401,
            timeout=5,
            what="studio to see the disable",
        )
    assert (
        limited(no_token, "/auth/refresh", {"refresh_token": u["refresh_token"]}).status_code == 401
    )
    assert signin(no_token, u["email"], u["password"]).status_code == 401
    assert (
        ok(admin.put(f"/auth/admin/users/{uid}/disabled", json={"disabled": False}))["disabled"]
        is False
    )
    assert sign_in(u["email"], u["password"])["user"]["id"] == uid


def test_reset_password(admin, new_user, no_token):
    u = new_user("Reset")
    uid, password = u["user"]["id"], f"Reset-{uuid.uuid4().hex}"
    assert (
        admin.put(f"/auth/admin/users/{uid}/password", json={"password": "short"}).status_code
        == 422
    )
    assert (
        admin.put(f"/auth/admin/users/{uid}/password", json={"password": password}).status_code
        == 204
    )
    with make_client(headers(u["access_token"])) as c:
        assert c.get("/auth/me").status_code == 401
    assert signin(no_token, u["email"], u["password"]).status_code == 401
    assert sign_in(u["email"], password)["user"]["id"] == uid


def test_root_admin_cannot_be_changed(admin, admin_session):
    me = ok(admin.get("/auth/me"))
    assert "admin" in me["roles"] and me["id"] == admin_session["user"]["id"]
    for path, body in (
        ("disabled", {"disabled": True}),
        ("roles", {"roles": ["user"]}),
        ("password", {"password": f"Other-{uuid.uuid4().hex}"}),
    ):
        assert admin.put(f"/auth/admin/users/{me['id']}/{path}", json=body).status_code == 409, path


def test_unknown_user(admin):
    for uid in (NO_SUCH_ID, "not-a-uuid"):
        assert (
            admin.put(f"/auth/admin/users/{uid}/disabled", json={"disabled": True}).status_code
            == 404
        )
        assert (
            admin.put(f"/auth/admin/users/{uid}/roles", json={"roles": ["user"]}).status_code == 404
        )


def test_non_admin_is_forbidden(api, user_a):
    uid = user_a["user"]["id"]
    email, password = creds()
    calls = [
        ("GET", "/auth/admin/users", None),
        ("POST", "/auth/admin/users", {"email": email, "password": password, "roles": ["user"]}),
        ("PUT", f"/auth/admin/users/{uid}/roles", {"roles": ["dev"]}),
        ("PUT", f"/auth/admin/users/{uid}/disabled", {"disabled": True}),
        ("PUT", f"/auth/admin/users/{uid}/password", {"password": password}),
        ("GET", "/auth/admin/apikeys", None),
        ("POST", "/auth/admin/apikeys", {"client": "contract"}),
    ]
    for method, path, body in calls:
        r = api.request(method, path, json=body)
        assert r.status_code == 403 and r.json()["detail"] == "admin role required", (
            f"{method} {path}: {r.text}"
        )
