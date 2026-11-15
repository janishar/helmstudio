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
error: never stuck queued or running) and that a new run afterwards succeeds. `studio-queued` restarts studio while a
second run is still queued behind the first, and requires that run to succeed. `batch-pipelines`, `batch-studio`,
`batch-nats`, `batch-postgres` restart that service in the middle of a 12-row slow echo batch and `batch-kill` SIGKILLs
the pipelines container and starts it again; the script presses Resume whenever the batch is paused, and the batch
must end `done` with exactly 12 runs (one per row) and 12 takes. `pipeline-parked` runs a 3-row echo pipeline with a
gate, picks row 1 and restarts pipelines and studio while its edit runs and rows 2 and 3 are parked, then resumes
and picks the rest: the batch must end `done` with exactly 6 runs and 6 distinct takes, one per row. It takes a few
minutes.

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
| `test_qwen.py` | opt-in (`QWEN=1`, skipped otherwise): `qwen-image` on the Mac torch runner (`make runners`, joined to the stack `BASE_URL` points at), turbo int8 768², a fixed and a random seed, and an edit of an uploaded reference (`encode.refs`, canvas fixed), up to 15 min each: phases on every progress event, one PNG of the size, `resolved.seed` = the output's seed, the edit's `parents` = the reference |
| `test_image_inputs.py` | image inputs on echo's `refs` (skipped when the fake runner lacks it): references recorded in `made_with`, `parents` (each once) and the reference's `children`; deleted, foreign, pending and unknown take ids 422 naming `echo.refs[n].image`, 11 items, unknown fields and non-ref items 422; "Use in…" (`/use`) and its 404/422s; reuse gives the references back; a deleted reference leaves `parents` but stays in `made_with` and reuse (so reposting 422s); batch lines of take ids and a table cell of references; B can use, see or "Use in" none of A's takes |
| `test_events.py` | JSON `?since=` paging and `last_seq`, bad `since` 422, SSE replay from `Last-Event-ID` or `?since=` (the header wins) |
| `test_queue.py` | the dispatcher on the one fake host: three users' runs start round-robin (fresh users Y and Z), positions 1..n that only go down, `lane`/`attempt`, `GET /queue` (own runs only, others as a count), cancel of a queued run ends it at once without running, no live host fails at once (skipped when every workflow has one) |
| `test_takes.py` | an echo output is a take (`made_with`, prompt, no seed or model, signed `url`/`thumb_url`), titles, newest first, filters (kind, source, starred, session), `q` search over prompt and title, stable cursor pages, 422s, rename and star, reuse (posts as a run), soft delete, a deleted session keeps its takes |
| `test_uploads.py` | POST /uploads → PUT the signed link → complete: a PNG becomes a take of its size; pending is hidden and 409; single-use PUT; bad type/size 422; a text file sent as PNG is 422 and discarded; tampered, re-dated, sibling, GET-signed links 403, wrong length 411, wrong type 415 |
| `test_isolation.py` | user B gets 404 for A's session, runs, events and cancel; A's output file only through its signed link (missing, tampered or re-dated signatures and sibling keys are 403); B gets 404 on A's takes and pending upload (get, patch, delete, reuse, complete), never lists them, and cannot PUT under A's prefix with its own signature |
| `test_batches.py` | echo batches (pipelines_service): 6 pasted lines end `done` with a run (lane `batch`, `batch_id`/`batch_row`) and a take each; row 1 ends before any other row's run is created; table and grid rows and inputs (first axis slowest); seeds 422 on echo (repeats checked only when a live fake workflow has a seed param); Submit 422s (bad row named, 600 rows, unknown workflow, no live host); pause holds queued rows, resume finishes; stop cancels the rest and ends `stopped`; a `fail=true` row fails alone and Retry reruns just it (attempt 2, new run); an interactive run jumps the waiting batch rows; batch events ordered (JSON, SSE, `since`, `Last-Event-ID`); B gets 404 on A's batch; the batch's takes in `GET /takes` (`source: pipeline`) |
| `test_user_workflows.py` | user workflows (API 1.5): `GET /nodes` (echo kinds with their schemas, `env` filter), copying the echo template (`mine`, same graph), PUT with `version` (stale 409) and PATCH, templates read-only (409), every `fixtures/graphs` valid/invalid graph through validate and save (expected problems from `contracts/check.py` against the live palette, equal to the fixture's own when all its kinds are live; caps 422), `unknown_kind` 422 on run and batch, a run of the edited workflow (one take per output node, `made_with`/`workflow_version`), reuse `current` → `changed` after an edit → `deleted` (running it 422), delete 409 while a run or batch is unfinished, a batch over a user workflow, isolation (B: 404 on get/put/patch/delete/copy, 422 `unknown workflow` on run and batch, B's list only templates and B's own) |
| `test_elements.py` | elements (API 1.8): library CRUD with `version` (stale 409), name/handle/text rules, handle unique per owner (409), image rules (deleted, foreign, pending, unknown 404; 0 or 5 and duplicates 422), a deleted take drops out, `q`/`kind`/`group`/`limit`, the 200 cap (a fresh user); an echo run mentioning an element (resolved prompt and appended `echo.refs` in `made_with.inputs`, `typed`, `elements` with its version, no element parents, twice appends once), unknown handles 422, `@@`; reuse `current` → `changed` → `deleted`; a lines batch resolved per row against its copy and unknown rows listed at Submit; a pipeline input bound to a step prompt; isolation; every `fixtures/mentions/resolve.*` case on echo against `check.py resolve_mentions` over the live graph and palette. Not covered: `not an image` / `has no size` (the fake runner makes only sized images) |
| `test_pipelines.py` | saved pipelines (API 1.7): CRUD with `version` (stale 409), PATCH, copy, newest first, the 100 cap (a fresh user); every `fixtures/pipelines` document through validate and save (expected problems from `contracts/check.py` `pipeline_problems` against the live workflows and palette, its save-blocking ones equal to the fixture's); `unknown pipeline` / `pipeline cannot run` at Submit; an echo → echo chain over 3 lines (6 takes, `parents`/`children`, `made_with.pipeline`, cells, snapshot kept after an edit, delete 409 while it runs); a gate: rows park, the batch is `waiting`, the review grid, refused picks (422/409/404), picks in reverse row order, a repeated pick sends nothing, a finished row is 409; a reject loop capped at 2 (new takes each visit, `then` ends the row with no takes, stacked cell attempts, the execution step list with loop counts and edges, the state after a step while the batch lives, 404 after); Retry row from here (earlier runs and takes reused, `retried_from`, `?attempt=`, 422 for an unknown step, 409 once done); Pause / Resume / Stop with parked rows (a pick while paused is held); Save as pipeline (a two-step chain, a title, an upload as an Image input, 422 for an upload); isolation (B: 404 on every pipeline, review, pick, execution, state, retry and save-as route of A's, `unknown pipeline` at Submit); a workflow batch has no execution view |
| `test_loras.py` | LoRAs (API 1.9): own uploads of synthetic safetensors built from `contracts/fixtures/loras` (peft, kohya, comfyui → ready with rank, size, key style, family, licence, sha256; `upload not received` before the PUT); every upload check that rejects (text, pickle, zip, an 8 MB header, not an object, a full model, a stray key, a dtype, offsets past the file, broken pairs, rank over 4096; listed `rejected` with the reason); create 422s (`.safetensors` only, over 2 GB); the 50-LoRA and 20 GB caps (fresh users); rename, delete, unknown ids 404; list order and `source`/`family`/`q`; shared LoRAs (skipped when no live host publishes one: 409 on rename/delete, 404 on complete). With a fake LoRA stack (`echo/lora-stack`, skipped otherwise): a user workflow prompt → stack → echo; an empty stack; `made_with.loras` and reuse `current`; the run 422s (strength, duplicate, unknown, pending, rejected, family unknown, 9 rows); a deleted LoRA (reuse `deleted`, a new run 422, a queued run fails `LoRA <name> was deleted`); a grid batch over strengths 0.5 and 1.0; isolation (B: 404 on A's LoRA, B's list = shared + B's own; B's run, batch, workflow (`lora_unknown`) and pipeline naming A's id refused without A's LoRA name) |
| `test_looks.py` | looks (API 1.10): the echo pack's starter looks (`fixtures/registry/looks.valid.json`) listed for every user, `GET /library/echo/<id>`, PUT/DELETE 409, unknown 404; `fits`/`reason` for `?workflow=` (echo, and the fake LoRA stack when there is one); own look CRUD with `version` (stale 409), the look rules, filters (`kind`, `group`, `source`, `family`, `q`, `limit`), the 200 cap shared with elements (a fresh user); Save as look (text as typed, usable LoRAs without mode, no images, upload 422, foreign/deleted 404, `from_take_id` null once the take is deleted); runs with looks (the resolved prompt after the element lines, look images after the element rows, a repeated look once, identical lines once, `made_with.looks`, `Run.looks`), unknown and not-fitting looks 422, a LoRA look on the stack (the user's own row wins); reuse `current` → `changed` → `deleted`; a lines batch and a table `looks` column against the batch's copy, a pipeline batch's `looks` 422; a pipeline step's `looks`, `unknown_look` at Submit; isolation; every `fixtures/mentions/resolve.looks-*` case whose graph is echo against `check.py resolve_mentions`. Not covered: the echo image's drawn text (made_with is checked), starter thumbnails (none in the echo pack) |

`helpers.py` has the polling, the SSE reader and the run steps; `conftest.py` the clients, the users and `echo_done`,
user A's finished quick echo run that the runs, events and isolation tests share.
