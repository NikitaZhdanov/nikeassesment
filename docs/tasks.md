# Implementation tasks

Execute in order. A task is complete only when its artifacts and specified checks exist; record checks actually run separately from planned checks.

| Order | Status | Task | Traceability | Completion evidence |
| --- | --- | --- | --- | --- |
| 1 | Done | Write requirements and explicit assumptions | All FR/NFR | requirements.md |
| 2 | Done | Define acceptance use cases | UC-01–UC-07 | use-cases.md |
| 3 | Done | Design storage, API, pagination, failure behavior and tests | All FR/NFR | design.md, database comparison with sources |
| 4 | Done | Scaffold Go module, product types, validators and cursors | FR-05–08, NFR-03 | Table-driven unit tests |
| 5 | Done | Write migrations, indexes and pgx repository | FR-01–06, FR-08, NFR-01–02 | pgxmock unit tests, real PostgreSQL integration tests |
| 6 | Done | Implement HTTP routes, strict decoding and error mapping | FR-01–09, NFR-07 | httptest coverage of success and failure branches |
| 7 | Done | Implement configuration, startup and graceful shutdown | FR-09, NFR-07 | Lifecycle/configuration tests |
| 8 | Done | Add coverage enforcement, race detection, vet and CI | NFR-04 | >=90% unit statement coverage across all production packages |
| 9 | Done | Add isolated Compose integration/E2E environments | NFR-05–06, UC-01–07 | Successful real-database and black-box runs, outage/recovery |
| 10 | Done | Write README and query-plan experiment | NFR-02, NFR-08 | Reproducible commands, supported versions, observed results |
| 11 | Done | Review specs against behavior and report verification | All | Final test results and limitations below |

## Verification record

Executed locally on 2026-10-30:

| Check | Result |
| --- | --- |
| `make check` | Passed: go vet, unit tests with race detector, and coverage gate self-checks |
| Unit coverage | **95.42% — 271 / 284 production statements**; includes command wiring and startup code; no production package exclusions |
| Coverage gate negative tests | Passed: below-threshold, empty and malformed profiles fail; exact-threshold and duplicate-block merging work |
| `sh scripts/test-compose.sh all` | Passed: real PostgreSQL integration tests and UC-01–UC-07 E2E, including outage and recovery |
| Final HTTP route change | Unit tests and final Compose E2E rerun passed, including outage/recovery and cleanup |
| `sh scripts/benchmark-compose.sh` | Passed with 300,000 products; results below |
| Compose configurations and shell syntax | Both Compose files validate; all shell scripts parse |

The initial full system run used PostgreSQL 17.6 and Go 1.26.8 container images. Integration tests exercised migration up/down, four expected indexes, CRUD, filter combinations, pagination ties, invalid-value constraints, update conflict, and simultaneous duplicate inserts. The Compose workflow completed with exit status 0 and removed its test containers, network, and volume.

Unit tests cover all statements in catalog validation/pagination, HTTP handlers, and repository code. The uncovered statements are in the command entry point and successful startup wiring; those paths run in Compose E2E. Integration/E2E coverage is not included in the unit percentage. GitHub Actions is configured but has not been executed remotely in this work session.

### Query-plan observations

One local Docker Desktop run on 300,000 synthetic products (100 categories, varied prices, roughly 268-byte estimated rows). Timings are PostgreSQL EXPLAIN execution times from one run, not HTTP latency, a concurrent benchmark, or a performance guarantee.

| Query | Index | Rows read by index scan | Execution time |
| --- | --- | --- | --- |
| Global first page | products_price_id_idx | 21 | 0.097 ms |
| Category + inclusive price range | products_category_price_id_idx | 21 | 0.083 ms |
| Deep cursor page | products_price_id_idx | 21 | 0.073 ms |
| OFFSET 270000 comparison | products_price_id_idx | 270,021 | 193.069 ms |

All fixtures were rolled back and the benchmark database was removed. The meaningful observation is bounded index work for cursor queries versus scanning skipped rows for OFFSET; cache state, distribution, row size, hardware and concurrent load affect timings.
