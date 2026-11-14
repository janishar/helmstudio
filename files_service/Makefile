.PHONY: dev test gen lint build image
-include .env
export

dev:
	go run ./cmd

test:
	go test -count=1 ./...

# no NATS messages and no generated types: HTTP is GET/HEAD/PUT /files/* only
gen:
	@echo "files_service: nothing to generate"

lint:
	gofmt -l . | (! grep .) && go vet ./...

build:
	CGO_ENABLED=0 go build -tags nomsgpack -o bin/files ./cmd

image:
	docker build --build-context orago=../orago -t helmstudio/files:dev .
