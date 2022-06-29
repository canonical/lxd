#!/bin/sh -eu

echo "Checking for mixed tabs and spaces in shell scripts..."

OUT=$(git grep -P '\t' . 2>/dev/null | grep '\.sh:' || true)
if [ -n "${OUT}" ]; then
  echo "ERROR: mixed tabs and spaces in script: ${OUT}"
  exit 1
fi
