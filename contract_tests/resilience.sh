#!/bin/sh
# Resilience check on the running test stack (compose project helmstudio-test, Kong on 8390; never the dev stack): for
# each service, start a long echo run, restart the service mid-run, and require that the run ends (succeeded, or
# failed/cancelled with an error, never stuck queued/running) and that a new run afterwards succeeds.
#   make resilience                (from the repo root: brings the test stack up and runs this)
#   API_KEY=... ADMIN_EMAIL=admin@helm.test ADMIN_PASSWORD=... ./resilience.sh [svc ...]
# Default services: studio studio-queued runner nats postgres kong batch-pipelines batch-studio batch-nats
# batch-postgres batch-kill pipeline-parked agents-parked director-mid handover-studio; studio-queued restarts studio while a second run is still queued behind the first (not yet
# released) and requires that it then succeeds. batch-<svc> restarts <svc> during a 12-row slow echo batch (batch-kill:
# SIGKILL the pipelines container, then start it), presses Resume whenever the batch is paused (pipelines pauses its
# batches after a restart) and requires the batch to end done with exactly 12 runs (one per row) and 12 takes.
# pipeline-parked runs a 3-row echo pipeline with a gate (still -> review -> edit -> out), picks row 1 and restarts
# pipelines and studio while row 1's edit runs and rows 2 and 3 are parked, then presses Resume whenever the batch is
# paused and picks every parked row; the batch must end done with exactly 6 runs and 6 distinct takes (a still and an
# edit per row) and every row succeeded with one take.
# agents-parked (API 1.14) runs a 3-row pipeline still -> judge (Suggest, echo-ask) -> out with slow stills, and once
# row 1 is parked on the judge's suggestion restarts pipelines and studio while the other rows make stills or wait
# on their judge calls; it presses Resume when paused and picks every suggestion: the batch must end done with
# exactly 3 still runs, 3 judge runs and one decision per row (no decision asked twice) and 3 rows succeeded:1.
# director-mid (API 1.15) runs a 1-row pipeline director (echo-ask, a list of 3 shots) -> shots (fan-out, slow echo) ->
# out, and once one shot is done tells the director in its chat to add a row of 2 shots, then restarts pipelines while
# the director is mid-reply and the fan-out half done; it presses Resume when paused: the batch must end done with
# exactly 2 rows (succeeded with 3 and 2 takes), 5 distinct shot runs and takes, and one director reply.
# handover-studio restarts studio while a run waits for an unload (see handover_check; skipped on the fake stack).
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

# agents_check: restarts while rows wait on judge calls and row 1 is parked on a suggestion (see the header)
agents_check() {
  if ! api GET /v1/studio/workflows/echo-ask >/dev/null; then
    echo "SKIP $1: the fake runner has no echo-ask (before API 1.14)"
    return
  fi
  doc=$(python3 -c 'import json
print(json.dumps({"title": "resilience agents", "nodes": {
  "inputs": {"kind": "inputs", "inputs": [{"name": "prompt", "type": "Text"}]},
  "still": {"kind": "step", "workflow": "echo", "bind": {"prompt.text": "$prompt"},
            "inputs": {"echo.steps": 12, "echo.delay_ms": 300}},
  "judge": {"kind": "judge", "takes": "still", "criteria": {"must": ["Shows the subject"]}, "mode": "suggest"},
  "out": {"kind": "output", "from": "judge.picked", "collection": "resilience agents"}},
  "edges": [["inputs", "still"], ["still", "judge"], ["judge", "out"]]}))')
  pid=$(api POST /v1/studio/pipelines "$doc" | field id) || pid=""
  bsid=$(api POST /v1/studio/sessions '{"title":"resilience agents"}' | field id)
  body=$(python3 -c 'import json,sys
ans = json.dumps({"takes": [{"take": 1, "checks": [True], "reason": "fine"}], "ranking": [1]})
print(json.dumps({"pipeline_id": sys.argv[1], "session_id": sys.argv[2], "rows": {"lines": {"param": "inputs.prompt",
  "values": [f"agents row {i} ECHO-ANSWER: {ans}" for i in (1, 2, 3)]}}}))' "$pid" "$bsid")
  bid=$(api POST /v1/studio/batches "$body" | field id) || bid=""
  [ -n "$bid" ] || { fails=$((fails + 1)); echo "FAIL $1: the pipeline batch was not accepted"; return; }
  for _ in $(seq 60); do                     # row 1 parked on its suggestion; rows 2 and 3 still to judge
    [ "$(api GET "/v1/studio/batches/$bid" | field counts waiting || echo 0)" -ge 1 ] 2>/dev/null && break
    sleep 1
  done
  dc restart pipelines studio >/dev/null 2>&1
  dc up -d --wait >/dev/null 2>&1
  signin
  status=unknown
  for _ in $(seq $((END_TIMEOUT * 2))); do
    status=$(api GET "/v1/studio/batches/$bid" | field status) || status=unreachable
    case $status in
      done | failed | stopped) break ;;
      paused) api POST "/v1/studio/batches/$bid/resume" >/dev/null || true ;;
      waiting)                               # agree with every suggestion
        api GET "/v1/studio/batches/$bid/review" | python3 -c 'import json,sys
