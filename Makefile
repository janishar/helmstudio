# helmstudio: the whole stack from the repo root. Each service has its own Makefile (test and lint; dev and gen where
# they apply).
# The dev stack is compose project helmstudio (Kong on 127.0.0.1:8300); the contract suite runs on its own project,
# helmstudio-test (Kong on 8390, Postgres on 5464, image tag gate), never on the dev stack.

# STACK_TLS=on (here or in deploy/.env) adds deploy/compose/tls.yml to the dev stack: TLS for NATS and S3 (GPU hosts)
STACK_TLS ?= $(shell sed -n 's/^STACK_TLS=//p' deploy/.env 2>/dev/null)
DEV  := docker compose -f deploy/docker-compose.yml -f deploy/compose/fake.yml $(if $(filter on,$(STACK_TLS)),-f deploy/compose/tls.yml)
# the test stack turns remote models on: its pipelines trusts only the fake OpenAI server's certificate (fake.yml);
# TLS is always on in it (tls.yml): the fake runner joins over tls:// and https://
TEST := KONG_HTTP_PORT=8390 POSTGRES_HOST_PORT=5464 NATS_HOST_PORT=4323 S3_HOST_PORT=8434 S3_TLS_HOST_PORT=8444 \
        HELM_NET=172.30.98 IMAGE_TAG=gate REMOTE_MODELS=on \
        docker compose -p helmstudio-test -f deploy/docker-compose.yml -f deploy/compose/fake.yml -f deploy/compose/tls.yml
# the test admin set by deploy/compose/fake.yml
TEST_ADMIN := ADMIN_EMAIL=admin@helm.test ADMIN_PASSWORD="$${HELM_TEST_ADMIN_PASSWORD:-helm test admin pass 1}"

.PHONY: secrets envs build up dev down logs ps contracts-check test resilience test-down runners runner-torch runner-mlx runner-native runners-stop h3 \
        gpu-host-secrets gpu-host-reload gpu-host-tunnel gpu-host-tunnel-stop

secrets:          ## deploy/secrets/ (git-ignored); never overwrites
	.tools/make-secrets

envs:             ## every missing .env / .test.env from its example
	.tools/copy-envs

build: secrets envs
	$(DEV) build

# a running nats keeps the nats.conf it started with: HUP reloads it (users and permissions) before every up
up: secrets envs  ## build and start the stack (fake runner), wait until healthy
	-@$(DEV) kill -s HUP nats >/dev/null 2>&1
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
	-@$(TEST) kill -s HUP nats >/dev/null 2>&1
	$(TEST) up -d --build --wait
	$(TEST) --profile hosts create runner-b   # the second fake host: test_hosts.py starts and stops it
	key=$$($(TEST) run --rm -T auth apikey contract) && \
	  $(MAKE) -C contract_tests test BASE_URL=http://localhost:8390 API_KEY="$$key" CONTRACT_SCHEMAS=1 $(TEST_ADMIN)

resilience: secrets envs  ## contract_tests/resilience.sh on the helmstudio-test stack (restarts its services); SCENARIOS="a b" runs only those
	-@$(TEST) kill -s HUP nats >/dev/null 2>&1
	$(TEST) up -d --build --wait
	key=$$($(TEST) run --rm -T auth apikey contract) && API_KEY="$$key" $(TEST_ADMIN) contract_tests/resilience.sh $(SCENARIOS)

test-down:        ## stop the test stack; its volumes stay
	$(TEST) down

