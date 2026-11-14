# pipelines_service: `build` makes the venv with uv; the runtime gets only the venv and the code (no torch, no
# numpy, no model code). Health check: `python health.py` (exit 0 while NATS is connected and the database answers).
FROM python:3.14.10-slim-trixie AS build
RUN pip install --no-cache-dir uv==0.13.8
WORKDIR /app
ENV UV_PROJECT_ENVIRONMENT=/app/.venv UV_COMPILE_BYTECODE=1 UV_LINK_MODE=copy
COPY pyproject.toml uv.lock ./
RUN --mount=type=cache,target=/root/.cache/uv uv sync --frozen --no-dev --no-install-project

FROM python:3.14.10-slim-trixie AS runtime
WORKDIR /app
ENV PATH=/app/.venv/bin:$PATH PYTHONUNBUFFERED=1
RUN useradd --system --uid 10001 pipelines
COPY --from=build /app/.venv /app/.venv
COPY . .
USER pipelines
EXPOSE 8082
CMD ["python", "main.py"]
