test_container_local_cross_pool_handling() {
  ensure_import_testimage

  # shellcheck disable=2039
  local LXD_STORAGE_DIR lxd_backend
  # shellcheck disable=SC2034
  lxd_backend=$(storage_backend "$LXD_DIR")
  LXD_STORAGE_DIR=$(mktemp -d -p "${TEST_DIR}" XXXXXXXXX)
  chmod +x "${LXD_STORAGE_DIR}"
  spawn_lxd "${LXD_STORAGE_DIR}" true

  (
    set -e
    # shellcheck disable=2030
    LXD_DIR="${LXD_STORAGE_DIR}"
    ensure_import_testimage

    if storage_backend_available "btrfs"; then
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-btrfs" btrfs size=100GB
    fi

    if storage_backend_available "ceph"; then
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-ceph" ceph volume.size=25MB ceph.osd.pg_num=1
    fi

    lxc storage create "lxdtest-$(basename "${LXD_DIR}")-dir" dir

    if storage_backend_available "lvm"; then
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-lvm" lvm volume.size=25MB
    fi

    if storage_backend_available "zfs"; then
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-zfs" zfs size=100GB
    fi

    for driver in "btrfs" "ceph" "dir" "lvm", "zfs"; do
      if [ "$lxd_backend" = "$driver" ]; then
        pool_opts=

        if [ "$driver" = "btrfs" ] || [ "$driver" = "zfs" ]; then
          pool_opts="size=100GB"
        fi

        if [ "$driver" = "ceph" ]; then
          pool_opts="volume.size=25MB ceph.osd.pg_num=1"
        fi

        if [ "$driver" = "lvm" ]; then
          pool_opts="volume.size=25MB"
        fi

        if [ -n "${pool_opts}" ]; then
          # shellcheck disable=SC2086
          lxc storage create "lxdtest-$(basename "${LXD_DIR}")-${driver}1" "${driver}" $pool_opts
        else
          lxc storage create "lxdtest-$(basename "${LXD_DIR}")-${driver}1" "${driver}"
        fi

        lxc init testimage c1
        lxc copy c1 c2 -s "lxdtest-$(basename "${LXD_DIR}")-${driver}1"
        lxc storage volume show "lxdtest-$(basename "${LXD_DIR}")-${driver}1" container/c2
        lxc delete -f c2
        lxc move c1 c2 -s "lxdtest-$(basename "${LXD_DIR}")-${driver}1"
        ! lxc list c1
        lxc storage volume show "lxdtest-$(basename "${LXD_DIR}")-${driver}1" container/c2
        lxc delete -f c2

        lxc init testimage c1
        lxc snapshot c1
        lxc snapshot c1
        lxc copy c1 c2 -s "lxdtest-$(basename "${LXD_DIR}")-${driver}1" --container-only
        lxc storage volume show "lxdtest-$(basename "${LXD_DIR}")-${driver}1" container/c2
        ! lxc storage volume show "lxdtest-$(basename "${LXD_DIR}")-${driver}1" container/c2/snap0
        ! lxc storage volume show "lxdtest-$(basename "${LXD_DIR}")-${driver}1" container/c2/snap1
        lxc delete -f c2
        lxc move c1 c2 -s "lxdtest-$(basename "${LXD_DIR}")-${driver}1" --container-only
        ! lxc list c1
        lxc storage volume show "lxdtest-$(basename "${LXD_DIR}")-${driver}1" container/c2
        ! lxc storage volume show "lxdtest-$(basename "${LXD_DIR}")-${driver}1" container/c2/snap0
        ! lxc storage volume show "lxdtest-$(basename "${LXD_DIR}")-${driver}1" container/c2/snap1
        lxc delete -f c2

        lxc init testimage c1
        lxc snapshot c1
        lxc snapshot c1
        lxc copy c1 c2 -s "lxdtest-$(basename "${LXD_DIR}")-${driver}1"
        lxc storage volume show "lxdtest-$(basename "${LXD_DIR}")-${driver}1" container/c2
        lxc storage volume show "lxdtest-$(basename "${LXD_DIR}")-${driver}1" container/c2/snap0
        lxc storage volume show "lxdtest-$(basename "${LXD_DIR}")-${driver}1" container/c2/snap1
        lxc delete -f c2
        lxc move c1 c2 -s "lxdtest-$(basename "${LXD_DIR}")-${driver}1"
        ! lxc list c1
        lxc storage volume show "lxdtest-$(basename "${LXD_DIR}")-${driver}1" container/c2
        lxc storage volume show "lxdtest-$(basename "${LXD_DIR}")-${driver}1" container/c2/snap0
        lxc storage volume show "lxdtest-$(basename "${LXD_DIR}")-${driver}1" container/c2/snap1
        lxc delete -f c2
      fi
    done
  )

  # shellcheck disable=SC2031
  LXD_DIR="${LXD_DIR}"
  kill_lxd "${LXD_STORAGE_DIR}"
}

