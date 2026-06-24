#!/bin/bash
set -euo pipefail

# Ensure a go-install-based tool from tools/go.mod is present at its pinned
# version, (re)installing via `go install <pkg>@<pinned-version>` if missing
# or out of date. This avoids `go install <pkg>@latest`'s network lookup when
# the tool is already at the expected version.
#
# Usage: check-tool-version.sh <mod-path> <install-path> <binary-name>
#   <mod-path>:     module path as listed in tools/go.mod (used with `go list -m`)
#   <install-path>: package path passed to `go install`
#   <binary-name>:  resulting binary name in $GOPATH/bin

MOD_PATH="${1}"
INSTALL_PATH="${2}"
BIN_NAME="${3}"

GIT_ROOT="$(git rev-parse --show-toplevel)"
bin="${GOPATH:-"$(go env GOPATH)"}/bin"
bin_path="${bin}/${BIN_NAME}"
expected_version="$(go list -C "${GIT_ROOT}/tools" -m -f '{{.Version}}' "${MOD_PATH}")"

installed_version=""
if [ -x "${bin_path}" ]; then
    installed_version="$(go version -m "${bin_path}" 2>/dev/null | awk '$1 == "mod" { print $3 }' || true)"
fi

if [ "${installed_version}" != "${expected_version}" ]; then
    echo "${BIN_NAME} version mismatch: installed '${installed_version:-none}', expected '${expected_version}'. Installing..."
    GOBIN="${bin}" go install "${INSTALL_PATH}@${expected_version}"
else
    echo "${BIN_NAME} is up to date (${installed_version})"
fi
