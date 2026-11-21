"""Every env var in one place. `make dev` loads `.env`; compose passes the same names. X_FILE=<path> (compose
secrets) sets X to that file's contents."""

import os
from pathlib import Path
from urllib.parse import urlparse

# paths read by their users, never into the environment: the Hub token (per job), the TLS CA, cert and key
PATHS = {"RUNNER_HF_TOKEN_FILE", "NATS_CA_FILE", "NATS_CERT_FILE", "NATS_KEY_FILE", "S3_CA_FILE"}
for _k, _v in list(os.environ.items()):
    if _k.endswith("_FILE") and _v and _k not in PATHS:
        os.environ[_k.removesuffix("_FILE")] = Path(_v).read_text().strip()

VERSION = "0.3.0"
# which runner this is: fake/fake is the compose fake runner (the echo pack); the native Mac runners are mac/torch,
# mac/mlx and mac/native (HEALTH_PORT 8091, 8092, 8093 from the root Makefile).
# RUNNER_ENV picks the packs (those with a runtime of that env).
HOST = os.environ.get("RUNNER_HOST", "fake")
ENV = os.environ.get("RUNNER_ENV", "fake")
PUBLISHED_BY = f"runner {ENV} {VERSION}"

NATS_URL = os.environ.get("NATS_URL", "nats://localhost:4222")  # nats://runner:<password>@nats:4222
# nats.conf lets each runner user subscribe only to _INBOX_<user>.>
INBOX_PREFIX = f"_INBOX_{urlparse(NATS_URL).username or 'runner'}"
HEALTH_PORT = int(os.environ.get("HEALTH_PORT", "8081"))
# loopback only: the health check asks from inside the same host or container; a GPU host uses host networking
HEALTH_HOST = os.environ.get("HEALTH_HOST", "127.0.0.1")
# TLS (contracts/README.md "A remote GPU host"): NATS_URL tls://…, S3_ENDPOINT https://…; empty = the system CAs
NATS_CA_FILE = os.environ.get("NATS_CA_FILE", "")  # verify the NATS server against this CA
NATS_CERT_FILE = os.environ.get("NATS_CERT_FILE", "")  # optional client cert and key
NATS_KEY_FILE = os.environ.get("NATS_KEY_FILE", "")
S3_CA_FILE = os.environ.get("S3_CA_FILE", "")  # the S3 client's `verify`

S3_ENDPOINT = os.environ.get("S3_ENDPOINT", "")  # empty = AWS; http://seaweedfs:8333 in compose
S3_BUCKET = os.environ.get("S3_BUCKET", "helmstudio")
S3_REGION = os.environ.get("S3_REGION", "us-east-1")
# empty = the AWS default credential chain
S3_ACCESS_KEY_ID = os.environ.get("S3_ACCESS_KEY_ID", "")
S3_SECRET_ACCESS_KEY = os.environ.get("S3_SECRET_ACCESS_KEY", "")


def plain_with_ca() -> str | None:
    """Why the start is refused: a CA file set (a link meant to be TLS) next to a plain NATS_URL or S3_ENDPOINT, which
    would send the password and keys in clear (contracts/README.md, A remote GPU host: never a plain fallback)."""
    if NATS_CA_FILE and not NATS_URL.startswith("tls://"):
        return "NATS_CA_FILE is set but NATS_URL is not tls://: refusing a plain NATS link"
    if S3_CA_FILE and not S3_ENDPOINT.startswith("https://"):
        return "S3_CA_FILE is set but S3_ENDPOINT is not https://: refusing a plain S3 link"
    return None


# model weights: plain folders on this host's disk, never in an image or the object store
MODELS = Path(os.environ.get("HELMSTUDIO_MODELS", "~/GenAI/models")).expanduser()
# residency manager: budget = physical RAM - RESERVE_GB; what stays loaded after a run
RESERVE_GB = float(os.environ.get("RUNNER_RESERVE_GB", "12"))  # on CUDA: VRAM - RESERVE_GB
RAM_RESERVE_GB = float(
    os.environ.get("RUNNER_RAM_RESERVE_GB", "4")
)  # CUDA: host RAM budget = RAM - this
DEVICE = os.environ.get(
    "RUNNER_DEVICE", ""
)  # torch: force mps | cuda | cpu; empty = the first present
# torch, CUDA: how an Fp8Linear multiplies (API 1.33): auto = torch._scaled_mm when this GPU takes it, else dequant
# per layer; dequant = always dequant (the reference for PSNR checks)
FP8_MATMUL = os.environ.get("RUNNER_FP8_MATMUL", "auto")
AFTER_RUN = os.environ.get("RUNNER_AFTER_RUN", "keep_warm")  # unload | keep_warm | keep_pinned
IDLE_UNLOAD_S = float(
    os.environ.get("RUNNER_IDLE_UNLOAD_S", "600")
)  # keep_warm: unload after idle this long
# the runner's own files: the LoRA cache (<cache>/loras/<sha256>.safetensors, own LoRAs, LRU) and the shared
# LoRA hashes; never weights of a model
CACHE = Path(os.environ.get("RUNNER_CACHE", "~/.cache/helmstudio-runner")).expanduser()
LORA_CACHE_GB = float(os.environ.get("RUNNER_LORA_CACHE_GB", "20"))
PINS = {c for c in os.environ.get("RUNNER_PIN", "").split(",") if c}  # components keep_pinned keeps

# model jobs (contracts/README.md "Models and model jobs"): the Hub (the fake hub in tests and the fake overlay) and
# its token, a file read only when a gated repo is downloaded; absent = anonymous. Never logged or published.
HF_ENDPOINT = (os.environ.get("HF_ENDPOINT") or "https://huggingface.co").rstrip("/")
HF_TOKEN_FILE = Path(
    os.environ.get("RUNNER_HF_TOKEN_FILE")
    or Path(__file__).resolve().parent.parent / "deploy/secrets/hf_token"
)
