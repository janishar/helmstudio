"""Kong: the client API key is required everywhere except health, the JWKS and signed file links."""

from conftest import headers, raw_client
from helpers import STUDIO, ok


def test_missing_api_key(anon):
    r = anon.get(f"{STUDIO}/sessions")
    assert r.status_code == 401 and r.json()["detail"] == "x-api-key header is missing"
    assert (
        anon.post("/auth/signin", json={"email": "a@example.com", "password": "x"}).status_code
        == 401
    )


def test_invalid_api_key():
    with raw_client({"X-API-Key": "helm_" + "x" * 43}) as c:
        r = c.get(f"{STUDIO}/workflows")
    assert r.status_code == 401 and r.json()["detail"] == "invalid api key"


def test_api_key_without_user_token(no_token):
    for path in (f"{STUDIO}/sessions", f"{STUDIO}/workflows", f"{STUDIO}/hosts", "/auth/me"):
        assert no_token.get(path).status_code == 401, path


def test_bad_bearer_token():
    with raw_client(headers("not.a.jwt")) as c:
        assert c.get(f"{STUDIO}/sessions").status_code == 401
        assert c.get("/auth/me").status_code == 401


def test_health_needs_no_key():
    with raw_client({}) as c:
        r = c.get("/auth/health")
    assert r.status_code == 200 and r.json() == {"status": "ok"}


def test_jwks_needs_no_key(anon):
    keys = ok(anon.get("/auth/.well-known/jwks.json"))["keys"]
    assert keys and all(k["alg"] == "RS256" and k["kid"] for k in keys)
