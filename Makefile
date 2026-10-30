.PHONY: run build test coverage vet check integration e2e test-system up down migrate

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
vet:
	go vet ./...
check: vet coverage
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
