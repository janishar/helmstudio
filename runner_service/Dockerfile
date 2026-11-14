# runner_service as the fake runner (RUNNER_ENV fake: the echo pack). `build` makes the venv with uv, without the
# "torch" extra (the torch runner runs natively: `make runners`); the runtime gets only the venv and the code.
# Health check: `python health.py` (exit 0 while NATS is connected).
FROM python:3.14.10-slim-trixie AS build
RUN pip install --no-cache-dir uv==0.13.8
WORKDIR /app
ENV UV_PROJECT_ENVIRONMENT=/app/.venv UV_COMPILE_BYTECODE=1 UV_LINK_MODE=copy
COPY pyproject.toml uv.lock ./
RUN --mount=type=cache,target=/root/.cache/uv uv sync --frozen --no-dev --no-install-project

FROM python:3.14.10-slim-trixie AS runtime
WORKDIR /app
ENV PATH=/app/.venv/bin:$PATH PYTHONUNBUFFERED=1
RUN useradd --system --uid 10001 runner
COPY --from=build /app/.venv /app/.venv
COPY . .
USER runner
EXPOSE 8081
CMD ["python", "main.py"]
