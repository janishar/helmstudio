"""Clients and shared objects. Everything goes over HTTP to BASE_URL; nothing here imports the server's code.

Env: BASE_URL, API_KEY (sent as X-API-Key), CONTRACT_SCHEMAS=1 to validate JSON bodies against
contracts/openapi.yaml (see schemas.py). Sign-up is closed, so users A and B are created on every run (fresh e-mails)
by the test stack's admin (ADMIN_EMAIL / ADMIN_PASSWORD: deploy/compose/fake.yml's), unless BEARER_TOKEN and
BEARER_TOKEN_B are given."""

import os
import uuid

import httpx
import pytest
import schemas
from helpers import CATALOG, ok, processed, started

BASE_URL = os.environ.get("BASE_URL", "http://localhost:8190").rstrip("/")
API_KEY = os.environ.get("API_KEY", "")
ADMIN_EMAIL = os.environ.get("ADMIN_EMAIL", "contract-admin@example.com")
ADMIN_PASSWORD = os.environ.get("ADMIN_PASSWORD", "contract admin pass 1")


def headers(token: str = "") -> dict:
    h = {"X-API-Key": API_KEY} if API_KEY else {}
    return h | ({"Authorization": f"Bearer {token}"} if token else {})


def make_client(h: dict) -> httpx.Client:
    hooks = {"response": [schemas.check]} if schemas.ENABLED else {}
    return httpx.Client(base_url=BASE_URL, headers=h, timeout=60, event_hooks=hooks)


def admin_token() -> str:
    """The test stack's admin's access token."""
    with make_client(headers()) as c:
        return ok(c.post("/auth/signin", json={"email": ADMIN_EMAIL, "password": ADMIN_PASSWORD}))["access_token"]


def sign_up(name: str) -> str:
    """A new user's access token: the admin creates the account (POST /auth/admin/users), then it signs in."""
    email = f"contract-{name.lower()}-{uuid.uuid4().hex[:10]}@example.com"
    password = f"Contract-{uuid.uuid4().hex}"
    with make_client(headers(admin_token())) as c:
        body = {"email": email, "password": password, "name": f"Contract {name}", "roles": ["user"]}
        ok(c.post("/auth/admin/users", json=body), 201)
    with make_client(headers()) as c:
        return ok(c.post("/auth/signin", json={"email": email, "password": password}))["access_token"]


@pytest.fixture(scope="session")
def api():
    """User A."""
    with make_client(headers(os.environ.get("BEARER_TOKEN") or sign_up("A"))) as c:
        yield c


@pytest.fixture(scope="session")
def api_b():
    """User B: sees none of A's objects."""
    with make_client(headers(os.environ.get("BEARER_TOKEN_B") or sign_up("B"))) as c:
        yield c


@pytest.fixture(scope="session")
def no_token():
    """The API key without a user token (401 checks)."""
    with make_client(headers()) as c:
        yield c


@pytest.fixture(scope="session")
def anon():
    """No credentials at all (signed file links)."""
    with make_client({}) as c:
        yield c


@pytest.fixture(scope="session")
def lib(api) -> dict:
    return ok(api.get(f"{CATALOG}/library"))


@pytest.fixture(scope="session")
def ready(api) -> dict:
    """A started session (12 SKUs: 7 rings in two groups, 4 necklaces, 1 anklet), nothing run yet; shared by the
    generation tests, which walk it forward in file order."""
    return started(api)


@pytest.fixture
def fresh(api) -> dict:
    """A new processed session (questions open, not started)."""
    return processed(api)
