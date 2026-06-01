#!/usr/bin/env bash
set -euo pipefail

PROFILE="${1:?usage: check-coverage.sh <cover.out> <threshold>}"
THRESHOLD="${2:?usage: check-coverage.sh <cover.out> <threshold>}"

TOTAL=$(go tool cover -func="$PROFILE" | tail -1 | awk '{print $NF}' | tr -d '%')

echo "Total coverage: ${TOTAL}%  (threshold: ${THRESHOLD}%)"

if awk "BEGIN {exit !(${TOTAL} < ${THRESHOLD})}"; then
  echo "FAIL: coverage ${TOTAL}% is below threshold ${THRESHOLD}%"
  exit 1
fi

echo "OK: coverage meets threshold"
