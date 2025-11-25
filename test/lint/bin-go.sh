#!/bin/bash
set -eu
set -o pipefail

if [ -z "${GITHUB_ACTIONS:-}" ]; then
    echo "Skipping binary Go version check on local runs"
    exit 0
fi

echo "Check binaries were compiled with the Go minimum version"
GIT_ROOT="$(git rev-parse --show-toplevel)"
GOMIN="$(sed -n 's/^GOMIN=\([0-9.]\+\)$/\1/p' "${GIT_ROOT}/Makefile")"
UNEXPECTED_GO_VER="$(go version -v ~/go/bin/lxc* ~/go/bin/lxd* | grep -vF ": go${GOMIN}" || true)"
if [ -n "${UNEXPECTED_GO_VER:-}" ]; then
  echo "Some binaries were compiled with an unexpected Go version (!= ${GOMIN}):"
  echo "${UNEXPECTED_GO_VER}"
  exit 1
fi
