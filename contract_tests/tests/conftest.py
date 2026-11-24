"""Clients and shared objects. Everything goes over HTTP to BASE_URL (Kong); nothing here imports the server's code.

Env: BASE_URL, API_KEY (sent as X-API-Key), CONTRACT_SCHEMAS=1 to validate JSON bodies against
contracts/openapi.yaml (see schemas.py). There is no sign-up, so the test stack's root admin (ADMIN_EMAIL /
ADMIN_PASSWORD) creates users A and B with fresh e-mails on every run."""

import base64
import json
import os
import time
import uuid

import httpx
import pytest
import schemas
from helpers import QUICK, STUDIO, new_session, ok, run_events, signin, start_run

BASE_URL = os.environ.get("BASE_URL", "http://127.0.0.1:8390").rstrip("/")
API_KEY = os.environ.get("API_KEY", "")
ADMIN_EMAIL = os.environ.get("ADMIN_EMAIL", "contract-admin@example.com")
ADMIN_PASSWORD = os.environ.get("ADMIN_PASSWORD", "contract admin pass 1")


def headers(token: str = "") -> dict:
    h = {"X-API-Key": API_KEY} if API_KEY else {}
    return h | ({"Authorization": f"Bearer {token}"} if token else {})


# access token → (email, password) of a session signed in here, and an expired token → its fresh one: the suite
# takes longer than ACCESS_TTL (15 min), so a session-scoped client signs in again when its token has expired
CREDS: dict[str, tuple[str, str]] = {}
FRESH: dict[str, str] = {}


def expired(token: str) -> bool:
    """The JWT's exp has passed (read, not verified: the server decides; this only says when to sign in again)."""
    try:
        body = token.split(".")[1]
        return (
            json.loads(base64.urlsafe_b64decode(body + "=" * (-len(body) % 4)))["exp"]
            <= time.time() + 5
        )
    except IndexError, ValueError, KeyError:
        return False


class Relogin(httpx.Auth):
    """Sends the newest token of the request's session; an expired one is signed in again first. A token this suite
    did not sign in, or one that is not expired (revoked, forged), is sent as it is, so 401 tests still see 401."""

    def auth_flow(self, request):
        auth = request.headers.get("Authorization", "")
        if auth.startswith("Bearer "):
            token = auth[7:]
            while token in FRESH:
                token = FRESH[token]
            if token in CREDS and expired(token):
                new = sign_in(*CREDS[token])["access_token"]
                FRESH[token], token = new, new
            request.headers["Authorization"] = f"Bearer {token}"
        yield request


def make_client(h: dict) -> httpx.Client:
    hooks = {"response": [schemas.check]} if schemas.ENABLED else {}
    return httpx.Client(base_url=BASE_URL, headers=h, timeout=30, event_hooks=hooks, auth=Relogin())


def raw_client(h: dict) -> httpx.Client:
    """No schema check: for routes outside the contract (health, the closed sign-up)."""
    return httpx.Client(base_url=BASE_URL, headers=h, timeout=30)


def sign_in(email: str, password: str) -> dict:
    """A Session (tokens and user)."""
    with make_client(headers()) as c:
        session = ok(signin(c, email, password))
    CREDS[session["access_token"]] = (email, password)
    return session


def pytest_sessionfinish(session, exitstatus):
    """Revokes the run's own API key (root make test issues a "contract" key each run), pass or fail. Only a key whose
    prefix matches API_KEY is revoked, so a key another run is using stays."""
    if not API_KEY:
        return
    try:
        with make_client(headers(sign_in(ADMIN_EMAIL, ADMIN_PASSWORD)["access_token"])) as c:
            for k in c.get("/auth/admin/apikeys").json()["api_keys"]:
                if (
                    k["client"] == "contract"
                    and not k["revoked_at"]
                    and API_KEY.startswith(k["prefix"])
                ):
                    c.delete(f"/auth/admin/apikeys/{k['id']}")
    except (
        httpx.HTTPError,
        AssertionError,
        KeyError,
        ValueError,
    ) as e:  # never turn a run's result into a cleanup failure
        print(f"\ncould not revoke the contract API key: {e}")


@pytest.fixture(scope="session")
def admin_session() -> dict:
    return sign_in(ADMIN_EMAIL, ADMIN_PASSWORD)


@pytest.fixture(scope="session")
def admin(admin_session):
    """The root admin."""
    with make_client(headers(admin_session["access_token"])) as c:
        yield c


@pytest.fixture(scope="session")
def new_user(admin):
    """new_user(name, roles=["user"]) → {"email", "password", "user", "access_token", "refresh_token"}: an account
    the admin creates, signed in."""

    def make(name: str, roles: list[str] | None = None) -> dict:
        email = f"contract-{name.lower()}-{uuid.uuid4().hex[:10]}@example.com"
        password = f"Contract-{uuid.uuid4().hex}"
        body = {
            "email": email,
            "password": password,
            "name": f"Contract {name}",
            "roles": roles or ["user"],
        }
        ok(admin.post("/auth/admin/users", json=body), 201)
        return {"email": email, "password": password} | sign_in(email, password)

    return make


@pytest.fixture(scope="session")
def user_a(new_user) -> dict:
    return new_user("A")


@pytest.fixture(scope="session")
def api(user_a):
    """User A."""
    with make_client(headers(user_a["access_token"])) as c:
        yield c


@pytest.fixture(scope="session")
def api_b(new_user):
    """User B: sees none of A's objects."""
    with make_client(headers(new_user("B")["access_token"])) as c:
        yield c


@pytest.fixture(scope="session")
def no_token():
    """The API key without a user token (401 checks)."""
    with make_client(headers()) as c:
        yield c


@pytest.fixture(scope="session")
def anon():
    """No credentials at all (signed file links, health)."""
    with make_client({}) as c:
        yield c


@pytest.fixture(scope="session")
def echo_done(api, user_a) -> dict:
    """User A's finished quick echo run in a new session: {"session", "run" (as created), "events" (the run's
    SessionEvents from the stream), "owner_id"}."""
    session = new_session(api, "Contract echo")
    run = start_run(api, session["id"], QUICK)
    events = run_events(api, session["id"], run["id"])
    session = ok(api.get(f"{STUDIO}/sessions/{session['id']}"))  # a run bumps its updated_at
    return {"session": session, "run": run, "events": events, "owner_id": user_a["user"]["id"]}
