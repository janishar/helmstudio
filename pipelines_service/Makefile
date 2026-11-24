.PHONY: dev test lint gen image
# uv 0.13.8 is required (pyproject [tool.uv] required-version); `uvx --from uv==0.13.8 uv` stands in for an older uv
UV ?= $(shell uv --version 2>/dev/null | grep -q '^uv 0.13.8' && echo uv || echo 'uvx --from uv==0.13.8 uv')

# needs NATS (CMD/EVT streams, the pipelines user) and the pipelines database with the values in .env
dev:
	set -a; [ -f .env ] && . ./.env; set +a; $(UV) run python main.py

# unit tests: no NATS; the engine tests need TEST_DATABASE_URL (.test.env, .tools/test-db; else they skip)
test:
	set -a; [ -f .test.env ] && . ./.test.env; set +a; $(UV) run pytest -q

lint:
	$(UV) lock --check
	$(UV) run ruff check .
	$(UV) run ruff format --check .

# message schemas, copied from contracts/ (committed; tests/test_messages.py fails when they drift)
gen:
	rm -rf gen && mkdir -p gen/messages gen/tools gen/plugins
	cp ../contracts/messages/*.schema.json gen/messages/
	cp ../contracts/registry.schema.json gen/
	cp ../contracts/tools/*.json gen/tools/
	cp ../contracts/plugins/*.json gen/plugins/

image:
	docker build -t helmstudio/pipelines:dev .
