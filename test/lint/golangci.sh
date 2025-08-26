#!/bin/bash
set -eu

# golangci/golangci-lint-action runs on PR
if [ "${GITHUB_EVENT_NAME:-}" = "pull_request" ]; then
    echo "Skipping golangci-lint script during PR tests (already done by golangci-lint action)"
    exit 0
fi

echo "Checking for golangci-lint errors..."
exec golangci-lint run --timeout 5m
