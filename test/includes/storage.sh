# Helper functions related to storage backends.

# is_backend_available checks if a given backend is available by matching it against the list of available storage backends.
# Surrounding spaces in the pattern (" $(available_storage_backends) ") are used to ensure exact matches,
# avoiding partial matches (e.g., "dir" matching "directory").
is_backend_available() {
    case " $(available_storage_backends) " in
        *" $1 "*) return 0;;
        *) return 1;;
    esac
}

# Whether a storage backend is available
storage_backend_available() {
    local backends
    backends="$(available_storage_backends)"
    if [ "${backends#*"$1"}" != "$backends" ]; then
        return 0
    elif [ "${1}" = "cephfs" ] && [ "${backends#*"ceph"}" != "$backends" ] && [ -n "${LXD_CEPH_CEPHFS:-}" ]; then
        return 0
    fi

    return 1
}

# Returns 0 if --optimized-storage works for backups (export/import)
storage_backend_optimized_backup() {
    [ "${1}" = "btrfs" ] && return 0
    [ "${1}" = "zfs" ]   && return 0
    return 1
}

# Choose a random available backend, excluding LXD_BACKEND
random_storage_backend() {
    # shellcheck disable=SC2046
    shuf --head-count=1 --echo $(available_storage_backends 2>/dev/null)
}

# Return the storage backend being used by a LXD instance
storage_backend() {
    echo "$(< "${1}/lxd.backend")"
}

# Return a list of available storage backends
available_storage_backends() {
    local backend backends storage_backends

    backends="dir" # always available

    if [ -n "${PURE_GATEWAY:-}" ] && [ -n "${PURE_API_TOKEN}" ]; then
        backends="$backends pure"
    fi

    storage_backends="btrfs zfs"

    if uname -r | grep -- '-kvm$' >/dev/null; then
        echo "The -kvm kernel flavor is missing CONFIG_DM_THIN_PROVISIONING needed for lvm thin pools, lvm backend won't be available" >&2
    else
        storage_backends="${storage_backends} lvm"
    fi

    if [ -z "${LXD_CEPH_CLUSTER:-}" ]; then
        echo "The ceph backend won't be available because LXD_CEPH_CLUSTER is not set" >&2
    else
        storage_backends="${storage_backends} ceph"
    fi

    for backend in $storage_backends; do
        if command -v "$backend" >/dev/null 2>&1; then
            backends="$backends $backend"
        fi
    done

    echo "$backends"
}

import_storage_backends() {
    local backend
    for backend in $(available_storage_backends); do
        # shellcheck disable=SC1090
        . "backends/${backend}.sh"
    done
}

configure_loop_device() {
    local -n _img_out="${1}"
    local -n _dev_out="${2}"
    # Minimal size for different FSes:
    # * ext4: 224K
    # * ZFS: 64M
    # * btrfs: 109M
    # * XFS: 300M
    local size="${3:-"64M"}"
    local lv_loop_file pvloopdev

    # shellcheck disable=SC2153
    lv_loop_file="$(mktemp -p "${TEST_DIR}" lxdtest-XXX.img)"
    truncate -s "${size}" "${lv_loop_file}"
    if ! pvloopdev="$(losetup --show -f "${lv_loop_file}")"; then
        echo "failed to setup loop" >&2
        return 1
    fi

    # Record the loop device
    echo "${pvloopdev}" >> "${TEST_DIR}/loops"

    # Assign values back to the passed variable names using namerefs
    _img_out="${lv_loop_file}"
    _dev_out="${pvloopdev}"
}

deconfigure_loop_device() {
    local lv_loop_file loopdev success
    lv_loop_file="${1}"
    loopdev="${2}"
    success=0
    for _ in $(seq 20); do
        if ! losetup "${loopdev}"; then
            success=1
            break
        fi

        if losetup -d "${loopdev}"; then
            success=1
            break
        fi

        sleep 0.1
    done

    if [ "${success}" = "0" ]; then
        echo "Failed to tear down loop device"
        return 1
    fi

    rm -f "${lv_loop_file}"
    sed -i "\|^${loopdev}\$| d" "${TEST_DIR}/loops"
}

umount_loops() {
    local line test_dir
    test_dir="$1"

    if [ -f "${test_dir}/loops" ]; then
        while read -r line; do
            losetup -d "${line}" || true
        done < "${test_dir}/loops"
    fi
}

create_object_storage_pool() {
  poolName="${1}"
  lxd_backend=$(storage_backend "$LXD_DIR")

  # Pool cannot already exist.
  if lxc storage show "${poolName}"; then
    echo "Storage pool pool ${poolName} already exists"
    exit 1
  fi

  if [ "${lxd_backend}" != "ceph" ]; then
    echo "Object storage pools require the ceph (cephobject) backend"
    exit 1
  fi

  lxc storage create "${poolName}" cephobject cephobject.radosgw.endpoint="${LXD_CEPH_CEPHOBJECT_RADOSGW}"
}

delete_object_storage_pool() {
  poolName="${1}"

  lxc storage delete "${poolName}"
}
