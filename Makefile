# helmstudio: the whole stack from the repo root. Each service has its own Makefile (test and lint; dev and gen where
# they apply).
# The dev stack is compose project helmstudio (Kong on 127.0.0.1:8300); the contract suite runs on its own project,
# helmstudio-test (Kong on 8390, Postgres on 5464, image tag gate), never on the dev stack.

DEV  := docker compose -f deploy/docker-compose.yml -f deploy/compose/fake.yml
TEST := KONG_HTTP_PORT=8390 POSTGRES_HOST_PORT=5464 NATS_HOST_PORT=4323 S3_HOST_PORT=8434 HELM_NET=172.30.98 IMAGE_TAG=gate \
        docker compose -p helmstudio-test -f deploy/docker-compose.yml -f deploy/compose/fake.yml
# the test admin set by deploy/compose/fake.yml
TEST_ADMIN := ADMIN_EMAIL=admin@helm.test ADMIN_PASSWORD="$${HELM_TEST_ADMIN_PASSWORD:-helm test admin pass 1}"

.PHONY: secrets envs build up dev down logs ps contracts-check test resilience test-down runners runner-torch runner-mlx runners-stop

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

# The native runners of this Mac outside Docker, host mac: env torch (the torch packs; uv project runner_service/) and
# env mlx (the mlx packs; uv project runner_service/envs/mlx), joined to the dev stack through the loopback ports
# compose publishes (NATS_HOST_PORT, S3_HOST_PORT). Each runs in the background with its own pid file and log,
# runner_service/.run/runner-mac-<env>.{pid,log}; the pid file names a small sh supervisor that restarts the runner
# after a stall (exit 70) or a SIGKILL (137: jetsam) and stops it on SIGTERM (make runners-stop).
# One GPU: only one of them may hold weights at a time (contracts/README.md, Two envs on one host). Studio's GPU gate
# asks the other env to unload (runner.unload) before it releases a run; until it does, start one (make runner-<env>).
RUN_DIR := runner_service/.run
# runner_service pins uv 0.13.8; an older uv on PATH is stood in for by uvx (the command line still holds "uv run")
UV ?= $(shell uv --version 2>/dev/null | grep -q '^uv 0.13.8' && echo uv || echo 'uvx --from uv==0.13.8 uv')
SECRETS := $(CURDIR)/deploy/secrets
UV_RUN_torch := run --extra torch
UV_RUN_mlx := run --project envs/mlx
HEALTH_PORT_torch := 8091   # each runner's GET /health on 0.0.0.0 (two on one Mac need two ports)
HEALTH_PORT_mlx := 8092

runners: runner-torch runner-mlx  ## both native runners in the background (needs make up); make runners-stop stops them

runner-torch runner-mlx: runner-%: secrets  ## one native runner (env torch or mlx) in the background
	@mkdir -p $(RUN_DIR); pid=$$(cat $(RUN_DIR)/runner-mac-$*.pid 2>/dev/null); \
	if [ -n "$$pid" ] && ps -p $$pid -o command= | grep -q 'uv run'; then echo "runner $* already runs ($$pid)"; exit 0; fi; \
	cd runner_service && nohup sh -c 'echo $$$$ > .run/runner-mac-$*.pid; trap "kill \$$c; wait \$$c; exit 143" TERM INT; \
	while :; do env \
	  RUNNER_HOST=mac RUNNER_ENV=$* HEALTH_PORT=$(HEALTH_PORT_$*) NATS_URL_FILE=$(SECRETS)/nats_runner_mac_$*_url \
	  S3_ENDPOINT=http://127.0.0.1:$${S3_HOST_PORT:-8433} S3_BUCKET=$${S3_BUCKET:-helmstudio} \
	  S3_ACCESS_KEY_ID_FILE=$(SECRETS)/s3_access_key_id S3_SECRET_ACCESS_KEY_FILE=$(SECRETS)/s3_secret_access_key \
	  HELMSTUDIO_MODELS=$${HELMSTUDIO_MODELS:-$$HOME/GenAI/models} CONTRACTS_DIR=$(CURDIR)/contracts \
	  $(UV) $(UV_RUN_$*) python main.py & c=$$!; wait $$c; s=$$?; [ $$s -eq 70 ] || [ $$s -eq 137 ] || exit $$s; \
	  echo "runner exited $$s: restarting"; sleep 2; done' > .run/runner-mac-$*.log 2>&1 & \
	sleep 1; echo "runner $* started ($$(cat $(RUN_DIR)/runner-mac-$*.pid)); log runner_service/.run/runner-mac-$*.log"

runners-stop:     ## stops both native runners (each by its pid file; only a process make started)
	@for env in torch mlx; do f=$(RUN_DIR)/runner-mac-$$env.pid; pid=$$(cat $$f 2>/dev/null) || continue; \
	if ps -p $$pid -o command= | grep -q 'uv run'; then kill $$pid && echo "stopped runner $$env ($$pid)"; \
	else echo "runner $$env ($$pid) is not running"; fi; rm -f $$f; done
