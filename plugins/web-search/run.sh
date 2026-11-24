#!/bin/sh
# Start the web-search plugin (127.0.0.1:8421; WEB_SEARCH_HOST / WEB_SEARCH_PORT to change). Needs uv.
cd "$(dirname "$0")"
exec uv run --frozen --no-dev python server.py
