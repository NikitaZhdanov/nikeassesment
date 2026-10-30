# Catalog use cases

These scenarios are executable acceptance criteria for [requirements](requirements.md). The HTTP E2E suite uses the UC identifiers in test names. Each scenario owns unique SKUs/categories so runs do not depend on shared fixtures.

## UC-01 — Create and retrieve a product

Actor: catalog maintainer. Preconditions: API and database ready.

1. POST a valid product (including price zero as an allowed boundary).
2. Receive 201, Location `/v1/products/{id}`, generated UUID, and timestamps.
3. GET Location; receive 200 with the same persisted values.

Alternatives: missing/null required fields, bad SKU/category, blank name, negative/fractional/oversized price, malformed JSON, unknown fields (including incorrect capitalization), duplicate properties (even identical keys or escaped equivalents), malformed UTF-8, NUL in any writable string (including escaped `\u0000`), trailing JSON, or an overlong field return 400. Wrong media type returns 415; a body over 64 KiB returns 413. Invalid writes do not create products. Valid multibyte/escaped Unicode and surrounding name whitespace survive creation and retrieval unchanged. Omitted/null description remains an empty string. Covers FR-01, FR-02, FR-07, NFR-01, NFR-07.

## UC-02 — Enforce unique SKUs

Actor: catalog maintainer. Preconditions: a product exists.

1. POST another product with the same SKU; receive 409.
2. Create a different product, then PUT it with the first SKU; receive 409.
3. GET both products; confirm the failed writes did not alter them.

Two concurrent creates of one SKU result in exactly one success and one conflict (verified at the persistence boundary). Covers FR-08.

## UC-03 — Replace a product

Actor: catalog maintainer. Preconditions: a product exists.

1. PUT all required editable fields with changed name, SKU, category, and price; omit description to clear it.
2. Receive 200; ID/created_at are unchanged and updated_at advances.
3. GET confirms the replacement. Listing in the new category includes it and the old category excludes it.

Alternatives: incomplete replacement returns 400; valid replacement of a missing ID returns 404; malformed ID returns 400. The original product survives invalid updates, including strict JSON/encoding failures. Concurrent PUTs each return one complete replacement; final storage contains the last committed replacement, with ID/created_at preserved and updated_at strictly advancing. A persistence test controls commit order using PostgreSQL row locks; concurrent HTTP requests verify the same contract end to end. SKU conflicts still return 409 and leave both products unchanged. Covers FR-03, FR-07, FR-08.

## UC-04 — Delete a product

Actor: catalog maintainer. Preconditions: a product exists.

1. DELETE its UUID; receive 204 with an empty body.
2. GET and repeated DELETE return 404; the product disappears from category listings.

Malformed UUID returns 400. Covers FR-04.

## UC-05 — Browse, filter, and paginate

Actor: catalog shopper. Preconditions: products in two categories, multiple prices, and at least two equal-price products.

1. GET a category with both inclusive price bounds and limit 2.
2. Check only matching products, ordered by price then UUID; a continuation cursor is present if more matches exist.
3. Follow cursors with the same filters until next_cursor is null.
4. Check each matching product appeared once, including products exactly at each bound and tied prices.

Alternatives: no filters, category only, minimum only, maximum only, and zero bounds work. No matches produce an empty array and null cursor. Default/max limits are honored. Invalid/inverted ranges, repeated/unknown query keys, limits outside 1–100, malformed cursors, and changed cursor filters return 400. Page size may change between requests. Covers FR-05, FR-06, FR-07, NFR-02.

## UC-06 — Handle invalid routes and missing resources

Actor: any API client.

1. GET, PUT, and DELETE a valid but missing UUID; receive 404.
2. Supply a malformed UUID; receive 400.
3. Call an unknown route; receive JSON 404. Use an unsupported method on a known route; receive JSON 405 with an Allow header listing supported methods.
4. Check errors have `error.code`, `error.message`, and a request ID header, without database details.

Persistence failure alternatives: unexpected SQL/schema/driver failures return generic 500 `internal_error`; database connection failures and operation timeouts return generic 503 `unavailable`. Unit tests exercise each mapping and verify that the actual cause is logged with the response request ID, without exposing internal details in HTTP. Covers FR-02 through FR-04, FR-07, NFR-07.

## UC-07 — Probe service health

Actor: service operator.

1. GET /healthz; receive 200.
2. GET /readyz with PostgreSQL available; receive 200.
3. Stop the test database: /healthz stays 200, /readyz becomes 503, and product reads return generic 503 errors within the request deadline.
4. Restart PostgreSQL and wait for readiness; a previously created product remains readable.

The Compose orchestration performs the outage/recovery phase after normal acceptance tests. Covers FR-09, NFR-01, NFR-07.

## Developer acceptance — CI quality and security checks

Actor: contributor. These are delivery checks for NFR-09, verified through the same Make commands used in GitHub Actions, rather than HTTP scenarios.

1. Push a commit or open a pull request: unit coverage, static analysis, security, and system tests appear as separate checks.
2. `make static` rejects unformatted Go code, vet diagnostics, and Staticcheck findings, including code behind integration/E2E build tags.
3. `make security` verifies the module cache and scans the application and tests with govulncheck. Reachable known vulnerabilities or scan failures return a nonzero exit status.
4. `make check` runs static checks, the existing >=90% unit coverage gate with race detection, and security checks locally.
5. Weekly/manual workflow runs detect newly published advisories without requiring source changes. Weekly Dependabot configuration covers Go modules, Actions, and Docker base images.

These checks provide results; requiring them for merges is a repository branch-protection setting.
