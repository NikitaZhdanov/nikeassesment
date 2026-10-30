#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
mode=${1:-all}
case "$mode" in all|integration|e2e) ;; *) echo "Usage: $0 [all|integration|e2e]" >&2; exit 2;; esac
# A unique project keeps tests isolated from local development and other runs.
project="catalog-test-$(date +%s)-$$"
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

if [ "$mode" != e2e ]; then
  compose build integration
  compose run --rm integration
fi
if [ "$mode" != integration ]; then
  compose build api e2e
  compose up -d api
  compose run --rm --no-deps e2e
  compose stop postgres
  compose run --rm --no-deps -e E2E_PHASE=outage e2e
  compose start postgres
  compose run --rm --no-deps -e E2E_PHASE=recovery e2e
fi
