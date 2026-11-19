.PHONY: dev test test-torch lint gen image image-cuda h3
# uv 0.13.8 is required (pyproject [tool.uv] required-version); `uvx --from uv==0.13.8 uv` stands in for an older uv
UV ?= $(shell uv --version 2>/dev/null | grep -q '^uv 0.13.8' && echo uv || echo 'uvx --from uv==0.13.8 uv')

# needs NATS (CMD/EVT/DLQ streams, KV registry/hosts/cancels) and S3 reachable with the values in .env
dev:
	set -a; [ -f .env ] && . ./.env; set +a; $(UV) run python main.py

# unit tests: no NATS, no S3
test:
	$(UV) run pytest -q

# the torch env: unit tests including the torch loader (CPU/MPS, tiny tensors); `-m gpu` loads real weights
test-torch:
	$(UV) sync --extra torch
	$(UV) run --extra torch pytest -q

lint:
	$(UV) run ruff check .
	$(UV) run ruff format --check .

# message and registry schemas, copied from contracts/ (committed; tests/test_registry.py fails when they drift).
# The registry values come from the packs; tests/test_packs.py compares them with the canonical fixtures.
gen:
	rm -rf gen && mkdir -p gen/messages
	cp ../contracts/messages/*.schema.json gen/messages/
	cp ../contracts/registry.schema.json gen/

# the env native's binary (packs/minimax_h3): the pinned h3.c in vendor/h3c (VERSION), Apple clang, about 1 s; it runs
# with cwd = vendor/h3c (h3_shaders.metal is compiled at run time). Needs Metal: macOS only.
h3:
	$(MAKE) -C vendor/h3c -j8 h3

image:
	docker build -t helmstudio/runner:dev .

# the torch runner for Linux + NVIDIA (deploy/gpu-host/): linux/amd64, torch's CUDA 13 wheels from uv.lock (several GB)
image-cuda:
	docker build --platform linux/amd64 -f Dockerfile.cuda -t helmstudio/runner-cuda:dev .
