#!/bin/sh
# The release stack's first-start step (compose.yml, service `secrets`), on every `up`; safe to re-run.
# - The secrets: .tools/make-secrets into the keys volume (/stack/deploy/secrets), as for a source install; it makes
#   what is missing and never overwrites a secret. One GPU host, HELMSTUDIO_GPU_HOST (default
#   `gpu`): its torch and native NATS users and S3 identity, for the host runners (make runners-nvidia STACK=release).
# - The admin: HELMSTUDIO_ADMIN_EMAIL and HELMSTUDIO_ADMIN_PASSWORD are saved on the first start only (later values
#   are ignored); auth reads them on every start (without them it would demote every admin) and the `admin` step
#   creates the account with them.
# - Each service's own secrets, copied to keys/run/<service>/ (mounted at its /run/secrets, the paths its *_FILE
#   variables name), and this version's config files (nats.conf, postgres init.sh, the one-shots' scripts, the
#   built-in plugins) into the config volume (/config). Files are overwritten in place: running containers keep their
#   mounts.
set -eu
s=/stack/deploy/secrets
h=${HELMSTUDIO_GPU_HOST:-gpu}
GPU_HOSTS=$h sh /stack/.tools/make-secrets
cd "$s"

if ! [ -s admin_email ]; then
  e=${HELMSTUDIO_ADMIN_EMAIL:-} p=${HELMSTUDIO_ADMIN_PASSWORD:-}
  if [ -z "$e" ] || [ -z "$p" ]; then
    echo "error: the first start needs the admin: set HELMSTUDIO_ADMIN_EMAIL and HELMSTUDIO_ADMIN_PASSWORD (in a .env" \
      "beside compose.yml, or in the environment), then docker compose up -d again" >&2
    exit 1
  fi
  case $e in *@*.*) ;; *) echo "error: HELMSTUDIO_ADMIN_EMAIL: an email address" >&2; exit 1;; esac
  [ "${#p}" -ge 10 ] || { echo "error: HELMSTUDIO_ADMIN_PASSWORD: at least 10 characters" >&2; exit 1; }
  (umask 077 && printf %s "$p" >admin_password && printf %s "$e" >admin_email)   # the email last: it marks done
  echo "saved the admin $e (first start)"
fi

put() { # service file[=name]...: copies into run/<service>/ (in place, so the inode stays)
  d=run/$1 && shift && mkdir -p "$d" && chmod 755 run "$d"
  for f; do cp "${f%%=*}" "$d/${f#*=}" && chmod 644 "$d/${f#*=}"; done
}
put auth auth_database_url nats_auth_url jwt_private_key web_api_key helm_api_key admin_email admin_password
chown 10001 run/auth/admin_password && chmod 600 run/auth/admin_password   # auth runs as uid 10001
put admin auth_database_url admin_email admin_password
chown 10001 run/admin/admin_password && chmod 600 run/admin/admin_password
put studio studio_database_url nats_studio_url file_url_secret s3_access_key_id s3_secret_access_key
put files file_url_secret s3_access_key_id s3_secret_access_key
put web web_api_key
put pipelines pipelines_database_url nats_pipelines_url
mkdir -p run/pipelines/providers   # the mount point of providers/ (writable, uid 10001: make-secrets)
put nats nats_admin_password nats_auth_password nats_studio_password nats_runner_password \
  nats_runner_mac_torch_password nats_runner_mac_mlx_password nats_runner_mac_native_password nats_pipelines_password \
  nats_gpu_hosts.conf=nats_gpu_hosts
put nats-setup nats_admin_url
put postgres postgres_password auth_db_password studio_db_password pipelines_db_password
put seaweedfs s3_access_key_id s3_secret_access_key s3_config.json=s3_config

mkdir -p /config/nats /config/postgres /config/tools /config/plugins
for f in /stack/deploy/nats/*; do cat "$f" >"/config/nats/${f##*/}"; done
cat /stack/deploy/postgres/init.sh >/config/postgres/init.sh
for f in nats-setup storage-setup; do cat "/stack/.tools/$f" >"/config/tools/$f"; done
rm -rf /config/plugins/* && cp -R /stack/plugins/. /config/plugins/
echo "secrets and config ready"
