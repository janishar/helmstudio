"""Every env var in one place. `make dev` loads `.env`; compose passes the same names. X_FILE=<path> (compose
secrets) sets X to that file's contents."""

import os
from pathlib import Path

for _k, _v in list(os.environ.items()):
    if _k.endswith("_FILE") and _v:
        os.environ[_k.removesuffix("_FILE")] = Path(_v).read_text().strip()

VERSION = "0.1.0"
# the fake runner: one host, one env, the echo family (contracts/fixtures/registry carry the same names)
HOST = "fake"
ENV = "fake"
PUBLISHED_BY = f"runner {HOST} {VERSION}"

NATS_URL = os.environ.get("NATS_URL", "nats://localhost:4222")  # nats://runner:<password>@nats:4222
HEALTH_PORT = int(os.environ.get("HEALTH_PORT", "8081"))

S3_ENDPOINT = os.environ.get("S3_ENDPOINT", "")  # empty = AWS; http://seaweedfs:8333 in compose
S3_BUCKET = os.environ.get("S3_BUCKET", "helmstudio")
S3_REGION = os.environ.get("S3_REGION", "us-east-1")
# empty = the AWS default credential chain
S3_ACCESS_KEY_ID = os.environ.get("S3_ACCESS_KEY_ID", "")
S3_SECRET_ACCESS_KEY = os.environ.get("S3_SECRET_ACCESS_KEY", "")
