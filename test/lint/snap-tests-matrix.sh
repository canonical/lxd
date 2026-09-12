#!/bin/bash
set -eu
set -o pipefail
shopt -s inherit_errexit

# This is a meta-test: it makes sure every script under test/snap/ is referenced by the
# snap-tests job matrix in both tests.yml and snap.yml, so a script added/removed under
# test/snap/ can't silently drift out of sync with what CI actually runs.

if ! command -v yq > /dev/null; then
    echo "This check requires 'yq'." >&2
    exit 1
fi

# Ensure predictable sorting
export LC_ALL=C.UTF-8

# Tests that require specialized hardware (GPU passthrough, IOMMU, SR-IOV NICs) unavailable on
# GitHub-hosted runners; these run via dedicated workflows (e.g. gpu-passthrough-tests.yml)
# instead, so they're intentionally absent from tests.yml/snap.yml and excluded from this check.
HARDWARE_ONLY_TESTS="gpu-container gpu-mig network network-sriov"

ALL_SCRIPTS="$(mktemp)"
EXPECTED_TESTS="$(mktemp)"
MATRIX_TESTS="$(mktemp)"
trap 'rm -f "${ALL_SCRIPTS}" "${EXPECTED_TESTS}" "${MATRIX_TESTS}"' EXIT

find test/snap -maxdepth 1 -type f ! -name COPYING ! -name '.*' -printf '%f\n' | sort -u > "${ALL_SCRIPTS}"
comm -23 "${ALL_SCRIPTS}" <(echo "${HARDWARE_ONLY_TESTS}" | tr ' ' '\n' | sort -u) > "${EXPECTED_TESTS}"

RC=0
for workflow in .github/workflows/tests.yml .github/workflows/snap.yml; do
    # Extract the base script name (e.g. "network-ovn ovn:deb" -> "network-ovn") of every
    # entry in that workflow's `snap-tests` job `strategy.matrix.test` list.
    yq -r '.jobs["snap-tests"].strategy.matrix.test[]' "${workflow}" | awk '{print $1}' | sort -u > "${MATRIX_TESTS}"

    MISSING="$(comm -23 "${EXPECTED_TESTS}" "${MATRIX_TESTS}")"
    if [ -n "${MISSING}" ]; then
        echo "FAIL: ${workflow} is missing test/snap/ scripts from its snap-tests matrix:" >&2
        echo "${MISSING}" >&2
        RC=1
    fi

    # Catches typos/removed scripts still referenced by the matrix.
    PHANTOM="$(comm -13 "${ALL_SCRIPTS}" "${MATRIX_TESTS}")"
    if [ -n "${PHANTOM}" ]; then
        echo "FAIL: ${workflow}'s snap-tests matrix references non-existent test/snap/ scripts:" >&2
        echo "${PHANTOM}" >&2
        RC=1
    fi
done

exit "${RC}"
