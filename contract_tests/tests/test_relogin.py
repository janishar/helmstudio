"""The suite's own re-sign-in (conftest.Relogin): only an expired token of a session it signed in is replaced."""

import base64
import json
import time

import httpx
from conftest import CREDS, FRESH, Relogin, expired


def jwt(exp: float) -> str:
    body = base64.urlsafe_b64encode(json.dumps({"exp": exp}).encode()).decode().rstrip("=")
    return f"h.{body}.s"


def test_only_an_expired_known_token_is_replaced(monkeypatch):
    old, live, forged = jwt(time.time() - 1), jwt(time.time() + 600), jwt(time.time() - 1)
    assert expired(old) and not expired(live) and not expired("not a jwt")
    CREDS[old] = ("e", "p")
    monkeypatch.setattr("conftest.sign_in", lambda e, p: {"access_token": "new"})
    sent = []
    for token in (old, old, live, forged):
        req = httpx.Request("GET", "http://x/", headers={"Authorization": f"Bearer {token}"})
        flow = Relogin().auth_flow(req)
        sent.append(next(flow).headers["Authorization"][7:])
    assert sent == ["new", "new", live, forged]
    del CREDS[old], FRESH[old]
