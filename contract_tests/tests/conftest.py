"""Clients and shared objects. Everything goes over HTTP to BASE_URL (Kong); nothing here imports the server's code.

Env: BASE_URL, API_KEY (sent as X-API-Key), CONTRACT_SCHEMAS=1 to validate JSON bodies against
contracts/openapi.yaml (see schemas.py). There is no sign-up, so the test stack's root admin (ADMIN_EMAIL /
ADMIN_PASSWORD) creates users A and B with fresh e-mails on every run."""

import os
import uuid

import httpx
import pytest
import schemas
from helpers import QUICK, STUDIO, new_session, ok, run_events, signin, start_run

BASE_URL = os.environ.get("BASE_URL", "http://127.0.0.1:8300").rstrip("/")
API_KEY = os.environ.get("API_KEY", "")
ADMIN_EMAIL = os.environ.get("ADMIN_EMAIL", "contract-admin@example.com")
ADMIN_PASSWORD = os.environ.get("ADMIN_PASSWORD", "contract admin pass 1")


def headers(token: str = "") -> dict:
    h = {"X-API-Key": API_KEY} if API_KEY else {}
    return h | ({"Authorization": f"Bearer {token}"} if token else {})


def make_client(h: dict) -> httpx.Client:
    hooks = {"response": [schemas.check]} if schemas.ENABLED else {}
    return httpx.Client(base_url=BASE_URL, headers=h, timeout=30, event_hooks=hooks)


def raw_client(h: dict) -> httpx.Client:
    """No schema check: for routes outside the contract (health, the closed sign-up)."""
    return httpx.Client(base_url=BASE_URL, headers=h, timeout=30)


def sign_in(email: str, password: str) -> dict:
    """A Session (tokens and user)."""
    with make_client(headers()) as c:
        return ok(signin(c, email, password))


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
