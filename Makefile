# helmstudio: the whole stack from the repo root. Each service has its own Makefile (test and lint; dev and gen where
# they apply).
# The dev stack is compose project helmstudio (Kong on 127.0.0.1:8300); the contract suite runs on its own project,
# helmstudio-test (Kong on 8390, Postgres on 5464, image tag gate), never on the dev stack.

DEV  := docker compose -f deploy/docker-compose.yml -f deploy/compose/fake.yml
TEST := KONG_HTTP_PORT=8390 POSTGRES_HOST_PORT=5464 HELM_NET=172.30.98 IMAGE_TAG=gate \
        docker compose -p helmstudio-test -f deploy/docker-compose.yml -f deploy/compose/fake.yml
# the test admin set by deploy/compose/fake.yml
TEST_ADMIN := ADMIN_EMAIL=admin@helm.test ADMIN_PASSWORD="$${HELM_TEST_ADMIN_PASSWORD:-helm test admin pass 1}"

.PHONY: secrets envs build up dev down logs ps contracts-check test resilience test-down runners

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

runners:
	@echo "Native GPU runners arrive in M1. In M0 the fake runner runs in the compose stack (service runner): make up"
