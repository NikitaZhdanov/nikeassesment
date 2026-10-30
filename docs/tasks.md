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
| 12 | Done | Add separate CI static/security jobs, pinned Go-native scanners, weekly checks and dependency updates | NFR-09 | make static, make security, make check; workflow and Dependabot validation |
| 13 | Done | Remediate review findings: exact/duplicate JSON keys, UTF-8/NUL validation, persistence error classification, concurrent replacement tests, and specification traceability | FR-03, FR-07–08, NFR-01, NFR-04–09, UC-01–UC-07 | Extended HTTP/domain/pgxmock unit tests, real PostgreSQL commit-order/error tests, and black-box strict-input/concurrent PUT scenarios |

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

### CI and security follow-up

Added independent GitHub checks for coverage, static analysis, security, and system tests; weekly/manual triggers; and grouped Dependabot updates for Go, Actions, Docker, and Compose. Staticcheck v0.7.0 and govulncheck v1.9.0 remain development-only commands. The README documents the gates and how to require them through repository branch protection.

The initial security scan failed as intended on known advisories in chi, pgx, x/net and x/text. Scanning with the exact application toolchain also detected affected Go standard-library symbols. Updated the build, local toolchain, CI, and scanner to Go 1.26.10, and upgraded affected dependencies (including compatible x/crypto/x/net/x/text transitive updates). No scanner findings were suppressed.

| Follow-up check | Result |
| --- | --- |
| `make check` with Go 1.26.10 | Passed: formatting, tagged vet/Staticcheck, race tests, 95.42% unit coverage, module integrity, and tagged/test-inclusive govulncheck |
| Security gate failure behavior | Verified: initial vulnerability findings caused a nonzero Make exit status |
| Workflow / Dependabot YAML | Parsed successfully; jobs use read-only permissions and bounded timeouts |
| Final `sh scripts/test-compose.sh all` | Passed with Go 1.26.10 and updated dependencies: PostgreSQL integration, all E2E scenarios, outage/recovery, and test-resource cleanup |

The final govulncheck run reported **0 reachable vulnerabilities**; it also reported informational findings in unused package/module code. This matches the documented reachability-based gate and does not claim that every transitive dependency is advisory-free.

The updated workflow and Dependabot configuration have not been executed remotely in this work session. No branch-protection settings were changed.

### Review remediation verification (2026-10-30)

The review fixes preserve package boundaries, explicit SQL, migrations/indexes, keyset cursor semantics, request IDs, graceful shutdown, and health probes. No dependencies or runtime infrastructure were added. Description omission/null behavior and surrounding name whitespace are unchanged.

| Review finding | Implementation and verification |
| --- | --- |
| Case-insensitive/duplicate JSON keys | `httpapi.decodeProduct` uses standard-library object tokens and exact decoded names; POST/PUT tables reject case variants in either order, identical/conflicting duplicates, escaped-equivalent duplicates, malformed objects and multiple values. |
| UTF-8/NUL data corruption or persistence errors | Bounded raw payloads are checked before decoding; domain validation rejects NUL in all writable strings. HTTP and E2E tests cover malformed byte sequences, escaped NUL, valid multibyte/escaped Unicode and preserved whitespace. Boundary tests cover exactly 64 KiB, one byte over, trailing whitespace and Unicode field lengths. |
| All persistence failures incorrectly returned 503 | Repository paths preserve the original cause and classify known constraints, connectivity and timeouts. SQL/schema/protocol/authentication/scan bugs remain internal (500), including scan codecs wrapping EOF/timeouts. pgxmock tables cover all operations and list iteration; HTTP mocks verify each status and correlated internal logging; real PostgreSQL tests exercise CHECK metadata and missing-schema failures. |
| Missing concurrent PUT evidence | A PostgreSQL test observes the blocking transaction, controls commit order, checks visibility/complete replacements, immutable fields, monotonic timestamps and failed SKU-conflict atomicity. Concurrent HTTP PUTs verify complete responses and final storage. Existing uniqueness/pagination/UC-01–UC-07 scenarios remain intact. |

| Final check | Result |
| --- | --- |
| `make check` (Go 1.26.10) | Passed: gofmt, tagged go vet/Staticcheck, unit race tests, coverage gate/self-tests, module integrity and tagged/test-inclusive govulncheck |
| Unit coverage | **96.01% — 313 / 326 production statements**; unit-only, all production packages included; changed HTTP/domain/repository functions have 100% statement coverage |
| `make test-system` | Passed: real PostgreSQL integration with race detection, Compose E2E UC-01–UC-07 plus strict-input/concurrent PUT scenarios, database outage/recovery and cleanup; exit status 0 |
| `git diff --check` and final review | Passed; changes limited to review remediation, tests and specification clarification |
| GitHub Actions consistency | Existing static/coverage/security jobs match `make check`; system job calls `make test-system`; no workflow edits needed |

The default host Go symlink is broken; verification used the already-cached Go 1.26.10 toolchain on PATH. Go cache/network and Docker socket access required sandbox approval; execution succeeded with that access. govulncheck reported **0 reachable vulnerabilities**, with informational findings in unused dependency code. GitHub Actions was inspected but not run remotely. No commit or push was created.
