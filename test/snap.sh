#!/bin/bash
set -euo pipefail

usage() {
    cat << EOF
Usage: sudo -E ./test/snap.sh test/snap/<suite> [snap-channel] [suite-arguments...]

Run an LXD snap integration test. Set LXD_SNAP_SIDELOAD=1 to sideload binaries
from LXD_SNAP_BINARY_DIR or \$(go env GOPATH)/bin.
EOF
}

if [ "${1:-}" = "--help" ] || [ "${1:-}" = "-h" ]; then
    usage
    exit 0
fi

if [ "${EUID}" -ne 0 ]; then
    echo "This script must be run as root." >&2
    exit 1
fi

test_path="${1:-}"
if [ -z "${test_path}" ]; then
    usage >&2
    exit 1
fi

case "${test_path}" in
    test/snap/*)
        ;;
    *)
        echo "Test path must be below test/snap/." >&2
        exit 1
        ;;
esac

script_dir="$(dirname "$(realpath "${BASH_SOURCE[0]}")")"
repo_root="$(dirname "${script_dir}")"
test_dir="$(realpath "${repo_root}/test/snap")"
test_script="$(realpath -e "${repo_root}/${test_path}")"
case "${test_script}" in
    "${test_dir}"/*)
        ;;
    *)
        echo "Test path must be below test/snap/." >&2
        exit 1
        ;;
esac

if ! [ -f "${test_script}" ]; then
    echo "Test script must be a regular file." >&2
    exit 1
fi

lxd_snap_channel="${2:-${LXD_SNAP_CHANNEL:-latest/edge}}"
if [ "${#}" -gt 1 ]; then
    shift 2
else
    shift
fi
export LXD_SNAP_CHANNEL="${lxd_snap_channel}"

# Wait for cloud-init to finish preparing the local VM environment.
if command -v cloud-init > /dev/null && systemd-detect-virt --quiet --vm; then
    echo "Waiting for cloud-init"
    cloud-init status --wait --long
    echo "Done"
fi

# Set ulimit to ensure core dumps are output.
ulimit -c unlimited
echo '|/bin/sh -c $@ -- eval exec gzip --fast > /var/crash/%e.%p.gz' > /proc/sys/kernel/core_pattern

if [ -n "${GOCOVERDIR:-}" ]; then
    mkdir -p "${GOCOVERDIR}"
    chmod 0777 "${GOCOVERDIR}"
fi

test_name="$(basename "${test_script}")"

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
bash -euo pipefail -c '
    . "$1"
    . "$2"

    export DEBIAN_FRONTEND=noninteractive
    FAIL=1
    trap cleanup EXIT HUP INT TERM

    test_script="$3"
    shift 3
    . "${test_script}" "$@"
' bash "${repo_root}/test/includes/snap.sh" "${repo_root}/test/includes/snap-helpers.sh" "${test_script}" "${@}"