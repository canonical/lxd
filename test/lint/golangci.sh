#!/bin/bash
set -eu

# golangci/golangci-lint-action runs on PR
if [ "${GITHUB_EVENT_NAME:-}" = "pull_request" ]; then
    echo "Skipping golangci-lint script during PR tests (already done by golangci-lint action)"
    exit 0
fi

# Default target branch.
target_branch="${1:-stable-5.21}"
if [ -n "${GITHUB_BASE_REF:-}" ]; then
  # Target branch when scanning a Github pull request
  target_branch="${GITHUB_BASE_REF}"
elif [ -n "${GITHUB_BEFORE:-}" ]; then
  # Previous revision when scanning a Github push event (e.g. after pull request merge).
  # This environment variable is set in the workflow yaml to the value of `github.event.before`:
  # https://docs.github.com/en/rest/using-the-rest-api/github-event-types?apiVersion=2022-11-28#pushevent
  target_revision="${GITHUB_BEFORE}"
fi

if [ -n "${target_revision:-}" ]; then
  target_revision="$(git log --max-count=1 --format=%H "origin/${target_branch}" || true)"
fi

echo "Checking for golangci-lint errors between HEAD and ${target_branch} (${target_revision})..."
golangci-lint run --timeout 5m --new --new-from-rev "${target_revision}" --whole-files
