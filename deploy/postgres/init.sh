#!/bin/sh
# One database + owner role per service, plus catalog_service's scratch test database. Runs on an empty ora-catalog
# pgdata volume; safe to re-run (it skips what exists), so .tools/test-db and an existing stack can add a new service:
#   docker compose -f deploy/docker-compose.yml exec postgres sh /docker-entrypoint-initdb.d/init.sh
# Passwords come from the compose secrets <svc>_db_password (.tools/make-secrets); <svc>_database_url holds the same.
set -eu
q() { psql -v ON_ERROR_STOP=1 -U "${POSTGRES_USER:-postgres}" -d postgres -tA "$@"; }
for svc in auth catalog agents; do
  if [ -z "$(q -c "SELECT 1 FROM pg_roles WHERE rolname = '$svc'")" ]; then
    q -v svc="$svc" -v pw="$(cat "/run/secrets/${svc}_db_password")" <<'SQL'
CREATE ROLE :"svc" LOGIN PASSWORD :'pw';
SQL
  fi
  for db in "$svc" $([ "$svc" = catalog ] && echo catalog_test); do   # catalog_test: catalog_service/.test.env
    [ -n "$(q -c "SELECT 1 FROM pg_database WHERE datname = '$db'")" ] && continue
    q -c "CREATE DATABASE \"$db\" OWNER \"$svc\"" -c "REVOKE ALL ON DATABASE \"$db\" FROM PUBLIC"
  done
done
