# helmstudio: the whole stack from the repo root. Each service has its own Makefile (test and lint; dev and gen where
# they apply).
# The dev stack is compose project helmstudio (Kong on 127.0.0.1:8300); the contract suite runs on its own project,
# helmstudio-test (Kong on 8390, Postgres on 5464, image tag gate), never on the dev stack.

DEV  := docker compose -f deploy/docker-compose.yml -f deploy/compose/fake.yml
TEST := KONG_HTTP_PORT=8390 POSTGRES_HOST_PORT=5464 NATS_HOST_PORT=4323 S3_HOST_PORT=8434 HELM_NET=172.30.98 IMAGE_TAG=gate \
        docker compose -p helmstudio-test -f deploy/docker-compose.yml -f deploy/compose/fake.yml
# the test admin set by deploy/compose/fake.yml
TEST_ADMIN := ADMIN_EMAIL=admin@helm.test ADMIN_PASSWORD="$${HELM_TEST_ADMIN_PASSWORD:-helm test admin pass 1}"

.PHONY: secrets envs build up dev down logs ps contracts-check test resilience test-down runners runners-stop

secrets:          ## deploy/secrets/ (git-ignored); never overwrites
	.tools/make-secrets

envs:             ## every missing .env / .test.env from its example
	.tools/copy-envs

build: secrets envs
	$(DEV) build

up: secrets envs  ## build and start the stack (fake runner), wait until healthy
	$(DEV) up -d --build --wait

dev: up

down:             ## stop the stack; data volumes stay
	$(DEV) down

logs:             ## make logs s=studio (s empty = every service)
	$(DEV) logs -f --tail=100 $(s)

ps:
	$(DEV) ps

contract_tests/.venv:
	$(MAKE) -C contract_tests venv

contracts-check: contract_tests/.venv   ## schemas, fixtures, openapi.yaml (no stack needed)
	$(MAKE) -C contract_tests check-contracts

test: secrets envs contract_tests/.venv  ## the contract suite on the helmstudio-test stack
	$(TEST) up -d --build --wait
	key=$$($(TEST) run --rm -T auth apikey contract) && \
	  $(MAKE) -C contract_tests test BASE_URL=http://localhost:8390 API_KEY="$$key" CONTRACT_SCHEMAS=1 $(TEST_ADMIN)

resilience: secrets envs  ## contract_tests/resilience.sh on the helmstudio-test stack (restarts its services)
	$(TEST) up -d --build --wait
	key=$$($(TEST) run --rm -T auth apikey contract) && API_KEY="$$key" $(TEST_ADMIN) contract_tests/resilience.sh

test-down:        ## stop the test stack; its volumes stay
	$(TEST) down

# The native torch runner (host mac, env torch) outside Docker, joined to the dev stack through the loopback ports
# compose publishes (NATS_HOST_PORT, S3_HOST_PORT). exec: the pid file names the runner process itself.
RUN_PID := runner_service/.run/runner-mac-torch.pid
# runner_service pins uv 0.13.8; an older uv on PATH is stood in for by uvx (the command line still holds "uv run")
UV ?= $(shell uv --version 2>/dev/null | grep -q '^uv 0.13.8' && echo uv || echo 'uvx --from uv==0.13.8 uv')
SECRETS := $(CURDIR)/deploy/secrets

runners: secrets  ## the native torch runner in the foreground (needs make up); Ctrl-C or make runners-stop
	mkdir -p runner_service/.run && echo $$$$ > $(RUN_PID) && cd runner_service && exec env \
	  RUNNER_HOST=mac RUNNER_ENV=torch NATS_URL_FILE=$(SECRETS)/nats_runner_mac_url \
	  S3_ENDPOINT=http://127.0.0.1:$${S3_HOST_PORT:-8433} S3_BUCKET=$${S3_BUCKET:-helmstudio} \
	  S3_ACCESS_KEY_ID_FILE=$(SECRETS)/s3_access_key_id S3_SECRET_ACCESS_KEY_FILE=$(SECRETS)/s3_secret_access_key \
	  HELMSTUDIO_MODELS=$${HELMSTUDIO_MODELS:-$$HOME/GenAI/models} CONTRACTS_DIR=$(CURDIR)/contracts \
	  $(UV) run --extra torch python main.py

runners-stop:     ## stops only the runner make runners started (by its pid file)
	@pid=$$(cat $(RUN_PID) 2>/dev/null) || { echo "no runner pid file"; exit 0; }; \
	if ps -p $$pid -o command= | grep -q 'uv run'; then kill $$pid && echo "stopped runner $$pid"; \
	else echo "runner $$pid is not running"; fi; rm -f $(RUN_PID)
