"""Sign-in, refresh rotation, sign-out, me, password change; no public sign-up."""

import uuid

from conftest import headers, make_client, raw_client, sign_in
from helpers import STUDIO, limited, ok, signin, wait_for


def test_signin_and_me(user_a, api):
    assert user_a["access_token"] and user_a["refresh_token"]
    assert (user_a["user"]["email"], user_a["user"]["roles"], user_a["user"]["disabled"]) == (
        user_a["email"],
        ["user"],
        False,
    )
    me = ok(api.get("/auth/me"))
    assert (me["id"], me["email"], me["name"]) == (
        user_a["user"]["id"],
        user_a["email"],
        "Contract A",
    )


def test_signin_failures_are_generic(no_token, user_a):
    for email, password in (
        (user_a["email"], "wrong password 1"),
        ("nobody-" + user_a["email"], "x" * 12),
    ):
        r = signin(no_token, email, password)
        assert r.status_code == 401 and r.json()["detail"] == "Wrong email or password."


def test_signup_is_closed():
    body = {
        "email": f"public-{uuid.uuid4().hex[:8]}@example.com",
        "password": "a long passphrase",
        "name": "Pub",
    }
    with raw_client(headers()) as c:
        assert c.post("/auth/signup", json=body).status_code == 404


def test_refresh_rotates_and_reuse_revokes_the_family(new_user, no_token):
    u = new_user("Refresh")
    s2 = ok(limited(no_token, "/auth/refresh", {"refresh_token": u["refresh_token"]}))
    assert s2["refresh_token"] != u["refresh_token"] and s2["access_token"] != u["access_token"]
    with make_client(headers(s2["access_token"])) as c:
        assert ok(c.get("/auth/me"))["id"] == u["user"]["id"]
    # the used token again: 401, and the whole family is gone
    assert (
        limited(no_token, "/auth/refresh", {"refresh_token": u["refresh_token"]}).status_code == 401
    )
    assert (
        limited(no_token, "/auth/refresh", {"refresh_token": s2["refresh_token"]}).status_code
        == 401
    )
    assert limited(no_token, "/auth/refresh", {"refresh_token": ""}).status_code == 401


def test_signout_revokes_the_session(new_user, no_token):
    u = new_user("Signout")
    with make_client(headers(u["access_token"])) as c:
        assert ok(c.get(f"{STUDIO}/sessions")) is not None  # studio caches the validation
        assert c.delete("/auth/signout").status_code == 204
        assert c.get("/auth/me").status_code == 401
        # studio drops its cached validation on evt.auth.revoked (core NATS: allow it a moment)
        wait_for(
            lambda: c.get(f"{STUDIO}/sessions").status_code == 401,
            timeout=5,
            what="studio to see the sign-out",
        )
    assert (
        limited(no_token, "/auth/refresh", {"refresh_token": u["refresh_token"]}).status_code == 401
    )


def test_change_password(new_user):
    u = new_user("Password")
    new_password = f"Changed-{uuid.uuid4().hex}"
    with make_client(headers(u["access_token"])) as c:
        r = c.put(
            "/auth/password",
            json={"current_password": "not it at all", "new_password": new_password},
        )
        assert r.status_code == 401
        r = c.put(
            "/auth/password", json={"current_password": u["password"], "new_password": "short"}
        )
        assert r.status_code == 422
        assert (
            c.put(
                "/auth/password",
                json={"current_password": u["password"], "new_password": new_password},
            ).status_code
            == 204
        )
        assert c.get("/auth/me").status_code == 401  # every session of the user is revoked
    assert sign_in(u["email"], new_password)["user"]["id"] == u["user"]["id"]
