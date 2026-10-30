# Product catalog API

A Go REST service for creating, reading, replacing, deleting, and browsing e-commerce products. PostgreSQL stores the catalog; category and inclusive price filters use indexed cursor pagination designed for hundreds of thousands of products and a workload dominated by reads.

The project follows spec-driven development:

1. [Requirements](docs/requirements.md) — scope, data rules, acceptance criteria.
2. [Use cases](docs/use-cases.md) — scenarios mapped to E2E tests.
3. [Design](docs/design.md) — architecture, pagination, database decisions, and tradeoffs against MongoDB, Elasticsearch, DynamoDB, and ClickHouse.
4. [Tasks](docs/tasks.md) — implementation order and verification record.

## Requirements

| Tool | Required version | When needed |
| --- | --- | --- |
| Go | 1.26+; pinned toolchain/build version 1.26.8 | Local builds and unit tests; the Go launcher can download the pinned toolchain |
| Docker Engine / Docker Desktop | Engine 20.10+ with a running daemon | Container startup and system tests |
| Docker Compose | v2.6+ (`docker compose`) | Local stack and integration/E2E orchestration |
| PostgreSQL | 17; Compose pins 17.6 | Local database if running without Compose; Compose supplies it otherwise |
| Make, POSIX shell, awk | Standard Unix tools | Convenience commands and coverage gate |
| C compiler | GCC/Clang compatible with Go | Local `-race` tests; included in the test container |

You only need Docker and Compose for the containerized application and system tests. No host PostgreSQL installation is required. Dependency downloads and image pulls need internet access on the first run. On Windows, use WSL2 for the Make/shell commands.

## Start the complete application

```sh
docker compose up --build -d
curl http://localhost:8080/readyz
```

Compose starts PostgreSQL, applies versioned migrations, then starts the API. The API listens at `http://localhost:8080`; PostgreSQL is available on `localhost:5432`. Both published ports bind to localhost. Development credentials are `catalog` / `catalog`, database `catalog`.

```sh
docker compose logs -f api
docker compose down
```

Database data survives `down`. `docker compose down --volumes` also deletes the development database; use it only when you intend to reset local data.

## Run Go locally

```sh
docker compose up -d postgres
docker compose run --rm migrate
export DATABASE_URL='postgres://catalog:catalog@localhost:5432/catalog?sslmode=disable'
make run
```

If the containerized API is already running, stop it with `docker compose stop api` before binding the same port locally. For an existing PostgreSQL 17 instance, create a dedicated database, apply `migrations/` using golang-migrate v4, and set DATABASE_URL accordingly. Schema migrations must complete before starting the service.

| Variable | Default | Meaning |
| --- | --- | --- |
| `DATABASE_URL` | Required | PostgreSQL connection URL, including credentials and TLS settings |
| `HTTP_ADDR` | `:8080` | API listen address |
| `DB_MAX_CONNS` | `10` | Database pool limit; allowed range 1–100 |

Use the connection URL's TLS options for remote databases. `sslmode=disable` and the supplied credentials are for the local Compose environment. The application reads environment variables directly; it does not load `.env` files itself. SIGINT/SIGTERM allows up to 10 seconds to drain requests. Logs are structured JSON.

Migrations live in `migrations/` with paired up/down files. `make migrate` applies outstanding migrations to the Compose database. Never edit a migration already deployed; add a new numbered migration. The initial down migration drops the products table and its data. Production migration credentials and application credentials should be separate.

## API examples

Prices are **integer euro cents** in a single EUR catalog. A price of `12999` means EUR 129.99. SKUs must be unique uppercase identifiers; categories are lowercase slugs.

```sh
curl -i -X POST http://localhost:8080/v1/products \
  -H 'Content-Type: application/json' \
  -d '{"sku":"RUN-001","name":"Running shoe","description":"Daily trainer","category":"running-shoes","price_cents":12999}'
```

This returns `201 Created`, a `Location` header and the product with generated `id`, `created_at`, and `updated_at`. Substitute that ID below:

```sh
curl http://localhost:8080/v1/products/PRODUCT_ID

curl -X PUT http://localhost:8080/v1/products/PRODUCT_ID \
  -H 'Content-Type: application/json' \
  -d '{"sku":"RUN-001","name":"Updated running shoe","category":"running-shoes","price_cents":11999}'

curl --get http://localhost:8080/v1/products \
  --data-urlencode 'category=running-shoes' \
  --data-urlencode 'min_price_cents=5000' \
  --data-urlencode 'max_price_cents=15000' \
  --data-urlencode 'limit=20'

curl -i -X DELETE http://localhost:8080/v1/products/PRODUCT_ID
```

PUT replaces every editable field; `sku`, `name`, `category`, and `price_cents` are required. Omitting `description` clears it. Writes use last committed write wins. PATCH is not supported.

List responses have this shape:

```json
{"items": [], "next_cursor": null}
```

