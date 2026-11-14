.PHONY: build test lint

build:
	CGO_ENABLED=0 go build -o bin/helm .

test:
	go test -count=1 ./...

lint:
	gofmt -l . | (! grep .) && go vet ./...
