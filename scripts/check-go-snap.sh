#!/bin/bash
set -euo pipefail

# Verify that the Go snap is available for all required architectures.
#
# Arguments:
# $1: expected Go version (e.g. "1.26.2")

EXPECTED_VERSION="${1}"
REQUIRED_ARCHES="amd64 arm64 armhf ppc64el riscv64 s390x"

# Derive the channel track from the major.minor of the expected version
CHANNEL="$(echo "${EXPECTED_VERSION}" | grep -oE '^[0-9]+\.[0-9]+')/stable"

SNAP_OUT="$(./scripts/check-snap.py go --channel "${CHANNEL}" --format plain)"

SNAP_LINE="$(echo "${SNAP_OUT}" | grep "^${EXPECTED_VERSION}:" || true)"
if [ -z "${SNAP_LINE}" ]; then
    echo "Error: Go snap ${EXPECTED_VERSION} not found in ${CHANNEL} channel" >&2
    echo "${SNAP_OUT}" >&2
    exit 1
fi

MISSING_ARCHES=""
for arch in ${REQUIRED_ARCHES}; do
    if ! echo "${SNAP_LINE}" | grep -qw "${arch}"; then
        MISSING_ARCHES="${MISSING_ARCHES} ${arch}"
    fi
done

if [ -n "${MISSING_ARCHES}" ]; then
    echo "Error: Go snap ${EXPECTED_VERSION} is missing architectures:${MISSING_ARCHES} in ${CHANNEL} channel" >&2
    echo "${SNAP_OUT}" >&2
    exit 1
fi
