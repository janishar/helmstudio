.PHONY: test lint check

# files that may import Gin: the HTTP edge only (docs/reference-notes/framework-go.md)
GIN_ALLOWED := ^helmgo/(network|middleware|micro)/|_test\.go$$|/tests/|/api/[^/]+/controller\.go$$|^files_service/api/file/|/api/auth/middleware/|/common/payload\.go$$|/startup/

# integration tests need TEST_DATABASE_URL (.test.env); without it they skip
test:
	set -a; [ -f .test.env ] && . ./.test.env; set +a; go test -count=1 ./...

lint:
	gofmt -l . | (! grep .) && go vet ./...

# helmgo stays free of domain words, Gin stays at the HTTP edge, JetStream is reached only through helmgo/micro
# (docs/reference-notes/framework-go.md), and the services still build on helmgo
check: test lint
	! grep -rEil 'job|gallery|brand|owner' --include='*.go' --exclude='*_test.go' .
	cd .. && grep -rl --include='*.go' 'gin-gonic/gin' helmgo auth_service studio_service files_service | grep -vE '$(GIN_ALLOWED)' | (! grep .)
	cd .. && grep -rl --include='*.go' 'nats-io/nats.go/jetstream' helmgo auth_service studio_service files_service | grep -v '^helmgo/micro/' | (! grep .)
	cd ../auth_service && go build ./... && go vet ./...
	cd ../studio_service && go build ./... && go vet ./...
	cd ../files_service && go build ./... && go vet ./...
