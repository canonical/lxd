#!/bin/sh -eu

echo "Checking that there are no trailing spaces in shell scripts..."

OUT=$(grep -r --exclude-dir=.git " $" . 2>/dev/null | grep '\.sh:' || true)
if [ -n "${OUT}" ]; then
  echo "ERROR: trailing space in script: ${OUT}"
  exit 1
fi
