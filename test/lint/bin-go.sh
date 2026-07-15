#!/bin/bash
set -eu
set -o pipefail
shopt -s inherit_errexit

if [ -z "${GITHUB_ACTIONS:-}" ]; then
    echo "Skipping binary Go version check on local runs"
    exit 0
fi

GIT_ROOT="$(git rev-parse --show-toplevel)"
GOMIN="$(sed -n 's/^GOMIN=\([0-9.]\+\)$/\1/p' "${GIT_ROOT}/Makefile")"
EXPECTED_GO_VERSION="${LXD_CI_GO_SNAP_VERSION:-go${GOMIN}}"
BIN="${GOPATH:-"$(go env GOPATH)"}/bin"

echo "Check binaries were compiled with ${EXPECTED_GO_VERSION}"
UNEXPECTED_GO_VER="$(go version -v "${BIN}"/lxc* "${BIN}"/lxd* | grep -vF ": ${EXPECTED_GO_VERSION}" || true)"
if [ -n "${UNEXPECTED_GO_VER:-}" ]; then
  echo "Some binaries were compiled with an unexpected Go version (!= ${EXPECTED_GO_VERSION}):"
  echo "${UNEXPECTED_GO_VER}"
  exit 1
fi
