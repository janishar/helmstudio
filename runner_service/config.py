"""Every env var in one place. `make dev` loads `.env`; compose passes the same names. X_FILE=<path> (compose
secrets) sets X to that file's contents."""

import os
from pathlib import Path
from urllib.parse import urlparse

for _k, _v in list(os.environ.items()):
    if _k.endswith("_FILE") and _v:
        os.environ[_k.removesuffix("_FILE")] = Path(_v).read_text().strip()

VERSION = "0.2.0"
# which runner this is: fake/fake is the compose fake runner (the echo pack); the native Mac runner is mac/torch.
# RUNNER_ENV picks the packs (those with a runtime of that env).
HOST = os.environ.get("RUNNER_HOST", "fake")
ENV = os.environ.get("RUNNER_ENV", "fake")
PUBLISHED_BY = f"runner {ENV} {VERSION}"

NATS_URL = os.environ.get("NATS_URL", "nats://localhost:4222")  # nats://runner:<password>@nats:4222
# nats.conf lets each runner user subscribe only to _INBOX_<user>.>
INBOX_PREFIX = f"_INBOX_{urlparse(NATS_URL).username or 'runner'}"
HEALTH_PORT = int(os.environ.get("HEALTH_PORT", "8081"))

S3_ENDPOINT = os.environ.get("S3_ENDPOINT", "")  # empty = AWS; http://seaweedfs:8333 in compose
S3_BUCKET = os.environ.get("S3_BUCKET", "helmstudio")
S3_REGION = os.environ.get("S3_REGION", "us-east-1")
# empty = the AWS default credential chain
S3_ACCESS_KEY_ID = os.environ.get("S3_ACCESS_KEY_ID", "")
S3_SECRET_ACCESS_KEY = os.environ.get("S3_SECRET_ACCESS_KEY", "")

# model weights: plain folders on this host's disk, never in an image or the object store
MODELS = Path(os.environ.get("HELMSTUDIO_MODELS", "~/GenAI/models")).expanduser()
# residency manager: budget = physical RAM - RESERVE_GB; what stays loaded after a run
RESERVE_GB = float(os.environ.get("RUNNER_RESERVE_GB", "12"))
AFTER_RUN = os.environ.get("RUNNER_AFTER_RUN", "keep_warm")  # unload | keep_warm | keep_pinned
IDLE_UNLOAD_S = float(
    os.environ.get("RUNNER_IDLE_UNLOAD_S", "600")
)  # keep_warm: unload after idle this long
PINS = {c for c in os.environ.get("RUNNER_PIN", "").split(",") if c}  # components keep_pinned keeps
