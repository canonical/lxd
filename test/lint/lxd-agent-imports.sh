#!/bin/sh
set -eu

echo "Checking for imports that have been added to the lxd-agent..."

# Ensure predictable sorting
export LC_ALL=C.UTF-8

OUT=$(go list -f '{{ join .Deps "\n" }}' ./lxd-agent | grep -F . | sort -u | diff -u test/lxd-agent-godeps.list - || true)
if [ -n "${OUT}" ]; then
  echo "ERROR: you added a new dependency to the lxd-agent; please make sure this is what you want"
  echo "${OUT}"
  exit 1
fi
