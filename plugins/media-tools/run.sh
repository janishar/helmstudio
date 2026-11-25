#!/bin/sh
# Start the media-tools plugin (127.0.0.1:8422; MEDIA_TOOLS_HOST / MEDIA_TOOLS_PORT to change). Needs uv, ffmpeg and ffprobe.
cd "$(dirname "$0")"
exec uv run --frozen --no-dev python server.py
