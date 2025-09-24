#!/bin/bash
set -eu

echo "Checking for mixed tabs and spaces in shell scripts..."

OUT="$(git grep -n --untracked -P '\t' '*.sh' || true)"
if [ -n "${OUT}" ]; then
  echo "ERROR: mixed tabs and spaces in script:"
  echo "${OUT}"
  exit 1
fi
