#!/bin/bash

# This script is meant to be sourced by the test scripts under test/snap/,
# not executed directly.
if [ "${BASH_SOURCE[0]}" = "${0}" ]; then
    echo "This script is not meant to be run directly." >&2
    echo "Run a test script under test/snap/ instead, e.g.: sudo -E ./test/snap/cgroup" >&2
    exit 1
fi

# Do nothing when the test environment is already set up: the inner wrapper
# below re-sources the test script after setup.
if [ -n "${LXD_SNAP_TEST_SETUP:-}" ]; then
    return 0
fi

# The test script sourcing this file must itself be executed directly, not
# sourced: a directly executed script has BASH_SOURCE[0] == $0.
if [ "${BASH_SOURCE[1]:-}" != "${0}" ]; then
    echo "Snap test scripts must be executed directly, not sourced." >&2
    echo "E.g.: sudo -E ./test/snap/cgroup" >&2
    return 1
fi

# Set shell options only after the guards above so that an accidentally
# sourcing shell is left untouched.
set -euo pipefail

# Identify the calling test script.
test_script="$(realpath "${BASH_SOURCE[1]}")"
test_name="$(basename "${test_script}")"

script_dir="$(dirname "$(realpath "${BASH_SOURCE[0]}")")"
repo_root="$(dirname "${script_dir}")"
test_dir="$(realpath "${repo_root}/test/snap")"

# Ensure the calling script is actually under test/snap/.
case "${test_script}" in
    "${test_dir}"/*)
        ;;
    *)
        echo "Test script must be below test/snap/." >&2
        exit 1
        ;;
esac

if ! [ -f "${test_script}" ]; then
    echo "Test script must be a regular file." >&2
    exit 1
fi

if [ "${EUID}" -ne 0 ]; then
    echo "This script must be run as root." >&2
    exit 1
fi

lxd_snap_channel="${1:-${LXD_SNAP_CHANNEL:-latest/edge}}"
if [ "${#}" -gt 0 ]; then
    shift
fi
export LXD_SNAP_CHANNEL="${lxd_snap_channel}"
export LXD_SNAP_TEST_SETUP=1

# Wait for cloud-init to finish preparing the local VM environment.
if command -v cloud-init > /dev/null && systemd-detect-virt --quiet --vm; then
    echo "Waiting for cloud-init"
    cloud-init status --wait --long
    echo "Done"
fi

# Grow /tmp to 5GiB if it is a tmpfs with less than 4GiB available. Some CI
# runners mount /tmp as a small tmpfs that cannot hold the multi-GiB VM exports
# produced by some test suites. Only tmpfs can be resized online with remount,
# and growing the limit does not allocate memory upfront.
if [ "$(df --output=fstype /tmp | tail -n1)" = "tmpfs" ]; then
    tmp_avail="$(df -B1 --output=avail /tmp | tail -n1)"
    if [ "${tmp_avail}" -lt 4294967296 ]; then
        echo "==> /tmp is a tmpfs with less than 4GiB available (${tmp_avail} bytes), remounting with size=5G"
        mount -o remount,size=5G /tmp
    fi
fi

# Set ulimit to ensure core dumps are output.
ulimit -c unlimited
echo '|/bin/sh -c $@ -- eval exec gzip --fast > /var/crash/%e.%p.gz' > /proc/sys/kernel/core_pattern

# Create GOCOVERDIR if needed and make it usable by non-root users.
# shellcheck disable=SC1091
. "${script_dir}/includes/coverage.sh"
setup_gocoverdir

# shellcheck disable=SC2317,SC2329 # Invoked via trap; false positive caused by the sourced-execution guard.
cleanup() {
    local status=$?

    trap - EXIT
    set +e

    if [ -n "${GOCOVERDIR:-}" ] && systemctl is-active --quiet snap.lxd.daemon.service; then
        systemctl stop snap.lxd.daemon.service
    fi

    exit "${status}"
}
trap cleanup EXIT

echo "==> Running ${test_name} against ${lxd_snap_channel}" >&2
status=0
bash -euo pipefail -c '
    . "$1"
    . "$2"

    export DEBIAN_FRONTEND=noninteractive
    FAIL=1
    trap cleanup EXIT HUP INT TERM

    test_script="$3"
    shift 3
    . "${test_script}" "$@"
' bash "${repo_root}/test/includes/snap.sh" "${repo_root}/test/includes/snap-helpers.sh" "${test_script}" "${@}" || status=$?

# This script is sourced by the test script: exit here so the test script
# body does not run again in the calling shell. The EXIT trap above handles
# cleanup.
exit "${status}"
