# contract_tests

Black-box HTTP suite for the helmstudio public API (`contracts/openapi.yaml`): it talks only HTTP to `BASE_URL`
(Kong in front of the services) and never imports server code. It is the gate for every milestone.

The stack under test runs the **fake runner** (host `fake`, env `fake`, workflow `echo`) and must be one you can
throw away: every run creates users, sessions and runs that it never deletes.

## Run

```sh
make -C contract_tests venv              # uv sync --frozen into .venv
make -C contract_tests check-contracts   # ../contracts: schemas, fixtures, openapi.yaml (no stack needed)
make -C contract_tests collect lint      # no stack needed

# against the test stack (helmstudio-test, Kong on 127.0.0.1:8390); `make test` from the repo root brings it up
# and runs this with a fresh key and the test admin
make -C contract_tests test CONTRACT_SCHEMAS=1 API_KEY=... ADMIN_EMAIL=admin@helm.test ADMIN_PASSWORD=...
make -C contract_tests test API_KEY=... ARGS="-k runs -x"   # any pytest args
```

| env | meaning |
|---|---|
| `BASE_URL` | the API root (default `http://127.0.0.1:8390`, the test stack; never the dev stack on 8300) |
| `API_KEY` | sent as `X-API-Key`: a client key (`auth apikey <client>`) |
| `ADMIN_EMAIL`, `ADMIN_PASSWORD` | the stack's root admin; it creates users A and B on every run (there is no sign-up) |
| `CONTRACT_SCHEMAS=1` | validate every JSON response, and every SSE event, against `contracts/openapi.yaml` for its path, method and status; a route or status the spec doesn't list fails (`OPENAPI_PATH` overrides the location; see `tests/schemas.py`) |
| `JOB_TIMEOUT` | seconds to wait for a run or an event (default 60) |

## Resilience

`resilience.sh` restarts studio, runner, nats, postgres and kong of the running helmstudio-test stack one at a time,
each in the middle of a ~12 s echo run, and requires that the run ends (succeeded, or failed/cancelled with an
error: never stuck queued or running) and that a new run afterwards succeeds. It takes a few minutes.

```sh
make resilience   # from the repo root: brings the test stack up, makes a key, runs the script
API_KEY=... ADMIN_EMAIL=admin@helm.test ADMIN_PASSWORD=... contract_tests/resilience.sh [svc ...]   # test stack already up
```

Kong's sign-in rate limit is 20 a minute per IP; the suite signs in about that often, so a test may pause once for
`Retry-After`.

## Layout

| Module | What |
|---|---|
| `test_gateway.py` | missing or invalid API key (401), a key without a user token, a bad bearer token, `/auth/health` and the JWKS without a key |
| `test_auth.py` | sign-in and `me`, generic sign-in failures, no sign-up (404), refresh rotation (reuse revokes the family), sign-out (auth and studio both refuse the token), password change |
| `test_admin_users.py` | create (email normalised, name from the email, roles sorted; 409 duplicate; 422 for `admin` and bad input), roles and password reset revoke sessions, disable revokes sessions and blocks sign-in, enable, the root admin is 409 to change, unknown ids 404, a non-admin 403 |
| `test_sessions.py` | create (`Untitled`), list newest first, get, rename (and its 422s), delete, unknown and malformed ids 404 |
| `test_workflows.py` | `echo` listed, its graph and node schemas, unknown ids 404; `GET /hosts` shows `fake`/`fake` idle or busy, redacted (only `memory` and `resident` beyond the state) |
| `test_runs.py` | the echo run: queued, events in seq order (`queued`, `running`, one progress per step, the output, `succeeded`), the PNG through its signed link, the finished run; `fail=true` fails with an error; cancel mid-run (delete is 409 meanwhile) ends `cancelled` with no output; invalid inputs (null for a non-seed param included) and an unknown `workflow_id` 422; unknown ids 404 |
| `test_qwen.py` | opt-in (`QWEN=1`, skipped otherwise): `qwen-image` on the Mac torch runner (`make runners`, joined to the stack `BASE_URL` points at), turbo int8 768², a fixed and a random seed, up to 15 min each: phases on every progress event, one PNG of the size, `resolved.seed` = the output's seed |
| `test_events.py` | JSON `?since=` paging and `last_seq`, bad `since` 422, SSE replay from `Last-Event-ID` or `?since=` (the header wins) |
| `test_isolation.py` | user B gets 404 for A's session, runs, events and cancel; A's output file only through its signed link (missing, tampered or re-dated signatures and sibling keys are 403) |

`helpers.py` has the polling, the SSE reader and the run steps; `conftest.py` the clients, the users and `echo_done`,
user A's finished quick echo run that the runs, events and isolation tests share.
