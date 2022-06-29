#!/bin/sh -eu

echo "Checking for short form imports..."

OUT=$(grep -r -n --include \*.go -P '^\s*import\s+"' . 2>/dev/null | grep -v '"C"')
if [ -n "${OUT}" ]; then
  echo "ERROR: found short form imports: ${OUT}"
  exit 1
fi
