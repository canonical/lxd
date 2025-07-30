#!/bin/bash
set -eu

echo "Checking usage of unneeded format variant of 'logger.(Debug|Info|Warn|Error)f()' functions..."

# XXX: ignore format specifier args (%x or slice expansion like `a...)`)
OUT="$(git grep --untracked -E '(l|log|logger)\.(Debug|Info|Warn|Error)f\(' '*.go' | grep -v '%.' | grep -vF '...)' || true)"
if [ -n "${OUT}" ]; then
  echo "ERROR: unneeded format variant of logger.(Debug|Info|Warn|Error)f() function in script: ${OUT}"
  exit 1
fi
