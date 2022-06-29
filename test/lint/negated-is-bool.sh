#!/bin/sh -eu

echo "Checking usage of negated shared.Is(True|False)*() functions..."

OUT=$(grep -Pr --exclude-dir=.git '!(shared\.)?Is(True|False).*\(' . 2>/dev/null || true)
if [ -n "${OUT}" ]; then
  echo "ERROR: negated shared.Is(True|False)*() function in script: ${OUT}"
  exit 1
fi
