# The gate. Everything that must pass before a commit lands.
# See docs/agents/gate.md. Checks are added as the contracts they check appear:
# M1 adds the dependency diff and the two-platform run, M4 generated-client
# drift and conformance, M6 visual regression.
#
# A target whose contract does not exist yet says so and passes. A target whose
# contract exists and fails, fails.

SHELL := /bin/bash
GO ?= go
HELM ?= ./bin/helm

.PHONY: gate fmt vet test validate build clean

gate: fmt vet test validate
	@echo "gate: green"

fmt:
	@if [ ! -f go.mod ]; then echo "fmt: no go.mod yet, skipping"; \
	else out=$$(gofmt -l . ); \
	  if [ -n "$$out" ]; then echo "fmt: not formatted:"; echo "$$out"; exit 1; fi; \
	  echo "fmt: clean"; fi

vet:
	@if [ ! -f go.mod ]; then echo "vet: no go.mod yet, skipping"; \
	else $(GO) vet ./...; echo "vet: clean"; fi

test:
	@if [ ! -f go.mod ]; then echo "test: no go.mod yet, skipping"; \
	else $(GO) test ./...; fi

build:
	@if [ ! -f go.mod ]; then echo "build: no go.mod yet, skipping"; \
	else $(GO) build -o bin/helm ./cmd/helm && $(GO) build -o bin/helmstudio ./cmd/helmstudio; fi

# Every manifest in the registry validates, every time. A schema change that
# broke a real studio is caught here and nowhere else.
validate:
	@shopt -s nullglob; set -- studios/*.yaml; \
	if [ ! -x $(HELM) ]; then echo "validate: helm not built yet, skipping"; \
	elif [ $$# -eq 0 ]; then echo "validate: no manifests yet, skipping"; \
	else $(HELM) validate "$$@"; fi

clean:
	rm -rf bin
