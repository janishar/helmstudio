.PHONY: dev test gen lint build image
-include .env
export

dev:
	go run ./cmd

# integration tests need TEST_DATABASE_URL (.test.env); without it they skip
test:
	set -a; [ -f .test.env ] && . ./.test.env; set +a; go test -count=1 ./...

# HTTP bodies are hand-written DTOs (api/studio/dto); gen/ holds the NATS envelope from contracts/messages (hand-kept:
# one struct; message data is decoded per type where it is used) and a copy of contracts/tools/catalog.json (embedded;
# gen/tools_test.go fails when it drifts)
gen:
	mkdir -p gen/tools && cp ../contracts/tools/catalog.json gen/tools/

lint:
	gofmt -l . | (! grep .) && go vet ./...

build:
	CGO_ENABLED=0 go build -tags nomsgpack -o bin/studio ./cmd

image:
	docker build --build-context helmgo=../helmgo -t helmstudio/studio:dev .
