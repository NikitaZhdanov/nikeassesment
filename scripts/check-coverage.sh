#!/bin/sh
set -eu
# Merge duplicate blocks from -coverpkg profiles before computing the weighted
# statement percentage. Never compare rounded go tool cover display values.
awk -v minimum="${2:-90}" '
  NR == 1 { if ($0 !~ /^mode: (set|count|atomic)$/) exit 2; next }
  NF != 3 || $2 !~ /^[0-9]+$/ || $3 !~ /^[0-9]+$/ { invalid=1; next }
  { statements[$1]=$2; if ($3>0) covered[$1]=1 }
  END {
    for (block in statements) { total+=statements[block]; if (covered[block]) hit+=statements[block] }
    if (invalid || total==0) { print "Invalid or empty coverage profile"; exit 2 }
    printf "Unit coverage: %.2f%% (%d/%d statements); required: %.2f%%\n", 100*hit/total, hit, total, minimum
    if (100*hit < minimum*total) exit 1
  }
' "${1:-coverage.out}"
