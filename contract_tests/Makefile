-include .env
export

BASE_URL ?= http://localhost:8190
API_KEY ?= contract-key
ARGS ?=

.PHONY: venv test check-contracts

venv:
	uv venv .venv
	uv sync --frozen 2>/dev/null || uv pip install --python .venv/bin/python -r pyproject.toml

check-contracts:   # validate ../contracts (schemas, fixtures, openapi.yaml)
	.venv/bin/python ../contracts/check.py

test:
	@BASE_URL=$(BASE_URL) API_KEY=$(API_KEY) .venv/bin/pytest $(ARGS)
