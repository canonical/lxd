#!/bin/bash
set -eu

# golangci-lint is run via GitHub actions so avoid checking twice
if [ -n "${GITHUB_ACTIONS:-}" ]; then
    echo "Skipping golangci-lint script (already done by golangci-lint action)"
    exit 0
fi

echo "Checking for golangci-lint errors..."
exec golangci-lint run --timeout 5m
