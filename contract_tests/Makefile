-include .env
export

BASE_URL ?= http://127.0.0.1:8300
ARGS ?=

.PHONY: venv test collect lint check-contracts

venv:
	uv sync --frozen

check-contracts:   # validate ../contracts (schemas, fixtures, openapi.yaml)
	.venv/bin/python ../contracts/check.py

test:
	.venv/bin/pytest $(ARGS)

collect:           # no stack needed: every test imports and is found
	.venv/bin/pytest --collect-only -q

lint:
	uvx ruff@0.17.5 check . && uvx ruff@0.17.5 format --check .
