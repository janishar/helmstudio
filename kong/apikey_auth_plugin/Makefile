.PHONY: test build lint
test:
	go test -count=1 ./...
build:
	CGO_ENABLED=0 go build -o bin/apikey-auth-plugin .
lint:
	gofmt -l . | (! grep .) && go vet ./...
