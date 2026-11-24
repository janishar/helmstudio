#!/bin/sh
# Start the personal claude bridge. Its env (random token, chmod 600) lives outside plugins/ (the studio mounts that
# folder): ${BRIDGE_ENV:-~/.helm/claude-code-bridge.env}, made on first run; an env from an old place
# (plugins/claude-code-bridge/.env, ~/.config/helmstudio/claude-code-bridge.env) is moved there, same token.
cd "$(dirname "$0")"
umask 077
[ -d "$HOME/.helm" ] || mkdir -m 700 "$HOME/.helm"
export BRIDGE_ENV="${BRIDGE_ENV:-$HOME/.helm/claude-code-bridge.env}"
mkdir -p "$(dirname "$BRIDGE_ENV")"
for old in .env "$HOME/.config/helmstudio/claude-code-bridge.env"; do
  if [ ! -f "$BRIDGE_ENV" ] && [ -f "$old" ]; then
    mv "$old" "$BRIDGE_ENV"
  fi
done
if [ ! -f "$BRIDGE_ENV" ]; then
  printf 'BRIDGE_TOKEN=%s\nBRIDGE_HOST=127.0.0.1\nBRIDGE_PORT=8420\nBRIDGE_TIMEOUT=180\n' "$(python3 -c 'import secrets;print(secrets.token_urlsafe(32))')" > "$BRIDGE_ENV"
fi
chmod 600 "$BRIDGE_ENV"
exec python3 bridge.py
