.PHONY: dev test test-torch lint gen image image-cuda h3 zonos3 zonos3-cuda zonos3-get
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

# the env native's zonos3-server (packs/zonos3): the pinned ZONOS3 ggml server in vendor/zonos3 (VERSION), CMake +
# Apple clang + cargo (the textnorm library), static, Metal library embedded, offline (no crates.io, no git fetch);
# about 2 min from clean (cargo's fat LTO is most of it). Out: vendor/zonos3/build/zonos3-server. macOS (Linux + CUDA: zonos3-cuda).
ZONOS3_BUILD := vendor/zonos3/build
zonos3:
	cmake -S vendor/zonos3/servers/ggml -B $(ZONOS3_BUILD) -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF \
		-DBUILD_TESTING=OFF -DGGML_METAL_EMBED_LIBRARY=ON -DGGML_CCACHE=OFF
	CARGO_NET_OFFLINE=true cmake --build $(ZONOS3_BUILD) -j8 --target zonos3-server

# the same server for Linux + NVIDIA (M9; Dockerfile.cuda's zonos3 stage runs it): nvcc from CUDA 12.8 or later for
# sm_120 (RTX PRO 6000), ggml-cuda for 120a-real only, no Metal, no OpenMP (as the Mac build); cudart and cuBLAS are
# linked shared and found in lib/ beside the binary (the release job and the Dockerfile copy them there) through the
# RUNPATH $ORIGIN/lib. ZONOS3_CUDA_ARCHS: the release job builds several (alpha.6 row 2). libstdc++ and libgcc are
# linked static so the binary needs only glibc (2.39: Ubuntu 24.04) of the host; ponytail: unbuilt with them so far.
ZONOS3_CUDA_ARCHS ?= 120a-real
ZONOS3_CUDA_LDFLAGS ?= -static-libstdc++ -static-libgcc
zonos3-cuda:
	cmake -S vendor/zonos3/servers/ggml -B $(ZONOS3_BUILD) -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF \
		-DBUILD_TESTING=OFF -DGGML_CCACHE=OFF -DGGML_OPENMP=OFF -DGGML_CUDA=ON \
		'-DCMAKE_CUDA_ARCHITECTURES=$(ZONOS3_CUDA_ARCHS)' '-DCMAKE_BUILD_RPATH=$$ORIGIN/lib' \
		'-DCMAKE_EXE_LINKER_FLAGS=$(ZONOS3_CUDA_LDFLAGS)'
	CARGO_NET_OFFLINE=true cmake --build $(ZONOS3_BUILD) -j$$(nproc) --target zonos3-server

# the prebuilt Linux x86_64 CUDA 13 server of this checkout's release (the release job zonos3-cuda), checked against
# that release's SHA256SUMS, unpacked into vendor/zonos3/build (zonos3-server, lib/ with cudart and cuBLAS, licences).
# The release is the nearest tag (ponytail: a checkout past it whose vendor/zonos3 changed gets the older server;
# ZONOS3_RELEASE names another); ZONOS3_URL another place (the tests: a file:// folder). make runners-nvidia runs it.
ZONOS3_RELEASE ?= $(shell git describe --tags --abbrev=0 2>/dev/null)
ZONOS3_URL ?= https://github.com/janishar/helmstudio/releases/download/$(ZONOS3_RELEASE)
ZONOS3_TAR = zonos3-server-$(ZONOS3_RELEASE:v%=%)-linux-x86_64-cuda13.tar.gz
SHA256 ?= $(shell command -v sha256sum >/dev/null && echo sha256sum || echo 'shasum -a 256')
zonos3-get:
	@d=$(ZONOS3_BUILD)/.get; mkdir -p $$d; rm -f $$d/$(ZONOS3_TAR) $$d/SHA256SUMS; \
	if ! curl -fsSL -o $$d/$(ZONOS3_TAR) "$(ZONOS3_URL)/$(ZONOS3_TAR)" || \
	  ! curl -fsSL -o $$d/SHA256SUMS "$(ZONOS3_URL)/SHA256SUMS"; then \
	  echo "no prebuilt zonos3-server for '$(ZONOS3_RELEASE)' (a dev checkout?): make -C runner_service zonos3-cuda (nvcc 12.8+)"; \
	  exit 1; fi; \
	(cd $$d && grep " $(ZONOS3_TAR)$$" SHA256SUMS | $(SHA256) -c - >/dev/null 2>&1) || \
	  { echo "$(ZONOS3_TAR) does not match the release's SHA256SUMS: refused"; rm -f $$d/$(ZONOS3_TAR); exit 1; }; \
	tar -xzf $$d/$(ZONOS3_TAR) -C $(ZONOS3_BUILD) && rm -f $$d/$(ZONOS3_TAR) $$d/SHA256SUMS && \
	echo "zonos3-server $(ZONOS3_RELEASE) in $(ZONOS3_BUILD)"

image:
	docker build -t helmstudio/runner:dev .

# the torch runner for Linux + NVIDIA (deploy/gpu-host/): linux/amd64, torch's CUDA 13 wheels from uv.lock (several GB)
image-cuda:
	docker build --platform linux/amd64 -f Dockerfile.cuda --target runtime -t helmstudio/runner-cuda:dev .