for i in json.load(sys.stdin)["items"]:
    print(i["row"], json.dumps({"gate": i["gate"], "take_ids": i.get("suggested") or [i["candidates"][0]["take_id"]]}))' 2>/dev/null |
          while read -r n pick; do api POST "/v1/studio/batches/$bid/rows/$n/pick" "$pick" >/dev/null || true; done ;;
    esac
    sleep 1
  done
  runs=$(api GET "/v1/studio/sessions/$bsid/runs" | python3 -c 'import json,sys
r=[x for x in json.load(sys.stdin)["runs"] if x.get("batch_id")==sys.argv[1]]
print(sum(x["workflow_id"]=="echo" for x in r), sum(x["workflow_id"]=="echo-ask" for x in r), len({x["id"] for x in r}))' "$bid" 2>/dev/null) || runs="? ? ?"
  decided=$(for n in 1 2 3; do api GET "/v1/studio/batches/$bid/rows/$n/decisions" | python3 -c 'import json,sys
d=json.load(sys.stdin)["decisions"]; print(str(len(d)) + ":" + str(sum(len(x["run_ids"]) for x in d)))' 2>/dev/null || echo "?"; done | tr '\n' ' ')
  rows=$(api GET "/v1/studio/batches/$bid/rows" | python3 -c 'import json,sys
print(" ".join(r["status"] + ":" + str(len(r["take_ids"])) for r in json.load(sys.stdin)["rows"]))' 2>/dev/null) || rows="?"
  verdict=PASS
  [ "$status" = done ] && [ "$runs" = "3 3 6" ] && [ "$decided" = "1:1 1:1 1:1 " ] &&
    [ "$rows" = "succeeded:1 succeeded:1 succeeded:1" ] || verdict=FAIL
  [ $verdict = PASS ] || fails=$((fails + 1))
  echo "$verdict $1: batch -> $status; runs (stills, judge calls, distinct) $runs; decisions:runs per row $decided; rows $rows"
  api DELETE "/v1/studio/sessions/$bsid" >/dev/null || true
  api DELETE "/v1/studio/pipelines/$pid" >/dev/null || true
}

# director_check: restart pipelines while the director is mid-reply and a fan-out is half done (see the header)
director_check() {
  if ! api GET /v1/studio/workflows/echo-ask >/dev/null; then
    echo "SKIP $1: the fake runner has no echo-ask"
    return
  fi
  # a row's visit is one turn (set_items, route); the chat's turn 1 adds a row, its turn 2 answers. A script nested in
  # the chat's args is written with \u003a for its colon, so it stays inert until the args are decoded
  scripts=$(python3 -c 'import json,re
q = lambda v: re.sub(r"(ECHO-TURN \d+):", r"\1\\u003a", json.dumps(v, separators=(",", ":")))
go = {"tool": "route", "args": {"to": "go", "reason": "set"}}
row = lambda name, n: name + " ECHO-TURN 1: " + q({"say": "", "calls": [
  {"tool": "set_items", "args": {"items": [f"{name} shot {i}" for i in range(n)]}}, go]})
add = {"say": "", "calls": [{"tool": "rows_add", "args": {"inputs": {"script": row("director added", 2)}}}]}
print(json.dumps({"row": row("director first", 3), "chat": "Add a row ECHO-TURN 1: " + q(add) +
  " ECHO-TURN 2: " + q({"say": "Added.", "calls": []})}))')
  doc=$(python3 -c 'import json
print(json.dumps({"title": "resilience director", "nodes": {
  "inputs": {"kind": "inputs", "inputs": [{"name": "script", "type": "Text"}]},
  "director": {"kind": "agent", "instruction": "Make the shots.", "from": ["$script"], "model": "echo-ask",
               "director": True, "tools": ["rows.add"], "list": {"max": 3}, "wake_on_rows": "none",
               "routes": {"go": "make them", "stop": "none"}},
  "shots": {"kind": "step", "workflow": "echo", "each": "director.items", "bind": {"prompt.text": "director.item"},
            "inputs": {"echo.steps": 12, "echo.delay_ms": 300}},
  "out": {"kind": "output", "from": "shots.takes", "collection": "resilience director"}},
  "edges": [["inputs", "director"], ["director", {"routes": {"go": "shots", "stop": "out"}, "max": 1, "then": "go"}],
            ["shots", "out"]]}))')
  pid=$(api POST /v1/studio/pipelines "$doc" | field id) || pid=""
  bsid=$(api POST /v1/studio/sessions '{"title":"resilience director"}' | field id)
  body=$(printf "%s\n" "$scripts" | python3 -c 'import json,sys
s = json.load(sys.stdin)
print(json.dumps({"pipeline_id": sys.argv[1], "session_id": sys.argv[2],
  "rows": {"lines": {"param": "inputs.script", "values": [s["row"]]}}}))' "$pid" "$bsid")
  bid=$(api POST /v1/studio/batches "$body" | field id) || bid=""
  [ -n "$bid" ] || { fails=$((fails + 1)); echo "FAIL $1: the pipeline batch was not accepted"; return; }
  for _ in $(seq 60); do                     # one of row 1's three shots done
    [ "$(api GET "/v1/studio/batches/$bid/rows" | python3 -c 'import json,sys
r = json.load(sys.stdin)["rows"][0]
print(sum(a["status"] == "succeeded" for c in r.get("cells", []) if c["node"] == "shots" for a in c["attempts"]))' 2>/dev/null || echo 0)" -ge 1 ] && break
    sleep 1
  done
  api POST "/v1/studio/batches/$bid/chat" "$(printf "%s\n" "$scripts" | python3 -c 'import json,sys
print(json.dumps({"text": json.load(sys.stdin)["chat"]}))')" >/dev/null || true
  sleep 1                                    # its turn waits behind the shots on the fake host: mid-reply
  dc restart pipelines >/dev/null 2>&1
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
  runs=$(api GET "/v1/studio/sessions/$bsid/runs" | python3 -c 'import json,sys
r=[x for x in json.load(sys.stdin)["runs"] if x.get("batch_id")==sys.argv[1] and x["workflow_id"]=="echo"]
print(len(r), len({x["id"] for x in r}))' "$bid" 2>/dev/null) || runs="? ?"
  takes=$(api GET "/v1/studio/sessions/$bsid/takes?limit=200" | python3 -c 'import json,sys
t=[x for x in json.load(sys.stdin)["takes"] if x.get("batch_id")==sys.argv[1]]
print(len(t), len({x["id"] for x in t}))' "$bid" 2>/dev/null) || takes="? ?"
  rows=$(api GET "/v1/studio/batches/$bid/rows" | python3 -c 'import json,sys
print(" ".join(r["status"] + ":" + str(len(r["take_ids"])) for r in json.load(sys.stdin)["rows"]))' 2>/dev/null) || rows="?"
  replies=$(api GET "/v1/studio/batches/$bid/chat" | python3 -c 'import json,sys
print(sum(m["role"] == "director" for m in json.load(sys.stdin)["messages"]))' 2>/dev/null) || replies="?"
  verdict=PASS
  [ "$status" = done ] && [ "$runs" = "5 5" ] && [ "$takes" = "5 5" ] && [ "$replies" = 1 ] &&
    [ "$rows" = "succeeded:3 succeeded:2" ] || verdict=FAIL
  [ $verdict = PASS ] || fails=$((fails + 1))
  echo "$verdict $1: batch -> $status; shot runs (distinct) $runs; takes (distinct) $takes; rows $rows; director replies $replies"
  api DELETE "/v1/studio/sessions/$bsid" >/dev/null || true
  api DELETE "/v1/studio/pipelines/$pid" >/dev/null || true
}

# handover_check: restart studio while a run waits for another env of its host to unload (contracts/README.md, Two
# envs on one host: the hand-over). HANDOVER_HOLD runs first on one env and leaves its weights resident; HANDOVER_WAIT
# (another env of that host, too big to fit beside them) then waits for the unload while studio restarts. It must
# succeed with attempt 1 and exactly one `running` status: started once. Skipped when no host has two envs that
# report memory (the fake stack has one env, host fake, and no memory).
handover_check() {
  envs=$(api GET /v1/studio/hosts | python3 -c 'import json,sys,collections
c=collections.Counter(h["host"] for h in json.load(sys.stdin)["hosts"] if h.get("host_memory"))
print(max(c.values(), default=0))') || envs=0
  if [ "$envs" -lt 2 ] || [ -z "${HANDOVER_HOLD:-}" ] || [ -z "${HANDOVER_WAIT:-}" ]; then
    echo "SKIP $1: no host with two envs reporting memory, or HANDOVER_HOLD / HANDOVER_WAIT unset"
    return
  fi
  hsid=$(api POST /v1/studio/sessions '{"title":"resilience handover"}' | field id)   # its own event log (500 a page)
  hold=$(api POST "/v1/studio/sessions/$hsid/runs" "{\"workflow_id\":\"$HANDOVER_HOLD\",\"inputs\":{}}" | field id)
  wait_status "$hold" "succeeded failed cancelled" 120 >/dev/null || true
  rid=$(api POST "/v1/studio/sessions/$hsid/runs" "{\"workflow_id\":\"$HANDOVER_WAIT\",\"inputs\":{}}" | field id)
  sleep 1                                    # released only after the unload: still queued now
  was=$(api GET "/v1/studio/runs/$rid" | field status || true)
  dc restart studio >/dev/null 2>&1
  dc up -d --wait >/dev/null 2>&1
  signin
  end=$(wait_status "$rid" "succeeded failed cancelled" "$END_TIMEOUT") || true
  attempt=$(api GET "/v1/studio/runs/$rid" | field attempt || true)
  starts=$(api GET "/v1/studio/sessions/$hsid/events?since=0" | python3 -c 'import json,sys
print(sum(1 for e in json.load(sys.stdin)["events"] if e["run_id"]==sys.argv[1] and e["type"]=="status" and e["data"]["status"]=="running"))' "$rid") || starts=?
  verdict=PASS
  { [ "$was" = queued ] && [ "$end" = succeeded ] && [ "$attempt" = 1 ] && [ "$starts" = 1 ]; } || { verdict=FAIL; fails=$((fails + 1)); }
  api DELETE "/v1/studio/sessions/$hsid" >/dev/null || true
  echo "$verdict $1: waiting run was $was -> $end, attempt $attempt, started $starts time(s)"
}

signin
SID=$(api POST /v1/studio/sessions '{"title":"resilience"}' | field id)
fails=0
for svc in ${*:-studio studio-queued runner nats postgres kong batch-pipelines batch-studio batch-nats batch-postgres batch-kill pipeline-parked agents-parked director-mid handover-studio}; do
  case $svc in
    batch-*) batch_check "$svc"; continue ;;
    pipeline-parked) pipeline_check "$svc"; continue ;;
    agents-parked) agents_check "$svc"; continue ;;
    director-mid) director_check "$svc"; continue ;;
    handover-studio) handover_check "$svc"; continue ;;
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
