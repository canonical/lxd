#!/bin/bash
set -euo pipefail

# golangci-lint is run via GitHub actions so avoid checking twice
if [ -n "${GITHUB_ACTIONS:-}" ]; then
    echo "Skipping golangci-lint script (already done by golangci-lint action)"
    exit 0
fi

bin="${GOPATH:-"$(go env GOPATH)"}/bin"
expected_version="$(go list -C tools -m -f '{{.Version}}' github.com/golangci/golangci-lint/v2)"

if command -v golangci-lint >/dev/null; then
    installed_version="v$(golangci-lint version --short)"
    if [ "${installed_version}" != "${expected_version}" ]; then
        echo "golangci-lint version mismatch: installed ${installed_version}, expected ${expected_version}. Reinstalling..."
        curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b "${bin}" "${expected_version}"
    fi
else
    curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b "${bin}" "${expected_version}"
fi

echo "Checking for golangci-lint errors..."
exec golangci-lint run --timeout 5m
