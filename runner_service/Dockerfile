# runner_service as the fake runner (RUNNER_ENV fake: the echo pack). `build` makes the venv with uv, without the
# "torch" extra (the torch runner runs natively: `make runners`); the runtime gets only the venv and the code.
# Health check: `python health.py` (exit 0 while NATS is connected). ffmpeg (apt) runs its media jobs.
FROM python:3.14.10-slim-trixie AS build
RUN pip install --no-cache-dir uv==0.13.8
WORKDIR /app
ENV UV_PROJECT_ENVIRONMENT=/app/.venv UV_COMPILE_BYTECODE=1 UV_LINK_MODE=copy
COPY pyproject.toml uv.lock ./
RUN --mount=type=cache,target=/root/.cache/uv uv sync --frozen --no-dev --no-install-project

FROM python:3.14.10-slim-trixie AS runtime
WORKDIR /app
ENV PATH=/app/.venv/bin:$PATH PYTHONUNBUFFERED=1
# a home: RUNNER_CACHE (~/.cache/helmstudio-runner: the LoRA cache, model hashes, download temp files) lives in it
RUN useradd --system --create-home --uid 10001 runner
# ffmpeg for media jobs (exports, frames: messaging/media.py) with libx264, the software H.264 encoder off the Mac;
# no recommends, so no X, docs or extra codecs
RUN apt-get update && apt-get install -y --no-install-recommends ffmpeg && rm -rf /var/lib/apt/lists/*
COPY --from=build /app/.venv /app/.venv
COPY . .
USER runner
EXPOSE 8081
CMD ["python", "main.py"]
