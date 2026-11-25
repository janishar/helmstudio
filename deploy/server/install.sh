#!/bin/sh
# Installs or updates helmstudio on a Linux machine (README.md). Two folders: ~/helmstudio (HELMSTUDIO_DIR) holds the
# downloaded pack, replaced whole by each update; ~/.helmstudio (HELMSTUDIO_HOME) holds what must survive updates: the
# settings (helmstudio.env) and the secrets (secrets/, with the admin's password and the providers' keys). The pack
# links to them: .env -> HELMSTUDIO_HOME/helmstudio.env, deploy/secrets -> HELMSTUDIO_HOME/secrets. The data is in
# Docker volumes.
#   curl -fsSL https://github.com/janishar/helmstudio/releases/latest/download/install.sh | sh
#   curl -fsSL https://github.com/janishar/helmstudio/releases/latest/download/install.sh | sh -s -- 0.1.0-alpha.4
#     no pack beside it (or HELMSTUDIO_FETCH=1, as update.sh does): finds the newest release (or takes the version),
#     downloads its server pack and SHA256SUMS, refuses the pack unless its checksum matches, stops a running install,
#     puts the pack in HELMSTUDIO_DIR in place of the old one and runs the unpacked install.sh
#   ./install.sh   in an unpacked pack: checks Docker, makes the settings and secrets (kept on a re-run), saves the
#     admin account, starts the stack on this pack's version and waits until it is healthy
# The admin: asked on the terminal, or HELMSTUDIO_ADMIN_EMAIL and HELMSTUDIO_ADMIN_PASSWORD_FILE. Never uses sudo.
# HELMSTUDIO_PACK_URL (a folder URL holding the pack and SHA256SUMS, file:// too) replaces the release's, for tests.
set -eu
die() { echo "error: $*" >&2; exit 1; }
repo=https://github.com/janishar/helmstudio
home=${HELMSTUDIO_HOME:-$HOME/.helmstudio}

need_docker() {
  command -v docker >/dev/null && return
  cat >&2 <<'EOF'
error: Docker is not installed. Install Docker Engine with the Compose plugin, let your user run it, then run this again:
  https://docs.docker.com/engine/install/        (for example: curl -fsSL https://get.docker.com | sudo sh)
  sudo usermod -aG docker "$USER"                 (then log out and in)
On Windows: Docker Desktop with the WSL2 backend, then run this in the WSL2 shell.
EOF
  exit 1
}

here=$(dirname "$0")
if [ -n "${HELMSTUDIO_FETCH:-}" ] || ! [ -f "$here/compose.yml" ] || ! [ -f "$here/VERSION" ]; then
  case $(uname -s)/$(uname -m) in
    Linux/x86_64|Linux/amd64|Linux/aarch64|Linux/arm64) ;;
    *) die "this installs on Linux (x86_64 or arm64), or Windows through WSL2; found $(uname -s) $(uname -m)" ;;
  esac
  for c in curl tar; do command -v "$c" >/dev/null || die "$c is needed"; done
  if command -v sha256sum >/dev/null; then sum="sha256sum -c -"
  elif command -v shasum >/dev/null; then sum="shasum -a 256 -c -"; else die "sha256sum is needed"; fi
  need_docker
  v=${1:-}
  v=${v#v}
  if [ -z "$v" ]; then
    [ -z "${HELMSTUDIO_PACK_URL:-}" ] || die "HELMSTUDIO_PACK_URL needs the version: sh -s -- <version>"
    # the newest release, prereleases too, from the public releases feed (no API token)
    v=$(curl -fsSL "$repo/releases.atom" | sed -n 's|.*/releases/tag/v\([0-9A-Za-z.-]*\)".*|\1|p' | head -n 1)
    [ -n "$v" ] || die "could not find the newest release at $repo/releases"
  fi
  case $v in *[!0-9A-Za-z.-]*) die "a version like 0.1.0-alpha.4";; esac
  base=${HELMSTUDIO_PACK_URL:-$repo/releases/download/v$v}
  pack=helmstudio-server-$v.tar.gz
  dir=${HELMSTUDIO_DIR:-$HOME/helmstudio}
  dir=${dir%/}
  [ "$dir" != "${home%/}" ] || die "HELMSTUDIO_DIR and HELMSTUDIO_HOME must be different folders"
  if [ -d "$dir" ] && [ -n "$(ls -A "$dir")" ] && ! { [ -f "$dir/compose.yml" ] && [ -f "$dir/VERSION" ]; }; then
    die "$dir is not a helmstudio pack folder: set HELMSTUDIO_DIR to another one"
  fi
  mkdir -p "$(dirname "$dir")"
  new=$(mktemp -d "$dir.new.XXXXXX") && trap 'rm -rf "$new"' EXIT
  echo "helmstudio $v: downloading the server pack"
  curl -fsSL -o "$new/$pack" "$base/$pack" || die "no server pack at $base/$pack (releases before 0.1.0-alpha.4 have none)"
  curl -fsSL -o "$new/SHA256SUMS" "$base/SHA256SUMS" || die "no SHA256SUMS at $base"
  (cd "$new" && grep "  $pack\$" SHA256SUMS | $sum >/dev/null) || die "the pack's checksum does not match SHA256SUMS: not installed"
  mkdir "$new/pack" && tar -xzf "$new/$pack" -C "$new/pack" --strip-components=1
  if [ -f "$dir/.env" ]; then echo "stopping the running install"; (cd "$dir" && docker compose down); fi
  rm -rf "$dir" && mv "$new/pack" "$dir"
  rm -rf "$new" && trap - EXIT
  echo "helmstudio $v unpacked into $dir"
  unset HELMSTUDIO_FETCH
  # piped into sh, stdin is this script: the unpacked install.sh asks for the admin on the terminal if there is one
  if (exec </dev/tty) 2>/dev/null; then exec sh "$dir/install.sh" </dev/tty; else exec sh "$dir/install.sh" </dev/null; fi
