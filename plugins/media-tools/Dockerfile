# media-tools in the stack (owner, 2026-11-25: a container, the same on the Mac app's VM, dev and a Linux server):
# Alpine Python with ffmpeg from apk, the plugin's deps from uv.lock, a non-root user; listens on 0.0.0.0:8422 inside
# the compose network only (no published port). Health: GET /health.
FROM python:3.14.10-alpine3.23 AS build
RUN pip install --no-cache-dir uv==0.13.8
WORKDIR /app
ENV UV_PROJECT_ENVIRONMENT=/app/.venv UV_COMPILE_BYTECODE=1 UV_LINK_MODE=copy
COPY pyproject.toml uv.lock ./
RUN --mount=type=cache,target=/root/.cache/uv uv sync --frozen --no-dev --no-install-project

FROM python:3.14.10-alpine3.23
RUN apk add --no-cache ffmpeg && adduser -S -u 10003 media
WORKDIR /app
ENV PATH=/app/.venv/bin:$PATH PYTHONUNBUFFERED=1 MEDIA_TOOLS_HOST=0.0.0.0 MEDIA_TOOLS_PORT=8422
COPY --from=build /app/.venv /app/.venv
COPY server.py plugin.json ./
USER media
EXPOSE 8422
HEALTHCHECK --interval=5s --timeout=3s --retries=20 CMD ["python", "-c", "import urllib.request as u; u.urlopen('http://127.0.0.1:8422/health', timeout=2)"]
CMD ["python", "server.py"]
