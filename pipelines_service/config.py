"""Every env var in one place. `make dev` loads `.env`; compose passes the same names. X_FILE=<path> (compose
secrets) sets X to that file's contents."""

import os
from pathlib import Path
from urllib.parse import urlparse

for _k, _v in list(os.environ.items()):
    if _k.endswith("_FILE") and _v:
        os.environ[_k.removesuffix("_FILE")] = Path(_v).read_text().strip()

VERSION = "0.1.0"

# postgres://pipelines:<password>@postgres:5432/pipelines: the batches, the inbox and LangGraph's checkpoints
DATABASE_URL = os.environ.get("DATABASE_URL", "postgresql://pipelines@localhost:5463/pipelines")
# nats://pipelines:<password>@nats:4222
NATS_URL = os.environ.get("NATS_URL", "nats://localhost:4222")
# nats.conf lets the pipelines user subscribe only to _INBOX_pipelines.> (studio.runs.create replies)
INBOX_PREFIX = f"_INBOX_{urlparse(NATS_URL).username or 'pipelines'}"
HEALTH_PORT = int(os.environ.get("HEALTH_PORT", "8082"))
# rows driven at once (each holds a database connection while its graph runs; parked rows hold nothing)
DRIVE_CONCURRENCY = int(os.environ.get("PIPELINES_DRIVE_CONCURRENCY", "8"))
# plan 16.4: calls that leave the machine are off unless the deployment turns them on (deploy/.env REMOTE_MODELS)
REMOTE_MODELS = os.environ.get("PIPELINES_REMOTE_MODELS", "").lower() in ("1", "true", "on", "yes")
# test tooling only (the fake compose overlay): a remote provider may resolve to a private or loopback address (the
# fake OpenAI server inside the compose network); never link-local. Off: a remote must be a public address
REMOTE_ALLOW_PRIVATE = os.environ.get("PIPELINES_REMOTE_ALLOW_PRIVATE", "").lower() in (
    "1",
    "true",
    "on",
    "yes",
)
# where a remote provider's key_secret file lives (compose secrets); the key is read at call time, never logged
PROVIDER_KEYS_DIR = Path(os.environ.get("PIPELINES_PROVIDER_KEYS_DIR", "/run/secrets"))
# GPU hosts this service is the `server` env runner of (local-server calls); empty: server models have no host
SERVER_HOSTS = [h for h in os.environ.get("PIPELINES_SERVER_HOSTS", "").split(",") if h.strip()]
