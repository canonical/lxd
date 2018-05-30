test_storage_local_volume_handling() {
  ensure_import_testimage

  # shellcheck disable=2039
  local LXD_STORAGE_DIR lxd_backend
  # shellcheck disable=SC2034
  lxd_backend=$(storage_backend "$LXD_DIR")
  LXD_STORAGE_DIR=$(mktemp -d -p "${TEST_DIR}" XXXXXXXXX)
  chmod +x "${LXD_STORAGE_DIR}"
  spawn_lxd "${LXD_STORAGE_DIR}" false

  ensure_import_testimage

  (
    set -e
    # shellcheck disable=2030
    LXD_DIR="${LXD_STORAGE_DIR}"

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

    # This looks complex but is basically just the following test matrix:
    #
    #        | btrfs |  ceph |  dir  |  lvm  |  zfs  |
    # -------|-------|-------|-------|-------|-------|
    #  btrfs |   x   |   x   |   x   |   x   |   x   |
    # -------|-------|-------|-------|-------|-------|
    #  ceph  |   x   |   x   |   x   |   x   |   x   |
    # -------|-------|-------|-------|-------|-------|
    #  dir   |   x   |   x   |   x   |   x   |   x   |
    # -------|-------|-------|-------|-------|-------|
    #  lvm   |   x   |   x   |   x   |   x   |   x   |
    # -------|-------|-------|-------|-------|-------|
    #  zfs   |   x   |   x   |   x   |   x   |   x   |
    # -------|-------|-------|-------|-------|-------|

    for driver in "btrfs" "ceph" "dir" "lvm" "zfs"; do
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

        lxc storage volume create "lxdtest-$(basename "${LXD_DIR}")-${driver}" vol1
        lxc storage volume copy "lxdtest-$(basename "${LXD_DIR}")-${driver}/vol1" "lxdtest-$(basename "${LXD_DIR}")-${driver}1/vol1"
        lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-${driver}1" vol1
        lxc storage volume move "lxdtest-$(basename "${LXD_DIR}")-${driver}/vol1" "lxdtest-$(basename "${LXD_DIR}")-${driver}1/vol1"
        ! lxc storage volume show "lxdtest-$(basename "${LXD_DIR}")-${driver}" vol1
        lxc storage volume show "lxdtest-$(basename "${LXD_DIR}")-${driver}1" vol1
        lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-${driver}1" vol1
        lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-${driver}1"
      fi
    done

    for source_driver in "btrfs" "ceph" "dir" "lvm" "zfs"; do
      for target_driver in "btrfs" "ceph" "dir" "lvm" "zfs"; do
        if [ "$source_driver" != "$target_driver" ] && [ "$lxd_backend" = "$source_driver" ] && storage_backend_available "$target_driver"; then
          # source_driver -> target_driver
          lxc storage volume create "lxdtest-$(basename "${LXD_DIR}")-${source_driver}" vol1
          lxc storage volume copy "lxdtest-$(basename "${LXD_DIR}")-${source_driver}/vol1" "lxdtest-$(basename "${LXD_DIR}")-${target_driver}/vol1"
          lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-${target_driver}" vol1

          lxc storage volume move "lxdtest-$(basename "${LXD_DIR}")-${source_driver}/vol1" "lxdtest-$(basename "${LXD_DIR}")-${target_driver}/vol1"
          ! lxc storage volume show "lxdtest-$(basename "${LXD_DIR}")-${source_driver}" vol1
          lxc storage volume show "lxdtest-$(basename "${LXD_DIR}")-${target_driver}" vol1
          lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-${target_driver}" vol1

          # target_driver -> source_driver
          lxc storage volume create "lxdtest-$(basename "${LXD_DIR}")-${target_driver}" vol1
          lxc storage volume copy "lxdtest-$(basename "${LXD_DIR}")-${target_driver}/vol1" "lxdtest-$(basename "${LXD_DIR}")-${source_driver}/vol1"
          lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-${source_driver}" vol1

          lxc storage volume move "lxdtest-$(basename "${LXD_DIR}")-${target_driver}/vol1" "lxdtest-$(basename "${LXD_DIR}")-${source_driver}/vol1"
          ! lxc storage volume show "lxdtest-$(basename "${LXD_DIR}")-${target_driver}" vol1
          lxc storage volume show "lxdtest-$(basename "${LXD_DIR}")-${source_driver}" vol1
          lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-${source_driver}" vol1
        fi
      done
    done
  )

  # shellcheck disable=SC2031
  LXD_DIR="${LXD_DIR}"
  kill_lxd "${LXD_STORAGE_DIR}"
}
