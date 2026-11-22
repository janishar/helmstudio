.PHONY: build test lint gen

build:
	CGO_ENABLED=0 go build -o bin/helm .

test:
	go test -count=1 ./...

lint:
	gofmt -l . | (! grep .) && go vet ./...

# gen/catalog.json: a copy of contracts/tools/catalog.json, embedded for helm mcp (gen_test.go fails when it drifts)
gen:
	mkdir -p gen && cp ../contracts/tools/catalog.json gen/
