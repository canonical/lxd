#!/bin/bash
set -eu

# golangci/golangci-lint-action runs on PR
if [ "${GITHUB_EVENT_NAME:-}" = "pull_request" ]; then
    echo "Skipping golangci-lint script during PR tests (already done by golangci-lint action)"
    exit 0
fi

# Default target branch.
target_branch="${1:-main}"
target_revision="$(git log --max-count=1 --format=%H "origin/${target_branch}" || true)"

echo "Checking for golangci-lint errors between HEAD and ${target_branch} (${target_revision})..."
golangci-lint run --timeout 5m --new --new-from-rev "${target_revision}" --whole-files
