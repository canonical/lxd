#!/bin/sh -eu

# Default target branch.
target_branch="main"
if [ -n "${GITHUB_BASE_REF:-}" ]; then
  # Target branch when scanning a Github pull request
  target_branch="${GITHUB_BASE_REF}"
elif [ -n "${GITHUB_BEFORE:-}" ]; then
  # Target branch when scanning a Github merge
  target_branch="${GITHUB_BEFORE}"
elif [ -n "${1:-}" ]; then
  # Allow a target branch parameter.
  target_branch="${1}"
fi

# Gets the most recent commit hash from the target branch.
rev="$(git log --max-count=1 --format=%H "origin/${target_branch}" || true)"
if [ -z "${rev}" ]; then
    # actions/checkout creates shallow clones by default
    echo "Convert shallow clone into treeless clone"
    git fetch --filter=tree:0 origin "${target_branch}"
    rev="$(git log --max-count=1 --format=%H "origin/${target_branch}")"
fi

if [ -z "${rev}" ]; then
  echo "No revision found for the tip of the target branch, aborting."
  false
fi

echo "Checking for golangci-lint errors between HEAD and ${target_branch} (${rev})..."
golangci-lint run --timeout 5m --new --new-from-rev "${rev}"
