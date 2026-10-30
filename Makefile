.PHONY: run build test coverage fmt-check vet static security check integration e2e test-system up down migrate

# Tool-only dependencies stay outside the application's module graph.
STATICCHECK_VERSION := v0.7.0
GOVULNCHECK_VERSION := v1.9.0
CHECK_TOOLCHAIN := go1.26.10
CHECK_TAGS := integration,e2e

run:
	go run ./cmd/catalog
build:
	go build -o bin/catalog ./cmd/catalog
test:
	go test -race -count=1 ./...
coverage:
	sh scripts/test-coverage.sh
	go test -race -count=1 -covermode=atomic -coverpkg=./... -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out
	sh scripts/check-coverage.sh coverage.out 90
fmt-check:
	@set -eu; files="$$(gofmt -l cmd internal tests)"; if [ -n "$$files" ]; then printf 'Run gofmt on:\n%s\n' "$$files"; exit 1; fi
vet:
	go vet -tags=$(CHECK_TAGS) ./...
static: fmt-check vet
	GOTOOLCHAIN=$(CHECK_TOOLCHAIN) go run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) -tags=$(CHECK_TAGS) ./...
security:
	go mod verify
	GOTOOLCHAIN=$(CHECK_TOOLCHAIN) go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) -test -tags=$(CHECK_TAGS) ./...
check: static coverage security
integration:
	sh scripts/test-compose.sh integration
e2e:
	sh scripts/test-compose.sh e2e
test-system:
	sh scripts/test-compose.sh all
up:
	docker compose up --build -d
down:
	docker compose down
migrate:
	docker compose run --rm migrate
