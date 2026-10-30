#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
project="catalog-bench-$(date +%s)-$$"
compose() { docker compose -p "$project" -f compose.test.yaml "$@"; }
cleanup() {
  result=$?
  trap - EXIT
  if [ "$result" -ne 0 ]; then compose logs --no-color || true; fi
  compose down --volumes --remove-orphans || true
  exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
compose run --rm migrate
compose exec -T postgres psql -U catalog -d catalog_test -v ON_ERROR_STOP=1 < scripts/query-plan.sql
