.PHONY: dev test lint gen image
# uv 0.13.8 is required (pyproject [tool.uv] required-version); `uvx --from uv==0.13.8 uv` stands in for an older uv
UV ?= uv

# needs NATS (CMD/EVT/DLQ streams, KV registry/hosts/cancels) and S3 reachable with the values in .env
dev:
	set -a; [ -f .env ] && . ./.env; set +a; $(UV) run python main.py

# unit tests: no NATS, no S3
test:
	$(UV) run pytest -q

lint:
	$(UV) run ruff check .
	$(UV) run ruff format --check .

# message/registry schemas and the canonical echo registry values, copied from contracts/ (committed;
# tests/test_registry.py fails when they drift)
gen:
	rm -rf gen && mkdir -p gen/messages gen/registry
	cp ../contracts/messages/*.schema.json gen/messages/
	cp ../contracts/registry.schema.json gen/
	cp ../contracts/fixtures/registry/nodes.valid.json ../contracts/fixtures/registry/workflows.valid.json \
		../contracts/fixtures/registry/models.valid.json gen/registry/

image:
	docker build -t helmstudio/runner:dev .