fi

cd "$here"
need_docker
docker info >/dev/null 2>&1 || die "Docker is not running, or this user may not use it (add it to the docker group)"
cv=$(docker compose version --short 2>/dev/null) || die "the Docker Compose plugin is missing (docker compose)"
cv=${cv#v}
maj=${cv%%.*} rest=${cv#*.}
[ "$maj" -gt 2 ] || { [ "$maj" -eq 2 ] && [ "${rest%%.*}" -ge 24 ]; } || die "Docker Compose 2.24 or later is needed (found $cv)"
command -v openssl >/dev/null || die "openssl is needed to make the secrets"

case $home in /*) ;; *) die "HELMSTUDIO_HOME must be an absolute path";; esac
mkdir -p "$home/secrets" && chmod 700 "$home" "$home/secrets"
conf=$home/helmstudio.env
[ -f "$conf" ] || cat >"$conf" <<EOF
# helmstudio server settings (docker compose reads them: the pack's .env links here). After a change: install.sh
COMPOSE_PROJECT_NAME=helmstudio
COMPOSE_FILE=deploy/docker-compose.yml:deploy/compose/prod.yml:compose.yml
COMPOSE_PROFILES=s3local
HELMSTUDIO_HOME=$home
HELMSTUDIO_VERSION=
HELMSTUDIO_ADMIN_EMAIL=
# Host ports, all on 127.0.0.1 (reach them through a reverse proxy or an SSH tunnel)
KONG_HTTP_PORT=8300
POSTGRES_HOST_PORT=5463
NATS_HOST_PORT=4322
S3_HOST_PORT=8433
S3_TLS_HOST_PORT=8443
# The address people open (links in emails and files use it); behind TLS also set WEB_COOKIE_SECURE=true
# PUBLIC_BASE_URL=https://helmstudio.example.com
# Remote model providers (OpenAI and others), off by default
# REMOTE_MODELS=on
EOF
v=$(cat VERSION)
sed "s/^HELMSTUDIO_VERSION=.*/HELMSTUDIO_VERSION=$v/" "$conf" >"$conf.new" && mv "$conf.new" "$conf"
# the pack's links to the settings and the secrets (a real folder there is an older layout: never deleted here)
[ ! -d deploy/secrets ] || [ -L deploy/secrets ] || die "deploy/secrets is a folder: move it to $home/secrets first"
ln -sfn "$conf" .env
ln -sfn "$home/secrets" deploy/secrets

sh .tools/make-secrets
mkdir -p "${HELM_HOME:-$HOME/.helm}/plugins"   # studio mounts it; Docker would make it as root

admin=$home/secrets/admin.env   # auth reads ADMIN_EMAIL / ADMIN_PASSWORD on every start (compose.yml's env_file)
if ! [ -s "$admin" ]; then
  email=${HELMSTUDIO_ADMIN_EMAIL:-}
  if [ -n "${HELMSTUDIO_ADMIN_PASSWORD_FILE:-}" ]; then
    pw=$(cat "$HELMSTUDIO_ADMIN_PASSWORD_FILE") || die "cannot read HELMSTUDIO_ADMIN_PASSWORD_FILE"
    pw2=$pw
  fi
  if [ -z "$email" ] || [ -z "${pw:-}" ]; then
    [ -t 0 ] || die "no terminal: set HELMSTUDIO_ADMIN_EMAIL and HELMSTUDIO_ADMIN_PASSWORD_FILE"
    [ -n "$email" ] || { printf 'Admin email: '; read -r email; }
    if [ -z "${pw:-}" ]; then
      stty -echo; trap 'stty echo' EXIT
      printf 'Admin password (12 characters or more): '; read -r pw; echo
      printf 'Repeat it: '; read -r pw2; echo
      stty echo; trap - EXIT
    fi
  fi
  case $email in *[[:space:]\']*|*@*@*|'') die "enter an email address";; ?*@?*.?*) ;; *) die "enter an email address";; esac
  [ "${#pw}" -ge 12 ] || die "the password needs at least 12 characters"
  [ "$pw" = "$pw2" ] || die "the passwords differ"
  case $pw in *\'*|*"
"*) die "the password cannot contain a quote or a line break";; esac
  (umask 077 && printf "ADMIN_EMAIL='%s'\nADMIN_PASSWORD='%s'\n" "$email" "$pw" >"$admin")
  awk -v e="$email" '/^HELMSTUDIO_ADMIN_EMAIL=/ { $0 = "HELMSTUDIO_ADMIN_EMAIL=" e } 1' "$conf" >"$conf.new" && mv "$conf.new" "$conf"
  echo "admin account saved (the password only in $admin)"
fi

docker compose up -d --wait --remove-orphans   # pulls the images it does not have (release tags never change)
echo "helmstudio $v is running: http://$(docker compose port kong 8000)"
