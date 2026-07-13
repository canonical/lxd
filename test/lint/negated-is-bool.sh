#!/bin/bash
set -eu
set -o pipefail
shopt -s inherit_errexit

echo "Checking usage of negated shared.Is(True|False)*() functions..."

OUT=$(git grep -n --untracked -P '!(shared\.)?Is(True|False).*\(' '*.go' || true)
if [ -n "${OUT}" ]; then
  echo "ERROR: negated shared.Is(True|False)*() function in script:"
  echo "${OUT}"
  exit 1
fi
