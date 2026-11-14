.PHONY: dev test gen lint build image
-include .env
export

dev:
	go run ./cmd

# integration tests need TEST_DATABASE_URL (.test.env); without it they skip
test:
	set -a; [ -f .test.env ] && . ./.test.env; set +a; go test -count=1 ./...

# types are hand-written in api/*/dto from contracts/openapi.yaml (tag auth); nothing generated yet
gen:
	@echo "auth_service: no generated code"

lint:
	gofmt -l . | (! grep .) && go vet ./...

build:
	CGO_ENABLED=0 go build -tags nomsgpack -o bin/auth ./cmd

image:
	docker build --build-context helmgo=../helmgo -t helmstudio/auth:dev .
