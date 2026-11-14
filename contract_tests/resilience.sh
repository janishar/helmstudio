#!/bin/sh
# Resilience check on the running test stack (compose project helmstudio-test, Kong on 8390; never the dev stack): for
# each service, start a long echo run, restart the service mid-run, and require that the run ends (succeeded, or
# failed/cancelled with an error, never stuck queued/running) and that a new run afterwards succeeds.
#   make resilience                (from the repo root: brings the test stack up and runs this)
#   API_KEY=... ADMIN_EMAIL=admin@helm.test ADMIN_PASSWORD=... ./resilience.sh [svc ...]
# Default services: studio studio-queued runner nats postgres kong; studio-queued restarts studio while a second run
# is still queued behind the first (not yet released) and requires that it then succeeds. Needs curl and python3. Exit 1 when any service fails.
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
api() {
  curl -sf -m 10 -X "$1" -H "X-API-Key: $API_KEY" -H "Authorization: Bearer $TOKEN" \
    -H 'Content-Type: application/json' ${3:+-d "$3"} "$BASE_URL$2"
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

signin
SID=$(api POST /v1/studio/sessions '{"title":"resilience"}' | field id)
fails=0
for svc in ${*:-studio studio-queued runner nats postgres kong}; do
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
