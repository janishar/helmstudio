# The gate. Everything that must pass before a commit lands.
# See docs/agents/gate.md. Checks are added as the contracts they check appear;
# M1 adds the dependency diff and the two-platform run, M4 client drift and
# conformance, M6 visual regression.

SHELL := /bin/bash
GO ?= go
HELM ?= ./bin/helm

.PHONY: gate test fmt vet validate build clean tools

gate: fmt vet test validate
	@echo "gate: green"

fmt:
	@if [ ! -f go.mod ]; then echo "fmt: no go.mod yet, skipping"; exit 0; fi
	@out=$$($(GO) run mvdan.cc/gofumpt@latest -l . 2>/dev/null || gofmt -l .); \
	if [ -n "$$out" ]; then echo "fmt: not formatted:"; echo "$$out"; exit 1; fi
	@echo "fmt: clean"

vet:
	@if [ ! -f go.mod ]; then echo "vet: no go.mod yet, skipping"; exit 0; fi
	$(GO) vet ./...

test:
	@if [ ! -f go.mod ]; then echo "test: no go.mod yet, skipping"; exit 0; fi
	$(GO) test ./...

build:
	@if [ ! -f go.mod ]; then echo "build: no go.mod yet, skipping"; exit 0; fi
	$(GO) build -o bin/helm ./cmd/helm
	$(GO) build -o bin/helmstudio ./cmd/helmstudio

# Every manifest in the registry must validate, every time. A schema change
# that breaks a real studio is caught here and nowhere else.
validate:
	@if [ ! -x $(HELM) ]; then echo "validate: helm not built yet, skipping"; exit 0; fi
	@shopt -s nullglob; set -- studios/*.yaml; \
	if [ $$# -eq 0 ]; then echo "validate: no manifests yet, skipping"; exit 0; fi; \
	$(HELM) validate "$$@"

clean:
	rm -rf bin
