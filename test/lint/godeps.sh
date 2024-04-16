#!/bin/bash
set -eu
set -o pipefail

# Ensure predictable sorting
export LC_ALL=C.UTF-8

for pkg in client lxc/config lxd-agent shared/api; do
  echo "==> Checking for imports/deps that have been added to ${pkg}..."
  DEP_FILE="test/godeps/$(echo "${pkg}" | sed 's/\//-/g').list"
  OUT="$(go list -f '{{ join .Deps "\n" }}' ./${pkg} | grep -F . | sort -u | diff -u "${DEP_FILE}" -)"
  if [ -n "${OUT}" ]; then
    echo "ERROR: you added a new dependency to ${pkg}; please make sure this is what you want"
    echo "${OUT}"
    exit 1
  fi
done
