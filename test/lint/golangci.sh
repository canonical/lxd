#!/bin/bash
set -eu

# Default target branch.
target_branch="main"
target_revision=""
if [ -n "${GITHUB_BASE_REF:-}" ]; then
  # Target branch when scanning a Github pull request
  target_branch="${GITHUB_BASE_REF}"
elif [ -n "${GITHUB_BEFORE:-}" ]; then
  # Previous revision when scanning a Github push event (e.g. after pull request merge).
  # This environment variable is set in the workflow yaml to the value of `github.event.before`:
  # https://docs.github.com/en/rest/using-the-rest-api/github-event-types?apiVersion=2022-11-28#pushevent
  target_revision="${GITHUB_BEFORE}"
elif [ -n "${1:-}" ]; then
  # Allow a target branch parameter.
  target_branch="${1}"
fi

if [ -z "${target_revision}" ]; then
  # If we don't already have a target revision. Try to get one from the branch.
  if [ -n "${GITHUB_ACTIONS:-}" ]; then
    # If we're in CI, just attempt and continue, we may be in a shallow clone.
    target_revision="$(git log --max-count=1 --format=%H "origin/${target_branch}" || true)"
  else
    # If we're local, fail if we don't find a revision (and don't prefix with "origin").
    target_revision="$(git log --max-count=1 --format=%H "${target_branch}")"
  fi
fi

# If we still don't have a target revision, we need to fetch the branch as actions/checkout performs a shallow clone by default.
# Otherwise if this target revision was set via GITHUB_BEFORE, we may also not have the revision locally. So double check.
if [ -z "${target_revision}" ]; then
  echo "Convert shallow clone into treeless clone"
  git fetch --filter=tree:0 origin "${target_branch}"
  target_revision="$(git log --max-count=1 --format=%H "origin/${target_branch}")"
elif ! git log --max-count=1 --format=%H "${target_revision}" >/dev/null 2>&1; then
  echo "Convert shallow clone into treeless clone"
  git fetch --filter=tree:0 origin "${target_revision}"
fi

echo "Checking for golangci-lint errors between HEAD and ${target_branch} (${target_revision})..."
golangci-lint run --timeout 5m --new --new-from-rev "${target_revision}" --whole-files
