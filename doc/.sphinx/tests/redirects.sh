#!/bin/bash
# Check that old documentation URLs still lead to a page that loads.
# Each URL is requested following redirects, and the final page must resolve
# with 200. The destination itself is not checked.

set -u

URLS=(
    "https://documentation.ubuntu.com/lxd/"
    "https://documentation.ubuntu.com/lxd/latest/"
)

failures=0

for url in "${URLS[@]}"; do
    code="$(curl --silent --show-error -L -o /dev/null --retry 3 --retry-all-errors --max-time 30 -w '%{http_code}' "${url}")"
    if [ "${code}" = "200" ]; then
        echo "ok:   ${url} status ${code}"
    else
        echo "FAIL: ${url}: expected final status 200, got ${code}"
        failures=$((failures + 1))
    fi
done

echo
echo "${#URLS[@]} checks, ${failures} failed"
[ "${failures}" -eq 0 ]
