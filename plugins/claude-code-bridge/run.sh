#!/bin/sh
# Start the bridge (plugin "Your Claude subscription"). Its env (random token, chmod 600) lives outside plugins/ (the
# studio mounts that folder, and the Mac app's copy is inside the app): ${BRIDGE_ENV:-$HELM_HOME/claude-code-bridge.env}
# (HELM_HOME defaults to ~/.helm), made on first run; the runner reads BRIDGE_TOKEN there for Add as provider (plugin.json
# key_from: no key to paste); an env from an old place (plugins/claude-code-bridge/.env,
# ~/.config/helmstudio/claude-code-bridge.env) is moved there, same token.
# No claude, or not signed in: one plain line and exit 78, so the runner shows it and waits for Try again. Only
# `claude auth status` is asked; no credential file is ever read.
cd "$(dirname "$0")"
umask 077
export PATH="$PATH:$HOME/.local/bin"  # Claude Code's own installer puts claude there
if ! command -v claude >/dev/null 2>&1 || ! claude auth status --json 2>/dev/null | grep -q '"loggedIn": *true'; then
  echo "Install Claude Code and sign in, then Try again"
  exit 78
fi
HELM="${HELM_HOME:-$HOME/.helm}"
[ -d "$HELM" ] || mkdir -m 700 "$HELM"
export BRIDGE_ENV="${BRIDGE_ENV:-$HELM/claude-code-bridge.env}"
mkdir -p "$(dirname "$BRIDGE_ENV")"
for old in .env "$HOME/.config/helmstudio/claude-code-bridge.env"; do
  if [ ! -f "$BRIDGE_ENV" ] && [ -f "$old" ]; then
    mv "$old" "$BRIDGE_ENV"
  fi
done
if [ ! -f "$BRIDGE_ENV" ]; then
  printf 'BRIDGE_TOKEN=%s\nBRIDGE_HOST=127.0.0.1\nBRIDGE_PORT=8420\nBRIDGE_TIMEOUT=180\n' \
    "$(LC_ALL=C tr -dc 'A-Za-z0-9_-' </dev/urandom | head -c 43)" > "$BRIDGE_ENV"
fi
chmod 600 "$BRIDGE_ENV"
# uv (bundled in the Mac app) finds or fetches a Python; else the system's python3
command -v uv >/dev/null 2>&1 && exec uv run --no-project python bridge.py
exec python3 bridge.py
