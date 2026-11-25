#!/bin/sh
# Stops helmstudio and removes its containers and network. Nothing else is removed: the settings and secrets
# (~/.helmstudio, HELMSTUDIO_HOME), the data (Docker volumes) and this folder stay; this says how to remove them.
set -eu
cd "$(dirname "$0")"
[ -f .env ] || { echo "error: no .env here: nothing was installed from this folder" >&2; exit 1; }
docker compose down
p=$(sed -n 's/^COMPOSE_PROJECT_NAME=//p' .env)
p=${COMPOSE_PROJECT_NAME:-$p}
home=$(sed -n 's/^HELMSTUDIO_HOME=//p' .env)
cat <<EOF
helmstudio is stopped. Kept:
  the data          Docker volumes ${p}_pgdata18a ${p}_natsdata ${p}_seaweeddata
  settings, secrets $home (the keys that open that data, the admin account)
  the pack          $(pwd)
To delete everything for good (it cannot be undone):
  docker volume rm ${p}_pgdata18a ${p}_natsdata ${p}_seaweeddata
  rm -rf "$home" "$(pwd)"
EOF
