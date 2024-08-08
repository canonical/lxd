# Helper functions related to storage backends.

# Whether a storage backend is available
storage_backend_available() {
    local backends
    backends="$(available_storage_backends)"
    if [ "${backends#*"$1"}" != "$backends" ]; then
        true
        return
    elif [ "${1}" = "cephfs" ] && [ "${backends#*"ceph"}" != "$backends" ] && [ -n "${LXD_CEPH_CEPHFS:-}" ]; then
        true
        return
    fi

    false
}

# Returns 0 if --optimized-storage works for backups (export/import)
storage_backend_optimized_backup() {
    [ "${1}" = "btrfs" ] && return 0
    [ "${1}" = "zfs" ]   && return 0
    return 1
}

# Choose a random available backend, excluding LXD_BACKEND
random_storage_backend() {
    # shellcheck disable=2046
    shuf -e $(available_storage_backends) | head -n 1
}

# Return the storage backend being used by a LXD instance
storage_backend() {
    read -r backend < "$1/lxd.backend" && echo "${backend}"
}

# Return a list of available storage backends
available_storage_backends() {
    local backend backends storage_backends

    backends="dir" # always available

    storage_backends="btrfs lvm zfs"
    if [ -n "${LXD_CEPH_CLUSTER:-}" ]; then
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
    local lv_loop_file pvloopdev

    # shellcheck disable=SC2153
    lv_loop_file=$(mktemp -p "${TEST_DIR}" XXXX.img)
    truncate -s 10G "${lv_loop_file}"
    pvloopdev=$(losetup --show -f "${lv_loop_file}")
    if [ ! -e "${pvloopdev}" ]; then
        echo "failed to setup loop"
        false
    fi
    # shellcheck disable=SC2153
    echo "${pvloopdev}" >> "${TEST_DIR}/loops"

    # The following code enables to return a value from a shell function by
    # calling the function as: fun VAR1

    local __tmp1="${1}"
    local res1="${lv_loop_file}"
    if [ "${__tmp1}" ]; then
        eval "${__tmp1}='${res1}'"
    fi

    local __tmp2="${2}"
    local res2="${pvloopdev}"
    if [ "${__tmp2}" ]; then
        eval "${__tmp2}='${res2}'"
    fi
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
    sed -i "\\|^${loopdev}|d" "${TEST_DIR}/loops"
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

  # Check cephobject.radosgw.endpoint is required for cephobject pools.
  if [ "${lxd_backend}" = "ceph" ]; then
    lxc storage create "${poolName}" cephobject cephobject.radosgw.endpoint="${LXD_CEPH_CEPHOBJECT_RADOSGW}"
  else

    # Create a loop device for dir pools as MinIO doesn't support running on tmpfs (which the test suite can do).
    # This is because tmpfs does not support O_direct which MinIO requires. This landed in kernel 6.6 (https://kernelnewbies.org/Linux_6.6#TMPFS).
    if [ "${lxd_backend}" = "dir" ]; then
      mkdir -p "${TEST_DIR}/s3/${poolName}"
      configure_loop_device loop_file_1 loop_device_1
      # shellcheck disable=SC2154
      mkfs.ext4 "${loop_device_1}"
      mount "${loop_device_1}" "${TEST_DIR}/s3/${poolName}"
      mkdir "${TEST_DIR}/s3/${poolName}/objects"
      lxc storage create "${poolName}" dir source="${TEST_DIR}/s3/${poolName}/objects"
      # shellcheck disable=SC2154
      echo "${loop_device_1}" > "${TEST_DIR}/s3/${poolName}/dev"
      # shellcheck disable=SC2154
      echo "${loop_file_1}" > "${TEST_DIR}/s3/${poolName}/file"
    else
      lxc storage create "${poolName}" "${lxd_backend}"
    fi

    buckets_addr="127.0.0.1:$(local_tcp_port)"
    lxc config set core.storage_buckets_address "${buckets_addr}"
  fi
}

delete_object_storage_pool() {
  poolName="${1}"
  lxd_backend=$(storage_backend "$LXD_DIR")

  lxc storage delete "${poolName}"
  if [ "$lxd_backend" = "dir" ]; then
    loop_file="$(cat "${TEST_DIR}/s3/${poolName}/file")"
    loop_device="$(cat "${TEST_DIR}/s3/${poolName}/dev")"
    umount "${TEST_DIR}/s3/${poolName}"
    rmdir "${TEST_DIR}/s3/${poolName}"

    # shellcheck disable=SC2154
    deconfigure_loop_device "${loop_file}" "${loop_device}"
  fi
}