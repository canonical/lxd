#!/bin/bash
set -euo pipefail

# Arguments:
# $1: files to check/commit (space separated)
# $2: commit message

FILES="${1}"
MESSAGE="${2}"

# Exit early if there are no changes
# shellcheck disable=SC2086
git diff --quiet -- ${FILES} && exit 0

if [ -t 0 ]; then
  read -r -p "Would you like to commit changes to ${FILES} (Y/n)? " answer
  if [ "${answer:-y}" = "y" ] || [ "${answer:-y}" = "Y" ]; then
    # Commit changes and exit
    # shellcheck disable=SC2086
    exec git commit -Ssm "${MESSAGE}" -- ${FILES}
  fi
fi

# shellcheck disable=SC2086
if ! git diff --exit-code -- ${FILES}; then
  echo "==> Please update the generated files in your commit" >&2
  exit 1
fi
