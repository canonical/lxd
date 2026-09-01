#!/bin/bash
set -eux
set -o pipefail

# wait for cloud-init to be done setting up the local VM env
if command -v cloud-init > /dev/null && systemd-detect-virt --quiet --vm; then
    echo "Waiting for cloud-init"
    cloud-init status --wait --long
    echo "Done"
fi

# Set ulimit to ensure core dump is outputted.
ulimit -c unlimited
echo '|/bin/sh -c $@ -- eval exec gzip --fast > /var/crash/%e.%p.gz' > /proc/sys/kernel/core_pattern

script="${1}"
lxd_snap_channel="${2}"
shift 2
[ -f "${script}" ]
test_name="$(basename "${script}")"
_script="$(mktemp -t "${test_name}.XXXX")"

# Create GOCOVERDIR if needed and make it world-writable
[ -n "${GOCOVERDIR:-}" ] && mkdir -p "${GOCOVERDIR}" && chmod 0777 "${GOCOVERDIR}"

echo "==> Running the job ${test_name} against ${lxd_snap_channel}" >&2
sed "1 r bin/helpers" "${script}" | sed "s|@@LXD_SNAP_CHANNEL@@$|LXD_SNAP_CHANNEL=${lxd_snap_channel}|" > "${_script}"
[ -s "${_script}" ]
exec bash -euo pipefail "${_script}" "${@}"
