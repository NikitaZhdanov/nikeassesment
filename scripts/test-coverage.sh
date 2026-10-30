#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
scratch=$(mktemp -d)
trap 'rm -f "$scratch/profile"; rmdir "$scratch"' EXIT

check() {
  expected=$1
  actual=0
  sh scripts/check-coverage.sh "$scratch/profile" 90 >/dev/null 2>&1 || actual=$?
  if [ "$actual" -ne "$expected" ]; then
    echo "Coverage gate: expected exit $expected, got $actual" >&2
    exit 1
  fi
}

# Exact threshold passes, using statement weights instead of averaging blocks.
cat > "$scratch/profile" <<'PROFILE'
mode: atomic
example/a.go:1.1,2.1 9 1
example/a.go:3.1,4.1 1 0
PROFILE
check 0

# A rounded display of 90.00% must still fail if the actual value is below 90%.
cat > "$scratch/profile" <<'PROFILE'
mode: atomic
example/a.go:1.1,2.1 89999 1
example/a.go:3.1,4.1 10001 0
PROFILE
check 1

# The same block can appear in several -coverpkg profiles; count it only once
# and consider it covered if any test exercised it.
cat > "$scratch/profile" <<'PROFILE'
mode: atomic
example/a.go:1.1,2.1 9 0
example/a.go:1.1,2.1 9 1
example/a.go:3.1,4.1 1 0
PROFILE
check 0

printf 'mode: atomic\n' > "$scratch/profile"
check 2
printf 'mode: atomic\ninvalid input\n' > "$scratch/profile"
check 2
echo 'Coverage gate self-checks passed'
