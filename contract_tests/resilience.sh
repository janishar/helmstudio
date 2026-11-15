#!/bin/sh
# Resilience check on the running test stack (compose project helmstudio-test, Kong on 8390; never the dev stack): for
# each service, start a long echo run, restart the service mid-run, and require that the run ends (succeeded, or
# failed/cancelled with an error, never stuck queued/running) and that a new run afterwards succeeds.
#   make resilience                (from the repo root: brings the test stack up and runs this)
#   API_KEY=... ADMIN_EMAIL=admin@helm.test ADMIN_PASSWORD=... ./resilience.sh [svc ...]
# Default services: studio studio-queued runner nats postgres kong batch-pipelines batch-studio batch-nats
# batch-postgres batch-kill pipeline-parked; studio-queued restarts studio while a second run is still queued behind the first (not yet
# released) and requires that it then succeeds. batch-<svc> restarts <svc> during a 12-row slow echo batch (batch-kill:
# SIGKILL the pipelines container, then start it), presses Resume whenever the batch is paused (pipelines pauses its
# batches after a restart) and requires the batch to end done with exactly 12 runs (one per row) and 12 takes.
# pipeline-parked runs a 3-row echo pipeline with a gate (still -> review -> edit -> out), picks row 1 and restarts
# pipelines and studio while row 1's edit runs and rows 2 and 3 are parked, then presses Resume whenever the batch is
# paused and picks every parked row; the batch must end done with exactly 6 runs and 6 distinct takes (a still and an
# edit per row) and every row succeeded with one take.
# Needs curl and python3. Exit 1 when any service fails.
set -eu
cd "$(dirname "$0")"
BASE_URL="${BASE_URL:-http://127.0.0.1:8390}"
: "${API_KEY:?}" "${ADMIN_EMAIL:?}" "${ADMIN_PASSWORD:?}"
END_TIMEOUT="${END_TIMEOUT:-180}"   # seconds a run may take to end after the restart (the lease sweep needs ~60 s)
dc() {
  KONG_HTTP_PORT=8390 POSTGRES_HOST_PORT=5464 NATS_HOST_PORT=4323 S3_HOST_PORT=8434 HELM_NET=172.30.98 IMAGE_TAG=gate \
    docker compose -p helmstudio-test -f ../deploy/docker-compose.yml -f ../deploy/compose/fake.yml "$@"
}
field() { python3 -c 'import json,sys; v=json.load(sys.stdin); [v:=v[k] for k in sys.argv[1:]]; print("" if v is None else v)' "$@" 2>/dev/null; }

# api METHOD PATH [JSON]: the body on 2xx, else empty and a non-zero status (curl fails while a service is down)
# (a POST or DELETE always sends a body, empty if none: without Content-Length Kong answers 411)
api() {
  if [ "$1" = GET ]; then
    curl -sf -m 10 -H "X-API-Key: $API_KEY" -H "Authorization: Bearer $TOKEN" "$BASE_URL$2"
  else
    curl -sf -m 10 -X "$1" -H "X-API-Key: $API_KEY" -H "Authorization: Bearer $TOKEN" \
      -H 'Content-Type: application/json' -d "${3:-}" "$BASE_URL$2"
  fi
}
signin() {
  body=$(python3 -c 'import json,os; print(json.dumps({"email": os.environ["ADMIN_EMAIL"], "password": os.environ["ADMIN_PASSWORD"]}))')
  TOKEN=$(curl -sf -m 10 -H "X-API-Key: $API_KEY" -H 'Content-Type: application/json' -d "$body" \
    "$BASE_URL/auth/signin" | field access_token)
}
# run SID STEPS: a new run's id (retries while the stack comes back)
run() {
  for _ in $(seq 30); do
    id=$(api POST "/v1/studio/sessions/$1/runs" \
      "{\"workflow_id\":\"echo\",\"inputs\":{\"prompt.text\":\"resilience\",\"echo.steps\":$2,\"echo.delay_ms\":300}}" |
      field id) && [ -n "$id" ] && { echo "$id"; return; }
    sleep 2
  done
  return 1
}
# wait_status RID STATUSES SECONDS: prints the status once it is one of STATUSES (space-separated), else the last seen
wait_status() {
  last=unknown
  for _ in $(seq "$3"); do
    last=$(api GET "/v1/studio/runs/$1" | field status) || last=unreachable
    case " $2 " in *" $last "*) echo "$last"; return 0 ;; esac
    sleep 1
  done
  echo "$last"
  return 1
}