Products are sorted by price ascending, then UUID ascending. The default page size is 20, maximum 100. To continue, send `cursor=NEXT_CURSOR` with the **same category and price filters**; `limit` may change. Use URL encoding and treat cursors as opaque. `next_cursor: null` means there is no next page. Total counts and numbered pages are intentionally omitted.

```sh
curl --get http://localhost:8080/v1/products \
  --data-urlencode 'category=running-shoes' \
  --data-urlencode 'min_price_cents=5000' \
  --data-urlencode 'max_price_cents=15000' \
  --data-urlencode 'cursor=NEXT_CURSOR'
```

| Operation | Success | Common failures |
| --- | --- | --- |
| `POST /v1/products` | 201 | 400 invalid input, 409 duplicate SKU |
| `GET /v1/products/{id}` | 200 | 400 invalid UUID, 404 missing product |
| `PUT /v1/products/{id}` | 200 | 400 invalid input, 404 missing product, 409 duplicate SKU |
| `DELETE /v1/products/{id}` | 204, empty body | 400 invalid UUID, 404 missing product |
| `GET /v1/products` | 200 | 400 invalid filters/cursor |
| `GET /healthz` | 200 | Process liveness; independent of database |
| `GET /readyz` | 200 | 503 database unavailable |

JSON writes require `Content-Type: application/json` (415 otherwise) and are limited to 64 KiB (413). Unknown fields/query keys and repeated query parameters are rejected. Repository failures return 503; unexpected panics return 500. Error bodies are consistent:

```json
{"error":{"code":"not_found","message":"product not found"}}
```

Every response includes `X-Request-ID`. Exact field lengths, price bounds, and cursor semantics are in the [requirements](docs/requirements.md).

## Tests and coverage

```sh
make check         # go vet + unit tests with race detector + >=90% coverage gate
make test          # unit tests with race detector, without coverage report
make integration   # migrated real PostgreSQL; repository/constraint tests
make e2e           # real API + PostgreSQL; UC-01 through UC-07 over HTTP
make test-system   # integration and E2E, including database outage/recovery
```

**Coverage is unit-test-only and statement-weighted across all production Go packages**, including `cmd/catalog`; no production paths are excluded. The gate fails below 90% using unrounded coverage counts. Inspect the generated report with:

```sh
go tool cover -html=coverage.out
```

Unit tests use testify, httptest, and pgxmock. Integration tests execute real SQL, verify up/down migrations and indexes, and exercise constraints, concurrent SKU uniqueness, CRUD, filter bounds and pagination ties. E2E tests make black-box HTTP calls against the built API image; they also verify database outage responses and persistence after recovery.

System tests use the standalone `compose.test.yaml`, a unique project name, no published ports, and a separate test volume. The runner propagates failures, prints container logs on failure, and removes test containers/volumes on exit. It does not touch the development stack. Docker must be running; tests fail if prerequisites are absent.

To run tagged tests against an existing **disposable, migrated** environment:

```sh
TEST_DATABASE_URL='postgres://user:password@localhost:5432/catalog_test?sslmode=disable' \
  go test -tags=integration -race -count=1 ./internal/postgres

E2E_BASE_URL=http://localhost:8080 \
  go test -tags=e2e -count=1 ./tests/e2e
```

The second command runs the normal acceptance phase; the full outage/recovery sequence requires `make e2e`. Tagged suites require explicit URLs instead of silently skipping when configuration is missing. Use only disposable environments: tests create and delete fixtures and exercise schema rollback inside transactions.

GitHub Actions runs `make check` and `make test-system`. [The task record](docs/tasks.md#verification-record) distinguishes executed checks from pending ones.

## Query-plan experiment

To create a disposable database, run the experiment, and clean up automatically:

```sh
sh scripts/benchmark-compose.sh
```

Or, on an existing disposable Compose database with migrations applied:

```sh
docker compose exec -T postgres psql -U catalog -d catalog \
  -v ON_ERROR_STOP=1 < scripts/query-plan.sql
```

The script inserts 300,000 representative products inside a transaction, analyzes them, prints actual plans for global/category/range lists and deep cursor vs offset browsing, then rolls back the fixtures. Inspect index use, rows scanned, buffer reads, and execution times on your hardware. This is a query-plan experiment, not a concurrent load test or an SLA claim.

## Layout and limitations

```text
cmd/catalog/       process entry point
internal/app/      configuration and server lifecycle
internal/catalog/  product validation, filter parsing, cursor contract
internal/httpapi/  chi routes, JSON decoding, errors, request logging
internal/postgres/ pgx repository and persistence tests
migrations/        versioned PostgreSQL schema
tests/e2e/         black-box acceptance scenarios
scripts/           coverage gate, Compose test runner, query-plan experiment
docs/              requirements → use cases → design → tasks
```

The service has no authentication, inventory, orders, search ranking, cache, or multi-currency support. Pagination is a live view: concurrent changes to price/category can move products between pages. There is no snapshot-export guarantee or optimistic concurrency protection. Production deployment needs an authenticated gateway, TLS, backup/recovery and monitoring appropriate to its availability requirements. The design explains when replicas, caching, or a search index would become useful.
