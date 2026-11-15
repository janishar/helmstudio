# helmstudio-next plan — Dependencies

[← Plan index](../README.md)

## 18. Dependencies and keeping them current

The project starts on the latest stable release of everything, checked against
the package registries on 2026-11-14. The reference repository is a patch or a
minor behind on several of these; helmstudio-next does not copy its lockfiles.
This section owns version numbers; other sections name libraries without them.

### 18.1 Pins

**Go** (`go.mod` holds minimums, `go.sum` locks)

| Item | Version |
|---|---|
| Go toolchain | 1.27.4 (`go 1.27`, `toolchain go1.27.4`; image `golang:1.27.4-alpine3.24`) |
| gin | v1.12.0 |
| pgx/v5 | v5.13.0 |
| goose/v3 | v3.30.1 |
| nats.go | v1.55.0 |
| nats-server/v2 (tests) | v2.15.3 |
| aws-sdk-go-v2 | core v1.48.0, s3 v1.114.10, config v1.33.18, credentials v1.21.0 (bump together) |
| golang.org/x/crypto | v0.59.0 |
| golang.org/x/image | v0.47.0 (studio: webp header check of uploads) |
| golang-jwt/jwt/v5 | v5.3.1 |

**Infrastructure** (exact image tags, by digest in production)

| Item | Version |
|---|---|
| Kong (open source) | 3.9.4 — the newest open-source line; do not plan on later features |
| Kong go-pdk | v0.11.2 (maintenance mode) |
| PostgreSQL | 18.7 |
| NATS server | 2.15.3 |
| SeaweedFS | 4.53 |
| nats-box (stream and KV setup) | 0.20.2 |
| Alpine (Go and web runtime images) | 3.24.3 |
| Docker Compose | 2.20 or later (current v5.7.1); no `version:` key |

**Python** (`pyproject` floors plus a committed `uv.lock`)

| Item | Version |
|---|---|
| Python | 3.14 (`>=3.14,<3.15`; 3.15 is a month old and unproven across the ML stack; never 3.14.1, which torchvision excludes) |
| uv | 0.13.8 (exact, via `required-version`) |
| ruff | 0.17.5 (exact) |
| pytest, pytest-asyncio | ≥ 9.2.0, ≥ 1.5.0 |
| pydantic | ≥ 2.14.3 |
| nats-py | ≥ 2.16.0 |
| psycopg, psycopg-pool | ≥ 3.3.9, ≥ 3.3.3 |
| langgraph | ≥ 1.2.22, < 1.3 |
| langchain-core | ≥ 1.8.0, < 2 |
| langchain-openai, langchain-anthropic | ≥ 1.8.0, ≥ 1.7.10 (optional extras) |
| langgraph-checkpoint, -postgres | ≥ 4.3.2, < 5; ≥ 3.2.1, < 4 |
| jsonschema | ≥ 4.26.0 |
| boto3 | ≥ 1.44.3 (wrapped in threads; not `aioboto3`, which pins a year-old stack) |
| huggingface_hub | ≥ 2.4.0, < 3 |
| safetensors | ≥ 0.8.0 |
| torch + torchvision | 2.15.* with 0.29.* (an exact pair) |
| diffusers, transformers, accelerate | ≥ 0.43.0; ≥ 5.23.0, < 6; ≥ 1.15.0 |
| mlx | ≥ 0.32.4 (mlx-lm is not used: the llm pack runs text and images through mlx-vlm) |
| mlx-vlm | ≥ 0.7.12 (Mac-only: the mlx env's extra `llm`, with llguidance for constrained JSON; `make runners` syncs it) |
| numpy, pillow | ≥ 2.5.4, ≥ 13.0.0 |

**Web** (exact for framework and compiler, ranges for the rest; lockfile committed)

| Item | Version |
|---|---|
| Node.js | 26.13.1 LTS |
| Next.js (+ eslint-config-next) | 16.4.6 |
| React, React DOM | 19.3.1 |
| TypeScript | 6.0.3 — deliberately not 7 (the native compiler); the type generator and lint plugins have not declared support |
| Tailwind CSS | ^4.3.5 |
| shadcn CLI | 4.23.0 (run with `npx`, not a dependency) |
| lucide-react | ^1.73.0 |
| @xyflow/react, elkjs | ^12.12.1, ^0.12.1 |
| openapi-typescript, openapi-fetch | 7.13.0, 0.17.0 (exact) |
| vitest, Playwright, eslint | ^5.1.0, 1.65.1 (exact), ^10.15.0 |
| prettier | 3.10.1 (exact) |
| next-themes, server-only | ^0.4.6, ^0.0.1 |

### 18.2 Things that must move together

- `langchain-core`, `langchain-openai` and `langgraph`: each release line
  requires the others' current floor.
- `torch` with `torchvision`; on Linux, torch 2.15 brings CUDA 13.2 wheels.
- `mlx-vlm` needs `mlx` ≥ 0.32.4 and `transformers` ≥ 5.14.
- The `aws-sdk-go-v2` modules.
- `next` with `eslint-config-next`; `react` with `react-dom`.

Each runner env (`mlx`, `torch`) has its own lockfile, so a model that needs an
older torch gets its own env rather than holding the rest back (section 6).

### 18.3 Staying current

- **Lockfiles are the truth** and are committed: `go.sum`, `uv.lock`
  (`uv lock --check` in CI), `package-lock.json` (`npm ci`).
- **Renovate**, weekly, one config for Go modules, uv projects, npm,
  Dockerfiles and compose files. Related packages are grouped into one update
  (the groups in 18.2). Patch updates merge themselves when the contract suite
  is green; minors are reviewed; majors wait on the dashboard.
- **CI checks**: `govulncheck`, `pip-audit`, `npm audit`, and a report of
  what is outdated.
- **Monthly, by hand**: toolchains and servers — Go, Node LTS, the Python
  minor, Postgres, NATS, Kong 3.9.x patches, SeaweedFS.
- A version bump that changes a golden output (section 11.1) is a decision,
  not a routine update.