# batch_check batch-SVC: the crash test of one service during a batch (see the header)
batch_check() {
  target=${1#batch-}
  bsid=$(api POST /v1/studio/sessions '{"title":"resilience batch"}' | field id)
  body=$(python3 -c 'import json,sys; print(json.dumps({"workflow_id": "echo", "session_id": sys.argv[1],
    "inputs": {"echo.steps": 5, "echo.delay_ms": 300},
    "rows": {"lines": {"param": "prompt.text", "values": [f"row {i}" for i in range(1, 13)]}}}))' "$bsid")
  bid=$(api POST /v1/studio/batches "$body" | field id) || bid=""
  [ -n "$bid" ] || { fails=$((fails + 1)); echo "FAIL $1: the batch was not accepted"; return; }
  for _ in $(seq 60); do                     # mid-batch: row 1 and one more row done, the rest still to go
    [ "$(api GET "/v1/studio/batches/$bid" | field counts succeeded || echo 0)" -ge 2 ] 2>/dev/null && break
    sleep 1
  done
  if [ "$target" = kill ]; then
    dc kill -s KILL pipelines >/dev/null 2>&1
    dc start pipelines >/dev/null 2>&1
  else
    dc restart "$target" >/dev/null 2>&1
  fi
  dc up -d --wait >/dev/null 2>&1
  signin
  status=unknown
  for _ in $(seq $((END_TIMEOUT * 2))); do
    status=$(api GET "/v1/studio/batches/$bid" | field status) || status=unreachable
    case $status in
      done | failed | stopped) break ;;
      paused) api POST "/v1/studio/batches/$bid/resume" >/dev/null || true ;;
    esac
    sleep 1
  done
  # runs of this batch (and their distinct rows), takes of this batch (and distinct ids)
  runs=$(api GET "/v1/studio/sessions/$bsid/runs" | python3 -c 'import json,sys
r=[x for x in json.load(sys.stdin)["runs"] if x.get("batch_id")==sys.argv[1]]
print(len(r), len({x["batch_row"] for x in r}))' "$bid" 2>/dev/null) || runs="? ?"
  takes=$(api GET "/v1/studio/sessions/$bsid/takes?limit=200" | python3 -c 'import json,sys
t=[x for x in json.load(sys.stdin)["takes"] if x.get("batch_id")==sys.argv[1]]
print(len(t), len({x["id"] for x in t}))' "$bid" 2>/dev/null) || takes="? ?"
  verdict=PASS
  [ "$status" = done ] && [ "$runs" = "12 12" ] && [ "$takes" = "12 12" ] || verdict=FAIL
  [ $verdict = PASS ] || fails=$((fails + 1))
  echo "$verdict $1: batch -> $status; runs (rows) $runs; takes (distinct) $takes"
  api DELETE "/v1/studio/sessions/$bsid" >/dev/null || true
}

# pick_parked BID: pick the first candidate of every row parked on a gate
pick_parked() {
  api GET "/v1/studio/batches/$1/review" | python3 -c 'import json,sys
for i in json.load(sys.stdin)["items"]:
    print(i["row"], json.dumps({"gate": i["gate"], "take_ids": [i["candidates"][0]["take_id"]]}))' 2>/dev/null |
    while read -r n pick; do api POST "/v1/studio/batches/$1/rows/$n/pick" "$pick" >/dev/null || true; done
}

# pipeline_check: restarts while two rows are parked and one is running (see the header)
pipeline_check() {
  doc=$(python3 -c 'import json
step = lambda bind, steps: {"kind": "step", "workflow": "echo", "bind": bind,
                            "inputs": {"echo.steps": steps, "echo.delay_ms": 300}}
print(json.dumps({"title": "resilience pipeline", "nodes": {
  "inputs": {"kind": "inputs", "inputs": [{"name": "prompt", "type": "Text"}]},
  "still": step({"prompt.text": "$prompt"}, 2),
  "review": {"kind": "gate", "takes": "still"},
  "edit": step({"prompt.text": "$prompt", "echo.refs": "review.picked"}, 40),
  "out": {"kind": "output", "from": "edit.takes", "collection": "resilience"}},
  "edges": [["inputs", "still"], ["still", "review"], ["review", "edit"], ["edit", "out"]]}))')
  pid=$(api POST /v1/studio/pipelines "$doc" | field id) || pid=""
  bsid=$(api POST /v1/studio/sessions '{"title":"resilience pipeline"}' | field id)
  body=$(python3 -c 'import json,sys; print(json.dumps({"pipeline_id": sys.argv[1], "session_id": sys.argv[2],
    "rows": {"lines": {"param": "inputs.prompt", "values": ["row 1", "row 2", "row 3"]}}}))' "$pid" "$bsid")
  bid=$(api POST /v1/studio/batches "$body" | field id) || bid=""
  [ -n "$bid" ] || { fails=$((fails + 1)); echo "FAIL $1: the pipeline batch was not accepted"; return; }
  for _ in $(seq 60); do                     # every row parked at the gate
    [ "$(api GET "/v1/studio/batches/$bid" | field counts waiting || echo 0)" -ge 3 ] 2>/dev/null && break
    sleep 1
  done
  api POST "/v1/studio/batches/$bid/rows/1/pick" "$(api GET "/v1/studio/batches/$bid/review" | python3 -c 'import json,sys
i = json.load(sys.stdin)["items"][0]; print(json.dumps({"gate": i["gate"], "take_ids": [i["candidates"][0]["take_id"]]}))')" \
    >/dev/null || true
  sleep 4                                    # row 1's edit (40 x 300 ms) is running; rows 2 and 3 are parked
  dc restart pipelines studio >/dev/null 2>&1
  dc up -d --wait >/dev/null 2>&1
  signin
  status=unknown
  for _ in $(seq $((END_TIMEOUT * 2))); do
    status=$(api GET "/v1/studio/batches/$bid" | field status) || status=unreachable
    case $status in
      done | failed | stopped) break ;;
      paused) api POST "/v1/studio/batches/$bid/resume" >/dev/null || true ;;
      waiting) pick_parked "$bid" ;;
    esac
    sleep 1
  done
  runs=$(api GET "/v1/studio/sessions/$bsid/runs" | python3 -c 'import json,sys
r=[x for x in json.load(sys.stdin)["runs"] if x.get("batch_id")==sys.argv[1]]
print(len(r), len({x["id"] for x in r}))' "$bid" 2>/dev/null) || runs="? ?"
  takes=$(api GET "/v1/studio/sessions/$bsid/takes?limit=200" | python3 -c 'import json,sys
t=[x for x in json.load(sys.stdin)["takes"] if x.get("batch_id")==sys.argv[1]]
print(len(t), len({x["id"] for x in t}))' "$bid" 2>/dev/null) || takes="? ?"
  rows=$(api GET "/v1/studio/batches/$bid/rows" | python3 -c 'import json,sys
print(" ".join(r["status"] + ":" + str(len(r["take_ids"])) for r in json.load(sys.stdin)["rows"]))' 2>/dev/null) || rows="?"
  verdict=PASS
  [ "$status" = done ] && [ "$runs" = "6 6" ] && [ "$takes" = "6 6" ] &&
    [ "$rows" = "succeeded:1 succeeded:1 succeeded:1" ] || verdict=FAIL
  [ $verdict = PASS ] || fails=$((fails + 1))
  echo "$verdict $1: batch -> $status; runs (distinct) $runs; takes (distinct) $takes; rows $rows"
  api DELETE "/v1/studio/sessions/$bsid" >/dev/null || true
  api DELETE "/v1/studio/pipelines/$pid" >/dev/null || true
}

signin
SID=$(api POST /v1/studio/sessions '{"title":"resilience"}' | field id)
fails=0
for svc in ${*:-studio studio-queued runner nats postgres kong batch-pipelines batch-studio batch-nats batch-postgres batch-kill pipeline-parked}; do
  case $svc in
    batch-*) batch_check "$svc"; continue ;;
    pipeline-parked) pipeline_check "$svc"; continue ;;
  esac
  rid=$(run "$SID" 40)                       # 40 steps x 300 ms: ~12 s of work
  wait_status "$rid" "running" 30 >/dev/null || true
  sleep 2
  if [ "$svc" = studio-queued ]; then        # restart studio while a second run waits, not yet released
    hold=$rid
    rid=$(run "$SID" 3)
    was=$(api GET "/v1/studio/runs/$rid" | field status || true)
    dc restart studio >/dev/null 2>&1
    wait_status "$hold" "succeeded failed cancelled" "$END_TIMEOUT" >/dev/null || true
  else
    dc restart "$svc" >/dev/null 2>&1
  fi
  end=$(wait_status "$rid" "succeeded failed cancelled" "$END_TIMEOUT") || true
  detail=$(api GET "/v1/studio/runs/$rid" | field error || true)
  dc up -d --wait >/dev/null 2>&1            # everything healthy again before the next run
  signin
  next=$(run "$SID" 3)
  after=$(wait_status "$next" "succeeded failed cancelled" 120) || true
  verdict=PASS
  case "$end" in succeeded | failed | cancelled) ;; *) verdict=FAIL ;; esac
  [ "$after" = succeeded ] || verdict=FAIL
  # the queued run was never sent, so the restart must not cost it anything: it runs once the host is free
  [ "$svc" != studio-queued ] || { [ "$was" = queued ] && [ "$end" = succeeded ]; } || verdict=FAIL
  [ $verdict = PASS ] || fails=$((fails + 1))
  echo "$verdict restart $svc: run during restart -> $end${detail:+ ($detail)}; next run -> $after"
done
api DELETE "/v1/studio/sessions/$SID" >/dev/null || true
exit $((fails > 0))