# The native runners of this Mac outside Docker, host mac: env torch (the torch packs; uv project runner_service/),
# env mlx (the mlx packs; uv project runner_service/envs/mlx) and env native (packs that run a native binary as a child
# process, MiniMax H3's h3; uv project runner_service/ without extras), joined to the dev stack through the loopback ports
# compose publishes (NATS_HOST_PORT, S3_HOST_PORT). Each runs in the background with its own pid file and log,
# runner_service/.run/runner-mac-<env>.{pid,log}; the pid file names a small sh supervisor that restarts the runner
# after a stall (exit 70) or a SIGKILL (137: jetsam) and stops it on SIGTERM (make runners-stop).
# One GPU, one memory budget (contracts/README.md, Two envs on one host): studio's dispatcher admits runs against the
# host's budget and asks an env to unload named components (runner.unload) to make room.
RUN_DIR := runner_service/.run
# runner_service pins uv 0.13.8; an older uv on PATH is stood in for by uvx (the command line still holds "uv run")
UV ?= $(shell uv --version 2>/dev/null | grep -q '^uv 0.13.8' && echo uv || echo 'uvx --from uv==0.13.8 uv')
SECRETS := $(CURDIR)/deploy/secrets
UV_RUN_torch := run --extra torch
UV_RUN_mlx := run --project envs/mlx --extra llm
UV_RUN_native := run
HEALTH_PORT_torch := 8091   # each runner's GET /health on 0.0.0.0 (envs on one Mac need their own ports)
HEALTH_PORT_mlx := 8092
HEALTH_PORT_native := 8093

runners: runner-torch runner-mlx runner-native  ## the native runners in the background (needs make up); make runners-stop stops them

# the native env runs the vendored h3 (runner_service/vendor/h3c, pinned h3.c): make rebuilds it only when a source changed
runner-native: h3

h3:               ## build the vendored h3 binary (Command Line Tools clang; about 1 s)
	$(MAKE) -C runner_service/vendor/h3c -j8 h3

runner-torch runner-mlx runner-native: runner-%: secrets  ## one native runner (env torch, mlx or native) in the background
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

runners-stop:     ## stops the native runners (each by its pid file; only a process make started)
	@for env in torch mlx native; do f=$(RUN_DIR)/runner-mac-$$env.pid; pid=$$(cat $$f 2>/dev/null) || continue; \
	if ps -p $$pid -o command= | grep -q 'uv run'; then kill $$pid && echo "stopped runner $$env ($$pid)"; \
	else echo "runner $$env ($$pid) is not running"; fi; rm -f $$f; done

# Remote GPU hosts (deploy/gpu-host/README.md; contracts/README.md, A remote GPU host). Each has its own NATS user
# runner_<h>_torch and S3 identity runner-<h>, made by .tools/make-secrets; a running dev stack takes a new host
# without a restart of the stack: NATS reloads its users on HUP, SeaweedFS (identities) restarts alone.
gpu-host-secrets: ## a GPU host's credentials (GPU_HOSTS="gcp"; default: those that exist, else gcp), then gpu-host-reload
	GPU_HOSTS="$(GPU_HOSTS)" .tools/make-secrets
	@$(MAKE) --no-print-directory gpu-host-reload

gpu-host-reload:  ## the running dev stack picks up the GPU hosts' users and identities (only services already running)
	-@$(DEV) kill -s HUP nats >/dev/null 2>&1 && echo "nats reloaded"
	@if [ -n "$$($(DEV) ps -q seaweedfs 2>/dev/null)" ]; then $(DEV) restart seaweedfs; fi

# The test rig: an SSH reverse tunnel from this machine puts the dev stack's NATS and S3 https (STACK_TLS=on) on the
# GPU host's loopback (127.0.0.1:4222 and :8333); no port of this machine is on the internet. Never run by make up.
gpu-host-tunnel:  ## make gpu-host-tunnel VM=<instance> ZONE=<zone>: the tunnel in the background (restarts on drop)
	@[ -n "$(VM)" ] && [ -n "$(ZONE)" ] || { echo "usage: make gpu-host-tunnel VM=<instance> ZONE=<zone>"; exit 2; }
	.tools/gpu-host-tunnel start $(VM) $(ZONE)

gpu-host-tunnel-stop: ## stops the tunnel (by its pid file; only a process the helper started)
	.tools/gpu-host-tunnel stop
