#!/bin/sh -eu

echo "Checking for mixed tabs and spaces in shell scripts..."

OUT=$(git grep -lP '\t' '*.sh' || true)
if [ -n "${OUT}" ]; then
  echo "ERROR: mixed tabs and spaces in script(s): ${OUT}"
  exit 1
fi
