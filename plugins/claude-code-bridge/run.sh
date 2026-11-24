#!/bin/sh
# Start the personal claude bridge. Makes .env (random token, chmod 600) on first run.
cd "$(dirname "$0")"
if [ ! -f .env ]; then
  umask 077
  printf 'BRIDGE_TOKEN=%s\nBRIDGE_HOST=127.0.0.1\nBRIDGE_PORT=8420\nBRIDGE_TIMEOUT=180\n' "$(python3 -c 'import secrets;print(secrets.token_urlsafe(32))')" > .env
fi
chmod 600 .env
exec python3 bridge.py
