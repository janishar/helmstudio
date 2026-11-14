# contract_tests

Black-box HTTP suite for the Catalog Agent public API: it talks only HTTP to `BASE_URL` (Kong in front of the
services) and never imports server code. It is the gate for every change that crosses a service boundary.

The stack under test must run in **fake mode** (`deploy/compose/fake.yml`) against a project you can throw away: the
suite signs up two new users on every run and creates sessions, SKUs, runs and zips that it never deletes. Use the
`ora-catalog-test` project (Kong on 8190), never the live `ora-catalog` one (8100).

## Run

```sh
make -C contract_tests venv                 # uv venv .venv + install from pyproject.toml
make -C contract_tests check-contracts      # ../contracts: schemas, fixtures, openapi.yaml (no stack needed)

# from the repo root: the test project in fake mode (these variables make plain `docker compose` mean it)
export COMPOSE_PROJECT_NAME=ora-catalog-test COMPOSE_FILE=deploy/docker-compose.yml:deploy/compose/fake.yml \
  IMAGE_TAG=gate KONG_HTTP_PORT=8190 POSTGRES_HOST_PORT=5444 ORA_NET=172.30.91
docker compose up -d --build
make -C contract_tests test CONTRACT_SCHEMAS=1 API_KEY=$(docker compose run --rm -T auth apikey contract)
make -C contract_tests test CONTRACT_SCHEMAS=1 API_KEY=... ARGS="-k generation -x"   # any pytest args
```

| env | meaning |
|---|---|
| `BASE_URL` | the API root (default `http://localhost:8190`, the `ora-catalog-test` project) |
| `API_KEY` | sent as `X-API-Key` (a client key: `run --rm auth apikey <client>`) |
| `ADMIN_EMAIL`, `ADMIN_PASSWORD` | the test stack's admin (`deploy/compose/fake.yml`), which creates users A and B: sign-up is closed |
| `BEARER_TOKEN`, `BEARER_TOKEN_B` | optional users A and B; empty = the admin creates two new users |
| `CONTRACT_SCHEMAS=1` | validate every JSON response against `contracts/openapi.yaml` for its path, method and status; a route or status the spec doesn't list fails (`OPENAPI_PATH` overrides the location; see `tests/schemas.py`) |
| `JOB_TIMEOUT` | seconds to wait for processing or a run (default 120) |

Signed file links are absolute, so catalog_service's `PUBLIC_BASE_URL` must be the address the tests reach (compose
sets it from `KONG_HTTP_PORT`).

## Layout

| Module | What |
|---|---|
| `test_sessions.py` | create, list, get, patch (and its 422s), unknown ids, auth required, library, favorites, estimate, memory |
| `test_inputs.py` | raw multipart, a folder upload's `paths` (one SKU per sub-folder; bad paths 422), CSV, links; guidelines and uploads; pose suggestions; events polling and SSE (`Last-Event-ID`); questions; Start refused while a blocking question is open, then Start |
| `test_generation.py` | one started session walked forward: Next step card (sample → batch → all → download), runs, single and bulk approval, regenerate keeping the old revision, typed requests (incl. `not_understood`: no run), SKU overrides, counts and stats, the zip through the signed `/files` link |
| `test_products.py` | the library's 23 products in 3 families; raw photos named for jewellery, furniture and garments sort into the right products and codes; the `families` filter (and its 422); a furniture SKU has no model and still renders, reviewed with the furniture goals; moving SKUs to another product (`category`, `apply_to_category`: codes kept, model and poses follow, another family's background falls back); per-product `max_attempts` (every product follows `MAX_REGENERATIONS` today) and `default_resolution`; 16 models, a `full_body_url` for each with a turnaround sheet, garment defaults on such a model and pose examples from the turnaround crops; `GET /metrics` after a fake run (counts by product, first-try rate, tries, rejected, feedback, credits per approved; another user's session is empty; bad `session_id` 422) |
| `test_refresh_reset.py` | Refresh keeps SKUs, runs and memory; Reset keeps only memory |
| `test_tries_poses.py` | the added ratios (4:3, 3:2, 5:4, 21:9) and `pose_noun` per family; `pose_ratios` / `pose_notes` PATCH round trip and their 422s (unknown ratio, another product's pose, a 501-character note), another user 404; a pose renders at its own ratio (fake renders at the asked ratio, so pixel size shows it); every try in `Image.attempts` signed under `images/<id>/attempt_<n>`, exactly one best (same bytes as the image), a tampered or unsigned try link 403; regenerate at another ratio (bad ratio 422, another user 404), the old revision keeps its tries and the brief ratio is unchanged |
| `test_views.py` | jewellery raw photos named `<code>_White Side.png`, `<code>_Model.png` … get view tags (`attributes.sides`, `raw_views`); a packshot that fails review (fake mode: a `fake-reject` pose note) fails the SKU's other shots with the gate message (again when one is re-run while that packshot is live) and leads the Next step line; a Mood pose for every jewellery product, outside its defaults |
| `test_jobs.py` | `GET /jobs` lists a session's run and processing job newest first; `GET /jobs/{id}` gives the processing steps, the run's images with their tries and its usage (fake mode: one image call per try, credits, no LLM call); another user sees none of them (404) |
| `test_access.py` | closed beta: `/auth/signup` is 404; the admin creates accounts (dev role, name from the email; 409 duplicate; 422 for admin), the new user signs in, a user can't create accounts (403), and the `.env` admin's account is 409 to change |
| `test_isolation.py` | user B gets 404 on every id route of user A |

`helpers.py` has the test images, polling and the session steps (processed, started, run); `conftest.py` the clients
and the shared started session. The raw photos are named so the fake sorter files them: 7 rings in two groups (three
lifestyle shots, so the guideline txt raises its conflict), 4 necklaces (two share a name: the duplicate question),
1 anklet.
