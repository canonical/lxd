#!/bin/sh
set -eu

echo "Checking for imports that have been added to client or shared..."

# Ensure predictable sorting
export LC_ALL=C.UTF-8

OUT=$(go list -f '{{ join .Imports "\n" }}' ./client ./shared/api ./lxc/config | grep -F . | sort -u | diff -u test/client-godeps.list - || true)
if [ -n "${OUT}" ]; then
  echo "ERROR: you added a new dependency to the client or shared; please make sure this is what you want"
  echo "${OUT}"
  exit 1
fi
